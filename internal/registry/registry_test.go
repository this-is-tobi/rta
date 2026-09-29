package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func testPlugin(name string) plugin.Plugin {
	return plugin.Plugin{
		Name:    name,
		Summary: "test",
		Capabilities: []plugin.Capability{{
			ID:      name + ".thing.list",
			Summary: "list things",
			Safety:  plugin.Read,
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.Text{Body: "ok"}, nil
			},
		}},
	}
}

func TestRegisterAndLookup(t *testing.T) {
	r := New()
	if err := r.Register(testPlugin("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(testPlugin("beta")); err != nil {
		t.Fatal(err)
	}

	if _, ok := r.Capability("alpha.thing.list"); !ok {
		t.Error("capability not found")
	}
	if _, ok := r.Capability("nope.thing.list"); ok {
		t.Error("phantom capability found")
	}
	if got := len(r.Plugins()); got != 2 {
		t.Errorf("Plugins() = %d, want 2", got)
	}
	caps := r.Capabilities()
	if len(caps) != 2 || caps[0].ID != "alpha.thing.list" {
		t.Errorf("Capabilities() not sorted: %v", caps)
	}
}

func TestRegisterRejectsDuplicateNamespace(t *testing.T) {
	r := New()
	if err := r.Register(testPlugin("dup")); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(testPlugin("dup")); err == nil {
		t.Error("duplicate namespace accepted")
	}
}

func TestRegisterRejectsInvalidPlugin(t *testing.T) {
	r := New()
	p := testPlugin("bad")
	p.Capabilities[0].Safety = "nope"
	if err := r.Register(p); err == nil {
		t.Error("invalid plugin accepted")
	}
}

// Every capability the registry hands out runs behind the check of its closed
// sets — on each surface's lookup, and in the plugin's own listing — and the
// declaration the caller registered is left as it was.
func TestARegisteredCapabilityHoldsItsOptions(t *testing.T) {
	p := testPlugin("gamma")
	p.Capabilities[0].Inputs = []plugin.Field{{Name: "mode", Type: plugin.String, Options: []string{"fast", "safe"}}}
	original := p.Capabilities[0].Run
	r := New()
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	c, _ := r.Capability("gamma.thing.list")
	listed := r.Plugins()[0].Capabilities[0]
	for _, got := range []plugin.Capability{c, listed} {
		_, err := got.Run(context.Background(), plugin.NewRequest(map[string]any{"mode": "quick"}, false, false))
		if verr := view.AsError(err, "test"); err == nil || verr.Code != "core.input.option" {
			t.Errorf("an unlisted option ran: %v", err)
		}
		if _, err := got.Run(context.Background(), plugin.NewRequest(map[string]any{"mode": "safe"}, false, false)); err != nil {
			t.Errorf("a listed option was refused: %v", err)
		}
	}
	if _, err := original(context.Background(), plugin.NewRequest(map[string]any{"mode": "quick"}, false, false)); err != nil {
		t.Errorf("the caller's own declaration was changed: %v", err)
	}
}

// A built-in whose error is a *view.Error variable that stayed nil has
// succeeded, on every surface that reaches it through the registry, with and
// without inputs for the guard to hold: each of them read the non-nil error
// interface as a failure, and the CLI turned it into a nil *view.Error to
// render.
func TestARegisteredHandlersNilViewErrorIsNoFailure(t *testing.T) {
	var none *view.Error
	p := testPlugin("delta")
	p.Capabilities[0].Run = func(context.Context, plugin.Request) (view.View, error) {
		return view.Text{Body: "ok"}, none
	}
	p.Capabilities[0].Prefill = func(context.Context, plugin.Request) (map[string]any, error) {
		return map[string]any{"mode": "fast"}, none
	}
	guarded := p.Capabilities[0]
	guarded.ID = "delta.thing.show"
	guarded.Inputs = []plugin.Field{{Name: "mode", Type: plugin.String}}
	p.Capabilities = append(p.Capabilities, guarded)
	r := New()
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"delta.thing.list", "delta.thing.show"} {
		c, _ := r.Capability(id)
		v, err := c.Run(context.Background(), plugin.NewRequest(nil, false, false))
		if err != nil {
			t.Errorf("%s: a call that worked came back as a failure: %#v", id, err)
		}
		if text, ok := v.(view.Text); !ok || text.Body != "ok" {
			t.Errorf("%s: the view did not come back: %#v", id, v)
		}
		if _, err := c.Prefill(context.Background(), plugin.NewRequest(nil, false, false)); err != nil {
			t.Errorf("%s: a prefill that worked came back as a failure: %#v", id, err)
		}
	}
}

// A built-in's failure with no code or no message in it reaches every
// surface coded and worded, as the plugin process's server sends one: an
// empty &view.Error{} was drawn on the CLI, in the TUI and to an agent as
// ERROR with nothing after it. What the handler did say is kept as it came.
func TestARegisteredHandlersEmptyFailureIsCodedAndWorded(t *testing.T) {
	for _, tc := range []struct {
		returned              error
		code, message, prefix string
	}{
		{&view.Error{}, "echo.thing.list.failed", "", "echo.thing.list failed, and its handler gave no message"},
		{&view.Error{Code: "echo.gone"}, "echo.gone", "", "echo.thing.list failed"},
		{&view.Error{Message: "the server went away"}, "echo.thing.list.failed", "the server went away", ""},
		{fmt.Errorf("listing shop: %w", &view.Error{}), "echo.thing.list.failed", "listing shop", ""},
	} {
		p := testPlugin("echo")
		p.Capabilities[0].Run = func(context.Context, plugin.Request) (view.View, error) { return nil, tc.returned }
		p.Capabilities[0].Prefill = func(context.Context, plugin.Request) (map[string]any, error) {
			return nil, tc.returned
		}
		r := New()
		if err := r.Register(p); err != nil {
			t.Fatal(err)
		}
		c, _ := r.Capability("echo.thing.list")
		_, err := c.Run(context.Background(), plugin.NewRequest(nil, false, false))
		var got *view.Error
		if !errors.As(err, &got) || got.Code != tc.code || tc.message != "" && got.Message != tc.message ||
			!strings.HasPrefix(got.Message, tc.prefix) {
			t.Errorf("%#v: the call failed as %#v, want code %s and a message %q", tc.returned, err, tc.code,
				tc.message+tc.prefix)
		}
		_, err = c.Prefill(context.Background(), plugin.NewRequest(nil, false, false))
		if !errors.As(err, &got) || got.Code == "" || got.Message == "" {
			t.Errorf("%#v: the prefill failed as %#v, with no code or no message", tc.returned, err)
		}
	}

	// A failure that says something is handed on as it came, so what a
	// surface asks of it is still there to ask.
	p := testPlugin("echo")
	p.Capabilities[0].Run = func(context.Context, plugin.Request) (view.View, error) { return nil, context.Canceled }
	r := New()
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	c, _ := r.Capability("echo.thing.list")
	if _, err := c.Run(context.Background(), plugin.NewRequest(nil, false, false)); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancellation came back as %#v", err)
	}
}
