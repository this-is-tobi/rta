package pluginhost

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/this-is-tobi/rta/internal/config"
)

// Linux confines a plugin only when the operator asks, with `confine: strict`
// (config.Config.Confine), and then with Landlock, the kernel's unprivileged
// allow list.
//
// Not by default, and the reason is the model, not the mechanism. macOS
// confines every plugin with a deny list over `(allow default)` (denyset.go
// argues why a deny list there), and Landlock has no deny: it is an allow list,
// additive, fixed for the life of the process. So strict is the stricter
// model and not the same one — a plugin may reach the system, a temporary
// directory of its own, its own binary, the directories on its PATH outside the home directory and the
// credential locations `rta plugin access add` gave it, and nothing else of the
// home directory — and it is the
// operator's to turn on, on a server that runs plugins it does not otherwise
// watch, where tools are the system's.
//
// The mechanism is what once ruled Landlock out, and no longer does. The design
// before this one confined the rta process itself, through go-landlock's
// all-threads path, which returns ENOTSUP in a cgo binary — and `go install`
// builds one by default. A Landlock domain belongs to a thread and survives
// execve, so the plugin is started through rta itself (`rta __confine`,
// ConfineMain) on one locked thread that restricts itself and then becomes the
// plugin: no other thread is involved, cgo or not.

// confined reports whether this platform confines every plugin. Linux does so
// only under strict, which DenySet.Strict carries for one launch.
const confined = false

func profile(DenySet) string { return "" }

// wrap starts the plugin through the shim when the deny set asks for strict
// confinement, and runs the command unchanged otherwise. The policy is on the
// command line, as the macOS profile is: stdin belongs to the plugin protocol,
// and an environment variable would be inherited by everything the plugin
// spawns.
func wrap(d DenySet, name string, args []string) (string, []string) {
	if !d.Strict {
		return name, args
	}
	argv := append([]string{ConfineCommand}, strictPolicy(d, name)...)
	argv = append(argv, "--", name)
	return d.Shim, append(argv, args...)
}

func available() error { return nil }

// Confined reports whether this platform confines every plugin process: false
// on Linux, where only `confine: strict` does (Strict).
func Confined() bool { return confined }

// Strict reports whether plugins here are confined strictly, and with which
// Landlock ABI, for `rta doctor`; the error is why they would not start.
func Strict() (on bool, abi int, err error) {
	on, err = strictAsked()
	if !on || err != nil {
		return on, 0, err
	}
	abi = landlockABI()
	if abi < 1 {
		return true, 0, errNoLandlock
	}
	return true, abi, nil
}

// errNoLandlock is the refusal of a launch strict confinement cannot hold.
var errNoLandlock = errors.New("confine: strict needs Landlock (Linux 5.13 or later, with landlock among the " +
	"kernel's security modules), and this kernel has none — no plugin starts under it; remove `confine: strict` " +
	"to run plugins unconfined")

// strictAsked reads whether the operator asked for strict confinement. A
// configuration that does not load is refused rather than read as not asking:
// it may be the very file that asks.
func strictAsked() (bool, error) {
	cfg, err := config.Load()
	if err != nil {
		return false, fmt.Errorf("the configuration does not load, so whether plugins are to be confined "+
			"strictly is not known, and none starts: %w", err)
	}
	return cfg.Confine == "strict", nil
}

