// Package paths resolves where rta keeps its own files.
//
// It exists so there is exactly one answer to "where does state live?".
// Grants (internal/grant) and the built-in stores (builtin/internal/itemstore)
// both need it, and they sit in packages that cannot import each other; a
// second copy of this logic would be a directory that silently diverges the
// day someone adds an env var to one of them.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// Data resolves where local state lives: RTA_DATA_DIR overrides (tests,
// portable setups), otherwise XDG data conventions.
func Data() string {
	if d := os.Getenv("RTA_DATA_DIR"); d != "" {
		return d
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "rta")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".local", "share", "rta")
}

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
func EnsureData() (string, error) {
	dir := Data()
	return dir, os.MkdirAll(dir, 0o700)
}

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
