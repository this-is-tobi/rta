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
