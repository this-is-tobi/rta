package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func saReq(surface plugin.Surface, dryRun bool, values map[string]any) plugin.Request {
	r := plugin.NewRequest(values, dryRun, false)
	return r.WithSurface(surface)
}

// failIfInvoked stands in for kubectl and fails the test the moment it is
// actually run — the property under test for provision's dry run is not
// "the output looks like a dry run" but "the cluster was never touched",
// and this is what makes that concrete instead of inferred from the result.
func failIfInvoked(t *testing.T) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "kubectl")
	body := "#!/bin/sh\necho 'kubectl invoked during a dry run' >&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := kubectlBin
	kubectlBin = script
	t.Cleanup(func() { kubectlBin = orig })
}

// fakeRevokeCluster fakes `get`/`delete` for one ServiceAccount name across
// all three kinds, logging every invocation so the test can assert on what
// was and was not deleted — `role`/`roleLabelled` and
// `roleBinding`/`roleBindingLabelled` let a test simulate a Role or
// RoleBinding that exists under the target name but was NOT created by this
// plugin (no provisionedByLabel), the exact shape a security review found
// unprotected: revoke used to delete both by bare name with no ownership
// check of their own, unlike the ServiceAccount it does check.
func fakeRevokeCluster(t *testing.T, name string, roleExists, roleLabelled, bindingExists, bindingLabelled bool) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "kubectl")
	logFile := filepath.Join(dir, "log")

	// One line of JSON per response, single-quoted for the shell — safe
	// because this test controls every byte of it and it never contains a
	// single quote.
	labelsJSON := func(labelled bool) string {
		if labelled {
			return `{"labels":{"` + provisionedByLabel + `":"` + provisionedByValue + `"}}`
		}
		return `{"labels":{}}`
	}
	getCase := func(kind string, exists, labelled bool) string {
		if !exists {
			return "*\"get " + kind + " " + name + "\"*) exit 1 ;;\n"
		}
		return "*\"get " + kind + " " + name + "\"*) printf '%s\\n' '{\"metadata\":" +
			labelsJSON(labelled) + "}' ;;\n"
	}

	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("echo \"$@\" >> '" + logFile + "'\n")
	b.WriteString("case \"$*\" in\n")
	b.WriteString(getCase("serviceaccount", true, true))
	b.WriteString(getCase("role", roleExists, roleLabelled))
	b.WriteString(getCase("rolebinding", bindingExists, bindingLabelled))
	b.WriteString("*\"delete \"*) exit 0 ;;\n")
	b.WriteString("*) exit 1 ;;\n")
	b.WriteString("esac\n")

	if err := os.WriteFile(script, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := kubectlBin
	kubectlBin = script
	t.Cleanup(func() { kubectlBin = orig })
	return logFile
}

