package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, d := range declared {
		add(d)
	}

	// Split where the shell would: everything up to the last separator is the
	// directory being listed, the rest is the fragment to match on.
	dir, fragment := "", typed
	if i := strings.LastIndexByte(typed, filepath.Separator); i >= 0 {
		dir, fragment = typed[:i+1], typed[i+1:]
	}
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

// expandHome resolves a leading ~/ for reading. It is only ever used to *look*
// at the filesystem; what the caller typed is what gets submitted, since a
// path expanded behind somebody's back is a path they can no longer edit.
func expandHome(path string) string {
	if path == "" {
		return "."
	}
	if !strings.HasPrefix(path, "~/") && path != "~" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}
