//go:build !darwin

package pluginhost

// Linux and Windows run plugins unconfined, and rta says so rather than
// implying otherwise.
//
// Linux is a deliberate removal, not an omission. The Landlock allowlist was
// designed, and dropped whole: `go install ./cmd/rta` is today's install path
// and defaults to CGO_ENABLED=1; go-landlock's pre-ABI-8 path uses
// unix.AllThreadsSyscall, which returns ENOTSUP in a cgo binary, and
// BestEffort() downgrades rulesets rather than syscall errors. Under the
// fail-closed rule this package applies everywhere else, that is *no plugin
// ever spawns* — in the milestone whose acceptance gate is a stranger
// shipping a hello-plugin in fifteen minutes.
//
// What Linux does get is in host.go and applies on every platform: the
// environment allowlist, process-group reaping, PR_SET_DUMPABLE=0, mTLS, and
// the descriptor handling. Those are the parts that were load-bearing anyway.

//
// Windows gets CREATE_NEW_PROCESS_GROUP (procattr_windows.go) — one handle,
// one kill, no orphans — documented there as lifetime control and not as
// confinement. A job object with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE would be
// the stronger form; it is not what runs today, and procattr_windows.go
// says so rather than letting this file's own silence about it read as
// "already done".

const confined = false

func profile(DenySet) string { return "" }

// wrap runs the command unchanged. It exists so the spawn path has no
// platform branch in it: one code path that is sometimes wrapped, rather than
// two that drift.
func wrap(_ DenySet, name string, args []string) (string, []string) { return name, args }

func available() error { return nil }

// Confined reports whether this platform confines plugin processes.
func Confined() bool { return confined }
