package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ExpandHome replaces a leading ~ with the user's home directory, the way a
// shell would for a path that never passed through one — a plugin's Local
// path inputs, --out and --file, arrive as typed.
//
// Only "~" and "~/…", and on Windows "~\…" as well, which is the same path
// spelled with that system's own separator: a PowerShell user types it, and
// left alone it was a directory named "~" under the working directory, which
// is nobody's home. "~user" is deliberately not supported: resolving another
// account's home is not something any input here means, and a file literally
// named "~something" in the current directory has to keep working. A home
// directory that cannot be resolved leaves the path as it is, which is then
// refused by whatever opens it, with the path in the message.
//
// Exported because eight plugins carried their own ten-line copy of this
// rule, with nothing to import: the host's copy is internal, and a plugin
// is a separate module. Eight copies of one rule is how the ninth gets it
// wrong.
func ExpandHome(p string) string { return expandHome(runtime.GOOS, p) }

// expandHome is ExpandHome on goos, whose separators are the ones a path
// after the ~ may open on.
func expandHome(goos, p string) string {
	rest, tilde := strings.CutPrefix(p, "~")
	if !tilde {
		return p
	}
	seps := "/"
	if goos == "windows" {
		seps = `/\`
	}
	if rest != "" && !strings.ContainsRune(seps, rune(rest[0])) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if rest == "" {
		return filepath.Clean(home)
	}
	return filepath.Join(home, rest[1:])
}
