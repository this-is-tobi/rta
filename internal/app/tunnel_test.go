package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// connRegistry is a plugin whose connection can be pointed at a tunnel: a
// Config-keyed host and port carrying the endpoint roles, and a credential a
// profile's `secrets:` can map onto.
func connRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "db", Summary: "db plugin",
		Capabilities: []plugin.Capability{{
			ID: "db.status", Summary: "status", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "host", Type: plugin.String, Default: "localhost", Config: "host",
					Local: true, Endpoint: plugin.EndpointHost, Help: "host"},
				{Name: "port", Type: plugin.Int, Default: 5432, Config: "port",
					Local: true, Endpoint: plugin.EndpointPort, Min: 1, Max: 65535, Help: "port"},
				{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true, Help: "password"},
			},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return view.Text{Body: "reached " + req.String("host")}, nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// runWith is run() with a config file this test wrote.
func runWith(t *testing.T, reg *registry.Registry, yaml string, args ...string) (string, string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", path)
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_KV_PASSPHRASE", "")
	t.Setenv("RTA_KV_IDENTITY", "")
	SetInstalled(reg)
	t.Cleanup(func() { SetInstalled(nil) })
	root := NewRoot(reg, "test")
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

// A profile whose credential cannot be read is an error, not a crash.
//
// resolveProfile hands back a teardown that runCapability defers *before* it
// checks the error — deliberately, so a forward raised by a call that then
// fails is still closed. That makes a nil closer on any failure path a segfault
// rather than a message, and the first version of this returned one: every
// `rta <plugin> <cmd> --profile X` against a profile with a broken `secrets:`
// reference panicked instead of printing the refusal it had already composed.
//
// Found by running the built binary rather than by a test, which is the reason
// this one exists.
func TestABrokenCredentialReferenceIsRefusedRatherThanCrashing(t *testing.T) {
	out, errOut, err := runWith(t, connRegistry(t), `
profiles:
  homelab:
    plugins:
      db:
        secrets:
          password: kv:nothing-here
`, "db", "status", "--profile", "homelab")

	if err == nil {
		t.Fatalf("a profile naming a missing store entry succeeded: %q", out)
	}
	if !strings.Contains(errOut, "core.profile.secret.failed") {
		t.Errorf("stderr does not carry the refusal it composed: %q", errOut)
	}
}

// The same property, asserted directly rather than through a command: no path
// out of resolveProfile returns a nil teardown.
//
// A table rather than one case, because the failure is per return statement —
// each one is its own chance to write `nil` where `noop` belongs, and the cost
// is a panic on somebody's terminal rather than a test failure here.
func TestResolveProfileNeverReturnsANilTeardown(t *testing.T) {
	reg := connRegistry(t)
	for _, tc := range []struct {
		name, yaml, profile string
	}{
		{"no such profile", "profiles: {}", "nowhere"},
		{"profile is silent about this plugin", `
profiles:
  other:
    plugins:
      elsewhere:
        set: {host: x}
`, "other"},
		{"a key nothing reads", `
profiles:
  homelab:
    plugins:
      db:
        st: {host: x}
`, "homelab"},
		{"a credential that cannot be read", `
profiles:
  homelab:
    plugins:
      db:
        secrets:
          password: kv:nothing-here
`, "homelab"},
		{"a coordinate that cannot be forwarded", `
profiles:
  homelab:
    plugins:
      db:
        kube: nope/nope/svc/nope:1
`, "homelab"},
		{"a connection that works", `
profiles:
  homelab:
    plugins:
      db:
        set: {host: db.internal}
`, "homelab"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("RTA_CONFIG", path)
			t.Setenv("RTA_DATA_DIR", t.TempDir())
			// kubectl is deliberately absent from this PATH, so the coordinate
			// case fails at the open rather than reaching anybody's cluster.
			t.Setenv("PATH", t.TempDir())
			SetInstalled(reg)
			t.Cleanup(func() { SetInstalled(nil) })

			c, ok := reg.Capability("db.status")
			if !ok {
				t.Fatal("db.status is not registered")
			}
			cmd := NewRoot(reg, "test")
			cmd.SetArgs([]string{"db", "status", "--profile", tc.profile})
			// The flag has to be parsed for resolveProfile to see it, and
			// building the whole command tree is how the host declares it.
			sub, _, err := cmd.Find([]string{"db", "status"})
			if err != nil {
				t.Fatal(err)
			}
			if err := sub.Flags().Set("profile", tc.profile); err != nil {
				t.Fatal(err)
			}
			_, _, closeTunnel, _ := resolveProfile(context.Background(), sub, c, nil)
			if closeTunnel == nil {
				t.Fatal("the teardown is nil, so runCapability's deferred close panics here")
			}
			closeTunnel()
		})
	}
}

// What `rta profile show` calls a problem is what a run would actually refuse
// — no wider.
//
// The two used different rules. The page listed a namespace's *Secret* inputs
// and flagged every `secrets:` reference outside that list as "an input pg does
// not offer", while Fill gates on ProfileFillable — so mapping `user` onto a
// Secret's `username` key worked perfectly and was reported as broken. That is
// the report-versus-reality drift this repo has recorded three times, in the
// direction that cries wolf; the direction it could equally have taken is the
// one where a page calls a connection healthy and the call refuses.
//
// Asserted in both directions, because a rule that flags nothing agrees with a
// run just as badly as one that flags everything.
func TestTheProfilePageFlagsExactlyWhatARunWouldRefuse(t *testing.T) {
	reg := connRegistry(t)
	// db.status declares host, port (endpoint roles, Config-keyed) and a
	// Secret password. host is fillable and is not a Secret: the case that
	// used to be reported as an input the plugin does not offer.
	out, _, _ := runWith(t, reg, `
profiles:
  homelab:
    plugins:
      db:
        secrets:
          host: kv:db-host
          password: kv:db-password
          nosuchinput: kv:whatever
`, "profile", "show", "homelab")

	if strings.Contains(out, "secrets.host names an input") {
		t.Errorf("a mapping onto a fillable non-Secret input was called a problem:\n%s", out)
	}
	if !strings.Contains(out, "kv:db-host") {
		t.Errorf("a mapping that decides the connection is invisible on the page describing it:\n%s", out)
	}
	if !strings.Contains(out, "secrets.nosuchinput names an input") {
		t.Errorf("a mapping onto an input the plugin really does not have was not flagged:\n%s", out)
	}
}
