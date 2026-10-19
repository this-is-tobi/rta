package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A list that is slow is not a cluster that is down. The 20 second ceiling
// fired on a very large list and said the cluster may be unreachable, which
// sent the reader to their VPN for a problem that was the size of the answer.
func TestASlowListIsSaidToBeSlowNotUnreachable(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	verr := classifyKubectl(ctx, errors.New("signal: killed"), "", "rolebindings")
	if verr.Code != "audit.kube.timeout" {
		t.Errorf("code = %q, want audit.kube.timeout", verr.Code)
	}
	if !strings.Contains(verr.Message, "did not finish reading rolebindings within 20s") {
		t.Errorf("message = %q", verr.Message)
	}
	if strings.Contains(verr.Message, "unreachable") || strings.Contains(verr.Message, "answer") {
		t.Errorf("message = %q says the cluster did not answer", verr.Message)
	}
	// The hint offers both causes, the likelier first, and a way out of the
	// one that is about size.
	if !strings.Contains(verr.Hint, "namespace") || !strings.Contains(verr.Hint, "unreachable") {
		t.Errorf("hint = %q", verr.Hint)
	}
}

// The cluster knowing the account and refusing it the list is a refused
// credential, named so (`.denied`), which is what lets a dashboard tile stop
// asking after two of them; a cluster that did not answer, or a kubectl that
// ran out of time, is not, and the tile has to keep asking to say it is back.
func TestAForbiddenListIsARefusedCredentialAndAnOutageIsNot(t *testing.T) {
	forbidden := `Error from server (Forbidden): pods is forbidden: User "u" cannot list resource "pods" ` +
		`in API group "" at the cluster scope`
	verr := classifyKubectl(context.Background(), errors.New("exit status 1"), forbidden, "pods")
	if verr.Code != "audit.kube.denied" {
		t.Fatalf("code = %q, want audit.kube.denied: %s", verr.Code, verr.Message)
	}
	if !verr.CredentialRefused() {
		t.Error("a list the cluster refused the account is not read as a refused credential")
	}

	refused := `E1003 14:46:33 memcache.go:265] "Unhandled Error" err="couldn't get current server API group ` +
		`list: Get \"https://127.0.0.1:1/api?timeout=32s\": dial tcp 127.0.0.1:1: connect: connection refused"`
	if verr := classifyKubectl(context.Background(), errors.New("exit status 1"), refused, "pods"); verr.CredentialRefused() {
		t.Errorf("%s is read as a refused credential, and the cluster refused nothing", verr.Code)
	}

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if verr := classifyKubectl(ctx, errors.New("signal: killed"), "", "pods"); verr.CredentialRefused() {
		t.Errorf("%s is read as a refused credential, and the cluster did not answer in time", verr.Code)
	}
}

