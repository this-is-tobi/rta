package profile

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// **A connection that states the value and fences it still refuses a call that
// does not name it, and the refusal says why.** "is blank, and profile reaches
// only: apps" beside a profile that visibly states `namespace: apps` reads as the
// tool contradicting the file; what happened is that the input is a scope, which
// a call names whatever a profile says. Told to whoever can act on it, on each
// surface in its own words for the input, with the values the call may name.
func TestAFenceRefusalSaysTheConnectionStatesWhatACallHasToName(t *testing.T) {
	reg := clusterRegistry(t)
	c := mustCap(t, reg, "cluster.pod.list")
	conn := config.Connection{
		Set:  map[string]any{"namespace": "apps"},
		Only: map[string]any{"namespace": []any{"apps", "apps-jobs"}},
	}
	for _, tc := range []struct {
		surface plugin.Surface
		input   string
	}{
		{plugin.SurfaceCLI, "--namespace"},
		{plugin.SurfaceTUI, "the namespace box"},
	} {
		verr := Fenced("staging", conn, c, plugin.Inputs{}, noEnv, tc.surface)
		if verr == nil || verr.Code != "core.profile.fenced" {
			t.Fatalf("%v: Fenced = %v", tc.surface, verr)
		}
		want := tc.input + ` is not given: profile "staging" states namespace: apps, but a call has to name it`
		if verr.Message != want {
			t.Errorf("%v: message = %q\nwant      %q", tc.surface, verr.Message, want)
		}
		for _, part := range []string{"cluster.pod.list takes it as its scope, which a profile never fills",
			"give it one of apps, apps-jobs", "rta profile set staging --plugin cluster --only namespace="} {
			if !strings.Contains(verr.Hint, part) {
				t.Errorf("%v: hint = %q, want %q", tc.surface, verr.Hint, part)
			}
		}
	}

	// A blank the connection says nothing about is the plain sentence.
	bare := config.Connection{Only: conn.Only}
	if verr := Fenced("staging", bare, c, plugin.Inputs{}, noEnv, plugin.SurfaceCLI); verr == nil ||
		!strings.Contains(verr.Message, `--namespace is blank, and profile "staging" reaches only: apps, apps-jobs`) {
		t.Errorf("a blank with nothing stated = %v", verr)
	}

	// And so is a blank sent over an input a profile does fill: the caller sent
	// the empty string, and a default of the profile's did not help it.
	filled := mustCap(t, reg, "cluster.sa.provision")
	verr := Fenced("staging", conn, filled, plugin.Inputs{Caller: map[string]any{"namespace": ""}}, noEnv, plugin.SurfaceCLI)
	if verr == nil || !strings.Contains(verr.Message, "is blank") || strings.Contains(verr.Message, "states") {
		t.Errorf("a blank sent over an input the profile fills = %v", verr)
	}
}
