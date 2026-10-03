package tunnel

import (
	"os/exec"
	"runtime"
)

// startPinned starts cmd and waits for it, from one goroutine that keeps its
// OS thread until the command has exited, and returns a channel that closes
// when it has.
//
// **Linux ties a child's parent-death signal to the thread that started it,
// not to the process** (prctl(2), PR_SET_PDEATHSIG), and Go starts a child
// from whichever thread the goroutine happens to be on: one that goes away
// takes the child with it. harden asks for that signal, so that a kubectl or
// ssh whose parent was killed outright — a SIGKILL, the OOM killer — does not
// go on listening on a loopback port into the operator's cluster with nothing
// watching it; holding the thread is what makes the signal mean the process
// died and nothing less. Elsewhere pinning costs nothing and the thread goes
// back to the pool when the command is done.
//
// track says the child is a forward that outlives the call that opened it and
// is to be remembered (remember) for the platforms with no parent-death
// signal, to be stopped by the next start if this process cannot.
func startPinned(cmd *exec.Cmd, track bool) (<-chan struct{}, error) {
	exited := make(chan struct{})
	started := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(exited)
		if err := cmd.Start(); err != nil {
			started <- err
			return
		}
		if track {
			remember(cmd)
			defer forget(cmd)
		}
		started <- nil
		_ = cmd.Wait()
	}()
	return exited, <-started
}
