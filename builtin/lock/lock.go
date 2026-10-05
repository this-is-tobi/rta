// Package lock is the instant no: freeze one principal across the network
// surfaces, effective on its next call, without restarting anything.
//
// It exists for the two revocations nothing else delivered fast. Revoking
// grants takes standing authority back, but a misbehaving agent's bearer
// token still opens every ungated read tool; and a compromised operator
// key stays enrolled until someone edits the roster and restarts, because
// the roster is deliberately read once. A lock lands on the next request.
//
// None of it is reachable over MCP, and for once both directions matter:
// `lock add` from an agent would let it deny service to its operator's
// other agents, and `lock rm` would let it unfreeze itself — the second
// being the same authority-expanding shape that puts `rta lock` on the
// harness deny list `rta audit clients --fix` prints. The local CLI and
// TUI are never gated by locks either way: the person at the terminal is
// the authority locks answer to, not a party they restrain.
package lock

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/term"

	rtagrant "github.com/this-is-tobi/rta/builtin/grant"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/lockdown"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/internal/stdio"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Plugin returns the lock plugin declaration.
func Plugin() plugin.Plugin {
	// A flag with a default, not a positional, and both halves matter. One
	// of the three kinds is the answer almost every time — an incident is an
	// agent misbehaving, and the other two exist for a remote server's
	// bearer identities and roster labels — so `rta lock add claude` should
	// be the whole command. As a leading positional it could not be: the
	// name would land in it. The name is what a person reaching for the
	// emergency brake actually knows, so the name is the positional.
	kindHelp := "what to freeze: agent (the name a server runs as, the default), credential (a bearer " +
		"identity, exactly as the record's credential column shows it), or operator (a roster " +
		"label on the operator channel)"
	return plugin.Plugin{
		Name:    "lock",
		Summary: "Freeze one principal now — the instant path when revoking and restarting are too slow",
		Capabilities: []plugin.Capability{
			{
				ID:      "lock.add",
				Flash:   true,
				Summary: "Lock one principal out of the network surfaces, effective on its next call",
				Description: "Freezes an agent name, a credential, or an operator label: every tool " +
					"call from a locked agent or credential is refused before any other gate — the " +
					"ungated read tier included, which is what `grant.revoke` alone never covered — " +
					"and a locked operator key gets no verb on the operator channel. Running servers " +
					"pick it up on their next request, no restart. Re-locking the same principal " +
					"replaces the row, so a new note or window needs no rm first. With `all`: every " +
					"agent at once, the ones that have not connected yet included — the stop for an " +
					"incident whose agent has not been named yet. Asks for no " +
					"passphrase: a lock only subtracts, and an incident is the wrong moment to " +
					"demand a secret. With `server`: places the lock on a remote rta server, " +
					"as a signed operator call. Never reachable over MCP.",
				Safety:     plugin.Write,
				Idempotent: true,
				Scope:      "name",
				Inputs: []plugin.Field{
					{Name: "kind", Type: plugin.String,
						Default: string(lockdown.KindAgent), Options: kindNames(), Help: kindHelp},
					{Name: "name", Type: plugin.String, Positional: true, Suggest: suggestAgentsToLock,
						Help: "the principal to freeze, exactly as the surface verifies it"},
					{Name: "all", Type: plugin.Bool,
						Help: "freeze every agent, the ones that have not connected yet included"},
					{Name: "note", Type: plugin.String,
						Help: "shown to the locked party on every refusal — write it for them"},
					{Name: "ttl", Type: plugin.String,
						Help: "lift itself after this window (30m, 2h, 1d); omit for a lock that stands until removed"},
					{Name: "server", Type: plugin.String, Local: true, Remote: true,
						Help: "place the lock on this remote server (a name from remotes.yaml)"},
					operatorid.PassphraseField.OnlyWith("server"),
				},
				HumanOnly: true,
				Run:       runAdd,
			},
			{
				ID:      "lock.list",
				Summary: "Who is frozen right now, and why",
				Description: "The live locks: kind, principal, the note the locked party is shown, " +
					"who placed it and until when. With `server`: reads a remote server's " +
					"locks as a signed operator call. Never reachable over MCP — who an operator " +
					"has frozen is incident state, and not an agent's to enumerate.",
				Safety:     plugin.Read,
				Idempotent: true,
				NoPreview:  true, // incident state, empty on a healthy machine — not a dashboard tile
				Inputs: []plugin.Field{
					{Name: "server", Type: plugin.String, Local: true, Remote: true,
						Help: "read this remote server's locks (a name from remotes.yaml)"},
					operatorid.PassphraseField.OnlyWith("server"),
				},
				HumanOnly: true,
				// Lifting a lock is a row action on the list itself, where both halves of
				// the principal are on the row and the surface matches them to lock.rm's
				// inputs by column name; `a` places one from the same screen.
				Actions: []plugin.Action{
					{Key: "a", Label: "lock", Target: "lock.add"},
					{Key: "x", Label: "lift", Target: "lock.rm", Source: plugin.ActionRow},
				},
				Run: runList,
			},
			{
				ID:      "lock.rm",
				Flash:   true,
				Summary: "Lift one lock",
				Description: "Removes a lock so the principal's next call is judged by the ordinary " +
					"gates again. This is the expanding direction — the one an agent must never " +
					"hold, which is why the harness deny list from `audit.clients` with `fix` covers " +
					"`rta lock` and why this is never reachable over MCP. With `all`: lifts the " +
					"lock `lock.add` placed on every agent, and only that one — a lock on one " +
					"agent stands until it is lifted by name. With `server`: " +
					"lifts a lock on a remote server, as a signed operator call.",
				Safety:     plugin.Write,
				Idempotent: true,
				Scope:      "name",
				Inputs: []plugin.Field{
					{Name: "kind", Type: plugin.String,
						Default: string(lockdown.KindAgent), Options: kindNames(), Help: kindHelp},
					{Name: "name", Type: plugin.String, Positional: true,
						Help: "the principal to unfreeze", Suggest: suggestLockedNames},
					{Name: "all", Type: plugin.Bool,
						Help: "lift the lock on every agent; locks on single agents stay"},
					{Name: "server", Type: plugin.String, Local: true, Remote: true,
						Help: "lift the lock on this remote server (a name from remotes.yaml)"},
					operatorid.PassphraseField.OnlyWith("server"),
				},
				HumanOnly: true,
				Run:       runRm,
			},
		},
	}
}

