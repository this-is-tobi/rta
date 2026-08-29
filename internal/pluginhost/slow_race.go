//go:build race

package pluginhost

import "time"

// Under the race detector, a plugin takes longer to start than any real one
// ever will.
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
