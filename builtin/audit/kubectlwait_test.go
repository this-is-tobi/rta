package audit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A credential helper that outlives kubectl must not wedge an audit.
//
// A kubeconfig's exec plugin is handed kubectl's own pipes, and one that
// forks something and exits leaves the write end open after kubectl is
// gone. With WaitDelay unset, Wait blocks until every pipe sees EOF —
// forever, and past the twenty-second ceiling, because what is stuck is
// os/exec's copying goroutines rather than the process. The fake here is
// that shape: a background child holding stdout, then the answer, then
// exit.
func TestAnOrphanHoldingKubectlsPipesDoesNotWedgeTheAudit(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "kubectl")
	body := "#!/bin/sh\n(sleep 30) &\necho '{\"items\":[{\"metadata\":{\"name\":\"one\"}}]}'\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := kubectlBin
	kubectlBin = script
	t.Cleanup(func() { kubectlBin = orig })

	done := make(chan struct{})
	var got list[limitedNamespace]
	var verr error
	go func() {
		defer close(done)
		if v := kubeGetJSON(context.Background(), "", "", "namespaces", &got); v != nil {
			verr = v
		}
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("kubeGetJSON never returned: kubectl exited and nothing bounded the wait for " +
			"pipes an orphan is still holding")
	}
	// The answer survives the bound: kubectl finished and printed it whole,
	// and ErrWaitDelay is not a reason to throw that away.
	if verr != nil {
		t.Fatalf("a complete answer was refused because an orphan held the pipes: %v", verr)
	}
	if len(got.Items) != 1 || got.Items[0].Metadata.Name != "one" {
		t.Errorf("items = %+v, want the one namespace the fake printed", got.Items)
	}
}
