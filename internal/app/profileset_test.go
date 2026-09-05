package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	grantbuiltin "github.com/this-is-tobi/rta/builtin/grant"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// `rta profile set` exists because everything else that writes a profile
// needs a terminal, and a team that cannot script setup writes the YAML by
// hand instead — the path where a credential ends up under `set:`, inert and
// in plaintext, and nothing says so until a connection fails to
// authenticate.
//
// So the tests that matter here are the refusals. A command that only writes
// faster than an editor is not worth having; what makes it worth having is
// that it will not write the things an editor happily does.

// setRegistry is a plugin with one of each shape a `--set` value can take: a
// string, an integer, a boolean, and a credential that must never be one.
func setRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	// The grant built-in rides along, because one test has to check that
	// removing an environment takes its grants with it — and a grant is
	// issued by a capability, not by an app command.
	if err := reg.Register(grantbuiltin.Plugin(reg.Capabilities)); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(plugin.Plugin{
		Name: "db", Summary: "db plugin",
		Capabilities: []plugin.Capability{{
			ID: "db.status", Summary: "status", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "host", Type: plugin.String, Default: "localhost", Config: "host",
					Local: true, Endpoint: plugin.EndpointHost, Help: "host"},
				{Name: "port", Type: plugin.Int, Default: 5432, Config: "port",
					Local: true, Endpoint: plugin.EndpointPort, Min: 1, Max: 65535, Help: "port"},
				{Name: "tls", Type: plugin.Bool, Default: true, Config: "tls", Local: true, Help: "tls"},
				{Name: "dbname", Type: plugin.String, Config: "dbname", Local: true, Help: "database"},
				{Name: "timeout", Type: plugin.Float, Config: "timeout", Local: true, Help: "seconds"},
				{Name: "sslmode", Type: plugin.String, Config: "sslmode", Local: true,
					Options: []string{"disable", "require"}, Help: "tls negotiation"},
				{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true, Help: "password"},
			},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return view.Text{Body: "reached " + req.String("host")}, nil
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// session is runWith for a test that needs more than one command to share a
// config file, a data directory and a registry — `rta grant allow` followed
// by `rta profile rm`, which is the only way to check that removing an
// environment takes its grants with it.
func session(t *testing.T, reg *registry.Registry) func(args ...string) (string, string, error) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(home, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", filepath.Join(home, "data"))
	t.Setenv("RTA_KV_PASSPHRASE", "")
	t.Setenv("RTA_KV_IDENTITY", "")
	SetInstalled(reg)
	t.Cleanup(func() { SetInstalled(nil) })
	return func(args ...string) (string, string, error) {
		root := NewRoot(reg, "test")
		var out, errOut bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs(args)
		err := root.ExecuteContext(context.Background())
		return out.String(), errOut.String(), err
	}
}

// configOf reads the file a run wrote, so an assertion is about what a later
// invocation will load rather than about what this one printed.
func configOf(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(config.Path())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// **A credential cannot be stated with `--set`.** This is the mistake the
// command exists to make impossible: `secrets:` holds a reference and `set:`
// holds a value, so a password in the wrong block is inert — and it is inert
// inside a 0644 file, which is the part that costs something.
func TestSetRefusesToWriteADeclaredCredential(t *testing.T) {
	const secret = "hunter2-must-not-be-written"
	_, errOut, err := runWith(t, setRegistry(t), "",
		"profile", "set", "staging", "--plugin", "db", "--set", "password="+secret)
	if err == nil {
		t.Fatal("a declared credential was accepted into `set:`")
	}
	if !strings.Contains(errOut, "core.profile.set.secret") {
		t.Errorf("refused for some other reason: %q", errOut)
	}
	// The refusal must not repeat the value. Somebody who typed a credential
	// on a command line has already put it in one place; rta will not put it
	// in a second.
	if strings.Contains(errOut, secret) {
		t.Errorf("the refusal echoed the credential: %q", errOut)
	}
	// And it must name the flag that does work, or the operator's next move
	// is to edit the file by hand — which is the path this replaces.
	if !strings.Contains(errOut, "--secret") {
		t.Errorf("the refusal does not say what to do instead: %q", errOut)
	}
	if body := configOf(t); strings.Contains(body, secret) || strings.Contains(body, "profiles:") {
		t.Errorf("a refused run still wrote to the config file:\n%s", body)
	}
}

// **`--secret` takes a reference, never a value.** The single most likely
// thing to be sitting where a scheme belongs is the credential itself, so
// this refusal is also the one that must say the least.
func TestSecretRefusesAValueWhereAReferenceBelongs(t *testing.T) {
	const secret = "hunter2-typed-in-the-wrong-flag"
	_, errOut, err := runWith(t, setRegistry(t), "",
		"profile", "set", "staging", "--plugin", "db", "--secret", "password="+secret)
	if err == nil {
		t.Fatal("a bare value was written as a `secrets:` reference")
	}
	if strings.Contains(errOut, secret) {
		t.Errorf("the refusal echoed what was passed: %q", errOut)
	}
	if !strings.Contains(errOut, "password") {
		t.Errorf("the refusal does not say which input it is about: %q", errOut)
	}
	// Worth saying out loud, because it is true and actionable: the thing is
	// in a history file now.
	if !strings.Contains(errOut, "history") {
		t.Errorf("the refusal does not mention where that value now is: %q", errOut)
	}
}

func TestAReferenceWithASchemeIsAccepted(t *testing.T) {
	out, errOut, err := runWith(t, setRegistry(t), "",
		"profile", "set", "staging", "--plugin", "db",
		"--set", "dbname=orders", "--secret", "password=kv:staging-db-password")
	if err != nil {
		t.Fatalf("a well-formed reference was refused: %v %q", err, errOut)
	}
	if !strings.Contains(out, "kv:staging-db-password") {
		t.Errorf("the card does not say where the credential comes from:\n%s", out)
	}
	if !strings.Contains(configOf(t), "password: kv:staging-db-password") {
		t.Errorf("the reference did not reach the file:\n%s", configOf(t))
	}
}

// **A value is written as the type the field declares.** A flag argument is
// always text and every reader downstream is a type assertion, so writing the
// string is how `port: "5432"` reaches a handler as 0 and `tls: "true"` as
// false. This is the one place that can decide, so it has to.
func TestSetWritesTheDeclaredTypeAndNotText(t *testing.T) {
	_, errOut, err := runWith(t, setRegistry(t), "",
		"profile", "set", "staging", "--plugin", "db",
		"--set", "port=6432", "--set", "tls=false", "--set", "dbname=orders",
		"--set", "timeout=2.5")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	body := configOf(t)
	for _, want := range []string{"port: 6432", "tls: false", "dbname: orders", "timeout: 2.5"} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q in the file:\n%s", want, body)
		}
	}
	for _, quoted := range []string{`port: "6432"`, `tls: "false"`} {
		if strings.Contains(body, quoted) {
			t.Errorf("%s was written as text, so the handler reads the zero:\n%s", quoted, body)
		}
	}
}

// The spellings somebody reaches for. `yes` is the one that matters: it is
// what people write when they mean true, and YAML 1.2 reads it as text — so
// the file version of this mistake silently turns off whatever the field
// turns on.
func TestSetRefusesAValueTheDeclaredTypeCannotHold(t *testing.T) {
	for _, tc := range []struct{ name, pair string }{
		{"yes for a bool", "tls=yes"},
		{"on for a bool", "tls=on"},
		{"words for an int", "port=six-thousand"},
		{"a decimal for an int", "port=64.32"},
		{"words for a float", "timeout=quickly"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errOut, err := runWith(t, setRegistry(t), "",
				"profile", "set", "staging", "--plugin", "db", "--set", tc.pair)
			if err == nil {
				t.Fatal("accepted — the handler would read the zero")
			}
			if !strings.Contains(errOut, "core.profile.set.type") {
				t.Errorf("refused for some other reason: %q", errOut)
			}
		})
	}
}

