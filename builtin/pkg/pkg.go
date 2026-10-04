// Package pkg is what is outdated on this machine, from every package manager
// that is on it, and one upgrade at a time.
//
// # The question it answers
//
// A machine accumulates package managers: the OS's own, Homebrew, a version
// manager like mise, and then every language's global installer — pipx and
// uv for Python, npm and bun for JavaScript, cargo, gem, go install — plus the
// binaries somebody downloaded from a GitHub release and dropped in
// ~/.local/bin. Each answers "what is outdated" in its own shape, and none of
// them knows the others exist. This is one table across all of them, with
// the exact upgrade command on every row, the OS's own pending updates and
// reboot state beside it, and a way to run one upgrade at a time.
//
// # Built in, and why
//
// No credential, nothing beyond the standard library, and a persona of
// everyone with a machine: the same rule that put eol in the binary. The
// deciding fact is the upgrade of a direct binary — fetch, hash, verify,
// extract one member, place atomically — which is the path plugin install
// already walks in internal/plugindist, and a plugin cannot import it. A
// binary installed into ~/.local/bin deserves the same evidence-before-
// placement as a plugin rta will launch, and the way to give it that is to
// call the same functions.
//
// # Shelling out
//
// This is the third built-in that runs a tool on $PATH (builtin/audit's
// kubectl and builtin/kv's editor are the others), and it runs a dozen. The
// shape is kubectl.go's: a package-level seam for tests, a bounded context,
// stdin closed, the first line of stderr as the message. Every manager is one
// file that knows its list command and its upgrade command, and adding one is
// adding a file and a line to the list in manager.go.
//
// # None of it is reachable by an agent
//
// Every capability here is HumanOnly — never an MCP tool, reads included —
// the way builtin/agent and builtin/lock are. The reads are the reason as
// much as the upgrade: a table of what is installed on this host, with the
// versions that are behind, is the vulnerability map an attacker builds
// first, and an agent that could read it could hand it out. The upgrade is
// the other half — an agent that could run `apt-get upgrade` or place a
// binary on $PATH would hold exactly the authority the harness deny lists
// exist to keep from it. The right answer to both is not "with a grant" but
// "not here": a person runs these, at the terminal or in the TUI.
//
// Within that wall the read tier still keeps its own discipline: the
// registries it asks — PyPI, the npm registry, crates.io, the Go module
// proxy, the GitHub API — are fixed hosts, and the names sent to them come
// from this machine's installed lists, never from a caller. pkg.upgrade is
// destructive, one manager or one tool per call, never everything. rta never
// calls sudo: a manager that needs root gets its command printed and a
// refusal when rta is not root.
package pkg

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// runCommand is the seam every tool invocation goes through: tests replace
// it with recorded output, and nothing else in the package touches os/exec
// for a query.
var runCommand = func(ctx context.Context, name string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	cmd.Stdin = nil
	cmd.WaitDelay = pipeDelay
	err = cmd.Run()
	return out.String(), errBuf.String(), err
}

// pipeDelay bounds how long a finished-or-killed manager may hold its pipes.
//
// A manager that forks something and exits — a background auto-update, a
// version manager's shim — leaves the child holding this process's end of
// stdout, and Wait blocks until it sees EOF: not slow, forever, and past
// listTimeout, because what is stuck is os/exec's copying goroutines rather
// than the process the deadline killed. The same bound internal/tunnel and
// builtin/audit put on every kubectl they start. It only starts counting
// once the process has exited or been killed, so a healthy run never meets
// it.
const pipeDelay = 2 * time.Second

// lookPath is the seam for detection. exec.LookPath is the right call for
// "which managers are here": the question is about $PATH itself.
var lookPath = exec.LookPath

// listTimeout bounds one manager's list command. `brew outdated` can take
// ten seconds on a cold tap; anything past a minute is a manager hung on the
// network, and the table should say so rather than never appear.
const listTimeout = 60 * time.Second

