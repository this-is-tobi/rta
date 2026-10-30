package pluginhost

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// flagged is the paths policy gives after flag.
func flagged(policy []string, flag string) []string {
	var out []string
	for i := 0; i+1 < len(policy); i += 2 {
		if policy[i] == flag {
			out = append(out, policy[i+1])
		}
	}
	return out
}

// A strict plugin reaches the system, TMPDIR, its own binary, the tools on its
// PATH and what it was granted — and of the home directory nothing else: not
// a PATH entry inside it, not the directory its binary sits in.
func TestTheStrictPolicyAllowsWhatAPluginNeedsAndNothingElseOfHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	kube := filepath.Join(home, ".kube")
	bin := filepath.Join(home, "bin")
	for _, dir := range []string{kube, bin} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	plugin := filepath.Join(bin, "rta-plugin-demo")
	if err := os.WriteFile(plugin, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:"+home+":relative")

	policy := strictPolicy(DenySet{Strict: true, Readable: []string{kube}}, plugin)
	rx, ro, rw := flagged(policy, "--rx"), flagged(policy, "--ro"), flagged(policy, "--rw")
	for _, want := range []struct {
		in   []string
		path string
	}{{rx, "/usr"}, {rx, plugin}, {rx, "/usr/bin"}, {ro, "/proc"}, {ro, kube}, {rw, "/dev/null"}} {
		if !slices.Contains(want.in, want.path) {
			t.Errorf("the policy does not allow %s: %q", want.path, policy)
		}
	}
	for _, p := range append(append(rx, ro...), rw...) {
		if p == home || p == bin || p == "relative" || p == os.TempDir() {
			t.Errorf("the policy allows %s, which hands the plugin the home directory or everybody's temporary one: %q", p, policy)
		}
	}
}

// The temporary directory a strict plugin gets is the one made for its launch,
// on its policy and as its TMPDIR, in place of the one it inherited.
func TestAStrictLaunchGetsATemporaryDirectoryOfItsOwn(t *testing.T) {
	cmd := exec.Command("/opt/rta/rta", ConfineCommand, "--rx", "/usr", "--", "/usr/bin/true")
	cmd.Env = []string{"PATH=/usr/bin", "TMPDIR=/tmp"}
	confineLaunch(cmd, DenySet{Strict: true}, "/tmp/rta-sock-1")
	if want := []string{"/opt/rta/rta", ConfineCommand, "--rw", "/tmp/rta-sock-1", "--rx", "/usr", "--", "/usr/bin/true"}; !slices.Equal(cmd.Args, want) {
		t.Errorf("args = %q, want %q", cmd.Args, want)
	}
	if want := []string{"PATH=/usr/bin", "TMPDIR=/tmp/rta-sock-1"}; !slices.Equal(cmd.Env, want) {
		t.Errorf("env = %q, want %q", cmd.Env, want)
	}
	plain := exec.Command("/usr/bin/true")
	confineLaunch(plain, DenySet{}, "/tmp/rta-sock-2")
	if len(plain.Args) != 1 {
		t.Errorf("an unconfined launch was given a policy: %q", plain.Args)
	}
}

// Strict starts the plugin through rta, with the policy before the command;
// anything else runs the command as it is.
func TestAStrictLaunchGoesThroughTheShim(t *testing.T) {
	name, argv := wrap(DenySet{Strict: true, Shim: "/opt/rta/rta"}, "/usr/bin/true", []string{"serve"})
	if name != "/opt/rta/rta" || argv[0] != ConfineCommand {
		t.Fatalf("a strict launch runs %s %q, want the shim", name, argv)
	}
	if i := slices.Index(argv, "--"); i < 0 || !slices.Equal(argv[i+1:], []string{"/usr/bin/true", "serve"}) {
		t.Errorf("the command after the policy is %q", argv)
	}
	if name, argv := wrap(DenySet{}, "/usr/bin/true", []string{"serve"}); name != "/usr/bin/true" || !slices.Equal(argv, []string{"serve"}) {
		t.Errorf("an unconfined launch runs %s %q", name, argv)
	}
}

