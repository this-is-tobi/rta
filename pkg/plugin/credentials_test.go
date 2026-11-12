package plugin

import "testing"

// Where a credential goes is named the way the call reads it: the plugin's
// own variable with no profile, the profile's own with one, none for a
// labelled instance, which reads secrets: alone.
func TestCredentialSourcesNameTheVariableTheCallReads(t *testing.T) {
	cli := NewRequest(nil, false, false).WithSurface(SurfaceCLI)
	for _, tc := range []struct {
		req  Request
		want string
	}{
		{cli, "$" + LocalEnvVar("keycloak.overview", "client-secret") + " or --client-secret, " +
			"or mapped from the store in a profile's secrets:"},
		{cli.WithProfile("staging", TunnelNone), "$" + ProfileEnvVar("staging", "client-secret") +
			" or --client-secret, or mapped from the store in profile staging's secrets:"},
		{cli.WithProfile("staging/eu", TunnelNone), "--client-secret, or mapped from the store in profile staging/eu's secrets:"},
	} {
		if got := CredentialSources(tc.req, "keycloak.overview", "client-secret"); got != tc.want {
			t.Errorf("profile %q: got %q, want %q", tc.req.Profile(), got, tc.want)
		}
	}
	if got := CredentialSources(NewRequest(nil, false, false).WithSurface(SurfaceMCP).WithProfile("staging", TunnelNone),
		"keycloak.overview", "client-secret"); got != "$"+ProfileEnvVar("staging", "client-secret")+
		" or the operator's `client-secret` setting, or mapped from the store in profile staging's secrets:" {
		t.Errorf("to an agent: %q", got)
	}
}
