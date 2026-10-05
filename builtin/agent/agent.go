// Package agent is the operator's view of what AI agents are doing with
// rta: what they asked for and got (the ledger), what they are asking for
// right now (parked requests), and the answer.
//
// **None of it is reachable by an agent.** Every capability here is
// HumanOnly — never registered as an MCP tool, reads included — and that is
// stronger than NeedsGrant on purpose. An agent that could approve its own parked request would make
// the mechanism theatre — the precedent grant.allow/grant.revoke already
// set — and one that could read the ledger could enumerate the operator's
// other agents, their profiles and their records, which is the inventory
// disclosure InputSchema already refuses to hand out. The right answer to
// both is not "with permission" but "not here".
//
// It is its own namespace rather than more of `grant` because the objects
// differ. A grant is a standing policy a person writes; a pending request
// is a question an agent asked; the ledger is history. `rta grant list`
// answers what may happen, `rta agent log` answers what did.
package agent

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	rtagrant "github.com/this-is-tobi/rta/builtin/grant"
	"github.com/this-is-tobi/rta/builtin/internal/compact"
	"github.com/this-is-tobi/rta/builtin/internal/timefmt"
	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/guard"
	"github.com/this-is-tobi/rta/internal/lockdown"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/internal/stdio"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// maxRows bounds a listing. The ledger is append-only and unbounded; a
// table is a thing somebody reads.
const maxRows = 500

// Plugin returns the agent plugin declaration.
// Plugin takes the catalogue and the artifact lookup for one verb: answering
// a parked call with a whole role goes through grant's own issue flow.
func Plugin(catalog func() []plugin.Capability, artifact func(string) (string, bool)) plugin.Plugin {
	return plugin.Plugin{
		Name:    "agent",
		Summary: "What AI agents asked rta for, what they got, and what is waiting on you",
		Capabilities: []plugin.Capability{
			{
				ID:      "agent.overview",
				Summary: "Agent activity at a glance: recent calls, refusals, anything waiting",
				Description: "The last hour of calls that arrived over MCP, the refusals told apart " +
					"by who can act on them — what needs your grant, what the agent got wrong — " +
					"and how many requests are parked waiting for you to answer right now. With " +
					"`agent`: one agent's part of it. With " +
					"`detail`: the chain's integrity, where the record lives and how big it is.",
				Safety:     plugin.Read,
				Idempotent: true,
				Detailed:   true,
				Inputs:     []plugin.Field{agentField("only this agent's calls, servers and waiting requests")},
				HumanOnly:  true,
				// The tile says how many calls are waiting; these are the places to go
				// from there. `g` because l is navigation and every other letter in "log"
				// is spoken for. `w` is bare for the tile's own promise — "press w to
				// answer" has to land on the queue, not on a form asking which remote
				// server this machine's own waiting calls are on. `L` opens the lock form
				// from the same screen you notice you need it on: another plugin's
				// capability, which is why it is never bare — what another plugin runs is
				// always on a form the operator reads first.
				Actions: []plugin.Action{
					{Key: "w", Label: "waiting", Target: "agent.pending", Bare: true},
					{Key: "g", Label: "log", Target: "agent.log"},
					{Key: "L", Label: "lock", Target: "lock.add"},
				},
				Run: runOverview,
			},
			{
				ID:      "agent.log",
				Summary: "The record of what agents did — one line per call, refusals included",
				Description: "Every call that arrived over MCP: the capability, the records it named, " +
					"exactly as the call spelled them, the arguments (secrets masked), the profile, " +
					"what happened, and how it was authorized — no " +
					"grant needed, a standing grant, or you answering live. At a terminal each call " +
					"is one line — when, what, the record, what became of it; `detail`, a pipe or a " +
					"machine format gives every field of every call. The file is chained, so " +
					"an edited or missing line is visible: `detail` verifies it and says where it " +
					"breaks. This is history and not policy; `grant.list` is what may happen next.",
				Safety:     plugin.Read,
				Idempotent: true,
				Detailed:   true,
				NoPreview:  true, // agent.overview is the tile; this is the page
				Inputs: []plugin.Field{
					{Name: "limit", Type: plugin.Int, Default: 30, Min: 1, Max: maxRows,
						Help: "how many of the most recent calls to show"},
					{Name: "refused", Type: plugin.Bool, Help: "only the calls rta would not make"},
					agentField("only this agent's calls"),
					{Name: "role", Type: plugin.String,
						Help: "only calls a grant of this role covered — what the dev role did today"},
					{Name: "session", Type: plugin.String,
						Help: "only one server's calls — the id `agent.overview` shows beside each connected client",
						Suggest: func(context.Context, plugin.Request) []string {
							open, _ := session.List()
							out := make([]string, 0, len(open))
							for _, s := range open {
								out = append(out, s.ID+"\t"+s.Agent+" "+s.Client)
							}
							return out
						}},
					{Name: "since", Type: plugin.String, Suggest: suggestSince,
						Help: "only calls after this: `today`, `yesterday`, a span like `2h` or `3d`, or a date like 2026-08-30"},
					{Name: "after", Type: plugin.Int, Min: 0,
						Help: "only calls after this `seq` — an exact cursor, for shipping the record somewhere"},
				},
				HumanOnly: true,
				Run:       runLog,
			},
			{
				ID:      "agent.metrics",
				Summary: "The record as Prometheus metrics, for a dashboard and an alert",
				Description: "One command, the standard text exposition format, no listener and no " +
					"port: its output written to /var/lib/node_exporter/textfile_collector/rta.prom " +
					"on a timer is the whole integration. Calls by capability, agent, outcome and " +
					"how they were authorized; grants in force; calls parked waiting for you; and " +
					"whether the record's hash chain still verifies — which is the one worth an " +
					"alert, because a record that stops verifying is either a bug or somebody " +
					"editing it. Nothing is kept: every number is derived from the record, so it " +
					"is a number you could recompute. The Grafana stack's other half needs nothing " +
					"here — `agent.log` with `after`, as JSON, is already a cursor over an " +
					"append-only record, which is what a log shipper wants.",
				Safety:     plugin.Read,
				Idempotent: true,
				NoPreview:  true, // a full pass over the record is not a tile
				HumanOnly:  true,
				Run:        runMetrics(artifact),
			},
			{
				ID:      "agent.pending",
				Summary: "Calls parked right now, waiting for you to allow or deny",
				Description: "With `rta mcp serve --consent`, a call that needs a grant nobody " +
					"issued is parked instead of refused, and waits for you. Each row is one such " +
					"call: its id, what it wants, against which connection, and how long it will " +
					"keep waiting. Answer with `agent.allow` or `agent.deny`. " +
					"With `server` (a name from remotes.yaml): the same queue read from a " +
					"remote rta server as a signed operator call, your operator key's passphrase " +
					"asked first.",
				Safety:     plugin.Read,
				Idempotent: true,
				Inputs: []plugin.Field{
					{Name: "server", Type: plugin.String, Local: true, Remote: true,
						Help: "read a remote server's parked queue instead of this machine's (a name from remotes.yaml)"},
					operatorid.PassphraseField.OnlyWith("server"),
				},
				HumanOnly: true,
				// The consent queue, answerable from the screen the operator is already
				// looking at: a parked call is a question with exactly two answers, and
				// until now both of them lived in another terminal.
				//
				// `a` and `d` are the verbs' own initials, and the asymmetry between them
				// is the point. Deny is bare, so it runs on the keypress — the safe answer
				// is one key, and a denial the operator did not mean costs the agent a
				// retry. Allow is not, so the TUI opens its form (--ttl above all):
				// granting access stops for a confirmation, which is the direction that
				// cannot be taken back once a secret has been read. The asymmetry used to
				// fall out of the declarations alone — deny had no second input — until the
				// remote consent flow gave deny `--server` and a passphrase; now it is
				// declared here and pinned by the TUI's consent tests.
				//
				// `d` also means "done" on the task lists, and net.hosts.list avoided
				// exactly that overlap. It is deliberate here: this screen is a security
				// prompt rather than another list, both keys spell their own verb, and the
				// mistake the overlap could produce — denying a call meant to be allowed —
				// is the recoverable one.
				//
				// enter is "show" on every list, and a parked call has more to show than a
				// row can hold — what it would actually do, most of all. Reading before
				// answering is the point, so the key that opens the detail is the one
				// already in everybody's fingers — and bare, because a form between the
				// list and the reading would teach people to answer without the reading.
				// `L` is the instant no: it opens the lock form beside the call that made
				// you want it, the agent filled in from the queue's own column so the name
				// the gate verifies is the one the call carried, not one retyped under
				// pressure.
				//
				// Live: a call parks, another expires, and the queue used to be current
				// only as a dashboard tile — opened, it showed the moment it was opened
				// until `r`.
				Actions: []plugin.Action{
					{Key: "enter", Label: "show", Target: "agent.show", Source: plugin.ActionRow, Bare: true},
					{Key: "a", Label: "allow", Target: "agent.allow", Source: plugin.ActionRow},
					{Key: "d", Label: "deny", Target: "agent.deny", Source: plugin.ActionRow, Bare: true},
					{Key: "L", Label: "lock", Target: "lock.add", Source: plugin.ActionRow, Seed: map[string]string{"name": "agent"}},
				},
				Live: true,
				Run:  runPending,
			},
			{
				ID:      "agent.show",
				Summary: "Everything about one parked call, including what it would do",
				Description: "The request in full: which capability, which record, against which " +
					"connection, every argument, and — for a destructive call rta could preview — " +
					"what running it would actually do, taken from the capability's own dry run. " +
					"That last part is the difference between approving an intention and approving " +
					"an outcome. For a kv call it also says when this shell has nothing that opens the " +
					"store, since allowing would then release a call that fails. Answer with " +
					"`agent.allow` or `agent.deny`.",
				Safety:     plugin.Read,
				Idempotent: true,
				Inputs: []plugin.Field{
					{Name: "id", Type: plugin.String, Positional: true, Suggest: suggestPending,
						Help: "the request id from `agent.pending`; left out when exactly one call is waiting"},
					{Name: "server", Type: plugin.String, Local: true, Remote: true,
						Help: "the request is parked on this remote server (a name from remotes.yaml)"},
					operatorid.PassphraseField.OnlyWith("server"),
				},
				HumanOnly: true,
				// The two answers again from the detail page, so reading it does not mean
				// going back to the list to act on what you read.
				Actions: []plugin.Action{
					{Key: "a", Label: "allow", Target: "agent.allow", Source: plugin.ActionSelf},
					{Key: "d", Label: "deny", Target: "agent.deny", Source: plugin.ActionSelf, Bare: true},
					{Key: "L", Label: "lock", Target: "lock.add", Source: plugin.ActionSelf, Seed: map[string]string{"name": "agent"}},
				},
				Run: runShow,
			},
			{
				ID:      "agent.allow",
				Flash:   true,
				Summary: "Allow one parked call",
				Description: "Authorizes exactly the call the request names, and nothing else — " +
					"the agent's call proceeds, and no standing state is created. At a terminal it " +
					"shows the call first — the capability, the record, the arguments and what its " +
					"dry run says it would do — and asks; `yes` and anything that is not a " +
					"terminal answer without asking. With `ttl` it " +
					"also issues the grants you would have typed (same target, same connection, one " +
					"for each record the call names and none wider), which is worth doing when the " +
					"same question is about to be asked five more times. Never reachable over MCP: " +
					"an agent that could answer its own request would make the whole mechanism " +
					"theatre. With `server`: answers " +
					"a call parked on a remote rta server as a signed operator call — every remote " +
					"answer costs your operator key's passphrase, one-shot included, because the " +
					"local one-shot's shell-equivalence argument does not travel a network.",
				Safety: plugin.Write,
				Scope:  "id",
				Inputs: []plugin.Field{
					{Name: "id", Type: plugin.String, Positional: true, Suggest: suggestPending,
						Help: "the request id from `agent.pending`; left out when exactly one call is waiting"},
					{Name: "ttl", Type: plugin.String,
						Help: "also issue a standing grant for this long, e.g. 15m (max 24h)"},
					{Name: "role", Type: plugin.String, Suggest: rtagrant.SuggestRoles,
						Help: "also issue this whole role to the asking agent — the day's grants under one passphrase, then this call"},
					// One passphrase field serves both gates that can ask: the
					// local guard's (with --ttl), and — with --server — the
					// operator key's. Same name, same channels, same argv refusal.
					guard.PassphraseField,
					{Name: "server", Type: plugin.String, Local: true, Remote: true,
						Help: "the request is parked on this remote server (a name from remotes.yaml)"},
				},
				HumanOnly: true,
				Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
					return runAllow(ctx, req, catalog, artifact)
				},
			},
			{
				ID:      "agent.deny",
				Flash:   true,
				Summary: "Deny one parked call",
				Description: "The agent's call is refused with your answer rather than with a " +
					"timeout, which is the difference between a model that stops and one that " +
					"retries. Never reachable over MCP. With `server`: denies a call parked " +
					"on a remote rta server, as a signed operator call.",
				Safety: plugin.Write,
				Scope:  "id",
				Inputs: []plugin.Field{
					{Name: "id", Type: plugin.String, Positional: true, Suggest: suggestPending,
						Help: "the request id from `agent.pending`; left out when exactly one call is waiting"},
					{Name: "server", Type: plugin.String, Local: true, Remote: true,
						Help: "the request is parked on this remote server (a name from remotes.yaml)"},
					operatorid.PassphraseField.OnlyWith("server"),
				},
				HumanOnly: true,
				Run:       runDeny,
			},
		},
	}
}

