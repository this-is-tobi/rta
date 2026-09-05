// Package sdktest is the conformance suite a plugin has to pass to be a
// correct rta plugin. It is a public package, and one call:
//
//	func TestConformance(t *testing.T) {
//		sdktest.Check(t, myplugin.Plugin())
//	}
//
// The rules it enforces are the ones the host silently assumes and no
// renderer re-checks — an unreachable default, a view that cannot become
// JSON, a --dry-run that is not dry. Each one is here because it has already
// shipped broken at least once. The third is the one that cost most: an
// http write capability whose --dry-run performed the write and then
// described it.
//
// # What it does to your machine
//
// It runs your Read capabilities, once each, with their declared defaults.
// That is a real invocation: whatever they read, they read. A capability that
// should not be run unasked declares NoPreview — the same flag that keeps it
// off the TUI dashboard, for the same reason — and the suite skips it and
// says so.
//
// It never runs a Write or Destructive capability for real. Those are run
// with DryRun set, exactly once, against a temporary RTA_DATA_DIR, and the
// suite's job is to prove the directory did not change.
//
// # What it cannot see
//
// The inertness check watches a directory, so it catches a dry run that
// writes to disk and nothing else. The `http post --dry-run` bug that
// motivates the rule sent an HTTP request; on-box, nothing had changed, and
// this suite would have passed it. A dry run that reaches off the machine is
// still the author's problem to get right.
package sdktest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// reporter is the failure surface the rules write to: testing.TB narrowed to
// the three methods they use. *testing.T satisfies it directly.
//
// It exists so that sdktest's own tests can assert that a broken plugin is
// *caught* — a conformance suite nobody has watched fail is a conformance
// suite that passes everything, and this one has to be trustworthy before
// M2 tells strangers to depend on it.
type reporter interface {
	Helper()
	Errorf(format string, args ...any)
	Logf(format string, args ...any)
}

// Rule names one conformance rule, for Skip.
type Rule string

const (
	// RuleDeclaration: the plugin validates, and its inputs are reachable —
	// no duplicate names, no default the declared type cannot hold.
	RuleDeclaration Rule = "declaration"
	// RuleViews: every view a capability returns survives the JSON encoding
	// every non-terminal surface uses.
	RuleViews Rule = "views"
	// RuleDryRun: a Write or Destructive capability run with --dry-run leaves
	// the data directory byte-identical.
	RuleDryRun Rule = "dry-run"
	// RuleVerbs: the last ID segment is a word the catalogue already uses.
	// Warning only: see checkVerbs for why an error would be wrong here.
	RuleVerbs Rule = "verbs"
	// RuleRedaction: a view that declares a redacted field names one that
	// exists, and a capability that handles secrets declares one at all.
	RuleRedaction Rule = "redaction"
)

// runTimeout bounds one capability. A handler that ignores ctx would
// otherwise hang the whole suite on a plugin author's laptop with no clue as
// to which capability did it.
const runTimeout = 30 * time.Second

// Option configures Check.
type Option func(*config)

type config struct {
	inputs func(dir string) map[string]map[string]any
	skips  map[Rule]map[string]string
}

// WithInputs supplies values for capabilities the suite cannot drive from
// their declared defaults — anything with a required input that has none.
// Without it those capabilities are skipped, which is reported but is not
// coverage: `note.add` is precisely the kind of capability the dry-run rule
// exists for, and it needs a title.
//
// The function is called once, before anything runs, with the temporary
// directory the suite watches (also RTA_DATA_DIR for the duration). Writing a
// fixture there and pointing an input at it is the supported way to test a
// capability that edits a file: a dry run that touches the fixture is then
// caught by the same check that watches the data directory.
//
// The outer key is a capability ID; the inner map is merged over the declared
// defaults, exactly as a surface's collected values are.
func WithInputs(f func(dir string) map[string]map[string]any) Option {
	return func(c *config) { c.inputs = f }
}

// Skip opts one capability out of one rule.
//
// The reason is required and is printed on every run. An opt-out nobody can
// read is how a rule quietly stops applying to a plugin — the suite would go
// green for years while the thing it was checking rotted — so the cost of
// silencing a rule is a line of test output saying which rule, which
// capability, and why.
func Skip(rule Rule, capabilityID, why string) Option {
	return func(c *config) {
		if c.skips[rule] == nil {
			c.skips[rule] = map[string]string{}
		}
		c.skips[rule][capabilityID] = why
	}
}

