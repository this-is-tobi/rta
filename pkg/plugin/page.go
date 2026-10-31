package plugin

import (
	"context"
	"fmt"

	"github.com/this-is-tobi/rta/pkg/view"
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
//     host) so a composed section can reach what the caller reached, over
//     the defaults the embedded capability declares, as the host lays them
//     for a call it runs itself.
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

// Add runs a sibling capability and appends its view as a titled section,
// skipping it if the handler fails or returns nothing. values overlay the
// page's inherited inputs.
func (p *Page) Add(title string, c Capability, values map[string]any) *Page {
	return p.AddAs("", title, c, values)
}

// AddAs is Add with an explicit stable section id, for a section a script or
// an agent addresses by name. See view.Section for why the title cannot serve
// as that handle.
//
// c must be a capability every caller of the page could call with nobody's
// leave, and this panics otherwise (embeddable). A Page's embedded calls run
// direct: grant.Reserve and every other check the MCP bridge applies wrap
// the one Capability an MCP call named, not a handler a sibling capability
// calls internally, so nothing here could gate a section even if it wanted
// to.
func (p *Page) AddAs(id, title string, c Capability, values map[string]any) *Page {
	embeddable(fmt.Sprintf("Page.AddAs(%q)", id), c)
	v, err := c.Run(p.ctx, p.section(c, values))
	if err = Failure(err); err != nil {
		// The section is still dropped — that is the whole point of a
		// composed page surviving one bad sensor — but it stops being
		// invisible. Coded, so a machine consumer can tell which part is
		// missing and why without parsing the prose — and worded, since a
		// section handed a sibling's &view.Error{} warned with nothing to
		// say (HandlerFailure).
		what := id
		if what == "" {
			what = title
		}
		// Without the input the sibling's refusal names, which is an input of
		// the sibling's call and not one this page takes.
		p.Warn(HandlerFailure(err, "the "+what+" section", "page.section.failed").WithInput(""))
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

// Run executes a sibling capability with the page's inherited inputs without
// appending anything, for the cases where the caller wants to decide what a
// failure should look like rather than have the section dropped.
//
// c is held to what AddAs holds it to, for the same reason.
func (p *Page) Run(c Capability, values map[string]any) (view.View, error) {
	embeddable("Page.Run", c)
	v, err := c.Run(p.ctx, p.section(c, values))
	return v, Failure(err)
}

// embeddable panics unless c is a capability any caller of a page may call
// with nobody's leave: Read, and neither NeedsGrant, HumanOnly nor Reveals.
//
// **The declaration, never a word restated beside a handler.** The page
// used to take a handler and the safety class its caller said it had, and
// checked the word: plugin.Read passed for a read that needs a grant — a
// destination the caller chooses, as net.probe and the http reads take —
// and for one that reveals a value or answers the person at the terminal
// alone. A free page embedding one handed its answer to a caller the gate
// would have refused, with a record naming the page. Nothing in rta embedded
// one; nothing stopped a page from it either. Taking the Capability makes
// the check read what the gate reads.
//
// A capability with no handler is refused too: it is what a lookup by an ID
// the plugin does not declare returns (Plugin.Capability), and dropped as a
// section that failed it would be a page one heading shorter for a typo.
//
// HostSpecific is not held here, because the page's own capability is not
// known to it: a page offered over a remote transport embeds what describes
// this machine only if its author put it there. Every page in rta describes
// the same machine as its sections.
func embeddable(where string, c Capability) {
	var why string
	switch {
	case c.Run == nil:
		why = "has no handler, which is what a lookup by an ID its plugin does not declare returns"
	case c.Safety != Read:
		why = "is " + string(c.Safety)
	case c.NeedsGrant:
		why = "needs a grant"
	case c.HumanOnly:
		why = "answers the person at the terminal alone"
	case c.Reveals:
		why = "reveals a value another view withholds"
	default:
		return
	}
	panic(fmt.Sprintf("plugin: %s: %q %s — a Page composes a call directly, with none of the checks "+
		"the MCP bridge applies to a named capability, so it embeds only a Read capability that "+
		"needs no grant", where, c.ID, why))
}

// section derives the request an embedded call to c runs with: what c
// declares as defaults, under the page's own inputs, under values.
//
// The host lays a capability's declared defaults before its handler runs
// (Resolve), and a section is a call the host never saw. Without them a
// handler read "" for an input declared with one: a target list whose
// `health` defaults to `any` matched nothing, and a page summing up three
// targets showed an empty section beneath the sum, with no warning, since
// the handler had answered.
func (p *Page) section(c Capability, values map[string]any) Request {
	given := p.base.Values()
	overlay := make(map[string]any, len(values)+len(c.Inputs)+1)
	for _, f := range c.Inputs {
		if _, set := given[f.Name]; !set && f.Default != nil {
			overlay[f.Name] = f.Default
		}
	}
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
