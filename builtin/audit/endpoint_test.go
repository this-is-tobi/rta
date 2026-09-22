package audit

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/findings"
)

// A gateway that authenticates with basic auth is configured as
// https://user:secret@gateway/, and the endpoint row echoed the variable
// whole — the one credential this audit printed, on the screen that gets
// pasted into an issue. The host and path stay, since they are the finding;
// the credential does not.
func TestTheEndpointRowNeverPrintsACredentialInTheURL(t *testing.T) {
	for _, k := range []string{"ANTHROPIC_API_URL", "OPENAI_BASE_URL", "OPENAI_API_BASE"} {
		t.Setenv(k, "")
	}
	t.Setenv("ANTHROPIC_BASE_URL", "https://alice:hunter2@gateway.example/v1")
	var r findings.Report
	auditModelEndpoint(&r)
	var row string
	for _, f := range r.Findings {
		if f.Check == "endpoint" {
			row = f.Detail
		}
	}
	if row == "" {
		t.Fatal("no endpoint row for a redirected base URL")
	}
	if strings.Contains(row, "hunter2") || strings.Contains(row, "alice") {
		t.Errorf("the row prints the credential: %q", row)
	}
	if !strings.Contains(row, "https://gateway.example/v1") || !strings.Contains(row, "not shown") {
		t.Errorf("the row does not name the gateway and say what it left out: %q", row)
	}
}

// A value with no userinfo, or one that is not a URL at all, is shown as it
// is: there is nothing to hide, and rewriting it would be the audit
// misquoting the setting.
func TestAnEndpointWithoutCredentialsIsShownVerbatim(t *testing.T) {
	for _, v := range []string{"https://gateway.example/v1", "not a url at all", "localhost:8080"} {
		if got := endpointForDisplay(v); got != v {
			t.Errorf("endpointForDisplay(%q) = %q, want it unchanged", v, got)
		}
	}
}
