package plugin

import "testing"

// While a run drops the section that says where a plugin goes, the credential
// exported for that destination stays out of a call that does not say where it
// goes itself, and goes with one that does. A `plugin dev` run sent
// RTA_PROM_TOKEN to whatever listened on the plugin's default port.
func TestAHeldAmbientCredentialGoesOnlyWhereTheCallIsAimed(t *testing.T) {
	c := Capability{ID: "prom.alert.list", Summary: "alerts", Safety: Read, Inputs: []Field{
		{Name: "url", Type: String, Default: "http://127.0.0.1:9090", Endpoint: EndpointURL},
		{Name: "tls", Type: Bool, Endpoint: EndpointTLS},
		{Name: "token", Type: Secret, Local: true, EnvFallback: true},
	}}
	t.Setenv(LocalEnvVar(c.ID, "token"), "exported-for-prom-internal")

	if got := Resolve(c, Inputs{HoldAmbient: true})["token"]; got != nil {
		t.Errorf("a call aimed nowhere was given the credential: %v", got)
	}
	// The TLS switch says how to talk, not where.
	if got := Resolve(c, Inputs{HoldAmbient: true, Caller: map[string]any{"tls": true}})["token"]; got != nil {
		t.Errorf("a call naming only TLS was given the credential: %v", got)
	}
	aimed := Inputs{HoldAmbient: true, Caller: map[string]any{"url": "https://prom.internal"}}
	if got := Resolve(c, aimed)["token"]; got != "exported-for-prom-internal" {
		t.Errorf("a call aimed by the person who typed it lost the credential: %v", got)
	}
	if got := Resolve(c, Inputs{})["token"]; got != "exported-for-prom-internal" {
		t.Errorf("an ordinary run lost the credential: %v", got)
	}
}