// A key nothing reads is refused in the words the report already uses, rather
// than in new ones invented here.
func TestSetRefusesAKeyThePluginDoesNotRead(t *testing.T) {
	_, errOut, err := runWith(t, setRegistry(t), "",
		"profile", "set", "staging", "--plugin", "db", "--set", "hsot=typo.internal")
	if err == nil {
		t.Fatal("a misspelled key was written, so the value would silently do nothing")
	}
	if !strings.Contains(errOut, "hsot") {
		t.Errorf("the refusal does not name the key: %q", errOut)
	}
}

// **Running it twice is running it once.** The thing this is for runs more
// than once — a provisioning script, a dotfiles bootstrap, a CI job — and a
// command that fails the second time gets wrapped in `|| true`, which
// silences every refusal above along with the one it was aimed at.
func TestSetIsIdempotent(t *testing.T) {
	args := []string{"profile", "set", "staging", "--note", "shared staging", "--ttl", "8h",
		"--plugin", "db", "--set", "host=db.internal", "--set", "port=6432"}
	reg := setRegistry(t)
	if _, errOut, err := runWith(t, reg, "", args...); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	first := configOf(t)
	// runWith points RTA_CONFIG at a fresh file each call, so the second run
	// starts from the first one's output rather than from nothing.
	path := config.Path()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := runWith(t, reg, string(body), args...); err != nil {
		t.Fatalf("the second run failed: %v %q", err, errOut)
	}
	if second := configOf(t); second != first {
		t.Errorf("the same command twice produced two files:\n--- first\n%s\n--- second\n%s",
			first, second)
	}
}

