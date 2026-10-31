// Package paths resolves where rta keeps its own files.
//
// It exists so there is exactly one answer to "where does state live?".
// Grants (internal/grant) and the built-in stores (builtin/internal/itemstore)
// both need it, and they sit in packages that cannot import each other; a
// second copy of this logic would be a directory that silently diverges the
// day someone adds an env var to one of them.
package paths

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// Data resolves where local state lives: RTA_DATA_DIR overrides (tests,
// portable setups), otherwise XDG data conventions.
//
// A machine whose environment names no home — a service started without HOME,
// a container run as a uid with no environment — is asked its account database
// before anything else, and when that has no home either the state is kept in
// a private directory under the temporary one, with a notice saying so (see
// stranded). It used to be "." here, which put the grant file, its seal key and
// the stores in whatever directory rta happened to run in: inside a project an
// agent was working on, readable by it, and one `git add .` from a commit.
func Data() string {
	dir, _ := resolveData()
	return dir
}

// Refused is why the state has no usable directory, or nil: the stranded
// directory exists and is not a private directory of this account (stranded).
// Data answers a path nothing can be opened under in that case, so a reader
// of it finds an error; this is the sentence to give the person who has to
// act on it.
func Refused() error {
	_, problem := resolveData()
	return problem
}

// resolveData is Data and, for the stranded directory, why it cannot be used.
func resolveData() (dir string, problem error) {
	if d := os.Getenv("RTA_DATA_DIR"); d != "" {
		return d, nil
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "rta"), nil
	}
	if home := homeDir(); home != "" {
		return filepath.Join(home, ".local", "share", "rta"), nil
	}
	return stranded()
}

// homeDir is the account's home: $HOME, then the account database, and ""
// when neither names one.
func homeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return passwdHome()
}

// passwdHome is the home the account database gives this uid, overridable so a
// test can be a machine whose database has none.
var passwdHome = func() string {
	u, err := user.Current()
	if err != nil || !filepath.IsAbs(u.HomeDir) {
		return ""
	}
	return u.HomeDir
}

// stranded is where state goes on a machine with no home at all: a directory
// of the account's own under the temporary one, which survives neither a
// reboot nor a container, and which is used only when it is the account's own
// and owner-only, since a name in a shared directory is one anybody can create
// first.
//
// **Judged here, on every way in, and not only where the directory is made.**
// The check lived in EnsureData alone, and a reader never goes through that:
// the grant gate reads grants.json and the key that seals it from wherever
// Data says, so a directory another account had made at this name, holding a
// grant file and a key that agree with each other, was read as the account's
// own grants. A directory that is not private is answered as a path nothing
// opens under (refusedState), so the read is an error, which the gate takes
// for a grant file it cannot trust, and problem says why.
//
// Made here too, if it is not there, so that what is returned is a directory
// this account made: with the check apart from the creation, an account could
// rename a prepared directory into the name between the two. The sticky bit of
// the temporary directory keeps anyone else from removing or renaming ours.
//
// Said once per process, on stderr, and in words that end in what to do: a
// grant that vanishes at the next reboot is fail-closed, but an audit trail
// that does is a surprise, and the person who is told on the first run has
// the whole run to fix it in. Not refused for want of a place: a bare
// container that only reads the machine (rta sys, rta net) has no use for
// state and no reason to be turned away for lacking somewhere to put it, so a
// directory that cannot be made is left to the writer that needs it to say so.
func stranded() (string, error) {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("rta-%d", os.Getuid()))
	problem := privateStranded(dir)
	strandedNotice.Do(func() {
		if problem != nil {
			fmt.Fprintf(noticeTo, "rta: no home directory is set, and %v\n", problem)
			return
		}
		fmt.Fprintf(noticeTo, "rta: no home directory is set, so its state (grants, the record, stores) is "+
			"kept in %s, which does not outlast a reboot or a container — set HOME or RTA_DATA_DIR to keep it\n", dir)
	})
	if problem != nil {
		return refusedState, problem
	}
	return dir, nil
}

