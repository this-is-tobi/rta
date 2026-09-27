package shellquote

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestArgLeavesAPlainWordBareAndQuotesTheRest(t *testing.T) {
	for in, want := range map[string]string{
		"":               "",
		"db.status":      "db.status",
		"prod/edge":      "prod/edge",
		"host=a.example": "host=a.example",
		"ports=22,80":    "ports=22,80",
		"=x":             "'=x'",
		"a b":            "'a b'",
		"it's":           `'it'"'"'s'`,
		"$(id)":          "'$(id)'",
		"~/x":            "'~/x'",
		"a\x1b[31mb":     `$'a\033[31mb'`,
		"tab\there":      `$'tab\011here'`,
		"it's\n":         `$'it\'s\012'`,
		"back\\slash\r":  `$'back\\slash\015'`,
		"bad\xff":        `$'bad\377'`,
	} {
		if got := Arg(in); got != want {
			t.Errorf("Arg(%q) = %s, want %s", in, got, want)
		}
	}
	// A character that reorders or hides is spelled by its bytes, so the
	// printed command shows it rather than doing it.
	if got := Arg("a" + string(rune(0x202e)) + "b"); got != `$'a\342\200\256b'` {
		t.Errorf("an override = %s, want it spelled by its bytes", got)
	}
}

// A character that draws as nothing is spelled by its bytes, as a control
// is. unicode.IsPrint counts a Hangul filler a letter, a Braille blank and a
// null notehead symbols, and a variation selector or a combining grapheme
// joiner a mark, so each went into plain single quotes, and the grant command
// a refusal hands on read as the bare record quoted — the record the person
// running it did not mean. What decides is textclean's own rule for what a
// reader sees, so the command and the record it is shown beside cannot
// disagree about which characters are there.
func TestArgSpellsACharacterThatDrawsAsNothingByItsBytes(t *testing.T) {
	for _, r := range []rune{0x3164, 0x115f, 0xffa0, 0x2800, 0x1d159, 0xfe0f, 0xe0100, 0x034f, 0xad, 0xa0} {
		s := "prod/db" + string(r)
		got := Arg(s)
		if !strings.HasPrefix(got, "$'prod/db\\") || strings.ContainsRune(got, r) {
			t.Errorf("Arg(prod/db + U+%04X) = %s, want it spelled by its bytes in $'...'", r, got)
		}
	}
	// A character a reader sees as itself stays as it is, however far from
	// ASCII: an accented letter, an ideograph, an emoji.
	for _, s := range []string{"café", "東京", string(rune(0x1f600))} {
		if got := Arg(s); got != "'"+s+"'" {
			t.Errorf("Arg(%q) = %s, want it in plain single quotes", s, got)
		}
	}
}

// What a person pastes has to be what was meant: every spelling Arg makes,
// read back by a shell, is the value it was given. bash reads $'...' in
// every version still shipped, 3.2 included, and the other shells that read
// it are run too where they are installed.
func TestArgReadsBackThroughAShell(t *testing.T) {
	values := []string{
		"plain", "a b; $(id)", "it's", `"double" \ back`, "*?[x]", "~/x", "=x", "a,b=c",
		"a\x1b[31mb\x07", "line\nbreak", "\x9bc1", "bad\xff", "a" + string(rune(0x202e)) + "bad",
		string(rune(0xe0041)) + "tag", "nbsp" + string(rune(0xa0)) + "x",
		"filler" + string(rune(0x3164)), "blank" + string(rune(0x2800)), "vs" + string(rune(0xfe0f)),
	}
	words := make([]string, len(values))
	for i, v := range values {
		words[i] = Arg(v)
	}
	script := "set -- " + strings.Join(words, " ") + `; printf '%s\0' "$@"`
	ran := false
	for _, name := range []string{"bash", "zsh", "ksh"} {
		shell, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		ran = true
		out, err := exec.Command(shell, "-c", script).Output()
		if err != nil {
			t.Fatalf("%s -c %q: %v", shell, script, err)
		}
		got := bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0})
		if len(got) != len(values) {
			t.Fatalf("%s read back %d words, want %d: %q", name, len(got), len(values), out)
		}
		for i, v := range values {
			if string(got[i]) != v {
				t.Errorf("%s read %s back as %q, want %q", name, words[i], got[i], v)
			}
		}
	}
	if !ran {
		t.Skip("no shell that reads $'...' to read the words back")
	}
}
