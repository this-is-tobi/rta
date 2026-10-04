package operator

import (
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/internal/pipetest"
)

// The three files the operator channel trusts to say who may sign, where the
// signatures go and what signs them are all read by every server start and
// every operator call, and none was opened without waiting: a named pipe
// planted at one held them for good.
func TestANamedPipeWhereAnOperatorFileGoesDoesNotHoldTheChannel(t *testing.T) {
	t.Run("the key", func(t *testing.T) {
		freshHome(t)
		pipetest.Plant(t, Path())
		pipetest.Returns(t, Path(), "reading the operator key", func() { _ = Fingerprint() })
	})
	t.Run("the server list", func(t *testing.T) {
		t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
		pipetest.Plant(t, RemotesPath())
		pipetest.Returns(t, RemotesPath(), "reading the server list", func() { _, _ = ServerURL("prod") })
	})
	t.Run("the roster", func(t *testing.T) {
		roster := filepath.Join(t.TempDir(), "roster")
		pipetest.Plant(t, roster)
		pipetest.Returns(t, roster, "reading the roster", func() { _, _, _ = LoadRoster(roster) })
	})
}