// liftCall is the call that lifts l, as sf, the surface asking, makes it. The
// kind is given only when it is not the default: a hint has to be the call
// somebody can make, and handing `--kind agent` back would teach an input
// nobody needs.
func liftCall(sf plugin.Surface, l lockdown.Lock) string {
	if l.Kind == lockdown.KindAgent && l.Name == lockdown.Everyone {
		return sf.Call("lock.rm", plugin.Arg{Name: "all", Value: true})
	}
	args := []plugin.Arg{{Name: "name", Value: l.Name, Positional: true}}
	if l.Kind != lockdown.KindAgent {
		args = append(args, plugin.Arg{Name: "kind", Value: string(l.Kind)})
	}
	return sf.Call("lock.rm", args...)
}

// kindNames is the closed set a kind may be, so the surfaces offer it and a
// typo is refused with the list rather than accepted as a principal nothing
// will ever match.
func kindNames() []string {
	kinds := lockdown.Kinds()
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, string(k))
	}
	return out
}

// principal is the kind and name a request freezes or lifts.
//
// **`all` is a spelling of one name, and the agent kind's alone.** It stands
// for the row on lockdown.Everyone, so a lock on every agent is placed, listed
// and lifted as any other lock is, and nothing downstream has a second kind of
// thing to learn. A name beside it is refused rather than ignored: "lock
// claude --all" read as the one agent by somebody who meant everyone, or the
// other way round, is the misreading a lock must not allow, and both are cheap
// to retype. A credential or an operator label has no "every" — freezing every
// operator would lock out the one person who can lift it from afar.
func principal(req plugin.Request, ask string) (kind, name string, verr *view.Error) {
	sf := req.Surface()
	kind, name = strings.TrimSpace(req.String("kind")), req.String("name")
	if kind == "" {
		kind = string(lockdown.KindAgent)
	}
	if !req.Bool("all") {
		if strings.TrimSpace(name) == "" {
			return "", "", view.Errorf("core.lock.name", "a lock needs the principal's name").
				WithHint(fmt.Sprintf(ask, sf.InputName("all")))
		}
		return kind, name, nil
	}
	if strings.TrimSpace(name) != "" {
		return "", "", view.Errorf("core.lock.all",
			"%s is every agent, so a name beside it says nothing", sf.InputName("all")).
			WithHint("drop the name to take in every agent, or drop " + sf.InputName("all") + " for just that one")
	}
	if kind != string(lockdown.KindAgent) {
		return "", "", view.Errorf("core.lock.all",
			"%s is every agent — there is no every %s", sf.InputName("all"), kind).
			WithHint("a credential or an operator label is named one at a time")
	}
	return kind, lockdown.Everyone, nil
}