// A run that asks for a state the profile is already in says so, rather than
// reporting a write it did not make.
//
// The case this exists for: somebody is told a stray port-forward is breaking
// their plugin and types `--direct`. If the coordinate was never there, the
// file does not change and "updated me" tells them they fixed something that
// was never wrong — which is worse than saying nothing, because they stop
// looking for the real cause.
//
// Both directions, and the second is not decoration. Profile.Plugins is a map,
// so the obvious implementation compares two structs that share it and reports
// every edit as unchanged. That version passes the first half of this test.
func TestARunThatChangesNothingSaysSo(t *testing.T) {
	reg := setRegistry(t)
	create := []string{"profile", "set", "staging",
		"--plugin", "db", "--set", "host=db.internal"}
	out, errOut, err := runWith(t, reg, "", create...)
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "wrote") || !strings.Contains(out, "created staging") {
		t.Fatalf("creating a profile did not report a write:\n%s", out)
	}
	seed := configOf(t)

	// The same command again: nothing to do, and the file must be untouched.
	out, errOut, err = runWith(t, reg, seed, create...)
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "unchanged") {
		t.Errorf("a run that changed nothing reported a write:\n%s", out)
	}
	if strings.Contains(out, "wrote") {
		t.Errorf("a run that changed nothing said `wrote`:\n%s", out)
	}
	if after := configOf(t); after != seed {
		t.Errorf("a run that changed nothing rewrote the file:\n--- before\n%s\n--- after\n%s",
			seed, after)
	}

	// And a run that does change something still reports it. Without this the
	// aliasing bug above is invisible.
	out, errOut, err = runWith(t, reg, seed, "profile", "set", "staging",
		"--plugin", "db", "--set", "host=elsewhere.internal")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "wrote") || strings.Contains(out, "unchanged") {
		t.Errorf("a real change was reported as unchanged:\n%s", out)
	}
	if after := configOf(t); after == seed {
		t.Error("a real change did not reach the file")
	}
	if !strings.Contains(configOf(t), "elsewhere.internal") {
		t.Errorf("the new value is not in the file:\n%s", configOf(t))
	}
}

// Each block is replaced by what the flags state; a block no flag mentions is
// left exactly as it was. That is what lets one line of a script restate a
// connection without disturbing its credentials.
func TestEachBlockIsStatedSeparately(t *testing.T) {
	const before = `profiles:
  staging:
    plugins:
      db:
        set:
          host: old.internal
          dbname: orders
        secrets:
          password: kv:staging-db-password
`
	out, errOut, err := runWith(t, setRegistry(t), before,
		"profile", "set", "staging", "--plugin", "db", "--set", "host=new.internal")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	body := configOf(t)
	if !strings.Contains(body, "host: new.internal") {
		t.Errorf("the stated key was not written:\n%s", body)
	}
	if strings.Contains(body, "dbname: orders") {
		t.Errorf("`--set` states the whole block, so an omitted key should be gone:\n%s", body)
	}
	if !strings.Contains(body, "password: kv:staging-db-password") {
		t.Errorf("restating `set:` took `secrets:` with it — a connection lost its credential:\n%s", body)
	}
	if !strings.Contains(out, "kv:staging-db-password") {
		t.Errorf("the card does not show the surviving credential:\n%s", out)
	}
}

// The connection flags need to know which plugin they are about, and saying
// so is better than picking one.
//
// Against an *existing* profile deliberately. On a new one the "configures no
// plugin" refusal fires anyway, so a version of this that created one would
// pass whether or not the rule it is named after exists — and the case that
// matters is the silent one: a `--set` that reaches nothing while the command
// reports success.
func TestConnectionFlagsWithoutAPluginAreRefused(t *testing.T) {
	const before = `profiles:
  staging:
    plugins:
      db:
        set:
          host: db.internal
`
	_, errOut, err := runWith(t, setRegistry(t), before,
		"profile", "set", "staging", "--set", "host=other.internal")
	if err == nil {
		t.Fatal("a connection flag was accepted with nothing to attach it to, and did nothing")
	}
	if !strings.Contains(errOut, "core.profile.noplugin") {
		t.Errorf("refused for some other reason: %q", errOut)
	}
}

// `--set` states the whole block, so an empty value is either a key meant to
// be removed — done by omitting it — or a shell variable that expanded to
// nothing. The second is the one worth catching: a script whose `$DB_HOST` is
// unset would otherwise write a host of "" and connect to the declared
// default while claiming to have connected to staging.
func TestAnEmptySetValueIsRefused(t *testing.T) {
	_, errOut, err := runWith(t, setRegistry(t), "",
		"profile", "set", "staging", "--plugin", "db", "--set", "host=")
	if err == nil {
		t.Fatal("an empty value was accepted")
	}
	if !strings.Contains(errOut, "core.profile.set.empty") {
		t.Errorf("refused for some other reason: %q", errOut)
	}
	if !strings.Contains(errOut, "expanded") {
		t.Errorf("the refusal does not raise the likely cause: %q", errOut)
	}
}

