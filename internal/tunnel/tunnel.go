// Package tunnel resolves a named target to a local host:port the caller can
// dial, and tears it down afterwards (ADR 0018).
//
// The operator names targets in configuration; a caller — including an agent
// over MCP — selects one by name and can never supply a cluster coordinate of
// its own. The reach a target grants is reach the operator already granted by
// writing it down; what a target adds over a plain configured host is that
// resolving it is an *action*, which is why it is named rather than typed.
//
// kube targets shell out to `kubectl port-forward` rather than linking
// client-go. Measured for the surface a resolver needs — clientcmd, spdy,
// portforward — that dependency is +67 modules and +11 MB on a 27 MB binary,
// for a feature a minority of installs uses. The rest of the argument is
// authentication: OIDC refresh, `aws eks get-token`, `gke-gcloud-auth-plugin`
// and whatever exec credential plugin an organisation runs are all solved on
// the user's machine already, and adopting client-go means adopting their
// maintenance. ADR 0018 §3.
package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Target is one entry in the operator's `targets:` block.
type Target struct {
	// Kube is context/namespace/kind/name:port, e.g.
	// homelab/databases/svc/postgres:5432.
	Kube string `yaml:"kube,omitempty" json:"kube,omitempty"`
	// Secret names a Secret in the target's own namespace holding the
	// credentials for the thing at the other end of the tunnel.
	Secret string `yaml:"secret,omitempty" json:"secret,omitempty"`
	// From maps a declared input name to a key in that Secret. Written by the
	// operator, never by the plugin — see Secrets.
	From map[string]string `yaml:"from,omitempty" json:"from,omitempty"`
}

// Endpoint is what a resolved target hands back: a local address a plugin can
// dial, knowing nothing about how it got there.
type Endpoint struct {
	Host string
	Port int
}

// Tunnel is a live forward. Close is idempotent and safe from any goroutine.
type Tunnel struct {
	Endpoint
	cmd      *exec.Cmd
	closeOne sync.Once
	// gaveUp records that a wait fell through to its timeout instead of
	// seeing the exit. Exported to tests through TimedOut, because the defect
	// it exists to catch — two waiters and one consumable signal — shows up
	// as *latency*, and asserting on a wall clock inside a -race -shuffle
	// suite measures the machine's load as much as the code's.
	gaveUp atomic.Bool
	// exited is closed when kubectl has been reaped. A closed channel rather
	// than a value on a buffered one, because two places wait for the exit —
	// awaitForwarding, to be sure stderr is complete, and Close, to be sure
	// the forward is really gone — and a send is consumed by whichever
	// arrives first. That cost two seconds on every failed open before the
	// tests were timed rather than merely watched to pass.
	exited chan struct{}
}

// kubectl is overridable in tests, which have no cluster and must not need
// one to exercise the lifecycle.
var kubectl = "kubectl"

// forwarding matches the line kubectl prints once the listener is up:
//
//	Forwarding from 127.0.0.1:54321 -> 5432
//
// Parsed rather than pre-chosen. Binding :0 ourselves and passing an explicit
// LOCAL:REMOTE would avoid the parse, and would race every other process on
// the machine for that port between our close and kubectl's bind — a race
// that fails at connect time, intermittently, on a busy machine. This output
// has been stable for years and a change to it fails loudly here rather than
// silently somewhere else.
var forwarding = regexp.MustCompile(`Forwarding from (\d{1,3}(?:\.\d{1,3}){3}):(\d+)`)

