package app

import (
	"testing"

	"github.com/this-is-tobi/rta/internal/pipetest"
	"github.com/this-is-tobi/rta/internal/policy"
)

// `rta policy require` reads the operator's policy file before it writes it
// back, and the file sits in a directory something else can write to: a named
// pipe in its place held the command for good, as it held every other reader
// of a file the operator owns.
func TestANamedPipeWhereThePolicyFileGoesDoesNotHoldPolicyRequire(t *testing.T) {
	t.Setenv("RTA_POLICY", "")
	t.Chdir(t.TempDir())
	run := session(t, testRegistry(t))
	pipe := policy.OperatorPath()
	pipetest.Plant(t, pipe)
	pipetest.Returns(t, pipe, "policy require", func() {
		_, _, _ = run("policy", "require", "--dry-run", "-o", "json")
	})
}
