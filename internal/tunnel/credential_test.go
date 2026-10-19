package tunnel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// One kubectl message reaches three classifiers, and each read it as
// something else: a missing service on the forward, a missing secret on the
// read, an expired login on a listing. All three now name the tool, because
// installing it is the fix. Both spellings kubectl uses, captured from a
// real one: a bare name looked up on $PATH, and an absolute path.
func TestAMissingCredentialPluginIsNamedOnEverySurface(t *testing.T) {
	for exe, stderr := range map[string]string{
		"tsh": `Unable to connect to the server: getting credentials: exec: executable tsh not found`,
		"acme-auth": `E0921 04:13:36.061482 94227 memcache.go:265] "Unhandled Error" err="couldn't get current server API group list: Get \"https://127.0.0.1:1/api?timeout=32s\": getting credentials: exec: fork/exec /opt/acme/acme-auth: no such file or directory"` +
			"\n" + `Unable to connect to the server: getting credentials: exec: fork/exec /opt/acme/acme-auth: no such file or directory`,
	} {
		for surface, verr := range map[string]*view.Error{
			"forward": kubectlFailed("homelab-pg", homelab, stderr),
			"secret":  secretFailed("homelab-pg", "databases", "pg-creds", stderr),
			"listing": listFailed("namespaces in homelab", stderr),
		} {
			if verr.Code != "tunnel.credential.missing" {
				t.Errorf("%s/%s: code = %s, want tunnel.credential.missing", exe, surface, verr.Code)
			}
			if !strings.Contains(verr.Message, exe+", the context's credential plugin") {
				t.Errorf("%s/%s: message %q does not name the tool", exe, surface, verr.Message)
			}
		}
	}
}

// What the cluster did to the account decides whether a dashboard tile stops
// asking (view.Error.CredentialRefused): a login it did not accept (401), and
// an account it knows and refused the verb (403), are the same credential
// refused again at the next refresh. A cluster that did not answer, a secret
// that is not there, and a credential plugin that is not installed or that
// failed before there was a request to refuse, are not, and a tile that stopped
// on one of them would be hiding an outage or a thing to install, or would stay
// paused after the person had logged in again.
func TestAClusterRefusingTheAccountIsReadAsARefusedCredentialOnEverySurface(t *testing.T) {
	const unreachable = `Unable to connect to the server: dial tcp 10.0.0.1:6443: i/o timeout`
	cases := []struct {
		name, stderr string
		refused      bool
		code         map[string]string
	}{
		{"expired login", `error: You must be logged in to the server (Unauthorized)`, true,
			map[string]string{"forward": "tunnel.auth.failed", "secret": "tunnel.auth.failed", "listing": "tunnel.auth.failed"}},
		{"credential plugin failed", `Unable to connect to the server: getting credentials: exec: executable tsh failed`, false,
			map[string]string{"forward": "tunnel.credential.missing", "secret": "tunnel.credential.missing", "listing": "tunnel.credential.missing"}},
		{"credential plugin kubectl cannot use", `error: exec plugin: invalid apiVersion "client.authentication.k8s.io/v1alpha1"`, false,
			map[string]string{"forward": "tunnel.credential.missing", "secret": "tunnel.credential.missing", "listing": "tunnel.credential.missing"}},
		{"request without a credential", `error: You must be logged in to the server (the server has asked for the client to provide credentials)`, true,
			map[string]string{"forward": "tunnel.auth.failed", "secret": "tunnel.auth.failed", "listing": "tunnel.auth.failed"}},
		{"rbac", `Error from server (Forbidden): pods is forbidden: User "u" cannot create resource "pods/portforward"`, true,
			map[string]string{"forward": "tunnel.denied", "secret": "tunnel.secret.denied", "listing": "tunnel.list.denied"}},
		{"cluster did not answer", unreachable, false,
			map[string]string{"forward": "tunnel.cluster.unreachable", "secret": "tunnel.cluster.unreachable"}},
		{"credential plugin not installed", `Unable to connect to the server: getting credentials: exec: executable tsh not found`, false,
			map[string]string{"forward": "tunnel.credential.missing", "secret": "tunnel.credential.missing", "listing": "tunnel.credential.missing"}},
		{"secret not there", `Error from server (NotFound): secrets "pg-creds" not found`, false,
			map[string]string{"secret": "tunnel.secret.missing"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			answers := map[string]*view.Error{
				"forward": kubectlFailed("homelab-pg", homelab, tc.stderr),
				"secret":  secretFailed("homelab-pg", "databases", "pg-creds", tc.stderr),
				"listing": listFailed("namespaces in homelab", tc.stderr),
			}
			for surface, want := range tc.code {
				verr := answers[surface]
				if verr.Code != want {
					t.Errorf("%s: code = %s, want %s", surface, verr.Code, want)
				}
				if verr.CredentialRefused() != tc.refused {
					t.Errorf("%s: %s read as refused = %v, want %v", surface, verr.Code, verr.CredentialRefused(), tc.refused)
				}
			}
		})
	}
}

// A credential plugin that failed never asked the cluster anything, so the
// answer does not say the cluster does not know who you are, which is what it
// says for a 401 and would send somebody to the wrong end of the login.
func TestAFailedCredentialPluginIsNotSaidToBeTheClusterNotKnowingYou(t *testing.T) {
	const stderr = `Unable to connect to the server: getting credentials: exec: executable tsh failed with exit code 1`
	for surface, verr := range map[string]*view.Error{
		"forward": kubectlFailed("homelab-pg", homelab, stderr),
		"secret":  secretFailed("homelab-pg", "databases", "pg-creds", stderr),
		"listing": listFailed("namespaces in homelab", stderr),
	} {
		if strings.Contains(verr.Message, "does not know who you are") || !strings.Contains(verr.Message, "credential plugin") {
			t.Errorf("%s: message %q is not about the credential plugin", surface, verr.Message)
		}
		if verr.Hint == "" {
			t.Errorf("%s: no hint says how to log in again", surface)
		}
	}
}

// A cancelled context ends the forward the way Close does: SIGTERM to the
// group, which kubectl answers by telling the API server the forward is
// over — not os/exec's default SIGKILL of the leader alone, which nothing
// can answer. The fake traps TERM and leaves a mark, which SIGKILL could
// never produce.
func TestCancellingTheContextTerminatesTheForwardGracefully(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "terminated")
	fakeKubectl(t, "trap 'echo yes > "+marker+"; exit 0' TERM\n"+
		"echo 'Forwarding from 127.0.0.1:54321 -> 5432'\n"+
		"while :; do sleep 1; done\n")
	ctx, cancel := context.WithCancel(context.Background())
	tun, verr := Open(ctx, "homelab-pg", Target{Kube: homelab})
	if verr != nil {
		t.Fatal(verr.Message)
	}
	defer tun.Close()
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("kubectl was not sent SIGTERM within five seconds of the context ending")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
