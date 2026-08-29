package main

import (
	"testing"

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
	}
}