// A profile that configures nothing is one `rta use` refuses, so creating one
// is creating something unusable.
func TestANewProfileHasToConfigureSomething(t *testing.T) {
	_, errOut, err := runWith(t, setRegistry(t), "",
		"profile", "set", "staging", "--note", "just a note")
	if err == nil {
		t.Fatal("an empty profile was created")
	}
	if !strings.Contains(errOut, "core.profile.empty") {
		t.Errorf("refused for some other reason: %q", errOut)
	}
}

// **A forward fills the endpoint inputs itself**, so a stated host beside a
// coordinate is a line no run reads — and the connection is refused over the
// pair. Which leaves what `--kube` alone should do to a key the file already
// held: remove it and say so, rather than refuse to add a forward because of
// a key set in some earlier command.
func TestAddingAForwardFreesTheKeysItFills(t *testing.T) {
	const before = `profiles:
  staging:
    plugins:
      db:
        set:
          host: old.internal
          port: 6432
          dbname: orders
`
	out, errOut, err := runWith(t, setRegistry(t), before,
		"profile", "set", "staging", "--plugin", "db", "--kube", "prod/db/svc/postgres:5432")
	if err != nil {
		t.Fatalf("adding a forward was refused over keys it replaces: %v %q", err, errOut)
	}
	body := configOf(t)
	for _, gone := range []string{"host: old.internal", "port: 6432"} {
		if strings.Contains(body, gone) {
			t.Errorf("%q survived beside a forward that overrides it:\n%s", gone, body)
		}
	}
	if !strings.Contains(body, "dbname: orders") {
		t.Errorf("a key the forward does not fill was removed too:\n%s", body)
	}
	// Removed, never silently. This is the operator's own line going away.
	if !strings.Contains(out, "set.host") || !strings.Contains(out, "set.port") {
		t.Errorf("the removal has no receipt:\n%s", out)
	}
}

// The same pair stated in one breath is refused, not repaired. Quietly
// dropping half of what somebody just typed is a different thing from
// clearing a line they are not looking at.
func TestAForwardAndAKeyItFillsInTheSameRunAreRefused(t *testing.T) {
	_, errOut, err := runWith(t, setRegistry(t), "",
		"profile", "set", "staging", "--plugin", "db",
		"--kube", "prod/db/svc/postgres:5432", "--set", "host=db.internal")
	if err == nil {
		t.Fatal("two contradictory statements about the destination were both written")
	}
	if !strings.Contains(errOut, "overridden by the forward") {
		t.Errorf("refused for some other reason: %q", errOut)
	}
}

func TestTwoForwardsAreRefused(t *testing.T) {
	_, errOut, err := runWith(t, setRegistry(t), "",
		"profile", "set", "staging", "--plugin", "db",
		"--kube", "prod/db/svc/postgres:5432", "--ssh", "bastion/db.internal:5432")
	if err == nil {
		t.Fatal("a connection was written stating two forwards")
	}
	if !strings.Contains(errOut, "one") {
		t.Errorf("the refusal does not say why: %q", errOut)
	}
}

// `--tunnel-tls` states something about the far side of a forward, so it is
// refused the moment there is no forward for it to describe — neither
// stated in this run nor already on file.
func TestTLSWithoutAForwardIsRefused(t *testing.T) {
	_, errOut, err := runWith(t, setRegistry(t), "",
		"profile", "set", "staging", "--plugin", "db", "--tunnel-tls")
	if err == nil {
		t.Fatal("--tunnel-tls with no kube: or ssh: was written")
	}
	if !strings.Contains(errOut, "forward") {
		t.Errorf("the refusal does not say why: %q", errOut)
	}
}

// Stated beside a forward in the same run, it is written as an ordinary peer
// of `kube:` — the CLI's half of what internal/profile.endpointValues then
// acts on.
func TestAddingATLSForwardWritesIt(t *testing.T) {
	_, _, err := runWith(t, setRegistry(t), "",
		"profile", "set", "staging", "--plugin", "db",
		"--kube", "prod/db/svc/postgres:5432", "--tunnel-tls")
	if err != nil {
		t.Fatalf("kube: with tunnelTLS: true was refused: %v", err)
	}
	body := configOf(t)
	if !strings.Contains(body, "tunnelTLS: true") {
		t.Errorf("tunnelTLS: true was not written:\n%s", body)
	}
}

