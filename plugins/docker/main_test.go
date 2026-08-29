package main

import (
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/sdk/sdktest"
)

// sdktest is the definition of "a correct plugin" — no exemption for docker,
// and until now no test file at all, which is the same exemption taken
// silently. Three mutating capabilities (stop, restart, rm) had nothing
// checking that their dry runs describe rather than act, and rm is
// Destructive: `--dry-run` is an accepted substitute for `--yes` on a
// Destructive capability, so the dry-run rule is the only thing standing
// between "preview" and an unconfirmed removal.
//
// conformanceInputs names a container that does not exist, so a dry run that
// stops being dry fails as a lookup against the operator's own daemon rather
// than by stopping something of theirs. The daemon may not be running at all,
// which is fine: the rule is about what the handler does, and a refusal is
// not a mutation.
func TestConformance(t *testing.T) {
	sdktest.Check(t, Plugin(), sdktest.WithInputs(conformanceInputs))
}

func conformanceInputs(string) map[string]map[string]any {
	container := map[string]any{"container": "rta-conformance-does-not-exist"}
	return map[string]map[string]any{
		"docker.container.stop":    container,
		"docker.container.restart": container,
		"docker.container.rm":      container,
		"docker.container.inspect": container,
	}
}