// parseKube splits context/namespace/kind/name:port.
func parseKube(spec string) (ctx, ns, kind, name string, port int, verr *view.Error) {
	bad := func(why string) *view.Error {
		return view.Errorf("tunnel.target.malformed", "%q is not a kube target: %s", spec, why).
			WithHint("the form is context/namespace/kind/name:port, e.g. " +
				"homelab/databases/svc/postgres:5432")
	}
	parts := strings.Split(spec, "/")
	if len(parts) != 4 {
		return "", "", "", "", 0, bad("want four slash-separated segments")
	}
	name, portStr, ok := strings.Cut(parts[3], ":")
	if !ok {
		return "", "", "", "", 0, bad("no :port")
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p < 1 || p > 65535 {
		return "", "", "", "", 0, bad("port is not 1-65535")
	}
	for i, seg := range []string{parts[0], parts[1], parts[2], name} {
		if seg == "" {
			return "", "", "", "", 0, bad([]string{"empty context", "empty namespace",
				"empty kind", "empty name"}[i])
		}
	}
	return parts[0], parts[1], parts[2], name, p, nil
}

// openCeiling is the fallback deadline Open imposes on waiting for the
// listener line, so it can never hang forever regardless of what context a
// caller passes in — see openInstrumented's waitCtx comment. A listener
// ordinarily comes up in a handful of seconds; a minute is generous
// headroom for a slow cluster without being an operator-visible wait on the
// ordinary path, where the loop returns as soon as the line appears.
var openCeiling = 60 * time.Second

// Open resolves t and returns a live tunnel. The caller must Close it.
func Open(ctx context.Context, name string, t Target) (*Tunnel, *view.Error) {
	tun, verr := openInstrumented(ctx, name, t)
	if verr != nil {
		return nil, verr
	}
	return tun, nil
}

// openInstrumented is Open, keeping the Tunnel even when the open failed so
// that tests can ask how the failure was reached. A caller has nothing to do
// with a dead tunnel, which is why Open drops it.
func openInstrumented(ctx context.Context, name string, t Target) (*Tunnel, *view.Error) {
	if t.Kube == "" {
		return nil, view.Errorf("tunnel.target.empty", "target %q declares no tunnel", name).
			WithHint("give it a `kube:` coordinate")
	}
	kctx, ns, kind, obj, port, verr := parseKube(t.Kube)
	if verr != nil {
		return nil, verr
	}
	if _, err := exec.LookPath(kubectl); err != nil {
		return nil, view.Errorf("tunnel.kubectl.missing",
			"target %q needs kubectl and it is not on $PATH", name).
			WithHint("rta shells out rather than linking client-go, so the cluster " +
				"credentials you already have keep working — install kubectl, or " +
				"give the plugin a plain --host and --port")
	}

	// Its own process group, so a kubectl that ignores its parent's death is
	// still reaped — the same reason pluginhost sets Setpgid.
	cmd := exec.CommandContext(ctx, kubectl,
		"--context", kctx, "--namespace", ns,
		"port-forward", kind+"/"+obj, fmt.Sprintf(":%d", port))
	harden(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, view.Errorf("tunnel.open.failed", "%v", err)
	}
	// Guarded, because os/exec writes Stderr from a goroutine it owns while
	// awaitForwarding reads it to classify a failure — the race detector
	// caught this on the first run of the tests below, which is the argument
	// for -race being in the bar rather than an option.
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, view.Errorf("tunnel.open.failed", "could not start kubectl: %v", err)
	}

	tun := &Tunnel{cmd: cmd, exited: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(tun.exited) }()

	// waitCtx bounds only how long we wait for the listener line — a
	// fallback ceiling so Open can never hang forever regardless of what
	// context the caller passed in (every current call site is a test using
	// context.Background(), which never expires on its own). context.WithTimeout
	// always takes the earlier of two deadlines, so a caller-supplied one
	// that is already sooner still wins; this only ever adds a floor under
	// an absent one. Deliberately not the ctx cmd itself runs under (above,
	// unchanged): cancelling waitCtx the moment we are done waiting must
	// not kill a kubectl that only just started forwarding successfully.
	waitCtx, cancel := context.WithTimeout(ctx, openCeiling)
	defer cancel()
	ep, verr := awaitForwarding(waitCtx, stdout, name, t.Kube, stderr, tun.exited, &tun.gaveUp)
	if verr != nil {
		tun.Close()
		// The Tunnel travels back with the error so a test can ask how the
		// failure was reached; Open drops it, because a caller has nothing to
		// do with a dead tunnel.
		return tun, verr
	}
	tun.Endpoint = ep
	return tun, nil
}

