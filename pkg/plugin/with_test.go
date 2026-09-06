package plugin

import (
	"strings"
	"testing"
)

// With is a dependency between two declared inputs, and one that names
// nothing — or itself — would silently hide the input on every form.
func TestWithMustNameASibling(t *testing.T) {
	for _, tc := range []struct {
		name, with, wantSub string
	}{
		{"a sibling", "server", ""},
		{"nothing declared", "nowhere", "does not declare"},
		{"itself", "extra", "With itself"},
	} {
		p := validPlugin()
		p.Capabilities[0].Inputs = append(p.Capabilities[0].Inputs,
			Field{Name: "server", Type: String, Local: true, Remote: true},
			Field{Name: "extra", Type: String, With: tc.with})
		err := p.Validate()
		switch {
		case tc.wantSub == "" && err != nil:
			t.Errorf("%s: refused: %v", tc.name, err)
		case tc.wantSub != "" && (err == nil || !strings.Contains(err.Error(), tc.wantSub)):
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.wantSub)
		}
	}
	f := Field{Name: "passphrase", Type: Secret}.OnlyWith("server")
	if f.With != "server" || f.Name != "passphrase" {
		t.Fatalf("OnlyWith = %+v", f)
	}
}
