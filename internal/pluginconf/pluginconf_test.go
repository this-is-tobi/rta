package pluginconf

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
)

// origins builds the lookup Resolve takes, from a map of namespace to origin.
func origins(m map[string]registry.Origin) Origin {
	return func(ns string) (registry.Origin, bool) {
		o, ok := m[ns]
		return o, ok
	}
}

const digest = "1a2b3c4d5e6f7890aabbccddeeff00112233445566778899aabbccddeeff0011"

var installed = origins(map[string]registry.Origin{
	"sys": {},                                                     // built-in: no path, no digest
	"pg":  {Path: "/usr/local/bin/rta-plugin-pg", Digest: digest}, // on $PATH
})

func section(t *testing.T, cfg config.Config, ns string) map[string]any {
	t.Helper()
	r, _ := Resolve(cfg, installed)
	return r.For(ns)
}

// The attack this package exists for.
//
// A plugin's namespace is what it says about itself, decoded off the wire,
// and $PATH order decides who gets to say it first — so a binary in any
// directory ahead of the real one can declare Name: "pg" and win the
// registration. It can already impersonate pg.query. What it must not also
// get is the operator's stated values, handed over unprompted on every
// invocation and on the dashboard's five-second refresh.
func TestAnUnpinnedSectionReachesNoInstalledPlugin(t *testing.T) {
	cfg := config.Config{Plugins: map[string]map[string]any{
		"pg": {"host": "db.internal", "port": 5432},
	}}
	if got := section(t, cfg, "pg"); got != nil {
		t.Fatalf("an unpinned section was handed to an installed plugin: %v", got)
	}
	r, problems := Resolve(cfg, installed)
	_ = r
	if len(problems) != 1 || !strings.Contains(problems[0].Hint, "pg@1a2b3c4d5e6f") {
		t.Fatalf("problems = %v, want one naming the installed digest", problems)
	}
}

func TestAPinnedSectionReachesThePluginItNames(t *testing.T) {
	cfg := config.Config{Plugins: map[string]map[string]any{
		"pg@1a2b3c4d5e6f": {"host": "db.internal", "port": 5432},
	}}
	got := section(t, cfg, "pg")
	if got["host"] != "db.internal" {
		t.Fatalf("section = %v, want the stated host", got)
	}
	if _, problems := Resolve(cfg, installed); len(problems) != 0 {
		t.Errorf("a matching pin reported problems: %v", problems)
	}
}

// The ordinary state after upgrading a plugin, and the reason the doctor row
// exists: the digest changed, so the config stops applying, and the operator
// has to be told which one is installed rather than left to look it up.
func TestAStalePinReachesNothingAndSaysWhichIsInstalled(t *testing.T) {
	cfg := config.Config{Plugins: map[string]map[string]any{
		"pg@ffffffffffff": {"host": "db.internal"},
	}}
	if got := section(t, cfg, "pg"); got != nil {
		t.Fatalf("a stale pin still delivered %v", got)
	}
	_, problems := Resolve(cfg, installed)
	if len(problems) != 1 || !strings.Contains(problems[0].Hint, "1a2b3c4d5e6f") {
		t.Fatalf("problems = %v, want one naming the installed digest", problems)
	}
}

// An empty pin is not a prefix of everything: it is a missing decision. The
// same rule destructiveAllowed applies to --allow-destructive.
func TestAnEmptyPinMatchesNothing(t *testing.T) {
	cfg := config.Config{Plugins: map[string]map[string]any{
		"pg@": {"host": "db.internal"},
	}}
	if got := section(t, cfg, "pg"); got != nil {
		t.Fatalf("an empty pin matched: %v", got)
	}
}

// A pin below minPinLen is cheap enough to grind that a hand-written or
// copy-truncated one degrades pinning back into trusting whatever currently
// answers to the name — even though, unlike a stale pin, this one is a
// genuine prefix of the real digest.
func TestAShortPinIsRefusedEvenWhenItMatches(t *testing.T) {
	cfg := config.Config{Plugins: map[string]map[string]any{
		"pg@1a2b3c": {"host": "db.internal"},
	}}
	if got := section(t, cfg, "pg"); got != nil {
		t.Fatalf("a pin below the minimum length matched: %v", got)
	}
	_, problems := Resolve(cfg, installed)
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "too short") {
		t.Fatalf("problems = %v, want one naming the pin as too short", problems)
	}
}

