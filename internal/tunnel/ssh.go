package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// ssh targets: [user@]host[:port]/desthost:destport, resolved by shelling out
// to the operator's own ssh — the kubectl argument (package doc) holds even
// harder here. An SSH client is ~/.ssh/config with Match blocks and Includes,
// ProxyJump chains, an agent, hardware keys and a known_hosts trust store, and
// a Go SSH library adopts the maintenance of all of it; known_hosts handling
// re-done wrong is a security regression, not a dependency saving.
//
// The shape differs from kube in one load-bearing way. kubectl port-forward
// picks a free port and prints it, and rta parses the line. OpenSSH refuses a
// dynamic local forward outright — `-L 0:host:port` is "Bad local forwarding
// specification", verified against OpenSSH 10.2 — and binding a port first to
// pass it with -L is the bind-then-release race the kube path's `forwarding`
// comment already rejects. So for ssh, **rta owns the listener**: it binds
// 127.0.0.1:0 itself, holds the socket, and splices each accepted connection
// through `ssh -W desthost:destport` — the stdio-forward primitive ProxyJump
// is built on. No output is parsed and no port is raced; the cost is one ssh
// process per TCP connection, which the operator's own ControlMaster settings
// collapse to milliseconds, and which a tunnel that lives for one call
// rarely sees more than a handful of.

// sshBin is overridable in tests, which have no bastion and must not need one
// to exercise the lifecycle.
var sshBin = "ssh"

// sshSpec is Target.SSH parsed: where to log in, and what to dial from there.
type sshSpec struct {
	// host is the ssh destination, [user@]host — one argv element, always
	// placed after `--` so a crafted name can never be read as an option.
	host string
	// port is the -p value; empty leaves the port to ssh_config and the
	// default, which is what lets a bare alias carry everything.
	port string
	// dest is host:port the far end dials, the -W argument.
	dest string
}

// parseSSH splits [user@]host[:port]/desthost:destport.
func parseSSH(spec string) (sshSpec, *view.Error) {
	bad := func(why string) *view.Error {
		return view.Errorf("tunnel.target.malformed", "%q is not an ssh target: %s", spec, why).
			WithHint("the form is [user@]host[:port]/desthost:destport, e.g. " +
				"tobi@bastion:2222/vault.internal:8200 — the head is an ~/.ssh/config " +
				"alias or hostname, the tail is what the far end dials")
	}
	head, dest, ok := strings.Cut(spec, "/")
	if !ok || dest == "" {
		return sshSpec{}, bad("no /desthost:destport")
	}
	user, hostport := "", head
	if u, rest, found := strings.Cut(head, "@"); found {
		if u == "" {
			return sshSpec{}, bad("empty user before @")
		}
		user, hostport = u, rest
	}
	host, port := hostport, ""
	if strings.Contains(hostport, ":") {
		h, p, err := net.SplitHostPort(hostport)
		if err != nil {
			return sshSpec{}, bad("the ssh port is not host:port (an IPv6 host needs [brackets])")
		}
		if !validPort(p) {
			return sshSpec{}, bad("ssh port is not 1-65535")
		}
		host, port = h, p
	}
	if host == "" {
		return sshSpec{}, bad("empty host")
	}
	destHost, destPort, err := net.SplitHostPort(dest)
	if err != nil || destHost == "" {
		return sshSpec{}, bad("the destination is not desthost:destport (an IPv6 host needs [brackets])")
	}
	if !validPort(destPort) {
		return sshSpec{}, bad("destination port is not 1-65535")
	}
	// A leading dash is refused rather than escaped. Every one of these lands
	// in ssh's argv; `--` before the destination already stops ssh reading one
	// as an option — verified: `ssh -W … -- -evil` fails on the hostname, not
	// on a flag — and refusing here keeps the guarantee even if the argv shape
	// changes, with a message about the target rather than about ssh.
	for _, s := range []string{user, host, destHost} {
		if strings.HasPrefix(s, "-") {
			return sshSpec{}, bad("a name may not begin with -")
		}
	}
	sshHost := host
	if user != "" {
		sshHost = user + "@" + host
	}
	return sshSpec{host: sshHost, port: port, dest: net.JoinHostPort(destHost, destPort)}, nil
}

func validPort(p string) bool {
	n, err := strconv.Atoi(p)
	return err == nil && n >= 1 && n <= 65535
}

