package sdktest

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// --- (a) declaration ----------------------------------------------------

// checkDeclaration holds the plugin to what the host assumes before it ever
// runs anything: Validate passes.
//
// Validate is called rather than reimplemented — it is the same function the
// registry runs at load time, so agreeing with it is the point. It reports
// whether the declaration is sound enough to run, which is what tells Check
// whether driving the plugin would mean anything.
//
// This used to add two rules of its own, for declarations Validate then
// accepted: a default the declared type cannot hold, and a default outside
// the input's Options. Validate holds both now (StatedTypeProblem, and the
// options read the way the guard reads a value), and the copies here had
// drifted from what a run does: a StringSlice default among its options was
// compared as the text "[red]" and reported as none of them, and a bare
// string for a list or a float64 for an Int — each read correctly by its
// accessor — were reported as read as the zero. A conformance failure for a
// plugin that loads and runs is the suite disagreeing with the host, so they
// went, the way the Min/Max rules went before them (see checkBounds).
func checkDeclaration(t reporter, p plugin.Plugin) bool {
	t.Helper()

	if err := p.Validate(); err != nil {
		t.Errorf("sdktest: %s: %v", RuleDeclaration, err)
		return false
	}
	return true
}

// --- (b) survives every renderer ----------------------------------------

// checkViews holds every view a capability returned to all four surfaces.
//
// This was written as the pkg-only half of the rule, on the belief that a
// package under pkg/ cannot import internal/render. That is wrong: Go's
// internal rule is about where the importing package sits in the source tree,
// and pkg/sdk/sdktest sits inside the tree rooted at this module, so the
// import is permitted — and it stays permitted for a stranger who imports
// sdktest from their own module, because the restriction is applied per
// package and not transitively.
//
// It matters because the half that was missing is the half a plugin author
// can most easily break. view.ToMap failing takes a NaN in a chart; the
// pretty renderer indexes rows against columns and measures strings in
// terminal cells, so a Table whose rows disagree with its header, or a Chart
// with an empty series, is an ordinary mistake with a panic at the end of it —
// inside rta, on the author's first run, with the plugin named in the trace
// only if they are lucky.
//
// The cost is that a plugin author's *test* binary links glamour, lipgloss and
// asciigraph. Their plugin binary does not; nothing here is imported by
// pkg/plugin or pkg/view.
func checkViews(t reporter, seen []observed, cfg config) {
	t.Helper()

	for _, o := range seen {
		if cfg.skipped(RuleViews, o.cap.ID) {
			continue
		}
		if o.err != nil {
			// AsError wraps a foreign error under a fallback code, so this
			// never breaks a surface — it costs the caller the stable,
			// namespaced code they were promised to branch on, and replaces
			// it with whichever generic one the host reached for. A warning,
			// because a handler is allowed to return an error from a library
			// it does not control; it should just not hand that error on.
			var coded *view.Error
			if !errors.As(o.err, &coded) {
				t.Logf("sdktest: %s: %s returned a plain error (%v); return view.Errorf so callers get a stable code",
					RuleViews, o.cap.ID, o.err)
			}
			continue
		}
		walkViews(o.view, func(v view.View) { checkOneView(t, o.cap, v) })
	}
}

