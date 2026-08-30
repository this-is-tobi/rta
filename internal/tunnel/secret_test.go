package tunnel

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

// b64 is what a Secret's `data` holds. Written out so the fixtures read like
// the JSON kubectl actually returns rather than like a helper's output.
func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func secretJSON(t *testing.T, kv map[string]string) string {
	t.Helper()
	var parts []string
	for k, v := range kv {
		parts = append(parts, fmt.Sprintf("%q:%q", k, b64(v)))
	}
	return `{"data":{` + strings.Join(parts, ",") + `}}`
}

func creds(t *testing.T) Target {
	t.Helper()
	return Target{
		Kube:   homelab,
		Secret: "postgres-creds",
		From:   map[string]string{"user": "username", "password": "password"},
	}
}

// The credential path makes the same split as the forward, and it matters
// more here: this is where a credential is fetched, so "not allowed to read
// secret X" sends somebody to argue about RBAC for a secret they can read
// perfectly well once they have logged in again.
func TestAnExpiredLoginReadingASecretIsNotReportedAsRBAC(t *testing.T) {
	fakeKubectl(t, `echo 'error: You must be logged in to the server (Unauthorized)' >&2; exit 1`+"\n")
	_, verr := Secrets(context.Background(), "homelab", Target{
		Kube: homelab, Secret: "postgres-creds", From: map[string]string{"password": "password"},
	})
	if verr == nil {
		t.Fatal("a failing kubectl produced no error")
	}
	if verr.Code != "tunnel.unauthenticated" {
		t.Errorf("code = %q, want tunnel.unauthenticated", verr.Code)
	}
	if strings.Contains(verr.Hint, "verb is `get`") {
		t.Errorf("hint sends the operator to RBAC for an authentication failure: %q", verr.Hint)
	}
}

func TestSecretsFillTheInputsTheOperatorMapped(t *testing.T) {
	fakeKubectl(t, "cat <<'JSON'\n"+
		secretJSON(t, map[string]string{"username": "appuser", "password": "s3cr3t", "unused": "x"})+
		"\nJSON\n")

	got, verr := Secrets(context.Background(), "homelab-pg", creds(t))
	if verr != nil {
		t.Fatalf("Secrets: %v", verr)
	}
	// Keyed by input, not by secret key: the plugin declared `user`, the
	// cluster calls it `username`, and reconciling the two is the operator's
	// mapping doing its job.
	if got["user"] != "appuser" || got["password"] != "s3cr3t" {
		t.Errorf("filled %v, want user=appuser password=s3cr3t", got)
	}
	// A key nobody mapped is a key nobody gets. The mapping is an allowlist,
	// which is what keeps a plugin from reaching anything the operator did
	// not name.
	if _, leaked := got["unused"]; leaked {
		t.Error("an unmapped key reached the caller")
	}
}

// A target with no secret is the ordinary case and must not shell out at all.
func TestATargetWithNoSecretAsksTheClusterNothing(t *testing.T) {
	fakeKubectl(t, "echo 'kubectl was called' >&2; exit 1\n")
	got, verr := Secrets(context.Background(), "homelab-pg", Target{Kube: homelab})
	if verr != nil || got != nil {
		t.Fatalf("Secrets = %v, %v; want nil, nil", got, verr)
	}
}

func TestSecretFailuresAreClassified(t *testing.T) {
	cases := []struct {
		name, script, want string
	}{
		{"no such secret",
			`echo 'Error from server (NotFound): secrets "postgres-creds" not found' >&2; exit 1`,
			"tunnel.secret.missing"},
		{"not allowed to read it",
			`echo 'Error from server (Forbidden): secrets "postgres-creds" is forbidden' >&2; exit 1`,
			"tunnel.secret.denied"},
		{"kubectl said nothing",
			`exit 1`,
			"tunnel.secret.unreadable"},
		{"something else entirely",
			`echo 'Unable to connect to the server: dial tcp: i/o timeout' >&2; exit 1`,
			"tunnel.secret.unreadable"},
		{"not json",
			`echo 'not json at all'`,
			"tunnel.secret.unreadable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeKubectl(t, tc.script+"\n")
			_, verr := Secrets(context.Background(), "homelab-pg", creds(t))
			if verr == nil {
				t.Fatal("no error")
			}
			if verr.Code != tc.want {
				t.Errorf("code = %s, want %s (message: %s)", verr.Code, tc.want, verr.Message)
			}
		})
	}
}

// A mapped key the secret does not have is the failure most likely to be met
// in anger — an operator renames a key, or copies a mapping between two
// clusters that spell it differently. An empty password reaching the plugin
// would surface as "authentication failed", which names the wrong thing
// entirely.
func TestAMappedKeyThatIsMissingSaysWhichKeysExist(t *testing.T) {
	fakeKubectl(t, "cat <<'JSON'\n"+
		secretJSON(t, map[string]string{"username": "appuser", "pgpass": "s3cr3t"})+
		"\nJSON\n")

	_, verr := Secrets(context.Background(), "homelab-pg", creds(t))
	if verr == nil {
		t.Fatal("a missing key was accepted")
	}
	if verr.Code != "tunnel.secret.key.missing" {
		t.Fatalf("code = %s, want tunnel.secret.key.missing", verr.Code)
	}
	if !strings.Contains(verr.Hint, "pgpass") || !strings.Contains(verr.Hint, "username") {
		t.Errorf("hint = %q, want the keys the secret does have", verr.Hint)
	}
}

func TestAnUndecodableValueIsNotHandedOverAsGarbage(t *testing.T) {
	fakeKubectl(t, `echo '{"data":{"username":"appuser","password":"!!!not base64!!!"}}'`+"\n")
	_, verr := Secrets(context.Background(), "homelab-pg", creds(t))
	if verr == nil || verr.Code != "tunnel.secret.undecodable" {
		t.Fatalf("verr = %v, want tunnel.secret.undecodable", verr)
	}
}

// The credential must never reach a message. An error naming the secret is
// useful; an error naming its contents is a credential in a log, a terminal
// scrollback and whatever an agent does with the text it is handed.
func TestNoFailureMessageCarriesTheCredential(t *testing.T) {
	const password = "s3cr3t-do-not-print"
	fakeKubectl(t, "cat <<'JSON'\n"+
		secretJSON(t, map[string]string{"username": "appuser", "password": password})+
		"\nJSON\n")

	target := creds(t)
	target.From["missing"] = "nope" // force a failure with the secret in hand
	_, verr := Secrets(context.Background(), "homelab-pg", target)
	if verr == nil {
		t.Fatal("expected the missing key to fail")
	}
	if strings.Contains(verr.Message+verr.Hint, password) {
		t.Errorf("the credential appears in the error: %s / %s", verr.Message, verr.Hint)
	}
}
