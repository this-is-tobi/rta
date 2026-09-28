package textclean

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/internal/textclean/glyph"
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
//
// **What a reader cannot see is what glyph.Seen says, not what Deceives
// does.** Deceives is the set the display filter drops, and it leaves out on
// purpose what a result may carry intact — a joiner that builds an emoji or a
// letter form, a selector that picks how the character before it is drawn
// (isInvisible says why) — and it never named the Hangul fillers, the soft
// hyphen or the Braille blank, which draw as nothing too. Source has no such
// data to keep, and each of them hides something in it: a Hangul filler is a
// letter to Go, so an identifier made of one is a name nobody sees, and a run
// of selectors after one emoji spells bytes nobody reads. So a file is held to
// Record's rule rather than Terminal's: every character in it reads as itself.
// The tree met it but for three selectors choosing an emoji's colour form: the
// docs' two went, as the changelog's warning sign never had one, and the
// fixture's is built from its code point.
//
// **And no file may hold a character that reads as an ASCII one it is not**
// (lookalike): a curly quote, a hyphen other than the ASCII one, a space
// other than the ASCII one. A reviewer sees those, and sees the wrong thing.
// gofmt makes the first without asking: its doc-comment printer rewrites two
// backquotes as U+201C and two single quotes as U+201D, so a comment that
// wrote the empty string the way a shell does landed as a closing double
// quote nobody typed. In a string the cost is the operator's: a hint that
// quotes a value in curly quotes, or spells a flag with a non-breaking
// hyphen, is copied into a shell as characters the shell does not treat as
// the ones it shows. And a fixture reads as what it imitates — the test of
// an agent name holding U+2011 read, in its own source, as the test of the
// ASCII name it is refused for resembling.
//
// Held by what they stand in for rather than for being typographic: the em
// and en dashes, the ellipsis, the arrows and the box drawing the prose and
// the renderers use stand in for nothing anybody types, and stay.
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
	rdquo, nbsp, nbhy := string(rune(0x201d)), string(rune(0xa0)), string(rune(0x2011))
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
	// A character that reads as an ASCII one it is not, wherever it stands;
	// and the prose's own typography, which stands in for nothing, passed over.
	write("quote.go", "package main\n\n// the empty string, "+rdquo+" in a shell\n")
	write("nbsp.md", "a"+nbsp+"b\n")
	write("hyphen.yaml", "name: claude"+nbhy+"desktop\n")
	write("prose.go", "package main\n\n// one "+string(rune(0x2014))+" and "+string(rune(0x2026))+" "+
		string(rune(0x2192))+" "+string(rune(0x26a0))+"\n")
	// What draws as nothing though Deceives passes it: an identifier Go reads
	// as a letter, and a selector after an emoji, the shape a run of them
	// spells bytes in.
	write("filler.go", "package main\n\nvar "+string(rune(0x3164))+" = 1\n")
	write("selector.md", "mark it "+string(rune(0x2764))+string(rune(0xfe0f))+"\n")
	// A binary file is read by nothing that reviews it as text: it is passed
	// over, whatever its bytes happen to spell.
	write("logo.png", "\x89PNG\r\n\x1a\n\x00\x00"+rlo)
	tracked := []string{"docs/windows.md", "main.go", "charts/x/templates/_helpers.tpl",
		"mise.toml", "go.mod", ".gitignore", "logo.png", "quote.go", "nbsp.md", "hyphen.yaml", "prose.go",
		"filler.go", "selector.md"}
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
	want := []string{
		".gitignore:2 holds U+202E", "charts/x/templates/_helpers.tpl:1 holds U+202E", "filler.go:3 holds U+3164",
		"go.mod:1 holds U+202E", "hyphen.yaml:1 holds U+2011", "main.go:3 holds U+202E",
		"mise.toml:2 holds U+202E", "nbsp.md:1 holds U+00A0", "quote.go:3 holds U+201D",
		"selector.md:1 holds U+FE0F",
	}
	if len(found) != len(want) {
		t.Fatalf("found %q, want each of %q and nothing else", found, want)
	}
	for i, at := range want {
		if !strings.HasPrefix(found[i], at) {
			t.Errorf("found %q, want %s", found[i], at)
		}
	}
}

// What gofmt writes into a doc comment is what the guard holds. Its printer
// turns two single quotes into U+201D and two backquotes into U+201C, which
// is how a curly quote reaches a file whose author typed none; a gofmt that
// wrote some other character there would fail here first.
func TestTheSourceGuardHoldsWhatGofmtWrites(t *testing.T) {
	src := "package p\n\n// Empty is '', or `` in a shell.\nfunc Empty() string { return \"\" }\n"
	out, err := format.Source([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var wrote []rune
	for _, r := range string(out) {
		if r > unicode.MaxASCII {
			wrote = append(wrote, r)
		}
	}
	if len(wrote) != 2 || wrote[0] != 0x201d || wrote[1] != 0x201c {
		t.Fatalf("gofmt wrote %q into the doc comment, want U+201D and U+201C", string(wrote))
	}
	for _, r := range wrote {
		if lookalike(r) != '"' {
			t.Errorf("U+%04X, which gofmt writes, is not held as the quote it reads as", r)
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
			case Deceives(string(r)), !glyph.Seen(r) && lookalike(r) == 0:
				found = append(found, fmt.Sprintf("%s:%d holds U+%04X, which no reader of the file can see; "+
					"remove it, or build it at run time from its code point where it is the point", rel, line, r))
			case lookalike(r) != 0:
				ascii := string(lookalike(r))
				why := ""
				if r == 0x201c || r == 0x201d {
					why = " (gofmt writes one for two backquotes or two single quotes in a doc comment)"
				}
				found = append(found, fmt.Sprintf("%s:%d holds U+%04X, which reads as %q and is not it%s; "+
					"write %q, or build it at run time from its code point where it is the point",
					rel, line, r, ascii, why, ascii))
			}
		}
	}
	return found, nil
}

// lookalike is the ASCII character r reads as and is not, or 0 for a
// character that reads as itself. The primes are here because most fonts
// draw them as the straight quotes; every space but the ASCII one draws as
// one.
func lookalike(r rune) rune {
	switch {
	case r >= 0x2018 && r <= 0x201b, r == 0x2032:
		return '\''
	case r >= 0x201c && r <= 0x201f, r == 0x2033:
		return '"'
	case r >= 0x2010 && r <= 0x2012, r == 0x2212:
		return '-'
	case r != ' ' && unicode.Is(unicode.Zs, r):
		return ' '
	}
	return 0
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
