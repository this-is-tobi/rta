package plugin

import (
	"context"
	"fmt"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Page builds the full-page view a Detailed capability returns when it owns
// the whole screen — a dashboard tile opened, `--detail` on the CLI.
//
// Detail pages are assembled, not written: each section is the view some
// sibling capability already returns, so one fact has one implementation and
// its rendering, JSON shape and MCP payload come along for free
// (view.Sections). Every Detailed capability building its own Sections
// literal was the same four decisions made independently each time — which
// inputs the embedded call inherits, whether a failing section sinks the
// page, whether an empty section is worth a heading, and whether "detail"
// leaks down into the parts. Page answers them once:
//
//   - Embedded calls inherit the page's inputs (an unlock key, a target
//     host) so a composed section can reach what the caller reached.
//   - "detail" is cleared for embedded calls unless asked for explicitly:
//     sections are the parts, and a part that expanded into its own full
//     page would nest a report inside a report.
//   - A section whose handler fails or returns nothing is dropped, and the
//     failure is recorded as a warning. A partial report beats a failed one —
//     a sensor a platform lacks should not cost the reader the six sections
//     that did answer — but a dropped section used to leave nothing behind,
//     so "this platform has no such sensor" and "this sensor errored" both
//     rendered as a heading that simply was not there. When the absence is
//     itself the finding, say so with Put and a view that explains.
type Page struct {
	ctx      context.Context
	base     Request
	items    []view.Section
	warnings []view.Error
}

// NewPage starts a page whose embedded calls inherit base's inputs and
// surface. Pass the request the Detailed handler itself received.
func NewPage(ctx context.Context, base Request) *Page {
	return &Page{ctx: ctx, base: base}
}

// Add runs a sibling handler and appends its view as a titled section,
// skipping it if the handler fails or returns nothing. values overlay the
// page's inherited inputs.
func (p *Page) Add(title string, run Handler, safety Safety, values map[string]any) *Page {
	return p.AddAs("", title, run, safety, values)
}

// AddAs is Add with an explicit stable section id, for a section a script or
// an agent addresses by name. See view.Section for why the title cannot serve
// as that handle.
//
// safety must be Read, and this panics otherwise. A Page's embedded calls
// run direct: grant.Reserve and every other check the MCP bridge applies
// wrap the one Capability an MCP call named, not a handler a sibling
// capability calls internally, so nothing here could gate a Write or
// Destructive section even if it wanted to. Requiring the caller to state
// the safety class, checked immediately rather than trusted, is what turns
// "nothing currently composes anything ungated" into "nothing ever can" —
// found by review: every embedded call in the registry
// today happens to be Read, but nothing stopped a future one from not
// being, and the gap would have been invisible until it was exploited.
func (p *Page) AddAs(id, title string, run Handler, safety Safety, values map[string]any) *Page {
	if run == nil {
		return p
	}
	if safety != Read {
		panic(fmt.Sprintf("plugin: Page.AddAs(%q): embedding a %s handler bypasses grant enforcement — "+
			"Page composes a call directly, with none of the checks the MCP bridge applies to a named "+
			"capability, so only Read handlers may be embedded", id, safety))
	}
	v, err := run(p.ctx, p.section(values))
	if err != nil {
		// The section is still dropped — that is the whole point of a
		// composed page surviving one bad sensor — but it stops being
		// invisible. Coded, so a machine consumer can tell which part is
		// missing and why without parsing the prose.
		p.Warn(view.AsError(err, "page.section.failed"))
		return p
	}
	return p.PutAs(id, title, v)
}

// Put appends a view the page already has in hand — the compact summary the
// caller just built, or an explanation standing in for a section that could
// not be produced. A nil view is ignored.
func (p *Page) Put(title string, v view.View) *Page {
	return p.PutAs("", title, v)
}

// PutAs is Put with an explicit stable section id.
func (p *Page) PutAs(id, title string, v view.View) *Page {
	if v == nil {
		return p
	}
	p.items = append(p.items, view.Section{ID: id, Title: title, View: v})
	return p
}

// Warn records something the page could not produce. Renderers show it
// alongside the sections that did assemble, so a degraded page reads as
// degraded instead of merely shorter.
func (p *Page) Warn(e *view.Error) *Page {
	if e == nil {
		return p
	}
	p.warnings = append(p.warnings, *e)
	return p
}

// Run executes a sibling handler with the page's inherited inputs without
// appending anything, for the cases where the caller wants to decide what a
// failure should look like rather than have the section dropped.
//
// safety must be Read, checked the same way and for the same reason as
// AddAs.
func (p *Page) Run(run Handler, safety Safety, values map[string]any) (view.View, error) {
	if safety != Read {
		panic(fmt.Sprintf("plugin: Page.Run: embedding a %s handler bypasses grant enforcement — "+
			"Page composes a call directly, with none of the checks the MCP bridge applies to a named "+
			"capability, so only Read handlers may be embedded", safety))
	}
	return run(p.ctx, p.section(values))
}

// section derives the request an embedded call runs with.
func (p *Page) section(values map[string]any) Request {
	overlay := make(map[string]any, len(values)+1)
	overlay["detail"] = false
	for k, v := range values {
		overlay[k] = v
	}
	return p.base.With(overlay)
}

// Empty reports whether nothing could be assembled, which is the one case a
// detail page should fail rather than render.
func (p *Page) Empty() bool { return len(p.items) == 0 }

// View returns the assembled page.
func (p *Page) View() view.Sections {
	return view.Sections{Items: p.items, Warnings: p.warnings}
}
