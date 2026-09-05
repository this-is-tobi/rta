package plugin_test

import (
	"context"
	"errors"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func titles(s view.Sections) []string {
	out := make([]string, 0, len(s.Items))
	for _, it := range s.Items {
		out = append(out, it.Title)
	}
	return out
}

func text(body string) plugin.Handler {
	return func(context.Context, plugin.Request) (view.View, error) {
		return view.Text{Body: body}, nil
	}
}

func TestPageDropsSectionsThatCannotAnswer(t *testing.T) {
	broken := func(context.Context, plugin.Request) (view.View, error) {
		return nil, errors.New("no sensor on this platform")
	}
	silent := func(context.Context, plugin.Request) (view.View, error) { return nil, nil }

	p := plugin.NewPage(t.Context(), plugin.NewRequest(nil, false, false))
	p.Add("host", text("host"), plugin.Read, nil)
	p.Add("sensors", broken, plugin.Read, nil)
	p.Add("quiet", silent, plugin.Read, nil)
	p.Add("load", text("load"), plugin.Read, nil)

	got := titles(p.View())
	if len(got) != 2 || got[0] != "host" || got[1] != "load" {
		t.Fatalf("a failing section must not sink the page: %v", got)
	}
}

// A composed section must be able to reach whatever the page itself was
// given — the kv detail page cannot list keys with an unlock key its own
// caller passed but the embedded call never sees.
func TestPageSectionsInheritTheCallersInputs(t *testing.T) {
	var seen string
	echo := func(_ context.Context, req plugin.Request) (view.View, error) {
		seen = req.String("identity")
		return view.Text{Body: seen}, nil
	}
	base := plugin.NewRequest(map[string]any{"identity": "~/.ssh/id_ed25519"}, false, false)
	plugin.NewPage(t.Context(), base).Add("keys", echo, plugin.Read, nil)

	if seen != "~/.ssh/id_ed25519" {
		t.Fatalf("section did not inherit the page's inputs: %q", seen)
	}
}

// Sections are the parts of a report. A part that expanded into its own full
// report would nest a detail page inside a detail page — and, for anything
// paginated, quietly multiply the work every refresh does.
func TestPageClearsDetailForTheSectionsItEmbeds(t *testing.T) {
	var detail bool
	echo := func(_ context.Context, req plugin.Request) (view.View, error) {
		detail = req.Bool("detail")
		return view.Text{Body: "x"}, nil
	}
	base := plugin.NewRequest(map[string]any{"detail": true}, false, false)
	plugin.NewPage(t.Context(), base).Add("part", echo, plugin.Read, nil)

	if detail {
		t.Fatal("detail leaked into an embedded section")
	}
}

func TestPageSectionValuesOverrideInheritedOnes(t *testing.T) {
	var got string
	echo := func(_ context.Context, req plugin.Request) (view.View, error) {
		got = req.String("kind")
		return view.Text{Body: got}, nil
	}
	base := plugin.NewRequest(map[string]any{"kind": "note"}, false, false)
	plugin.NewPage(t.Context(), base).Add("keys", echo, plugin.Read, map[string]any{"kind": "cert"})

	if got != "cert" {
		t.Fatalf("kind = %q, want the section's own value", got)
	}
}

// Deriving a request must not reach back into the one the caller is holding:
// a page assembling six sections would otherwise have each one's inputs
// bleed into the next.
func TestWithDoesNotMutateTheOriginalRequest(t *testing.T) {
	base := plugin.NewRequest(map[string]any{"host": "example.com"}, false, false)
	derived := base.With(map[string]any{"host": "other.test", "extra": 1})

	if base.String("host") != "example.com" || base.Int("extra") != 0 {
		t.Fatalf("base request was mutated: host=%q extra=%d", base.String("host"), base.Int("extra"))
	}
	if derived.String("host") != "other.test" || derived.Int("extra") != 1 {
		t.Fatalf("derived request is wrong: host=%q extra=%d", derived.String("host"), derived.Int("extra"))
	}
}

// The surface is a trust signal, not an input: a section that lost it could
// prompt on a surface with nobody at the other end.
func TestWithKeepsTheSurfaceAndFlags(t *testing.T) {
	base := plugin.NewRequest(nil, true, true).WithSurface(plugin.SurfaceMCP)
	derived := base.With(map[string]any{"a": 1})

	if derived.Surface() != plugin.SurfaceMCP || !derived.DryRun || !derived.Yes {
		t.Fatalf("derived lost invocation context: surface=%q dry=%v yes=%v",
			derived.Surface(), derived.DryRun, derived.Yes)
	}
}

// A regression test for a real architectural gap review found: Page
// composes a handler directly, with none of the checks the MCP
// bridge applies to a capability an MCP call actually names — no grant, no
// --allow-write/--allow-destructive gate. Nothing in the registry embeds a
// Write or Destructive handler today, but nothing prevented a future one
// from doing so either, and the gap would have been invisible until
// exploited. AddAs/Add/Run now require the caller to state the safety
// class and panic rather than silently embed anything but Read.
func TestPageRefusesToEmbedAnythingButRead(t *testing.T) {
	write := func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil }
	p := plugin.NewPage(t.Context(), plugin.NewRequest(nil, false, false))

	assertPanics := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: did not panic embedding a non-Read handler", name)
			}
		}()
		f()
	}
	assertPanics("Add", func() { p.Add("x", write, plugin.Write, nil) })
	assertPanics("AddAs", func() { p.AddAs("x", "x", write, plugin.Destructive, nil) })
	assertPanics("Run", func() { _, _ = p.Run(write, plugin.Write, nil) })
}

func TestPagePutIgnoresNilViews(t *testing.T) {
	p := plugin.NewPage(t.Context(), plugin.NewRequest(nil, false, false))
	p.Put("nothing", nil)
	if !p.Empty() {
		t.Fatalf("nil view produced a section: %v", titles(p.View()))
	}
	p.Put("something", view.Text{Body: "x"})
	if p.Empty() {
		t.Fatal("page still reports empty after a real section")
	}
}
