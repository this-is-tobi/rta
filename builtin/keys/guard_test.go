package keys

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A comment lands after the key on the one line of the .pub file, and
// ssh-copy-id installs that file as it is: a line break inside it would
// install whatever follows as a key of its own. Refused before anything is
// generated or written, on both commands that take one.
func TestACommentWithALineBreakIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	words, err := toMnemonic(priv.Seed())
	if err != nil {
		t.Fatal(err)
	}
	comment := "me@laptop\nssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGxvb2tzIGxpa2UgYSBrZXkgYnV0IGlzbnQ intruder"
	for name, run := range map[string]func(out string) error{
		"restore": func(out string) error {
			_, err := runRestore(context.Background(), req(map[string]any{"out": out, "words": words, "comment": comment}))
			return err
		},
		"add": func(out string) error {
			_, err := runAdd(context.Background(), req(map[string]any{"out": out, "comment": comment}))
			return err
		},
	} {
		out := filepath.Join(dir, name)
		if got := errCode(run(out)); got != "keys.comment.invalid" {
			t.Errorf("%s: code = %q, want keys.comment.invalid", name, got)
		}
		for _, p := range []string{out, out + ".pub"} {
			if _, err := os.Stat(p); err == nil {
				t.Errorf("%s: %s was written", name, p)
			}
		}
	}
}

// The pipe is read for a phrase, not for whatever is on it: a stray `cat` of
// something large is refused rather than read whole and offered to the
// checksum as twenty-four words.
func TestPipedInputFarLargerThanAPhraseIsRefused(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = saved
		_ = r.Close()
	})
	go func() {
		_, _ = w.Write([]byte(strings.Repeat("word ", maxPipedWords/4)))
		_ = w.Close()
	}()
	_, verr := readPipedWords(req(map[string]any{}).WithSurface(plugin.SurfaceCLI))
	if verr == nil || verr.Code != "keys.restore.stdin" {
		t.Fatalf("verr = %v, want keys.restore.stdin", verr)
	}
}
