package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// This plugin drives the `docker` CLI rather than linking the Engine SDK, for
// the reasons internal/tunnel records for kubectl, and
// plugins/kube repeats: the SDK is a large dependency for a small surface,
// and — the bigger half — connecting to a daemon is a solved problem on the
// operator's machine. Docker Desktop's socket, a rootless socket, a remote
// context over SSH, a TLS-secured DOCKER_HOST: the CLI already knows how to
// reach all of them, and every one of those is a thing this would otherwise
// have to reimplement and keep working.

// dockerBin is overridable in tests, which have no daemon and must not need
// one. Nothing outside this package writes to it.
var dockerBin = "docker"

// timeout bounds one docker invocation.
//
// The daemon is usually local and answers instantly; when it is not running,
// the CLI can sit waiting on a socket that will never answer. A call nobody
// is waiting for is a subprocess holding a slot in the plugin host.
const timeout = 20 * time.Second

// stopTimeout is how long `docker stop` gives a container to exit on its own
// before the daemon kills it, and it is deliberately not the CLI's default
// of ten seconds.
//
// A container that ignores SIGTERM is common and a container that needs
// longer than ten seconds to flush is not exotic — a database, a queue
// worker mid-batch. rta's own bound has to be past the daemon's so that what
// gives up first is the daemon, with its own reason, rather than this
// process abandoning a stop that was about to succeed.
const stopSeconds = 10

// nameRe is what may be passed to docker as a container, image or context
// name.
//
// **Argument-injection defence.** Values are interpolated into an argv slice
// and never into a shell string, so there is no shell to escape into — but a
// value beginning with `-` is read by docker as a *flag*, and something like
// `--host=tcp://elsewhere` arriving where a container name was expected would
// point the call at a different daemon. Every flag here is passed in the
// `--flag=value` form, which already keeps the value in its own argv element;
// this refuses it earlier and a second time, so a future caller reaching for
// the two-element form cannot reintroduce it.
//
// Docker's own rule for names is [a-zA-Z0-9][a-zA-Z0-9_.-]*; ids are hex,
// and image references add slashes, colons, at-signs and (for a digest) the
// sha256: prefix.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,252}$`)

func checkName(kind, v string) *view.Error {
	if v == "" {
		return nil
	}
	if !nameRe.MatchString(v) {
		return view.Errorf("docker.name.invalid", "%q is not a usable %s name", v, kind).
			WithHint("names are letters, digits and ._:/@- and may not begin with a dash")
	}
	return nil
}

// connection is which daemon a call is aimed at.
type connection struct {
	Host    string
	Context string
}

func connectionOf(req plugin.Request) (connection, *view.Error) {
	c := connection{
		Host:    strings.TrimSpace(req.String("host")),
		Context: strings.TrimSpace(req.String("context")),
	}
	if verr := checkName("context", c.Context); verr != nil {
		return connection{}, verr
	}
	// A host is a URL rather than a name, so nameRe would refuse legitimate
	// values. What matters is the same thing: it must not be read as a flag.
	if strings.HasPrefix(c.Host, "-") {
		return connection{}, view.Errorf("docker.host.invalid",
			"%q is not a usable daemon address", c.Host)
	}
	return c, nil
}

func (c connection) args(rest ...string) []string {
	out := []string{}
	if c.Host != "" {
		out = append(out, "--host="+c.Host)
	}
	if c.Context != "" {
		out = append(out, "--context="+c.Context)
	}
	return append(out, rest...)
}

// run executes docker and returns its stdout.
//
// stderr is captured separately and never merged: stdout is parsed, and the
// CLI writes warnings and progress to stderr, so merging them would turn a
// good answer into a parse error.
func run(ctx context.Context, c connection, args ...string) ([]byte, *view.Error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, dockerBin, c.args(args...)...)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	// No stdin: nothing here prompts, and a subprocess that inherited this
	// process's stdin would be reading the plugin host's gRPC channel.
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	return nil, classify(ctx, err, errBuf.String(), args)
}

// classify turns a docker failure into something an operator can act on.
func classify(ctx context.Context, err error, stderr string, args []string) *view.Error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return view.Errorf("docker.unreachable", "docker did not answer within %s", timeout).
			WithHint("the daemon may not be running — `docker info` is the same question")
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return view.Errorf("docker.cancelled", "the call was cancelled")
	}
	var notFound *exec.Error
	if errors.As(err, &notFound) {
		return view.Errorf("docker.cli.missing", "docker is not on this machine's PATH").
			WithHint("this plugin drives the docker CLI rather than linking the Engine SDK, so it " +
				"needs the binary the operator already uses — install it, or put it on PATH")
	}
	msg := firstLine(stderr)
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "cannot connect to the docker daemon"),
		strings.Contains(low, "is the docker daemon running"),
		strings.Contains(low, "connection refused"):
		return view.Errorf("docker.unreachable", "%s", msg).
			WithHint("start Docker, or point this at the right daemon with the host input")
	case strings.Contains(low, "permission denied"):
		return view.Errorf("docker.denied", "%s", msg).
			WithHint("this account cannot reach the daemon's socket")
	case strings.Contains(low, "no such container"), strings.Contains(low, "no such object"),
		strings.Contains(low, "no such image"):
		return view.Errorf("docker.notfound", "%s", msg).
			WithHint("`rta docker container list --all` shows what is there, stopped ones included")
	case strings.Contains(low, "context") && strings.Contains(low, "not found"):
		return view.Errorf("docker.context.unknown", "%s", msg)
	case msg != "":
		return view.Errorf("docker.failed", "%s", msg)
	}
	return view.Errorf("docker.failed", "docker %s failed: %v", strings.Join(args, " "), err)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(strings.TrimPrefix(s, "Error response from daemon:"))
}

// jsonLines decodes the CLI's `--format json` output, which is one JSON
// object per line rather than a JSON array.
func jsonLines[T any](raw []byte) ([]T, *view.Error) {
	var out []T
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item T
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, view.Errorf("docker.unreadable", "docker's answer could not be read: %v", err)
		}
		out = append(out, item)
	}
	return out, nil
}

// short trims an id to the twelve characters docker itself displays.
func short(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// plural is one word or two.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// count renders "1 container" / "3 containers".
func count(n int, one, many string) string {
	return fmt.Sprintf("%d %s", n, plural(n, one, many))
}
