//go:build !windows && !linux

package tunnel

import "syscall"

// deathSignal is nothing here: no parent-death signal exists outside Linux.
// The forwards a killed process leaves behind are found by the next start
// instead (remember, ReapOrphans).
func deathSignal(*syscall.SysProcAttr) {}