// refusedState is the path Data answers when the stranded directory is not
// one rta may use: below a file that is not a directory, so nothing can be
// read from it or made in it, whatever an account may have put at the name it
// stands in for.
var refusedState = filepath.Join(os.DevNull, "rta-state-refused")

// privateStranded makes dir if nobody has and reports why it is not a
// directory of this account that no other account can enter.
func privateStranded(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil
	}
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s cannot be looked at (%w), so rta will not keep its state there — set HOME or RTA_DATA_DIR", dir, err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 || !ownedByUs(info) {
		return fmt.Errorf("%s exists and is not a private directory of this account, so rta will "+
			"not keep its state there — set HOME or RTA_DATA_DIR", dir)
	}
	return nil
}

// strandedNotice and noticeTo are the once and the writer stranded says it
// with, overridable so a test can see it.
var (
	strandedNotice sync.Once
	noticeTo       io.Writer = os.Stderr
)

// System resolves the read-only plugin root: what a container image or a
// package filled at build time and rta only ever reads. RTA_SYSTEM_DIR
// overrides — set but empty means there is none — otherwise /usr/local/lib/rta
// on Linux and nothing elsewhere. Every reader treats "" as "no such root".
//
// A second root rather than a second data directory, because the data
// directory is the operator's: mounted, backed up, mode 0700, written by
// every command. A container image that installed its plugins there baked
// them under the very path a volume then masks, and the chart grew a step
// that copied the image's data directory into the volume before the mount
// hid it. What an image ships belongs where the image's other binaries are,
// outside anything a user mounts, and it is read the way /usr/lib is read: by
// everyone, written by nobody at run time. Discovery scans it after $PATH and
// before the operator's own store; trust from it is read and never written.
func System() string {
	if d, set := os.LookupEnv("RTA_SYSTEM_DIR"); set {
		return d
	}
	if runtime.GOOS == "linux" {
		return "/usr/local/lib/rta"
	}
	return ""
}

// EnsureData creates the data directory if it is missing and returns it.
//
// **This is the only place the directory is created, and it is created
// owner-only.** Everything inside it — the grant file and its seal key, the
// record, the store, parked consent requests, the presence files — is written
// 0600, and `rta doctor` warns when the directory itself lets other accounts
// list those names. For two releases the warning could be about a mode rta
// had chosen: ten writers created the directory with 0700 and six others
// (the notebook, the store's recipients file, the plugin store, the index
// clones, the describe cache) with 0755 through MkdirAll's parent creation,
// so the mode depended on which command a machine happened to run first.
// One creator with one mode is what makes the doctor row a statement about
// the operator's machine rather than about rta's own inconsistency.
//
// An existing directory is left exactly as found: a mode the operator chose
// is theirs to change, and the doctor row is where they are told.
//
// The one directory not left as found is the stranded one: it is named by
// nothing but the uid, in a directory every account can write to, so one that
// is not the account's own or lets others in is refused rather than used
// (stranded), here and for every reader of Data. It is also the one made
// before this runs, by whichever reader resolves it first, with the same
// owner-only mode: the refusal has to be made where it is read, and a check
// apart from the creation leaves room to rename another account's directory
// into the name between the two.
func EnsureData() (string, error) {
	dir, problem := resolveData()
	if problem != nil {
		return dir, problem
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		if verr := Unwritable(dir, err); verr != nil {
			return dir, verr
		}
		return dir, err
	}
	return dir, nil
}

// Indexes is the directory under the data directory the plugin indexes are
// cloned into, one directory each.
//
// Here rather than in internal/plugindist, which clones into it, for
// ConfigFile's reason: the MCP path gate needs the same answer, to know the
// clones for the public content they are (internal/pathguard's readState),
// and a leaf every built-in imports has no business importing the plugin
// installer to learn one directory's name.
func Indexes() string { return filepath.Join(Data(), "indexes") }