// An expired login is the ordinary daily failure of a cluster reached through an
// exec credential plugin, and it was audit.kube.failed with kubectl's first line,
// so a tile of an audit on it started kubectl and the plugin again at every
// refresh until the person had logged in. The cluster not accepting the login is
// a refused credential (`.auth.failed`), which a tile stops asking on. A
// credential plugin that failed, or that this machine lacks, never reached the
// cluster: nothing was refused, the credential is not there, and a tile that
// stopped on it would stay paused after the person had logged in again.
func TestAnExpiredLoginIsARefusedCredentialAndAFailedCredentialPluginIsNot(t *testing.T) {
	for _, tc := range []struct {
		name, stderr, code string
		refused            bool
		message            string
	}{
		{"expired login", `error: You must be logged in to the server (Unauthorized)`,
			"audit.kube.auth.failed", true, "You must be logged in to the server (Unauthorized)"},
		{"a 401 from the api server", `Error from server (Unauthorized): unknown`,
			"audit.kube.auth.failed", true, "Unauthorized"},
		{"a request without a credential",
			`error: You must be logged in to the server (the server has asked for the client to provide credentials)`,
			"audit.kube.auth.failed", true, "asked for the client to provide credentials"},
		{"a credential plugin that failed",
			"Unable to connect to the server: getting credentials: exec: executable tsh failed with exit code 1\n",
			"audit.kube.credential.missing", false, "credential plugin"},
		{"a credential plugin kubectl cannot use", `error: exec plugin: invalid apiVersion "client.authentication.k8s.io/v1alpha1"`,
			"audit.kube.credential.missing", false, "credential plugin"},
		{"an auth provider kubectl no longer ships", `error: no Auth Provider found for name "gcp"`,
			"audit.kube.credential.missing", false, "credential plugin"},
		{"a credential plugin that is not installed",
			`Unable to connect to the server: getting credentials: exec: executable tsh not found`,
			"audit.kube.credential.missing", false, "tsh, the context's credential plugin, is not installed"},
		{"a credential plugin named by its path",
			`Unable to connect to the server: getting credentials: exec: fork/exec /opt/acme/acme-auth: no such file or directory`,
			"audit.kube.credential.missing", false, "acme-auth, the context's credential plugin, is not installed"},
		// The words that ended in a refused login before, and still do not
		// when the cluster simply did not answer.
		{"a cluster that did not answer", `Unable to connect to the server: dial tcp 10.0.0.9:6443: i/o timeout`,
			"audit.kube.unreachable", false, "i/o timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verr := classifyKubectl(context.Background(), errors.New("exit status 1"), tc.stderr, "pods")
			if verr.Code != tc.code {
				t.Fatalf("code = %q, want %s: %s", verr.Code, tc.code, verr.Message)
			}
			if verr.CredentialRefused() != tc.refused {
				t.Errorf("%s read as a refused credential = %v, want %v", verr.Code, verr.CredentialRefused(), tc.refused)
			}
			if !strings.Contains(verr.Message, tc.message) {
				t.Errorf("message %q does not say %q", verr.Message, tc.message)
			}
			if verr.Hint == "" && tc.code != "audit.kube.failed" {
				t.Errorf("%s has no hint saying what to do", verr.Code)
			}
		})
	}
}

// A narrowed audit asks for its namespace first, and a login that expired is
// what that first call meets: the audit says so rather than going on, the way
// it does for a namespace the account may not read.
func TestAnExpiredLoginStopsANarrowedAuditBeforeItsNamespaceCheck(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "kubectl")
	body := "#!/bin/sh\necho 'error: You must be logged in to the server (Unauthorized)' >&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := kubectlBin
	kubectlBin = script
	t.Cleanup(func() { kubectlBin = orig })

	if _, verr := scopeOf(context.Background(), newScopedRequest("prod")); verr == nil || verr.Code != "audit.kube.auth.failed" {
		t.Fatalf("verr = %v, want audit.kube.auth.failed", verr)
	}
}

// A cluster that did not answer is told as that. The current kubectl says it
// as a klog line — a timestamp, a process id and a source file around the one
// clause that matters — and the audit put that line in front of the reader as
// the error, where the forward and the secret read had already said which
// server did not answer and why.
func TestAClusterThatDidNotAnswerIsNotTheKlogLine(t *testing.T) {
	klog := `E1003 14:46:33.515018   94197 memcache.go:265] "Unhandled Error" ` +
		`err="couldn't get current server API group list: Get \"https://127.0.0.1:1/api?timeout=32s\": ` +
		`dial tcp 127.0.0.1:1: connect: connection refused"`
	dir := t.TempDir()
	script := filepath.Join(dir, "kubectl")
	body := "#!/bin/sh\necho '" + strings.ReplaceAll(klog, "'", `'\''`) + "' >&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := kubectlBin
	kubectlBin = script
	t.Cleanup(func() { kubectlBin = orig })

	var got list[limitedNamespace]
	verr := kubeGetJSON(context.Background(), "", "", "namespaces", &got)
	if verr == nil {
		t.Fatal("a kubectl that failed was not an error")
	}
	if verr.Code != "audit.kube.unreachable" {
		t.Errorf("code = %q, want audit.kube.unreachable", verr.Code)
	}
	for _, want := range []string{"https://127.0.0.1:1", "connection refused"} {
		if !strings.Contains(verr.Message, want) {
			t.Errorf("message %q does not name %q", verr.Message, want)
		}
	}
	for _, noise := range []string{"memcache.go", "Unhandled Error", "94197"} {
		if strings.Contains(verr.Message, noise) {
			t.Errorf("message %q carries kubectl's log line (%q)", verr.Message, noise)
		}
	}
}
