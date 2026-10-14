//go:build race || slowhost

package pluginhost

import "time"

// Under the race detector, or in a build that asks for the same bounds with
// the slowhost tag, a plugin takes longer to start than any real one ever
// will.
//
// The bounds in host.go describe a real machine: fork, exec and one write,
// with headroom for a Gatekeeper scan or a cold network filesystem. A
// race-instrumented test binary spawning a race-instrumented *plugin* binary
// is not that machine, and it is slower in a way production never is, because
// nothing rta ships is built with -race.
//
// The symptom was `make hard` failing intermittently in the three packages
// that spawn a real plugin — pluginhost, plugindist, tunnel — always with
// "timeout while waiting for plugin to start", always passing when those
// packages were re-run alone. A suite people re-run is a suite they stop
// reading, which is expensive exactly once: the run where the failure was
// real.
//
// **What that measurement could not separate**, and it is worth writing down
// rather than implying otherwise: the machine it was taken on was carrying an
// unrelated CPU load heavy enough to put it at four times its core count, so
// how much of the timing was instrumentation and how much was contention is
// not a question those runs answer. What stands on its own is the asymmetry —
// the instrumented build is slower, production is never instrumented — and
// that a spawn bound tight enough to be useful on a real machine has nothing
// to say about a test host, shared CI runner included, that is busy with
// something else.
//
// **What can be separated now** is what a first run costs on its own, measured
// on a laptop at a load average of four: a two-line shell script written a
// moment before, a plugin just built, a copy of a binary that had already run
// and a copy of that with a new modification time each took 120 to 320 ms the
// first time and 5 to 10 ms the second, and the call that starts the process
// returned within a few milliseconds either way, so the wait is spent after
// the exec, inside the handshake window. That is the operating system looking
// at a file it has not seen run. Every new file pays it, a copy no less than a
// build, and it is not instrumentation: it is there in a build with no -race
// at all. A suite that writes or builds dozens of programs and starts each one
// pays it dozens of times at once, and it has been seen to pass five seconds.
//
// Hence the tag. The end-to-end tests in cmd/rta drive a binary built without
// -race, because an instrumented one would slow every invocation they make,
// and that binary kept the production bounds while the test process around it
// had the wide ones: a stand-in that exits at once, and a plugin that `rta
// plugin dev` had just compiled, timed out in the race pass of `make ci`.
// Nothing a test does can warm what `rta plugin dev` builds for itself, so
// the bound is what gives, and TestMain builds that binary with the tag.
//
// The alternatives were both worse for the shipped behaviour. Raising the
// product timeout slows every genuine failure on every user's machine to fix
// a problem no user has. Capping `go test -p` so the suite competes with
// itself less measured noticeably slower on every run, and does nothing about
// load that is not the suite's. This is the narrowest change that is also
// true, and it moves nothing a user ever runs.
//
// Kept well under the minute go-plugin would spend on its own default, which
// is the bound TestABinaryThatIsNotAPluginFailsQuickly exists to defend —
// that test derives its own limit from startTimeout so the two cannot drift.
func init() {
	startTimeout = 20 * time.Second
	describeTimeout = 20 * time.Second
}
