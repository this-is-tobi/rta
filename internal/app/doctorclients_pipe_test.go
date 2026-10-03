//go:build unix

package app

import (
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/internal/pipetest"
)

// `rta doctor` reads the .mcp.json of the directory it runs in, which is
// whatever that directory holds, and the client's own file in the home
// directory. A named pipe at either held the diagnosis that was run to find
// out what was wrong.
func TestANamedPipeWhereAClientsConfigGoesDoesNotHoldDoctor(t *testing.T) {
	for _, name := range []string{".mcp.json", ".claude.json"} {
		t.Run(name, func(t *testing.T) {
			home, dir := t.TempDir(), t.TempDir()
			pipe := filepath.Join(dir, name)
			if name == ".claude.json" {
				pipe = filepath.Join(home, name)
			}
			pipetest.Plant(t, pipe)
			pipetest.Returns(t, pipe, "reading "+name, func() { _ = claudeRegistrations(home, dir) })
		})
	}
}
