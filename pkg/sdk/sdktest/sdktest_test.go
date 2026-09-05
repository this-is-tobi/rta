package sdktest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// recorder stands in for *testing.T so these tests can assert that a broken
// plugin is caught. Every rule below was written against a plugin that
// violates it; without this, "the suite passes" and "the suite checks
// nothing" are the same observation.
type recorder struct {
	errs []string
	logs []string
}

func (*recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func (r *recorder) Logf(format string, args ...any) {
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

func (r *recorder) errText() string { return strings.Join(r.errs, "\n") }
func (r *recorder) logText() string { return strings.Join(r.logs, "\n") }

func noConfig() config { return config{skips: map[Rule]map[string]string{}} }

// ok is a capability that does everything right, so a test can change exactly
// one thing about it and attribute the failure.
func ok() plugin.Capability {
	return plugin.Capability{
		ID: "demo.item.list", Summary: "list items", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "limit", Type: plugin.Int, Default: 10, Min: 1, Max: 100}},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Table{Columns: []view.Column{{Name: "name"}}, Rows: [][]string{{"a"}}}, nil
		},
	}
}

func TestACorrectPluginPassesEveryRule(t *testing.T) {
	rec := &recorder{}
	p := plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: []plugin.Capability{ok()}}
	if !checkDeclaration(rec, p, noConfig()) {
		t.Fatalf("a correct plugin failed to validate:\n%s", rec.errText())
	}
	checkVerbs(rec, p, noConfig())
	seen := drive(rec, p, noConfig(), t.TempDir(), nil)
	checkViews(rec, seen, noConfig())
	checkRedaction(rec, seen, noConfig())
	if len(rec.errs) > 0 {
		t.Fatalf("a correct plugin was rejected:\n%s", rec.errText())
	}
}

// Two inputs sharing a name reaches pflag, which panics with "flag
// redefined" while the command tree is built — killing every rta
// invocation, doctor included. Caught by Validate itself (via
// checkDeclaration, the same path real plugin loading takes), not by
// checkInputs — see checkInputs' own comment for why that logic moved.
func TestTwoInputsWithOneNameAreRejected(t *testing.T) {
	c := ok()
	c.Inputs = append(c.Inputs, plugin.Field{Name: "limit", Type: plugin.String})
	p := plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: []plugin.Capability{c}}
	rec := &recorder{}
	if checkDeclaration(rec, p, noConfig()) {
		t.Fatal("a plugin with two inputs sharing a name passed declaration checks")
	}
	if !strings.Contains(rec.errText(), `declares input "limit" twice`) {
		t.Errorf("duplicate input accepted: %q", rec.errText())
	}
}

// The failure this pins is silent everywhere else: Resolve does not recognise
// the Go type, leaves the string in place, and req.Int hands the handler 0 —
// while --help advertises a default that never applies.
func TestADefaultTheDeclaredTypeCannotHoldIsRejected(t *testing.T) {
	c := ok()
	c.Inputs = []plugin.Field{{Name: "timeout", Type: plugin.Int, Default: "30"}}
	rec := &recorder{}
	checkInputs(rec, c)
	if !strings.Contains(rec.errText(), "the handler will read the zero value") {
		t.Errorf("mistyped default accepted: %q", rec.errText())
	}
	// The widths an integer legitimately arrives in must keep passing: YAML
	// hands the config loader uint64 and JSON hands it float64, and a suite
	// that rejected those would be wrong about the one place defaults come
	// from other than the author's own source.
	for _, v := range []any{10, int64(10), uint64(10)} {
		rec := &recorder{}
		checkInputs(rec, plugin.Capability{ID: "d.x", Inputs: []plugin.Field{{Name: "n", Type: plugin.Int, Default: v}}})
		if len(rec.errs) > 0 {
			t.Errorf("%T default rejected: %s", v, rec.errText())
		}
	}
}

