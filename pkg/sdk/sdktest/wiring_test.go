package sdktest

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func serving(v view.View) plugin.Handler {
	return func(context.Context, plugin.Request) (view.View, error) { return v, nil }
}

// The rules each have a test against the function that holds them, and those
// pass whether or not Check calls the function. This runs the whole suite over
// one plugin broken in every way the declaration contract added, so a rule
// nobody wired in is a rule that fails here and not one that quietly stops
// applying.
func TestTheWholeSuiteCatchesAPluginBrokenInEveryWayTheContractAdded(t *testing.T) {
	masked := view.KeyValue{Pairs: []view.Pair{{Key: "token", Value: "s3cret"}}, Redacted: []string{"token"}}
	p := plugin.Plugin{Name: "demo", Summary: "Demo", Capabilities: []plugin.Capability{
		{
			ID: "demo.secret.show", Summary: "Show a secret's facts", Safety: plugin.Read,
			Description: "Shows the facts of one secret and masks its value.", Run: serving(masked),
		},
		{
			ID: "demo.secret.get", Summary: "Reveal a secret", Safety: plugin.Write, NeedsGrant: true,
			Scope: "name", Reveals: true, Description: "Hands back the value itself.",
			Inputs: []plugin.Field{{Name: "name", Type: plugin.String, Required: true, Help: "which secret"}},
			Run:    serving(masked),
		},
		{
			ID: "demo.wait.get", Summary: "Wait for a thing", Safety: plugin.Read, Keywords: []string{"wait"},
			Description: "Run it from a terminal and read the table.",
			Inputs:      []plugin.Field{{Name: "timeout", Type: plugin.Int, Default: 30, Help: "how long"}},
			Run:         serving(view.Text{Body: "ok"}),
		},
	}}
	rec := &recorder{}
	checkAll(rec, p, noConfig(), t.TempDir(), map[string]map[string]any{"demo.secret.get": {"name": "a"}})

	for _, want := range []string{
		"redaction: demo.secret.show masks token but its view says nowhere to read it",
		"redaction: demo.secret.get declares Reveals and marks token Redacted",
		"wording: demo.wait.get description says \"from a terminal\"",
		"wording: demo.wait.get input \"timeout\" is a length of time given as a bare int",
	} {
		if !strings.Contains(rec.errText(), want) {
			t.Errorf("the suite did not report %q:\n%s", want, rec.errText())
		}
	}
	if !strings.Contains(rec.logText(), `keyword "wait" is already a word`) {
		t.Errorf("the suite did not note a keyword the ID already carries:\n%s", rec.logText())
	}
}
