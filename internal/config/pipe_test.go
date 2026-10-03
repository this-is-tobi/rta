//go:build unix

package config

import (
	"testing"

	"github.com/this-is-tobi/rta/internal/pipetest"
)

// The config is read before every command, and the working-directory fallback
// reads whatever a directory somebody else filled holds. open(2) on a named
// pipe waits for a writer, so one at the config's path held every command that
// reads it, for good, with a thread pinned apiece.
func TestANamedPipeWhereTheConfigGoesDoesNotHoldEveryCommand(t *testing.T) {
	path := setPath(t)
	pipetest.Plant(t, path)
	pipetest.Returns(t, path, "reading the config", func() { _, _ = LoadFile() })
}
