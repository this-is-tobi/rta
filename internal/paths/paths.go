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
