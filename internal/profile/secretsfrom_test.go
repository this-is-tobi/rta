package profile

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

// The case this field exists for: a service on an address rta reaches
// directly, whose password lives in a cluster Secret. Before `secrets-from:`
// the only way to name the cluster was a `kube:` coordinate, and stating one
// laid its forward's endpoint over `set: host` — so the connection was
// dragged through a port-forward it never needed, and a public address became
// dead config. Neither half of that was ever argued for; it fell out of one
// field carrying two facts.
func TestACredentialFromAClusterDoesNotForceAForward(t *testing.T) {
	reg := tunnelledRegistry(t)
	cfg := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        set:
          host: db.example.com
          port: 5432
        secrets:
          password: kube:pg-creds/password
        secrets-from: homelab/databases
`)
	if problems := Check(cfg, reg); len(problems) != 0 {
		for _, p := range problems {
			t.Errorf("Check refused a direct connection with a cluster credential: %s", p.Reason)
		}
	}
	// `set: host` must survive: nothing overrides it, because nothing opens a
	// forward. That is the whole point of the change.
	conn := cfg.Profiles["homelab"].Plugins["pg"]
	if conn.Tunnelled() {
		t.Error("a connection that only reads a Secret from a cluster counts as tunnelled")
	}
	if conn.Set["host"] != "db.example.com" {
		t.Errorf("host = %v, want the directly-reachable address left alone", conn.Set["host"])
	}
}

// Two statements about one fact. A coordinate already names the namespace its
// Secrets come from, so a second source beside it is either the same answer
// written twice or a different one nobody can rank — the treatment `kube:`
// and `ssh:` together already get.
func TestACoordinateAndASecretSourceTogetherAreRefused(t *testing.T) {
	reg := tunnelledRegistry(t)
	cfg := load(t, `
profiles:
  homelab:
    plugins:
      pg:
        secrets:
          password: kube:pg-creds/password
        kube: homelab/databases/svc/postgres:5432
        secrets-from: other/elsewhere
`)
	problems := Check(cfg, reg)
	if len(problems) == 0 {
		t.Fatal("a connection naming two Secret sources was accepted")
	}
	if !strings.Contains(problems[0].Reason, "secrets-from") {
		t.Errorf("reason = %q, want it to name the redundant line", problems[0].Reason)
	}
}

// A coordinate's four segments are the forward's business; this field takes
// the two that name a cluster and nothing more. A service and port here would
// be a forward somebody thinks they declared and did not.
func TestASecretSourceIsAContextAndNamespaceOnly(t *testing.T) {
	reg := tunnelledRegistry(t)
	for _, tc := range []struct{ what, spec string }{
		{"a full coordinate", "homelab/databases/svc/postgres:5432"},
		{"a bare context", "homelab"},
		{"an empty namespace", "homelab/"},
		{"a flag-shaped context", "-kubeconfig=/tmp/mine/databases"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			cfg := config.Config{Profiles: map[string]config.Profile{
				"homelab": {Plugins: map[string]config.Connection{
					"pg": {
						Secrets:     map[string]string{"password": "kube:pg-creds/password"},
						SecretsFrom: tc.spec,
					},
				}},
			}}
			if problems := Check(cfg, reg); len(problems) == 0 {
				t.Errorf("%q was accepted as a cluster and namespace", tc.spec)
			}
		})
	}
}

// The invariant that has to survive the change: a connection reads Secrets
// from exactly one stated namespace. Moving that namespace is a different
// credential authenticating the same call, so a standing grant must not carry
// across it.
func TestRepointingWhereACredentialComesFromMovesTheStamp(t *testing.T) {
	dev := config.Connection{
		Set:         map[string]any{"host": "db.example.com"},
		Secrets:     map[string]string{"password": "kube:pg-creds/password"},
		SecretsFrom: "homelab/dev",
	}
	prod := dev
	prod.SecretsFrom = "homelab/prod"
	if ConnStamp("pg@abcd", dev) == ConnStamp("pg@abcd", prod) {
		t.Error("dev and prod credentials share a stamp, so a grant issued against " +
			"one keeps authorizing calls made with the other")
	}
}
