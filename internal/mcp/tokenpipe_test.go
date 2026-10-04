package mcp

import (
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/internal/pipetest"
)

// The token file is read when the HTTP listener starts. A named pipe at its
// path held the start, and the server never came up and never said why.
func TestANamedPipeWhereTheTokenFileGoesDoesNotHoldTheListener(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens")
	pipetest.Plant(t, path)
	pipetest.Returns(t, path, "reading the token file", func() { _, _, _ = LoadTokenFile(path) })
}
