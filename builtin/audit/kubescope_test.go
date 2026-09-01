package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// fakeKubectl installs a kubectl that answers per resource kind and records
// every invocation, so a test can assert not only what came back but what was
// asked for — which is the whole question for the narrowing below.
func fakeKubectl(t *testing.T, bodies map[string]string) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")

	var sb strings.Builder
	sb.WriteString("#!/bin/sh\necho \"$@\" >> " + logPath + "\nfor a in \"$@\"; do\n  case \"$a\" in\n")
	for kind, body := range bodies {
		file := filepath.Join(dir, kind+".json")
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		sb.WriteString("    " + kind + ") cat " + file + "; exit 0;;\n")
	}
	sb.WriteString("  esac\ndone\necho '{\"items\":[]}'\n")

	script := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(script, []byte(sb.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := kubectlBin
	kubectlBin = script
	t.Cleanup(func() { kubectlBin = orig })
	return logPath
}

func calls(t *testing.T, logPath string) string {
	t.Helper()
	b, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	return string(b)
}

func newScopedRequest(ns string) plugin.Request {
	return plugin.NewRequest(map[string]any{"namespace": ns}, false, false)
}

// renderText flattens a view to something a test can search. The audits return
// a table whose cells carry the sentences under test.
func renderText(t *testing.T, v view.View) string {
	t.Helper()
	table, ok := v.(view.Table)
	if !ok {
		t.Fatalf("view is %T, want view.Table", v)
	}
	var sb strings.Builder
	for _, row := range table.Rows {
		sb.WriteString(strings.Join(row, " | "))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// The bug this narrowing would have shipped with, stated as a test.
//
// Namespaces are cluster-scoped, so `kubectl get namespaces --namespace=gitea`
// does not filter — kubectl takes the flag and ignores it. Passing the
// narrowing to both reads therefore narrows only the second: the quota read
// comes back holding gitea alone while the namespace read comes back holding
// the whole cluster, and every namespace that is not gitea then looks like one
// with no coverage. The naive version reports nineteen namespaces as findings
// where four qualify, each one a namespace the caller explicitly excluded.
func TestNarrowingAQuotaAuditDoesNotReportOtherNamespaces(t *testing.T) {
	logPath := fakeKubectl(t, map[string]string{
		"namespaces": `{"items":[{"metadata":{"name":"gitea"}},{"metadata":{"name":"argo-cd"}},
			{"metadata":{"name":"monitoring"}},{"metadata":{"name":"vault-system"}}]}`,
		"resourcequotas": `{"items":[]}`,
	})

	missing, verr := coverageGaps(t.Context(), "", "gitea", "resourcequotas")
	if verr != nil {
		t.Fatal(verr)
	}
	if len(missing) != 1 || missing[0] != "gitea" {
		t.Fatalf("missing = %v, want only [gitea] — every other namespace was explicitly excluded", missing)
	}
	// Two stronger claims, because the output above is the same whether or not
	// the reads were actually narrowed — filtering a cluster-wide answer
	// afterwards produces an identical table and leaves the bug one refactor
	// away.
	got := calls(t, logPath)
	// The namespace list must not have been read at all.
	if strings.Contains(got, " namespaces ") {
		t.Errorf("the namespace list was read despite the audit being narrowed:\n%s", got)
	}
	// And the quota read must itself be narrowed. A narrowed audit that still
	// pulls every namespace's objects reads far more than it names, which is
	// the property this whole tool is built to be honest about — and the one
	// thing a caller narrowing an audit is most likely to assume it does not.
	if strings.Contains(got, "--all-namespaces") {
		t.Errorf("the quota read swept every namespace despite the narrowing:\n%s", got)
	}
	if !strings.Contains(got, "--namespace=gitea") {
		t.Errorf("the quota read did not carry the narrowing:\n%s", got)
	}
}

// The cluster-wide path must keep working: a narrowing fix that quietly
// stopped sweeping every namespace would pass the test above and break the
// capability's actual job.
func TestAnUnscopedQuotaAuditStillSweepsEveryNamespace(t *testing.T) {
	fakeKubectl(t, map[string]string{
		"namespaces": `{"items":[{"metadata":{"name":"gitea"}},{"metadata":{"name":"argo-cd"}},
			{"metadata":{"name":"kube-system"}}]}`,
		"resourcequotas": `{"items":[{"metadata":{"namespace":"argo-cd"}}]}`,
	})

	missing, verr := coverageGaps(t.Context(), "", "", "resourcequotas")
	if verr != nil {
		t.Fatal(verr)
	}
	// argo-cd has one; kube-system is filtered as a system namespace.
	if len(missing) != 1 || missing[0] != "gitea" {
		t.Fatalf("missing = %v, want [gitea]", missing)
	}
}

// The system-namespace filter exists so a cluster-wide sweep does not report
// kube-system for a policy nobody expects it to carry. Somebody who typed
// `kube-system` is asking about kube-system, and answering "nothing to report"
// because of a filter they did not ask for is a wrong answer.
func TestANamedSystemNamespaceIsStillAudited(t *testing.T) {
	fakeKubectl(t, map[string]string{"networkpolicies": `{"items":[]}`})

	missing, verr := coverageGaps(t.Context(), "", "kube-system", "networkpolicies")
	if verr != nil {
		t.Fatal(verr)
	}
	if len(missing) != 1 || missing[0] != "kube-system" {
		t.Fatalf("missing = %v, want [kube-system] — a namespace asked for by name is not filtered", missing)
	}
}

// namespacesToCheck must not touch the cluster when narrowed. Proven by
// pointing kubectl at a path that cannot execute: if it were called, this
// would error rather than return the name.
func TestNarrowingSkipsTheNamespaceReadEntirely(t *testing.T) {
	orig := kubectlBin
	kubectlBin = "/nonexistent/kubectl-must-not-run"
	t.Cleanup(func() { kubectlBin = orig })

	names, verr := namespacesToCheck(t.Context(), "", "gitea")
	if verr != nil {
		t.Fatalf("namespacesToCheck contacted the cluster when narrowed: %v", verr)
	}
	if len(names) != 1 || names[0] != "gitea" {
		t.Errorf("names = %v, want [gitea]", names)
	}
}

// Every clean-result sentence was an absolute claim about the cluster, written
// when these audits could only run cluster-wide. Narrowed, those sentences stay
// absolute and become false — an audit of one namespace reporting that no pod
// runs privileged, on a cluster where many do, is worse than no check at all
// because somebody would believe it.
func TestCleanResultsClaimOnlyWhatWasExamined(t *testing.T) {
	if got := within(""); got != "cluster-wide" {
		t.Errorf("within(\"\") = %q", got)
	}
	if got := within("gitea"); !strings.Contains(got, "gitea") {
		t.Errorf("within(gitea) = %q, want the namespace named", got)
	}

	wide := coverageClean("", "NetworkPolicy")
	if !strings.Contains(wide, "every non-system namespace") {
		t.Errorf("cluster-wide coverage sentence = %q", wide)
	}
	scoped := coverageClean("gitea", "NetworkPolicy")
	if strings.Contains(scoped, "every") || !strings.Contains(scoped, "gitea") {
		t.Errorf("scoped coverage sentence = %q, want a claim about gitea alone", scoped)
	}

	// The sharpest case: the cluster-wide RBAC sentence names cluster-admin,
	// and a narrowed run has not examined a single ClusterRoleBinding, so it
	// must not repeat that claim.
	if !strings.Contains(rbacClean(""), "cluster-admin") {
		t.Errorf("cluster-wide rbac sentence = %q", rbacClean(""))
	}
	if strings.Contains(rbacClean("gitea"), "cluster-admin") {
		t.Errorf("scoped rbac sentence = %q, want no claim about cluster-admin", rbacClean("gitea"))
	}
}

// Narrowing the RBAC audit drops two thirds of its subject: ClusterRoleBindings
// to cluster-admin and wildcard ClusterRoles belong to no namespace, so they
// cannot be narrowed — only omitted.
//
// Omitting them silently is the dangerous option, because a cluster-admin
// binding is the headline finding this audit exists for: a scoped run would
// otherwise report a clean RBAC posture having never looked. So the omission is
// itself reported. This asserts both halves — that the cluster-scoped reads do
// not happen, and that the result says they did not.
func TestANarrowedRBACAuditSaysWhatItDidNotExamine(t *testing.T) {
	logPath := fakeKubectl(t, map[string]string{
		// A binding that would grade fail if it were ever read.
		"clusterrolebindings": `{"items":[{"metadata":{"name":"oops"},
			"roleRef":{"name":"cluster-admin"},
			"subjects":[{"kind":"Group","name":"system:authenticated"}]}]}`,
		"roles":        `{"items":[]}`,
		"clusterroles": `{"items":[]}`,
	})

	out, err := runKubeRBAC(t.Context(), newScopedRequest("gitea"))
	if err != nil {
		t.Fatal(err)
	}
	got := calls(t, logPath)
	for _, kind := range []string{"clusterrolebindings", "clusterroles"} {
		if strings.Contains(got, " "+kind+" ") {
			t.Errorf("%s was read despite the audit being narrowed to a namespace:\n%s", kind, got)
		}
	}
	rendered := renderText(t, out)
	if !strings.Contains(rendered, "not examined") {
		t.Errorf("a narrowed RBAC audit did not report what it skipped:\n%s", rendered)
	}
	// And it must not have reported the binding it never read.
	if strings.Contains(rendered, "oops") {
		t.Errorf("a narrowed audit reported a cluster-scoped finding:\n%s", rendered)
	}
}

// A namespace goes into a kubectl argument, and a name that is not one should
// be refused as a name rather than surface as kubectl's complaint about a flag.
func TestAnUnusableNamespaceIsRefused(t *testing.T) {
	for _, bad := range []string{"-kubeconfig=/tmp/theirs", "Gitea", "git ea", "a/b", strings.Repeat("x", 64)} {
		if verr := checkNamespace(bad); verr == nil {
			t.Errorf("%q was accepted as a namespace name", bad)
		}
	}
	for _, ok := range []string{"", "gitea", "kube-system", "a", "argo-cd-2"} {
		if verr := checkNamespace(ok); verr != nil {
			t.Errorf("%q was refused: %v", ok, verr)
		}
	}
}