// awaitForwarding waits for the listener line, kubectl exiting, or the
// context. Whichever happens first is the answer.
func awaitForwarding(ctx context.Context, stdout io.Reader, name, spec string,
	stderr *syncBuffer, exited <-chan struct{}, gaveUp *atomic.Bool) (Endpoint, *view.Error) {
	lines := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if m := forwarding.FindStringSubmatch(sc.Text()); m != nil {
				p, _ := strconv.Atoi(m[2])
				lines <- m[1] + " " + strconv.Itoa(p)
				return
			}
		}
		close(lines)
	}()

	select {
	case line, ok := <-lines:
		if !ok {
			// stdout closed without a listener line: wait for the exit so
			// stderr is complete before it is read.
			select {
			case <-exited:
			case <-time.After(2 * time.Second):
				gaveUp.Store(true)
			}
			return Endpoint{}, kubectlFailed(name, spec, stderr.String())
		}
		host, portStr, _ := strings.Cut(line, " ")
		p, _ := strconv.Atoi(portStr)
		return Endpoint{Host: host, Port: p}, nil
	case <-exited:
		return Endpoint{}, kubectlFailed(name, spec, stderr.String())
	case <-ctx.Done():
		return Endpoint{}, view.Errorf("tunnel.open.timeout",
			"target %q did not come up in time", name).
			WithHint("`kubectl --context … port-forward` by hand shows what it is waiting for")
	}
}

// kubectlFailed turns kubectl's stderr into something actionable. Its messages
// are stable and specific, which is most of why shelling out is tolerable:
// the failure an operator sees is the one they already know how to read.
func kubectlFailed(name, spec, stderr string) *view.Error {
	s := strings.TrimSpace(stderr)
	one := s
	if i := strings.IndexByte(one, '\n'); i >= 0 {
		one = one[:i]
	}
	switch {
	case strings.Contains(s, "context") && strings.Contains(s, "does not exist"):
		return view.Errorf("tunnel.context.unknown", "target %q names a kube context that does not exist", name).
			WithHint("`kubectl config get-contexts` lists them")
	case strings.Contains(s, "not found"):
		return view.Errorf("tunnel.service.missing", "%s: %s", name, one).
			WithHint("check the namespace and the service name in `" + spec + "`")
	case strings.Contains(s, "forbidden") || strings.Contains(s, "Unauthorized"):
		return view.Errorf("tunnel.denied", "%s: not allowed to port-forward there", name).
			WithHint("this is the cluster refusing, not rta — the verb is `create` on `pods/portforward`")
	case strings.Contains(s, "address already in use"):
		return view.Errorf("tunnel.port.taken", "%s: the local port kubectl chose is already in use", name).
			WithHint("retrying usually picks another")
	case s == "":
		return view.Errorf("tunnel.open.failed", "target %q: kubectl exited without forwarding", name).
			WithHint("`kubectl --context … port-forward` by hand shows why")
	default:
		return view.Errorf("tunnel.open.failed", "%s: %s", name, one).
			WithHint("that message is kubectl's; rta shells out to it so your cluster credentials keep working")
	}
}

// TimedOut reports whether any wait for kubectl's exit fell through to its
// timeout. False on every healthy path, including every failure kubectl
// reports itself.
func (t *Tunnel) TimedOut() bool { return t != nil && t.gaveUp.Load() }

// Close tears the forward down. Idempotent.
func (t *Tunnel) Close() {
	if t == nil {
		return
	}
	t.closeOne.Do(func() {
		if t.cmd.Process != nil {
			reap(t.cmd)
		}
		select {
		case <-t.exited:
		case <-time.After(2 * time.Second):
			t.gaveUp.Store(true)
		}
	})
}

// syncBuffer is an io.Writer safe to read while os/exec writes to it.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
