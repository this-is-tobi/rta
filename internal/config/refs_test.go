package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The small helpers every surface leans on and this package's own tests
// never called: a tile's key, a profile reference's two halves, a plugins
// key's namespace, and a profile's window. Each is a grammar other packages
// build on, so the answer is pinned here, where the grammar lives.

func TestATileKeyIsTheCapabilityAloneOrAtItsProfile(t *testing.T) {
	for _, tc := range []struct{ id, profile, want string }{
		{"pg.overview", "", "pg.overview"},
		{"pg.overview", "prod", "pg.overview@prod"},
		{"pg.overview", "prod/analytics", "pg.overview@prod/analytics"},
	} {
		if got := TileKey(tc.id, tc.profile); got != tc.want {
			t.Errorf("TileKey(%q, %q) = %q, want %q", tc.id, tc.profile, got, tc.want)
		}
		if got := (Tile{ID: tc.id, Profile: tc.profile}).Key(); got != tc.want {
			t.Errorf("Tile.Key() = %q, want %q", got, tc.want)
		}
	}
}

func TestAProfileReferenceSplitsAtTheFirstSlash(t *testing.T) {
	for _, tc := range []struct{ ref, name, instance string }{
		{"staging", "staging", ""},
		{"staging/analytics", "staging", "analytics"},
	} {
		name, instance := SplitRef(tc.ref)
		if name != tc.name || instance != tc.instance {
			t.Errorf("SplitRef(%q) = (%q, %q), want (%q, %q)", tc.ref, name, instance, tc.name, tc.instance)
		}
		if RefName(tc.ref) != tc.name || RefInstance(tc.ref) != tc.instance {
			t.Errorf("RefName/RefInstance(%q) = (%q, %q)", tc.ref, RefName(tc.ref), RefInstance(tc.ref))
		}
	}
}

func TestAPluginsKeyNamespaceDropsTheInstanceAndThePin(t *testing.T) {
	for key, want := range map[string]string{
		"pg":                          "pg",
		"pg@bc86f6f7f56e":             "pg",
		"pg/analytics":                "pg",
		"pg/analytics@bc86f6f7f56e":   "pg",
		"kube/homelab@55c5df69c0802f": "kube",
	} {
		if got := PluginNamespace(key); got != want {
			t.Errorf("PluginNamespace(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestAProfilesWindowIsItsTTLAndAnUnreadableOneIsReported(t *testing.T) {
	for _, tc := range []struct {
		ttl  string
		want time.Duration
		has  bool
		bad  bool
	}{
		{"", 0, false, false},
		{"90m", 90 * time.Minute, true, false},
		{"soon", 0, false, true},
		{"-5m", 0, false, true},
	} {
		p := Profile{TTL: tc.ttl}
		d, has := p.Window()
		if d != tc.want || has != tc.has || p.BadTTL() != tc.bad {
			t.Errorf("TTL %q: Window() = (%v, %v), BadTTL() = %v; want (%v, %v), %v",
				tc.ttl, d, has, p.BadTTL(), tc.want, tc.has, tc.bad)
		}
	}
}

func TestAConnectionIsTunnelledByEitherSchemeAndNamesWhich(t *testing.T) {
	for _, tc := range []struct {
		conn Connection
		key  string
	}{
		{Connection{}, ""},
		{Connection{Kube: "homelab/databases/svc/pg:5432"}, "kube"},
		{Connection{SSH: "bastion/pg.internal:5432"}, "ssh"},
		{Connection{Kube: "a/b/svc/c:1", SSH: "d/e:2"}, "kube"},
	} {
		if got := tc.conn.Tunnelled(); got != (tc.key != "") {
			t.Errorf("%+v: Tunnelled() = %v", tc.conn, got)
		}
		if got := tc.conn.TunnelKey(); got != tc.key {
			t.Errorf("%+v: TunnelKey() = %q, want %q", tc.conn, got, tc.key)
		}
	}
}

func TestProfilesForNamesTheProfilesCoveringANamespaceSorted(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"staging": {Plugins: map[string]Connection{"pg": {}}},
		"prod":    {Plugins: map[string]Connection{"pg/analytics@bc86f6f7f56e": {}, "kube": {}}},
		"lab":     {Plugins: map[string]Connection{"kube": {}}},
	}}
	if got := strings.Join(cfg.ProfilesFor("pg"), " "); got != "prod staging" {
		t.Errorf("ProfilesFor(pg) = %q", got)
	}
	if got := strings.Join(cfg.ProfilesFor("s3"), " "); got != "" {
		t.Errorf("ProfilesFor(s3) = %q, want none", got)
	}
	if got := strings.Join(cfg.ProfileNames(), " "); got != "lab prod staging" {
		t.Errorf("ProfileNames() = %q", got)
	}
}

// A config from a path nobody named draws the automatic dashboard, whatever
// its block says: a cloned repository's ./.rta.yaml must not arrange a
// screen, and `hidden:` there could take the agent tile off it.
func TestAnUnnamedConfigsDashboardBlockIsNotDrawn(t *testing.T) {
	cfg := Config{Dashboard: Dashboard{Hidden: []string{"agent.overview"}, Columns: 1}}
	if cfg.Trusted() {
		t.Fatal("a zero Config reads as trusted")
	}
	if got := cfg.TrustedDashboard(); len(got.Hidden) != 0 || got.Columns != 0 {
		t.Errorf("TrustedDashboard() = %+v, want the empty block", got)
	}
}

// A profile built in memory takes the provenance of the file it is going
// into, and only from a Config the loader read: a zero one, which is what
// nothing vouched for looks like, stamps it untrusted.
func TestAProfileBuiltInMemoryTakesTheProvenanceOfItsFile(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	named, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if p := (Profile{}); p.Trusted() {
		t.Fatal("a zero Profile reads as trusted")
	}
	if !named.Stamp(Profile{}).Trusted() {
		t.Error("a profile going into a named config file reads as untrusted")
	}
	if (Config{}).Stamp(Profile{}).Trusted() {
		t.Error("a Config nothing loaded vouched for a profile")
	}
}

func TestASecretReferenceWithAnUnknownSchemeIsNamed(t *testing.T) {
	c := Connection{Secrets: map[string]string{
		"password": "kv:prod-db-password",
		"token":    "vault:secret/token",
		"cert":     "kube:pg-creds/tls.crt",
	}}
	if got := strings.Join(c.BadSecretRefs(), " "); got != "secrets.token" {
		t.Errorf("BadSecretRefs() = %q, want the vault: one alone", got)
	}
}
