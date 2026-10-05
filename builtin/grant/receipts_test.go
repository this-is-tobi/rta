package grant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/lockdown"
	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func revokeAt(t *testing.T, values map[string]any, dry bool) string {
	t.Helper()
	v, err := runRevoke(context.Background(),
		plugin.NewRequest(values, dry, true).WithSurface(plugin.SurfaceCLI))
	if err != nil {
		t.Fatal(err)
	}
	return v.(view.Text).Body
}

func connect(t *testing.T, agent string) {
	t.Helper()
	if err := session.Start(session.Record{
		ID: session.NewID(), Agent: agent, Since: time.Now(), PID: os.Getpid(),
	}); err != nil {
		t.Fatal(err)
	}
}

func freeze(t *testing.T, name string) {
	t.Helper()
	l, verr := lockdown.Build("agent", name, "", "", "terminal")
	if verr != nil {
		t.Fatal(verr)
	}
	if verr := lockdown.Add(l); verr != nil {
		t.Fatal(verr)
	}
}

// "revoked 5 grants" is a count, and a count cannot be checked against what was
// meant: the revoke that took the wrong five had to be retyped from memory.
func TestRevokeNamesTheGrantsItTookBackAndHowToPutThemBack(t *testing.T) {
	setup(t)
	issueRows(t,
		core.Grant{Target: "kv.get", Scope: "db-password", Agent: "claude", MaxUses: 3},
		core.Grant{Target: "net.dns", Agent: "claude"})

	dry := revokeAt(t, map[string]any{"all": true}, true)
	for _, want := range []string{"would revoke 2 grants:", "claude may call kv.get on db-password", "claude may call net.dns"} {
		if !strings.Contains(dry, want) {
			t.Errorf("the dry run says %q, want it to say %q", dry, want)
		}
	}
	if strings.Contains(dry, "re-issue") {
		t.Errorf("a dry run offers to put back what it has not taken:\n%s", dry)
	}
	if grants, _ := core.Load(); len(grants) != 2 {
		t.Fatalf("the dry run revoked: %+v", grants)
	}

	body := revokeAt(t, map[string]any{"all": true}, false)
	for _, want := range []string{
		"revoked 2 grants:",
		"claude may call kv.get on db-password\n    re-issue: rta grant allow kv.get db-password --agent claude --ttl 59m",
		"--max-uses 3",
		"claude may call net.dns\n    re-issue: rta grant allow net.dns --agent claude --ttl 59m",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the receipt does not say %q:\n%s", want, body)
		}
	}
	if grants, _ := core.Load(); len(grants) != 0 {
		t.Errorf("left %+v", grants)
	}
}

// The re-issue is what the grant had left, not what it was first given: a spent
// budget put back at its first size is a widening nobody asked for.
func TestTheReissueCallIsWhatTheGrantHadLeft(t *testing.T) {
	setup(t)
	now := time.Now()
	if verr := core.Save([]core.Grant{{
		Target: "kv.get", Scope: "db-password", Agent: "claude",
		Issued: now, Expires: now.Add(10 * time.Minute), MaxUses: 5, Uses: 4,
	}}); verr != nil {
		t.Fatal(verr)
	}
	body := revokeAt(t, map[string]any{"all": true}, false)
	if !strings.Contains(body, "--max-uses 1") || strings.Contains(body, "--max-uses 5") {
		t.Errorf("the re-issue gives back the first budget:\n%s", body)
	}
}

// Revoking takes back what grants gave and nothing else: the reads an agent's
// server answers stay open, and the revoke somebody types in a hurry is the
// one for which "it is stopped" is what they believe.
func TestRevokeSaysWhoIsStillConnectedAndHowToStopThem(t *testing.T) {
	setup(t)
	issueRows(t, core.Grant{Target: "net.dns", Agent: "test"})
	body := revokeAt(t, map[string]any{"all": true}, false)
	if want := "test is still connected (reads stay open): `rta lock add test`"; !strings.HasSuffix(body, want) {
		t.Errorf("the receipt does not end with %q:\n%s", want, body)
	}

	connect(t, "cursor")
	issueRows(t, core.Grant{Target: "net.dns", Agent: "test"})
	body = revokeAt(t, map[string]any{"all": true}, false)
	if want := "cursor, test are still connected (reads stay open): `rta lock add --all` freezes them all, or `rta lock add <name>` one"; !strings.HasSuffix(body, want) {
		t.Errorf("with two connected the receipt does not end with %q:\n%s", want, body)
	}
}

