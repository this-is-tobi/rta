package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeSSH installs a script that stands in for `ssh` — the same argument as
// fakeKubectl: the lifecycle under test is rta's, not OpenSSH's. The one test
// that is about OpenSSH — does the real argv authenticate, splice and tear
// down against a real sshd — is TestSSHAgainstARealSSHD below.
func fakeSSH(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ssh")
	script := "#!/bin/sh\n" + body
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := sshBin
	sshBin = path
	t.Cleanup(func() { sshBin = saved })
}

const bastion = "tobi@bastion.example:2222/vault.internal:8200"

// `exec cat` is a complete fake for both phases at once. The probe runs with
// /dev/null as stdin, so cat sees EOF and exits 0 — the success signal. A
// spliced connection runs with the socket as stdin and stdout, so cat makes
// the tunnel an echo — and an endpoint that echoes proves bytes crossed rta's
// listener into the child and back, which dialling alone would not.
func TestSSHOpenServesAnEndpointThatSplices(t *testing.T) {
	fakeSSH(t, "exec cat\n")

	tun, verr := Open(context.Background(), "bastion-vault", Target{SSH: bastion})
	if verr != nil {
		t.Fatalf("open: %v", verr)
	}
	defer tun.Close()
	if tun.Host != "127.0.0.1" || tun.Port == 0 {
		t.Fatalf("endpoint = %s:%d, want 127.0.0.1 and a real port", tun.Host, tun.Port)
	}

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(tun.Host, strconv.Itoa(tun.Port)), 2*time.Second)
	if err != nil {
		t.Fatalf("the endpoint rta handed back does not answer: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 5)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping\n" {
		t.Fatalf("read back %q, %v — the splice did not carry the bytes", buf, err)
	}
}

// A tunnel that outlives its call is the hole this exists to avoid, and
// for ssh there are two things to end: the listener and every child serving a
// connection. Close must end both — a closed listener with a live child is a
// process nobody can find still holding a path to the far side.
func TestSSHCloseEndsTheListenerAndTheChildren(t *testing.T) {
	// First invocation is the probe: exit 0. Every later one is a splice
	// child that ignores nothing and never exits on its own, so only reap can
	// end it.
	state := filepath.Join(t.TempDir(), "probed")
	fakeSSH(t, fmt.Sprintf(
		"if [ ! -f %s ]; then touch %s; exit 0; fi\nwhile true; do sleep 1; done\n",
		state, state))

	tun, verr := Open(context.Background(), "bastion-vault", Target{SSH: bastion})
	if verr != nil {
		t.Fatalf("open: %v", verr)
	}
	addr := net.JoinHostPort(tun.Host, strconv.Itoa(tun.Port))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	// The child only exists once the splice has picked the connection up;
	// close before that and there is nothing to prove reaped.
	deadline := time.Now().Add(2 * time.Second)
	for {
		tun.mu.Lock()
		n := len(tun.children)
		tun.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no splice child appeared for the accepted connection")
		}
		time.Sleep(10 * time.Millisecond)
	}

	tun.Close()
	if tun.TimedOut() {
		t.Fatal("teardown fell through to its timeout — a child survived reap")
	}
	if _, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
		t.Fatal("the endpoint still answers after Close")
	}
}

// spliceSSH creates its pipes (cmd.StdinPipe/StdoutPipe — real os.Pipe()
// pairs) before it ever checks t.closing under the lock. Racing acceptSSH's
// goroutine spawn against a Close is what produces this in production; here
// closing is simply set before spliceSSH is ever called, which hits the
// exact same early return deterministically, many times over, to turn a
// couple of leaked fds per call into a difference /dev/fd can actually see.
func TestSpliceSSHClosesItsPipesOnTheClosingRace(t *testing.T) {
	if _, err := os.Stat("/dev/fd"); err != nil {
		t.Skip("no /dev/fd on this platform")
	}
	fakeSSH(t, "exec cat\n")
	openFDs := func() int {
		t.Helper()
		entries, err := os.ReadDir("/dev/fd")
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}

	tun := &Tunnel{closing: true}
	spec := sshSpec{host: "bastion", dest: "vault.internal:8200"}

	before := openFDs()
	const n = 50
	for i := 0; i < n; i++ {
		client, server := net.Pipe()
		// acceptSSH's own half of the contract spliceSSH relies on: Add
		// before the goroutine spawn, Done as spliceSSH's first deferred
		// call — see ssh.go:247/257.
		tun.served.Add(1)
		tun.spliceSSH(context.Background(), spec, server)
		_ = client.Close()
		_ = server.Close()
	}
	after := openFDs()

	if after > before+5 {
		t.Fatalf("open fds went from %d to %d after %d calls that each hit the closing race — "+
			"the pipes spliceSSH creates before checking t.closing are not being closed", before, after, n)
	}
}