// strictReady fills in what a strict launch needs, or says why there is none:
// the kernel's Landlock, and rta's own executable to start the plugin through.
func strictReady(d *DenySet) error {
	on, err := strictAsked()
	if err != nil || !on {
		return err
	}
	if landlockABI() < 1 {
		return errNoLandlock
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("confine: strict starts each plugin through rta itself, and rta cannot find its own "+
			"executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	d.Strict, d.Shim = true, self
	return nil
}

// The fixed parts of the strict policy. Only those that exist are named, since
// a rule needs a file to open.
var (
	// systemExec is where a plugin's libraries, its CA bundle, its resolver
	// configuration and the tools it may run live.
	systemExec = []string{"/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32", "/etc", "/opt", "/nix/store"}
	// systemRead is what the Go runtime and most programs read about
	// themselves and the machine: /proc/self, and the cgroup limits that set
	// GOMAXPROCS.
	systemRead = []string{"/proc", "/sys", "/dev/urandom", "/dev/random"}
	// systemWrite is the devices a program writes to without asking.
	systemWrite = []string{"/dev/null", "/dev/zero", "/dev/full"}
)

// strictPolicy is the allow list for launching name under d, as the shim's
// flags: --rx for reading and executing, --ro for reading, --rw for both and
// writing. Everything not named is refused, rta's own data and configuration
// directories and the rest of the home directory among it.
//
// Each directory it allows is carved, since a directory allowed is all of it:
// the system's around rta's own state and the home directory, which a
// server's layout puts inside them (the chart keeps the token file and the
// roster under /etc/rta, and a service account's home is often under /opt),
// and a granted location around rta's own state alone, since it lies in the
// home directory by design (a kv key kept in a granted ~/.ssh).
func strictPolicy(d DenySet, name string) []string {
	own := d.ownFiles()
	offList := own
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		offList = append(slices.Clone(own), withTarget(home)...)
	}
	var out []string
	add := func(flag string, without []string, paths ...string) {
		for _, p := range paths {
			for _, allowed := range carve(p, without) {
				out = append(out, flag, allowed)
			}
		}
	}
	add("--rx", offList, systemExec...)
	// The binary itself and not its directory: a plugin run from the home
	// directory would otherwise be handed all of it. Not carved, since a
	// managed plugin's binary lies in rta's own store, and it is the one file
	// of it the plugin is.
	if _, err := os.Lstat(name); err == nil {
		out = append(out, "--rx", name)
	}
	add("--rx", offList, pathDirs(d)...)
	add("--ro", offList, systemRead...)
	add("--ro", own, d.Readable...)
	add("--rw", offList, systemWrite...)
	return out
}

// carve is the rules that allow root without anything beneath it that off
// names: root itself when nothing of off lies inside it, nothing when root
// is one of off or lies inside one, and otherwise each of its entries, carved
// the same way, with root itself left unnamed.
//
// **Landlock only allows, and a directory allowed is everything beneath
// it.** A deny list says "/etc, but not /etc/rta"; an allow list can only
// name every entry of /etc that is not /etc/rta, which is what this does, as
// deep as the path to what is left out goes. A file created in root after the
// launch is on no rule, which is the direction to be wrong in.
//
// A link is judged by where it leads, since the kernel follows it before
// Landlock looks at what it reached: one that leads to or into what is left
// out is not allowed. One that leads elsewhere is allowed and never descended
// into, so a loop of links ends, and a link to a directory that holds what is
// left out is not allowed at all, rather than carved through, for the same
// reason. That is the shape a Kubernetes secret is mounted in: the file is a
// link into `..data`, a link to a directory named by its timestamp. A
// directory that cannot be listed allows nothing.
func carve(root string, off []string) []string {
	info, err := os.Lstat(root)
	if err != nil {
		return nil
	}
	link := info.Mode()&os.ModeSymlink != 0
	at := root
	if link {
		if at, err = filepath.EvalSymlinks(root); err != nil {
			return nil
		}
	}
	if slices.ContainsFunc(off, func(o string) bool { return inside(root, o) || inside(at, o) }) {
		return nil
	}
	if !slices.ContainsFunc(off, func(o string) bool { return inside(o, root) || inside(o, at) }) {
		return []string{root}
	}
	if link || !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, carve(filepath.Join(root, e.Name()), off)...)
	}
	return out
}

// confineLaunch gives a strict launch the one temporary directory it may use:
// the one made for it (socketDir), where its socket goes and which teardown
// removes, named on the policy and as its TMPDIR. Not TMPDIR itself, which is
// everybody's: on a machine with no home directory rta's own state is kept in
// a private directory under it (paths.Data), and a rule beneath TMPDIR would
// hand that over with everything else there.
func confineLaunch(cmd *exec.Cmd, d DenySet, dir string) {
	if !d.Strict || len(cmd.Args) < 2 || cmd.Args[1] != ConfineCommand {
		return
	}
	cmd.Args = slices.Insert(cmd.Args, 2, "--rw", dir)
	cmd.Env = append(slices.DeleteFunc(cmd.Env, func(kv string) bool { return strings.HasPrefix(kv, "TMPDIR=") }),
		"TMPDIR="+dir)
}

// pathDirs is the plugin's PATH, which it may run the system's tools from — the
// kube plugin runs kubectl — less the home directory, anything above it and
// anything in it, and anything rta's own state lies in. A tool kept in the
// home directory is part of the home directory: it is run by what reads the
// rest of it (a mise shim reads mise's own state there), and a script there
// may hold what the rest of it holds. A plugin's own binary is allowed on its
// own, wherever it was installed.
func pathDirs(d DenySet) []string {
	home, _ := os.UserHomeDir()
	var out []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if !filepath.IsAbs(dir) {
			continue
		}
		dir = filepath.Clean(dir)
		if home != "" && (inside(home, dir) || inside(dir, home)) {
			continue
		}
		if slices.ContainsFunc(d.NoAccess, func(own string) bool { return inside(dir, own) || inside(own, dir) }) {
			continue
		}
		if !slices.Contains(out, dir) && !strings.HasPrefix(dir, "/proc") {
			out = append(out, dir)
		}
	}
	return out
}
