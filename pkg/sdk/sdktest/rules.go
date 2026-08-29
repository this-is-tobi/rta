package sdktest

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/internal/render/cli"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// --- (a) declaration ----------------------------------------------------

// checkDeclaration holds the plugin to what the host assumes before it ever
// runs anything: Validate passes, and every declared input is reachable.
//
// Validate is called rather than reimplemented — it is the same function the
// registry runs at load time, so agreeing with it is the point. What this
// adds is the class of declaration Validate accepts and no surface survives:
// an input the CLI cannot register twice, and a default the declared type
// cannot hold.
// It reports whether the declaration is sound enough to run, which is what
// tells Check whether driving the plugin would mean anything.
func checkDeclaration(t reporter, p plugin.Plugin, cfg config) bool {
	t.Helper()

	if err := p.Validate(); err != nil {
		t.Errorf("sdktest: %s: %v", RuleDeclaration, err)
		return false
	}
	for _, c := range p.Capabilities {
		if cfg.skipped(RuleDeclaration, c.ID) {
			continue
		}
		checkInputs(t, c)
	}
	return true
}

func checkInputs(t reporter, c plugin.Capability) {
	t.Helper()

	// Two inputs sharing a name used to pass here unnoticed — the CLI
	// registers flags into one set and pflag panics with "flag redefined",
	// which takes down every rta invocation including the doctor that would
	// have explained it. That check now lives in Capability.validate itself
	// (found by audit), which p.Validate above
	// already calls and already returns false on — so it never reaches this
	// loop for that failure any more, and re-checking it here would be dead
	// code testing a rule Validate itself now owns.
	for _, f := range c.Inputs {
		if f.Default != nil && !holds(f.Type, f.Default) {
			// Nothing rejects this and nothing reports it. Resolve puts the
			// default in the values map, the type switch that would normalise
			// it does not recognise the Go type, and the accessor the handler
			// calls returns the zero value: Field{Type: Int, Default: "30"}
			// means every caller who did not pass --timeout gets 0, with no
			// error anywhere and a working-looking flag in --help.
			t.Errorf("sdktest: %s: %s input %q is %s but its default is %T (%v); the handler will read the zero value",
				RuleDeclaration, c.ID, f.Name, f.Type, f.Default, f.Default)
		}
		if len(f.Options) > 0 && f.Default != nil {
			// A closed set the default is not in makes the capability
			// unrunnable without an explicit flag, while --help advertises
			// the default as if it worked.
			if d := fmt.Sprint(f.Default); !contains(f.Options, d) {
				t.Errorf("sdktest: %s: %s input %q defaults to %q, which is not one of its options %v",
					RuleDeclaration, c.ID, f.Name, d, f.Options)
			}
		}
		// Min/Max used to be checked here — that a bound sits on a numeric
		// type, and that it is not inverted. Both moved into Validate, along
		// with the case neither of them covered (a non-numeric bound), because
		// failing conformance and registering anyway is the wrong split for a
		// rule about a bound that silently does not apply. checkDeclaration
		// reports what Validate returns, so they are still caught here.
	}
}

// holds reports whether a declared type can carry this default. It mirrors
// what Resolve and the Request accessors actually accept, which is wider than
// the obvious Go type: JSON gives float64, YAML gives uint64, and a bare `5`
// on a Float field is a perfectly good default.
func holds(ft plugin.FieldType, v any) bool {
	switch ft {
	case plugin.String, plugin.Text, plugin.Path, plugin.Secret:
		_, ok := v.(string)
		return ok
	case plugin.Bool:
		_, ok := v.(bool)
		return ok
	case plugin.Int:
		return isInt(v)
	case plugin.Float:
		switch v.(type) {
		case float32, float64:
			return true
		}
		return isInt(v)
	case plugin.StringSlice:
		switch v.(type) {
		case []string, []any:
			return true
		}
		return false
	}
	return true
}

func isInt(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}

func toF(v any) float64 {
	var f float64
	_, _ = fmt.Sscanf(fmt.Sprint(v), "%g", &f)
	return f
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
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
			if _, ok := o.err.(*view.Error); !ok {
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
// CSV is only offered a Table. Every other view type is declined by the csv
// renderer by design (it has no rows), and reporting that as a failure would
// mark the whole catalogue non-conformant for doing the right thing.
func checkRendered(t reporter, c plugin.Capability, v view.View) {
	t.Helper()

	formats := []cli.Format{cli.Pretty, cli.Markdown}
	if _, ok := v.(view.Table); ok {
		formats = append(formats, cli.CSV)
	}
	for _, f := range formats {
		if err := renderOnce(v, f); err != nil {
			t.Errorf("sdktest: %s: %s does not render as %s: %v", RuleViews, jsonName(c, v), f, err)
		}
	}
}

// renderOnce renders into nothing and reports what went wrong, panic included.
//
// Width is fixed and colour is off so the verdict is about the view and not
// about the terminal the author happens to be sitting at: a table that only
// fails at 80 columns is still a table that fails, and it must fail on CI too.
func renderOnce(v view.View, f cli.Format) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return cli.Render(io.Discard, v, cli.Options{Format: f, NoColor: true, Width: 80})
}

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
		if contains(vocabulary, verb) {
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
		if !contains(present, name) {
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
		if f.Type == plugin.Secret {
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