// agentField is the filter every listing of this namespace offers for "what
// did claude do", completing from the names this machine has seen.
func agentField(help string) plugin.Field {
	return plugin.Field{Name: "agent", Type: plugin.String, Suggest: rtagrant.SuggestAgents, Help: help}
}

// suggestSince offers the spans a person asks the record for, beside the
// spellings the flag's help describes.
func suggestSince(context.Context, plugin.Request) []string {
	return []string{
		"1h\tthe last hour",
		"today\tsince midnight",
		"yesterday\tsince the start of yesterday",
		"3d\tthe last three days",
	}
}

func suggestPending(context.Context, plugin.Request) []string {
	reqs, err := consent.Pending()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, r.ID+"\t"+r.Cap+" "+textclean.Records(r.Scopes))
	}
	return out
}

// Connected is one short line naming every server a client has open right
// now — its --as name and how many calls it has made — and how many there
// are. Short on purpose: it sits on a dashboard tile forty cells wide, and
// a glance wants "claude is attached and has called twice", not a
// paragraph. What the client called itself, since when, and the session id
// are the detail page's table (connectedTable) and `rta agent log`'s column.
// Shared with `rta doctor`, so the two never describe presence in different
// words.
//
// Returns the error rather than folding it into "nothing connected" the way
// an empty session store also would: session.List failing (the store's
// permissions changed, a transient I/O error) and it succeeding with no
// server open are not the same fact, and "is anything attached" is the one
// question an operator opens this during an incident to ask.
func Connected() (string, int, error) { return connectedFor("") }

// connectedFor is Connected narrowed to one agent's servers, or to all of them
// for an empty name.
//
// Servers that run under one name are one entry: three windows of the same
// client are `claude ×3`, which is what an operator counting their clients
// means, and their session ids are the detail page's table. Written one
// "claude (1 call)" after another it read as three agents, or as one repeated
// by mistake.
func connectedFor(agent string) (string, int, error) {
	open, calls, err := openSessions()
	if err != nil {
		return "", 0, err
	}
	if agent != "" {
		open = slices.DeleteFunc(open, func(s session.Record) bool { return s.Agent != agent })
	}
	if len(open) == 0 {
		return "", 0, nil
	}
	type group struct{ servers, calls int }
	var order []string
	by := map[string]*group{}
	for _, s := range open {
		name := agentOf(s)
		g := by[name]
		if g == nil {
			g = &group{}
			by[name] = g
			order = append(order, name)
		}
		g.servers++
		g.calls += calls[s.ID]
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		g := by[name]
		if g.servers > 1 {
			name += " ×" + strconv.Itoa(g.servers)
		}
		parts = append(parts, fmt.Sprintf("%s (%d %s)", name, g.calls, format.Plural(g.calls, "call", "calls")))
	}
	return fmt.Sprintf("%d — %s", len(open), strings.Join(parts, "; ")), len(open), nil
}

func openSessions() ([]session.Record, map[string]int, error) {
	open, err := session.List()
	if err != nil {
		return nil, nil, err
	}
	calls := map[string]int{}
	if len(open) > 0 {
		// Since the oldest open server started: every call of every open
		// session is inside that window, and nothing older matters here.
		// A second early: the record stamps entries to the second, and a
		// server's first call can land inside the second it started in.
		if entries, err := agentlog.Recent(open[0].Since.Add(-time.Second)); err == nil {
			for _, e := range entries {
				if e.Session != "" {
					calls[e.Session]++
				}
			}
		}
	}
	return open, calls, nil
}

func agentOf(s session.Record) string {
	if s.Agent == "" {
		return "(unnamed)"
	}
	return s.Agent
}

// connectedTable is presence in full, one row per open server. The record
// column is the file that server writes to: when it is not the one this
// process reads, that is the whole explanation for an empty log.
func connectedTable(agent string) (view.Table, error) {
	open, calls, err := openSessions()
	if err != nil {
		return view.Table{}, err
	}
	if agent != "" {
		open = slices.DeleteFunc(open, func(s session.Record) bool { return s.Agent != agent })
	}
	t := view.Table{Columns: []view.Column{
		{Name: "agent"}, {Name: "client"}, {Name: "since", Kind: view.KindTimestamp},
		{Name: "calls"}, {Name: "no grant"}, {Name: "roots"}, {Name: "session"},
		{Name: "directory"}, {Name: "record"},
	}}
	for _, s := range open {
		// "asks" or "refuses" is the whole answer to "why did that call not
		// park", and it was in the client's config file and nowhere rta showed.
		missing := "refuses"
		if s.Consent {
			missing = "asks"
		}
		t.Rows = append(t.Rows, []string{agentOf(s), s.Client, format.Ago(s.Since),
			strconv.Itoa(calls[s.ID]), missing, strings.Join(s.Roots, ", "), s.ID, s.Dir, s.Ledger})
	}
	t.Total = len(t.Rows)
	if len(t.Rows) == 0 {
		t.Empty = "nothing is connected — a client with an rta server open appears here"
	}
	return t, nil
}

// nothingWaiting is the empty queue's sentence, with the call that answers
// the next one as sf, the surface showing it, makes it.
func nothingWaiting(sf plugin.Surface) string {
	return "nothing is waiting — a parked call appears here, and `" +
		sf.Call("agent.allow", plugin.Arg{Name: "id", Value: "<id>", Positional: true}) + "` releases it"
}

