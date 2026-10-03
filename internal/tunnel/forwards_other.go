//go:build !darwin

package tunnel

import "os/exec"

// Where the kernel can say a child is not to outlive its parent (Linux,
// deathSignal) nothing is written down, and elsewhere there is no way to tell
// a forward from a process that was given its pid later, so there is nothing
// to stop on a start.
func remember(*exec.Cmd) {}

func forget(*exec.Cmd) {}

// ReapOrphans is how many forwards a previous process left running that this
// one stopped: none, on a platform that keeps no record of them.
func ReapOrphans() int { return 0 }
