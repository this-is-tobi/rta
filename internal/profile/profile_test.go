package profile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func load(t *testing.T, body string) config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", path)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func run(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }

func pgCap() plugin.Capability {
	return plugin.Capability{
		ID: "pg.query", Summary: "query", Safety: plugin.Read, Run: run,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Config: "host", Local: true},
			{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true},
			{Name: "sql", Type: plugin.String},
		},
	}
}

func s3Cap() plugin.Capability {
	return plugin.Capability{
		ID: "s3.ls", Summary: "ls", Safety: plugin.Read, Run: run,
		Inputs: []plugin.Field{
			{Name: "endpoint", Type: plugin.String, Config: "endpoint", Local: true},
			{Name: "prefix", Type: plugin.String},
		},
	}
}

func pgRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "pg", Summary: "pg", Capabilities: []plugin.Capability{pgCap()},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(plugin.Plugin{
		Name: "s3", Summary: "s3", Capabilities: []plugin.Capability{s3Cap()},
	}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// stagingPG is the smallest valid environment: one plugin, one value.
const stagingPG = `
profiles:
  staging:
    plugins:
      pg:
        set:
          host: s.internal
`

// A profile in a working-directory config file is refused, not quietly ignored.
//
// config.Path() falls back to ./.rta.yaml when os.UserConfigDir() fails —
// ordinary under `env -i`, in a container, in CI — so without this rule a
// cloned repository could ship a .rta.yaml defining "prod" and point it at the
// operator's own cluster. Refused rather than ignored because ignoring it runs
// the call against the base connection while the person believes they are
// talking to the profile.
func TestAProfileFromAnUntrustedFileIsRefused(t *testing.T) {
	// A named path is trusted: that is somebody's deliberate choice.
	trusted := load(t, stagingPG)
	if !trusted.Profiles["staging"].Trusted() {
		t.Fatal("a profile read from an explicit $RTA_CONFIG was treated as untrusted")
	}
	if _, verr := Lookup(trusted, pgCap(), "staging", pgRegistry(t)); verr != nil {
		t.Errorf("a trusted profile was refused: %v", verr)
	}

	// The fallback path is not. Simulated by taking away both the explicit
	// path and any user config dir, which is exactly the condition
	// config.Path() falls back under.
	t.Setenv("RTA_CONFIG", "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	untrusted := trusted
	// Built directly rather than reloaded: the loader stamps trusted, and a
	// zero-valued Profile is precisely what it stamps for the fallback path.
	untrusted.Profiles = map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{
			"pg": {Set: map[string]any{"host": "attacker.internal"}},
		}},
	}
	_, verr := Lookup(untrusted, pgCap(), "staging", pgRegistry(t))
	if verr == nil {
		t.Fatal("a profile from a working-directory file was honoured")
	}
	if verr.Code != "core.profile.untrusted" {
		t.Errorf("refused with %q, want core.profile.untrusted", verr.Code)
	}
}

// One environment spans several plugins, which is the whole point of the shape:
// "proj1-staging" is where the database, the bucket and the vault all are, and
// switching to it has to move all of them at once.
func TestOneEnvironmentCoversSeveralPlugins(t *testing.T) {
	cfg := load(t, `
profiles:
  proj1-staging:
    note: project one, staging
    plugins:
      pg:
        set:
          host: staging.db.internal
      s3:
        set:
          endpoint: minio.staging
`)
	reg := pgRegistry(t)
	p := cfg.Profiles["proj1-staging"]
	if got := p.Namespaces(); len(got) != 2 || got[0] != "pg" || got[1] != "s3" {
		t.Fatalf("Namespaces() = %v, want [pg s3]", got)
	}
	if problems := Check(cfg, reg); len(problems) != 0 {
		t.Fatalf("a valid two-plugin environment was reported: %v", problems)
	}

	pg, verr := Lookup(cfg, pgCap(), "proj1-staging", reg)
	if verr != nil {
		t.Fatal(verr)
	}
	if got := Bind("proj1-staging", pg, pgCap(), noEnv)["host"]; got != "staging.db.internal" {
		t.Errorf("pg host = %v, want the environment's", got)
	}
	s3, verr := Lookup(cfg, s3Cap(), "proj1-staging", reg)
	if verr != nil {
		t.Fatal(verr)
	}
	if got := Bind("proj1-staging", s3, s3Cap(), noEnv)["endpoint"]; got != "minio.staging" {
		t.Errorf("s3 endpoint = %v, want the environment's", got)
	}
}