func checkOneView(t reporter, c plugin.Capability, v view.View) {
	t.Helper()

	if view.TypeOf(v) == "unknown" {
		t.Errorf("sdktest: %s: %s returned %T, which is not a member of the view union", RuleViews, c.ID, v)
		return
	}
	m, err := view.ToMap(v)
	if err != nil {
		// The realistic cause is a NaN or an Inf in a Chart series: encoding
		// fails, and it fails inside `rta ... -o json` and inside the MCP
		// server's per-call goroutine, not in the handler that produced it.
		t.Errorf("sdktest: %s: %s does not encode: %v", RuleViews, jsonName(c, v), err)
		return
	}
	if got := m["type"]; got != view.TypeOf(v) {
		t.Errorf("sdktest: %s: %s encodes with type %v, want %q", RuleViews, jsonName(c, v), got, view.TypeOf(v))
	}
	if _, err := marshal(v); err != nil {
		t.Errorf("sdktest: %s: %s does not survive redaction and encoding: %v", RuleViews, jsonName(c, v), err)
	}
	checkRendered(t, c, v)

	switch t2 := v.(type) {
	case view.Table:
		// Total is the paginated row count, so a Total below the rows on hand
		// is arithmetic no reader can make sense of: "showing 40 of 12".
		if t2.Total > 0 && t2.Total < len(t2.Rows) {
			t.Errorf("sdktest: %s: %s reports Total %d with %d rows", RuleViews, c.ID, t2.Total, len(t2.Rows))
		}
		for _, w := range t2.Warnings {
			if w.Code == "" {
				// Same rule the Sections branch below holds, for the same
				// field: an uncoded warning cannot be told from any other
				// by anything that is not a person reading English.
				t.Logf("sdktest: %s: %s carries an uncoded warning %q", RuleViews, c.ID, w.Message)
			}
		}
		for i, row := range t2.Rows {
			if len(row) > len(t2.Columns) {
				// A cell past the last column has no column name, and a cell
				// with no name cannot be marked Redacted — which makes an
				// over-long row a redaction hole and not only a layout one.
				t.Errorf("sdktest: %s: %s row %d has %d cells for %d columns; the extra cells cannot be named or redacted",
					RuleViews, c.ID, i, len(row), len(t2.Columns))
				break
			}
		}
	case view.Chart:
		if t2.Kind != view.ChartLine && t2.Kind != view.ChartBar {
			t.Errorf("sdktest: %s: %s has chart kind %q, want %q or %q",
				RuleViews, c.ID, t2.Kind, view.ChartLine, view.ChartBar)
		}
	case view.Sections:
		for _, sec := range t2.Items {
			if sec.ID == "" {
				// A warning, not an error: view.Section leaves ID optional
				// on purpose, so a section pays for stability only when it
				// wants it, and plenty of pages never want it. But Key()
				// falling back to the title means a page without ids has
				// made its headings load-bearing — reword one and every
				// script that pulled that section out breaks, silently and
				// at a distance. rta's own catalogue sets one on every
				// section it produces.
				t.Logf("sdktest: %s: %s section %q has no ID, so its only stable handle is its heading; "+
					"set view.Section.ID (or Page.PutAs/AddAs) if anything is meant to address it",
					RuleViews, c.ID, sec.Title)
			}
		}
		for _, w := range t2.Warnings {
			if w.Code == "" {
				// Warnings exist so a partial page says it is partial. An
				// uncoded one cannot be told from any other partial page by
				// anything that is not a person reading English.
				t.Logf("sdktest: %s: %s carries an uncoded warning %q", RuleViews, c.ID, w.Message)
			}
		}
	}
}

// checkRendered holds one view to the renderers a person reads.
//
// A panic is the outcome worth catching and the reason this recovers rather
// than letting the test crash: the failure has to name the capability and the
// format, because "index out of range" ten frames inside a table layout names
// neither. rta itself does not recover — a panic in a renderer is a bug in
// rta or in the plugin, and swallowing it in production would replace a
// crash with silently wrong output — so this is the one place that turns it
// into a sentence.
//
// CSV is offered every view, not only a Table: it writes each shape as rows
// of its own, because a refusal there reached the caller after the handler
// had run — a write that landed reported as one that failed. A view csv
// cannot write is a failure like any other.
//
// Pretty is rendered twice, on a screen and into a pipe, because the two
// draw different things: an empty result's sentence (view.Table.Empty and
// its siblings) is drawn only on a screen, headings in its place into a
// pipe. Rendered into a pipe alone, a plugin's sentence reached the renderer
// for the first time on somebody's terminal.
func checkRendered(t reporter, c plugin.Capability, v view.View) {
	t.Helper()

	for _, r := range []struct {
		name   string
		f      cli.Format
		screen bool
	}{
		{"pretty on a screen", cli.Pretty, true},
		{"pretty into a pipe", cli.Pretty, false},
		{string(cli.Markdown), cli.Markdown, false},
		{string(cli.CSV), cli.CSV, false},
	} {
		if err := renderOnce(v, r.f, r.screen); err != nil {
			t.Errorf("sdktest: %s: %s does not render as %s: %v", RuleViews, jsonName(c, v), r.name, err)
		}
	}
}