// `--direct` removes the forward `tunnelTLS: true` describes, so it has to
// remove the statement too — left behind, it is not a stale value sitting
// quietly, it is a connection CheckConnection now refuses to resolve at
// all, over a flag that never mentioned it.
func TestDirectClearsAStoredTLSStatement(t *testing.T) {
	const before = `profiles:
  staging:
    plugins:
      db:
        kube: prod/db/svc/postgres:5432
        tunnelTLS: true
`
	_, _, err := runWith(t, setRegistry(t), before,
		"profile", "set", "staging", "--plugin", "db", "--direct")
	if err != nil {
		t.Fatalf("--direct was refused: %v", err)
	}
	body := configOf(t)
	if strings.Contains(body, "tunnelTLS: true") {
		t.Errorf("tunnelTLS: true survived --direct, which just removed the forward it describes:\n%s", body)
	}
}

// **Removing an environment revokes the grants naming it.** A grant for a
// name nothing can look up authorizes nothing, so leaving it behind is a row
// in `rta grant list` that reads like access and is not.
func TestRemovingAProfileRevokesTheGrantsNamingIt(t *testing.T) {
	run := session(t, setRegistry(t))
	if _, errOut, err := run("profile", "set", "staging", "--plugin", "db",
		"--set", "host=db.internal"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if _, errOut, err := run("profile", "set", "prod", "--plugin", "db",
		"--set", "host=prod.internal"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	// One grant naming it and one that does not, so this cannot pass by
	// dropping everything.
	if _, errOut, err := run("grant", "allow", "db.status", "--profile", "staging",
		"--agent", "claude", "--ttl", "1h"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if _, errOut, err := run("grant", "allow", "db.status", "--profile", "prod",
		"--agent", "claude", "--ttl", "1h"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if _, errOut, err := run("use", "staging"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}

	out, errOut, err := run("profile", "rm", "staging")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "revoked") {
		t.Errorf("a grant naming the removed profile was left standing:\n%s", out)
	}
	// It was switched on, so the switch has to follow — otherwise this
	// machine is bound to a name nothing can look up, and because the
	// selection also bounds agents, every agent call is refused against it.
	if !strings.Contains(out, "switched off") {
		t.Errorf("the machine is still switched on to a profile that is gone:\n%s", out)
	}

	list, _, err := run("grant", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(list, "staging") {
		t.Errorf("a grant for the removed profile survived:\n%s", list)
	}
	if !strings.Contains(list, "prod") {
		t.Errorf("removing one profile revoked another profile's grant:\n%s", list)
	}
}

func TestRemovingOnePluginNamesWhatIsLeft(t *testing.T) {
	const before = `profiles:
  staging:
    plugins:
      db:
        set:
          host: db.internal
`
	_, errOut, err := runWith(t, setRegistry(t), before, "profile", "rm", "staging", "--plugin", "nope")
	if err == nil {
		t.Fatal("removing an entry that is not there reported success")
	}
	if !strings.Contains(errOut, "db") {
		t.Errorf("the refusal does not say what the profile does configure: %q", errOut)
	}
}

// **A profile written where profiles are not honoured is a silent no-op.**
// config.Path falls back to ./.rta.yaml when there is no config directory —
// ordinary under `env -i`, in a container and in CI, which is exactly where a
// script runs. Succeeding there would make this command's whole purpose the
// case it gets wrong.
func TestWritingWhereProfilesAreNotHonouredIsRefused(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("RTA_CONFIG", "")
	// os.UserConfigDir reads $HOME on unix and $AppData on Windows; with
	// neither, Path falls back to the working directory.
	t.Setenv("HOME", "")
	t.Setenv("AppData", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("RTA_DATA_DIR", filepath.Join(dir, "data"))
	if config.TrustedPath() {
		t.Skip("this platform still finds a config directory, so there is nothing to refuse")
	}

	reg := setRegistry(t)
	SetInstalled(reg)
	t.Cleanup(func() { SetInstalled(nil) })
	root := NewRoot(reg, "test")
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"profile", "set", "staging", "--plugin", "db", "--set", "host=db.internal"})
	err := root.ExecuteContext(context.Background())
	errOut := errBuf.String()
	if err == nil {
		t.Fatal("wrote a profile into a file nothing honours")
	}
	if !strings.Contains(errOut, "core.profile.untrusted") {
		t.Errorf("refused for some other reason: %q", errOut)
	}
	if _, statErr := os.Stat(config.Path()); statErr == nil {
		t.Errorf("a refused run still created %s", config.Path())
	}
}

// A profile and a plugin namespace share a command line, so a profile called
// `db` is one nothing can name — `rta db status` is the plugin, every time.
// Check refuses it in the file; this refuses it at the keystroke, which is
// where somebody can still pick a different word.
func TestAProfileCannotTakeAPluginsName(t *testing.T) {
	_, errOut, err := runWith(t, setRegistry(t), "",
		"profile", "set", "db", "--plugin", "db", "--set", "host=db.internal")
	if err == nil {
		t.Fatal("a profile was created with a registered plugin's name")
	}
	if !strings.Contains(errOut, "core.profile.namespace") {
		t.Errorf("refused for some other reason: %q", errOut)
	}
	if strings.Contains(configOf(t), "profiles:") {
		t.Errorf("a refused run still wrote a profile:\n%s", configOf(t))
	}
}

// **The artifact pin is filled in, not demanded.** A profile entry for a
// plugin on $PATH has to name the artifact, because a namespace is something
// a plugin declares about itself and $PATH order decides who declares it
// first. But a digest is not something anybody should be asked to type, and
// typing one wrong is exactly the failure the pin exists to prevent — so
// `--plugin ext` writes `ext@<digest>` from what is actually registered.
func TestABareNamespaceIsPinnedToTheInstalledArtifact(t *testing.T) {
	reg := registry.New()
	const digest = "1a2b3c4d5e6f7890aabbccddeeff00112233445566778899aabbccddeeff0011"
	if err := reg.RegisterFrom(plugin.Plugin{
		Name: "ext", Summary: "an external plugin",
		Capabilities: []plugin.Capability{{
			ID: "ext.status", Summary: "status", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "host", Type: plugin.String, Config: "host", Local: true, Help: "host"},
			},
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.Text{Body: "ok"}, nil
			},
		}},
	}, registry.Origin{Path: "/usr/local/bin/rta-plugin-ext", Digest: digest}); err != nil {
		t.Fatal(err)
	}

	_, errOut, err := runWith(t, reg, "",
		"profile", "set", "staging", "--plugin", "ext", "--set", "host=x.internal")
	if err != nil {
		t.Fatalf("a bare namespace was refused instead of pinned: %v %q", err, errOut)
	}
	if body := configOf(t); !strings.Contains(body, "ext@"+digest[:12]) {
		t.Errorf("the entry was not pinned to the installed artifact:\n%s", body)
	}
}