func runAdd(ctx context.Context, req plugin.Request) (view.View, error) {
	kind, name, verr := principal(req, "name the agent to freeze, or %s for every agent")
	if verr != nil {
		return nil, verr
	}
	if server := req.String("server"); server != "" {
		return remoteAdd(ctx, req, server, kind, name)
	}
	// Measured the way a grant's origin is, not assumed: `rta lock add`
	// runs from any shell, and a lock placed by a script reads differently
	// from one a person typed during an incident.
	l, verr := lockdown.Build(kind, name, req.String("note"), req.String("ttl"),
		grant.Origin(req.Surface(), term.IsTerminal(int(stdio.Real().Fd()))))
	if verr != nil {
		return nil, verr
	}
	// Measured before the row is written, as a grant's is: a lock on a name no
	// agent has ever used freezes nobody while the screen says locked, and a
	// slip of the keyboard in an incident is the likeliest way to get one.
	unknown := unknownAgent(l)
	if req.DryRun {
		body := fmt.Sprintf("would lock %s — every call it makes to this "+
			"machine's network surfaces is then refused until `%s`%s", said(l.Kind, l.Name),
			liftCall(req.Surface(), l), windowText(l))
		if unknown != "" {
			body += "\nnote: " + unknown
		}
		return view.Text{Body: body}, nil
	}
	if verr := lockdown.Add(l); verr != nil {
		return nil, verr
	}
	return lockedView(req.Surface(), l, "", unknown), nil
}

// said is a principal as a receipt names it: its kind and its name, or "every
// agent" for the row on all of them, which a bare "*" would leave a person to
// decode.
func said(kind lockdown.Kind, name string) string {
	if kind == lockdown.KindAgent && name == lockdown.Everyone {
		return "every agent"
	}
	return string(kind) + " " + name
}

// unknownAgent is what a lock on an agent name nobody has used says about it,
// with the nearest name this machine has seen, or "" for a name it knows, for
// the row on every agent, and for the other two kinds, whose names the machine
// cannot list.
func unknownAgent(l lockdown.Lock) string {
	if l.Kind != lockdown.KindAgent || l.Name == lockdown.Everyone {
		return ""
	}
	return strings.TrimPrefix(rtagrant.UnknownAgentNote(l.Name), "note: ")
}

// suggestAgentsToLock completes an agent name from the ones this machine has
// seen — connected now, granted, or in the record — for the kind whose names
// it can list. The other two kinds are typed exactly, as the surface that
// verifies them shows them.
func suggestAgentsToLock(ctx context.Context, req plugin.Request) []string {
	if kind := strings.TrimSpace(req.String("kind")); kind != "" && kind != string(lockdown.KindAgent) {
		return nil
	}
	return rtagrant.SuggestAgents(ctx, req)
}

func runList(ctx context.Context, req plugin.Request) (view.View, error) {
	if server := req.String("server"); server != "" {
		return remoteList(ctx, req, server)
	}
	locks, verr := lockdown.Load()
	if verr != nil {
		return nil, verr
	}
	return lockTable(req.Surface(), locks), nil
}

// suggestLockedNames completes from the principals actually frozen right
// now — the set lock.rm can act on — filtered to the kind already chosen,
// the same way suggestHeldScopes narrows by the target already typed:
// offering an agent's name under --kind credential would offer a lift that
// cannot match anything.
func suggestLockedNames(_ context.Context, req plugin.Request) []string {
	locks, verr := lockdown.Load()
	if verr != nil {
		return nil
	}
	kind, kerr := lockdown.CheckKind(req.String("kind"))
	if kerr != nil {
		return nil
	}
	var out []string
	for _, l := range locks {
		if l.Kind == kind {
			out = append(out, l.Name)
		}
	}
	return out
}

func runRm(ctx context.Context, req plugin.Request) (view.View, error) {
	kindRaw, name, verr := principal(req, "name the lock to lift, or %s for the one on every agent")
	if verr != nil {
		return nil, verr
	}
	if server := req.String("server"); server != "" {
		return remoteRm(ctx, req, server, kindRaw, name)
	}
	kind, verr := lockdown.CheckKind(kindRaw)
	if verr != nil {
		return nil, verr
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would lift the lock on %s, if one stands", said(kind, name))}, nil
	}
	removed, verr := lockdown.Remove(kind, name)
	if verr != nil {
		return nil, verr
	}
	return rmView(req.Surface(), kind, name, removed, ""), nil
}