// connectedView and waitingView are the overview's sections, which a screen
// shows as a sentence when empty for the reason `agent pending` does: an
// empty bordered table under a heading reads as a screen that failed to load.
// The tables carry that sentence themselves (view.Table.Empty), so a parser
// reading the page still meets a table.
func connectedView(agent string) view.View {
	t, err := connectedTable(agent)
	if err != nil {
		return view.Text{Body: "unreadable — " + err.Error()}
	}
	return t
}

func waitingView(sf plugin.Surface, reqs []consent.Request, err error) view.View {
	if err != nil {
		return view.Text{Body: "unreadable — " + err.Error()}
	}
	return pendingTable(sf, reqs)
}

func runOverview(_ context.Context, req plugin.Request) (view.View, error) {
	agent := strings.TrimSpace(req.String("agent"))
	hour := time.Now().Add(-time.Hour)
	// Bounded by time, not by a display-sized window: a busy hour has more
	// than five hundred calls, and the tile said 500.
	entries, err := agentlog.Recent(hour)
	if err != nil {
		return nil, view.Errorf("agent.log.unreadable", "%v", err)
	}
	waiting, pendingErr := consent.Pending()
	if agent != "" {
		entries = slices.DeleteFunc(entries, func(e agentlog.Entry) bool { return e.Agent != agent })
		waiting = slices.DeleteFunc(waiting, func(r consent.Request) bool { return r.Agent != agent })
	}

	// The refusals told apart by who can act on them, because "refused 6" mixed
	// the three kinds a person reads three different ways: a call waiting on a
	// grant only they can issue, a call the agent got wrong and will fix, and a
	// gate that did its job. The split is read from the codes the record already
	// carries (kindOf), never stored.
	var recent, approved int
	var needsGrant, malformed, other []agentlog.Entry
	for _, e := range entries {
		recent++
		if e.Outcome == agentlog.Refused {
			switch kindOf(codeOf(e)) {
			case refusedNeedsGrant:
				needsGrant = append(needsGrant, e)
			case refusedMalformed:
				malformed = append(malformed, e)
			default:
				other = append(other, e)
			}
		}
		if e.Auth == agentlog.Live {
			approved++
		}
	}
	// The one line on this tile that is about the present rather than the
	// past, and the only one that asks for anything. A bare count reads as
	// another statistic beside the other three; the number and the key to
	// press together are what turn a tile somebody glances at into a queue
	// somebody clears.
	nowWaiting := fmt.Sprintf("%d", len(waiting))
	switch {
	case pendingErr != nil:
		nowWaiting = "unreadable — " + pendingErr.Error()
	case len(waiting) > 0 && req.Surface() == plugin.SurfaceTUI:
		nowWaiting += " — press w to answer"
	case len(waiting) > 0:
		// A key is the TUI's, and a terminal command line has none: the same
		// line printed there told somebody to press a key their shell does not
		// bind, in the one place that should name the command that answers.
		nowWaiting += " — see " + req.Surface().CapabilityName("agent.pending")
	}
	// Presence before activity: "is anything attached" is the question
	// every zero below raises, and it is the one the ledger cannot answer.
	connected, n, connErr := connectedFor(agent)
	switch {
	case connErr != nil:
		connected = "unreadable — " + connErr.Error()
	case n == 0 && agent != "":
		connected = "none — no server named " + agent + " has an rta server open"
	case n == 0:
		connected = "none — no client has an rta server open; `rta mcp install claude`, then restart the client"
	}
	sf := req.Surface()
	listed := sf.CapabilityWith("agent.log", "refused") + " lists them"
	pairs := []view.Pair{
		{Key: "waiting on you", Value: nowWaiting},
		{Key: "connected now", Value: connected},
		{Key: "locked", Value: lockedLine(sf)},
		{Key: "roles in force", Value: rolesLine()},
		{Key: "calls in the last hour", Value: fmt.Sprintf("%d", recent)},
		{Key: "needs your grant", Value: refusalCount(needsGrant, listed)},
	}
	// Only when there are some: a zero on a tile is read as a thing that is
	// watched, and these are not what an operator watches for.
	if len(malformed) > 0 {
		pairs = append(pairs, view.Pair{Key: "malformed or unknown calls", Value: refusalCount(malformed, "")})
	}
	if len(other) > 0 {
		pairs = append(pairs, view.Pair{Key: "refused otherwise", Value: refusalCount(other, "")})
	}
	pairs = append(pairs, view.Pair{Key: "you approved live", Value: fmt.Sprintf("%d", approved)})
	if agentlog.Started() {
		if err := agentlog.Writable(); err != nil {
			pairs = append(pairs, view.Pair{Key: "recording",
				Value: "cannot be written — a call that needs a grant is refused until it can, and the rest are not recorded"})
		}
	}
	if last, err := agentlog.Read(1); err == nil && len(last) > 0 {
		pairs = append(pairs, view.Pair{Key: "last call",
			Value: fmt.Sprintf("%s %s, %s", last[0].Cap, last[0].Outcome, format.Ago(last[0].At))})
	} else {
		pairs = append(pairs, view.Pair{Key: "last call", Value: "nothing recorded yet"})
	}
	if !req.Bool("detail") {
		return view.KeyValue{Pairs: pairs}, nil
	}

	rep, verr := agentlog.Verify()
	return view.Sections{Items: []view.Section{
		{ID: "activity", Title: "Activity", View: view.KeyValue{Pairs: pairs}},
		{ID: "connected", Title: "Connected now", View: connectedView(agent)},
		{ID: "record", Title: "The record", View: view.KeyValue{Pairs: recordPairs(rep, verr)}},
		{ID: "waiting", Title: "Waiting on you", View: waitingView(req.Surface(), waiting, pendingErr)},
	}}, nil
}

// refusalCount is how many refusals of one kind the hour held, which calls they
// were, and — beside the first kind only, the one that is the operator's to
// act on — where to read them.
func refusalCount(entries []agentlog.Entry, where string) string {
	if len(entries) == 0 {
		return "0"
	}
	line := fmt.Sprintf("%d — %s", len(entries), callList(entries))
	if where != "" {
		line += "; " + where
	}
	return line
}

// lockedLine says which principals are frozen, on the one screen an
// operator glances at. A lock used to be visible on `lock list` and nowhere
// else, so an agent frozen during an incident and forgotten stayed frozen
// with nothing on the dashboard saying so.
// rolesLine is which roles stand for which agents, the way the roster
// says it — the overview is the screen the dashboard tile opens onto, and
// "is dev still issued to claude" belongs beside connected and locked.
func rolesLine() string {
	force, verr := rtagrant.RolesInForce()
	if verr != nil {
		return "unreadable — " + verr.Message
	}
	if force != "" {
		return strings.ReplaceAll(force, "\n", "; ")
	}
	return "none"
}

func lockedLine(sf plugin.Surface) string {
	locks, verr := lockdown.Load()
	if verr != nil {
		return "unreadable — " + verr.Message
	}
	if len(locks) == 0 {
		return "nothing"
	}
	names := make([]string, 0, len(locks))
	for _, l := range locks {
		names = append(names, l.Label())
	}
	return strings.Join(names, ", ") + " — " + sf.CapabilityName("lock.list") + " says why"
}

func recordPairs(rep agentlog.Report, verr error) []view.Pair {
	pairs := []view.Pair{
		{Key: "file", Value: agentlog.Path()},
		{Key: "entries", Value: fmt.Sprintf("%d", rep.Entries)},
		{Key: "size", Value: format.Bytes(rep.Size)},
	}
	if rep.Files > 1 {
		pairs = append(pairs, view.Pair{Key: "files",
			Value: fmt.Sprintf("%d, rolled at 8 MB apiece", rep.Files)})
	}
	if rep.Missed > 0 {
		pairs = append(pairs, view.Pair{Key: "not recorded",
			Value: format.CountOf(int(rep.Missed), "call") +
				" rta could not write down — the entries after them say where"})
	}
	if len(rep.MarkLost) > 0 {
		pairs = append(pairs, view.Pair{Key: "end mark",
			Value: fmt.Sprintf("was missing before %s %s — anything removed from the end before then cannot be detected; everything after can",
				format.Plural(len(rep.MarkLost), "entry", "entries"), joinSeqs(rep.MarkLost))})
	}
	if len(rep.Foreign) > 0 {
		// Named here and not only in `rta doctor`, because this is the
		// screen somebody opens to ask whether the record is sound. A file
		// sitting in the data directory under a segment name whose last
		// entry does not verify is not part of the record and is not treated
		// as one — but something put it there, and that is worth knowing
		// whatever it failed to achieve.
		pairs = append(pairs, view.Pair{Key: "not rta's",
			Value: fmt.Sprintf("%s in the data directory %s named like the record and %s not written by rta: %s",
				format.Plural(len(rep.Foreign), "file", "files"),
				format.Plural(len(rep.Foreign), "is", "are"),
				format.Plural(len(rep.Foreign), "was", "were"),
				strings.Join(rep.Foreign, ", "))})
	}
	if rep.Retired > 0 {
		pairs = append(pairs, view.Pair{Key: "retired",
			Value: fmt.Sprintf("the first %s, dropped %s — the chain still verifies across the gap",
				format.CountOf(int(rep.Retired), "call"), rep.RetiredAt.Local().Format("2006-01-02 15:04"))})
	}
	switch {
	case verr != nil:
		pairs = append(pairs, view.Pair{Key: "chain", Value: verr.Error()})
	case rep.Broken != 0:
		pairs = append(pairs, view.Pair{Key: "chain",
			Value: fmt.Sprintf("BROKEN at entry %d — %s", rep.Broken, rep.Why)})
	default:
		pairs = append(pairs, view.Pair{Key: "chain",
			Value: "whole — every entry follows the one before it, matches its seal, and the record ends where rta last left it"})
	}
	return pairs
}

