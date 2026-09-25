package textclean

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// The rule this package applies to what rta displays, applied to what rta is
// written in: no source file may hold a character a reviewer cannot see.
//
// A bidi override reorders how a line displays without changing what the
// compiler reads, so a comment can visibly close before code that is really
// still inside it — the Trojan Source shape (CVE-2021-42574) — and a
// zero-width or tag character makes two identifiers that look identical
// different. Neither is caught by review, because review reads the rendered
// text, and none of the linters in .golangci.yml looks for them.
//
// It happened here. A test for this very escaping was written with its
// fixtures typed as \u escapes, and what landed on disk was the characters
// themselves: a literal U+202E inside a Go string, invisible in every editor.
// The fix was to plant them by code point, string(rune(0x202e)), and this is
// the check that would have caught it.
//
// Deliberately without an allowlist: the tree holds none of these today, and a
// fixture that needs one builds it at run time instead.
//
// The files read are the ones git tracks, not whatever is under the root. A
// walk of the tree read a nested worktree of another branch, a build output
// and a scratch file nobody meant to commit, and failed this branch's tests
// over them — and a dangling symlink ended the walk with an error that said
// nothing about hidden characters. What the repository ships is what git
// lists, so a new file is read from the moment it is added to the index, and
// not before. A carriage return before a line feed is a line ending: a
// Windows checkout converts every line to one.
//
// Every file git lists, whatever its name. The guard read a list of
// extensions, and the tree it passed held a Helm template shipped in the
// chart, the secret scanner's allowlist, the tool pins, go.mod and every
// ignore file, none of them on the list and every one read by something that
// acts on it. A name is not what makes a file text; its bytes are, so a file
// is passed over as binary the way git decides it — a NUL in its first 8000
// bytes — and nothing else is.
func TestNoSourceFileHidesACharacter(t *testing.T) {
	found, err := hiddenInTrackedSource(repoRoot(t))
	if err != nil {
		t.Skipf("no git checkout to list the tracked files of: %v", err)
	}
	for _, f := range found {
		t.Error(f)
	}
}

// The guard reads what git tracks and nothing beside it: an untracked nested
// worktree and a scratch file holding an override, a dangling symlink, and a
// file written with Windows line endings pass, and a tracked file holding an
// override is found, on its line.
func TestTheSourceGuardReadsWhatGitTracks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git to build a checkout with")
	}
	dir := t.TempDir()
	rlo := string(rune(0x202e))
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		t.Helper()
		if out, err := gitIn(dir, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	write("docs/windows.md", "one\r\ntwo\r\n")
	write("main.go", "package main\n\n// ends "+rlo+" here\n")
	// Whatever the name says. A Helm template, a tool pin, a module file and
	// an ignore list are read by something that acts on them as surely as Go
	// source is, and none of them has an extension a list would think of.
	write("charts/x/templates/_helpers.tpl", "{{/* "+rlo+" */}}\n")
	write("mise.toml", "[tools]\ngo = \"1."+rlo+"\"\n")
	write("go.mod", "module x"+rlo+"\n")
	write(".gitignore", "bin/\n"+rlo+"\n")
	// A binary file is read by nothing that reviews it as text: it is passed
	// over, whatever its bytes happen to spell.
	write("logo.png", "\x89PNG\r\n\x1a\n\x00\x00"+rlo)
	tracked := []string{"docs/windows.md", "main.go", "charts/x/templates/_helpers.tpl",
		"mise.toml", "go.mod", ".gitignore", "logo.png"}
	if err := os.Symlink("nowhere.md", filepath.Join(dir, "gone.md")); err == nil {
		tracked = append(tracked, "gone.md")
	}
	run(append([]string{"add", "--"}, tracked...)...)
	write("nested/worktrees/old/docs/x.md", "old "+rlo+" branch\n")
	write("scratch.txt", rlo+"\n")
	_ = os.Symlink("/nonexistent", filepath.Join(dir, "docs", "dangling.md"))

	found, err := hiddenInTrackedSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".gitignore:2", "charts/x/templates/_helpers.tpl:1", "go.mod:1", "main.go:3", "mise.toml:2"}
	if len(found) != len(want) {
		t.Fatalf("found %q, want an override reported in each of %q and nothing else", found, want)
	}
	for i, at := range want {
		if !strings.HasPrefix(found[i], at+" holds U+202E") {
			t.Errorf("found %q, want the override at %s", found[i], at)
		}
	}
}

// hiddenInTrackedSource reads the source files git tracks under root and says,
// one entry each, where a character no reader can see stands.
func hiddenInTrackedSource(root string) ([]string, error) {
	listed, err := gitIn(root, "ls-files", "-z").Output()
	if err != nil {
		return nil, err
	}
	var found []string
	for _, rel := range strings.Split(string(listed), "\x00") {
		if rel == "" {
			continue
		}
		file := filepath.Join(root, filepath.FromSlash(rel))
		data, rerr := os.ReadFile(file)
		if rerr != nil {
			// Deleted in the working tree and not yet committed, a link to
			// nothing, or a submodule: none of it is text anybody reads here.
			if info, serr := os.Stat(file); errors.Is(serr, fs.ErrNotExist) || (serr == nil && info.IsDir()) {
				continue
			}
			found = append(found, fmt.Sprintf("%s: %v", rel, rerr))
			continue
		}
		if binary(data) {
			continue
		}
		if !utf8.Valid(data) {
			found = append(found, rel+" is not valid UTF-8")
			continue
		}
		line := 1
		for _, r := range strings.ReplaceAll(string(data), "\r\n", "\n") {
			switch {
			case r == '\n':
				line++
			case r == '\t':
			case Deceives(string(r)):
				found = append(found, fmt.Sprintf("%s:%d holds U+%04X, which no reader of the file can see; "+
					"build it at run time from its code point instead", rel, line, r))
			}
		}
	}
	return found, nil
}

// gitIn is git run in dir, whatever repository the environment names: a hook
// or a script running the tests may export GIT_DIR, and that would list its
// files rather than dir's.
func gitIn(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	return cmd
}

// binary is git's own test for a file it will not diff as text: a NUL in the
// first 8000 bytes (buffer_is_binary in xdiff-interface.c).
func binary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the test's working directory")
		}
		dir = parent
	}
}