func TestADefaultOutsideItsOwnOptionsIsRejected(t *testing.T) {
	rec := &recorder{}
	checkInputs(rec, plugin.Capability{ID: "d.x", Inputs: []plugin.Field{
		{Name: "mode", Type: plugin.String, Options: []string{"fast", "slow"}, Default: "quick"},
	}})
	if !strings.Contains(rec.errText(), "not one of its options") {
		t.Errorf("unreachable default accepted: %q", rec.errText())
	}
}

// The Min/Max rules these two tests covered now live in plugin.Validate, and
// are tested there — the reason for the move is in checkBounds. What the
// suite still owes an author is that a Validate failure is *reported* rather
// than silently ending the run, which is what this checks: everything after
// the declaration rule is skipped when it fails, so if the failure were not
// printed the whole conformance run would come back green and empty.
func TestAFailedValidateIsReportedAndStopsTheRun(t *testing.T) {
	rec := &recorder{}
	ok := checkDeclaration(rec, plugin.Plugin{
		Name: "d", Summary: "demo",
		Capabilities: []plugin.Capability{
			{ID: "d.x.y", Summary: "x", Safety: plugin.Read, Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
				Inputs: []plugin.Field{
					{Name: "n", Type: plugin.Int, Min: 100, Max: 10},
				}},
		},
	}, config{skips: map[Rule]map[string]string{}})
	if ok {
		t.Error("checkDeclaration said a plugin Validate rejects is sound enough to drive")
	}
	if !strings.Contains(rec.errText(), "clamps to Max") {
		t.Errorf("the Validate failure was not reported: %q", rec.errText())
	}
}

// A NaN in a series is the realistic way a view stops encoding, and it fails
// inside `-o json` and inside the MCP server's per-call goroutine rather than
// in the handler that produced it.
func TestAViewThatCannotEncodeIsRejected(t *testing.T) {
	nan := func() float64 { var z float64; return z / z }()
	c := ok()
	c.Run = func(context.Context, plugin.Request) (view.View, error) {
		return view.Chart{Kind: view.ChartBar, Series: []view.Series{{Name: "x", Points: []float64{nan}}}}, nil
	}
	rec := &recorder{}
	p := plugin.Plugin{Name: "demo", Capabilities: []plugin.Capability{c}}
	checkViews(rec, drive(rec, p, noConfig(), t.TempDir(), nil), noConfig())
	if !strings.Contains(rec.errText(), "does not encode") {
		t.Errorf("unencodable view accepted: %q", rec.errText())
	}
}

// Sections is the composability seam, so a rule that stopped at the top-level
// view would stop applying the moment a capability grew a detail page out of
// the views it already had.
func TestTheViewRulesReachInsideSections(t *testing.T) {
	c := ok()
	c.Run = func(context.Context, plugin.Request) (view.View, error) {
		return view.Sections{Items: []view.Section{
			{Title: "inner", View: view.Table{Columns: []view.Column{{Name: "a"}}, Total: 1, Rows: [][]string{{"x"}, {"y"}}}},
		}}, nil
	}
	rec := &recorder{}
	p := plugin.Plugin{Name: "demo", Capabilities: []plugin.Capability{c}}
	checkViews(rec, drive(rec, p, noConfig(), t.TempDir(), nil), noConfig())
	if !strings.Contains(rec.errText(), "reports Total 1 with 2 rows") {
		t.Errorf("nested view not checked: %q", rec.errText())
	}
}

// This is the whole reason the redaction rule has an error half: Redact
// matches by name and silently does nothing when the name is wrong, so the
// declaration reviews like protection and prints the secret.
func TestRedactingAKeyTheViewDoesNotContainIsRejected(t *testing.T) {
	c := ok()
	c.Run = func(context.Context, plugin.Request) (view.View, error) {
		return view.KeyValue{
			Pairs:    []view.Pair{{Key: "Token", Value: "s3cret"}},
			Redacted: []string{"token"},
		}, nil
	}
	rec := &recorder{}
	p := plugin.Plugin{Name: "demo", Capabilities: []plugin.Capability{c}}
	checkRedaction(rec, drive(rec, p, noConfig(), t.TempDir(), nil), noConfig())
	if !strings.Contains(rec.errText(), "the value is printed in full") {
		t.Errorf("misspelt redaction accepted: %q", rec.errText())
	}
}

