package keys

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// refusedInput is the code of err and the input it names, held to the
// declaration of every capability that can answer it: a name one of them does
// not declare is one no surface acts on for it (plugin.Capability.ErrorInput),
// so it fails here rather than passing as a stamp that does nothing.
func refusedInput(t *testing.T, err error, ids ...string) (code, input string) {
	t.Helper()
	verr := view.AsError(err, "keys.test")
	if verr == nil {
		t.Fatalf("%v answered no error", ids)
	}
	if verr.Input != "" {
		for _, id := range ids {
			if _, ok := capabilityByID(t, id).ErrorInput(verr); !ok {
				t.Errorf("%s: %s names the input %q, which %s does not declare", id, verr.Code, verr.Input, id)
			}
		}
	}
	return verr.Code, verr.Input
}

// A refusal of the one input a call got wrong names it, so the form the call
// came from opens again on that box: a key file that is not there or is not
// a private ed25519 key, a passphrase that does not unlock it, a comment that
// would not stay on its line, an out that cannot be written, and seed words
// that are not a backup's.
func TestARefusalOfOneInputNamesIt(t *testing.T) {
	dir := t.TempDir()
	private, public := writeEd25519Keypair(t, dir, "id_ed25519", "")
	locked, _ := writeEd25519Keypair(t, dir, "id_locked", "hunter2")
	rsaKey := writeRSAKeypair(t, dir, "id_rsa", "")
	lockedRSA := writeRSAKeypair(t, dir, "id_rsa_locked", "hunter2")
	notes := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notes, []byte("not a key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lonely := filepath.Join(dir, "lonely")
	if err := os.WriteFile(lonely+".pub", []byte("ssh-ed25519 AAAA lonely\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, "fresh")
	nowhere := filepath.Join(dir, "nowhere", "id_ed25519")
	words := freshWords(t)
	twelve, err := toMnemonic(make([]byte, 16))
	if err != nil {
		t.Fatal(err)
	}

	runs := map[string]plugin.Handler{"keys.backup": runBackup, "keys.add": runAdd, "keys.restore": runRestore}
	for _, c := range []struct {
		name, id string
		values   map[string]any
		code     string
		input    string
	}{
		{"a key file that is not there", "keys.backup", map[string]any{"key": filepath.Join(dir, "id_missing")},
			"keys.backup.unreadable", "key"},
		{"the public half", "keys.backup", map[string]any{"key": public},
			"keys.key.public", "key"},
		{"a file that is no key", "keys.backup", map[string]any{"key": notes},
			"keys.key.invalid", "key"},
		{"an RSA key", "keys.backup", map[string]any{"key": rsaKey},
			"keys.backup.unsupported", "key"},
		{"a locked RSA key", "keys.backup", map[string]any{"key": lockedRSA},
			"keys.backup.unsupported", "key"},
		{"a locked key and no passphrase", "keys.backup", map[string]any{"key": locked},
			"keys.key.locked", "passphrase"},
		{"a locked key and the wrong passphrase", "keys.backup", map[string]any{"key": locked, "passphrase": "hunter3"},
			"keys.key.locked", "passphrase"},
		{"a comment holding a line break", "keys.add", map[string]any{"out": fresh, "comment": "me@laptop\nintruder"},
			"keys.comment.invalid", "comment"},
		{"out in a directory that is not there", "keys.add", map[string]any{"out": nowhere},
			"keys.add.nodir", "out"},
		{"out under a file", "keys.add", map[string]any{"out": filepath.Join(notes, "id_ed25519")},
			"keys.add.nodir", "out"},
		{"out that is a key already", "keys.add", map[string]any{"out": private},
			"keys.add.exists", "out"},
		{"a comment holding a carriage return", "keys.restore",
			map[string]any{"out": fresh, "words": words, "comment": "me@laptop\rintruder"},
			"keys.comment.invalid", "comment"},
		{"out in a directory that is not there", "keys.restore", map[string]any{"out": nowhere, "words": words},
			"keys.restore.nodir", "out"},
		{"out that is a key already", "keys.restore", map[string]any{"out": private, "words": words},
			"keys.restore.exists", "out"},
		{"out whose public half is there", "keys.restore", map[string]any{"out": lonely, "words": words},
			"keys.restore.exists", "out"},
		{"words with a broken checksum", "keys.restore", map[string]any{"out": fresh, "words": brokenChecksum(t, words)},
			"keys.restore.words", "words"},
		{"a phrase too short for a seed", "keys.restore", map[string]any{"out": fresh, "words": twelve},
			"keys.restore.words", "words"},
	} {
		_, err := runs[c.id](context.Background(), req(c.values))
		code, input := refusedInput(t, err, c.id)
		if code != c.code || input != c.input {
			t.Errorf("%s: %s: %s naming %q, want %s naming %q (%v)", c.id, c.name, code, input, c.code, c.input, err)
		}
	}
}

// Seed words left out, and a pipe holding far more than a phrase, are the
// words input refused: the form opens on its box, and nothing is written.
func TestSeedWordsNotGivenOrNotAPhraseNameTheWords(t *testing.T) {
	out := filepath.Join(t.TempDir(), "id_ed25519")
	_, err := runRestore(context.Background(), req(map[string]any{"out": out}).WithSurface(plugin.SurfaceTUI))
	if code, input := refusedInput(t, err, "keys.restore"); code != "keys.restore.nowords" || input != "words" {
		t.Errorf("no words in the TUI: %s naming %q, want keys.restore.nowords naming words", code, input)
	}

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
	_, err = runRestore(context.Background(), req(map[string]any{"out": out}).WithSurface(plugin.SurfaceCLI))
	if code, input := refusedInput(t, err, "keys.restore"); code != "keys.restore.stdin" || input != "words" {
		t.Errorf("a pipe far larger than a phrase: %s naming %q, want keys.restore.stdin naming words", code, input)
	}
}

// A passphrase three tries at a terminal did not get right names the
// passphrase, as one given and refused does.
func TestAPassphraseRefusedAtATerminalNamesThePassphrase(t *testing.T) {
	locked, _ := writeEd25519Keypair(t, t.TempDir(), "id_ed25519", "hunter2")
	oldCan, oldPrompt := canPrompt, promptKeyPassphrase
	canPrompt = func(plugin.Request) bool { return true }
	promptKeyPassphrase = func(string) (string, error) { return "wrong", nil }
	t.Cleanup(func() { canPrompt, promptKeyPassphrase = oldCan, oldPrompt })

	_, err := runBackup(context.Background(), req(map[string]any{"key": locked}))
	if code, input := refusedInput(t, err, "keys.backup"); code != "keys.key.locked" || input != "passphrase" {
		t.Errorf("three wrong answers: %s naming %q, want keys.key.locked naming passphrase", code, input)
	}
}

// Two answers at the prompt that differ are not the passphrase input refused:
// the input was never given, and the fix is typing the same one twice.
func TestTwoDifferentAnswersNameNoInput(t *testing.T) {
	for _, w := range newKeyWriters {
		t.Run(w.verb, func(t *testing.T) {
			atATerminal(t, "s3cret", "s3cr3t")
			_, err := w.run(t, filepath.Join(t.TempDir(), "id_new"), map[string]any{})
			if code, input := refusedInput(t, err, "keys."+w.verb); code != "keys."+w.verb+".mismatch" || input != "" {
				t.Errorf("%s naming %q, want keys.%s.mismatch naming none", code, input, w.verb)
			}
		})
	}
}

// What the write finds at out — a key that got there first, or a destination
// that cannot be written — is out refused, on both capabilities that write a
// key through it.
func TestAKeyThatCannotBeWrittenNamesOut(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	first := filepath.Join(dir, "first")
	if err := os.WriteFile(first, []byte("something else got here first"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(dir, "second")
	if err := os.WriteFile(second+".pub", []byte("something else got here first"), 0o644); err != nil {
		t.Fatal(err)
	}
	for out, want := range map[string]string{first: "keys.restore.exists", second: "keys.restore.exists"} {
		_, verr := publishRestoredKey(out, priv, nil, "")
		if code, input := refusedInput(t, verr, "keys.add", "keys.restore"); code != want || input != "out" {
			t.Errorf("%s: %s naming %q, want %s naming out", filepath.Base(out), code, input, want)
		}
	}

	original := publish
	t.Cleanup(func() { publish = original })
	publish = func(string, []byte, fs.FileMode, int) ([]byte, error) {
		return nil, errors.New("read-only file system")
	}
	_, verr := publishRestoredKey(filepath.Join(dir, "third"), priv, nil, "")
	if code, input := refusedInput(t, verr, "keys.add", "keys.restore"); code != "keys.restore.write" || input != "out" {
		t.Errorf("a write that fails: %s naming %q, want keys.restore.write naming out", code, input)
	}
}

// A key file that is there and that this process may not read names no input:
// the path is right, and the fix is a chmod, not the box. A directory where
// the file was wanted is the path's mistake, and names it.
func TestAKeyFileThatCannotBeReadNamesNoInput(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("mode bits do not stop this reader")
	}
	dir := t.TempDir()
	private, _ := writeEd25519Keypair(t, dir, "id_ed25519", "")
	if err := os.Chmod(private, 0o000); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{private: "", dir: "key"} {
		_, err := runBackup(context.Background(), plugin.NewRequest(map[string]any{"key": path}, false, false))
		if code, input := refusedInput(t, err, "keys.backup"); code != "keys.backup.unreadable" || input != want {
			t.Errorf("%s: %s naming %q, want keys.backup.unreadable naming %q", path, code, input, want)
		}
	}
}