// run executes one query and classifies the failure the way kubectl.go does:
// a non-zero exit is a failure, and its message is the first line of what
// the tool said on stderr.
//
// It once handed every non-zero exit back undecided, since a few managers
// exit non-zero to answer (dnf 100 for "updates available", npm 1 for
// "something is outdated"), and only dnf ever looked. A list that failed —
// mise refusing an untrusted config, npm behind a shim that could not find
// its runtime — wrote its reason to stderr and nothing to stdout, every
// other parser read no lines as nothing behind, and the manager's row said
// ok. Nothing is an answer now until the tool exits zero, and a caller
// whose tool answers with its status asks runStatus and reads it there.
func run(ctx context.Context, name string, args ...string) (string, *view.Error) {
	st, verr := runStatus(ctx, name, args...)
	if verr != nil {
		return "", verr
	}
	if st.code != 0 {
		return "", st.failed(name)
	}
	return st.out, nil
}

// status is what a query answered when its exit status is part of the
// answer: stdout, the status, and the first line of stderr — the reason,
// when the caller decides the status meant a failure after all.
type status struct {
	out    string
	code   int
	reason string
	// stderr is the whole of it, for the one caller that reads more there
	// than a reason: pipx names each venv it could not read on a line.
	stderr string
}

// failed is the refusal for a status the caller does not read as an answer.
func (s status) failed(name string) *view.Error {
	return view.Errorf("pkg.manager.failed", "%s: %s", name, firstLine(s.reason, fmt.Sprintf("exited %d and said nothing on stderr", s.code)))
}

// runStatus is run with the exit status handed back undecided, for the tools
// that answer with it: dnf check-update's 100, npm outdated's 1, pacman -Qu's
// 1 for nothing matched, needs-restarting's 1 for a reboot owed, and a
// binary's own --version, whose output says what it says whatever the status.
// A timeout, a missing binary and a process that never ran are failures
// here too: none of them answered anything.
func runStatus(ctx context.Context, name string, args ...string) (status, *view.Error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	out, stderr, err := runCommand(ctx, name, args...)
	// ErrWaitDelay is only ever returned for a process that exited zero —
	// the answer is complete and something else was holding the pipes.
	if err == nil || errors.Is(err, exec.ErrWaitDelay) {
		return status{out: out}, nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return status{}, view.Errorf("pkg.manager.timeout", "%s did not answer within %s", name, listTimeout).
			WithHint("a manager hung on its registry looks exactly like this — try it by hand")
	}
	var notFound *exec.Error
	if errors.As(err, &notFound) {
		return status{}, view.Errorf("pkg.manager.missing", "%s is not on this machine's PATH", name)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return status{out: out, code: exit.ExitCode(), reason: firstLine(stderr, ""), stderr: stderr}, nil
	}
	return status{}, view.Errorf("pkg.manager.failed", "%s: %s", name, firstLine(stderr, err.Error()))
}

func firstLine(s, fallback string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return fallback
	}
	return s
}

// host marks a capability as describing this machine and closes it to
// agents: HostSpecific for the remote transport's own rule, HumanOnly for
// every transport, because the inventory of what is installed here is not
// for an agent on any of them.
//
// NoPreview keeps every one of these off the automatic dashboard, and
// Refresh is for the person who names pkg.overview in their config anyway:
// a run is a dozen managers' list commands plus a registry query per
// installed package, and what it reports changes when a release ships,
// not between one five-second tick and the next. An hour is the pace a
// person checks for updates at when they are diligent.
func host(c plugin.Capability) plugin.Capability {
	c.HostSpecific = true
	c.HumanOnly = true
	c.NoPreview = true
	c.Refresh = time.Hour
	return c
}

// Plugin returns the pkg plugin declaration.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "pkg",
		Summary: "What is outdated on this machine — every package manager, your own binaries, the OS — and one upgrade at a time",
		Capabilities: []plugin.Capability{
			overviewCapability(),
			managersCapability(),
			outdatedCapability(),
			toolsCapability(),
			osCapability(),
			upgradeCapability(),
		},
	}
}