func TestSSHCloseIsIdempotent(t *testing.T) {
	fakeSSH(t, "exec cat\n")
	tun, verr := Open(context.Background(), "bastion-vault", Target{SSH: bastion})
	if verr != nil {
		t.Fatalf("open: %v", verr)
	}
	tun.Close()
	tun.Close()
}

// Each stderr here was captured from a real OpenSSH 10.2 against a real sshd
// — auth denied, unknown host key, unresolvable name, bastion refusing,
// destination refusing through a live bastion — not recalled. The
// classification is the reason shelling out is tolerable, so it is pinned
// per class.
func TestEverySSHFailureIsClassified(t *testing.T) {
	cases := []struct {
		stderr string
		code   string
	}{
		{"tobi@bastion.example: Permission denied (publickey).", "tunnel.auth.denied"},
		{"No ED25519 host key is known for bastion.example and you have requested strict checking.\nHost key verification failed.", "tunnel.hostkey.unknown"},
		{"ssh: Could not resolve hostname bastion.example: nodename nor servname provided, or not known", "tunnel.host.unknown"},
		{"ssh: connect to host bastion.example port 2222: Connection refused", "tunnel.bastion.unreachable"},
		{"channel 0: open failed: connect failed: Connection refused\nstdio forwarding failed", "tunnel.dest.unreachable"},
		{"", "tunnel.open.failed"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			if tc.stderr == "" {
				fakeSSH(t, "exit 255\n")
			} else {
				fakeSSH(t, fmt.Sprintf("printf '%%s\\n' %q >&2\nexit 255\n", tc.stderr))
			}
			_, verr := Open(context.Background(), "bastion-vault", Target{SSH: bastion})
			if verr == nil || verr.Code != tc.code {
				t.Fatalf("verr = %v, want code %s", verr, tc.code)
			}
		})
	}
}

func TestMalformedSSHTargetsAreRefusedWithTheForm(t *testing.T) {
	cases := []struct{ name, spec string }{
		{"no destination", "bastion.example"},
		{"empty destination", "bastion.example/"},
		{"destination without port", "bastion.example/vault.internal"},
		{"destination port out of range", "bastion.example/vault.internal:99999"},
		{"empty user", "@bastion.example/vault.internal:8200"},
		{"empty host", "tobi@/vault.internal:8200"},
		{"ssh port out of range", "bastion.example:0/vault.internal:8200"},
		{"unbracketed ipv6 head", "::1:22/vault.internal:8200"},
		// The injection shapes: every part of the target lands in ssh's argv,
		// and an option is one dash away from a ProxyCommand.
		{"option as host", "-oProxyCommand=evil/vault.internal:8200"},
		{"option as user", "-oProxyCommand=evil@bastion.example/vault.internal:8200"},
		{"option as destination", "bastion.example/-evil:8200"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if verr := CheckSSH(tc.spec); verr == nil || verr.Code != "tunnel.target.malformed" {
				t.Fatalf("CheckSSH(%q) = %v, want tunnel.target.malformed", tc.spec, verr)
			}
			_, verr := Open(context.Background(), "bastion-vault", Target{SSH: tc.spec})
			if verr == nil || verr.Code != "tunnel.target.malformed" {
				t.Fatalf("Open(%q) = %v, want tunnel.target.malformed", tc.spec, verr)
			}
		})
	}
	for _, ok := range []string{
		"bastion.example/vault.internal:8200",
		"tobi@bastion.example:2222/vault.internal:8200",
		"tobi@[::1]:2222/[::2]:8200",
		"bastion/db.internal:5432",
	} {
		if verr := CheckSSH(ok); verr != nil {
			t.Fatalf("CheckSSH(%q) = %v, want accepted", ok, verr)
		}
	}
}

