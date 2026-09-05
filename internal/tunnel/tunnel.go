// Package tunnel resolves a named target to a local host:port the caller can
// dial, and tears it down afterwards.
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
// maintenance.
package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// Target is one entry in the operator's `targets:` block. Kube and SSH are
// two spellings of one job — a forward filling the same declared endpoint
// inputs — and a target states exactly one of them.
type Target struct {
	// Kube is context/namespace/kind/name:port, e.g.
	// homelab/databases/svc/postgres:5432.
	Kube string `yaml:"kube,omitempty" json:"kube,omitempty"`

	// SSH is a jump-host target, [user@]host[:port]/desthost:destport, e.g.
	// tobi@bastion:2222/vault.internal:8200. The head names where to log in —
	// an ~/.ssh/config alias with everything the config gives it — and the
	// tail what the far end dials once there. See ssh.go for why this shape
	// resolves differently from a kube coordinate.
	SSH string `yaml:"ssh,omitempty" json:"ssh,omitempty"`
	// SecretsFrom is context/namespace, naming where Secret is read from when
	// this target opens no forward at all — the connection that reaches its
	// service directly and keeps only its credentials in a cluster. Ignored
	// when Kube is set, whose own namespace is then the answer; the two are
	// refused together well before this, at config.Check.
	SecretsFrom string `yaml:"secrets-from,omitempty" json:"secrets-from,omitempty"`
	// Secret names a Secret in the coordinate's own namespace holding the
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
	closeOne sync.Once
	// stop is this tunnel's teardown, installed by whichever opener built it:
	// a kube tunnel reaps one kubectl and waits for its exit, an ssh tunnel
	// closes its listener and reaps a child per spliced connection. Close is
	// the only caller, and a tunnel that failed before anything needed
	// tearing down simply never installs one.
	stop func()
	// gaveUp records that a wait fell through to its timeout instead of
	// seeing the exit. Exported to tests through TimedOut, because the defect
	// it exists to catch — two waiters and one consumable signal — shows up
	// as *latency*, and asserting on a wall clock inside a -race -shuffle
	// suite measures the machine's load as much as the code's.
	gaveUp atomic.Bool

	// The kube half: one long-lived kubectl owns the forward.
	cmd *exec.Cmd
	// exited is closed when kubectl has been reaped. A closed channel rather
	// than a value on a buffered one, because two places wait for the exit —
	// awaitForwarding, to be sure stderr is complete, and Close, to be sure
	// the forward is really gone — and a send is consumed by whichever
	// arrives first. That cost two seconds on every failed open before the
	// tests were timed rather than merely watched to pass.
	exited chan struct{}

	// The ssh half: rta owns the listener, and each accepted connection is an
	// `ssh -W` child of its own — see ssh.go for why there is no single
	// long-lived process to point at.
	ln       net.Listener
	mu       sync.Mutex
	closing  bool
	children map[*exec.Cmd]net.Conn
	served   sync.WaitGroup
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
		return view.Errorf("tunnel.target.malformed", "%q is not a kube coordinate: %s", spec, why).
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
		// The same refusal parseSSH makes, for the same reason, and it was
		// missing on this half. kind and name are joined into one bare
		// positional argument for kubectl, so a kind beginning with `-` is
		// read as a flag — measured, not assumed: `--context <v>` and
		// `--namespace <v>` are safe because pflag binds the next token as a
		// value even when it starts with a dash, and the positional is not.
		// A Kubernetes name or a kubectl short kind never begins with one.
		if strings.HasPrefix(seg, "-") {
			return "", "", "", "", 0, bad("a segment may not begin with -")
		}
	}
	return parts[0], parts[1], parts[2], name, p, nil
}

// parseKubeNamespace splits context/namespace — where a connection's Secrets
// are read from, with no service and no forward.
//
// Deliberately its own parser rather than a shorter branch inside parseKube.
// The two answer different questions and only one of them may ever open a
// listener, so a coordinate that names a service cannot arrive here by
// accident and a namespace cannot arrive at the forward path: parseKube still
// insists on four segments and a port, and this insists on exactly two.
func parseKubeNamespace(spec string) (kctx, ns string, verr *view.Error) {
	bad := func(why string) *view.Error {
		return view.Errorf("tunnel.secrets.malformed",
			"%q is not a cluster and namespace: %s", spec, why).
			WithHint("the form is context/namespace, e.g. homelab/databases — a service and " +
				"port belong in `kube:`, which also opens a forward")
	}
	parts := strings.Split(spec, "/")
	if len(parts) != 2 {
		return "", "", bad("want two slash-separated segments")
	}
	for i, seg := range parts {
		if seg == "" {
			return "", "", bad([]string{"empty context", "empty namespace"}[i])
		}
		// parseKube's refusal, for the same reason: both segments become
		// `--context <v>` / `--namespace <v>` in kubectl's argv, and while
		// pflag binds a dashed value to its flag rather than reading it as
		// one, the guarantee is worth holding here too rather than depending
		// on that staying true.
		if strings.HasPrefix(seg, "-") {
			return "", "", bad("a segment may not begin with -")
		}
	}
	return parts[0], parts[1], nil
}

