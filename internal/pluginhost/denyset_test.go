package pluginhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// The injection the validator exists for. SBPL is last-match-wins, so a path
// that closes the deny form and opens an allow form does not merely bypass
// its own rule — it overrides every tier above it, while rta goes on
// reporting the sandbox as enabled. That is strictly worse than no sandbox,
// because somebody would build a policy on the report.
func TestAPathThatCouldCloseAPolicyFormIsRefused(t *testing.T) {
	for _, bad := range []string{
		`/tmp/x")) (allow file-read* (subpath "/`,
		`/tmp/quote"here`,
		"/tmp/paren(here",
		"/tmp/paren)here",
		"/tmp/new\nline",
		"/tmp/carriage\rreturn",
	} {
		if err := validate([]string{bad}); err == nil {
			t.Errorf("%q was accepted into a policy", bad)
		}
	}
	// And the ordinary case still passes, including the spaces and unicode
	// real home directories have in them.
	for _, ok := range []string{
		"/Users/someone/.ssh",
		"/Users/Ada Lovelace/Library/Application Support/rta",
		"/home/user/.local/share/rta",
		"/Users/tobi/Développement/rta",
	} {
		if err := validate([]string{ok}); err != nil {
			t.Errorf("%q was refused: %v", ok, err)
		}
	}
}

// The separator is the one character whose answer is platform knowledge, and
// both answers are checked from whatever machine runs this — the property the
// runtime.GOOS branch exists for, since a build tag would leave one of them
// untested everywhere except the platform it is for.
//
// **This was total on Windows.** Every path there contains a backslash, so a
// machine-derived deny set had one in its first entry, ResolveAllowing
// returned the refusal, and Host.OpenAllowing passed it back to its caller:
// no plugin could launch at all. The policy it was protecting is never
// rendered off darwin — profile() returns "" and wrap() is the identity — so
// the check was failing closed over a file that does not exist.
func TestTheSeparatorIsRefusedOnlyWhereItIsAnAnomaly(t *testing.T) {
	if strings.ContainsRune(forbiddenIn("windows"), '\\') {
		t.Error("a backslash is refused on Windows, where it is the path separator — " +
			"no plugin could ever launch")
	}
	if !strings.ContainsRune(forbiddenIn("darwin"), '\\') {
		t.Error("a backslash reaches a rendered SBPL policy on darwin")
	}
	if !strings.ContainsRune(forbiddenIn("linux"), '\\') {
		t.Error("a backslash is accepted on linux, so the darwin renderer can still meet one")
	}
	// The characters that are anomalies anywhere stay refused everywhere,
	// which is the half of the original rule that platform knowledge does
	// not touch.
	for _, goos := range []string{"windows", "darwin", "linux"} {
		for _, bad := range []string{`"`, "\n", "\r", "(", ")"} {
			if !strings.Contains(forbiddenIn(goos), bad) {
				t.Errorf("%q is accepted on %s", bad, goos)
			}
		}
	}
}

// A refusal has to stop the spawn, not drop the entry. A deny set that
// silently shrinks is a sandbox that reports itself enabled while protecting
// less than it says.
func TestABadPathFailsTheWholeSetRatherThanBeingSkipped(t *testing.T) {
	err := validate([]string{"/tmp/fine", `/tmp/bad"`, "/tmp/also-fine"})
	if err == nil {
		t.Fatal("a set containing one unquotable path was accepted")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("the error does not name the offending path: %v", err)
	}
}

// rta's own directories are the point of tier 1, and both of them have to be
// in the resolved set or the sandbox protects the wrong thing.
func TestRtasOwnDirectoriesAreDeniedBothVerbs(t *testing.T) {
	data := t.TempDir()
	t.Setenv("RTA_DATA_DIR", data)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "cfg", "config.yaml"))

	d, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(d.NoAccess, data) {
		t.Errorf("the data directory is not denied: %v", d.NoAccess)
	}
	// Read-only would leave the write half open, which is exactly the gap an
	// 82-byte overwrite of grants.json walked through.
	if containsPath(d.NoRead, data) && !containsPath(d.NoAccess, data) {
		t.Error("the data directory is denied reads only")
	}
}

// ~/.aws → ~/dotfiles/aws is what home-manager, chezmoi and stow all produce,
// so a deny set that names only the link is bypassed in two syscalls on
// exactly the machines most likely to be running this.
func TestASymlinkedDenyPathDeniesItsTargetToo(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real-secrets")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	got := withTarget(link)
	if !containsPath(got, link) {
		t.Errorf("the link itself is missing: %v", got)
	}
	if !containsPath(got, real) {
		t.Errorf("the target is missing, so the rule is bypassed by following the link: %v", got)
	}
}

