//go:build windows

package tunnel

import "os/exec"

func harden(*exec.Cmd) {}

func reap(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