// CheckSSH reports what is wrong with an ssh target, without touching a
// network — CheckKube's contract, for the other scheme. It answers only
// whether the string is a target at all; whether the host resolves, the key is
// accepted and the destination answers are bastion questions, classified where
// they happen.
func CheckSSH(spec string) *view.Error {
	_, verr := parseSSH(spec)
	return verr
}

// sshArgs is the argv every ssh child runs with, probe and splice alike.
//
// -W implies -N, -T, ExitOnForwardFailure and ClearAllForwardings (ssh(1)),
// so a LocalForward the operator's config declares for this alias cannot fire
// alongside and collide. BatchMode because nothing here may prompt: a
// password or host-key question would hang a TUI keypress or an MCP call, and
// the refusal it becomes instead is classified with the command to run by
// hand. Nothing else is overridden — the operator's config working unchanged
// is the reason this shells out.
func sshArgs(s sshSpec) []string {
	args := []string{"-o", "BatchMode=yes"}
	if s.port != "" {
		args = append(args, "-p", s.port)
	}
	return append(args, "-W", s.dest, "--", s.host)
}

// openSSH resolves an ssh target: probe once so a target that cannot be
// resolved fails here with the reason, then bind the local listener the
// endpoint names and serve it until Close.
func openSSH(ctx context.Context, name string, t Target) (*Tunnel, *view.Error) {
	spec, verr := parseSSH(t.SSH)
	if verr != nil {
		return nil, verr
	}
	if _, err := exec.LookPath(sshBin); err != nil {
		return nil, view.Errorf("tunnel.ssh.missing",
			"profile %q needs ssh and it is not on $PATH", name).
			WithHint("rta shells out rather than linking an SSH library, so your " +
				"~/.ssh/config, agent and ProxyJump keep working — install OpenSSH")
	}
	tun := &Tunnel{}
	if verr := probeSSH(ctx, name, spec, tun); verr != nil {
		return tun, verr
	}
	// tcp4 loopback to match the shape every kube endpoint already has; the
	// port is the kernel's answer to :0 on a socket rta keeps holding, which
	// is what makes it race-free where bind-then-release is not.
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return tun, view.Errorf("tunnel.open.failed", "%v", err)
	}
	tun.ln = ln
	tun.children = map[*exec.Cmd]net.Conn{}
	tun.Endpoint = Endpoint{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port}
	tun.stop = tun.stopSSH
	go tun.acceptSSH(ctx, spec)
	return tun, nil
}

// probeSSH answers "can this target be resolved" before anything listens.
//
// Stdin is deliberately absent: os/exec hands the child /dev/null, so ssh
// sees EOF the moment the channel opens and forwards it, and a destination
// that closes on peer EOF — which is any well-behaved TCP server — closes the
// channel back and ssh exits 0. That makes success a deterministic exit code
// rather than a parsed line or a "no news within N ms" guess, at the cost of
// one connect-and-close the destination sees per open. Auth failure, an
// unknown host key, an unresolvable name and an unreachable destination all
// exit non-zero with stderr this package classifies — each verified against a
// real sshd. The known hole: a destination that ignores a half-close parks
// the probe until the ceiling, and the timeout's hint says so.
func probeSSH(ctx context.Context, name string, spec sshSpec, tun *Tunnel) *view.Error {
	cmd := exec.CommandContext(ctx, sshBin, sshArgs(spec)...)
	harden(cmd)
	// ssh has its own askpass and helper machinery that can outlive it and
	// keep this stderr pipe open. The probe itself is already bounded by
	// openCeiling and gaveUp, so what this bounds is the Wait goroutine
	// below, which would otherwise leak one per timed-out probe.
	cmd.WaitDelay = waitDelay
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return view.Errorf("tunnel.open.failed", "could not start ssh: %v", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()

	waitCtx, cancel := context.WithTimeout(ctx, openCeiling)
	defer cancel()
	select {
	case <-exited:
		if cmd.ProcessState.ExitCode() == 0 {
			return nil
		}
		return sshFailed(name, spec, stderr.String())
	case <-waitCtx.Done():
		reap(cmd)
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
			tun.gaveUp.Store(true)
		}
		return view.Errorf("tunnel.open.timeout",
			"profile %q did not come up in time", name).
			WithHint(fmt.Sprintf("`ssh -W %s %s` by hand shows what it is waiting for — "+
				"a destination that never closes an idle connection can also park this probe",
				spec.dest, spec.host))
	}
}

