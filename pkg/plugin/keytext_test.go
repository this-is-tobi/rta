package plugin

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// keyBody is what a key holds between its armour lines: the part that must
// never reach a message, whatever the armour says.
const keyBody = "MC4CAQAwBQYDK2VwBCIEIDoNotPrintThisKeyBodyAnywhereAtAll"

func pemKey(kind string) string {
	return "-----BEGIN " + kind + "-----\n" + keyBody + "\n-----END " + kind + "-----\n"
}

// keyCap is a connection plugin's capability with a file setting a profile
// fills, a credential, and an input that carries what a call stores.
func keyCap(ran *bool) Capability {
	c := Capability{ID: "demo.query", Summary: "query", Safety: Read,
		Inputs: []Field{
			{Name: "key-file", Type: String, Config: "key-file", Local: true, Help: "client key"},
			{Name: "password", Type: Secret, Local: true, EnvFallback: true, Help: "password"},
			{Name: "data", Type: Text, Help: "what to store"},
		},
		Run: func(context.Context, Request) (view.View, error) {
			*ran = true
			return view.Text{Body: "ran"}, nil
		},
	}
	c.Run = GuardInputs(c)
	return c
}

// A private key that a profile's reference or a config line delivers to a
// setting that is no credential is refused before the plugin reads it: read as
// the file's path, its text was repeated in the refusal of the open, and from
// there reached the agent and the agent log. The refusal names it by its shape,
// says where it is set, and never carries it.
func TestAPrivateKeyAProfileGivesASettingIsRefusedUnread(t *testing.T) {
	for _, kind := range []string{"PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY", "ENCRYPTED PRIVATE KEY", "OPENSSH PRIVATE KEY"} {
		for _, sf := range []Surface{SurfaceCLI, SurfaceTUI, SurfaceMCP} {
			ran := false
			c := keyCap(&ran)
			req := ResolveRequest(c, Inputs{ProfileName: "ohmlab", Profile: map[string]any{"key-file": pemKey(kind)}},
				false, false).WithSurface(sf)
			_, err := c.Run(context.Background(), req)
			verr := view.AsError(err, "")
			if ran || verr == nil || verr.Code != "core.input.privatekey" || verr.Input != "key-file" {
				t.Fatalf("%s on %s: %+v (ran %v)", kind, sf, verr, ran)
			}
			if strings.Contains(verr.Message+verr.Hint, keyBody) || !strings.Contains(verr.Message, `the profile "ohmlab"`) {
				t.Errorf("%s on %s: %q / %q", kind, sf, verr.Message, verr.Hint)
			}
		}
	}

	ran := false
	c := keyCap(&ran)
	req := ResolveRequest(c, Inputs{Config: map[string]any{"key-file": pemKey("PRIVATE KEY")}}, false, false)
	if verr := CheckInputs(c, req); verr == nil || verr.Code != "core.input.privatekey" ||
		!strings.Contains(verr.Message, "plugins.demo.key-file") || !strings.Contains(verr.Hint, "name the file that holds the key there instead") {
		t.Errorf("from the config: %+v", verr)
	}
	// Not a key from its armour alone: a line that only names one is not one.
	req = ResolveRequest(c, Inputs{Config: map[string]any{"key-file": "/srv/keys/PRIVATE KEY-----"}}, false, false)
	if verr := CheckInputs(c, req); verr != nil {
		t.Errorf("a path: %+v", verr)
	}
}

// What the rule leaves alone: a key in a credential input, where it belongs; a
// key a caller types, which reaches nobody it did not come from; and a
// certificate, which is public.
func TestAPrivateKeyIsRefusedOnlyWhereTheOperatorPutItInASetting(t *testing.T) {
	ran := false
	c := keyCap(&ran)
	for name, in := range map[string]Inputs{
		"a credential input":       {ProfileName: "ohmlab", Profile: map[string]any{"password": pemKey("PRIVATE KEY")}},
		"a caller's own value":     {Caller: map[string]any{"data": pemKey("PRIVATE KEY")}},
		"a certificate":            {ProfileName: "ohmlab", Profile: map[string]any{"key-file": pemKey("CERTIFICATE")}},
		"a caller over the config": {Caller: map[string]any{"key-file": "/srv/key.pem"}, Config: map[string]any{"key-file": pemKey("PRIVATE KEY")}},
	} {
		if verr := CheckInputs(c, ResolveRequest(c, in, false, false)); verr != nil {
			t.Errorf("%s: %+v", name, verr)
		}
	}
}
