//go:build unix

package tunnel

import (
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/internal/pipetest"
)

// SSHHosts runs on a keystroke of shell completion, so a named pipe at
// ~/.ssh/config held the shell that asked, and with it every completion of an
// ssh: target.
func TestANamedPipeWhereTheSSHConfigGoesDoesNotHoldCompletion(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config")
	pipetest.Plant(t, cfg)
	saved := sshConfigPath
	sshConfigPath = func() (string, error) { return cfg, nil }
	t.Cleanup(func() { sshConfigPath = saved })
	pipetest.Returns(t, cfg, "listing ssh hosts", func() { _ = SSHHosts() })
}
