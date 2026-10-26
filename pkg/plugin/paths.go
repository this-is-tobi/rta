package plugin

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ExpandHome replaces a leading ~ with the user's home directory, the way a
// shell would for a path that never passed through one — a plugin's Local
// path inputs, --out and --file, arrive as typed.
//
// Only "~" and "~/…". "~user" is deliberately not supported: resolving
// another account's home is not something any input here means, and a file
// literally named "~something" in the current directory has to keep working.
// A home directory that cannot be resolved leaves the path as it is, which is
// then refused by whatever opens it, with the path in the message.
//
// Exported because eight plugins carried their own ten-line copy of this
// rule, with nothing to import: the host's copy is internal, and a plugin
// is a separate module. Eight copies of one rule is how the ninth gets it
// wrong.
func ExpandHome(p string) string {
	rest, tilde := strings.CutPrefix(p, "~")
	if !tilde || (rest != "" && rest[0] != '/') {
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

// PathInput is the input a refusal to read the file a path input names is
// about (view.Error.Input): input itself when err says the path is what is
// wrong — nothing is there, a directory is where a file was wanted, or a file
// is where a directory was on the way — so the form the call came from opens
// again on that box, and "" when the file is there and cannot be read, which no
// box answers: the path is right, and the fix is a chmod or the machine.
//
//	return nil, view.Errorf("weather.file", "reading %s: %v", path, err).WithInput(plugin.PathInput("file", err))
//
// Exported for the reason ExpandHome is: nine plugins carried their own copy
// of this rule within a week of the field existing, and the host's built-ins
// hold their paths to it (builtin/internal/pathin builds on it).
func PathInput(input string, err error) string {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.EISDIR) {
		return input
	}
	return ""
}