// What strict asks of the configuration is held to: asked, a launch is ready
// with the shim on a kernel that has Landlock and refused on one that has not.
func TestStrictInTheConfigurationIsWhatTheLaunchGets(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	d, err := Resolve()
	if err != nil || d.Strict {
		t.Fatalf("with nothing asked, Resolve = strict %v, %v", d.Strict, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("confine: strict\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err = Resolve()
	switch {
	case landlockABI() < 1:
		if !errors.Is(err, errNoLandlock) {
			t.Errorf("strict on a kernel without Landlock = %v, want the refusal", err)
		}
	case err != nil || !d.Strict || d.Shim == "":
		t.Errorf("strict with Landlock = strict %v, shim %q, %v", d.Strict, d.Shim, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("confine: [strict\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(); err == nil {
		t.Error("a configuration that does not load let a launch through without saying whether it is strict")
	}
}

// The shim confines for real: under it, a shell reads and writes what the
// policy names, and is refused a file beside them it does not name — the
// same file it reads unconfined.
func TestTheShimConfinesWhatItStarts(t *testing.T) {
	if landlockABI() < 1 {
		t.Skip("this kernel has no Landlock")
	}
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	allowed := t.TempDir()
	script := "cat " + secret + "; echo written > " + allowed + "/f && cat " + allowed + "/f"
	args := []string{ConfineCommand}
	for _, p := range systemExec {
		args = append(args, "--rx", p)
	}
	args = append(args, "--rw", "/dev/null", "--rw", allowed, "--", "/bin/sh", "-c", script)
	out, _ := exec.Command(os.Args[0], args...).CombinedOutput() //nolint:gosec // the test binary, as the shim
	if strings.Contains(string(out), "hunter2") || !strings.Contains(string(out), "Permission denied") {
		t.Errorf("the confined shell read a file the policy does not name:\n%s", out)
	}
	if !strings.Contains(string(out), "written") {
		t.Errorf("the confined shell could not write where the policy allows:\n%s", out)
	}
	plain, err := exec.Command("/bin/sh", "-c", "cat "+secret).CombinedOutput()
	if err != nil || !strings.Contains(string(plain), "hunter2") {
		t.Fatalf("the file is not readable unconfined either, so the test proves nothing: %v %s", err, plain)
	}
}

// tree makes each of files under root, a name ending in / as a directory and
// one holding " -> " as a link to what follows it, and returns root.
func tree(t *testing.T, files ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		name, target, link := strings.Cut(f, " -> ")
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		var err error
		switch {
		case link:
			err = os.Symlink(target, p)
		case strings.HasSuffix(name, "/"):
			err = os.MkdirAll(p, 0o700)
		default:
			err = os.WriteFile(p, []byte(name), 0o600)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// A directory strict allows is allowed whole only when nothing it is to leave
// out lies inside it. Otherwise each entry is allowed in its place, as deep
// as the path down to what is left out, and that and a link leading to it are
// not; the one link that leads elsewhere is allowed as it stands.
func TestCarvingAllowsADirectoryWithoutWhatLiesInsideIt(t *testing.T) {
	root := tree(t, "hostname", "ssl/ca.pem", "rta/README", "rta/config/config.yaml", "rta/tokens/tokens",
		"in -> rta/tokens/tokens", "out -> ssl")
	off := []string{filepath.Join(root, "rta/config"), filepath.Join(root, "rta/tokens/tokens")}
	want := []string{"hostname", "out", "rta/README", "ssl"}
	for i, w := range want {
		want[i] = filepath.Join(root, w)
	}
	if got := carve(root, off); !slices.Equal(got, want) {
		t.Errorf("carve = %q, want %q", got, want)
	}
	if got := carve(root, nil); !slices.Equal(got, []string{root}) {
		t.Errorf("with nothing to leave out, carve = %q, want the directory whole", got)
	}
	for _, gone := range []string{filepath.Join(root, "rta/config"), filepath.Join(root, "rta/config/config.yaml"),
		filepath.Join(root, "missing")} {
		if got := carve(gone, off); got != nil {
			t.Errorf("carve(%s) = %q, want nothing: it is left out, inside what is, or not there", gone, got)
		}
	}
}

// A Kubernetes secret is mounted as a link to a file under `..data`, itself a
// link to a directory named by its timestamp. Every way into the file is left
// out: the link a server is started with, the link to the directory, and the
// directory itself.
func TestCarvingLeavesOutASecretMountedTheKubernetesWay(t *testing.T) {
	root := tree(t, "tokens/..2026_10_31_14_00_00.1/tokens", "tokens/..data -> ..2026_10_31_14_00_00.1",
		"tokens/tokens -> ..data/tokens", "hostname")
	named := filepath.Join(root, "tokens/tokens")
	if got := carve(root, withTarget(named)); !slices.Equal(got, []string{filepath.Join(root, "hostname")}) {
		t.Errorf("carve = %q, want only the hostname: nothing of the mounted secret", got)
	}
}

// The home directory is left out of the system's directories, where a
// service account's home often is, and rta's own state out of a location a
// plugin was granted, where the operator may keep the store's key.
func TestTheStrictPolicyCarvesRtasOwnAndTheHomeDirectoryOutOfWhatItAllows(t *testing.T) {
	granted := tree(t, "id_ed25519", "known_hosts", "rta.key")
	key := filepath.Join(granted, "rta.key")
	t.Setenv("HOME", t.TempDir())
	policy := strictPolicy(DenySet{Strict: true, NoAccess: []string{key}, Readable: []string{granted}}, "/bin/sh")
	ro := flagged(policy, "--ro")
	for _, want := range []string{filepath.Join(granted, "id_ed25519"), filepath.Join(granted, "known_hosts")} {
		if !slices.Contains(ro, want) {
			t.Errorf("the policy does not allow %s beside the key: %q", want, ro)
		}
	}
	if slices.Contains(ro, granted) || slices.Contains(ro, key) {
		t.Errorf("the policy allows the key, or the directory holding it whole: %q", ro)
	}
	opt := tree(t, "service/.ssh/id_ed25519", "share/zoneinfo")
	if got := carve(opt, withTarget(filepath.Join(opt, "service"))); !slices.Equal(got,
		[]string{filepath.Join(opt, "share")}) {
		t.Errorf("a system directory holding the home directory carves to %q, want everything but it", got)
	}
}

// The shim holds a carved location for real: a shell started under the policy
// reads beside the store's key in the location it was granted, and is refused
// the key.
func TestTheShimRefusesRtasOwnInsideAGrantedLocation(t *testing.T) {
	if landlockABI() < 1 {
		t.Skip("this kernel has no Landlock")
	}
	granted := tree(t, "known_hosts", "rta.key")
	key := filepath.Join(granted, "rta.key")
	if err := os.WriteFile(key, []byte("hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	policy := strictPolicy(DenySet{Strict: true, NoAccess: []string{key}, Readable: []string{granted}}, "/bin/sh")
	script := "cat " + filepath.Join(granted, "known_hosts") + "; cat " + key
	args := append(append([]string{ConfineCommand}, policy...), "--", "/bin/sh", "-c", script)
	out, _ := exec.Command(os.Args[0], args...).CombinedOutput() //nolint:gosec // the test binary, as the shim
	if !strings.Contains(string(out), "known_hosts") {
		t.Errorf("the confined shell could not read beside the key:\n%s", out)
	}
	if strings.Contains(string(out), "hunter2") || !strings.Contains(string(out), "Permission denied") {
		t.Errorf("the confined shell read the store's key in a granted location:\n%s", out)
	}
}
