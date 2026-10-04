package kv

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// With no store, every place that can say what to do next says the same thing:
// `kv set` makes one, locked with a passphrase chosen there. They disagreed —
// status sent a person to `kv set`, doctor to `kv init --generate` — and a
// missing key said only "run the listing", which lists nothing.
func TestEveryNoStoreHintGivesTheSameNextStep(t *testing.T) {
	setup(t)
	ctx := context.Background()
	const step = "`rta kv set db-password` creates one, locked with a passphrase you choose"

	_, err := runGet(ctx, cliReq(map[string]any{"key": "db-password"}))
	if ve := view.AsError(err, "x"); ve.Code != "kv.notfound" || ve.Hint != "no store yet — "+step {
		t.Errorf("a missing key with no store: %+v, want the hint %q", ve, "no store yet — "+step)
	}

	v, err := runStatus(ctx, cliReq(nil))
	if err != nil {
		t.Fatal(err)
	}
	var state string
	for _, p := range v.(view.KeyValue).Pairs {
		if p.Key == "state" {
			state = p.Value
		}
	}
	if want := "no store yet — " + strings.ReplaceAll(step, "db-password", "<key>"); state != want {
		t.Errorf("status = %q, want %q", state, want)
	}
	if got := NoStoreNext(plugin.SurfaceCLI, "db-password"); got != step {
		t.Errorf("NoStoreNext = %q, want %q", got, step)
	}

	_, err = runRekey(ctx, cliReq(map[string]any{"generate": true}))
	if ve := view.AsError(err, "x"); ve.Code != "kv.rekey.nostore" || !strings.Contains(ve.Hint, "creates one, locked with a passphrase you choose") {
		t.Errorf("rekey with no store: %+v", ve)
	}

	_, err = runInit(ctx, cliReq(nil))
	if ve := view.AsError(err, "x"); ve.Code != "kv.init.nokey" || !strings.Contains(ve.Hint, "or skip init: `rta kv set <key>` creates one") {
		t.Errorf("init naming no key: %+v, want the way that needs no init", ve)
	}

	l, err := runList(ctx, cliReq(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := l.(view.Table).Empty; !strings.HasPrefix(got, "No keys stored yet — `rta kv set <key>` creates one") {
		t.Errorf("an empty listing with no store = %q", got)
	}
}

// A person at a terminal is asked for the value, so the call they are shown does
// not put one on the command line, where shell history keeps it; an agent has no
// prompt, and gives its value as an argument.
func TestNoStoreRecipientsShowsACallThatKeepsTheValueOffTheCommandLine(t *testing.T) {
	setup(t)
	for sf, want := range map[plugin.Surface]string{
		plugin.SurfaceCLI: "rta kv set <key> ",
		plugin.SurfaceMCP: `kv_set {"key":"<key>","value":"<value>"}`,
	} {
		v, err := runRecipients(context.Background(), cliReq(nil).WithSurface(sf))
		if err != nil {
			t.Fatal(err)
		}
		got := v.(view.Table).Empty
		if !strings.Contains(got, want) || !strings.Contains(got, "a passphrase you choose, if you never run init") ||
			(sf == plugin.SurfaceCLI && strings.Contains(got, "<value>")) {
			t.Errorf("%s: %q, want it to contain %q (and, at a terminal, no value)", sf, got, want)
		}
	}
}

// Once a store exists the listing is the way on, and an empty store says so in
// the words it always had.
func TestAnEmptyStoreIsNotAMissingOne(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false)
	text(t, runRemove, map[string]any{"key": "k", "purge": true}, false)

	_, err := runGet(context.Background(), cliReq(map[string]any{"key": "k", "passphrase": "correct horse battery staple"}))
	if ve := view.AsError(err, "x"); ve.Hint != "`rta kv list` lists every key" {
		t.Errorf("hint = %q", ve.Hint)
	}
	if got := emptyList(plugin.SurfaceCLI, 0, "", "", ""); got != "No keys stored yet — `rta kv set` adds one" {
		t.Errorf("an empty store: %q", got)
	}
}

// Over MCP the agent is told what it can call, never a command line or a lock to
// choose: which lock the store gets is the operator's.
func TestNoStoreNextToAnAgentNamesOnlyItsTool(t *testing.T) {
	got := NoStoreNext(plugin.SurfaceMCP, "db")
	if got != "the `kv_set` tool creates it the first time it runs" {
		t.Errorf("NoStoreNext over MCP = %q", got)
	}
	if tui := NoStoreNext(plugin.SurfaceTUI, "db"); strings.Contains(tui, "rta ") || !strings.Contains(tui, "kv.set key=db") {
		t.Errorf("NoStoreNext in the TUI = %q, want its own names", tui)
	}
}
