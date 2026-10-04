package tunnel

import "syscall"

// deathSignal is nothing here: macOS has no parent-death signal.
// The forwards a killed process leaves behind are found by the next start
// instead (remember, ReapOrphans).
func deathSignal(*syscall.SysProcAttr) {}
