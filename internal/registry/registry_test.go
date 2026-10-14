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

// The namespace an installed plugin holds can be taken for a run, and what
// that plugin was goes with it: its origin and every capability, so what is
// read off the registry afterwards names the plugin that is running. A
// capability the new declaration no longer has must not stay behind, callable
// under a namespace that now means something else.
func TestRegisterOverReplacesAnInstalledPluginWhole(t *testing.T) {
	installed := Origin{Path: "/usr/local/bin/rta-plugin-acme", Digest: "aaaaaaaaaaaaaaaa"}
	built := Origin{Path: "/tmp/rta-plugin-dev", Digest: "bbbbbbbbbbbbbbbb"}
	old := testPlugin("acme")
	old.Capabilities = append(old.Capabilities, plugin.Capability{
		ID: "acme.thing.rm", Summary: "remove a thing", Safety: plugin.Destructive,
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "gone"}, nil },
	})
	r := New()
	if err := r.RegisterFrom(old, installed); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(testPlugin("other")); err != nil {
		t.Fatal(err)
	}

	displaced, err := r.RegisterOver(testPlugin("acme"), built)
	if err != nil {
		t.Fatal(err)
	}
	if displaced != installed {
		t.Errorf("displaced %+v, want the installed copy %+v", displaced, installed)
	}
	if got, _ := r.Origin("acme"); got != built {
		t.Errorf("origin of acme = %+v, want the build that replaced it %+v", got, built)
	}
	if digest, known := r.Artifact("acme"); !known || digest != built.Digest {
		t.Errorf("artifact of acme = %q (%v), want %q", digest, known, built.Digest)
	}
	if _, ok := r.Capability("acme.thing.rm"); ok {
		t.Error("a capability only the displaced plugin declared is still registered")
	}
	if _, ok := r.Capability("acme.thing.list"); !ok {
		t.Error("the replacement's capability is not registered")
	}
	if n := len(r.Plugins()); n != 2 {
		t.Errorf("%d plugins registered, want 2 (acme once, and other untouched)", n)
	}
	if got, _ := r.Origin("other"); got.External() {
		t.Errorf("another namespace's origin changed: %+v", got)
	}
}

// A free namespace is registered as RegisterFrom registers it, and displaces
// nothing.
func TestRegisterOverAFreeNamespaceDisplacesNothing(t *testing.T) {
	r := New()
	displaced, err := r.RegisterOver(testPlugin("acme"), Origin{Path: "/tmp/rta-plugin-dev", Digest: "bbbb"})
	if err != nil {
		t.Fatal(err)
	}
	if displaced.External() {
		t.Errorf("displaced %+v from a namespace nobody held", displaced)
	}
	if got, _ := r.Origin("acme"); got.Digest != "bbbb" {
		t.Errorf("origin of acme = %+v", got)
	}
}

// A built-in's namespace is compiled into the binary the operator chose to
// run, and its configuration is unpinned, so a plugin standing in for one
// would be handed that configuration. It is refused whole, and what was
// registered is as it was.
func TestRegisterOverNeverDisplacesABuiltIn(t *testing.T) {
	r := New()
	if err := r.Register(testPlugin("sys")); err != nil {
		t.Fatal(err)
	}
	_, err := r.RegisterOver(testPlugin("sys"), Origin{Path: "/tmp/rta-plugin-dev", Digest: "bbbb"})
	if !errors.Is(err, ErrBuiltIn) {
		t.Fatalf("err = %v, want ErrBuiltIn", err)
	}
	if got, _ := r.Origin("sys"); got.External() {
		t.Errorf("the built-in's origin became %+v", got)
	}
	if _, ok := r.Capability("sys.thing.list"); !ok {
		t.Error("the built-in lost its capability")
	}
}

// A declaration that fails Validate leaves the namespace as it found it: the
// check is made before anything is dropped, so a plugin that cannot register
// does not empty the namespace on the way to saying so.
func TestRegisterOverAnInvalidDeclarationLeavesTheInstalledPluginServing(t *testing.T) {
	installed := Origin{Path: "/usr/local/bin/rta-plugin-acme", Digest: "aaaa"}
	r := New()
	if err := r.RegisterFrom(testPlugin("acme"), installed); err != nil {
		t.Fatal(err)
	}
	bad := testPlugin("acme")
	bad.Capabilities[0].Safety = "nope"
	if _, err := r.RegisterOver(bad, Origin{Path: "/tmp/rta-plugin-dev", Digest: "bbbb"}); err == nil {
		t.Fatal("an invalid declaration was registered over an installed plugin")
	}
	if got, _ := r.Origin("acme"); got != installed {
		t.Errorf("origin of acme = %+v after a refused declaration, want %+v", got, installed)
	}
	if _, ok := r.Capability("acme.thing.list"); !ok {
		t.Error("the installed plugin's capability was dropped by a declaration that was refused")
	}
}

// A hint says which artifact a pin is compared with, and for the build `rta
// plugin dev` made for one run that is not "the installed one": it exists for
// that run only, so its digest is not one to copy into a file.
func TestAHintNamesTheInstalledArtifactOrTheBuildARunMadeFromADirectory(t *testing.T) {
	installed := Origin{Path: "/usr/local/bin/rta-plugin-pg", Digest: "1a2b3c4d5e6f7890"}
	dev := Origin{Path: "/tmp/rta-plugin-dev-1/rta-plugin-dev", Digest: "fedcba9876543210", Dev: true}

	if got, want := installed.Which("pg"), "the installed one is `pg@1a2b3c4d5e6f`"; got != want {
		t.Errorf("Which = %q, want %q", got, want)
	}
	if got, want := installed.Called("pg"), `the installed "pg"`; got != want {
		t.Errorf("Called = %q, want %q", got, want)
	}
	which := dev.Which("pg")
	for _, want := range []string{"this run uses `pg@fedcba987654`", "built from a directory", "for this run only"} {
		if !strings.Contains(which, want) {
			t.Errorf("Which = %q, want it to say %q", which, want)
		}
	}
	if strings.Contains(which, "installed") {
		t.Errorf("a build made for one run is called installed: %q", which)
	}
	if got, want := dev.Called("pg"), `the "pg" this run built from a directory`; got != want {
		t.Errorf("Called = %q, want %q", got, want)
	}
}