// ConfigFile resolves the config file: RTA_CONFIG overrides (tests, portable
// setups), otherwise config.yaml in rta's own config directory, otherwise
// ./.rta.yaml for a machine with no such directory at all.
//
// Here rather than in internal/config, which used to own it, because the MCP
// path gate needs the same answer and cannot import config without a cycle:
// what a caller may never name has to be decided from the same resolution
// the file is read with, or the two drift apart on the day one of them
// learns a new environment variable.
func ConfigFile() string {
	if p := os.Getenv("RTA_CONFIG"); p != "" {
		return p
	}
	if dir := OwnConfigDir(); dir != "" {
		return filepath.Join(dir, "config.yaml")
	}
	return filepath.Join(".", ".rta.yaml")
}

// OwnConfigDir is rta's configuration directory: $XDG_CONFIG_HOME/rta, else
// ~/.config/rta, on every OS — or "" when neither names an absolute place.
// Everything in it is rta's: config.yaml, the policy.yaml and remotes.yaml
// beside it, and the kv.identity key.
//
// **The same convention as Data, on purpose.** os.UserConfigDir answers
// ~/Library/Application Support on macOS, so the two halves of one product sat
// in unrelated trees: state honoured XDG_DATA_HOME and settings ignored
// XDG_CONFIG_HOME, and a dotfiles user who exported it was ignored without a
// message. The old name also held a space, which wraps in the middle of a path
// in every line rta prints one in, so a path it says is a path no one can
// copy off the screen. Nothing reads the old location any more, and no shim
// carries a file across: `rta doctor` names one it finds there
// (LegacyConfigDir) rather than ignoring it quietly.
//
// A relative XDG_CONFIG_HOME is ignored, as the XDG specification says to
// treat it, and the default is used instead; os.UserConfigDir refused the
// whole directory over it, which silently moved the configuration to the
// working-directory file nobody has honoured. $HOME is read alone, with no
// account-database fallback as Data has: no config directory is what makes
// ConfigFile fall back to ./.rta.yaml, and what that fallback is trusted with
// is decided from this same answer (internal/config's trustedPath), so a
// machine that cannot say where its home is must keep failing closed here.
func OwnConfigDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "rta")
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return ""
	}
	return filepath.Join(home, ".config", "rta")
}

// userConfigDir is the platform's own idea of a config directory, overridable
// so a test can be a macOS machine on any host.
var userConfigDir = os.UserConfigDir

// LegacyConfigDir is the directory earlier builds kept the configuration in,
// when it is somewhere other than OwnConfigDir and still exists — on macOS,
// ~/Library/Application Support/rta — or "" when it is the same place, as it
// is on Linux, when it is gone, or when RTA_CONFIG moves the configuration and
// the old directory never held it.
//
// Two readers need it. `rta doctor` has to say a file is being ignored: a
// config that stops applying after an upgrade, with every command still
// succeeding, is the failure that looks like nothing happened. And the path
// gate and the plugin sandbox keep it denied for as long as it is there,
// because what it holds is no less private for no longer being read: the
// kv.identity left in it decrypts the store, and a protection that lapsed with
// the move would have been a loosening bought by a rename.
func LegacyConfigDir() string {
	if os.Getenv("RTA_CONFIG") != "" {
		return ""
	}
	base, err := userConfigDir()
	if err != nil {
		return ""
	}
	old := filepath.Join(base, "rta")
	if old == OwnConfigDir() {
		return ""
	}
	if info, err := os.Stat(old); err != nil || !info.IsDir() {
		return ""
	}
	return old
}

// KVIdentityEnv names the key a key-mode store is unlocked with, wherever the
// operator keeps it: the secrets page tells them to keep it anywhere and name
// it here, and it is how a server an MCP client starts is handed one.
const KVIdentityEnv = "RTA_KV_IDENTITY"

