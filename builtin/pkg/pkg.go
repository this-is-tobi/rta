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
// Every capability here refuses the MCP surface outright, reads included,
// the way builtin/agent and builtin/lock do. The reads are the reason as
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
	"os/exec"
	"runtime"
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
	err = cmd.Run()
	return out.String(), errBuf.String(), err
}

// lookPath is the seam for detection. exec.LookPath is the right call for
// "which managers are here": the question is about $PATH itself.
var lookPath = exec.LookPath

// listTimeout bounds one manager's list command. `brew outdated` can take
// ten seconds on a cold tap; anything past a minute is a manager hung on the
// network, and the table should say so rather than never appear.
const listTimeout = 60 * time.Second

// run executes one query and classifies the failure the way kubectl.go does.
// A non-zero exit with output is returned to the caller undecided: several
// managers exit non-zero to mean "there are updates" (dnf 100, npm 1), and
// only the manager knows.
func run(ctx context.Context, name string, args ...string) (string, int, *view.Error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	out, stderr, err := runCommand(ctx, name, args...)
	if err == nil {
		return out, 0, nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", 0, view.Errorf("pkg.manager.timeout", "%s did not answer within %s", name, listTimeout).
			WithHint("a manager hung on its registry looks exactly like this — try it by hand")
	}
	var notFound *exec.Error
	if errors.As(err, &notFound) {
		return "", 0, view.Errorf("pkg.manager.missing", "%s is not on this machine's PATH", name)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out, exit.ExitCode(), nil
	}
	return "", 0, view.Errorf("pkg.manager.failed", "%s: %s", name, firstLine(stderr, err.Error()))
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

// host marks a capability as describing this machine and closes it to the
// MCP surface. HostSpecific hides it from a remote-transport server; the
// wrapper refuses a local one too, because the inventory of what is installed
// here is not for an agent on any transport.
func host(c plugin.Capability) plugin.Capability {
	c.HostSpecific = true
	c.NoPreview = true
	c.Run = humanOnly(c.Run)
	return c
}

func humanOnly(h plugin.Handler) plugin.Handler {
	return func(ctx context.Context, req plugin.Request) (view.View, error) {
		if req.Surface() == plugin.SurfaceMCP {
			return nil, view.Refusef("pkg.human",
				"what is installed on this machine, and upgrading it, belong to the person at the terminal, not to a caller over MCP").
				WithHint("ask the operator to run `rta pkg overview`")
		}
		return h(ctx, req)
	}
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

// supported says whether this platform is one the managers here know. The
// list commands and the reboot conventions are Unix-shaped; Windows has its
// own managers and none of the assumptions below, so it is refused plainly
// rather than answered with an empty table.
func supported() *view.Error {
	if runtime.GOOS == "windows" {
		return view.Errorf("pkg.unsupported", "pkg does not know Windows package managers yet").
			WithHint("winget and scoop have their own shapes; this is a Unix-shaped built-in for now")
	}
	return nil
}