// logFilter is what `agent log` keeps of the calls it read.
type logFilter struct {
	refused              bool
	agent, session, role string
	since                time.Time
	after                int64
}

// picks reports whether the filter keeps a scattered subset of the record, as
// opposed to a suffix of it. --after and --since keep a suffix: the newest
// thirty that pass them are the newest thirty that pass them, whether thirty
// or five hundred were read to find them. The others can come up short.
func (f logFilter) picks() bool {
	return f.refused || f.agent != "" || f.session != "" || f.role != ""
}

func (f logFilter) any() bool {
	return f.picks() || f.after > 0 || !f.since.IsZero()
}

func (f logFilter) keeps(e agentlog.Entry) bool {
	switch {
	case f.refused && e.Outcome != agentlog.Refused:
	case f.agent != "" && e.Agent != f.agent:
	case f.session != "" && e.Session != f.session:
	case f.role != "" && !roleCovers(e.Role, f.role):
	case e.Seq <= f.after:
	case !f.since.IsZero() && e.At.Before(f.since):
	default:
		return true
	}
	return false
}

func runLog(_ context.Context, req plugin.Request) (view.View, error) {
	limit := req.Int("limit")
	if limit <= 0 {
		limit = 30
	}
	since, sinceNote, sinceErr := parseSince(req.String("since"))
	if sinceErr != nil {
		return nil, sinceErr
	}
	f := logFilter{
		refused: req.Bool("refused"),
		agent:   strings.TrimSpace(req.String("agent")),
		session: strings.TrimSpace(req.String("session")),
		role:    strings.TrimSpace(req.String("role")),
		since:   since,
		after:   int64(req.Int("after")),
	}
	// Read more than asked for when filtering, so `--refused --limit 10`
	// answers with ten refusals rather than the refusals among the last ten
	// calls — which is the same number for a quiet server and nothing at
	// all for a busy one.
	//
	// --after and --since deliberately do not widen it, and the difference is
	// worth stating because the first draft widened all three. Those two keep
	// a *suffix* of the record: the newest thirty entries that pass them are
	// the newest thirty that pass them, whether thirty or five hundred were
	// read to find them. --refused keeps a scattered subset, which is the
	// only one of the three that can come up short.
	//
	// Selected newest-first, shown oldest-first. The selection walks back
	// from the end because "the last thirty" is what a limit means on a
	// log; the table is then put back in time order, newest at the bottom,
	// because that is where a log is read from — the terminal ends on the
	// latest call and the eye walks up into the past, and the TUI opens on
	// the last row for the same reason (view.Table.Tail).
	want := limit
	if f.picks() {
		want = maxRows
	}
	var entries []agentlog.Entry
	var err error
	more := false
	switch {
	case f.after > 0:
		// A cursor reads forward: the rows just past it, not the newest
		// rows that happen to be past it. The difference is the whole
		// shipping recipe — an archive appended from the newest end skips
		// whatever a burst wrote between two runs. One more than wanted is
		// read, which is how a page knows there is another.
		entries, err = agentlog.ReadAfter(f.after, want+1)
		if more = len(entries) > want; more {
			entries = entries[:want]
		}
	case !since.IsZero():
		// Bounded by the clock, not by a count: the calls since a time are all
		// of them, whatever else is asked of them, and the footer can then say
		// how many there are.
		entries, err = agentlog.Recent(since)
	default:
		entries, err = agentlog.Read(want)
	}
	if err != nil {
		return nil, view.Errorf("agent.log.unreadable", "%v", err)
	}
	matched := make([]agentlog.Entry, 0, min(len(entries), limit))
	for i := len(entries) - 1; i >= 0; i-- {
		if e := entries[i]; f.keeps(e) {
			matched = append(matched, e)
		}
	}
	shown := slices.Clone(matched[:min(limit, len(matched))])
	slices.Reverse(shown)

	// How many calls the rows were chosen from. A listing that stops at its
	// limit and does not say so reads as the whole record, and for an audit
	// view "I do not see it" reads as "it did not happen".
	//
	// The newest-first read is bounded by what it was asked for, so when it
	// came back full there may be more behind it: the record is counted then,
	// a line at a time and without keeping any, and only then.
	total, reachedBack := len(matched), true
	behind := f.after == 0 && since.IsZero() && len(entries) >= want
	if behind {
		reachedBack = false
		if !f.picks() {
			if n, err := agentlog.Calls(func(agentlog.Call) {}); err == nil {
				total, reachedBack = n, true
			}
		}
	}

	terse := compact.For(req)
	cols := logColumns(shown, terse, req.Bool("detail"))
	stamp := stampFormat(shown, time.Now())
	rows := make([][]string, 0, len(shown))
	for _, e := range shown {
		row := make([]string, len(cols))
		for i, c := range cols {
			row[i] = c.cell(e, stamp)
		}
		rows = append(rows, row)
	}
	columns := make([]view.Column, len(cols))
	for i, c := range cols {
		columns[i] = c.Column
	}
	table := view.Table{Columns: columns, Rows: rows, Total: total, Tail: true}
	if more && len(shown) > 0 {
		table.Page = &view.Cursor{Next: strconv.FormatInt(shown[len(shown)-1].Seq, 10)}
	}
	if sinceNote != "" {
		table.Warnings = append(table.Warnings, view.Error{Code: "agent.log.since", Message: sinceNote})
	}
	sf := req.Surface()
	matching := ""
	if f.any() {
		matching = "matching "
	}
	if hidden := total - len(shown); hidden > 0 && reachedBack && f.after == 0 {
		table.Warnings = append(table.Warnings, view.Error{Code: "agent.log.older", Advisory: true,
			Message: format.Count(hidden, "older "+matching+"call is", "older "+matching+"calls are") + " not shown",
			Hint:    moreHint(sf, total)})
	}
	if !reachedBack && f.picks() {
		table.Warnings = append(table.Warnings, view.Error{Code: "agent.log.window", Advisory: true,
			Message: fmt.Sprintf("only the newest %d calls were searched, so an older one that matches is not here", len(entries)),
			Hint:    sf.InputName("since") + " searches as far back as it names"})
	}

	// A sentence for a screen, as `agent pending` has: the record's columns
	// with nothing under them read as a listing that failed, and what a filter
	// matched nothing of is not the same news as a record with nothing in it.
	switch {
	case len(rows) > 0:
	case f.any():
		table.Empty = "no recorded call matches the filters given" + unknownAgentClause(f.agent)
	default:
		table.Empty = "no call has arrived over MCP yet — what an agent asks of rta appears here, refusals included"
	}
	if !req.Bool("detail") {
		return table, nil
	}
	rep, verr := agentlog.Verify()
	return view.Sections{Items: []view.Section{
		{ID: "calls", Title: "Calls", View: table},
		{ID: "integrity", Title: "The record itself", View: view.KeyValue{Pairs: recordPairs(rep, verr)}},
	}}, nil
}

// moreHint is how to see what a listing left out: the limit that shows it, as
// far as a limit goes, and the filters that make the rest smaller.
func moreHint(sf plugin.Surface, total int) string {
	ask := min(total, maxRows)
	hint := sf.InputTo("limit", ask) + " shows "
	if total > maxRows {
		hint += "the newest " + strconv.Itoa(maxRows)
	} else {
		hint += "all of them"
	}
	narrow := []string{sf.InputName("since"), sf.InputName("agent"), sf.InputName("refused")}
	return hint + "; " + strings.Join(narrow[:len(narrow)-1], ", ") + " and " + narrow[len(narrow)-1] + " narrow it"
}

// unknownAgentClause says, when --agent matched nothing, which agents the
// record does know: a name mistyped reads as an agent that did nothing.
func unknownAgentClause(agent string) string {
	if agent == "" {
		return ""
	}
	seen := map[string]bool{}
	if entries, err := agentlog.Read(maxRows); err == nil {
		for _, e := range entries {
			if e.Agent != "" {
				seen[e.Agent] = true
			}
		}
	}
	if seen[agent] {
		return ""
	}
	if len(seen) == 0 {
		return " — the record holds no call from an agent named " + strconv.Quote(agent)
	}
	return " — the record holds no call from an agent named " + strconv.Quote(agent) +
		", it knows " + strings.Join(slices.Sorted(maps.Keys(seen)), ", ")
}

// logColumn is one column of the log with the way to fill it.
type logColumn struct {
	view.Column
	cell func(e agentlog.Entry, stamp string) string
}