// Naming an environment that says nothing about this plugin is an error;
// *being switched on* to one is not.
//
// The difference is entirely how the name arrived. `--profile proj1-prod` on a
// command pg does not appear in is somebody asking for something that cannot
// happen. Being in proj1-prod and running a `sys` command is not a mistake at
// all — an environment does not have to contain every plugin, and refusing
// there would make switching on cost every command the profile is silent about.
func TestAmbientIsSilentWhereExplicitIsAnError(t *testing.T) {
	cfg := load(t, stagingPG)
	reg := pgRegistry(t)

	if _, verr := Lookup(cfg, s3Cap(), "staging", reg); verr == nil {
		t.Error("--profile staging was accepted for a plugin staging says nothing about")
	} else if verr.Code != "core.profile.mismatch" {
		t.Errorf("refused with %q, want core.profile.mismatch", verr.Code)
	}

	name, _, verr := Ambient(cfg, s3Cap(), "staging", reg)
	if verr != nil {
		t.Fatalf("being switched on cost a command the environment is silent about: %v", verr)
	}
	if name != "" {
		t.Errorf("Ambient named %q for a plugin the profile does not cover", name)
	}

	// A profile that says something *broken* about it still fails, because that
	// is a statement, and running somewhere else instead is the wrong answer
	// delivered confidently.
	broken := load(t, `
profiles:
  staging:
    plugins:
      pg:
        set:
          hsot: typo.internal
`)
	if _, _, verr := Ambient(broken, pgCap(), "staging", reg); verr == nil {
		t.Error("a broken statement about this plugin was silently skipped")
	}
}

// A profile may fill where a call goes, and never what it reads or where it
// writes.
func TestAProfileMayNotFillAPathOrTheScope(t *testing.T) {
	c := plugin.Capability{
		ID: "kv.get", Summary: "get", Safety: plugin.Read, Scope: "key",
		Inputs: []plugin.Field{
			{Name: "key", Type: plugin.String, Config: "key"},
			{Name: "out", Type: plugin.Path, Local: true, EnvFallback: true},
			{Name: "identity", Type: plugin.Path, Config: "identity"},
			{Name: "host", Type: plugin.String, Config: "host"},
		},
	}
	for _, f := range c.Inputs {
		got := plugin.ProfileFillable(c, f)
		want := f.Name == "host"
		if got != want {
			t.Errorf("ProfileFillable(%s) = %v, want %v", f.Name, got, want)
		}
	}
	// And Bind honours it: a profile naming a refused key contributes nothing.
	conn := config.Connection{Set: map[string]any{
		"key": "somebody-elses-secret", "identity": "/tmp/theirs", "host": "ok.internal",
	}}
	got := Bind("x", conn, c, noEnv)
	if _, leaked := got["key"]; leaked {
		t.Error("a profile filled the input the grant is checked against")
	}
	if _, leaked := got["identity"]; leaked {
		t.Error("a profile filled a Path, which the MCP path guard never sees")
	}
	if got["host"] != "ok.internal" {
		t.Errorf("host = %v, want the profile's value", got["host"])
	}
}

// Set is keyed by config key and arrives under the input name.
//
// The two are not the same thing — plugins/vault declares one input named
// "mount" against two keys, kv-mount and transit-mount — so this translation
// is what lets an operator write the key they would write under plugins: and
// have a handler read the input it declared.
func TestSetIsWrittenAsAConfigKeyAndArrivesAsAnInput(t *testing.T) {
	c := plugin.Capability{
		ID: "vault.kv.list", Summary: "list", Safety: plugin.Read, Run: run,
		Inputs: []plugin.Field{
			{Name: "mount", Type: plugin.String, Config: "kv-mount"},
		},
	}
	got := Bind("x", config.Connection{Set: map[string]any{"kv-mount": "team"}}, c, noEnv)
	if got["mount"] != "team" {
		t.Errorf("bound %v, want the value to arrive under the input name \"mount\"", got)
	}
	// And the input name is not itself a key: writing `mount:` addresses
	// nothing, which is what `rta doctor` reports rather than silently applying.
	got = Bind("x", config.Connection{Set: map[string]any{"mount": "team"}}, c, noEnv)
	if len(got) != 0 {
		t.Errorf("bound %v, want nothing — `mount` is an input name, not a config key", got)
	}
}