// renderOnce renders into nothing and reports what went wrong, panic included.
//
// Width is fixed and colour is off so the verdict is about the view and not
// about the terminal the author happens to be sitting at: a table that only
// fails at 80 columns is still a table that fails, and it must fail on CI too.
func renderOnce(v view.View, f cli.Format, screen bool) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return render(io.Discard, v, cli.Options{Format: f, NoColor: true, Width: 80, Screen: screen})
}

// render is cli.Render, a variable so a test can see what each view was
// rendered with.
var render = cli.Render

// --- (d) verb vocabulary ------------------------------------------------

// checkVerbs reports last ID segments that are not the word rta already uses.
//
// It logs and never errors: a hard error here would fight legitimate
// domain verbs, and the catalogue is full of them — `net.ping`, `codec.b64`,
// `cert.chain`. The signal worth having is narrower than "unknown word", so
// the two cases are reported differently: a word that duplicates one already
// in the vocabulary gets named with its replacement, and everything else is
// listed once per plugin rather than once per capability, because thirty
// individually-correct warnings are indistinguishable from noise.
func checkVerbs(t reporter, p plugin.Plugin, cfg config) {
	t.Helper()

	var novel []string
	for _, c := range p.Capabilities {
		if cfg.skipped(RuleVerbs, c.ID) {
			continue
		}
		words := c.Words()
		verb := words[len(words)-1]
		if slices.Contains(vocabulary, verb) {
			continue
		}
		if std, dup := synonyms[verb]; dup {
			t.Logf("sdktest: %s: %s — rta spells this %q; rename to %s.%s",
				RuleVerbs, c.ID, std, strings.Join(words[:len(words)-1], "."), std)
			continue
		}
		novel = append(novel, verb)
	}
	if len(novel) == 0 {
		return
	}
	sort.Strings(novel)
	novel = dedupe(novel)
	t.Logf("sdktest: %s: %s introduces %d verb(s) the catalogue does not use: %s. "+
		"Fine for a domain word; if one of these means %s, use that instead.",
		RuleVerbs, p.Name, len(novel), strings.Join(novel, ", "), strings.Join(vocabulary, "/"))
}

func dedupe(sorted []string) []string {
	out := sorted[:0]
	var last string
	for i, s := range sorted {
		if i == 0 || s != last {
			out = append(out, s)
		}
		last = s
	}
	return out
}

// --- (e) redaction ------------------------------------------------------

