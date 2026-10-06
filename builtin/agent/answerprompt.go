package agent

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"golang.org/x/term"

	"github.com/this-is-tobi/rta/builtin/kv"
	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/internal/shutdown"
	"github.com/this-is-tobi/rta/internal/stdio"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// requestNamed is the request an answer is about, which for a person with one
// call parked is no id at all: the question has one possible subject, and
// copying an eight-digit hex string off a notification that has already gone
// is the step that gets skipped, or typed from memory against the wrong call.
// With several parked the answer is a list to choose from, never a guess —
// allowing the wrong one of two is not a mistake the next command takes back.
//
// Only the local queue. A remote server's answer is signed over the digest of
// what the operator's own machine read (remoteAnswer), and reading the queue
// to pick the one request would cost the operator key's passphrase twice.
//
// **An allow that nothing shows the call for needs its id.** The one call that
// is parked when the command is typed is not necessarily the one parked when a
// script's `--yes` runs, and with the card skipped nothing ever puts the
// call that was meant beside the call that was answered. So a bare allow is
// for the person at a terminal, who is shown the call and asked, and for a dry
// run; `--yes` and anything that is not a terminal name the request, as
// they always had to. Showing and denying are never a release, so they guess.
//
// target is the capability being answered, for the call the list names.
func requestNamed(req plugin.Request, target string) (string, *view.Error) {
	if id := req.String("id"); id != "" {
		return id, nil
	}
	sf := req.Surface()
	if server := strings.TrimSpace(req.String("server")); server != "" {
		return "", view.Errorf("agent.request.unnamed", "name the request parked on %s", server).
			WithHint(sf.CapabilityName("agent.pending") + " with the server lists them")
	}
	q, err := consent.Scan()
	if err != nil {
		return "", view.Errorf("agent.pending.unreadable", "%v", err)
	}
	switch len(q.Waiting) {
	case 1:
		only := q.Waiting[0]
		if target == "agent.allow" && !req.DryRun && (req.Yes || !atTerminal(req)) {
			return "", view.Errorf("agent.request.unnamed",
				"%s is waiting, and nothing here would show it to you before it is allowed, so name it",
				callNamed(only.Cap, only.Scopes)).
				WithHint("`" + sf.Call(target, plugin.Arg{Name: "id", Value: only.ID, Positional: true}) +
					"` allows it; at a terminal `" + sf.Call(target) + "` alone shows the call and asks")
		}
		return only.ID, nil
	case 0:
		if len(q.Tampered) > 0 {
			return "", unknownRequest(sf, q.Tampered[0])
		}
		return "", view.Errorf("agent.request.none", "no call is waiting for an answer").
			WithHint("a parked call appears in " + sf.CapabilityName("agent.pending") +
				" and expires on its own, with the agent told")
	}
	waiting := append([]consent.Request(nil), q.Waiting...)
	sort.SliceStable(waiting, func(i, j int) bool { return waiting[i].AskedAt.Before(waiting[j].AskedAt) })
	lines := make([]string, 0, len(waiting))
	for _, r := range waiting {
		line := "  " + r.ID + "  " + callNamed(r.Cap, r.Scopes)
		if r.Agent != "" {
			line += "  (" + r.Agent + ", " + format.Ago(r.AskedAt) + ")"
		}
		lines = append(lines, line)
	}
	return "", view.Errorf("agent.request.several", "%d calls are waiting, so say which:\n%s",
		len(waiting), strings.Join(lines, "\n")).
		WithHint("`" + sf.Call(target, plugin.Arg{Name: "id", Value: "<id>", Positional: true}) + "`")
}

// atTerminal reports whether a person can be asked something: the command line,
// with a terminal on standard input. stdio.Real and not os.Stdin, which main
// has pointed at /dev/null by then (stdio.Claim) — the same question kv asks
// before it prompts for a passphrase.
var atTerminal = func(req plugin.Request) bool {
	return req.Surface() == plugin.SurfaceCLI && term.IsTerminal(int(stdio.Real().Fd()))
}

// putQuestion writes what a person at the terminal is shown and reads their
// line back. On standard error, never standard output, for the reason the
// passphrase prompt is: a redirected answer must not carry the question into a
// file. A var so a test can answer without a terminal.
var putQuestion = func(card, prompt string) (string, error) {
	defer shutdown.Prompting()()
	return askLine(stdio.Real(), os.Stderr, card, prompt)
}