// acceptSSH serves the listener until it closes: one ssh child per accepted
// connection, each gated so none can start once teardown has begun.
func (t *Tunnel) acceptSSH(ctx context.Context, spec sshSpec) {
	for {
		conn, err := t.ln.Accept()
		if err != nil {
			return
		}
		// The gate and the Add share t.mu with stopSSH's closing flag, which
		// is what keeps Add strictly before Wait — a WaitGroup where an Add
		// can land after Wait has begun on a zero counter is a documented
		// race, and Accept can return one last connection concurrently with
		// Close.
		t.mu.Lock()
		if t.closing {
			t.mu.Unlock()
			_ = conn.Close()
			continue
		}
		t.served.Add(1)
		t.mu.Unlock()
		go t.spliceSSH(ctx, spec, conn)
	}
}

// spliceSSH wires one accepted connection to one `ssh -W` child and waits it
// out. The connection is the child's stdin and stdout — os/exec runs the two
// copies — so rta never buffers or inspects the bytes.
func (t *Tunnel) spliceSSH(ctx context.Context, spec sshSpec, conn net.Conn) {
	defer t.served.Done()
	defer conn.Close()
	cmd := exec.CommandContext(ctx, sshBin, sshArgs(spec)...)
	// Pipes rather than `cmd.Stdin = conn; cmd.Stdout = conn`, and WaitDelay
	// is not an alternative here: when it fires it closes os/exec's own pipes
	// and then *blocks* on the copier, which is parked in conn.Read — closing
	// a pipe does not interrupt a read on a socket somebody else owns.
	// Assigning a net.Conn directly makes os/exec run both copies, so Wait
	// waited on a read from the caller's connection; an ssh child that died
	// first therefore left Wait blocked until the client closed its end, and
	// with it t.served.Done, t.abandon and the deferred conn.Close. That is a
	// goroutine, an fd and a map entry per connection, for the life of the
	// tunnel rather than only until teardown.
	//
	// With pipes, Wait returns when ssh exits. The conn->stdin copy stays
	// parked until the deferred conn.Close below fires, which is the point: a
	// child that died must not leave the caller holding a connection nothing
	// will ever answer.
	//
	// Checking t.closing, not just holding the lock, is why this is deferred
	// until here rather than done up front alongside StdinPipe/StdoutPipe.
	// Those calls stash a *child*-side fd apiece in cmd's own unexported
	// bookkeeping (parentIOPipes' counterpart) that only Start (or its
	// own failure-cleanup, when Start itself errors) ever closes — there is
	// no public way to release them otherwise. Creating the pipes before
	// knowing whether Start will ever run left exactly those fds leaked
	// every time this goroutine lost the race against a Close that set
	// t.closing first: two pipes, one child-side fd apiece, gone with
	// nothing left holding a reference to close them.
	//
	// Starting and registering are one critical section, and that is the
	// second half of the gate in acceptSSH. Held apart, teardown can snapshot
	// between them and reap a child whose Process is still nil — a no-op reap
	// racing Start's write, after which the process launches *behind* the
	// kill pass and survives teardown. Under the lock, a child in the map is
	// a started child, and once closing is set nothing starts at all — and
	// now nothing so much as opens a pipe, either.
	t.mu.Lock()
	if t.closing {
		t.mu.Unlock()
		return
	}
	in, ierr := cmd.StdinPipe()
	if ierr != nil {
		t.mu.Unlock()
		return
	}
	out, oerr := cmd.StdoutPipe()
	if oerr != nil {
		t.mu.Unlock()
		_ = in.Close()
		return
	}
	// Discarded rather than classified: the probe already turned this
	// target's failure modes into open-time errors, and a child failing
	// mid-call surfaces as the reset the plugin reports in its own words.
	cmd.Stderr = io.Discard
	harden(cmd)
	if err := cmd.Start(); err != nil {
		t.mu.Unlock()
		// Go's own cleanup handles this branch: os/exec closes both halves
		// of each pipe when Start fails partway through, in.Close/out.Close
		// included.
		return
	}
	// The connection is registered alongside because reaping the process is
	// not enough to end the splice: the conn->stdin copy below stays parked in
	// conn.Read past any reap while the caller holds its end open. Teardown
	// closes the connection to unblock it.
	t.children[cmd] = conn
	t.mu.Unlock()

	go func() {
		_, _ = io.Copy(in, conn)
		// Half-close: the destination sees EOF and, if it is well behaved,
		// closes back — the same clean shutdown probeSSH relies on.
		_ = in.Close()
	}()
	_, _ = io.Copy(conn, out)
	_ = cmd.Wait()
	t.abandon(cmd)
}

