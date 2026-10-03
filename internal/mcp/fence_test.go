package mcp

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/profile"
)

// The operator's selection fences the environments: while staging is on, a
// grant for production is not honoured. A selection file that is edited,
// truncated or replaced did not read as a fence that could not be trusted, it
// read as no fence, and the grant for production worked again on the machine
// where something had been at the file. Held shut instead, the way an
// unverifiable lock list is, until `rta use` writes it again.
func TestASelectionFileThatDoesNotVerifyClosesEveryProfile(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)
	now := time.Now()
	if verr := grant.Save([]grant.Grant{{
		Target: "pg", Profile: "prod", ProfilePin: f.pin("prod", "pg"), Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
	if verr := profile.SaveSelection(profile.Selection{Active: "prod"}); verr != nil {
		t.Fatal(verr)
	}
	if res := f.call(t, map[string]any{"profile": "prod", "sql": "select 1"}); res.IsError {
		t.Fatalf("the environment that is on was refused: %s", contentText(t, res))
	}

	// Edited to something nothing sealed, and to something that is not a
	// selection at all, which is the one that lifted the fence.
	for _, content := range []string{`{"active":"staging"}`, "not a selection", ""} {
		if err := os.WriteFile(profile.SelectionPath(), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		*f.sawHost = ""
		assertCode(t, f.call(t, map[string]any{"profile": "prod", "sql": "select 1"}), "core.grant.required")
		if *f.sawHost != "" {
			t.Errorf("the handler ran against %q with a fence nobody could believe (%q)", *f.sawHost, content)
		}
	}
	res := f.call(t, map[string]any{"profile": "prod", "sql": "select 1"})
	// Said as an ungranted call says it, so it is not a way to read what the
	// operator is working on, and on the record for the operator.
	if strings.Contains(contentText(t, res), "verif") {
		t.Errorf("the refusal tells the agent about the selection file: %s", contentText(t, res))
	}
	rows, err := agentlog.Read(1)
	if err != nil || len(rows) != 1 || !strings.Contains(rows[0].Note, "does not verify") {
		t.Errorf("the record does not say whose the fix is: %+v, %v", rows, err)
	}

	// The operator writes it again.
	if verr := profile.SaveSelection(profile.Selection{Active: "prod"}); verr != nil {
		t.Fatal(verr)
	}
	if res := f.call(t, map[string]any{"profile": "prod", "sql": "select 1"}); res.IsError {
		t.Fatalf("the fence still held shut after it was written again: %s", contentText(t, res))
	}
}
