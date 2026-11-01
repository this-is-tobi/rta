package sdktest

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A Skip that waives nothing fails the suite, as a WithInputs key that reaches
// nothing does. Its line said on every run that a rule was waived, for a
// capability a rename had left behind, while the one it had become was held
// to the rule.
func TestASkipThatWaivesNothingFails(t *testing.T) {
	p := plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: []plugin.Capability{ok()}}
	cfg := noConfig()
	for _, o := range []Option{
		Skip(RuleViews, "demo.greet", "renamed since"),
		Skip(RuleDryRun, "demo", "the plugin is not a capability"),
		Skip(Rule("dryrun"), "demo.item.list", "a rule spelled wrong"),
		Skip(RuleCodes, "demo.item.list", "a code is no capability's"),
		// Each of these waives something, or is not demo's to judge.
		Skip(RuleViews, "demo.item.list", "a real waiver"),
		Skip(RuleSpelling, "demo", "the plugin's own summary"),
		Skip(RuleViews, "other.thing.get", "another plugin's, from options a catalogue shares"),
	} {
		o(&cfg)
	}
	rec := &recorder{}
	checkSkips(rec, p, cfg)
	for _, want := range []string{
		`views: Skip names "demo.greet", which is no capability of demo`,
		`dry-run: Skip names "demo", which is no capability of demo`,
		`Skip names "dryrun" for demo.item.list, which is no rule a Skip waives`,
		`Skip names "codes" for demo.item.list, which is no rule a Skip waives`,
	} {
		if !strings.Contains(rec.errText(), want) {
			t.Errorf("a Skip that waives nothing was accepted, want %q:\n%s", want, rec.errText())
		}
	}
	if len(rec.errs) != 4 {
		t.Errorf("%d failures, want the 4 above alone:\n%s", len(rec.errs), rec.errText())
	}
}