// An external plugin's entry must name the artifact, not just the namespace.
//
// Registration is first-come and $PATH decides the order, which is why
// plugins: sections are pinned already. A bare namespace here would hand
// whatever binary won that race the operator's stated connection.
func TestAProfileForAnExternalPluginMustBePinned(t *testing.T) {
	reg := registry.New()
	origin := registry.Origin{Path: "/usr/local/bin/rta-plugin-pg", Digest: "1a2b3c4d5e6f"}
	if err := reg.RegisterFrom(plugin.Plugin{
		Name: "pg", Summary: "pg", Capabilities: []plugin.Capability{pgCap()},
	}, origin); err != nil {
		t.Fatal(err)
	}

	problems := Check(load(t, stagingPG), reg)
	if len(problems) == 0 {
		t.Fatal("a bare namespace was accepted for an external plugin — a $PATH impostor " +
			"would be handed the operator's connection")
	}
	if !strings.Contains(problems[0].Hint, origin.Short()) {
		t.Errorf("the problem does not name the installed digest: %v", problems[0])
	}
	if problems[0].Plugin != "pg" {
		t.Errorf("the problem does not say which entry to fix: %+v", problems[0])
	}

	if len(Check(load(t, envFor("pg@000000000000")), reg)) == 0 {
		t.Error("a pin that does not match the installed artifact was accepted")
	}
	if problems := Check(load(t, envFor("pg@1a2b3c")), reg); len(problems) != 0 {
		t.Errorf("a correctly pinned profile was reported as a problem: %v", problems)
	}
}

// envFor writes the one-plugin environment under an arbitrary plugins: key.
func envFor(key string) string {
	return "profiles:\n  staging:\n    plugins:\n      " + key +
		":\n        set:\n          host: s.internal\n"
}

// A built-in has no artifact to pin, so a pin on one is a check that is not
// happening.
func TestAProfileForABuiltInRefusesAPin(t *testing.T) {
	problems := Check(load(t, envFor("pg@deadbeef")), pgRegistry(t))
	if len(problems) == 0 {
		t.Fatal("a pin on a built-in was accepted")
	}
	if !strings.Contains(problems[0].Reason, "built in") {
		t.Errorf("the reason does not say why: %v", problems[0])
	}
}

// A key nothing reads is reported, and a key that is deliberately unfillable
// says so in different words.
func TestCheckReportsAKeyNothingReads(t *testing.T) {
	cfg := load(t, `
profiles:
  staging:
    plugins:
      pg:
        set:
          hsot: typo.internal
`)
	problems := Check(cfg, pgRegistry(t))
	if len(problems) == 0 {
		t.Fatal("a misspelled config key was accepted, so the operator's value silently does nothing")
	}
	if !strings.Contains(problems[0].Reason, "hsot") {
		t.Errorf("the problem does not name the key: %v", problems[0])
	}
	if problems[0].Plugin != "pg" {
		t.Errorf("the problem does not say which entry it is about: %+v", problems[0])
	}
}

// What `rta profile list` calls invalid is exactly what refuses to resolve.
//
// The pin was briefly advisory: Check reported an unpinned profile as invalid
// while Lookup honoured it, so `rta profile list` printed "invalid" for a
// profile that connected perfectly well. A check that appears on a report and
// not on the path that uses the value is a label, not a check — and this is
// the one it matters most for, since an unpinned profile hands whichever
// binary answered to the namespace the operator's stated connection.
func TestEveryProfileCheckRejectsIsAlsoRefusedAtUse(t *testing.T) {
	reg := registry.New()
	origin := registry.Origin{Path: "/usr/local/bin/rta-plugin-pg", Digest: "1a2b3c4d5e6f"}
	if err := reg.RegisterFrom(plugin.Plugin{
		Name: "pg", Summary: "pg", Capabilities: []plugin.Capability{pgCap()},
	}, origin); err != nil {
		t.Fatal(err)
	}

	for name, key := range map[string]string{
		"unpinned": "pg",
		"stale":    "pg@000000000000",
		"empty":    "pg@",
	} {
		cfg := load(t, envFor(key))
		if len(Check(cfg, reg)) == 0 {
			t.Errorf("%s: Check reported no problem", name)
		}
		if _, verr := Lookup(cfg, pgCap(), "staging", reg); verr == nil {
			t.Errorf("%s: Check calls this invalid and Lookup resolved it anyway", name)
		}
	}

	// And the matching pin works on both.
	ok := load(t, envFor("pg@1a2b3c"))
	if problems := Check(ok, reg); len(problems) != 0 {
		t.Errorf("a correctly pinned profile was reported: %v", problems)
	}
	if _, verr := Lookup(ok, pgCap(), "staging", reg); verr != nil {
		t.Errorf("a correctly pinned profile was refused: %v", verr)
	}

	// A nil origin resolves nothing, so it refuses rather than waving through.
	if _, verr := Lookup(ok, pgCap(), "staging", nil); verr == nil {
		t.Error("a caller that forgot to wire the origin accessor got a profile anyway")
	}
}