// Pointing a profile at a rebuilt plugin moves its blocks rather than leaving
// them behind.
//
// Every rebuild invalidates every pin at once — trust is keyed on the artifact
// digest, so this is routine rather than exceptional — and `--plugin <name>` is
// what re-pointing looks like. It used to add a second entry for the same
// namespace: the operator's `set:`, `secrets:` and coordinate stranded under
// the dead digest, an empty block under the live one, and nothing anywhere
// reporting that the profile now configured one plugin twice. The only repair
// was to edit the YAML by hand.
//
// A namespace resolves to one entry (Profile.For takes the first), so there is
// no reading under which two of them is what somebody meant.
func TestRePinningAPluginCarriesItsConfiguration(t *testing.T) {
	const digest = "1a2b3c4d5e6f7890aabbccddeeff00112233445566778899aabbccddeeff0011"
	reg := registry.New()
	if err := reg.RegisterFrom(plugin.Plugin{
		Name: "ext", Summary: "an external plugin",
		Capabilities: []plugin.Capability{{
			ID: "ext.status", Summary: "status", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "host", Type: plugin.String, Config: "host", Local: true, Help: "host"},
				{Name: "token", Type: plugin.Secret, Local: true, EnvFallback: true, Help: "token"},
			},
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.Text{Body: "ok"}, nil
			},
		}},
	}, registry.Origin{Path: "/usr/local/bin/rta-plugin-ext", Digest: digest}); err != nil {
		t.Fatal(err)
	}

	// A profile written against the artifact that was installed yesterday.
	const stale = `profiles:
  staging:
    plugins:
      ext@ffffffffffff:
        set:
          host: x.internal
        secrets:
          token: kv:ext-token
`
	out, errOut, err := runWith(t, reg, stale, "profile", "set", "staging", "--plugin", "ext")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	body := configOf(t)
	if strings.Contains(body, "ext@ffffffffffff") {
		t.Errorf("the stale pin survived beside the new one:\n%s", body)
	}
	if !strings.Contains(body, "ext@"+digest[:12]) {
		t.Errorf("the entry was not moved to the installed artifact:\n%s", body)
	}
	// The blocks came with it. Losing them is the whole failure this prevents.
	for _, kept := range []string{"x.internal", "kv:ext-token"} {
		if !strings.Contains(body, kept) {
			t.Errorf("%s was left behind on the old pin:\n%s", kept, body)
		}
	}
	if !strings.Contains(out, "re-pinned") {
		t.Errorf("the move was silent:\n%s", out)
	}
}

