package plugin

import (
	"os"
	"path/filepath"
	"strings"
)

// ExpandHome replaces a leading ~ with the user's home directory, the way a
// shell would for a path that never passed through one — a plugin's Local
// path inputs, --out and --file, arrive as typed.
//
// Only "~" and "~/…". "~user" is deliberately not supported: resolving
// another account's home is not something any input here means, and a file
// literally named "~something" in the current directory has to keep working.
// A home directory that cannot be resolved leaves the path as it is, which
// is then refused by whatever opens it, with the path in the message.
//
// Exported because eight plugins carried their own ten-line copy of this
// rule, with nothing to import: the host's copy is internal, and a plugin
// is a separate module. Eight copies of one rule is how the ninth gets it
// wrong.
func ExpandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
}