// A key rta does not act on is refused, never swallowed.
//
// goccy-yaml drops an unrecognised key without a word, so before this an
// operator could write a complete kube:/secret:/from: block — a coordinate, a
// Secret name and a key mapping — and get a profile that did none of it, with
// `rta profile show` printing neither the coordinate nor a complaint. A
// profile is where a silently-ignored key costs the most, because the thing
// quietly not happening is which server the call reaches.
func TestAProfileKeyThatDoesNothingIsRefused(t *testing.T) {
	reg := pgRegistry(t)

	// The cluster keys, which this release does act on. They were refused by
	// name while the tunnel was unwired; the assertion is now the other way
	// round, and it is the one that would catch the refusal being left behind
	// after the thing it guarded shipped — a profile an operator can write and
	// rta silently declines is indistinguishable from one rta cannot parse.
	// Against the tunnelled registry, because a coordinate is only valid for a
	// plugin a forward can fill (core.profile.untunnellable otherwise).
	// `set:` states a non-endpoint key: a `set: host` beside the coordinate
	// is itself a refusal now (checkSet), and this fixture is about the keys
	// being recognised, not about that conflict.
	cluster := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        kube: c/n/svc/pg:5432
        secrets:
          password: kube:creds/password
        set:
          database: app
`)
	if problems := Check(cluster, tunnelledRegistry(t)); len(problems) != 0 {
		t.Errorf("a profile stating a wired cluster coordinate was reported as a problem: %v", problems)
	}
	// Reported AND resolved — the pin's lesson in the other direction: the
	// report and the path that uses the value have to agree, whichever way
	// they agree.
	if _, verr := Lookup(cluster, tunnelCap(), "homelab", tunnelledRegistry(t)); verr != nil {
		t.Errorf("Check calls this valid and Lookup refused it: %v", verr)
	}

	// And a plain typo, which is the same failure one keystroke away.
	typo := load(t, `
profiles:
  p:
    plguins:
      pg:
        set:
          host: x
`)
	problems := Check(typo, reg)
	if len(problems) == 0 {
		t.Fatal("a misspelled key was accepted")
	}
	if !strings.Contains(problems[0].Reason, "plguins") {
		t.Errorf("the problem does not name the misspelled key, so it sends somebody "+
			"looking for a line that is right in front of them: %v", problems[0])
	}
	if _, verr := Lookup(typo, pgCap(), "p", reg); verr == nil {
		t.Error("a profile whose only content is a typo resolved")
	}

	// One level down, inside a plugin entry.
	inner := load(t, `
profiles:
  p:
    plugins:
      pg:
        st:
          host: x
`)
	if len(Check(inner, reg)) == 0 {
		t.Error("a misspelled key inside a plugin entry was accepted")
	}

	// A profile using only the keys this release acts on is untouched.
	fine := load(t, `
profiles:
  ok:
    note: hi
    ttl: 2h
    plugins:
      pg:
        set:
          host: x
`)
	if problems := Check(fine, reg); len(problems) != 0 {
		t.Errorf("a valid profile was reported: %v", problems)
	}
	if _, verr := Lookup(fine, pgCap(), "ok", reg); verr != nil {
		t.Errorf("a valid profile was refused: %v", verr)
	}
}

// A file written in the pre-environment shape gets a migration instruction, not
// "plugin, which nothing reads".
//
// The generic message is technically true and useless. Somebody whose config
// worked last release wants to be told what it became.
func TestTheOldSinglePluginShapeSaysWhatItBecame(t *testing.T) {
	reg := pgRegistry(t)
	old := load(t, `
profiles:
  staging:
    plugin: pg
    set:
      host: s.internal
`)
	problems := Check(old, reg)
	if len(problems) == 0 {
		t.Fatal("a profile in the old shape was accepted, and it configures nothing")
	}
	if !strings.Contains(problems[0].Reason, "old single-plugin shape") {
		t.Errorf("the report does not say what happened: %v", problems[0])
	}
	if !strings.Contains(problems[0].Hint, "plugins:") {
		t.Errorf("the hint does not say what to write instead: %v", problems[0])
	}
	_, verr := Lookup(old, pgCap(), "staging", reg)
	if verr == nil {
		t.Fatal("a profile in the old shape resolved")
	}
	if verr.Code != "core.profile.legacy" {
		t.Errorf("refused with %q, want core.profile.legacy", verr.Code)
	}
}

// A ttl that cannot be read is refused rather than silently meaning "forever".
//
// The quiet direction is the dangerous one here: a production environment whose
// deadline does not parse is a production environment that never switches off.
func TestAnUnreadableTTLIsRefused(t *testing.T) {
	reg := pgRegistry(t)
	cfg := load(t, `
profiles:
  prod:
    ttl: 2hh
    plugins:
      pg:
        set:
          host: p
`)
	if len(Check(cfg, reg)) == 0 {
		t.Error("a ttl that does not parse was accepted, so the switch never lapses")
	}
	if _, verr := Lookup(cfg, pgCap(), "prod", reg); verr == nil {
		t.Error("Check calls this invalid and Lookup resolved it anyway")
	}

	good := load(t, `
profiles:
  prod:
    ttl: 90m
    plugins:
      pg:
        set:
          host: p
`)
	d, has := good.Profiles["prod"].Window()
	if !has || d != 90*time.Minute {
		t.Errorf("Window() = %v, %v, want 90m", d, has)
	}
}

// A `secrets:` reference is fetched only where a handler is about to run, and
// only through a reader the caller supplied.
func TestASecretReferenceIsResolvedOnlyWhereItCanBe(t *testing.T) {
	reg := pgRegistry(t)
	cfg := load(t, `
profiles:
  prod:
    plugins:
      pg:
        set:
          host: p.internal
        secrets:
          password: kv:prod-db-password
`)
	conn, verr := Lookup(cfg, pgCap(), "prod", reg)
	if verr != nil {
		t.Fatal(verr)
	}

	// Bind is pure: it never reaches for the entry, whatever a reader would
	// have said. This is what keeps completion and the TUI's form seed free of
	// a store unlock on every keystroke.
	if got := Bind("prod", conn, pgCap(), noEnv); got["password"] != nil {
		t.Errorf("Bind resolved a secret reference: %v", got)
	}

	// Fill does, through the injected reader.
	asked := ""
	read := func(ref string) (string, *view.Error) {
		asked = ref
		return "from-the-store", nil
	}
	got, verr := Fill(context.Background(), "prod", conn, pgCap(), nil, noEnv, read)
	if verr != nil {
		t.Fatal(verr)
	}
	if asked != "prod-db-password" {
		t.Errorf("asked for %q, want the entry the operator named", asked)
	}
	if got["password"] != "from-the-store" {
		t.Errorf("password = %v, want the value the reader returned", got["password"])
	}

	// A caller who typed one wins, and nothing is fetched for it.
	asked = ""
	got, verr = Fill(context.Background(), "prod", conn, pgCap(), map[string]any{"password": "typed"}, noEnv, read)
	if verr != nil {
		t.Fatal(verr)
	}
	if asked != "" {
		t.Errorf("the store was opened for an input the caller had already supplied (%q)", asked)
	}
	if _, filled := got["password"]; filled {
		t.Error("a profile overwrote a value the caller typed")
	}

	// A surface with no reader gets no secret, and is told so rather than
	// silently connecting without one.
	if _, verr := Fill(context.Background(), "prod", conn, pgCap(), nil, noEnv, nil); verr == nil {
		t.Error("a surface that cannot open the store resolved a secret anyway")
	}

	// And a reference onto an input this capability does not have is refused,
	// rather than discovered as an authentication failure three steps later —
	// at resolution now (checkSecretRefs), not only at fill time.
	bad := load(t, `
profiles:
  prod:
    plugins:
      pg:
        set:
          host: p
        secrets:
          nosuchinput: kv:x
`)
	if _, verr := Lookup(bad, pgCap(), "prod", reg); verr == nil {
		t.Error("a secret mapped onto an input the plugin does not declare resolved")
	} else if verr.Code != "core.profile.secrets" {
		t.Errorf("code = %s, want core.profile.secrets", verr.Code)
	}
	// Fill keeps its own refusal, for the caller that reached it without
	// resolving — its check is per-capability where Lookup's is per-namespace,
	// and dropping the belt would leave that finer case silent.
	if _, verr := Fill(context.Background(), "prod",
		bad.Profiles["prod"].Plugins["pg"], pgCap(), nil, noEnv, read); verr == nil {
		t.Error("a secret mapped onto an input the plugin does not declare was accepted")
	}
}

// A reference with no scheme is refused rather than guessed at.
func TestASecretReferenceMustNameItsSource(t *testing.T) {
	reg := pgRegistry(t)
	cfg := load(t, `
profiles:
  prod:
    plugins:
      pg:
        set:
          host: p
        secrets:
          password: prod-db-password
`)
	if len(Check(cfg, reg)) == 0 {
		t.Error("a bare entry name was accepted; it is ambiguous the day a second source exists")
	}
	if _, verr := Lookup(cfg, pgCap(), "prod", reg); verr == nil {
		t.Error("Check calls this invalid and Lookup resolved it anyway")
	}
}

func noEnv(string) (string, bool) { return "", false }

// notRun is an Installed that also knows what discovery refused to launch,
// which is how internal/app wires the registry in the real binary.
type notRun struct {
	Installed
	names []string
}

func (n notRun) Untrusted(namespace string) bool {
	for _, s := range n.names {
		if s == namespace {
			return true
		}
	}
	return false
}

// "Installed and not run" is a different problem from "not installed", and
// telling them apart is the difference between a one-line approval and an
// afternoon spent checking $PATH.
//
// Trust is keyed on the digest, so rebuilding a plugin drops its approval and
// the plugin stops registering — an ordinary Tuesday for anyone developing
// one. The profile that names it is then refused whole, which is how one
// rebuilt plugin takes an environment spanning four working ones off the air,
// and the sentence explaining it sent the operator looking for a missing
// install.
func TestAProfileNamingAnUnapprovedPluginSaysSo(t *testing.T) {
	reg := registry.New()
	pinned := "weather@abcdef123456"

	t.Run("installed and not run", func(t *testing.T) {
		verr := checkPin(pinned, notRun{Installed: reg, names: []string{"weather"}})
		if verr == nil {
			t.Fatal("an unapproved plugin was accepted")
		}
		if verr.Code != "core.profile.untrustedplugin" {
			t.Errorf("code = %q, want the unapproved one", verr.Code)
		}
		if !strings.Contains(verr.Hint, "rta plugin trust weather") {
			t.Errorf("hint = %q, want the command that resolves it", verr.Hint)
		}
		if strings.Contains(verr.Error(), "not a registered plugin") {
			t.Errorf("still blames a missing install: %s", verr.Error())
		}
	})

	// Genuinely absent keeps the older sentence — the distinction only exists
	// because there are two causes, and collapsing them the other way would be
	// the same defect pointing at the other one.
	t.Run("not installed at all", func(t *testing.T) {
		verr := checkPin(pinned, notRun{Installed: reg})
		if verr == nil {
			t.Fatal("an unknown plugin was accepted")
		}
		if verr.Code != "core.profile.unknownplugin" {
			t.Errorf("code = %q, want the unknown-plugin one", verr.Code)
		}
	})

	// An Installed that cannot answer the question gets the weaker wording
	// rather than a panic: the interface is asserted, not required.
	t.Run("an Installed that does not know", func(t *testing.T) {
		verr := checkPin(pinned, reg)
		if verr == nil || verr.Code != "core.profile.unknownplugin" {
			t.Errorf("verr = %v, want the unknown-plugin refusal", verr)
		}
	})
}

// A coordinate the plugin cannot be pointed at is refused at resolution, not
// warned about on a page.
//
// Endpoint roles are declared by the installed artifact, so a plugin binary
// built before they existed declares none — and a `kube:` connection against
// one opens the forward, fills nothing, and sends the call to the plugin's
// own default host. Real data from the wrong server, under a badge naming
// the cluster: found in the field, by an operator whose freshly-written
// profile carried the page's "problem" row while every run sailed past it.
func TestACoordinateThePluginCannotBePointedAtIsRefused(t *testing.T) {
	cfg := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        kube: homelab/databases/svc/postgres:5432
`)
	_, verr := Lookup(cfg, pgCap(), "homelab", pgRegistry(t))
	if verr == nil {
		t.Fatal("a profile whose plugin declares no endpoint role resolved, so the " +
			"call would run against the default host while the forward is ignored")
	}
	if verr.Code != "core.profile.untunnellable" {
		t.Errorf("code = %s, want core.profile.untunnellable", verr.Code)
	}

	// Check reports the same rule in the same words, so the list and the run
	// cannot disagree about this profile.
	found := false
	for _, p := range Check(cfg, pgRegistry(t)) {
		if strings.Contains(p.Reason, "no input a tunnel can fill") {
			found = true
		}
	}
	if !found {
		t.Error("Check does not report what Lookup refuses")
	}

	// And the same coordinate against a plugin that does declare roles
	// resolves — the refusal is about the artifact, not the coordinate.
	reg := registry.New()
	tunnelled := plugin.Capability{
		ID: "pg.query", Summary: "query", Safety: plugin.Read, Run: run,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Config: "host", Local: true,
				Endpoint: plugin.EndpointHost, Help: "host"},
			{Name: "port", Type: plugin.Int, Default: 5432, Config: "port", Local: true,
				Endpoint: plugin.EndpointPort, Min: 1, Max: 65535, Help: "port"},
		},
	}
	if err := reg.Register(plugin.Plugin{Name: "pg", Summary: "pg",
		Capabilities: []plugin.Capability{tunnelled}}); err != nil {
		t.Fatal(err)
	}
	if _, verr := Lookup(cfg, tunnelled, "homelab", reg); verr != nil {
		t.Errorf("a plugin with endpoint roles was refused the same coordinate: %s", verr.Message)
	}
}