// A capability that handles secrets and marks nothing is asked about, not
// failed: kv.list names keys and shows none of their values, and erroring
// would make the cheapest way to go green a redaction entry that protects
// nothing.
func TestASecretHandlingCapabilityThatMarksNothingWarnsRatherThanFails(t *testing.T) {
	c := ok()
	c.NeedsGrant = true
	c.Run = func(context.Context, plugin.Request) (view.View, error) {
		return view.KeyValue{Pairs: []view.Pair{{Key: "token", Value: "s3cret"}}}, nil
	}
	rec := &recorder{}
	p := plugin.Plugin{Name: "demo", Capabilities: []plugin.Capability{c}}
	checkRedaction(rec, drive(rec, p, noConfig(), t.TempDir(), nil), noConfig())
	if len(rec.errs) > 0 {
		t.Errorf("warning half was made an error: %s", rec.errText())
	}
	if !strings.Contains(rec.logText(), "marks nothing Redacted") {
		t.Errorf("no warning raised: %q", rec.logText())
	}
}

// The rule the http post/put/delete postmortem paid for: a
// dry run reports what would happen, and does not do it first.
func TestADryRunThatWritesIsRejected(t *testing.T) {
	dir := t.TempDir()
	c := plugin.Capability{
		ID: "demo.item.rm", Summary: "remove", Safety: plugin.Destructive,
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			// The bug in its purest form: the handler does the work and then
			// says it would have.
			_ = os.WriteFile(filepath.Join(dir, "items.json"), []byte("{}"), 0o600)
			return view.Text{Body: "would remove item"}, nil
		},
	}
	rec := &recorder{}
	drive(rec, plugin.Plugin{Name: "demo", Capabilities: []plugin.Capability{c}}, noConfig(), dir, nil)
	if !strings.Contains(rec.errText(), "created items.json") {
		t.Errorf("a writing dry run passed: %q", rec.errText())
	}
}

// Rewriting a file with identical bytes has changed nothing, and a suite that
// failed on it would teach authors to distrust it.
func TestADryRunThatRewritesIdenticalBytesIsInert(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "items.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := plugin.Capability{
		ID: "demo.item.rm", Summary: "remove", Safety: plugin.Destructive,
		Run: func(context.Context, plugin.Request) (view.View, error) {
			_ = os.WriteFile(path, []byte("{}"), 0o600)
			return view.Text{Body: "would remove item"}, nil
		},
	}
	rec := &recorder{}
	drive(rec, plugin.Plugin{Name: "demo", Capabilities: []plugin.Capability{c}}, noConfig(), dir, nil)
	if len(rec.errs) > 0 {
		t.Errorf("an inert rewrite was reported as a change: %s", rec.errText())
	}
}

