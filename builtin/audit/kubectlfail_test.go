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
