package plugin

import (
	"strings"
	"testing"
)

// Widens is the plugin's statement that a switch reads past the input a profile
// can fence, and the host acts on it by refusing calls. One that names nothing,
// names itself, or sits on a value that is not a switch is a statement no call
// ever consults — a fence its author believes is closed — so each is refused
// where the author is.
func TestWidensMustBeABoolNamingASibling(t *testing.T) {
	for _, tc := range []struct {
		name    string
		widener Field
		wantSub string
	}{
		{"a bool naming a sibling", Field{Name: "all", Type: Bool, Widens: "scope"}, ""},
		{"nothing declared", Field{Name: "all", Type: Bool, Widens: "nowhere"}, "does not declare"},
		{"itself", Field{Name: "all", Type: Bool, Widens: "all"}, "widen itself"},
		{"a value rather than a switch", Field{Name: "all", Type: String, Widens: "scope"}, "only a Bool"},
	} {
		p := validPlugin()
		p.Capabilities[0].Inputs = append(p.Capabilities[0].Inputs,
			Field{Name: "scope", Type: String, Config: "scope"}, tc.widener)
		err := p.Validate()
		switch {
		case tc.wantSub == "" && err != nil:
			t.Errorf("%s: refused: %v", tc.name, err)
		case tc.wantSub != "" && (err == nil || !strings.Contains(err.Error(), tc.wantSub)):
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.wantSub)
		}
	}
}