// CheckKubeNamespace reports what is wrong with a `secrets-from:` value,
// without touching a cluster — CheckKube's contract for the read-only half.
func CheckKubeNamespace(spec string) *view.Error {
	_, _, verr := parseKubeNamespace(spec)
	return verr
}

// CheckKube reports what is wrong with a coordinate, without touching a
// cluster.
//
// The static half of "a target that cannot be resolved should be
// visible before the call that needs it, not during". It answers only whether
// the string is a coordinate at all — four slash-separated segments and a
// port — because that is the half that costs nothing and is the half people
// get wrong. Whether the context exists, whether the service is there and
// whether this operator may forward to it are cluster questions, and asking
// them would make `rta doctor` and `rta profile list` spawn a kubectl per
// profile on a path somebody runs while something is already broken.
//
// So a coordinate that passes this can still fail to open, and the failure is
// classified where it happens. What it catches is the typo, which is the case
// that would otherwise be discovered by a call in the middle of a piece of
// work.
func CheckKube(spec string) *view.Error {
	_, _, _, _, _, verr := parseKube(spec)
	return verr
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
	switch {
	case t.Kube != "" && t.SSH != "":
		// Belt and braces: config.Check refuses this long before a call, but
		// this package must not pick one of two stated reaches by itself.
		return nil, view.Errorf("tunnel.target.twice",
			"profile %q states two tunnels — `kube:` and `ssh:`", name).
			WithHint("a call opens one forward; keep whichever names where this connection really goes")
	case t.SSH != "":
		return openSSH(ctx, name, t)
	case t.Kube == "":
		return nil, view.Errorf("tunnel.target.empty", "profile %q declares no tunnel", name).
			WithHint("give it a `kube:` coordinate or an `ssh:` target")
	}
	kctx, ns, kind, obj, port, verr := parseKube(t.Kube)
	if verr != nil {
		return nil, verr
	}
	if _, err := exec.LookPath(kubectl); err != nil {
		return nil, view.Errorf("tunnel.kubectl.missing",
			"profile %q needs kubectl and it is not on $PATH", name).
			WithHint("rta shells out rather than linking client-go, so the cluster " +
				"credentials you already have keep working — install kubectl, or " +
				"give the plugin a plain --host and --port")
	}

	// Its own process group, so a kubectl that ignores its parent's death is
	// still reaped — the same reason pluginhost sets Setpgid.
	cmd := exec.CommandContext(ctx, kubectl,
		"--context", kctx, "--namespace", ns,
		// `--` for the same reason secret.go carries one: kind/obj is a bare
		// positional, and parseKube's dash refusal is the primary guard
		// rather than the only one.
		"port-forward", "--", kind+"/"+obj, fmt.Sprintf(":%d", port))
	harden(cmd)
	// Without this, a forward that *fails* while a credential helper still
	// holds the pipes takes both non-context arms of awaitForwarding's select
	// out at once: the scanner never sees EOF so `lines` is never closed, and
	// Wait never returns so `exited` never closes. Open then sits for the
	// whole openCeiling and reports tunnel.open.timeout — "did not come up in
	// time" — throwing away kubectl's real, already-buffered explanation. It
	// is the first failed open that pays this, not a long-running one.
	cmd.WaitDelay = waitDelay

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
	tun.stop = tun.stopKube
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
		found := false
		for sc.Scan() {
			if found {
				// kubectl writes "Handling connection for <port>" to stdout
				// for every connection it accepts, *before* it moves a byte.
				// Returning at the match left nobody reading that pipe for the
				// life of the tunnel, so at 64 KiB — about 2100 of those lines
				// — kubectl blocks inside its own Fprintf: a forward that goes
				// on accepting connections and carries none of them, which
				// looks from the plugin's side like a successful connect
				// followed by silence. Read and drop; Wait closes the pipe.
				continue
			}
			if m := forwarding.FindStringSubmatch(sc.Text()); m != nil {
				p, _ := strconv.Atoi(m[2])
				lines <- m[1] + " " + strconv.Itoa(p)
				found = true
			}
		}
		// Only when no listener line ever arrived: closing it after a
		// successful match would tell awaitForwarding the forward had failed.
		if !found {
			close(lines)
		}
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
			"profile %q did not come up in time", name).
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
		return view.Errorf("tunnel.context.unknown", "profile %q names a kube context that does not exist", name).
			WithHint("`kubectl config get-contexts` lists them")
	case strings.Contains(s, "not found"):
		return view.Errorf("tunnel.service.missing", "%s: %s", name, one).
			WithHint("check the namespace and the service name in `" + spec + "`")
	case notAuthenticated(s):
		// **401 and 403 are different questions and were one answer.** Both
		// landed on "not allowed to port-forward there — the verb is `create`
		// on `pods/portforward`", which sends somebody to audit RBAC when
		// what expired is their login. On any cluster reached through an exec
		// credential plugin — Teleport, `aws eks get-token`,
		// `gke-gcloud-auth-plugin`, an OIDC refresh — that is the ordinary
		// daily failure, and it is the one this said the least useful thing
		// about.
		return view.Errorf("tunnel.unauthenticated",
			"profile %q: this cluster does not know who you are", name).
			WithHint(loginHint(spec))
	case strings.Contains(s, "forbidden"):
		return view.Errorf("tunnel.denied", "%s: not allowed to port-forward there", name).
			WithHint("this is the cluster refusing, not rta — you are authenticated, and the " +
				"verb is `create` on `pods/portforward`")
	case strings.Contains(s, "address already in use"):
		return view.Errorf("tunnel.port.taken", "%s: the local port kubectl chose is already in use", name).
			WithHint("retrying usually picks another")
	case s == "":
		return view.Errorf("tunnel.open.failed", "profile %q: kubectl exited without forwarding", name).
			WithHint("`kubectl --context … port-forward` by hand shows why")
	default:
		return view.Errorf("tunnel.open.failed", "%s: %s", name, one).
			WithHint("that message is kubectl's; rta shells out to it so your cluster credentials keep working")
	}
}