// A built-in has no artifact of its own, so accepting a pin would imply a
// check that is not happening.
func TestABuiltInTakesNoPin(t *testing.T) {
	unpinned := config.Config{Plugins: map[string]map[string]any{"sys": {"top": 5}}}
	if got := section(t, unpinned, "sys"); got["top"] != 5 {
		t.Fatalf("a built-in's own section did not reach it: %v", got)
	}
	pinned := config.Config{Plugins: map[string]map[string]any{"sys@abc": {"top": 5}}}
	if got := section(t, pinned, "sys"); got != nil {
		t.Fatalf("a pinned built-in section was accepted: %v", got)
	}
}

// Refused rather than assumed harmless, the same way the MCP gate refuses an
// unknown namespace: the two things absence can mean are "built in" and
// "never heard of it".
func TestAnUnknownNamespaceGetsNothingAndIsReported(t *testing.T) {
	cfg := config.Config{Plugins: map[string]map[string]any{"nope": {"a": 1}}}
	if got := section(t, cfg, "nope"); got != nil {
		t.Fatalf("an unregistered namespace was handed %v", got)
	}
	_, problems := Resolve(cfg, installed)
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "registered") {
		t.Fatalf("problems = %v, want one saying it is not registered", problems)
	}
}

// Problems are diagnostics, not failures: a config file naming a plugin that
// is not installed on this machine is ordinary — uninstalled, not installed
// yet, one file shared across machines — and refusing to start over it would
// make configuration a liability.
func TestProblemsAreReportedInAStableOrder(t *testing.T) {
	cfg := config.Config{Plugins: map[string]map[string]any{
		"zebra": {"a": 1}, "alpha": {"b": 2}, "middle": {"c": 3},
	}}
	for range 5 {
		_, problems := Resolve(cfg, installed)
		if len(problems) != 3 {
			t.Fatalf("problems = %v, want three", problems)
		}
		if problems[0].Section != "alpha" || problems[2].Section != "zebra" {
			t.Fatalf("order = %s, %s, %s; want alphabetical",
				problems[0].Section, problems[1].Section, problems[2].Section)
		}
	}
}

// A nil resolver is the state on a machine with no config file at all, and
// every surface calls For on whatever it was handed.
func TestANilResolverAnswersNothingRatherThanPanicking(t *testing.T) {
	var r *Resolver
	if got := r.For("pg"); got != nil {
		t.Errorf("a nil resolver answered %v", got)
	}
	if got := r.Check(nil); got != nil {
		t.Errorf("a nil resolver reported %v", got)
	}
}

// RawSection is the one deliberate exception to the pin: an interactive
// config editor fixing a stale pin needs to show the values it would have
// applied, not the declared defaults For hands back for the same namespace.
func TestRawSectionIgnoresThePin(t *testing.T) {
	cfg := config.Config{Plugins: map[string]map[string]any{
		"pg@ffffffffffff": {"host": "db.internal"}, // stale — For returns nil for this
	}}
	// Confirm the premise: For really does refuse it.
	if got := section(t, cfg, "pg"); got != nil {
		t.Fatalf("test fixture is not actually stale: %v", got)
	}
	heading, values, found := RawSection(cfg, "pg")
	if !found {
		t.Fatal("RawSection did not find the stale section")
	}
	if heading != "pg@ffffffffffff" {
		t.Errorf("heading = %q, want the stale one as written", heading)
	}
	if values["host"] != "db.internal" {
		t.Errorf("values = %v, want the stale section's own values", values)
	}
}

func TestRawSectionAnswersFalseForANamespaceNeverWritten(t *testing.T) {
	cfg := config.Config{Plugins: map[string]map[string]any{
		"sys": {"x": 1},
	}}
	if _, _, found := RawSection(cfg, "pg"); found {
		t.Error("RawSection found a section for a namespace nothing named")
	}
}

// Two headings for one namespace — an old pin an operator never cleaned up,
// beside the new one — resolve to the same one twice running rather than to
// whichever a map happened to iterate first.
func TestRawSectionIsDeterministicAcrossTwoHeadingsForOneNamespace(t *testing.T) {
	cfg := config.Config{Plugins: map[string]map[string]any{
		"pg@aaaaaaaaaaaa": {"host": "old"},
		"pg@ffffffffffff": {"host": "new"},
	}}
	for i := 0; i < 20; i++ {
		heading, values, found := RawSection(cfg, "pg")
		if !found || heading != "pg@ffffffffffff" || values["host"] != "new" {
			t.Fatalf("run %d: heading=%q values=%v found=%v, want the lexicographically last "+
				"every time", i, heading, values, found)
		}
	}
}