// The regression test for the fix: a Role and RoleBinding exist under the
// target name but were never created by kube.serviceaccount.provision (no
// label) — revoke must leave both alone while still deleting the
// ServiceAccount it did verify, and must say so rather than silently doing
// nothing to them.
func TestRevokeNeverDeletesARoleOrRoleBindingItDoesNotOwn(t *testing.T) {
	logPath := fakeRevokeCluster(t, "agent-collision", true, false, true, false)

	v, err := runServiceAccountRevoke(context.Background(), saReq(plugin.SurfaceCLI, false, map[string]any{
		"name": "agent-collision", "namespace": "team-prod",
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := v.(view.Text).Body
	if !strings.Contains(body, "Left alone") || !strings.Contains(body, "role") {
		t.Errorf("result did not report the skipped objects: %q", body)
	}

	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"delete role agent-collision", "delete rolebinding agent-collision"} {
		if strings.Contains(string(log), forbidden) {
			t.Errorf("revoke issued %q against an object it does not own:\n%s", forbidden, log)
		}
	}
	if !strings.Contains(string(log), "delete serviceaccount agent-collision") {
		t.Errorf("revoke did not delete the ServiceAccount it does own:\n%s", log)
	}
}

// The companion case: when the Role/RoleBinding genuinely are this plugin's
// own (labelled), revoke deletes them same as before.
func TestRevokeDeletesARoleAndRoleBindingItDoesOwn(t *testing.T) {
	logPath := fakeRevokeCluster(t, "agent-owned", true, true, true, true)

	v, err := runServiceAccountRevoke(context.Background(), saReq(plugin.SurfaceCLI, false, map[string]any{
		"name": "agent-owned", "namespace": "team-prod",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(v.(view.Text).Body, "Left alone") {
		t.Errorf("reported objects left alone when both were owned: %q", v.(view.Text).Body)
	}

	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"delete role agent-owned", "delete rolebinding agent-owned", "delete serviceaccount agent-owned"} {
		if !strings.Contains(string(log), want) {
			t.Errorf("missing %q in log:\n%s", want, log)
		}
	}
}

func TestProvisionDryRunNeverTouchesTheCluster(t *testing.T) {
	failIfInvoked(t)
	v, err := runServiceAccountProvision(context.Background(), saReq(plugin.SurfaceCLI, true, map[string]any{
		"name": "agent-a", "namespace": "team-prod",
		"capability": []string{"kube.pod.list", "kube.deployment.list"},
		"ttl":        "15m",
	}))
	if err != nil {
		t.Fatalf("dry run itself failed: %v", err)
	}
	body := v.(view.Text).Body
	for _, want := range []string{"agent-a", "team-prod", "15m", "pods", "deployments"} {
		if !strings.Contains(body, want) {
			t.Errorf("dry run output missing %q:\n%s", want, body)
		}
	}
}

func TestProvisionRefusesOverMCP(t *testing.T) {
	_, err := runServiceAccountProvision(context.Background(), saReq(plugin.SurfaceMCP, false, map[string]any{
		"name": "agent-a", "namespace": "team-prod",
		"capability": []string{"kube.pod.list"}, "ttl": "15m",
	}))
	ve := view.AsError(err, "x")
	if ve == nil || ve.Code != "kube.serviceaccount.mcp" {
		t.Errorf("want kube.serviceaccount.mcp, got %v", err)
	}
}

// The asymmetry is the point: provision mints a credential and must never be
// reachable by an agent; revoke only takes access away and is meant to be
// usable over MCP under the ordinary Destructive consent gate. Getting this
// backwards in either direction is the one regression this whole feature
// cannot afford, so both halves are pinned in the same test.
func TestRevokeIsReachableOverMCPUnlikeProvision(t *testing.T) {
	failIfInvoked(t) // no real cluster reachable here either way; the point is which error comes back
	_, err := runServiceAccountRevoke(context.Background(), saReq(plugin.SurfaceMCP, false, map[string]any{
		"name": "agent-a", "namespace": "team-prod",
	}))
	ve := view.AsError(err, "x")
	if ve != nil && ve.Code == "kube.serviceaccount.mcp" {
		t.Error("revoke refused SurfaceMCP the same way provision does — it must not")
	}
}

func TestProvisionRejectsAMalformedTTLBeforeAnyClusterCall(t *testing.T) {
	failIfInvoked(t)
	_, err := runServiceAccountProvision(context.Background(), saReq(plugin.SurfaceCLI, false, map[string]any{
		"name": "agent-a", "namespace": "team-prod",
		"capability": []string{"kube.pod.list"}, "ttl": "not-a-duration",
	}))
	ve := view.AsError(err, "x")
	if ve == nil || ve.Code != "kube.serviceaccount.ttl.invalid" || ve.Hint == "" {
		t.Errorf("want a coded, hinted kube.serviceaccount.ttl.invalid, got %v", err)
	}
}

func TestProvisionRejectsZeroOrNegativeTTL(t *testing.T) {
	failIfInvoked(t)
	for _, ttl := range []string{"0s", "-15m"} {
		_, err := runServiceAccountProvision(context.Background(), saReq(plugin.SurfaceCLI, false, map[string]any{
			"name": "agent-a", "namespace": "team-prod",
			"capability": []string{"kube.pod.list"}, "ttl": ttl,
		}))
		ve := view.AsError(err, "x")
		if ve == nil || ve.Code != "kube.serviceaccount.ttl.invalid" {
			t.Errorf("ttl %q: want kube.serviceaccount.ttl.invalid, got %v", ttl, err)
		}
	}
}

// Found by an actual provision against a real cluster: kubectl create token
// fails outright below Kubernetes' own 10-minute TokenRequest floor, and
// before this fix that failure only surfaced after the ServiceAccount, Role
// and RoleBinding were already created — a partial provision the zero/negative
// check above never would have caught.
func TestProvisionRejectsATTLBelowTheTokenRequestFloor(t *testing.T) {
	failIfInvoked(t)
	for _, ttl := range []string{"1m", "9m59s"} {
		_, err := runServiceAccountProvision(context.Background(), saReq(plugin.SurfaceCLI, false, map[string]any{
			"name": "agent-a", "namespace": "team-prod",
			"capability": []string{"kube.pod.list"}, "ttl": ttl,
		}))
		ve := view.AsError(err, "x")
		if ve == nil || ve.Code != "kube.serviceaccount.ttl.invalid" {
			t.Errorf("ttl %q: want kube.serviceaccount.ttl.invalid, got %v", ttl, err)
		}
	}
}

func TestProvisionRefusesAnUngrantableCapabilityBeforeAnyClusterCall(t *testing.T) {
	failIfInvoked(t)
	_, err := runServiceAccountProvision(context.Background(), saReq(plugin.SurfaceCLI, false, map[string]any{
		"name": "agent-a", "namespace": "team-prod",
		"capability": []string{"kube.cert.list"}, "ttl": "15m",
	}))
	ve := view.AsError(err, "x")
	if ve == nil || ve.Code != "kube.serviceaccount.ungrantable" {
		t.Errorf("want kube.serviceaccount.ungrantable, got %v", err)
	}
}

// Caught during review: provision and revoke were first built as raw
// literals, copying kube.context.set's shape without its reason — set omits
// --context because switching *is* the context argument; provision and
// revoke have no such overlap and, like every other capability here, need to
// say which cluster they mean. Pinned so it can't quietly regress again.
func TestProvisionAndRevokeDeclareAContextField(t *testing.T) {
	for _, id := range []string{"kube.serviceaccount.provision", "kube.serviceaccount.revoke"} {
		found := false
		for _, c := range Plugin().Capabilities {
			if c.ID != id {
				continue
			}
			for _, f := range c.Inputs {
				if f.Name == "context" && f.Local {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s declares no Local context field", id)
		}
	}
}

func TestRevokeRefusesAnInvalidName(t *testing.T) {
	failIfInvoked(t)
	_, err := runServiceAccountRevoke(context.Background(), saReq(plugin.SurfaceCLI, false, map[string]any{
		"name": "-not-a-name", "namespace": "team-prod",
	}))
	ve := view.AsError(err, "x")
	if ve == nil || ve.Code != "kube.name.invalid" {
		t.Errorf("want kube.name.invalid, got %v", err)
	}
}