// protected is what Protect was told, for OwnFiles.
var protected struct {
	sync.Mutex
	files []string
}

// Protect adds to OwnFiles files of rta's own named on its command line, the
// bearer tokens and the operator roster `rta mcp serve --http` reads. A
// relative name is taken from the working directory now, as the command that
// reads the file takes it.
func Protect(files ...string) {
	protected.Lock()
	defer protected.Unlock()
	for _, f := range files {
		if f == "" {
			continue
		}
		if abs, err := filepath.Abs(f); err == nil {
			f = abs
		}
		protected.files = append(protected.files, f)
	}
}

// OwnFiles is what of rta's own lies outside its data and configuration
// directories, where the operator chose to put it: the key KVIdentityEnv
// names, and what Protect was told. The plugin sandbox denies each to a
// plugin for reading and writing, unless its path is one the sandbox profile
// cannot spell (pluginhost.ResolveAllowing), and the MCP path gate refuses
// each to a path argument, as they do rta's directories — and for the
// reasons they do: the key decrypts every copy of the store, which a backup
// of the data directory is meant to be "ciphertext and nothing else"
// without; a token read is every agent's credential on the HTTP transport;
// and a line written into the token file or the roster is a caller or an
// operator the operator never enrolled, from the next start on.
//
// A name that is a directory is left out. Each of these is a file, and a
// directory named in its place by mistake — the home directory, the root a
// server is serving — would deny everything under it to every plugin and
// every path argument, for a run that is about to refuse the name anyway.
// A name that does not exist yet is kept, as the deny set keeps every other.
//
// The key is named as kv names it when it opens it: a leading ~ is the home
// directory (plugin.ExpandHome, the rule pathguard.ExpandTilde gives kv),
// and anything else relative is taken from the working directory. Nothing
// expands the tilde before rta sees it in an MCP client's JSON env, a systemd
// Environment= line or a quoted export, which are how a server is handed a
// key, and read literally it named a directory called ~ under the working
// directory while the key itself was denied nowhere. The two files on the
// command line are taken as LoadTokenFile and LoadRoster take them, with no
// tilde of their own: a shell expands one it is given unquoted.
func OwnFiles() []string {
	var files []string
	if key := os.Getenv(KVIdentityEnv); key != "" {
		key = plugin.ExpandHome(key)
		if abs, err := filepath.Abs(key); err == nil {
			key = abs
		}
		files = append(files, key)
	}
	protected.Lock()
	files = append(files, protected.files...)
	protected.Unlock()
	out := files[:0]
	for _, f := range files {
		if info, err := os.Stat(f); err == nil && info.IsDir() {
			continue
		}
		out = append(out, f)
	}
	return out
}