// checkRedaction has an error half and a warning half, and the split is the
// whole design.
//
// The error half is a Redacted entry naming a key or column that does not
// exist. view.Redact matches by name and silently does nothing when the name
// is wrong, so `Redacted: []string{"token"}` above a pair called "Token" is a
// declaration that looks like protection, reviews like protection, and prints
// the secret. There is no case where that is intended.
//
// The warning half is a capability that handles secrets — a Secret input, or
// NeedsGrant because its class understates it — and returns a KeyValue or
// Table with nothing marked at all. That is often correct: `kv.list` names
// keys and shows none of their values, and `kv.status` reports whether a
// store is unlocked. Erroring would make the cheapest way to go green a
// redaction entry that protects nothing, which is strictly worse than the
// gap. So it asks, once, and the author answers by marking a field or by
// calling Skip with the reason — which is then printed on every run.
func checkRedaction(t reporter, seen []observed, cfg config) {
	t.Helper()

	for _, o := range seen {
		if cfg.skipped(RuleRedaction, o.cap.ID) || o.err != nil {
			continue
		}
		nameable, marked := false, false
		walkViews(o.view, func(v view.View) {
			switch t2 := v.(type) {
			case view.KeyValue:
				nameable = true
				marked = marked || len(t2.Redacted) > 0
				keys := make([]string, 0, len(t2.Pairs))
				for _, p := range t2.Pairs {
					keys = append(keys, p.Key)
				}
				reportUnmatched(t, o.cap, "key", t2.Redacted, keys)
			case view.Table:
				nameable = true
				marked = marked || len(t2.Redacted) > 0
				cols := make([]string, 0, len(t2.Columns))
				for _, c := range t2.Columns {
					cols = append(cols, c.Name)
				}
				reportUnmatched(t, o.cap, "column", t2.Redacted, cols)
			}
		})
		if nameable && !marked && handlesSecrets(o.cap) {
			t.Logf("sdktest: %s: %s %s but marks nothing Redacted. If it shows no secret, say so with "+
				"sdktest.Skip(sdktest.RuleRedaction, %q, \"...\").",
				RuleRedaction, o.cap.ID, secretReason(o.cap), o.cap.ID)
		}
	}
}

func reportUnmatched(t reporter, c plugin.Capability, kind string, redacted, present []string) {
	t.Helper()
	for _, name := range redacted {
		if !slices.Contains(present, name) {
			t.Errorf("sdktest: %s: %s redacts %s %q, which the view does not contain (has: %s); "+
				"the value is printed in full",
				RuleRedaction, c.ID, kind, name, strings.Join(present, ", "))
		}
	}
}

func handlesSecrets(c plugin.Capability) bool {
	if c.NeedsGrant {
		return true
	}
	for _, f := range c.Inputs {
		if f.Type.Sensitive() {
			return true
		}
	}
	return false
}

func secretReason(c plugin.Capability) string {
	if c.NeedsGrant {
		return "needs a grant"
	}
	return "takes a secret input"
}

// --- (f) actions ----------------------------------------------------------

// checkActions holds the one declared TUI behaviour Validate cannot see
// without a run: Copy names a column of the Table, or a key of the KeyValue,
// the capability actually returns. Validate sees a well-formed name; a name
// that misses the view is a copy key that hints on the footer and never
// fires, which is exactly the shape a conformance suite exists to catch.
// The rest of the declaration — keys, targets, bare, toggles, live, flash —
// is admitted by Validate, which checkDeclaration already runs.
func checkActions(t reporter, seen []observed, cfg config) {
	t.Helper()

	for _, o := range seen {
		c := o.cap
		if c.Copy == "" || o.err != nil || cfg.skipped(RuleActions, c.ID) {
			continue
		}
		switch v := o.view.(type) {
		case view.Table:
			names := make([]string, 0, len(v.Columns))
			for _, col := range v.Columns {
				names = append(names, col.Name)
			}
			if !slices.Contains(names, c.Copy) {
				t.Errorf("sdktest: %s: %s declares Copy %q, and its table has no such column (columns: %s)",
					RuleActions, c.ID, c.Copy, strings.Join(names, ", "))
			}
		case view.KeyValue:
			found := false
			for _, pair := range v.Pairs {
				found = found || pair.Key == c.Copy
			}
			if !found {
				t.Errorf("sdktest: %s: %s declares Copy %q, and its pairs carry no such key", RuleActions, c.ID, c.Copy)
			}
		default:
			t.Errorf("sdktest: %s: %s declares Copy %q, but returns a %s; c copies a Table's column or a KeyValue's key",
				RuleActions, c.ID, c.Copy, view.TypeOf(v))
		}
	}
}
