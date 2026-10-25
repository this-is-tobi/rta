package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A refusal of one input names it in the formats a program reads, so a script
// learns the input to change without parsing the sentence; and a name the
// capability does not declare, which a plugin's handler can write, is left out
// rather than handed on as an input no flag answers to.
func TestARefusalOfOneInputNamesItInJSON(t *testing.T) {
	_, errOut, err := run(t, testRegistry(t), "demo", "item", "pick", "", "-o", "json")
	if err == nil {
		t.Fatal("an empty required argument ran")
	}
	if got := jsonError(t, errOut); got["code"] != "core.input.missing" || got["input"] != "name" {
		t.Errorf("the refusal = %v, want core.input.missing naming name", got)
	}

	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "zz", Summary: "zz", Capabilities: []plugin.Capability{{
		ID: "zz.thing.show", Summary: "show a thing", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return nil, view.Errorf("zz.nothing", "no such thing").WithInput("ghost")
		},
	}}}); err != nil {
		t.Fatal(err)
	}
	_, errOut, err = run(t, reg, "zz", "thing", "show", "-o", "json")
	if err == nil {
		t.Fatal("a failing capability succeeded")
	}
	if got := jsonError(t, errOut); got["code"] != "zz.nothing" || got["input"] != nil {
		t.Errorf("the failure = %v, want zz.nothing naming no input", got)
	}
}

// A command line refused while it is parsed names the input it is about as
// the checks made once it has parsed do: a flag's value its type cannot hold,
// an option flag given no value, a required argument or a required flag left
// out. Two required flags left out, and a flag the command does not have, name
// none.
func TestACommandLineRefusedOverOneInputNamesIt(t *testing.T) {
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "zz", Summary: "zz", Capabilities: []plugin.Capability{{
		ID: "zz.thing.pick", Summary: "pick a thing", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "name", Type: plugin.String, Positional: true, Required: true, Help: "which thing"},
			{Name: "count", Type: plugin.Int, Min: 1, Max: 9, Default: 1, Help: "how many"},
			{Name: "mode", Type: plugin.String, Options: []string{"fast", "slow"}, Help: "how"},
			{Name: "target", Type: plugin.String, Required: true, Help: "where"},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "picked"}, nil },
	}, {
		ID: "zz.thing.pair", Summary: "pair two things", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "left", Type: plugin.String, Required: true, Help: "one"},
			{Name: "right", Type: plugin.String, Required: true, Help: "the other"},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "paired"}, nil },
	}}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args  []string
		input string
	}{
		{[]string{"zz", "thing", "pick", "x", "--target", "t", "--count", "abc"}, "count"},
		{[]string{"zz", "thing", "pick", "x", "--target", "t", "--mode"}, "mode"},
		{[]string{"zz", "thing", "pick", "--target", "t"}, "name"},
		{[]string{"zz", "thing", "pick", "x"}, "target"},
		{[]string{"zz", "thing", "pair"}, ""},
		{[]string{"zz", "thing", "pick", "x", "--target", "t", "--nope"}, ""},
	} {
		// Drawn by Execute, as every error the tree returns is, in the format
		// asked for: the error is what it draws. The format first, since a
		// flag left without its value takes the word after it.
		_, _, err := run(t, reg, append([]string{"-o", "json"}, tc.args...)...)
		var got *view.Error
		if !errors.As(err, &got) || got.Code != CodeUsage || got.Input != tc.input {
			t.Errorf("%v: the refusal = %#v, want %s naming %q", tc.args, err, CodeUsage, tc.input)
		}
	}
}

func jsonError(t *testing.T, stderr string) map[string]any {
	t.Helper()
	at := strings.Index(stderr, "{")
	if at < 0 {
		t.Fatalf("stderr holds no JSON error:\n%s", stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stderr[at:]), &got); err != nil {
		t.Fatalf("stderr is not one JSON error: %v\n%s", err, stderr)
	}
	return got
}