// askLine writes the card and the prompt to out and reads one line from in.
func askLine(in io.Reader, out io.Writer, card, prompt string) (string, error) {
	fmt.Fprint(out, card, prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil {
		// A closed input ends no line of its own, and what is written next
		// would run on from the question.
		fmt.Fprintln(out)
	}
	return strings.TrimSpace(line), err
}

// confirmAllow shows a person at a terminal the call they are about to
// release, and asks.
//
// **The prompt is for the person, not a gate.** Without a terminal, and with
// --yes, the answer is given as it always was, because the one-shot answer
// releases a single call an agent with a shell could have made itself (the
// reasoning at the guard in runAllow) and a prompt only a pty can answer is no
// control against anything that can open one. What it prevents is the other
// kind of mistake: `agent allow 6904368d` copied off a notification approved
// a note removal that was never on screen. The card is the capability, the
// record, the arguments and what the capability's own dry run said it would
// do — the page `agent show` draws, in the few lines a question has room for.
//
// Anything but y or yes is no, a closed input included: the call stays parked,
// and the person is told how to refuse it now or leave it to expire.
func confirmAllow(r consent.Request, question string, reveals bool) bool {
	// Written to the terminal directly, past the renderer that cleans everything
	// else a person reads here, so the card is cleaned on the way out: a record or
	// a preview carrying a cursor movement must not be able to redraw the card
	// above the question it is the subject of.
	answer, _ := putQuestion(textclean.Terminal(allowCard(r, reveals)), question+" [y/N] ")
	switch strings.ToLower(answer) {
	case "y", "yes":
		return true
	}
	return false
}

// allowQuestion is the question, which says what saying yes goes beyond the one
// call: a grant that keeps answering it, or the whole role the call's line
// belongs to. The person who typed --ttl or --role asked for that, and is
// still shown it, because the card above it is the thing they are agreeing to.
func allowQuestion(role, ttl, agent string) string {
	switch {
	case role != "":
		return "Allow this call, and issue the role " + role + " to " + agentOr(agent) + "?"
	case ttl != "":
		return "Allow this call, and keep allowing it for " + ttl + "?"
	}
	return "Allow once?"
}

// agentOr is the agent a question names, or the unnamed server's own words.
func agentOr(agent string) string {
	if agent == "" {
		return "the agent"
	}
	return agent
}

// allowCard is what the question is about, in lines.
func allowCard(r consent.Request, reveals bool) string {
	var b strings.Builder
	head := callNamed(r.Cap, r.Scopes)
	if r.Agent != "" {
		head += " — " + r.Agent + " asked " + format.Ago(r.AskedAt)
	}
	fmt.Fprintln(&b, head)
	row := func(label, value string) { fmt.Fprintf(&b, "  %-10s  %s\n", label, value) }
	if r.Profile != "" {
		row("connection", r.Profile)
	}
	if args := argsOf(r); args != "" {
		row("arguments", args)
	}
	if reveals {
		row("reveals", revealsLine)
	}
	body := r.Preview
	if body == "" {
		body = notPreviewedBrief(r)
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	const most = 12
	for i, line := range lines {
		label := ""
		if i == 0 {
			label = "would do"
		}
		if i == most {
			row(label, "… "+format.Count(len(lines)-most, "more line", "more lines"))
			break
		}
		row(label, line)
	}
	if w := kvWarning(r); w != "" {
		row("warning", w)
	}
	return b.String()
}

// argsOf is a parked call's arguments on one line, as the detail page shows
// each of them, clipped: the page has the whole of them.
func argsOf(r consent.Request) string {
	if len(r.Args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(r.Args))
	for k := range r.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+argValue(r.Args[k]))
	}
	return clip(strings.Join(parts, " "))
}

// declined is the answer to a question the person said no to, or did not
// answer: nothing was released, and what could be is named.
func declined(sf plugin.Surface, r consent.Request) view.View {
	id := plugin.Arg{Name: "id", Value: r.ID, Positional: true}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "not allowed", Value: callNamed(r.Cap, r.Scopes) + " is still waiting"},
		{Key: "to refuse it now", Value: "`" + sf.Call("agent.deny", id) + "`; left alone it expires and the agent is told"},
	}}
}

// revealsLine is what a person allowing a call that reveals is told: the
// answer is the stored value, and it goes where the agent's answers go.
const revealsLine = "the stored value itself, not a masked copy — it becomes part of the agent's context"

// revealsValue reports whether the capability a parked call names reveals a
// stored value, from this machine's own catalogue and never from the request,
// which is a file the agent's server wrote: a request that said it revealed
// nothing would have left the page quiet about the one thing it is for.
func revealsValue(catalog func() []plugin.Capability, id string) bool {
	if catalog == nil {
		return false
	}
	for _, c := range catalog() {
		if c.ID == id {
			return c.Reveals
		}
	}
	return false
}

// kvWarning is what a person allowing a kv call is told before the agent finds
// out: this shell has nothing that opens the store unattended, and a server
// inherits the environment it was started from — the assumption `rta doctor`
// makes on the agent's behalf (kv.Unlockable). Allowing releases the call and
// the call then fails, and the agent reads kv.passphrase.missing a minute after
// the operator said yes, with nothing on the operator's screen to connect the
// two.
//
// Worded as a likelihood: the server may have been started with a passphrase
// this shell does not hold, and a warning that claims the call will fail when
// it will not is one people stop reading. Empty when the store is not there to
// be unlocked, or cannot be read, which say their own thing elsewhere.
func kvWarning(r consent.Request) string {
	if plugin.Namespace(r.Cap) != "kv" {
		return ""
	}
	ok, why := kv.Unlockable()
	if ok || why == "no store" || why == "unreadable" {
		return ""
	}
	return "no passphrase or key here opens the kv store unattended — unless the server has one, " +
		"the agent gets kv.passphrase.missing once you allow"
}

// notPreviewedBrief is notPreviewed for a card, which has a line for it and
// not the paragraph the detail page gives: the same three reasons, in the
// words a question has room for.
func notPreviewedBrief(r consent.Request) string {
	switch {
	case r.Safety != string(plugin.Destructive):
		return "no preview — a " + r.Safety + " call, so the capability and arguments above are all of it"
	case r.Profile != "":
		return "no preview — the connection is resolved only after you answer"
	default:
		return "no preview — the plugin's own dry run is not something rta can check, or it did not finish"
	}
}
