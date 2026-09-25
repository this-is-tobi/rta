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

// What a person pastes has to be what was meant: every spelling Arg makes,
// read back by a shell, is the value it was given. bash reads $'...' in
// every version still shipped, 3.2 included, and the other shells that read
// it are run too where they are installed.
func TestArgReadsBackThroughAShell(t *testing.T) {
	values := []string{
		"plain", "a b; $(id)", "it's", `"double" \ back`, "*?[x]", "~/x", "=x", "a,b=c",
		"a\x1b[31mb\x07", "line\nbreak", "\x9bc1", "bad\xff", "a" + string(rune(0x202e)) + "bad",
		string(rune(0xe0041)) + "tag", "nbsp" + string(rune(0xa0)) + "x",
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
