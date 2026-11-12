package plugin

import (
	"context"
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

// ShortHome is ExpandHome's inverse, for showing a path: the home directory,
// and a path under it, spelled with ~, so a row names a path its reader
// recognises and does not print their username into whatever it is pasted
// into. Only the home itself and what is under home + "/": a sibling that
// shares its letters, /home/tobi-work beside /home/tobi, is not under it,
// and a home of "/" — what Docker gives a --user with no passwd entry — has
// nothing to shorten. home is passed in, since a caller that shows many
// paths has already looked it up once.
func ShortHome(p, home string) string {
	home = strings.TrimSuffix(home, "/")
	switch {
	case home == "":
		return p
	case p == home:
		return "~"
	}
	if rest, under := strings.CutPrefix(p, home+"/"); under {
		return "~/" + rest
	}
	return p
}

// AbsPath is a path a person named as the absolute path it is: ~ expanded,
// then resolved against the working directory. For a file a call writes or
// reads and then names back (a dump's receipt, the CA a connection used),
// where a relative path names a different file to whoever reads it from
// another directory. Five plugins wrapped ExpandHome in filepath.Abs on their
// own.
func AbsPath(p string) (string, error) { return filepath.Abs(ExpandHome(p)) }

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

// maxPathSuggestions bounds one completion of a path: a directory is listed
// on a keystroke, and a person choosing among more than this many types more.
const maxPathSuggestions = 200

// SuggestPath completes the path being typed into the input called name,
// directory by directory, for a file setting declared a String, a CA file, so
// that a profile can hold it: a Path input completes as one on its own, and a
// String lost that by being one. A directory is offered with its slash, an
// entry starting with a dot only once the dot is typed, and ~ stays as typed.
// Human surfaces only, as every Suggest.
func SuggestPath(name string) func(context.Context, Request) []string {
	return func(_ context.Context, req Request) []string {
		dir, prefix := filepath.Split(req.String(name))
		look := ExpandHome(dir)
		if look == "" {
			look = "."
		}
		entries, err := os.ReadDir(look)
		if err != nil {
			return nil
		}
		var out []string
		for _, e := range entries {
			n := e.Name()
			if !strings.HasPrefix(n, prefix) || (strings.HasPrefix(n, ".") && !strings.HasPrefix(prefix, ".")) {
				continue
			}
			if e.IsDir() {
				n += "/"
			}
			out = append(out, dir+n)
			if len(out) == maxPathSuggestions {
				break
			}
		}
		return out
	}
}
