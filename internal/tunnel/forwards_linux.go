package tunnel

import "os/exec"

// The kernel ends a forward with the server that started it (deathSignal), so
// nothing is written down and there is nothing to stop on a start.
func remember(*exec.Cmd) {}

func forget(*exec.Cmd) {}

// ReapOrphans is how many forwards a previous process left running that this
// one stopped: none, since a forward never outlives its server here.
func ReapOrphans() int { return 0 }