// A `set:` endpoint key beside a coordinate is refused at resolution, not
// warned about on a page.
//
// Dial deliberately lays the forward's endpoint over `set:` — two statements
// about the destination, and the forward is the one that exists — which
// keeps the run right and leaves the file naming a destination no run will
// ever read. checkSet feeds Lookup, so the file is fixed rather than
// tolerated; a row Check reports but Lookup honours would be the
// page-versus-run drift this package has removed five times.
func TestAnEndpointSetKeyBesideACoordinateIsRefused(t *testing.T) {
	reg := registry.New()
	tunnelled := plugin.Capability{
		ID: "pg.query", Summary: "query", Safety: plugin.Read, Run: run,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Config: "host", Local: true,
				Endpoint: plugin.EndpointHost, Help: "host"},
			{Name: "port", Type: plugin.Int, Default: 5432, Config: "port", Local: true,
				Endpoint: plugin.EndpointPort, Min: 1, Max: 65535, Help: "port"},
			{Name: "database", Type: plugin.String, Config: "database", Help: "database"},
		},
	}
	if err := reg.Register(plugin.Plugin{Name: "pg", Summary: "pg",
		Capabilities: []plugin.Capability{tunnelled}}); err != nil {
		t.Fatal(err)
	}

	cfg := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        kube: homelab/databases/svc/postgres:5432
        set:
          host: stale.internal
          database: app