// logColumns decides which columns the rows shown carry, and in which order.
//
// Every column but the always-there ones appears only once a row can fill it,
// which for a record written before agents were named, or before rta could
// serve over HTTP at all, is never: a column of em dashes on the screen an
// operator opens in a hurry is a column they learn to skip.
//
// **Two shapes.** The whole table, every field of the entry, is what a script
// and `--detail` read, and its columns are the record's own. The compact one
// is what a person scans on a terminal: when, what, the record it named and
// what became of it, in a row that fits eighty cells. The columns that say
// who — agent, credential, session — join it only when the rows shown have
// more than one to tell apart, because a column of one constant value says
// nothing and takes a fifth of the width.
func logColumns(shown []agentlog.Entry, terse, detail bool) []logColumn {
	has := func(f func(agentlog.Entry) bool) bool { return slices.ContainsFunc(shown, f) }
	several := func(f func(agentlog.Entry) string) bool {
		var first string
		for i, e := range shown {
			if i == 0 {
				first = f(e)
			} else if f(e) != first {
				return true
			}
		}
		return false
	}
	recorded := has(func(e agentlog.Entry) bool { return len(e.Records) > 0 })

	at := logColumn{view.Column{Name: "at", Kind: view.KindTimestamp},
		func(e agentlog.Entry, stamp string) string { return e.At.Local().Format(stamp) }}
	capability := logColumn{view.Column{Name: "capability"},
		func(e agentlog.Entry, _ string) string { return e.Cap }}
	agent := logColumn{view.Column{Name: "agent"},
		func(e agentlog.Entry, _ string) string { return whoCalled(e) }}
	credential := logColumn{view.Column{Name: "credential"},
		func(e agentlog.Entry, _ string) string { return credentialCell(e) }}
	session := logColumn{view.Column{Name: "session"},
		func(e agentlog.Entry, _ string) string { return sessionCell(e) }}
	record := logColumn{view.Column{Name: "record"},
		func(e agentlog.Entry, _ string) string { return recordsCell(e.Records) }}

	if terse {
		cols := []logColumn{at, capability}
		if several(whoCalled) {
			cols = append(cols, agent)
		}
		if several(credentialCell) {
			cols = append(cols, credential)
		}
		if several(sessionCell) {
			cols = append(cols, session)
		}
		if recorded {
			cols = append(cols, record)
		}
		return append(cols, logColumn{view.Column{Name: "result", Kind: view.KindStatus},
			func(e agentlog.Entry, _ string) string { return resultPhrase(e) }})
	}

	cols := []logColumn{
		{view.Column{Name: "seq"}, func(e agentlog.Entry, _ string) string { return strconv.FormatInt(e.Seq, 10) }},
		at, capability,
	}
	if has(func(e agentlog.Entry) bool { return e.Agent != "" || e.Client != "" }) {
		cols = append(cols, agent)
		// What the client called itself, beside the name the operator gave it:
		// the pair the docs describe, one a person chose and one that only the
		// client asserts. Only on the full page, so that the exact rows a log
		// shipper reads do not grow a field they were not written against.
		if detail && has(func(e agentlog.Entry) bool { return e.Client != "" }) {
			cols = append(cols, logColumn{view.Column{Name: "client"},
				func(e agentlog.Entry, _ string) string { return dashed(e.Client) }})
		}
	}
	if has(func(e agentlog.Entry) bool { return e.Credential != "" }) {
		cols = append(cols, credential)
	}
	if has(func(e agentlog.Entry) bool { return e.Session != "" }) {
		cols = append(cols, session)
	}
	if has(func(e agentlog.Entry) bool { return e.Role != "" }) {
		cols = append(cols, logColumn{view.Column{Name: "role"},
			func(e agentlog.Entry, _ string) string { return dashed(e.Role) }})
	}
	// The records as the call named them, shown as every other surface
	// shows a record (textclean.Record): the arguments beside them are
	// kept cleaned, and a record padded with a zero-width character read
	// there as the bare one.
	if recorded {
		cols = append(cols, record)
	}
	cols = append(cols,
		logColumn{view.Column{Name: "arguments"}, func(e agentlog.Entry, _ string) string { return argsLine(e.Args) }},
		logColumn{view.Column{Name: "profile"}, func(e agentlog.Entry, _ string) string { return e.Profile }},
		logColumn{view.Column{Name: "outcome", Kind: view.KindStatus},
			func(e agentlog.Entry, _ string) string { return string(e.Outcome) }},
		logColumn{view.Column{Name: "authorized"}, func(e agentlog.Entry, _ string) string { return string(e.Auth) }},
	)
	// Same appears-when-filled rule as the columns above: a record written
	// before the code/reason split — or one where nothing has gone wrong —
	// shows no code column at all, rather than a column of blanks. Old rows
	// keep their glued "code: message" in why; new rows carry the code here,
	// exactly, which is the cell a shipped copy of this table gets matched on.
	if has(func(e agentlog.Entry) bool { return e.Code != "" }) {
		cols = append(cols, logColumn{view.Column{Name: "code"}, func(e agentlog.Entry, _ string) string { return e.Code }})
	}
	return append(cols, logColumn{view.Column{Name: "why"}, func(e agentlog.Entry, _ string) string { return whyLine(e) }})
}

func runPending(ctx context.Context, req plugin.Request) (view.View, error) {
	if server := strings.TrimSpace(req.String("server")); server != "" {
		return remotePending(ctx, req, server)
	}
	reqs, err := consent.Pending()
	if err != nil {
		return nil, view.Errorf("agent.pending.unreadable", "%v", err)
	}
	return pendingTable(req.Surface(), reqs), nil
}

func pendingTable(sf plugin.Surface, reqs []consent.Request) view.Table {
	// Shown only when something is asking under a name. The queue is the one
	// screen where the answer is a decision, so "which of my agents is this"
	// belongs beside the capability rather than one command away — and where
	// nobody has named an agent there is only ever one asker.
	asking := false
	for _, r := range reqs {
		if r.Agent != "" {
			asking = true
			break
		}
	}
	rows := make([][]string, 0, len(reqs))
	for _, r := range reqs {
		left := time.Until(r.Deadline).Truncate(time.Second)
		if left < 0 {
			left = 0
		}
		// The preview itself is prose and belongs on `agent show`; what the
		// list owes is the fact that there is one to read before answering.
		what := argsLine(r.Args)
		if r.Preview != "" {
			what = r.Preview
		}
		// The record as the gate compares it, byte for byte: a padded or
		// invisible character in it is shown quoted and named, or the
		// operator reads the bare record while answering for another
		// (textclean.Record).
		row := []string{
			r.ID, r.Cap, recordsCell(r.Scopes), r.Safety, r.Profile,
			clip(what), format.Duration(left),
		}
		if asking {
			row = slices.Insert(row, 1, r.Agent)
		}
		rows = append(rows, row)
	}
	cols := []view.Column{
		{Name: "id"}, {Name: "capability"}, {Name: "record"}, {Name: "safety", Kind: view.KindStatus},
		{Name: "profile"}, {Name: "would do"}, {Name: "expires in", Kind: view.KindDuration},
	}
	if asking {
		cols = slices.Insert(cols, 1, view.Column{Name: "agent"})
	}
	t := view.Table{Columns: cols, Rows: rows, Total: len(rows)}
	// A sentence on a screen, not an empty bordered table: the queue is the
	// screen `press w to answer` lands on. The table under it all the same,
	// for everything that parses it — see view.Table.Empty.
	if len(rows) == 0 {
		t.Empty = nothingWaiting(sf)
	}
	return t
}

// recordsCell is a row's records, each as textclean.Record shows one, or a
// dash for a call that named none.
func recordsCell(records []string) string {
	if len(records) == 0 {
		return "—"
	}
	return textclean.Records(records)
}

// roleCovers reports whether a row's role column names the role asked
// for — one of several, when several grants covered one call.
func roleCovers(cell, want string) bool {
	for _, r := range strings.Split(cell, ",") {
		if strings.TrimSpace(r) == want {
			return true
		}
	}
	return false
}

