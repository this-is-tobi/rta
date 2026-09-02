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
// harness deny list `rta audit agents --fix` prints. The local CLI and
// TUI are never gated by locks either way: the person at the terminal is
// the authority locks answer to, not a party they restrain.
package lock

import (
	"context"
	"fmt"
	"strings"

	"github.com/this-is-tobi/rule-them-all/internal/lockdown"
	operatorid "github.com/this-is-tobi/rule-them-all/internal/operator"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Plugin returns the lock plugin declaration.
func Plugin() plugin.Plugin {
	kindHelp := "what to freeze: agent (a server's --as name), credential (a bearer token's " +
		"identity), or operator (a roster label on the operator channel)"
	return plugin.Plugin{
		Name:    "lock",
		Summary: "Freeze one principal now — the instant path when revoking and restarting are too slow",
		Capabilities: []plugin.Capability{
			{
				ID:      "lock.add",
				Summary: "Lock one principal out of the network surfaces, effective on its next call",
				Description: "Freezes an agent name, a credential, or an operator label: every MCP " +
					"call from a locked agent or credential is refused before any other gate — the " +
					"ungated read tier included, which is what `grant revoke` alone never covered — " +
					"and a locked operator key gets no verb on the operator channel. Running servers " +
					"pick it up on their next request, no restart. Re-locking the same principal " +
					"replaces the row, so a new note or window needs no rm first. Asks for no " +
					"passphrase: a lock only subtracts, and an incident is the wrong moment to " +
					"demand a secret. With --server <name>: places the lock on a remote rta server, " +
					"as a signed operator call. Never reachable over MCP.",
				Safety:     plugin.Write,
				Idempotent: true,
				Scope:      "name",
				Inputs: []plugin.Field{
					{Name: "kind", Type: plugin.String, Positional: true, Required: true, Help: kindHelp},
					{Name: "name", Type: plugin.String, Positional: true, Required: true,
						Help: "the principal to freeze, exactly as the surface verifies it"},
					{Name: "note", Type: plugin.String,
						Help: "shown to the locked party on every refusal — write it for them"},
					{Name: "ttl", Type: plugin.String,
						Help: "auto-lift after this window (30m, 2h); omit for a lock that stands until removed"},
					{Name: "server", Type: plugin.String, Local: true,
						Help: "place the lock on this remote server (a name from remotes.yaml)"},
					operatorid.PassphraseField,
				},
				Run: localOnly(runAdd),
			},
			{
				ID:      "lock.list",
				Summary: "Who is frozen right now, and why",
				Description: "The live locks: kind, principal, the note the locked party is shown, " +
					"who placed it and until when. With --server <name>: reads a remote server's " +
					"locks as a signed operator call. Never reachable over MCP — who an operator " +
					"has frozen is incident state, and not an agent's to enumerate.",
				Safety:     plugin.Read,
				Idempotent: true,
				NoPreview:  true, // incident state, empty on a healthy machine — not a dashboard tile
				Inputs: []plugin.Field{
					{Name: "server", Type: plugin.String, Local: true,
						Help: "read this remote server's locks (a name from remotes.yaml)"},
					operatorid.PassphraseField,
				},
				Run: localOnly(runList),
			},
			{
				ID:      "lock.rm",
				Summary: "Lift one lock",
				Description: "Removes a lock so the principal's next call is judged by the ordinary " +
					"gates again. This is the expanding direction — the one an agent must never " +
					"hold, which is why the harness deny list from `rta audit agents --fix` covers " +
					"`rta lock` and why this is never reachable over MCP. With --server <name>: " +
					"lifts a lock on a remote server, as a signed operator call.",
				Safety:     plugin.Write,
				Idempotent: true,
				Scope:      "name",
				Inputs: []plugin.Field{
					{Name: "kind", Type: plugin.String, Positional: true, Required: true, Help: kindHelp},
					{Name: "name", Type: plugin.String, Positional: true, Required: true,
						Help: "the principal to unfreeze"},
					{Name: "server", Type: plugin.String, Local: true,
						Help: "lift the lock on this remote server (a name from remotes.yaml)"},
					operatorid.PassphraseField,
				},
				Run: localOnly(runRm),
			},
		},
	}
}

// localOnly refuses the MCP surface, in one place — builtin/agent's helper,
// for builtin/agent's reason, with lock.rm sharpening it: an agent that
// could lift a lock would be unfreezing itself.
func localOnly(h plugin.Handler) plugin.Handler {
	return func(ctx context.Context, req plugin.Request) (view.View, error) {
		if req.Surface() == plugin.SurfaceMCP {
			return nil, view.Errorf("lock.human",
				"locks belong to the person at the terminal, not to a caller over MCP").
				WithHint("ask the operator to run `rta lock list`")
		}
		return h(ctx, req)
	}
}

func runAdd(_ context.Context, req plugin.Request) (view.View, error) {
	kind, name := req.String("kind"), req.String("name")
	if server := req.String("server"); server != "" {
		return remoteAdd(req, server, kind, name)
	}
	l, verr := lockdown.Build(kind, name, req.String("note"), req.String("ttl"), "terminal")
	if verr != nil {
		return nil, verr
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would lock %s %s — every call it makes to this "+
			"machine's network surfaces is then refused until `rta lock rm`%s", kind, name, windowText(l))}, nil
	}
	if verr := lockdown.Add(l); verr != nil {
		return nil, verr
	}
	return lockedView(l, ""), nil
}

