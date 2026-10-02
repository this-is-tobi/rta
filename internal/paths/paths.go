// Package paths resolves where rta keeps its own files.
//
// It exists so there is exactly one answer to "where does state live?".
// Grants (internal/grant) and the built-in stores (builtin/internal/itemstore)
// both need it, and they sit in packages that cannot import each other; a
// second copy of this logic would be a directory that silently diverges the
// day someone adds an env var to one of them.
package paths

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sync"
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

// resolveData is Data and whether the answer is the stranded directory, which
// is the one EnsureData has to look at before it is used.
func resolveData() (dir string, isStranded bool) {
	if d := os.Getenv("RTA_DATA_DIR"); d != "" {
		return d, false
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "rta"), false
	}
	if home := homeDir(); home != "" {
		return filepath.Join(home, ".local", "share", "rta"), false
	}
	return stranded(), true
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
// reboot nor a container, and which EnsureData only uses when it is
// the account's own and owner-only (see ownedPrivately), since a name in a
// shared directory is one anybody can create first.
//
// Said once per process, on stderr, and in words that end in what to do: a
// grant that vanishes at the next reboot is fail-closed, but an audit trail
// that does is a surprise, and the person who is told on the first run has
// the whole run to fix it in. Not refused: a bare container that only reads
// the machine (rta sys, rta net) has no use for state and no reason to be
// turned away for lacking a place to put it.
func stranded() string {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("rta-%d", os.Getuid()))
	strandedNotice.Do(func() {
		fmt.Fprintf(noticeTo, "rta: no home directory is set, so its state (grants, the record, stores) is "+
			"kept in %s, which does not outlast a reboot or a container — set HOME or RTA_DATA_DIR to keep it\n", dir)
	})
	return dir
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
// is not the account's own or lets others in is refused rather than used.
func EnsureData() (string, error) {
	dir, isStranded := resolveData()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return dir, err
	}
	if isStranded {
		info, err := os.Lstat(dir)
		if err != nil {
			return dir, err
		}
		if !info.IsDir() || info.Mode().Perm()&0o077 != 0 || !ownedByUs(info) {
			return dir, fmt.Errorf("%s exists and is not a private directory of this account, so rta will "+
				"not keep its state there — set HOME or RTA_DATA_DIR", dir)
		}
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
// setups), otherwise config.yaml in rta's own directory under the user's
// config directory, otherwise ./.rta.yaml for a machine with no such
// directory at all.
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

// OwnConfigDir is rta's directory under the user's config directory —
// ~/.config/rta, ~/Library/Application Support/rta — or "" when the platform
// has no such directory. Everything in it is rta's: config.yaml, and the
// remotes.yaml the operator channel reads beside it.
func OwnConfigDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "rta")
}