func dashed(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func sessionCell(e agentlog.Entry) string {
	if e.Session == "" {
		return "—"
	}
	return e.Session
}

// whoCalled is the one cell that answers "which agent was this".
//
// **Two fields, and the rendering keeps them apart.** e.Agent is the operator's
// own name for this server and is what the grant was compared against; e.Client
// is what the caller announced for itself, which anything speaking the protocol
// can set to anything. So a name the operator chose is printed plainly, and a
// name only the client asserts is printed in parentheses — the parentheses mean
// "nobody checked this". Printing them the same way would be the more readable
// table and the dishonest one.
func whoCalled(e agentlog.Entry) string {
	switch {
	case e.Agent != "":
		return e.Agent
	case e.Client != "":
		return "(" + e.Client + ")"
	default:
		return "—"
	}
}

// credentialCell is which bearer credential authenticated this call — a
// static token's label or an OIDC subject — distinct from whoCalled: --as
// names one principal per server, e.Client is whatever a caller claims about
// itself, and neither says which of possibly several valid tokens for that
// one server actually authenticated this particular row. Empty for every
// call served over stdio, where nothing on the wire verified an identity.
func credentialCell(e agentlog.Entry) string {
	if e.Credential == "" {
		return "—"
	}
	return e.Credential
}

func runShow(ctx context.Context, req plugin.Request) (view.View, error) {
	id, verr := requestNamed(req, "agent.show")
	if verr != nil {
		return nil, verr
	}
	if server := strings.TrimSpace(req.String("server")); server != "" {
		return remoteShow(ctx, req, server, id)
	}
	r, ok := consent.Find(id)
	if !ok {
		return nil, unknownRequest(req.Surface(), id)
	}
	var local []view.Pair
	if w := kvWarning(r); w != "" {
		local = append(local, view.Pair{Key: "warning", Value: w})
	}
	return showView(req.Surface(), r, local...), nil
}

// showView renders one request in full, wherever it was fetched from — the
// local queue and a remote server's answer the same question, and two
// renderings would drift apart exactly where an operator compares them. sf
// is the surface showing it, for the calls the page names; extra is what only
// the machine holding the queue can say about it, which a remote server's
// answer has no way to carry.
func showView(sf plugin.Surface, r consent.Request, extra ...view.Pair) view.View {
	left := time.Until(r.Deadline).Truncate(time.Second)
	if left < 0 {
		left = 0
	}
	pairs := []view.Pair{
		{Key: "capability", Value: r.Cap},
		{Key: "safety", Value: r.Safety},
	}
	if len(r.Scopes) > 0 {
		pairs = append(pairs, view.Pair{Key: "record", Value: textclean.Records(r.Scopes)})
	}
	if r.Profile != "" {
		pairs = append(pairs, view.Pair{Key: "connection", Value: r.Profile})
	}
	// Which agent asked, on the page where the operator decides. Omitted when
	// nothing was named, because "agent: —" on a detail page reads as a fact
	// about this request rather than as an absence of configuration.
	if r.Agent != "" {
		pairs = append(pairs, view.Pair{Key: "agent", Value: r.Agent})
	}
	if hint := roleHint(sf, r); hint != "" {
		pairs = append(pairs, view.Pair{Key: "role", Value: hint})
	}
	pairs = append(pairs,
		view.Pair{Key: "why you are being asked", Value: r.Why},
		view.Pair{Key: "asked", Value: format.Ago(r.AskedAt)},
		view.Pair{Key: "expires in", Value: format.Duration(left)},
	)
	pairs = append(pairs, extra...)
	sections := []view.Section{
		{ID: "request", Title: "The request", View: view.KeyValue{Pairs: pairs}},
	}
	if len(r.Args) > 0 {
		arg := make([]view.Pair, 0, len(r.Args))
		keys := make([]string, 0, len(r.Args))
		for k := range r.Args {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			arg = append(arg, view.Pair{Key: k, Value: argValue(r.Args[k])})
		}
		sections = append(sections, view.Section{
			ID: "arguments", Title: "Arguments", View: view.KeyValue{Pairs: arg}})
	}
	// The preview, and the sentence that bounds it. An operator reading
	// "would remove task 4" has to know whether they are reading a fact
	// about this call or a guess — and for a capability rta will not
	// preview, silence would read as "it would do nothing".
	body := r.Preview
	if body == "" {
		body = notPreviewed(r)
	}
	sections = append(sections, view.Section{
		ID: "outcome", Title: "What it would do", View: view.Text{Body: body}})
	return view.Sections{Items: sections}
}

// notPreviewed says why there is no preview, in the caller's terms.
func notPreviewed(r consent.Request) string {
	switch {
	case r.Safety != string(plugin.Destructive):
		return "no preview: rta previews destructive calls, and this one is a " + r.Safety +
			" — the capability and its arguments above are the whole of it"
	case r.Profile != "":
		return "no preview: this call names a connection, and rta resolves connections only " +
			"after you answer — a preview run without one would describe the wrong place convincingly"
	default:
		return "no preview: either this capability comes from an external plugin, whose dry run " +
			"is a promise rta cannot check, or its preview did not finish in time"
	}
}

// answeredBy names the surface that answered, for the decision file and the
// ledger. It was the literal "cli" once, which the TUI inherited by
// dispatching this same capability — a label, not a lie, but a wrong one.
// answeredBy is measured, not asserted: the same origin a grant records,
// so a one-shot answer typed at a terminal reads as such and one issued by
// a process with no terminal — an agent with a shell answering its own
// question — reads as "command", the way a self-issued grant does.
func answeredBy(req plugin.Request) string {
	return grant.Origin(req.Surface(), term.IsTerminal(int(stdio.Real().Fd())))
}

func runAllow(ctx context.Context, req plugin.Request, catalog func() []plugin.Capability, artifact func(string) (string, bool)) (view.View, error) {
	// As given, never trimmed, in show and deny too: the id is the one value
	// in an answer that says which question it answers, and allow and deny
	// declare it their record. An id is eight hex digits, so one with white
	// space around it names nothing waiting, and unknownRequest says so.
	id, verr := requestNamed(req, "agent.allow")
	if verr != nil {
		return nil, verr
	}
	if server := strings.TrimSpace(req.String("server")); server != "" {
		return remoteAnswer(ctx, req, server, id, true)
	}
	r, ok := consent.Find(id)
	if !ok {
		return nil, unknownRequest(req.Surface(), id)
	}
	// The whole role the call's line belongs to, issued through grant's own
	// flow — lines printed, ceiling per line, one passphrase, the team-role
	// rule — and then this call decided. Explicit, never a default: a
	// question about one call must not quietly become a day's authority.
	roleName := strings.TrimSpace(req.String("role"))
	if roleName != "" && strings.TrimSpace(req.String("ttl")) != "" {
		return nil, view.Errorf("agent.allow.either", "%s issues the whole role; %s issues this one line — pick one",
			req.Surface().InputName("role"), req.Surface().InputName("ttl"))
	}
	ttl := strings.TrimSpace(req.String("ttl"))
	// A person at a terminal is shown what they are about to release, and asked
	// (confirmAllow). After the refusals that need no answer from anybody, so a
	// yes is never followed by a no the ceiling already knew.
	if !req.DryRun && !req.Yes && atTerminal(req) {
		if verr := checkCeiling(r); verr != nil {
			return nil, verr
		}
		if !confirmAllow(r, allowQuestion(roleName, ttl, r.Agent)) {
			return declined(req.Surface(), r), nil
		}
	}
	var issued view.View
	if roleName != "" {
		sub := plugin.NewRequest(map[string]any{
			"role": roleName, "agent": r.Agent, "passphrase": req.String("passphrase"),
		}, req.DryRun, req.Yes).WithSurface(req.Surface())
		v, err := rtagrant.IssueRole(sub, catalog, artifact)
		if err != nil {
			return nil, err
		}
		issued = v
	}
	if req.DryRun {
		if issued != nil {
			return issued, nil
		}
		return view.Text{Body: fmt.Sprintf("would allow %s (%s) for the agent waiting on request %s",
			r.Cap, textclean.Records(r.Scopes), id)}, nil
	}
	// The team's ceiling binds a live "yes" exactly as it binds `grant allow`:
	// `never`/`neverProfile` are documented as needing nobody's agreement, not
	// as a default a rushed answer can override. Without this, a capability or
	// connection the policy forbids outright still reached internal/mcp's
	// askConsent — Reserve's refusal for "ceiling-suppressed" and "simply
	// ungranted" is deliberately the same core.grant.required, so an agent
	// cannot tell the two apart — and a bare `agent allow <id>` approved it
	// with no ceiling check anywhere in the path; only the --ttl branch below,
	// through alsoGrant, ever consulted the ceiling, and only after this
	// approval had already run. Checked here, at the one place a live
	// approval is minted, rather than duplicated where the call gets parked —
	// a second gate that could disagree with this one is the mistake
	// checkAgainst's own comment (internal/grant/grant.go) already names.
	if verr := checkCeiling(r); verr != nil {
		return nil, verr
	}
	// The guard, before the decision and not after: answering consumes the
	// parked request, so a passphrase refused past that point would release
	// the call, lose the standing grant, and leave nothing to retry against.
	// Refused here, the request stays parked and the retry costs a rerun.
	// The one-shot answer itself stays passphrase-free on purpose — it
	// releases a single call an agent with a shell could have run directly,
	// while --ttl mints authority that outlives this conversation, which is
	// exactly what the guard exists to price.
	//
	// Nor is the passphrase asked for a grant that will not be issued: whether one
	// narrow grant can stand for this call is known before anybody types
	// anything, and a passphrase spent on a refusal buys nothing.
	var unissued *view.Error
	if ttl != "" {
		unissued = noNarrowGrant(req.Surface(), r, ttl)
	}
	var signer *guard.Signer
	if ttl != "" && unissued == nil && guard.Enabled() {
		s, verr := guard.UnlockPrompted(req)
		if verr != nil {
			return nil, verr
		}
		signer = &s
	}
	if err := consent.Decide(id, true, answeredBy(req)); err != nil {
		return nil, view.Errorf("agent.allow.failed", "%v", err)
	}
	pairs := []view.Pair{
		{Key: "allowed", Value: callNamed(r.Cap, r.Scopes)},
		{Key: "for", Value: "this call only"},
	}
	// The bridge refuses a frozen agent before any other gate, so this
	// answer releases nothing while the lock stands — said here, because
	// "allowed" on the operator's screen and "refused" on the agent's is
	// the one disagreement between the two that nothing else explains.
	if l, _ := lockdown.NewPin().Frozen(lockdown.KindAgent, r.Agent); l != nil {
		lift := plugin.Arg{Name: "name", Value: r.Agent, Positional: true}
		who := "agent " + r.Agent
		if l.Name == lockdown.Everyone {
			lift, who = plugin.Arg{Name: "all", Value: true}, "every agent"
		}
		but := fmt.Sprintf("%s is locked, so the call is refused anyway until `%s`", who,
			req.Surface().Call("lock.rm", lift))
		if l.Held() {
			but = fmt.Sprintf("the locks rta keeps cannot be read or do not verify, so the call is refused anyway until `%s` "+
				"has been looked at", req.Surface().Call("lock.list"))
		}
		pairs = append(pairs, view.Pair{Key: "but", Value: but})
	}
	if w := kvWarning(r); w != "" {
		pairs = append(pairs, view.Pair{Key: "warning", Value: w})
	}
	if ttl != "" {
		note, verr := "", unissued
		if verr == nil {
			// Measured here rather than inside alsoGrant because the surface
			// is this request's fact, not the parked call's.
			from := grant.Origin(req.Surface(), term.IsTerminal(int(stdio.Real().Fd())))
			note, verr = alsoGrant(r, ttl, from, signer)
		}
		if verr != nil {
			// The call is already allowed; a bad --ttl must not read as if
			// nothing happened. The hint beside it, since the grant that was
			// not issued is often one the operator can still issue on
			// purpose, and the hint is how. A note beside a failure is the
			// grants issued before one failed, which stand.
			failed := "not issued: "
			if note != "" {
				pairs[1] = view.Pair{Key: "for", Value: note}
				failed = "not all issued: "
			}
			pairs = append(pairs, view.Pair{Key: "grant", Value: failed + verr.Message})
			if verr.Hint != "" {
				pairs = append(pairs, view.Pair{Key: "next", Value: verr.Hint})
			}
			return view.KeyValue{Pairs: pairs}, nil //nolint:nilerr // the call is already allowed; the grant that failed is reported in the answer, not as one
		}
		pairs[1] = view.Pair{Key: "for", Value: note}
	}
	if issued != nil {
		pairs[1] = view.Pair{Key: "for", Value: "this call, and the whole of role " + roleName + " issued to " + r.Agent}
		items := []view.Section{{ID: "answer", Title: "Answered", View: view.KeyValue{Pairs: pairs}}}
		if s, ok := issued.(view.Sections); ok {
			items = append(items, s.Items...)
		}
		return view.Sections{Items: items}, nil
	}
	return view.KeyValue{Pairs: pairs}, nil
}

// checkCeiling holds a parked call to the team's ceiling as a grant for it
// would be held: record by record, every one it names. It was asked with the
// call's record when there was one and with none otherwise, so a call naming
// two — every kv.rename, whose ScopeAlso names where the key goes — was read
// as a grant naming no record, and `requireScope: [kv.rename]` refused a
// call that named both of its own.
func checkCeiling(r consent.Request) *view.Error {
	for _, scope := range records(r) {
		if verr := grant.CheckCeiling(r.Cap, scope, r.Profile); verr != nil {
			return verr
		}
	}
	return nil
}

// records is what a parked call names, as grants would name it: each record,
// or the empty one a call about the capability itself names.
func records(r consent.Request) []string {
	if len(r.Scopes) == 0 {
		return []string{""}
	}
	return r.Scopes
}

// noNarrowGrant refuses a --ttl answer whose grants would reach past this
// call's records, or answers nil when a grant for each keeps to them. The
// case is a width the operator was never shown: the question they answered
// named one call and its records.
//
// A record ending in a slash is a folder to the grant matcher
// (grant.IsFolderScope), "https://" included. The agent chose that record,
// a grant on it covers every record under it, those not yet written
// included, and the pending list, the request's page and the answer all
// named it as one record — so an agent parking kv.get on "prod/" came away,
// one --ttl later, with every secret under prod/, and one parking http.get on
// "https://" with the whole web. A folder is grant.allow's decision, where
// the width is said out loud, and the hint is the calls that issue it, one
// for each record the call names.
//
// Several records are not such a case. A grant for each covers them and no
// record the call did not name — a kv.rename's key and where it goes, and
// nothing else under either. They were refused as one, which made every
// parked kv.rename, naming its destination since a rename grant has to
// cover it, a call --ttl could never answer.
func noNarrowGrant(sf plugin.Surface, r consent.Request, ttl string) *view.Error {
	var folders []string
	for _, scope := range r.Scopes {
		// Refused as grant.allow would refuse it, before the hint below hands
		// the operator a grant.allow call that would only be refused in turn.
		if verr := grant.CheckScope(scope); verr != nil {
			return verr
		}
		if grant.IsFolderScope(scope) {
			folders = append(folders, strconv.Quote(scope))
		}
	}
	if len(folders) == 0 {
		return nil
	}
	calls := make([]string, len(r.Scopes))
	for i, scope := range r.Scopes {
		args := []plugin.Arg{
			{Name: "target", Value: r.Cap, Positional: true},
			{Name: "scope", Value: scope, Positional: true},
		}
		if r.Profile != "" {
			args = append(args, plugin.Arg{Name: "profile", Value: r.Profile})
		}
		if r.Agent != "" {
			args = append(args, plugin.Arg{Name: "agent", Value: r.Agent})
		}
		args = append(args, plugin.Arg{Name: "ttl", Value: ttl})
		calls[i] = "`" + sf.Call("grant.allow", args...) + "`"
	}
	ends, issues := "ends", "issues"
	if len(folders) > 1 {
		ends = "end"
	}
	if len(calls) > 1 {
		issues = "issue"
	}
	return view.Errorf("agent.allow.folder",
		"%s %s in a slash, so a grant on it would cover every record under it, not this call's alone",
		andList(folders), ends).
		WithHint(andList(calls) + " " + issues + " that, if every record under it is what you mean")
}

// andList joins items as a sentence lists them: "a", "a and b", "a, b and c".
func andList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// alsoGrant issues exactly the grants the operator would have typed: one for
// each record the call names, or one on the capability for a call naming
// none.
//
// Through internal/grant's own path, so a grant issued from a prompt is
// indistinguishable from one issued deliberately — it appears in
// `rta grant list`, expires the same way, and is bound to the same
// connection. A second mechanism that also authorizes calls would be a
// second thing to audit.
//
// One at a time, as a role's lines are issued: internal/grant writes a grant
// at a time. Should one fail after another stood, the note names those that
// stand beside the refusal, so the answer never claims a grant it did not
// issue nor hides one it did.
func alsoGrant(r consent.Request, ttl, from string, signer *guard.Signer) (string, *view.Error) {
	asked, err := time.ParseDuration(ttl)
	if err != nil {
		return "", view.Errorf("agent.allow.ttl", "%q is not a duration", ttl).
			WithHint("try 15m, 1h — the maximum is 24h")
	}
	if asked <= 0 {
		return "", view.Errorf("agent.allow.ttl", "a grant has to last longer than nothing")
	}
	// The same two ceilings grant.allow's own parseTTL applies, by the same
	// call: rta's own day first, then whatever the team's policy file says.
	// Skipping this made the answer given below a lie by omission —
	// internal/grant.Load() re-applies the team's ceiling on every read
	// regardless of what issued the grant, so a standing grant from here
	// that outran a 15m policy stopped being honoured after 15m, with the
	// "for the next 4h" this function had just told the operator never
	// having said so.
	d, byPolicy, where := grant.ClampTTL(min(asked, grant.MaxTTL))
	now := time.Now()
	// Named as narrowly as each grant is: "note.rm 6 for the next 15m", not
	// "note.rm for the next 15m", which promised the next note.rm too — and
	// that one parked and expired, to an operator who had just been told
	// it would not.
	var issued []string
	note := func() string {
		return fmt.Sprintf("this call, and %s for the next %s", andList(issued), format.Duration(d))
	}
	for _, scope := range records(r) {
		g := grant.Grant{
			Target: r.Cap,
			Scope:  scope,
			// The name and the connection behind it, exactly as `grant allow`
			// records them: the pin is what makes this a grant against a place
			// rather than against a label.
			Profile:    r.Profile,
			ProfilePin: r.Pin,
			// And who asked. Without it, answering a named agent's question with
			// --ttl would issue a grant covering the *unnamed* server — a grant
			// that reads as consent, lists as live, and never authorizes the
			// agent it was granted to.
			Agent:  r.Agent,
			Issued: now,
			// Measured, never assumed. This was FromForm unconditionally once, on
			// the argument that answering a parked request is always a person —
			// but `rta agent allow` runs from any shell, and an agent that parks
			// a call through its own MCP session can answer it the same way it
			// would run `grant allow`. The assumption made this the one issuing
			// path where a self-issued grant got recorded as the *most* trusted
			// origin, while grant.allow was carefully writing `command` for the
			// identical act. Same measurement as grant.allow's, same honesty
			// clause: a pty can still fake `terminal`, and detection of the
			// ordinary case is still the point.
			From:    from,
			Expires: now.Add(d),
			TTL:     ttl,
			Note:    "issued while answering request " + r.ID,
		}
		// Signed after the Grant is fully built, so the signature covers the
		// struct as issued; Issue's own backstop refuses if the guard is on and
		// no signer reached this far.
		if signer != nil {
			grant.SignWith(*signer, &g)
		}
		if verr := grant.Issue(g, true); verr != nil {
			if len(issued) == 0 {
				return "", verr
			}
			return note(), verr
		}
		what := r.Cap
		if scope != "" {
			what += " " + textclean.Record(scope)
		}
		issued = append(issued, what)
	}
	out := note()
	if d < asked {
		// Which ceiling bit, the same distinction grant.allow's own message
		// makes: "capped" with no source sends the operator to change a flag
		// that was never the problem.
		if byPolicy {
			out += fmt.Sprintf("; capped at %s by your team's policy (you asked for %s) — %s",
				format.Duration(d), format.Duration(asked), where)
		} else {
			out += fmt.Sprintf("; capped at the %s maximum (you asked for %s)",
				format.Duration(grant.MaxTTL), format.Duration(asked))
		}
	}
	return out, nil
}

func runDeny(ctx context.Context, req plugin.Request) (view.View, error) {
	id, verr := requestNamed(req, "agent.deny")
	if verr != nil {
		return nil, verr
	}
	if server := strings.TrimSpace(req.String("server")); server != "" {
		return remoteAnswer(ctx, req, server, id, false)
	}
	r, ok := consent.Find(id)
	if !ok {
		return nil, unknownRequest(req.Surface(), id)
	}
	if req.DryRun {
		return view.Text{Body: "would deny " + r.Cap + " for request " + id}, nil
	}
	if err := consent.Decide(id, false, answeredBy(req)); err != nil {
		return nil, view.Errorf("agent.deny.failed", "%v", err)
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "denied", Value: callNamed(r.Cap, r.Scopes)},
		{Key: "the agent", Value: "gets your answer rather than a timeout"},
	}}, nil
}