// lockedView confirms one placed lock; where names the server for the
// remote flow, empty locally.
func lockedView(sf plugin.Surface, l lockdown.Lock, where, unknown string) view.View {
	effect := "refused on its next call — running servers need no restart"
	if l.Kind == lockdown.KindAgent && l.Name == lockdown.Everyone {
		effect = "every agent is refused on its next call, the ones running now and any that connects later"
	}
	pairs := []view.Pair{
		{Key: "locked", Value: said(l.Kind, l.Name) + where},
		{Key: "effect", Value: effect},
	}
	if unknown != "" {
		pairs = append(pairs, view.Pair{Key: "note", Value: unknown})
	}
	if l.Note != "" {
		pairs = append(pairs, view.Pair{Key: "shown to them", Value: l.Note})
	}
	if !l.Expires.IsZero() {
		pairs = append(pairs, view.Pair{Key: "lifts itself", Value: l.Expires.Local().Format("2006-01-02 15:04")})
	} else {
		pairs = append(pairs, view.Pair{Key: "until", Value: "somebody runs `" + liftCall(sf, l) + "`"})
	}
	return view.KeyValue{Pairs: pairs}
}

func rmView(sf plugin.Surface, kind lockdown.Kind, name string, removed bool, where string) view.View {
	who := said(kind, name)
	if !removed {
		return view.KeyValue{Pairs: []view.Pair{
			{Key: "nothing to lift", Value: who + where + " was not locked"},
		}}
	}
	pairs := []view.Pair{
		{Key: "unlocked", Value: who + where},
		{Key: "effect", Value: "its next call is judged by the ordinary gates again"},
	}
	if name == lockdown.Everyone && kind == lockdown.KindAgent {
		pairs[1].Value = "every agent's next call is judged by the ordinary gates again"
		if left := agentsStillLocked(where); left != "" {
			pairs = append(pairs, view.Pair{Key: "still locked", Value: left + " — " + sf.CapabilityName("lock.list") + " says why"})
		}
	}
	return view.KeyValue{Pairs: pairs}
}

// agentsStillLocked names the agents with a lock of their own, which lifting
// the one on every agent leaves standing: they were frozen by name, on
// purpose, and an operator who sees the stop lifted should not have to
// wonder whether those were lifted with it. Empty for a remote server, whose
// locks are not read from here.
func agentsStillLocked(where string) string {
	if where != "" {
		return ""
	}
	locks, verr := lockdown.Load()
	if verr != nil {
		return ""
	}
	var names []string
	for _, l := range locks {
		if l.Kind == lockdown.KindAgent {
			names = append(names, l.Name)
		}
	}
	return strings.Join(names, ", ")
}

func lockTable(sf plugin.Surface, locks []lockdown.Lock) view.View {
	rows := make([][]string, 0, len(locks))
	var warnings []view.Error
	for _, l := range locks {
		if l.Kind == lockdown.KindAgent && l.Name == lockdown.Everyone {
			// The row's name is the `*` lock.rm takes, which is what a row
			// action lifts it by; what it means is said beside the table, where
			// a person reading a column of names would not have to know it.
			warnings = append(warnings, view.Error{Code: "lock.everyone", Advisory: true,
				Message: "the row on * freezes every agent, the ones that have not connected yet included",
				Hint:    "`" + liftCall(sf, l) + "` lifts it, and locks on single agents stay"})
		}
		until := "until removed"
		if !l.Expires.IsZero() {
			until = "until " + l.Expires.Local().Format("2006-01-02 15:04")
		}
		rows = append(rows, []string{string(l.Kind), l.Name, l.Note, l.By, until})
	}
	return view.Table{
		// kind and name are spelled exactly as lock.rm's inputs are, so a TUI
		// row action can lift the lock under the cursor without a form: the
		// row carries both halves of the principal, and the surface matches
		// columns to inputs by name.
		Columns: []view.Column{{Name: "kind"}, {Name: "name"}, {Name: "note"}, {Name: "by"}, {Name: "stands"}},
		Rows:    rows,
		// Advisory, for the reason a grant on a replaced plugin is: the rows are
		// all here, and one of them means more than its cells say.
		Warnings: warnings,
		// The table even when nothing is locked, with the sentence beside it
		// for a screen: see view.Table.Empty.
		Empty: "nothing is locked",
	}
}

func windowText(l lockdown.Lock) string {
	if l.Expires.IsZero() {
		return ""
	}
	return ", lifting itself at " + l.Expires.Local().Format("15:04")
}