// A skip is not silence, and for a mutating capability it is not a log line
// either. The coverage the suite does not have looked exactly like coverage:
// every external plugin called Check, every one went green, and behind that
// six handlers wrote to remote systems under --dry-run because not one of
// them was ever driven. A read that cannot be driven is a gap worth naming; a
// Write or Destructive one that cannot be driven means the only rule with a
// postmortem behind it did not run, which is a failure.
func TestAnUndrivableCapabilityIsReportedRatherThanPassedOver(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.item.add", Summary: "add", Safety: plugin.Write,
		Inputs: []plugin.Field{{Name: "title", Type: plugin.String, Required: true}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return nil, nil },
	}
	rec := &recorder{}
	drive(rec, plugin.Plugin{Name: "demo", Capabilities: []plugin.Capability{c}}, noConfig(), t.TempDir(), nil)
	if !strings.Contains(rec.errText(), "was never run — no value for required input title") {
		t.Errorf("an undriven mutating capability passed: %q", rec.errText())
	}

	// The read half stays a log: it is missing coverage, not a broken promise,
	// and erroring would demand a live target for every diagnostic in the
	// catalogue — which is the thing conformanceInputs deliberately withholds.
	read := c
	read.ID, read.Safety = "demo.item.show", plugin.Read
	rec = &recorder{}
	drive(rec, plugin.Plugin{Name: "demo", Capabilities: []plugin.Capability{read}}, noConfig(), t.TempDir(), nil)
	if len(rec.errs) > 0 {
		t.Errorf("an undrivable read was an error: %s", rec.errText())
	}
	if !strings.Contains(rec.logText(), "no value for required input title") {
		t.Errorf("skip was silent: %q", rec.logText())
	}
	// And WithInputs is the way out of it: with a title, it runs.
	rec = &recorder{}
	drive(rec, plugin.Plugin{Name: "demo", Capabilities: []plugin.Capability{c}}, noConfig(), t.TempDir(),
		map[string]map[string]any{"demo.item.add": {"title": "x"}})
	if strings.Contains(rec.logText(), "no value for required input") {
		t.Errorf("WithInputs values were not used: %q", rec.logText())
	}
	// It ran, and returning nothing under --dry-run is its own failure.
	if !strings.Contains(rec.errText(), "returned nothing under --dry-run") {
		t.Errorf("a silent dry run passed: %q", rec.errText())
	}
}

// Warnings rather than errors here, and the suite has to keep that choice
// even for the case it is most confident about.
func TestASynonymOfAVocabularyWordWarnsAndNamesTheReplacement(t *testing.T) {
	rec := &recorder{}
	checkVerbs(rec, plugin.Plugin{Name: "demo", Capabilities: []plugin.Capability{
		{ID: "demo.item.remove", Summary: "s", Safety: plugin.Read},
	}}, noConfig())
	if len(rec.errs) > 0 {
		t.Errorf("verb check errored, it must only warn: %s", rec.errText())
	}
	if !strings.Contains(rec.logText(), "demo.item.rm") {
		t.Errorf("replacement not named: %q", rec.logText())
	}
}

// A domain verb is what this refuses to fight, so it is reported in one line
// per plugin rather than one per capability — thirty individually correct
// warnings are indistinguishable from noise.
func TestDomainVerbsAreReportedOncePerPlugin(t *testing.T) {
	rec := &recorder{}
	checkVerbs(rec, plugin.Plugin{Name: "net", Capabilities: []plugin.Capability{
		{ID: "net.ping", Summary: "s", Safety: plugin.Read},
		{ID: "net.trace", Summary: "s", Safety: plugin.Read},
		{ID: "net.dns", Summary: "s", Safety: plugin.Read},
	}}, noConfig())
	if len(rec.logs) != 1 {
		t.Fatalf("want one aggregate warning, got %d:\n%s", len(rec.logs), rec.logText())
	}
	if !strings.Contains(rec.logs[0], "dns, ping, trace") {
		t.Errorf("aggregate does not name the words: %q", rec.logs[0])
	}
}

// A word cannot be both the catalogue's spelling and a mistake for another
// one: the synonym advice would tell an author to rename a capability that
// was already right.
func TestNoVocabularyWordIsAlsoListedAsASynonym(t *testing.T) {
	for word := range synonyms {
		if slices.Contains(vocabulary, word) {
			t.Errorf("%q is both vocabulary and a synonym", word)
		}
	}
	for _, std := range synonyms {
		if !slices.Contains(vocabulary, std) {
			t.Errorf("synonym points at %q, which is not vocabulary", std)
		}
	}
}

func TestVocabularyCannotBeMutatedByACaller(t *testing.T) {
	Vocabulary()[0] = "mutated"
	if Vocabulary()[0] == "mutated" {
		t.Error("Vocabulary hands out the package's own slice")
	}
}