// Check runs every conformance rule against p and reports failures on t.
//
// It is the whole API. A plugin that passes this is a correct plugin; a
// plugin that does not is broken on at least one of the four surfaces, and
// the message says which rule and which capability.
//
// It sets RTA_DATA_DIR for the duration, so the test that calls it must not
// be parallel.
func Check(t *testing.T, p plugin.Plugin, opts ...Option) {
	t.Helper()

	cfg := config{skips: map[Rule]map[string]string{}}
	for _, o := range opts {
		o(&cfg)
	}

	// A fresh data directory per run, so a capability that legitimately
	// writes on a *real* invocation cannot leave state that makes the next
	// run's dry-run comparison pass for the wrong reason.
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)

	var inputs map[string]map[string]any
	if cfg.inputs != nil {
		inputs = cfg.inputs(dir)
	}

	for _, line := range skipLines(cfg) {
		t.Log(line)
	}

	// A plugin that does not validate is not driven. Validate is what
	// guarantees every capability has a handler at all, so running one anyway
	// turns a legible "nil handler" into a nil-pointer panic and a stack
	// trace, which is a worse answer to the same question.
	if !checkDeclaration(t, p, cfg) {
		return
	}
	checkVerbs(t, p, cfg)

	seen := drive(t, p, cfg, dir, inputs)
	checkViews(t, seen, cfg)
	checkRedaction(t, seen, cfg)
}

// skipLines renders the declared opt-outs in a stable order, because test
// output that reorders itself between runs cannot be diffed.
func skipLines(cfg config) []string {
	var out []string
	for rule, ids := range cfg.skips {
		for id, why := range ids {
			out = append(out, fmt.Sprintf("sdktest: %s: %s skipped — %s", rule, id, why))
		}
	}
	sort.Strings(out)
	return out
}

// observed is one capability invocation and what came back from it.
type observed struct {
	cap  plugin.Capability
	view view.View
	err  error
}

func (c config) skipped(rule Rule, id string) bool {
	_, ok := c.skips[rule][id]
	return ok
}