// A pin the operator wrote themselves is left alone, so a deliberate one at
// an artifact that is not installed is still refused by name rather than
// quietly rewritten to whatever is there.
func TestAnExplicitPinIsNotSilentlyCorrected(t *testing.T) {
	reg := registry.New()
	if err := reg.RegisterFrom(plugin.Plugin{
		Name: "ext", Summary: "an external plugin",
		Capabilities: []plugin.Capability{{
			ID: "ext.status", Summary: "status", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "host", Type: plugin.String, Config: "host", Local: true, Help: "host"},
			},
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.Text{Body: "ok"}, nil
			},
		}},
	}, registry.Origin{Path: "/usr/local/bin/rta-plugin-ext", Digest: "aaaa1111bbbb2222"}); err != nil {
		t.Fatal(err)
	}

	_, errOut, err := runWith(t, reg, "",
		"profile", "set", "staging", "--plugin", "ext@deadbeefcafe", "--set", "host=x.internal")
	if err == nil {
		t.Fatal("a pin naming an artifact that is not installed was accepted")
	}
	if !strings.Contains(errOut, "aaaa1111bbbb") {
		t.Errorf("the refusal does not name the artifact that is installed: %q", errOut)
	}
}

// **The other half of not needing a form.** A flag you have to look up is a
// flag people guess at, and a guessed key is a refusal that did not have to
// happen. So `--set` offers the keys the named plugin actually declares.
//
// And the two lists differ in exactly the way the two blocks do: a credential
// is never a config key — pkg/plugin.Validate refuses that declaration — so
// it cannot appear under `--set`, while `--secret` offers it and says what it
// is. The shapes of the flags teach the rule before any refusal has to.
func TestCompletionOffersTheKeysEachFlagActuallyTakes(t *testing.T) {
	reg := setRegistry(t)
	SetInstalled(reg)
	t.Cleanup(func() { SetInstalled(nil) })

	root := NewRoot(reg, "test")
	cmd, _, err := root.Find([]string{"profile", "set"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("plugin", "db"); err != nil {
		t.Fatal(err)
	}

	setKeys, _ := completeSetKeys(cmd, nil, "")
	joined := strings.Join(setKeys, " ")
	for _, want := range []string{"host=", "port=", "tls=", "dbname="} {
		if !strings.Contains(joined, want) {
			t.Errorf("--set does not offer %q: %v", want, setKeys)
		}
	}
	if strings.Contains(joined, "password") {
		t.Errorf("--set offers a credential, which is the one thing it refuses: %v", setKeys)
	}
	// A closed set is what somebody would otherwise go and read, so it is
	// the description rather than the help text.
	if !strings.Contains(joined, "disable|require") {
		t.Errorf("--set does not offer the values a closed set accepts: %v", setKeys)
	}

	secretInputs, _ := completeSecretInputs(cmd, nil, "")
	joinedSecrets := strings.Join(secretInputs, " ")
	if !strings.Contains(joinedSecrets, "password=") {
		t.Errorf("--secret does not offer the credential: %v", secretInputs)
	}
	if !strings.Contains(joinedSecrets, "credential") {
		t.Errorf("--secret does not mark which inputs are credentials: %v", secretInputs)
	}
	// Wider than the credentials on purpose: Fill gates on ProfileFillable,
	// so mapping `dbname` onto a Secret's key is an ordinary thing to want.
	if !strings.Contains(joinedSecrets, "dbname=") {
		t.Errorf("--secret offers only credentials, which is narrower than what Fill allows: %v",
			secretInputs)
	}
}

// Past the `=` there is a value, and rta does not know what a host should be
// — nor will it unlock the store to enumerate entry names on a keystroke.
func TestCompletionDoesNotGuessAtValues(t *testing.T) {
	reg := setRegistry(t)
	SetInstalled(reg)
	t.Cleanup(func() { SetInstalled(nil) })
	root := NewRoot(reg, "test")
	cmd, _, err := root.Find([]string{"profile", "set"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("plugin", "db"); err != nil {
		t.Fatal(err)
	}
	if got, _ := completeSetKeys(cmd, nil, "host="); len(got) != 0 {
		t.Errorf("--set suggested a value: %v", got)
	}
	got, _ := completeSecretInputs(cmd, nil, "password=")
	if len(got) != 1 || !strings.Contains(got[0], "kv:") {
		t.Errorf("--secret should offer the scheme and nothing from the store: %v", got)
	}
}

// With no plugin named there is nothing to offer, and a completion that
// answered anyway would be answering about some other plugin.
//
// And with no registry at all it must not panic. `installed` is an interface
// and a completion runs on a keystroke, so a nil one is a crash in the
// operator's shell rather than a test failure here — the same class of
// mistake as the nil teardown that only the built binary found.
func TestCompletionIsSilentWithoutAPluginOrARegistry(t *testing.T) {
	reg := setRegistry(t)
	SetInstalled(reg)
	t.Cleanup(func() { SetInstalled(nil) })
	root := NewRoot(reg, "test")
	cmd, _, err := root.Find([]string{"profile", "set"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := completeSetKeys(cmd, nil, ""); len(got) != 0 {
		t.Errorf("offered keys with no plugin named: %v", got)
	}

	if err := cmd.Flags().Set("plugin", "db"); err != nil {
		t.Fatal(err)
	}
	SetInstalled(nil)
	if got, _ := completeSetKeys(cmd, nil, ""); len(got) != 0 {
		t.Errorf("offered keys with no registry: %v", got)
	}
	if got, _ := completeSecretInputs(cmd, nil, ""); len(got) != 0 {
		t.Errorf("offered inputs with no registry: %v", got)
	}
	if got, _ := completeInstalledPlugins(cmd, nil, ""); len(got) != 0 {
		t.Errorf("offered plugins with no registry: %v", got)
	}
}

// **A restatement that drops a key says so.** Stating the whole block is what
// makes this idempotent, and it is also how somebody changing a host loses
// the `sslmode: require` sitting beside it — a security setting, gone,
// because they restated one key of four.
//
// Merging instead would make a key impossible to remove and stop a script
// being the source of truth for what it states, so the answer is not to keep
// the key. It is to name it. A loss an operator can see is a different thing
// from one they cannot.
func TestARestatementNamesTheKeysItDrops(t *testing.T) {
	const before = `profiles:
  staging:
    plugins:
      db:
        set:
          host: db.internal
          dbname: app
          sslmode: require
        secrets:
          password: kv:staging-db-password
`
	out, errOut, err := runWith(t, setRegistry(t), before,
		"profile", "set", "staging", "--plugin", "db", "--set", "host=new.internal")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	for _, gone := range []string{"set.dbname", "set.sslmode"} {
		if !strings.Contains(out, gone) {
			t.Errorf("%s went without a word:\n%s", gone, out)
		}
	}
	// The block that was not restated is not in the receipt, because nothing
	// happened to it.
	if strings.Contains(out, "secrets.password") {
		t.Errorf("a block no flag mentioned was reported as dropped:\n%s", out)
	}
	// And the two losses read differently: a key a forward makes dead is not
	// the same news as a key a restatement left out.
	if strings.Contains(out, "the forward fills") {
		t.Errorf("reported as a forward repair, which is a different thing:\n%s", out)
	}
}

// Restating `secrets:` is the same rule, and the more expensive one to get
// wrong: what goes missing is where a credential comes from.
func TestRestatingSecretsNamesWhatItDrops(t *testing.T) {
	const before = `profiles:
  staging:
    plugins:
      db:
        secrets:
          password: kv:staging-db-password
          dbname: kv:staging-db-name
`
	out, errOut, err := runWith(t, setRegistry(t), before,
		"profile", "set", "staging", "--plugin", "db", "--secret", "password=kv:rotated")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "secrets.dbname") {
		t.Errorf("a dropped credential mapping went without a word:\n%s", out)
	}
}

// Nothing dropped, nothing said. A receipt on every run is a receipt nobody
// reads.
func TestNothingIsReportedWhenNothingIsDropped(t *testing.T) {
	const before = `profiles:
  staging:
    plugins:
      db:
        set:
          host: db.internal
`
	out, errOut, err := runWith(t, setRegistry(t), before,
		"profile", "set", "staging", "--plugin", "db", "--set", "host=new.internal")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if strings.Contains(out, "dropped") {
		t.Errorf("a run that lost nothing reported a loss:\n%s", out)
	}
}

// The badge colour is one of the three things a profile carries beside its
// connections, and the two surfaces that edit a profile both offer it: the
// flag here, the field in the TUI form. `none` drops it, and a value that is
// not a colour is refused by name rather than written for `rta profile list`
// to complain about later.
func TestSetColorIsOfferedValidatedAndDroppable(t *testing.T) {
	reg := setRegistry(t)
	if _, errOut, err := runWith(t, reg, "", "profile", "set", "prod", "--color", "#dd3333",
		"--plugin", "db", "--set", "host=db.internal"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(configOf(t), "color: \"#dd3333\"") && !strings.Contains(configOf(t), "color: '#dd3333'") {
		t.Errorf("colour not written:\n%s", configOf(t))
	}
	body, _ := os.ReadFile(config.Path())
	if _, errOut, err := runWith(t, reg, string(body), "profile", "set", "prod", "--color", "red"); err == nil || !strings.Contains(errOut, "core.profile.color") {
		t.Errorf("a non-colour was accepted: %v %q", err, errOut)
	}
	if _, errOut, err := runWith(t, reg, string(body), "profile", "set", "prod", "--color", "none"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if strings.Contains(configOf(t), "color:") {
		t.Errorf("`none` did not drop the colour:\n%s", configOf(t))
	}
}
