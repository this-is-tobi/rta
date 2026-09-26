package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Every producer holds to that rule, not only a capability's own fields: the
// --profile flag, the profile names the app's commands complete, and what the
// plugin and index commands offer printed a note from the config file or a
// name from a file as it came. A note is the operator's text, but the config
// file is also a file a script or a tool writes, and the terminal acts on an
// escape in it whoever wrote it.
func TestEveryCompletionHoldsToTheSameRule(t *testing.T) {
	esc, bel, rlo := string(rune(0x1b)), string(rune(0x07)), string(rune(0x202e))
	note := "prod" + esc + "]0;owned" + bel + rlo + "db"
	config := "profiles:\n  staging:\n    note: " + strconv.Quote(note) +
		"\n    plugins:\n      db:\n        set:\n          host: " + strconv.Quote("db"+rlo+".internal") + "\n"
	reg := setRegistry(t)
	for _, args := range [][]string{
		{"use", ""},
		{"profile", "show", ""},
		{"db", "status", "--profile", ""},
	} {
		out, errOut, err := runWith(t, reg, config, append([]string{"__complete"}, args...)...)
		if err != nil {
			t.Fatalf("%v: %v %q", args, err, errOut)
		}
		if strings.ContainsAny(out, esc+bel) || strings.Contains(out, rlo) {
			t.Errorf("%v: offered %q, which a terminal acts on", args, out)
		}
		if !strings.Contains(out, "staging\t") {
			t.Errorf("%v: %q does not offer staging, described", args, out)
		}
	}
}

// cobra keeps flag completions in a table nothing can rewrite once they are
// registered, so a tree walk cannot hold them to the rule the way it does
// argument completion. completeFlag is the one registration; a second call
// site would be a producer the rule does not reach.
func TestEveryFlagCompletionIsRegisteredThroughOneRule(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(src), ".RegisterFlagCompletionFunc("); n > 0 {
			calls += n
			if f != "app.go" {
				t.Errorf("%s registers a flag completion itself; use completeFlag", f)
			}
		}
	}
	if calls != 1 {
		t.Errorf("%d calls to RegisterFlagCompletionFunc, want the one inside completeFlag", calls)
	}
}

// Shell completion holds what it offers to the rule the TUI and the list of
// recent values already keep: a value that would display as something other
// than what it is is not offered, and a description is cleaned the way any
// other text on the way to a terminal is.
//
// `rta __complete` printed a Suggest answer as it came, and zsh lists the
// description after the tab verbatim. A note title an agent wrote over MCP,
// holding OSC 0 and a right-to-left override, reached the operator's terminal
// on `rta note show <tab>`: the window title changed, and the line read in an
// order other than the one it is stored in.
func TestCompletionOffersNothingThatDisplaysAsSomethingElse(t *testing.T) {
	rlo := string(rune(0x202e))
	answers := []string{
		"ok\tfine",
		"bad" + rlo + "x\tlooks fine",
		"id\tti\x1b]0;owned\x07tle",
		"marked\t" + rlo + "note",
		"spread\tone\tline\nand another",
		"two\nlines",
		"esc\x1b[2J",
		"notutf8\x9b",
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "demo", Summary: "demo plugin",
		Capabilities: []plugin.Capability{{
			ID: "demo.item.show", Summary: "show an item", Safety: plugin.Read,
			Inputs: []plugin.Field{{Name: "id", Type: plugin.String, Positional: true, Required: true,
				Suggest: func(context.Context, plugin.Request) []string { return answers }}},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "shown"}, nil },
		}},
	}); err != nil {
		t.Fatal(err)
	}

	got := complete(t, reg, "demo", "item", "show", "")
	want := []string{"ok\tfine", "id\ttitle", "spread\tone line and another"}
	offered := map[string]bool{}
	for _, line := range got {
		offered[line] = true
		if strings.ContainsAny(line, "\x1b\x07\n") || strings.Contains(line, rlo) || strings.Contains(line, "\x9b") {
			t.Errorf("offered %q, which a terminal acts on", line)
		}
		value, _, _ := strings.Cut(line, "\t")
		switch value {
		case "bad" + rlo + "x", "two", "lines", "esc\x1b[2J", "notutf8\x9b":
			t.Errorf("offered %q, a value that displays as something other than itself", value)
		}
	}
	for _, w := range want {
		if !offered[w] {
			t.Errorf("completion = %q, want %q among it", got, w)
		}
	}
	marked := false
	for _, line := range got {
		if strings.HasPrefix(line, "marked\t") && strings.HasSuffix(line, "note") && len(line) > len("marked\tnote") {
			marked = true
		}
	}
	if !marked {
		t.Errorf("completion = %q, want the description holding an override spelled out", got)
	}
}