// unknownRequest is the answer when an id names nothing answerable.
//
// Every capability here that takes an id funnels through it — show, allow and
// deny — so that the one case worth distinguishing is distinguished in one
// place. That case is a request that *is* on disk and does not describe the
// call it is bound to: something rewrote it after rta parked it, which is an
// attempt to have the operator approve one call while reading another. It is
// refused either way, and reporting it as "no request is waiting" would file
// an attack on the consent prompt under housekeeping — the operator would
// shrug at a stale id and never learn that something on their machine is
// writing into rta's data directory.
//
// sf is the surface asking, for the capability its refusal names.
func unknownRequest(sf plugin.Surface, id string) *view.Error {
	q, err := consent.Scan()
	if err == nil {
		for _, bad := range q.Tampered {
			if bad != id {
				continue
			}
			return view.Errorf("agent.request.tampered",
				"request %q does not describe the call it is bound to, so it cannot be answered", id).
				WithHint("something rewrote it after rta parked it — the call it really names was " +
					"never released, and " + sf.CapabilityName("audit.doctor") + " reports this; whatever can write " +
					"rta's data directory is the thing to look at")
		}
	}
	e := view.Errorf("agent.request.unknown", "no request %q is waiting", id)
	if err != nil || len(q.Waiting) == 0 {
		return e.WithHint("nothing is waiting — a parked call expires on its own, and the agent is told")
	}
	ids := make([]string, 0, len(q.Waiting))
	for _, r := range q.Waiting {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	return e.WithHint("waiting right now: " + strings.Join(ids, ", "))
}

// whyLine is the last column: the refusal or error, and — where there was
// one — the admission that calls just before this one went unrecorded.
//
// On the row rather than in a footnote, because the gap has a position: an
// operator reading down this column to find out what happened at 14:32 has
// to be able to see that something at 14:32 is missing.
func whyLine(e agentlog.Entry) string {
	if e.Missed == 0 {
		return e.Reason
	}
	note := "(" + format.CountOf(int(e.Missed), "call") + " before this one could not be recorded)"
	if e.Reason == "" {
		return note
	}
	return e.Reason + " " + note
}

// callNamed is a call as an answer names it: the capability, and each
// record it names as the gate compares it. Joined rather than trimmed: a
// trim took a no-break space off the end of the last record, so the answer
// named the bare record the operator had not answered for.
func callNamed(capID string, records []string) string {
	if len(records) == 0 {
		return capID
	}
	return capID + " " + textclean.Records(records)
}

// argValue is one argument as a person reads it beside the record: a string
// as textclean.Record shows one, since an argument is where the record came
// from, and anything else as it prints.
//
// The arguments are the cleaned copy, though: the bridge drops the
// zero-width, direction and tag characters before the ledger or a parked
// request holds them, so an argument can still read as the bare record. The
// exact one is kept beside it, a ledger row's Records and a request's
// Scopes, and those are what the record column and pair show.
//
// A list element by element, in the brackets Go prints one in, since a list
// is where a capability taking several records names them — net hosts add's
// hostnames — and printed whole it showed a padded one as the bare one. The
// two types are the two a list arrives as: []string from the bridge, []any
// once a request or a ledger line is read back from its JSON.
func argValue(v any) string {
	switch t := v.(type) {
	case string:
		return textclean.Record(t)
	case []string:
		return "[" + textclean.Records(t) + "]"
	case []any:
		shown := make([]string, len(t))
		for i, e := range t {
			shown[i] = argValue(e)
		}
		return "[" + strings.Join(shown, " ") + "]"
	}
	return fmt.Sprintf("%v", v)
}

// argsLine renders arguments for one table cell: compact, ordered, and
// never wider than a person will read.
func argsLine(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+argValue(args[k]))
	}
	return clip(strings.Join(parts, " "))
}