// notAuthenticated reports whether kubectl's stderr is about identity rather
// than permission.
//
// The two shapes are the API server's 401 and the credential plugin failing
// before there is a request to make at all. The second one never reached the
// cluster, so it cannot be a permissions answer however it is worded.
func notAuthenticated(stderr string) bool {
	for _, s := range []string{
		"Unauthorized",
		"You must be logged in to the server",
		"getting credentials", // kubectl's own wrapper around an exec plugin
		"exec plugin",         // "exec plugin: invalid apiVersion", and friends
		"credential plugin",   //
		"no Auth Provider found",
	} {
		if strings.Contains(stderr, s) {
			return true
		}
	}
	return false
}

// loginHint names the command that fixes it, by asking kubectl locally which
// credential helper this context uses.
//
// **The generic version of this hint is useless and the specific one is one
// cheap question away.** "Log in again" leaves somebody to work out which of
// ten contexts, which tool, and which cluster flag; `kubectl config view` is
// a local read of a file, contacts nothing, and answers all three. It runs
// only here, on a path where something has already stopped.
//
// Anything unexpected falls back to the general sentence rather than to a
// guess: a wrong command in a hint is worse than no command.
func loginHint(spec string) string {
	kctx, _, _, _, _, verr := parseKube(spec)
	if verr != nil {
		return "authenticate to the cluster again — `kubectl get ns --context …` fails the same way"
	}
	helper, args := credentialHelper(kctx)
	switch {
	case helper == "":
		return "authenticate to the cluster again — `kubectl get ns --context " + kctx +
			"` fails the same way, and succeeds once you have"
	case helper == "tsh":
		if cluster := flagValue(args, "--kube-cluster"); cluster != "" {
			return "this context authenticates through Teleport — `tsh kube login " + cluster +
				"` renews it, and `tsh status` says when it expired"
		}
		return "this context authenticates through Teleport — `tsh kube login` renews it"
	}
	return "this context authenticates through `" + helper +
		"` — run whatever renews that, then retry; `kubectl get ns --context " + kctx +
		"` is the same question without rta in the way"
}

// credentialHelper is the exec plugin a context authenticates with, and its
// arguments — empty when the context uses none, or when kubectl cannot say.
func credentialHelper(kctx string) (string, []string) {
	out, err := exec.Command(kubectl, "config", "view", "--minify",
		"--context="+kctx, "-o", "jsonpath={.users[0].user.exec.command} {.users[0].user.exec.args}").Output()
	if err != nil {
		return "", nil
	}
	fields := strings.Fields(strings.NewReplacer("[", " ", "]", " ").Replace(string(out)))
	if len(fields) == 0 {
		return "", nil
	}
	return filepath.Base(fields[0]), fields[1:]
}

// flagValue reads --name=value out of an argument list.
func flagValue(args []string, name string) string {
	for i, a := range args {
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
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
		if t.stop != nil {
			t.stop()
		}
	})
}

// stopKube ends the one kubectl that owns a kube forward.
func (t *Tunnel) stopKube() {
	if t.cmd.Process != nil {
		reap(t.cmd)
	}
	select {
	case <-t.exited:
	case <-time.After(2 * time.Second):
		t.gaveUp.Store(true)
	}
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