// **The leaf is usually absent, and that used to switch symlink resolution
// off entirely.**
//
// filepath.EvalSymlinks fails on the whole path when any component is
// missing, and withTarget treated that failure exactly like "not a symlink".
// So on the layout it was written for — `.config` a symlink, which
// home-manager, chezmoi and stow all produce — a deny entry for a leaf that
// does not exist yet emitted only a spelling the kernel never produces. Not a
// narrower rule: an inert one, proven against sandbox-exec in
// confine_darwin_test.go. And the absent leaf is the normal case, not an
// edge: `~/.config/gcloud` with no gcloud installed, `~/.docker/config.json`
// before a docker login, rta's own data dir before rta has written anything.
func TestADenyPathBelowASymlinkResolvesEvenWhenItDoesNotExist(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "dotfiles", "config")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, ".config")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Deliberately never created — that is the whole point.
	absent := filepath.Join(link, "gcloud", "credentials.db")
	got := withTarget(absent)

	if !containsPath(got, absent) {
		t.Errorf("the link spelling is missing, so nothing pins the link: %v", got)
	}
	want := filepath.Join(real, "gcloud", "credentials.db")
	if !containsPath(got, want) {
		t.Errorf("the rule names nothing the kernel sees: got %v, want it to include %s", got, want)
	}
}

// The walk must not invent resolution where there is none. A plain missing
// path with no symlink anywhere above it has exactly one spelling, and
// emitting a second would be a rule for a path that is not the path.
func TestAMissingPathWithNoSymlinkAncestorResolvesToItself(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(base, "nothing", "here")
	if got := withTarget(absent); len(got) != 1 || got[0] != absent {
		t.Errorf("withTarget(%s) = %v, want just the path itself", absent, got)
	}
}

// The spec hash is a cache key, and a cache key that collides hands a call to
// a process launched under a different policy.
func TestTheSpecHashTracksThePolicy(t *testing.T) {
	a := DenySet{NoAccess: []string{"/one"}, NoRead: []string{"/two"}}
	if specHash(a) != specHash(a) {
		t.Error("the same policy hashed twice gave two answers")
	}
	if specHash(a) == specHash(DenySet{NoAccess: []string{"/one"}}) {
		t.Error("dropping a tier did not change the hash")
	}
	// The length prefixes exist for this: without them, moving a separator
	// into a path renders two different sets to the same bytes.
	x := DenySet{NoAccess: []string{"/a", "/b"}}
	y := DenySet{NoAccess: []string{"/a\n/b"}}
	if specHash(x) == specHash(y) {
		t.Error("two different deny sets collided")
	}
	// Swapping which tier a path is in is a real policy change: one denies
	// writes and the other does not.
	p := DenySet{NoAccess: []string{"/p"}}
	q := DenySet{NoRead: []string{"/p"}}
	if specHash(p) == specHash(q) {
		t.Error("moving a path between tiers did not change the hash")
	}
}

func containsPath(set []string, want string) bool {
	resolved, err := filepath.EvalSymlinks(want)
	if err != nil {
		resolved = want
	}
	for _, s := range set {
		if s == want || s == resolved {
			return true
		}
	}
	return false
}

// Runs on every platform, unlike the escape suite in confine_darwin_test.go,
// which is compiled out everywhere else — so `go test ./...` on Linux passes
// without running a single confinement assertion. That is the right structure
// (there is nothing to test where there is no sandbox) and it has one failure
// mode: somebody reads a green CI run on Linux as evidence the sandbox works.
//
// This pins the claim itself. Confined() is what `rta doctor` prints, and it
// must agree with what wrap() actually does — a platform that reports itself
// confined while running the command unchanged would be the worst of both.
func TestTheConfinementClaimMatchesTheBehaviour(t *testing.T) {
	d := DenySet{NoAccess: []string{"/deny-me"}}
	name, argv := wrap(d, "/bin/echo", []string{"hi"})

	if Confined() {
		if name == "/bin/echo" {
			t.Error("Confined() is true but wrap ran the command unchanged")
		}
		if len(argv) < 2 || argv[len(argv)-1] != "hi" {
			t.Errorf("the wrapped command lost its arguments: %v", argv)
		}
		if profile(d) == "" {
			t.Error("Confined() is true but the profile is empty")
		}
		return
	}

	// Unconfined platforms must not pretend. The command runs as given, and
	// doctor says so — see the per-platform reasons in confine_other.go.
	if name != "/bin/echo" || len(argv) != 1 || argv[0] != "hi" {
		t.Errorf("Confined() is false but wrap altered the command: %q %v", name, argv)
	}
	if profile(d) != "" {
		t.Error("Confined() is false but a profile was generated, which nothing applies")
	}
}

// Whatever the platform, the parts that are NOT confinement still apply, and
// they are the ones carrying the weight on Linux and Windows.
func TestTheNonConfinementHardeningIsPlatformIndependent(t *testing.T) {
	id := Identity{Path: "/bin/echo", Digest: "abc"}
	cmd := buildCmd(id, DenySet{}, nil)

	if len(cmd.Env) == 0 {
		t.Error("no environment was set, so the child would inherit nothing or everything")
	}
	for _, kv := range cmd.Env {
		if len(kv) > 4 && kv[:4] == "RTA_" {
			t.Errorf("an RTA_ variable reached the child: %q", kv)
		}
	}
	if cmd.SysProcAttr == nil {
		t.Error("no SysProcAttr, so the process group reaping does not apply")
	}
}

