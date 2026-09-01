package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// refusingKubectl stands in for a cluster that answers every call by refusing
// it, the way an API server does for a credential without the permission —
// non-zero exit, the refusal on stderr, nothing on stdout.
func refusingKubectl(t *testing.T, stderr string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "kubectl")
	body := "#!/bin/sh\ncat >/dev/null 2>&1\nprintf '%s\\n' " + shellQuote(stderr) + " >&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := kubectlBin
	kubectlBin = script
	t.Cleanup(func() { kubectlBin = orig })
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// An RBAC refusal is not silence, and overview used to report it as if it
// were: every failure of the namespaces read rendered as "did not answer",
// so a 403 came out as `did not answer — namespaces is forbidden: User ...
// cannot list resource "namespaces"` — a line that contradicts itself and
// sends the reader after a network problem they do not have.
//
// The distinction matters most in exactly this capability, because overview
// is what somebody runs when they do not yet know what is wrong.
func TestAForbiddenClusterIsReportedAsRefusingNotAsSilent(t *testing.T) {
	refusingKubectl(t, `Error from server (Forbidden): namespaces is forbidden: `+
		`User "system:serviceaccount:demo:agent" cannot list resource "namespaces" `+
		`in API group "" at the cluster scope`)

	v, err := runOverview(context.Background(), plugin.NewRequest(map[string]any{}, false, false))
	if err != nil {
		t.Fatalf("overview refused outright instead of reporting: %v", err)
	}
	body := fmt.Sprintf("%#v", v)
	if strings.Contains(body, "did not answer") {
		t.Errorf("a refusal was reported as silence:\n%s", body)
	}
	if !strings.Contains(body, "refused") {
		t.Errorf("the refusal was not named as one:\n%s", body)
	}
}

// The other half of the same property: a cluster that genuinely never
// answered must still say so. Guarding this direction too, because the
// obvious over-correction — treating every error as an answer — would be
// just as wrong and would read as a permission problem on a cluster that is
// simply unreachable.
func TestAnUnreachableClusterIsStillReportedAsSilent(t *testing.T) {
	refusingKubectl(t, "Unable to connect to the server: dial tcp 10.0.0.1:6443: i/o timeout")

	v, err := runOverview(context.Background(), plugin.NewRequest(map[string]any{}, false, false))
	if err != nil {
		t.Fatalf("overview refused outright instead of reporting: %v", err)
	}
	body := fmt.Sprintf("%#v", v)
	if !strings.Contains(body, "did not answer") {
		t.Errorf("an unreachable cluster was not reported as silent:\n%s", body)
	}
}

// hintOf is what the reader is sent to next, so a coded error must keep its
// own hint rather than fall back to the generic one.
func TestHintOfPrefersTheErrorsOwnHint(t *testing.T) {
	own := view.Errorf("kube.forbidden", "nope").WithHint("check the binding")
	if got := hintOf(own); got != "check the binding" {
		t.Errorf("hintOf = %q, want the error's own hint", got)
	}
	if got := hintOf(nil); got == "" {
		t.Error("hintOf(nil) returned nothing to check")
	}
}