// ownedByUs reports that info names a file this account owns. A FileInfo that
// carries no owner is not vouched for.
func ownedByUs(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

// ErrWeakMode is what a trust anchor refused for its mode wraps
// (CheckTrustAnchor): a chmod answers it, where another path would not.
var ErrWeakMode = errors.New("weak permissions")

// CheckTrustAnchor holds a file an operator hands rta as a trust anchor — a
// roster, a token file, remotes.yaml — to the discipline OpenSSH's StrictModes
// holds an authorized_keys file to; decides is what the file decides, for the
// refusal ("it decides who may operate this server"). rta never writes those
// files, so a check at load is the only guarantee available that a wider
// write did not happen along the way. info is the open handle's, so the file
// checked is the file read.
//
// Group write or execute, or any world bit, refuses. Group read reports
// groupReadable for the caller to warn about, because what it costs depends
// on the file: a token file's group holds every token, a roster's holds no
// secret at all.
//
// A mode says nothing about the account that owns the file: a roster at 0640
// owned by somebody else is one that account rewrites to enroll itself, with
// no bit a mode check refuses. So the owner must be this account or root, who
// could have written the file anyway: root is what a Kubernetes Secret or a
// package mounts one as.
//
// Nor does either say anything about the directories above it, which is the
// half of StrictModes the file's own check leaves out (heldAbove).
func CheckTrustAnchor(path string, info fs.FileInfo, decides string) (groupReadable bool, err error) {
	mode := info.Mode().Perm()
	// 0o037: every world bit, plus group write (2) and group execute (1).
	// Worth spelling the digits out, because the first attempt at this wrote
	// 0o027 and a comment claiming it covered group execute; 2 is write
	// alone, and only the test for an 0o610 file said so. A permission mask
	// is exactly the place a plausible-looking constant goes unchallenged.
	if mode&0o037 != 0 {
		return false, fmt.Errorf("%s has %w (mode %s) — someone besides its owner can write or execute it, "+
			"or any account on this machine can read it, and %s; chmod 600 it", path, ErrWeakMode, mode, decides)
	}
	if uid, ok := ownedByUsOrRoot(info); !ok {
		return false, fmt.Errorf("%s is owned by uid %d, neither this account nor root — that account can "+
			"rewrite it, and %s; chown it", path, uid, decides)
	}
	if err := heldAbove(path, decides); err != nil {
		return false, err
	}
	return mode&0o040 != 0, nil
}

// heldAbove walks every directory above a trust anchor, up to the root, and
// refuses one another account may write, or one owned by neither this account
// nor root. A directory somebody else can write is a file they can replace:
// unlinking the anchor and writing their own in its place needs no bit of the
// file, so its mode and owner, checked above, were the attacker's to choose. A
// sticky directory is the exception, since only an entry's owner may unlink or
// rename it there: /tmp is shared that way, and so is the directory Kubernetes
// mounts a Secret into, where the chart keeps the token file and the roster.
//
// The path as given and the file it leads to are both walked: a link in a
// directory somebody else writes is a link they can point at another file, and
// the file it leads to lies in directories of its own. A directory that is not
// there holds nothing to replace.
func heldAbove(path, decides string) error {
	starts := []string{path}
	if real, err := filepath.EvalSymlinks(path); err == nil && real != path {
		starts = append(starts, real)
	}
	walked := map[string]bool{}
	for _, start := range starts {
		abs, err := filepath.Abs(start)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		for dir := filepath.Dir(abs); !walked[dir]; dir = filepath.Dir(dir) {
			walked[dir] = true
			info, err := os.Stat(dir)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				continue
			case err != nil:
				return fmt.Errorf("%s is in %s, which cannot be checked (%w), and %s", path, dir, err, decides)
			}
			// A directory on a filesystem mounted read-only is one nobody
			// writes, whatever its bits say: Kubernetes gives a Secret's
			// volume the pod's fsGroup and group write with it, and the
			// chart's root filesystem is read-only throughout.
			if errors.Is(unix.Access(dir, unix.W_OK), unix.EROFS) {
				continue
			}
			if mode := info.Mode(); mode.Perm()&0o022 != 0 && mode&fs.ModeSticky == 0 {
				return fmt.Errorf("%s is in %s, whose %w (mode %s) let another account write in it — it can replace "+
					"the file, and %s; chmod go-w %s, or keep the file somewhere only you write",
					path, dir, ErrWeakMode, mode.Perm(), decides, dir)
			}
			if uid, ok := ownedByUsOrRoot(info); !ok {
				return fmt.Errorf("%s is in %s, owned by uid %d, neither this account nor root — that account "+
					"can replace the file, and %s; keep it in a directory you or root own", path, dir, uid, decides)
			}
		}
	}
	return nil
}

// ownedByUsOrRoot is the owner CheckTrustAnchor accepts, and the owner info
// has, for the refusal. A FileInfo that carries no owner is not vouched for.
func ownedByUsOrRoot(info fs.FileInfo) (uid int, ok bool) {
	st, has := info.Sys().(*syscall.Stat_t)
	if !has {
		return -1, false
	}
	uid = int(st.Uid)
	return uid, uid == os.Geteuid() || uid == 0
}