func (t *Tunnel) abandon(cmd *exec.Cmd) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.children, cmd)
}

// stopSSH tears an ssh tunnel down: no new children, no new connections, then
// every live child reaped and waited for, bounded the way the kube teardown
// is.
func (t *Tunnel) stopSSH() {
	t.mu.Lock()
	t.closing = true
	kids := make(map[*exec.Cmd]net.Conn, len(t.children))
	for c, conn := range t.children {
		kids[c] = conn
	}
	t.mu.Unlock()
	_ = t.ln.Close()
	for c, conn := range kids {
		reap(c)
		// The read side of the splice, not only the process — see adopt.
		_ = conn.Close()
	}
	done := make(chan struct{})
	go func() { t.served.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.gaveUp.Store(true)
	}
}

// sshConfigPath is overridable in tests; the default is the file ssh itself
// reads first.
var sshConfigPath = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

// SSHHosts lists the Host aliases in the operator's own ssh config, sorted —
// completion candidates for the head of an `ssh:` target, because an alias
// is the exact case this feature is best at: one word that carries the user,
// port, key and ProxyJump the config already states.
//
// A local file read, so it may run per keystroke (the boundary
// is somebody's infrastructure, not the operator's own disk). Patterns
// (*, ?, !) are skipped — a pattern matches hosts, it does not name one — and
// Include directives are not followed: the common case is one file, and a
// missing alias costs a completion, not a connection.
func SSHHosts() []string {
	path, err := sshConfigPath()
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var hosts []string
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "Host") {
			continue
		}
		for _, h := range fields[1:] {
			if strings.ContainsAny(h, "*?!") || seen[h] {
				continue
			}
			seen[h] = true
			hosts = append(hosts, h)
		}
	}
	sort.Strings(hosts)
	return hosts
}

// sshFailed turns ssh's stderr into something actionable — kubectlFailed's
// job for the other scheme, against messages verified rather than recalled.
func sshFailed(name string, spec sshSpec, stderr string) *view.Error {
	s := strings.TrimSpace(stderr)
	one := s
	if i := strings.IndexByte(one, '\n'); i >= 0 {
		one = one[:i]
	}
	switch {
	case strings.Contains(s, "Permission denied"):
		return view.Errorf("tunnel.auth.denied", "%s: %s refused the key", name, spec.host).
			WithHint("BatchMode is on, so nothing here can prompt — `ssh " + spec.host +
				"` by hand shows what it wants (an agent, a passphrase, a different key)")
	case strings.Contains(s, "Host key verification failed") ||
		strings.Contains(s, "REMOTE HOST IDENTIFICATION HAS CHANGED"):
		return view.Errorf("tunnel.hostkey.unknown", "%s: the host key for %s is not trusted", name, spec.host).
			WithHint("rta never answers a host-key prompt for you — connect once by hand " +
				"with `ssh " + spec.host + "` so it lands in known_hosts")
	case strings.Contains(s, "Could not resolve hostname"):
		return view.Errorf("tunnel.host.unknown", "%s: %s", name, one).
			WithHint("check the host in the `ssh:` target — an ~/.ssh/config alias works here too")
	case strings.HasPrefix(s, "ssh: connect to host"):
		return view.Errorf("tunnel.bastion.unreachable", "%s: %s", name, one).
			WithHint("the ssh host itself is not answering — the destination was never reached")
	case strings.Contains(s, "open failed") || strings.Contains(s, "stdio forwarding failed"):
		return view.Errorf("tunnel.dest.unreachable",
			"%s: %s cannot reach %s", name, spec.host, spec.dest).
			WithHint("the login worked and the far end refused — check the destination in the " +
				"`ssh:` target, or from the host: `nc -vz " + strings.Replace(spec.dest, ":", " ", 1) + "`")
	case s == "":
		return view.Errorf("tunnel.open.failed", "profile %q: ssh exited without forwarding", name).
			WithHint("`ssh -W " + spec.dest + " " + spec.host + "` by hand shows why")
	default:
		return view.Errorf("tunnel.open.failed", "%s: %s", name, one).
			WithHint("that message is ssh's; rta shells out to it so your ssh config keeps working")
	}
}
