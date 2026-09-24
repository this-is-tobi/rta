package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/internal/textclean"
)

// Completing a path in a form.
//
// The CLI hands this job to the shell, which is better at it than any program
// could be. Inside the TUI there is no shell to hand it to: a form field
// asking for a private key or an output file is a blank line, and the person
// in front of it is expected to remember a path exactly, with no way to look.
// That is the one input where "I know it is somewhere under ~/.ssh" is a
// perfectly good state to be in.
//
// So a Path field completes as it is typed, directory by directory, the way a
// shell does: tab fills in the only match, a directory completes with its
// trailing slash so the next tab walks into it, and nothing is ever taken away
// — a path that does not exist yet still types fine, which is what an output
// file is.

// maxPathSuggestions bounds one directory listing. A node_modules with four
// thousand entries is not a suggestion list, it is a stall.
const maxPathSuggestions = 200

// pathSuggestions returns the paths that could continue what has been typed,
// with anything the field declared for itself first — for `--identity` those
// are the keys you already have, which beat any amount of walking the disk.
//
// Every entry extends `typed` literally, including a leading "~/" or "./":
// suggestions are matched against the raw input by prefix, so rewriting the
// path the caller is typing would silently stop matching it.
func pathSuggestions(typed string, declared []string) []string {
	out := make([]string, 0, len(declared)+16)
	seen := map[string]bool{}
	// Filenames are somebody else's data: a directory entry can be named
	// anything the filesystem accepts, escape sequences included, and these
	// are drawn straight into the completion list. One that would display as
	// something other than what it is is left out rather than cleaned, for
	// candidateValues' reason: cleaned, a file holding an override was
	// offered as its backslash-u spelling, a path that does not exist — or,
	// beside a file literally named that way, merged with it into one entry
	// naming the wrong file.
	add := func(s string) {
		if s != "" && !textclean.Deceives(s) && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, d := range declared {
		add(d)
	}

	dir, fragment := splitTyped(typed, pathSeparators())
	entries, err := os.ReadDir(expandHome(dir))
	if err != nil {
		return out
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		// Hidden files stay hidden until asked for by name, exactly as a shell
		// does it — otherwise every listing of a home directory is dotfiles.
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(fragment, ".") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(name), strings.ToLower(fragment)) {
			continue
		}
		// A directory keeps its separator, so the next tab lists inside it
		// rather than stopping on the folder itself.
		if e.IsDir() {
			name += string(filepath.Separator)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for i, name := range names {
		if i == maxPathSuggestions {
			break
		}
		add(dir + name)
	}
	return out
}

// expandHome is pathguard.ExpandTilde plus this box's own empty default, and
// it is only ever used to *look* at the filesystem: what the caller typed is
// what gets submitted, since a path expanded behind somebody's back is a
// path they can no longer edit.
func expandHome(path string) string {
	if path == "" {
		return "."
	}
	return pathguard.ExpandTilde(path)
}

// pathSeparators is what may end a directory in a typed path. Windows
// accepts a forward slash wherever its own backslash goes and people type it
// — C:/Users/ is an ordinary spelling there — so both count, the way
// internal/pathguard already reads them. Splitting on the backslash alone
// offered a path typed that way no completions at all, which defeats the
// reason this exists: there is no shell inside the TUI to hand the job to.
func pathSeparators() string { return separatorsFor(runtime.GOOS) }

func separatorsFor(goos string) string {
	if goos == "windows" {
		return `\/`
	}
	return string(filepath.Separator)
}

// splitTyped is the split a shell makes: everything up to and including the
// last separator is the directory being listed, the rest is the fragment to
// match on.
func splitTyped(typed, seps string) (dir, fragment string) {
	if i := strings.LastIndexAny(typed, seps); i >= 0 {
		return typed[:i+1], typed[i+1:]
	}
	return "", typed
}