// One target stating both schemes is refused, not resolved by preference —
// config validation catches it earlier, and this package must not pick one of
// two stated reaches on its own either.
func TestATargetStatingTwoTunnelsIsRefused(t *testing.T) {
	_, verr := Open(context.Background(), "greedy", Target{Kube: homelab, SSH: bastion})
	if verr == nil || verr.Code != "tunnel.target.twice" {
		t.Fatalf("verr = %v, want tunnel.target.twice", verr)
	}
}

func TestAMissingSSHSaysWhatToDo(t *testing.T) {
	saved := sshBin
	sshBin = "ssh-that-is-not-installed-anywhere"
	t.Cleanup(func() { sshBin = saved })
	_, verr := Open(context.Background(), "bastion-vault", Target{SSH: bastion})
	if verr == nil || verr.Code != "tunnel.ssh.missing" {
		t.Fatalf("verr = %v, want tunnel.ssh.missing", verr)
	}
	if !strings.Contains(verr.Hint, "config") {
		t.Fatalf("the hint should say why rta shells out; got %q", verr.Hint)
	}
}

// A probe that hangs — a destination holding its half of the connection open,
// an ssh stuck behind an agent prompt BatchMode failed to suppress — must not
// hang the call. Same ceiling, same reasoning as the kube twin above.
func TestASSHProbeThatHangsTimesOut(t *testing.T) {
	fakeSSH(t, "while true; do sleep 1; done\n")
	saved := openCeiling
	openCeiling = 200 * time.Millisecond
	t.Cleanup(func() { openCeiling = saved })

	start := time.Now()
	_, verr := Open(context.Background(), "bastion-vault", Target{SSH: bastion})
	if verr == nil || verr.Code != "tunnel.open.timeout" {
		t.Fatalf("verr = %v, want tunnel.open.timeout", verr)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %v to give up", time.Since(start))
	}
}

