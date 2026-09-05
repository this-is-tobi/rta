package profile

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// instanced is staging with two pg databases — a default and analytics —
// and two s3 buckets with no default.
const instanced = `
profiles:
  staging:
    plugins:
      pg:
        set: {host: main.internal}
      pg/analytics:
        set: {host: analytics.internal}
      s3/assets:
        set: {endpoint: assets.internal}
      s3/logs:
        set: {endpoint: logs.internal}
`

// A ref naming an instance resolves that instance; a bare name resolves the
// default the profile wrote as one.
func TestLookupResolvesInstances(t *testing.T) {
	cfg := load(t, instanced)
	reg := pgRegistry(t)

	conn, verr := Lookup(cfg, pgCap(), "staging", reg)
	if verr != nil {
		t.Fatal(verr)
	}
	if conn.Set["host"] != "main.internal" {
		t.Errorf("bare ref resolved %v, want the default instance", conn.Set)
	}

	conn, verr = Lookup(cfg, pgCap(), "staging/analytics", reg)
	if verr != nil {
		t.Fatal(verr)
	}
	if conn.Set["host"] != "analytics.internal" {
		t.Errorf("staging/analytics resolved %v", conn.Set)
	}
}

// Several labeled connections and no default: the call must say which, and
// the refusal lists the refs in the grammar the fix uses.
func TestLookupRefusesToPickAmongLabeledInstances(t *testing.T) {
	cfg := load(t, instanced)
	reg := pgRegistry(t)

	_, verr := Lookup(cfg, s3Cap(), "staging", reg)
	if verr == nil {
		t.Fatal("a bare ref resolved despite two labeled s3 instances and no default")
	}
	if verr.Code != "core.profile.instance.required" {
		t.Errorf("code = %q", verr.Code)
	}
	if !strings.Contains(verr.Hint, "staging/assets") || !strings.Contains(verr.Hint, "staging/logs") {
		t.Errorf("hint does not list the instances: %q", verr.Hint)
	}
}

// A label that misses is told apart from a namespace the profile does not
// cover — the fix is the list, not "configure pg".
func TestLookupNamesTheInstancesWhenALabelMisses(t *testing.T) {
	cfg := load(t, instanced)
	reg := pgRegistry(t)

	_, verr := Lookup(cfg, pgCap(), "staging/missing", reg)
	if verr == nil {
		t.Fatal("an unknown instance resolved")
	}
	if verr.Code != "core.profile.instance" {
		t.Errorf("code = %q", verr.Code)
	}
	if !strings.Contains(verr.Hint, "staging/analytics") {
		t.Errorf("hint does not list what exists: %q", verr.Hint)
	}
}

// A malformed ref is refused before anything is looked up.
func TestLookupRefusesAMalformedRef(t *testing.T) {
	cfg := load(t, instanced)
	reg := pgRegistry(t)
	for _, ref := range []string{"staging/", "staging/a/b", "staging/UPPER"} {
		if _, verr := Lookup(cfg, pgCap(), ref, reg); verr == nil || verr.Code != "core.profile.invalid" {
			t.Errorf("Lookup(%q) = %v, want core.profile.invalid", ref, verr)
		}
	}
}

// A duplicated entry only breaks calls that address it: pg/analytics twice
// leaves the pg default resolvable, while Check still reports the profile.
func TestADuplicateBreaksOnlyTheEntryItDuplicates(t *testing.T) {
	cfg := load(t, `
profiles:
  staging:
    plugins:
      pg:
        set: {host: main.internal}
      pg/analytics:
        set: {host: a.internal}
      pg/analytics@aaaaaaaaaaaa:
        set: {host: b.internal}
`)
	reg := pgRegistry(t)

	if _, verr := Lookup(cfg, pgCap(), "staging", reg); verr != nil {
		t.Errorf("the default instance is broken by an unrelated duplicate: %v", verr)
	}
	_, verr := Lookup(cfg, pgCap(), "staging/analytics", reg)
	if verr == nil || verr.Code != "core.profile.duplicate" {
		t.Errorf("addressing the duplicated entry = %v, want core.profile.duplicate", verr)
	}
}

// The stamp a grant pins is the addressed instance's, and an ambiguous bare
// ref stamps to nothing — the fail-closed direction.
func TestConnStampForResolvesInstances(t *testing.T) {
	cfg := load(t, instanced)

	def := ConnStampFor(cfg, "staging", "pg")
	analytics := ConnStampFor(cfg, "staging/analytics", "pg")
	if def == "" || analytics == "" {
		t.Fatal("an instance ref stamped to nothing")
	}
	if def == analytics {
		t.Error("two instances share a stamp — a grant for one would keep matching the other")
	}
	if got := ConnStampFor(cfg, "staging", "s3"); got != "" {
		t.Errorf("an ambiguous bare ref stamped to %q, want nothing", got)
	}
	if got := ConnStampFor(cfg, "staging/assets", "s3"); got == "" {
		t.Error("naming the instance did not stamp")
	}
}

// The RTA_PROFILE_* channel belongs to the default instance only: every
// separator an env token can carry is one a profile name can also produce,
// so a labeled instance's variable would be forgeable by naming a profile
// carefully.
func TestBindIgnoresTheEnvChannelForLabeledInstances(t *testing.T) {
	conn := config.Connection{}
	look := func(key string) (string, bool) {
		if key == plugin.ProfileEnvVar("staging", "password") {
			return "hunter2-marker", true
		}
		return "", false
	}
	if got := Bind("staging", conn, pgCap(), look); got["password"] != "hunter2-marker" {
		t.Errorf("the default instance lost its env channel: %v", got)
	}
	if got := Bind("staging/analytics", conn, pgCap(), look); len(got) != 0 {
		t.Errorf("a labeled instance read from the environment: %v", got)
	}
}
