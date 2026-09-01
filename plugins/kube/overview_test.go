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

// The other three arms, driven through the real classify() with the stderr
// kubectl actually produces, so this pins the mapping rather than restating
// it. The local-cause cases matter most: "did not answer" sends somebody to
// check a VPN and a firewall over a missing binary, and kubectl not being
// installed is the likeliest way this plugin fails on a machine the first
// time.
func TestOverviewNamesWhichKindOfFailureItHit(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		want   string
		reject string
	}{
		{
			name:   "unreachable really is silence",
			stderr: "Unable to connect to the server: dial tcp 10.0.0.1:6443: i/o timeout",
			want:   "did not answer",
		},
		{
			name:   "kubectl is not installed",
			stderr: "no configuration has been provided, try setting KUBERNETES_MASTER",
			want:   "was never contacted",
			reject: "did not answer",
		},
		{
			name:   "the context does not exist",
			stderr: `error: context "prod" does not exist`,
			want:   "was never contacted",
			reject: "did not answer",
		},
		{
			name:   "an unrecognised failure claims nothing about the network",
			stderr: "error: something nobody has classified yet",
			want:   "could not be read",
			reject: "did not answer",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			refusingKubectl(t, c.stderr)
			v, err := runOverview(context.Background(), plugin.NewRequest(map[string]any{}, false, false))
			if err != nil {
				t.Fatalf("overview refused outright instead of reporting: %v", err)
			}
			body := fmt.Sprintf("%#v", v)
			if !strings.Contains(body, c.want) {
				t.Errorf("want %q in:\n%s", c.want, body)
			}
			if c.reject != "" && strings.Contains(body, c.reject) {
				t.Errorf("must not claim %q in:\n%s", c.reject, body)
			}
		})
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
