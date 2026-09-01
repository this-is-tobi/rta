package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk/sdktest"
)

// sdktest is the definition of "a correct plugin" — no exemption for kube,
// and until now no test file at all, which is the same exemption taken
// silently. kube.context.set is the one mutating capability here and it
// rewrites the operator's kubeconfig, so "does its dry run leave the file
// alone" is precisely the question this suite exists to ask.
//
// The named context does not exist, so on a machine with a kubeconfig the
// answer is a refusal and on a machine without one it is a different refusal.
// Neither is a mutation, which is the property under test.
func TestConformance(t *testing.T) {
	sdktest.Check(t, Plugin(), sdktest.WithInputs(conformanceInputs))
}

func conformanceInputs(string) map[string]map[string]any {
	return map[string]map[string]any{
		"kube.context.set": {"name": "rta-conformance-does-not-exist"},
		"kube.serviceaccount.provision": {
			"name": "rta-conformance-does-not-exist", "namespace": "rta-conformance-does-not-exist",
			"capability": []string{"kube.pod.list"}, "ttl": "15m",
		},
		"kube.serviceaccount.revoke": {
			"name": "rta-conformance-does-not-exist", "namespace": "rta-conformance-does-not-exist",
		},
	}
}

// **Every flag goes after the subcommand, and this is not cosmetic.**
//
// `--all-namespaces` is a flag of `get`, not a kubectl global. Placed before
// the verb, kubectl decides it is being asked for a plugin and refuses with
// "flags cannot be placed before plugin name: --request-timeout=15s" — which
// names the first flag it saw rather than the one it could not place. Every
// `--all-namespaces` call this plugin made failed that way, and the message
// sent the reader after a timeout that was not the problem. plugins/cnpg
// copied the order from here and inherited the bug with it.
func TestEveryFlagFollowsTheSubcommand(t *testing.T) {
	got := selection{Context: "homelab", AllNS: true}.args("get", "pods", "-o", "json")
	if len(got) == 0 || got[0] != "get" {
		t.Fatalf("args = %v, want the subcommand first", got)
	}
	verb := slices.Index(got, "get")
	for i, a := range got {
		if strings.HasPrefix(a, "-") && i < verb {
			t.Errorf("args = %v: %q comes before the subcommand", got, a)
		}
	}
	for _, want := range []string{"--context=homelab", "--all-namespaces", "-o", "json"} {
		if !slices.Contains(got, want) {
			t.Errorf("args = %v, missing %q", got, want)
		}
	}
}

// A namespace and every-namespace are exclusive, and the namespace form is
// the one a kubectl global flag would have tolerated in either position — so
// it is the one whose regression this would not catch without asking.
func TestANamespaceIsPassedAndNotBothForms(t *testing.T) {
	got := selection{Namespace: "keycloak-system"}.args("get", "pods")
	if !slices.Contains(got, "--namespace=keycloak-system") {
		t.Errorf("args = %v, missing the namespace", got)
	}
	if slices.Contains(got, "--all-namespaces") {
		t.Errorf("args = %v names both a namespace and every namespace", got)
	}
}

// args() resolves the two-fields-set case by preferring --all-namespaces,
// which is a scope bypass rather than a precedence rule: every capability
// here declares Scope: "namespace", and internal/grant derives the scope a
// call is checked against from the `namespace` value alone. A caller granted
// one namespace could send that namespace together with --all-namespaces,
// pass the check on the first and be answered from every namespace.
//
// So the property is not "args picks the narrower one" — it is that the
// combination never reaches args() at all.
func TestANamespaceAndEveryNamespaceTogetherAreRefused(t *testing.T) {
	_, verr := selectionOf(plugin.NewRequest(map[string]any{
		"namespace": "gitea", "all-namespaces": true,
	}, false, false))
	if verr == nil {
		t.Fatal("a scoped namespace sent alongside --all-namespaces was accepted")
	}
	if verr.Code != "kube.namespace.ambiguous" || verr.Hint == "" {
		t.Errorf("want a coded, hinted kube.namespace.ambiguous, got %+v", verr)
	}
}

// The two halves on their own stay accepted — the refusal above must not cost
// either ordinary form.
func TestEitherNamespaceFormAloneIsAccepted(t *testing.T) {
	one, verr := selectionOf(plugin.NewRequest(map[string]any{"namespace": "gitea"}, false, false))
	if verr != nil || one.Namespace != "gitea" || one.AllNS {
		t.Errorf("a plain namespace was not accepted: %+v %v", one, verr)
	}
	every, verr := selectionOf(plugin.NewRequest(map[string]any{"all-namespaces": true}, false, false))
	if verr != nil || !every.AllNS || every.Namespace != "" {
		t.Errorf("--all-namespaces alone was not accepted: %+v %v", every, verr)
	}
}