// drive invokes every capability the suite is allowed to invoke and collects
// the results the view rules then read.
//
// Read capabilities run as themselves. Write and Destructive capabilities run
// with DryRun set and are watched: the directory around them must be
// unchanged afterwards. Nothing runs twice, and nothing Write runs for real —
// a conformance suite that mutates the machine it is checking would be a
// worse bug than any it could find.
func drive(t reporter, p plugin.Plugin, cfg config, dir string, inputs map[string]map[string]any) []observed {
	t.Helper()

	var out []observed
	for _, c := range p.Capabilities {
		mutating := c.Safety == plugin.Write || c.Safety == plugin.Destructive
		rule := RuleViews
		if mutating {
			rule = RuleDryRun
		}
		if cfg.skipped(rule, c.ID) {
			continue
		}

		// NoPreview is the capability saying that running it unasked has a
		// cost — a network round trip, an unbounded scan. This suite is an
		// unasked run, so it is the same answer.
		if c.NoPreview && !mutating {
			t.Logf("sdktest: %s: %s not run — NoPreview (running it unasked has a cost)", rule, c.ID)
			continue
		}

		// No Config and no Profile: a conformance run has no operator, and
		// therefore neither a configuration file nor a named connection. A
		// capability that only works once somebody has written one is a
		// capability this suite should see failing.
		values := plugin.Resolve(c, plugin.Inputs{Caller: inputs[c.ID]})
		if missing := missingRequired(c, values); len(missing) > 0 {
			// A read that cannot be driven is a coverage gap. A *mutating* one
			// is this suite failing at the only job it has ever caught anything
			// doing: RuleDryRun exists because `http post --dry-run` and then
			// `net.send --dry-run` both shipped performing the act they
			// promised to describe, and a capability that is never driven has
			// never been asked. Reported as a pass, that is worse than no rule
			// at all — the external plugins each called Check, each went green,
			// and behind it six handlers wrote to remote systems under
			// --dry-run. Almost every mutating capability names a required
			// bucket, path or key, so "no inputs supplied" is the normal state
			// rather than an edge, which is exactly why it cannot be a log
			// line. An author who genuinely cannot supply one says so with
			// Skip, and that exemption is then on the record.
			if mutating {
				t.Errorf("sdktest: %s: %s was never run — no value for required input %s. "+
					"Supply one with sdktest.WithInputs, or state why not with "+
					"sdktest.Skip(sdktest.RuleDryRun, %q, …): an undriven capability has not been checked.",
					rule, c.ID, strings.Join(missing, ", "), c.ID)
				continue
			}
			t.Logf("sdktest: %s: %s not run — no value for required input %s; supply one with sdktest.WithInputs",
				rule, c.ID, strings.Join(missing, ", "))
			continue
		}

		// SurfaceCLI, not SurfaceUnknown: the suite stands in for a person at
		// a terminal running --dry-run, and a capability that refuses a
		// surface (git's remote reads refuse MCP) must be asked as the surface it
		// is meant to serve.
		req := plugin.NewRequest(values, mutating, false).WithSurface(plugin.SurfaceCLI)

		var before snapshot
		if mutating {
			var err error
			if before, err = snap(dir); err != nil {
				t.Errorf("sdktest: %s: %s: snapshotting %s: %v", RuleDryRun, c.ID, dir, err)
				continue
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
		v, err := c.Run(ctx, req)
		cancel()
		out = append(out, observed{cap: c, view: v, err: err})

		if !mutating {
			continue
		}
		after, serr := snap(dir)
		if serr != nil {
			t.Errorf("sdktest: %s: %s: snapshotting %s: %v", RuleDryRun, c.ID, dir, serr)
			continue
		}
		if diff := before.diff(after); len(diff) > 0 {
			// The whole point of --dry-run is that it is safe to run on
			// something you care about. `http post --dry-run` shipped sending
			// the request and then reporting what "would" happen, which is
			// worse than having no dry run at all.
			t.Errorf("sdktest: %s: %s changed the data directory under --dry-run: %s",
				RuleDryRun, c.ID, strings.Join(diff, ", "))
		}
		// A dry run that returns neither a view nor a coded error has told
		// the caller nothing, which is the same outcome as not implementing
		// one — only harder to notice.
		if v == nil && err == nil {
			t.Errorf("sdktest: %s: %s returned nothing under --dry-run; report what would happen",
				RuleDryRun, c.ID)
		}
	}
	return out
}

// missingRequired names the required inputs that have no value, which is what
// makes a capability undrivable rather than broken.
func missingRequired(c plugin.Capability, values map[string]any) []string {
	var missing []string
	for _, f := range c.Inputs {
		if !f.Required {
			continue
		}
		if _, ok := values[f.Name]; !ok {
			missing = append(missing, f.Name)
		}
	}
	return missing
}

// snapshot maps a path relative to the watched directory onto a digest of
// what is there.
type snapshot map[string]string

const dirMarker = "<dir>"

func snap(root string) (snapshot, error) {
	s := snapshot{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			s[rel] = dirMarker
			return nil
		}
		// Content, not mtime: a handler that rewrites a file with identical
		// bytes has still not changed anything, and a suite that failed on
		// that would teach authors to distrust it.
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		sum := sha256.Sum256(b)
		s[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if os.IsNotExist(err) {
		return s, nil
	}
	return s, err
}

// diff describes what changed, path by path. "the directory differs" would
// send the author looking; naming the file usually ends the search.
func (before snapshot) diff(after snapshot) []string {
	var out []string
	for path, sum := range after {
		old, existed := before[path]
		switch {
		case !existed:
			out = append(out, "created "+path)
		case old != sum:
			out = append(out, "rewrote "+path)
		}
	}
	for path := range before {
		if _, still := after[path]; !still {
			out = append(out, "removed "+path)
		}
	}
	sort.Strings(out)
	return out
}

// walkViews visits v and, for a Sections, every view nested inside it. The
// contract's composability is what makes this necessary: a rule that only
// looked at the top-level view would stop applying the moment a capability
// grew a detail page out of the views it already had.
func walkViews(v view.View, visit func(view.View)) {
	if v == nil {
		return
	}
	visit(v)
	if s, ok := v.(view.Sections); ok {
		for _, item := range s.Items {
			walkViews(item.View, visit)
		}
	}
}

// jsonName reports a stable label for a view inside a result, for messages
// that would otherwise say "a view".
func jsonName(c plugin.Capability, v view.View) string {
	return fmt.Sprintf("%s (%s)", c.ID, view.TypeOf(v))
}

// marshal is the exact encoding the JSON and YAML outputs and the MCP bridge
// all go through, redaction included.
func marshal(v view.View) ([]byte, error) {
	return json.Marshal(view.Envelope{View: view.Redact(v)})
}
