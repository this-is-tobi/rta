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
// pipe, so a Piped input is missing whatever the tile is pinned to, unless
// its with: states it. An optional credential that is not the call's subject
// — the key a signature is checked with — is not.
func TestAPipedInputNoTileStatesIsMissing(t *testing.T) {
	c := plugin.Capability{
		ID: "codec.jwt", Summary: "decode", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) { return nil, nil },
		Inputs: []plugin.Field{
			{Name: "token", Type: plugin.Secret, Positional: true, Piped: true},
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
}

// debug.ansi has codec.jwt's shape with no credential in it, and inferring
// the shape from "a positional credential" missed it: `rta dashboard add
// debug.ansi` and + wrote a tile answering "no text to explain" on every
// refresh. Its text is no credential, so --set states it, and the refusal
// says so.
func TestAPipedInputATileCanStateIsHintedWithSet(t *testing.T) {
	c := plugin.Capability{
		ID: "debug.ansi", Summary: "explain", Safety: plugin.Read,
		Run:    func(context.Context, plugin.Request) (view.View, error) { return nil, nil },
		Inputs: []plugin.Field{{Name: "input", Type: plugin.Text, Positional: true, Piped: true}},
	}
	if got := MissingInputs(c, nil, false); !slices.Equal(got, []string{"input"}) {
		t.Errorf("missing = %v, want [input]", got)
	}
	if got := Untileable(c); len(got) != 0 {
		t.Errorf("untileable = %v, want nothing: --set can state text", got)
	}
	if why := addRefusal(c, false); !strings.Contains(why, "--set input=") {
		t.Errorf("+ on debug.ansi: %q, want a --set hint", why)
	}
	// Nor does the automatic dashboard run it unasked, NoPreview or not.
	if previewable(c) {
		t.Error("a capability whose Piped input nothing states was put on the automatic dashboard")
	}
}

// A TUI form will not submit without a Piped input: there is no pipe behind
// it, and the handler's own "nothing to read" was the only thing saying so.
func TestAPipedInputIsRequiredInAForm(t *testing.T) {
	f := plugin.Field{Name: "input", Type: plugin.Text, Positional: true, Piped: true, Help: "text"}
	if err := validatorFor(f)(""); err == nil {
		t.Error("an empty Piped input was accepted")
	}
	if err := validatorFor(f)("some text"); err != nil {
		t.Errorf("a given one was refused: %v", err)
	}
	if d := fieldDescription(f); !strings.Contains(d, "required") {
		t.Errorf("description = %q, want it marked required", d)
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
			{Name: "token", Type: plugin.Secret, Positional: true, Piped: true},
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
