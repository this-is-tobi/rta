package grant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/policy"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// withPolicy puts a team ceiling above the working directory.
func withPolicy(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, policy.RepoFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
}

// allowing runs grant.allow the way a person does, returning what they read.
func allowing(t *testing.T, values map[string]any) (string, *view.Error) {
	t.Helper()
	v, err := runAllow(context.Background(), req(values), catalog)
	if err != nil {
		verr, ok := err.(*view.Error)
		if !ok {
			t.Fatalf("runAllow: %v", err)
		}
		return "", verr
	}
	return v.(view.Text).Body, nil
}

// **The clamp is user-visible, so it is pinned.** Load already stops a grant
// that outlives the ceiling, which is the enforcement; this is the other
// half the ADR asks for — a ceiling that applies has to say so, and it has to
// store what it says, or `grant list` shows a four-hour row that dies in
// fifteen minutes.
func TestATeamCeilingClampsTheTTLAndSaysWhichCeilingBit(t *testing.T) {
	setup(t)
	withPolicy(t, "maxTTL: 15m\n")

	body, verr := allowing(t, map[string]any{"target": "kv.get", "scope": "db", "ttl": "4h"})
	if verr != nil {
		t.Fatal(verr)
	}
	if !strings.Contains(body, "15m") {
		t.Errorf("the grant was not clamped to the team ceiling: %s", body)
	}
	if !strings.Contains(body, "team's policy") {
		t.Errorf("the message does not say which ceiling bit, so somebody goes and "+
			"edits the wrong thing: %s", body)
	}
	if !strings.Contains(body, policy.RepoFile) {
		t.Errorf("the message does not name the file to edit: %s", body)
	}

	// And what was stored is what was said.
	grants, gverr := core.Load()
	if gverr != nil {
		t.Fatal(gverr)
	}
	if len(grants) != 1 {
		t.Fatalf("stored %d grants, want 1", len(grants))
	}
	if window := grants[0].Expires.Sub(grants[0].Issued); window > 16*time.Minute {
		t.Errorf("stored window is %v — the message said 15m and the file says otherwise",
			window)
	}
}

// The control: rta's own 24h cap still speaks in its own words when it is the
// one that bit, rather than blaming a policy that is not there.
func TestWithNoPolicyTheOwnMaximumStillSaysSo(t *testing.T) {
	setup(t)
	withPolicy(t, "")

	body, verr := allowing(t, map[string]any{"target": "kv.get", "scope": "db", "ttl": "72h"})
	if verr != nil {
		t.Fatal(verr)
	}
	if strings.Contains(body, "team's policy") {
		t.Errorf("rta's own cap was reported as a team policy: %s", body)
	}
	if !strings.Contains(body, "maximum") {
		t.Errorf("the 24h cap did not say it applied: %s", body)
	}
}

// A refusal names the rule and the file, because a grant that silently never
// works is the failure this whole mechanism is written against.
func TestARefusalNamesTheRuleAndTheFile(t *testing.T) {
	setup(t)
	withPolicy(t, "never: [kv.get]\n")

	_, verr := allowing(t, map[string]any{"target": "kv.get", "scope": "db", "ttl": "5m"})
	if verr == nil {
		t.Fatal("a target the policy forbids was granted")
	}
	if verr.Code != "grant.policy.refused" {
		t.Errorf("code = %q, want grant.policy.refused", verr.Code)
	}
	if !strings.Contains(verr.Message, "kv.get") || !strings.Contains(verr.Message, policy.RepoFile) {
		t.Errorf("the refusal names neither the rule nor the file: %s", verr.Message)
	}
}