// **Every need names a location, and every denied location has a need.**
//
// Both directions are the point. A need with no path is a grant that grants
// nothing — the operator allows it, the plugin still cannot read the file, and
// nothing anywhere says why. A denied location with no need is the shape that
// broke plugins/kube and plugins/cnpg: ~/.kube was denied from this package's
// first commit, no plugin could ever be allowed it, and both plugins were
// unable to read a kubeconfig at all on macOS.
func TestEveryNeedNamesADeniedLocationAndBack(t *testing.T) {
	paths := tier2()
	for _, n := range plugin.Needs() {
		if paths[n] == "" {
			t.Errorf("need %q names no location, so allowing it would grant nothing", n)
		}
	}
	for n := range paths {
		if !plugin.KnownNeed(n) {
			t.Errorf("tier2 denies a location keyed by %q, which is not a need any plugin "+
				"can declare — nothing could ever be allowed it", n)
		}
	}
	if len(paths) != len(plugin.Needs()) {
		t.Errorf("%d locations against %d needs; the two sets have drifted",
			len(paths), len(plugin.Needs()))
	}
}

// A grant subtracts one location and leaves the rest of the policy alone.
func TestAGrantRemovesOnlyWhatItNames(t *testing.T) {
	base, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	kube := Tier2Path(plugin.NeedKubeconfig)
	if !containsPath(base.NoRead, kube) {
		t.Fatalf("the kubeconfig is not denied by default, so there is nothing to grant")
	}
	granted, err := ResolveAllowing([]plugin.Need{plugin.NeedKubeconfig})
	if err != nil {
		t.Fatal(err)
	}
	if containsPath(granted.NoRead, kube) {
		t.Error("the granted location is still denied")
	}
	if ssh := Tier2Path(plugin.NeedSSH); !containsPath(granted.NoRead, ssh) {
		t.Error("granting the kubeconfig also released ~/.ssh")
	}
	if len(granted.NoAccess) != len(base.NoAccess) {
		t.Errorf("a grant reached rta's own state: %d entries against %d",
			len(granted.NoAccess), len(base.NoAccess))
	}
}

// **A grant can only ever subtract.** There is no need naming anything in
// tier1, so no combination of grants reaches rta's own directories — the
// property that bounds the worst case of this whole mechanism.
func TestNoGrantCanReachRtasOwnState(t *testing.T) {
	everything, err := ResolveAllowing(plugin.Needs())
	if err != nil {
		t.Fatal(err)
	}
	base, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if len(everything.NoRead) != 0 {
		t.Errorf("allowing every need left %d locations denied", len(everything.NoRead))
	}
	for _, p := range base.NoAccess {
		if !containsPath(everything.NoAccess, p) {
			t.Errorf("allowing every need released %s, which is rta's own state", p)
		}
	}
}

// A stored grant this build does not recognise is dropped rather than obeyed.
// The store outlives the binary that wrote it, and denying is the safe way to
// be wrong: the plugin fails at the call that wanted the file and the operator
// is told what to grant.
func TestAnUnknownStoredGrantIsDropped(t *testing.T) {
	got := asNeeds([]string{"kubeconfig", "something-a-later-rta-invented"})
	if len(got) != 1 || got[0] != plugin.NeedKubeconfig {
		t.Errorf("asNeeds = %v, want only the known member", got)
	}
}

// **The deny set is byte-identical across calls, and the process cache
// depends on it.**
//
// specHash writes NoRead in slice order and the result is the cache key a
// running plugin is matched against. A set whose order moves between two
// calls hashes differently, the cache misses, and every call spawns a fresh
// plugin process — a launch every few seconds per plugin on a dashboard that
// refreshes on a timer.
//
// Not hypothetical. Keying tier2 by need turned it into a map, Go randomises
// map iteration, and this broke immediately: two Opens on the same binary
// produced two clients. TestTheSameBinaryIsNotLaunchedTwice caught it, which
// is the test that should — but it says "two clients", not "the deny set is
// unstable", so the reason lives here.
func TestTheDenySetIsStableAcrossCalls(t *testing.T) {
	first, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		again, err := Resolve()
		if err != nil {
			t.Fatal(err)
		}
		if specHash(first) != specHash(again) {
			t.Fatalf("call %d hashed differently:\n%v\n%v", i, first.NoRead, again.NoRead)
		}
	}
	// And with a grant, which is the path that iterates the keyed set.
	granted, err := ResolveAllowing([]plugin.Need{plugin.NeedKubeconfig})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		again, err := ResolveAllowing([]plugin.Need{plugin.NeedKubeconfig})
		if err != nil {
			t.Fatal(err)
		}
		if specHash(granted) != specHash(again) {
			t.Fatalf("granted call %d hashed differently:\n%v\n%v", i, granted.NoRead, again.NoRead)
		}
	}
	if specHash(first) == specHash(granted) {
		t.Error("a grant did not change the spec hash, so a granted plugin would " +
			"reuse the process of an ungranted one")
	}
}
