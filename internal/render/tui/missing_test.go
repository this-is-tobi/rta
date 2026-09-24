package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// codec.jwt's token is not Required, because a pipe supplies it on the CLI,
// and Required was all this looked at: `rta dashboard add codec.jwt` and +
// wrote a tile answering "no token to read" on every refresh. A tile reads no
// pipe, --set refuses a credential and no profile fills this one, so it is
// missing whatever the tile is pinned to. An optional credential that is not
// the call's subject — the key a signature is checked with — is not.
func TestAPositionalCredentialNoTileCanBeGivenIsMissing(t *testing.T) {
	c := plugin.Capability{
		ID: "codec.jwt", Summary: "decode", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) { return nil, nil },
		Inputs: []plugin.Field{
			{Name: "token", Type: plugin.Secret, Positional: true},
			{Name: "key", Type: plugin.Secret},
		},
	}
	for _, pinned := range []bool{false, true} {
		if got := MissingInputs(c, nil, pinned); !slices.Equal(got, []string{"token"}) {
			t.Errorf("pinned=%v: missing = %v, want [token]", pinned, got)
		}
	}
	// A with: written by hand does give it one.
	if got := MissingInputs(c, map[string]any{"token": "x"}, false); len(got) != 0 {
		t.Errorf("a stated token is still missing: %v", got)
	}
	// A positional credential a profile may fill is the profile's to give.
	c.Inputs[0].Local, c.Inputs[0].EnvFallback = true, true
	if got := MissingInputs(c, nil, true); len(got) != 0 {
		t.Errorf("a profile-fillable credential is missing from a pinned tile: %v", got)
	}
}

// + refuses such a capability in words that lead somewhere. It hinted
// `rta dashboard add codec.jwt --set token=…`, which `rta dashboard add`
// refuses in turn — a credential is never written into the config — so the
// flash sent the reader from one refusal to the next.
func TestPlusRefusesAnUntileableCredentialWithoutPointingAtSet(t *testing.T) {
	c := plugin.Capability{
		ID: "codec.jwt", Summary: "decode", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) { return nil, nil },
		Inputs: []plugin.Field{
			{Name: "token", Type: plugin.Secret, Positional: true},
			{Name: "key", Type: plugin.Secret},
		},
	}
	for _, pinned := range []bool{false, true} {
		why := addRefusal(c, pinned)
		if strings.Contains(why, "--set") || !strings.Contains(why, "token") ||
			!strings.Contains(why, "`rta codec jwt`") {
			t.Errorf("pinned=%v: %q", pinned, why)
		}
	}
	// A required input a --set can state is still hinted with one.
	c.Inputs = []plugin.Field{{Name: "namespace", Type: plugin.String, Required: true}}
	if why := addRefusal(c, false); !strings.Contains(why, "--set namespace=") {
		t.Errorf("a settable input lost its hint: %q", why)
	}
}