// clip bounds one table cell.
//
// Counted in runes, and cut on a rune boundary. A byte slice through text an
// agent chose ends in a replacement character whenever it is not ASCII — in
// the column a person reads while deciding whether to allow the call, which
// is the last place to put a mystery character.
func clip(line string) string {
	const wide = 60
	if utf8.RuneCountInString(line) <= wide {
		return line
	}
	cut := 0
	for i := range line {
		if cut == wide-1 {
			return line[:i] + "…"
		}
		cut++
	}
	return line
}

// parseSince reads what "since" means to a person: a span back from now, a
// day by its name or its date, or an exact instant.
//
// Several spellings because several questions ask it — "the last two hours"
// while something is going wrong, "today" or "yesterday" when writing it up,
// and an exact timestamp when joining this record against another system's. Refused rather
// than guessed at when it is none of them: a filter that silently matched
// everything would report an empty record as a quiet one.
//
// **A time of day the clock showed twice or never is said, not picked.** One
// it never showed (the night the clocks went forward) is refused, as `time at`
// refuses it: Go moves it an hour on, which drops the calls of the first half
// hour from an audit filter without a word. One it showed twice (the night they
// went back) is read as the earlier of the two, the reading that keeps every
// call the person could have meant, and the second return is the sentence the
// listing carries to say so and to name the other.
func parseSince(raw string) (time.Time, string, *view.Error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, "", nil
	}
	// A day by its name starts at the midnight the clock showed, so the
	// calls of yesterday evening are in "yesterday" and not in a span
	// counted back from this minute. Built from the date and not by
	// subtracting a day's hours, which a clock change makes an hour out.
	switch strings.ToLower(raw) {
	case "today", "yesterday":
		y, m, d := time.Now().Date()
		if strings.EqualFold(raw, "yesterday") {
			d--
		}
		return time.Date(y, m, d, 0, 0, 0, 0, time.Local), "", nil
	}
	if d, err := format.ParseWindow(raw); err == nil {
		if d < 0 {
			d = -d
		}
		return time.Now().Add(-d), "", nil
	}
	if t, ok := timefmt.ParseInstant(raw, time.Local); ok {
		if wall, skipped := timefmt.SkippedWallClock(raw, time.Local); skipped {
			return time.Time{}, "", view.Errorf("agent.log.since",
				"%q never showed on this machine's clock — the clocks went forward over it", raw).
				WithHint("name the instant with an offset (" + timefmt.SkippedExample(wall, t) +
					"), or write a time the clock did show")
		}
		if first, second, twice := timefmt.AmbiguousWallClock(raw, time.Local); twice {
			return first, fmt.Sprintf("%q is a time the clock showed twice that day: listing from the earlier, %s, "+
				"and the later one is %s — add an offset to name the one meant",
				raw, first.Format(time.RFC3339), second.Format(time.RFC3339)), nil
		}
		return t, "", nil
	}
	if field := timefmt.OutOfRange(raw, time.Local); field != "" {
		return time.Time{}, "", view.Errorf("agent.log.since",
			"%q is written as a date, but its %s is out of range", raw, field).WithHint(timefmt.RangeHint)
	}
	return time.Time{}, "", view.Errorf("agent.log.since",
		"%q is not a time this understands", raw).
		WithHint("`today` or `yesterday`, a span back from now (`2h`, `15m`, `3d`), a day (`2026-08-30`), " +
			"or an exact instant (`2026-08-30T14:00:00Z`)")
}

// stampFormat decides how much of an instant a row has to carry.
//
// The same table answers two questions. "What did it touch while I was in
// that meeting" is read on a terminal minutes later, where a date on every
// row is width taken from the arguments column; an archive is read months
// later, where a bare `15:04:05` is a row nobody can place. So the date
// appears exactly when it is load-bearing: when the rows are not all from
// today.
//
// The same shape as the agent column above it, which appears only once a row
// can fill it — a column that says nothing is a column people learn to skip.
//
// Today is now's, passed in rather than read here: a test that makes its rows
// "from today" and this reading the clock again a moment later disagreed on
// which day it was whenever midnight fell between the two readings.
func stampFormat(entries []agentlog.Entry, now time.Time) string {
	const timeOnly, dated = "15:04:05", "2006-01-02 15:04:05"
	today := now.Local().Format("2006-01-02")
	for _, e := range entries {
		if e.At.Local().Format("2006-01-02") != today {
			return dated
		}
	}
	return timeOnly
}

func joinSeqs(seqs []int64) string {
	parts := make([]string, 0, len(seqs))
	for _, s := range seqs {
		parts = append(parts, strconv.FormatInt(s, 10))
	}
	return strings.Join(parts, ", ")
}