func TestRevokeDoesNotNameAnAgentAlreadyFrozen(t *testing.T) {
	setup(t)
	connect(t, "cursor")
	freeze(t, "test")
	issueRows(t, core.Grant{Target: "net.dns", Agent: "test"})
	body := revokeAt(t, map[string]any{"all": true}, false)
	if strings.Contains(body, "test is still") || !strings.Contains(body, "cursor is still connected") {
		t.Errorf("a frozen agent was named as running, or a running one was not:\n%s", body)
	}

	issueRows(t, core.Grant{Target: "net.dns", Agent: "cursor"})
	freeze(t, lockdown.Everyone)
	if body := revokeAt(t, map[string]any{"all": true}, false); strings.Contains(body, "still connected") {
		t.Errorf("with every agent frozen the receipt says one is still running:\n%s", body)
	}
}

// In an incident the second revoke is as likely as the first.
func TestRevokeWithNothingLeftStillSaysWhoIsConnected(t *testing.T) {
	setup(t)
	body := revokeAt(t, map[string]any{"all": true}, false)
	if !strings.HasPrefix(body, "Nothing to revoke") || !strings.Contains(body, "test is still connected") {
		t.Errorf("a revoke with nothing to take back says %q", body)
	}
}

// `grant allow kv` reads like one grant and is every capability in kv that needs
// one, destructive ones included.
func TestAPluginWideGrantSaysHowMuchItCovers(t *testing.T) {
	setup(t)
	dry := plugin.NewRequest(map[string]any{"target": "todo", "ttl": "1h"}, true, true).WithSurface(plugin.SurfaceCLI)
	v, err := allowH(context.Background(), dry)
	if err != nil {
		t.Fatal(err)
	}
	body := v.(view.Text).Body
	if want := "note: todo covers 1 capability an agent needs a grant for, 1 destructive (todo.rm) — name one to allow only it"; !strings.Contains(body, want) {
		t.Errorf("the dry run says %q, want %q", body, want)
	}
	one, err := allowH(context.Background(), plugin.NewRequest(map[string]any{"target": "todo.rm", "ttl": "1h"}, true, true).
		WithSurface(plugin.SurfaceCLI))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(one.(view.Text).Body, "covers") {
		t.Errorf("a grant on one capability states a breadth: %q", one.(view.Text).Body)
	}
}

func TestTheDestructiveSummaryNeverSaysAndOneOther(t *testing.T) {
	for _, c := range []struct {
		ids  []string
		want string
	}{
		{nil, "none destructive"},
		{[]string{"a.rm"}, "1 destructive (a.rm)"},
		{[]string{"a", "b", "c"}, "3 destructive (a, b, c)"},
		{[]string{"a", "b", "c", "d"}, "4 destructive (a, b, c, d)"},
		{[]string{"a", "b", "c", "d", "e"}, "5 destructive (a, b, c and 2 others)"},
	} {
		if got := destructiveSummary(c.ids); got != c.want {
			t.Errorf("destructiveSummary(%v) = %q, want %q", c.ids, got, c.want)
		}
	}
}

// Renew extends time and nothing else, and that includes never taking it away:
// `renew --ttl 2h` on a grant with eight hours left set it to two and said
// renewed.
func TestRenewNeverShortensAGrant(t *testing.T) {
	setup(t)
	now := time.Now()
	long := now.Add(8 * time.Hour)
	if verr := core.Save([]core.Grant{
		{Target: "kv.get", Agent: "claude", Issued: now, Expires: long, TTL: "8h"},
	}); verr != nil {
		t.Fatal(verr)
	}
	v, err := renew(t, map[string]any{"ttl": "2h"})
	if err != nil {
		t.Fatal(err)
	}
	body := v.(view.Text).Body
	if !strings.HasPrefix(body, "nothing renewed — this grant already runs at least that long:") {
		t.Errorf("a renewal that changed nothing says:\n%s", body)
	}
	got, _ := core.Load()
	if len(got) != 1 || !got[0].Expires.Equal(long) {
		t.Errorf("the deadline moved: %+v", got)
	}
}

