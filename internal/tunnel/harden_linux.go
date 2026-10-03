//go:build linux

package tunnel

import "syscall"

// deathSignal asks the kernel to kill the child when the process that started
// it dies, however it dies: a forward whose parent was SIGKILLed or killed for
// memory is a listener nobody is watching, and nothing in this process can
// close it once this process is gone. SIGKILL and not SIGTERM, because there
// is no second chance to ask again. Only the child itself is signalled, which
// is kubectl or ssh and not the credential helper beside it; the helper's
// life ends with the connection it was serving. startPinned is what keeps the
// signal from firing early.
func deathSignal(attr *syscall.SysProcAttr) { attr.Pdeathsig = syscall.SIGKILL }