// The one test about OpenSSH rather than about rta: the exact argv sshArgs
// builds, against a real sshd, end to end — keys generated fresh, trust
// established the way the feature promises (the operator's own ssh config),
// bytes read back through the forward. Skipped where OpenSSH's daemon is not
// installed; environmental failures to boot it skip too, because they are
// facts about the machine, not about rta.
func TestSSHAgainstARealSSHD(t *testing.T) {
	sshd := findSSHD(t)
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("no ssh-keygen on $PATH")
	}
	me, err := user.Current()
	if err != nil || me.Username == "root" {
		t.Skip("needs an ordinary user to authenticate as")
	}

	dir := t.TempDir()
	keygen := func(name string) string {
		path := filepath.Join(dir, name)
		out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-f", path, "-N", "").CombinedOutput()
		if err != nil {
			t.Skipf("ssh-keygen: %v: %s", err, out)
		}
		return path
	}
	hostKey := keygen("host")
	clientKey := keygen("client")
	pub, err := os.ReadFile(clientKey + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	authorized := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(authorized, pub, 0o600); err != nil {
		t.Fatal(err)
	}

	port, cleanup := startSSHD(t, sshd, dir, hostKey, authorized)
	defer cleanup()

	// The trust store and identity travel the road the feature promises: an
	// ssh client config, nothing injected into rta's argv. It cannot be the
	// literal ~/.ssh/config — OpenSSH expands ~ from the passwd database, not
	// $HOME, so a test cannot point it anywhere without touching the real one
	// — so the sshBin seam carries a wrapper that execs the real ssh with
	// `-F <here>` and rta's own argv verbatim. The config supplies exactly
	// what a user's would: the user, the key, the trust store.
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("no ssh on $PATH")
	}
	config := fmt.Sprintf("Host 127.0.0.1\n  User %s\n  IdentityFile %s\n"+
		"  IdentitiesOnly yes\n  UserKnownHostsFile %s\n  StrictHostKeyChecking accept-new\n",
		me.Username, clientKey, filepath.Join(dir, "known_hosts"))
	cfgPath := filepath.Join(dir, "ssh_config")
	if err := os.WriteFile(cfgPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeSSH(t, fmt.Sprintf("exec %s -F %s \"$@\"\n", sshPath, cfgPath))

	// The destination is the sshd itself — the one listener the test already
	// knows is up — and its banner is the proof bytes crossed: sshd speaks
	// first, so a read succeeding through the endpoint is a full round trip
	// client → rta listener → ssh -W → sshd.
	tgt := Target{SSH: fmt.Sprintf("%s@127.0.0.1:%d/127.0.0.1:%d", me.Username, port, port)}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tun, verr := Open(ctx, "real-sshd", tgt)
	if verr != nil {
		t.Fatalf("open against a live sshd: %v (hint: %s)", verr, verr.Hint)
	}
	defer tun.Close()

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(tun.Host, strconv.Itoa(tun.Port)), 5*time.Second)
	if err != nil {
		t.Fatalf("dial through the forward: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	banner := make([]byte, 4)
	if _, err := io.ReadFull(conn, banner); err != nil || string(banner) != "SSH-" {
		t.Fatalf("read %q, %v through the forward, want the sshd banner", banner, err)
	}
	tun.Close()
	if tun.TimedOut() {
		t.Fatal("teardown fell through to its timeout")
	}
}

// findSSHD locates OpenSSH's daemon, which lives in sbin and is therefore
// rarely on $PATH.
func findSSHD(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("sshd"); err == nil {
		return p
	}
	for _, p := range []string{"/usr/sbin/sshd", "/usr/local/sbin/sshd", "/opt/homebrew/sbin/sshd"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("no sshd installed")
	return ""
}

// startSSHD boots a throwaway sshd on a loopback port and waits until it
// answers. The port comes from bind-then-release, which is a race — accepted
// in a test with retries, rejected in the resolver itself for the reason
// tunnel.go's `forwarding` comment records.
func startSSHD(t *testing.T, sshd, dir, hostKey, authorized string) (int, func()) {
	t.Helper()
	logPath := filepath.Join(dir, "sshd.log")
	for attempt := 0; attempt < 3; attempt++ {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()

		cfg := filepath.Join(dir, "sshd_config")
		conf := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\n"+
			"AuthorizedKeysFile %s\nPidFile %s\nAuthenticationMethods publickey\n"+
			"PasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\n"+
			"StrictModes no\nAllowTcpForwarding yes\nLogLevel QUIET\n",
			port, hostKey, authorized, filepath.Join(dir, "sshd.pid"))
		if err := os.WriteFile(cfg, []byte(conf), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(sshd, "-f", cfg, "-D", "-E", logPath)
		if err := cmd.Start(); err != nil {
			t.Skipf("sshd would not start: %v", err)
		}
		stop := func() {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
			if err == nil {
				_ = c.Close()
				return port, stop
			}
			time.Sleep(50 * time.Millisecond)
		}
		stop()
	}
	log, _ := os.ReadFile(logPath)
	t.Skipf("sshd never came up on a loopback port; its log: %s", log)
	return 0, nil
}

// The completion candidates for an ssh target's head: real aliases only —
// a pattern matches hosts rather than naming one — deduped and sorted, and a
// missing config is no candidates rather than an error, because completion is
// an assist.
func TestSSHHostsListsAliasesAndSkipsPatterns(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config")
	body := "Host bastion prod-bastion\n  User tobi\n\nhost lab\nHost *.internal\nHost web-?\nHost !jump\nHost bastion\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := sshConfigPath
	sshConfigPath = func() (string, error) { return cfg, nil }
	t.Cleanup(func() { sshConfigPath = saved })

	got := SSHHosts()
	want := []string{"bastion", "lab", "prod-bastion"}
	if len(got) != len(want) {
		t.Fatalf("hosts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hosts = %v, want %v", got, want)
		}
	}

	sshConfigPath = func() (string, error) { return filepath.Join(t.TempDir(), "absent"), nil }
	if hosts := SSHHosts(); hosts != nil {
		t.Fatalf("a missing config yielded %v, want none", hosts)
	}
}