func TestRenewSaysWhichGrantsItExtendedAndWhichItLeft(t *testing.T) {
	setup(t)
	now := time.Now()
	if verr := core.Save([]core.Grant{
		{Target: "kv.get", Agent: "claude", Issued: now, Expires: now.Add(8 * time.Hour), TTL: "8h"},
		{Target: "net.dns", Agent: "claude", Issued: now, Expires: now.Add(10 * time.Minute), TTL: "10m"},
	}); verr != nil {
		t.Fatal(verr)
	}
	v, err := renew(t, map[string]any{"ttl": "2h"})
	if err != nil {
		t.Fatal(err)
	}
	body := v.(view.Text).Body
	if !strings.HasPrefix(body, "renewed 1 of 2 grants:") ||
		!strings.Contains(body, "claude may call net.dns until") ||
		!strings.Contains(body, "claude may call kv.get until") ||
		!strings.Contains(body, "— unchanged, it already runs that long") {
		t.Errorf("a renewal that extended one of two says:\n%s", body)
	}

	dry := plugin.NewRequest(map[string]any{"ttl": "1m"}, true, true).WithSurface(plugin.SurfaceCLI)
	c := renewCap(t)
	dv, err := c.Run(context.Background(), dry)
	if err != nil {
		t.Fatal(err)
	}
	if body := dv.(view.Text).Body; !strings.HasPrefix(body, "nothing would be renewed — every grant already runs at least that long:") {
		t.Errorf("the dry run of a renewal that would change nothing says:\n%s", body)
	}
}

// A target that names nothing is as often a plugin not installed here as a typo,
// and the hint that says only "check for a typo" is the wrong one for it.
func TestAnUnknownTargetHintsTheInstallWhenItLooksLikeAService(t *testing.T) {
	setup(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	_, err := allowH(context.Background(), req(map[string]any{"target": "pg.query", "ttl": "1h"}))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "grant.unknowntarget" {
		t.Fatalf("got %v, want grant.unknowntarget", err)
	}
	for _, want := range []string{"check for a typo", "pg is a first-party plugin", "`rta plugin install pg`"} {
		if !strings.Contains(verr.Hint, want) {
			t.Errorf("the hint does not say %q: %q", want, verr.Hint)
		}
	}
	if strings.Contains(verr.Hint, "index add") {
		t.Errorf("the hint sends the install through an index command: %q", verr.Hint)
	}
}

// A word that names no first-party plugin and no attached index is a typo as
// likely as a service, and is not told it may be one.
func TestAnUnknownTargetOfNoKnownPluginIsNotGuessedToBeAService(t *testing.T) {
	setup(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	_, err := allowH(context.Background(), req(map[string]any{"target": "mongo.query", "ttl": "1h"}))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "grant.unknowntarget" {
		t.Fatalf("got %v, want grant.unknowntarget", err)
	}
	if strings.Contains(verr.Hint, "plugin install") || strings.Contains(verr.Hint, "index add") {
		t.Errorf("a word that is no known plugin was sent to an install: %q", verr.Hint)
	}
}

// With an index attached the hint is not a guess: the plugin is in it, and the
// install is the one command that makes the target grantable. Read from the
// index on disk, so a grant never reaches a network to find out.
func TestAnUnknownTargetNamesTheIndexThatCarriesThePlugin(t *testing.T) {
	setup(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(paths.Indexes(), "official", "index")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name: pg\nversion: 0.1.0\nsummary: PostgreSQL toolkit\nplatforms:\n  - os: linux\n    arch: amd64\n" +
		"    url: https://example.com/pg_linux_amd64\n    sha256: " + strings.Repeat("b", 64) + "\n" +
		"capabilities:\n  - id: pg.query\n    safety: write\n    grant: true\n"
	if err := os.WriteFile(filepath.Join(dir, "pg.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := allowH(context.Background(), req(map[string]any{"target": "pg.query", "ttl": "1h"}))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "grant.unknowntarget" {
		t.Fatalf("got %v, want grant.unknowntarget", err)
	}
	want := "pg is a plugin in the official index, not installed here — `rta plugin install pg` installs it, and then it can be granted"
	if !strings.Contains(verr.Hint, want) || strings.Contains(verr.Hint, "index add") {
		t.Errorf("the hint says %q, want it to say %q and not to attach what is attached", verr.Hint, want)
	}
}
