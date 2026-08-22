package main

import (
	"os"
	"path/filepath"
	"strings"
)

// expandHome resolves a leading ~ the way a shell would, since Local fields
// like --file/--out never pass through one. Mirrors builtin/kv and
// builtin/keys' own copies rather than centralizing a shared helper for a
// handful of lines each plugin already owns.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}