func runList(_ context.Context, req plugin.Request) (view.View, error) {
	if server := req.String("server"); server != "" {
		return remoteList(req, server)
	}
	locks, verr := lockdown.Load()
	if verr != nil {
		return nil, verr
	}
	return lockTable(locks), nil
}

func runRm(_ context.Context, req plugin.Request) (view.View, error) {
	kindRaw, name := req.String("kind"), req.String("name")
	if server := req.String("server"); server != "" {
		return remoteRm(req, server, kindRaw, name)
	}
	kind, verr := lockdown.CheckKind(kindRaw)
	if verr != nil {
		return nil, verr
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would lift the lock on %s %s, if one stands", kind, name)}, nil
	}
	removed, verr := lockdown.Remove(kind, name)
	if verr != nil {
		return nil, verr
	}
	return rmView(kind, name, removed, ""), nil
}

// lockedView confirms one placed lock; where names the server for the
// remote flow, empty locally.
func lockedView(l lockdown.Lock, where string) view.View {
	pairs := []view.Pair{
		{Key: "locked", Value: string(l.Kind) + " " + l.Name + where},
		{Key: "effect", Value: "refused on its next call — running servers need no restart"},
	}
	if l.Note != "" {
		pairs = append(pairs, view.Pair{Key: "shown to them", Value: l.Note})
	}
	if !l.Expires.IsZero() {
		pairs = append(pairs, view.Pair{Key: "lifts itself", Value: l.Expires.Local().Format("2006-01-02 15:04")})
	} else {
		pairs = append(pairs, view.Pair{Key: "until", Value: "somebody runs `rta lock rm " +
			string(l.Kind) + " " + l.Name + "`"})
	}
	return view.KeyValue{Pairs: pairs}
}

func rmView(kind lockdown.Kind, name string, removed bool, where string) view.View {
	if !removed {
		return view.KeyValue{Pairs: []view.Pair{
			{Key: "nothing to lift", Value: string(kind) + " " + name + where + " was not locked"},
		}}
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "unlocked", Value: string(kind) + " " + name + where},
		{Key: "effect", Value: "its next call is judged by the ordinary gates again"},
	}}
}

func lockTable(locks []lockdown.Lock) view.View {
	if len(locks) == 0 {
		return view.Text{Body: "nothing is locked"}
	}
	rows := make([][]string, 0, len(locks))
	for _, l := range locks {
		until := "until removed"
		if !l.Expires.IsZero() {
			until = "until " + l.Expires.Local().Format("2006-01-02 15:04")
		}
		rows = append(rows, []string{string(l.Kind), l.Name, l.Note, l.By, until})
	}
	return view.Table{
		Columns: []view.Column{{Name: "kind"}, {Name: "principal"}, {Name: "note"}, {Name: "by"}, {Name: "stands"}},
		Rows:    rows,
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

func remoteAdd(req plugin.Request, server, kind, name string) (view.View, error) {
	// The kind is checked before the passphrase is asked: a typo should
	// cost a retype, not an unlock.
	if _, verr := lockdown.CheckKind(kind); verr != nil {
		return nil, verr
	}
	if req.DryRun {
		return view.Text{Body: "would lock " + kind + " " + name + " on " + server +
			" as a signed operator call — the passphrase is asked first"}, nil
	}
	client, verr := remoteClient(req, server)
	if verr != nil {
		return nil, verr
	}
	spec := operatorid.LockSpec{Kind: kind, Name: name,
		Note: req.String("note"), TTL: strings.TrimSpace(req.String("ttl"))}
	var placed lockdown.Lock
	if verr := client.Call(operatorid.VerbLockAdd, spec, &placed); verr != nil {
		return nil, verr
	}
	return lockedView(placed, " on "+server), nil
}

func remoteList(req plugin.Request, server string) (view.View, error) {
	if req.DryRun {
		return view.Text{Body: "would read " + server + "'s locks as a signed operator call — " +
			"the passphrase is asked first"}, nil
	}
	client, verr := remoteClient(req, server)
	if verr != nil {
		return nil, verr
	}
	var list operatorid.LockList
	if verr := client.Call(operatorid.VerbLockList, nil, &list); verr != nil {
		return nil, verr
	}
	return lockTable(list.Locks), nil
}

func remoteRm(req plugin.Request, server, kindRaw, name string) (view.View, error) {
	kind, verr := lockdown.CheckKind(kindRaw)
	if verr != nil {
		return nil, verr
	}
	if req.DryRun {
		return view.Text{Body: "would lift the lock on " + kindRaw + " " + name + " on " + server +
			" as a signed operator call — the passphrase is asked first"}, nil
	}
	client, verr := remoteClient(req, server)
	if verr != nil {
		return nil, verr
	}
	var out operatorid.LockRmOutcome
	if verr := client.Call(operatorid.VerbLockRm, operatorid.LockRmSpec{Kind: kindRaw, Name: name}, &out); verr != nil {
		return nil, verr
	}
	return rmView(kind, name, out.Removed, " on "+server), nil
}