`)
	_, verr := Lookup(cfg, tunnelled, "homelab", reg)
	if verr == nil {
		t.Fatal("a profile stating both a coordinate and set.host resolved — the file " +
			"names two destinations and one of them is dead")
	}
	if verr.Code != "core.profile.set" {
		t.Errorf("code = %s, want core.profile.set", verr.Code)
	}
	if !strings.Contains(verr.Message, "overridden by the forward") {
		t.Errorf("message = %q, want it to say the forward shadows the key", verr.Message)
	}

	// Check reports the same rule in the same words, so the list and the run
	// cannot disagree about this profile.
	found := false
	for _, p := range Check(cfg, reg) {
		if strings.Contains(p.Reason, "overridden by the forward") {
			found = true
		}
	}
	if !found {
		t.Error("Check does not report what Lookup refuses")
	}

	// The refusal is about the shadowed key, not the pairing of set: with
	// kube: — the same coordinate over only non-endpoint keys resolves.
	clean := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        kube: homelab/databases/svc/postgres:5432
        set:
          database: app
`)
	if _, verr := Lookup(clean, tunnelled, "homelab", reg); verr != nil {
		t.Errorf("a non-endpoint set: key beside the coordinate was refused: %s", verr.Message)
	}
}

// A `secrets:` mapping onto an input the coordinate's forward fills is
// refused at resolution — the mapping's twin of the `set:` rule.
//
// Fill resolves the mapping and Dial then lays the forward's endpoint over
// everything Fill produced, so the credential is fetched — a real read, in
// the store's or cluster's audit log — and discarded, on every call. The
// file says the host comes from a secret; the run's host comes from the
// forward. Two statements, one dead, same as `set: host` beside `kube:`.
func TestASecretMappedOntoAForwardFilledInputIsRefused(t *testing.T) {
	reg := tunnelledRegistry(t)
	cfg := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        kube: homelab/databases/svc/postgres:5432
        secrets:
          host: kv:db-host
          password: kv:db-pass
`)
	_, verr := Lookup(cfg, tunnelCap(), "homelab", reg)
	if verr == nil {
		t.Fatal("a secret mapped onto the forward-filled host resolved — fetched and " +
			"discarded on every call")
	}
	if verr.Code != "core.profile.secrets" {
		t.Errorf("code = %s, want core.profile.secrets", verr.Code)
	}
	if !strings.Contains(verr.Message, "overridden by the forward") {
		t.Errorf("message = %q, want it to say the forward shadows the mapping", verr.Message)
	}

	// Check reports the same rule in the same words.
	found := false
	for _, p := range Check(cfg, reg) {
		if strings.Contains(p.Reason, "`secrets: host` is overridden by the forward") {
			found = true
		}
	}
	if !found {
		t.Error("Check does not report what Lookup refuses")
	}

	// The refusal is about the pair, both ways around: the same coordinate
	// with only a credential mapping resolves, and the same host mapping
	// with no coordinate — host from a local entry, connecting directly —
	// is an ordinary configuration.
	credOnly := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        kube: homelab/databases/svc/postgres:5432
        secrets:
          password: kv:db-pass
`)
	if _, verr := Lookup(credOnly, tunnelCap(), "homelab", reg); verr != nil {
		t.Errorf("a credential mapping beside the coordinate was refused: %s", verr.Message)
	}
	direct := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        secrets:
          host: kv:db-host
