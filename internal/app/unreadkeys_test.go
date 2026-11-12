package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// pgDeclaring is a pg plugin reading host and the TLS key tlsKey, which renamed
// the keys in renamed.
func pgDeclaring(tlsKey string, renamed map[string]string) plugin.Plugin {
	return plugin.Plugin{Name: "pg", Summary: "pg plugin", Version: "1.0.0", Renamed: renamed,
		Capabilities: []plugin.Capability{{ID: "pg.status", Summary: "status", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "host", Type: plugin.String, Config: "host", Local: true, Help: "host"},
				{Name: tlsKey, Type: plugin.String, Config: tlsKey, Local: true, Help: "tls mode"},
			},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "up"}, nil },
		}}}
}

// A section an upgrade left a renamed key in is repinned all the same, since
// the stale pin stopped the plugin's calls as surely, and the repin names each
// key the installed build does not read, with the one it reads in its place.
func TestConfigRepinNamesTheKeysTheInstalledBuildDoesNotRead(t *testing.T) {
	run := repinSession(t)
	writeConfig(t, "plugins:\n  pg@111111111111:\n    host: db.internal\n    sslmode: verify-full\n    sizes: 3\n")
	reg := registry.New()
	if err := reg.RegisterFrom(pgDeclaring("tls", map[string]string{"sslmode": "tls"}),
		registry.Origin{Path: "/usr/local/bin/rta-plugin-pg", Digest: pgNewDigest}); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := run(reg, "config", "repin", "--plugin", "pg", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	for _, want := range []string{
		"repinned to pg@222222222222",
		"`plugins.pg@222222222222` states `sslmode`, which the installed pg does not read; it reads `tls` in its place",
		"`plugins.pg@222222222222` states `sizes`, which the installed pg does not read",
		"pg's calls are refused while the section states a key it does not read",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the repin does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "states `host`") {
		t.Errorf("a key the build reads was named:\n%s", out)
	}
}

// A move's receipt names each key the operator's section and profiles state
// that the build moved to does not read: an upgrade is told what an old key
// became, and a move back what a key the newer build introduced was called.
func TestAMoveSaysWhichStatedKeysTheBuildItMovedToDoesNotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("RTA_CONFIG", path)
	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	older := pgDeclaring("sslmode", nil)
	newer := pgDeclaring("tls", map[string]string{"sslmode": "tls"})

	write("plugins:\n  pg@111111111111:\n    sslmode: verify-full\n" +
		"profiles:\n  staging:\n    plugins:\n      pg@111111111111:\n        set:\n          sslmode: require\n")
	up := plugindist.Upgraded{Report: plugindist.Report{Name: "pg", Version: "2.0.0", Declared: newer}, FromDeclared: older}
	got := pairText(leftBehind(up, false))
	for _, want := range []string{
		"`plugins.pg@111111111111` states `sslmode`, which pg 2.0.0 does not read; it reads `tls` in its place",
		"profile `staging`'s `set:` under pg@111111111111 states `sslmode`, which pg 2.0.0 does not read; it reads `tls` in its place",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the upgrade's receipt does not say %q:\n%s", want, got)
		}
	}

	write("plugins:\n  pg@222222222222:\n    tls: verify-full\n")
	back := plugindist.Upgraded{Report: plugindist.Report{Name: "pg", Version: "1.0.0", Declared: older},
		FromDeclared: newer, Older: true}
	if got := pairText(leftBehind(back, true)); !strings.Contains(got,
		"`plugins.pg@222222222222` states `tls`, which pg 1.0.0 will not read; it reads `sslmode` in its place") {
		t.Errorf("the move back's preview says:\n%s", got)
	}
}

// A profile entry moved onto the installed build is told, as a warning beside
// the table, each `set:` key the build does not read and what it reads in its
// place: the profile is refused until it is fixed.
func TestProfileRepinWarnsOfTheKeysTheInstalledBuildDoesNotRead(t *testing.T) {
	run := repinSession(t)
	writeConfig(t, "profiles:\n  staging:\n    plugins:\n      pg@111111111111:\n        set:\n"+
		"          host: db.internal\n          sslmode: verify-full\n")
	reg := registry.New()
	if err := reg.RegisterFrom(pgDeclaring("tls", map[string]string{"sslmode": "tls"}),
		registry.Origin{Path: "/usr/local/bin/rta-plugin-pg", Digest: pgNewDigest}); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := run(reg, "profile", "repin", "staging", "--plugin", "pg", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	for _, want := range []string{"repinned to pg@222222222222", "core.profile.unread",
		"profile `staging`'s `set:` under pg@222222222222 states `sslmode`, which the installed pg does not read; " +
			"it reads `tls` in its place"} {
		if !strings.Contains(out, want) {
			t.Errorf("the repin does not say %q:\n%s", want, out)
		}
	}
}

// pairText is pairs one per line, key and value.
func pairText(pairs []view.Pair) string {
	var b strings.Builder
	for _, p := range pairs {
		b.WriteString(p.Key + ": " + p.Value + "\n")
	}
	return b.String()
}