// remoteClient unlocks the operator key and aims it at one server — the
// builtin/agent remote flow, verb for verb.
func remoteClient(req plugin.Request, server string) (operatorid.Client, *view.Error) {
	base, verr := operatorid.ServerURL(server)
	if verr != nil {
		return operatorid.Client{}, verr
	}
	pass, verr := operatorid.PromptSecret(req, false)
	if verr != nil {
		return operatorid.Client{}, verr
	}
	signer, verr := operatorid.Unlock(pass)
	if verr != nil {
		return operatorid.Client{}, verr
	}
	return operatorid.Client{URL: base, Signer: signer}, nil
}

func remoteAdd(ctx context.Context, req plugin.Request, server, kind, name string) (view.View, error) {
	// Built and thrown away: every refusal Build can produce — the kind,
	// the principal's grammar, an over-long note, a ttl that is not a
	// window — is a typo, and a typo should cost a retype rather than an
	// unlock and a round trip. The server refuses the same things, but only
	// after both. Only the refusals are wanted here; the Lock itself is not
	// sent, because the At and Expires this clock mints are not the ones
	// the server stores. The known cost: a server running a newer rta with
	// a looser grammar has a valid lock refused client-side, which the kind
	// check here already accepted for the kind alone.
	if _, verr := lockdown.Build(kind, name, req.String("note"), req.String("ttl"), ""); verr != nil {
		return nil, verr
	}
	if req.DryRun {
		return view.Text{Body: "would lock " + said(lockdown.Kind(kind), name) + " on " + server +
			" as a signed operator call — the passphrase is asked first"}, nil
	}
	client, verr := remoteClient(req, server)
	if verr != nil {
		return nil, verr
	}
	// The window as Go spells one, so a server that has not learned days yet
	// reads "1d" as the 24h it means; Build above has already refused what is
	// no window.
	ttl := strings.TrimSpace(req.String("ttl"))
	if d, err := format.ParseWindow(ttl); err == nil {
		ttl = d.String()
	}
	spec := operatorid.LockSpec{Kind: kind, Name: name, Note: req.String("note"), TTL: ttl}
	var placed lockdown.Lock
	if verr := client.Call(ctx, operatorid.VerbLockAdd, spec, &placed); verr != nil {
		return nil, verr
	}
	// The principal comes back from what the operator typed, never from the
	// answer — remoteRm already reads only `removed` off the wire and spells
	// the principal itself. A confirmation is the one line that must not be
	// the server's to write: "locked agent claude" over a lock that named
	// something else is a false all-clear with nothing on the operator's
	// screen to check it against. A server that lies here already decides
	// whether any lock is enforced, so no authority changes hands; what is
	// closed is the sentence. Expires stays the server's, because the
	// server is the clock the window runs on — and that is a remaining
	// limit, not a closed one: with no --ttl sent, a server that stored a
	// sixty-second lock can answer with a zero Expires and this prints
	// "until somebody runs rta lock rm". The principal is the operator's
	// word now; the window is still the server's.
	placed.Kind, placed.Name, placed.Note = lockdown.Kind(kind), name, strings.TrimSpace(spec.Note)
	return lockedView(req.Surface(), placed, " on "+server, ""), nil
}

func remoteList(ctx context.Context, req plugin.Request, server string) (view.View, error) {
	if req.DryRun {
		return view.Text{Body: "would read " + server + "'s locks as a signed operator call — " +
			"the passphrase is asked first"}, nil
	}
	client, verr := remoteClient(req, server)
	if verr != nil {
		return nil, verr
	}
	var list operatorid.LockList
	if verr := client.Call(ctx, operatorid.VerbLockList, nil, &list); verr != nil {
		return nil, verr
	}
	return lockTable(req.Surface(), list.Locks), nil
}

func remoteRm(ctx context.Context, req plugin.Request, server, kindRaw, name string) (view.View, error) {
	kind, verr := lockdown.CheckKind(kindRaw)
	if verr != nil {
		return nil, verr
	}
	if req.DryRun {
		return view.Text{Body: "would lift the lock on " + said(kind, name) + " on " + server +
			" as a signed operator call — the passphrase is asked first"}, nil
	}
	client, verr := remoteClient(req, server)
	if verr != nil {
		return nil, verr
	}
	var out operatorid.LockRmOutcome
	if verr := client.Call(ctx, operatorid.VerbLockRm, operatorid.LockRmSpec{Kind: kindRaw, Name: name}, &out); verr != nil {
		return nil, verr
	}
	return rmView(req.Surface(), kind, name, out.Removed, " on "+server), nil
}