`)
	if _, verr := Lookup(direct, tunnelCap(), "homelab", reg); verr != nil {
		t.Errorf("a host from a local entry on a direct connection was refused: %s", verr.Message)
	}
}

// A `kube:` credential with no coordinate is refused at resolution, in the
// run path's own words — it was kubeSecrets' refusal alone, so the page
// called the profile valid and the first call failed.
func TestAClusterSecretWithoutACoordinateIsRefused(t *testing.T) {
	reg := tunnelledRegistry(t)
	cfg := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        secrets:
          password: kube:pg-creds/password
`)
	_, verr := Lookup(cfg, tunnelCap(), "homelab", reg)
	if verr == nil {
		t.Fatal("a cluster secret with nowhere to read from resolved")
	}
	if verr.Code != "core.profile.secrets" {
		t.Errorf("code = %s, want core.profile.secrets", verr.Code)
	}
	if !strings.Contains(verr.Message, "states no `kube:` coordinate") {
		t.Errorf("message = %q, want the run path's own words", verr.Message)
	}
	found := false
	for _, p := range Check(cfg, reg) {
		if strings.Contains(p.Reason, "states no `kube:` coordinate") {
			found = true
		}
	}
	if !found {
		t.Error("Check does not report what Lookup refuses")
	}
}

// A secret mapped onto a number is refused: the request readers do not
// coerce text, so the mapping would resolve, authenticate, and hand the
// handler a zero — the quiet-garbage variant of "never takes effect".
func TestASecretMappedOntoANumberIsRefused(t *testing.T) {
	reg := tunnelledRegistry(t)
	cfg := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        secrets:
          port: kv:db-port
`)
	_, verr := Lookup(cfg, tunnelCap(), "homelab", reg)
	if verr == nil {
		t.Fatal("a secret mapped onto an Int input resolved — the handler would read zero")
	}
	if verr.Code != "core.profile.secrets" || !strings.Contains(verr.Message, "read zero") {
		t.Errorf("verr = %s %q, want core.profile.secrets saying the handler reads zero",
			verr.Code, verr.Message)
	}
	found := false
	for _, p := range Check(cfg, reg) {
		if strings.Contains(p.Reason, "read zero") {
			found = true
		}
	}
	if !found {
		t.Error("Check does not report what Lookup refuses")
	}
	// A text-shaped target is the ordinary case and stays legal.
	ok := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        secrets:
          database: kv:db-name
`)
	if _, verr := Lookup(ok, tunnelCap(), "homelab", reg); verr != nil {
		t.Errorf("a String target was refused: %s", verr.Message)
	}
}
