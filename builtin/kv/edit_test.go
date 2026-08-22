package kv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// stubEditor stands in for the person and their editor: it reports what the
// buffer was opened with, writes back whatever the test wants, and records
// the directory it was handed so the test can check what survived it.
func stubEditor(t *testing.T, write func(path string, seen []byte) []byte) *editorRun {
	t.Helper()
	run := &editorRun{}
	origLaunch, origCan := launchEditor, canPrompt
	launchEditor = func(_ []string, path string) error {
		run.calls++
		run.path = path
		seen, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		run.seen = seen
		if write == nil {
			return nil
		}
		return os.WriteFile(path, write(path, seen), 0o600)
	}
	canPrompt = func(req plugin.Request) bool { return req.Surface() == plugin.SurfaceCLI }
	t.Cleanup(func() { launchEditor, canPrompt = origLaunch, origCan })
	return run
}

type editorRun struct {
	calls int
	path  string
	seen  []byte
}

func replaceWith(s string) func(string, []byte) []byte {
	return func(string, []byte) []byte { return []byte(s) }
}

// cliEdit runs kv.edit the way a person does: at a terminal, with the store
// unlocked from the environment the test set up.
func cliEdit(t *testing.T, key string) (view.View, error) {
	t.Helper()
	return runEdit(context.Background(), cliReq(map[string]any{
		"key": key, "passphrase": "correct horse battery staple",
	}))
}

// showPairs reads kv.show back as a map, so a test can name the field it
// cares about instead of an index.
func showPairs(t *testing.T, key string) map[string]string {
	t.Helper()
	v, err := runShow(context.Background(), req(map[string]any{"key": key}, false))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, p := range v.(view.KeyValue).Pairs {
		out[p.Key] = p.Value
	}
	return out
}

// storedEntry opens the store directly, for the metadata no view exposes
// verbatim.
func storedEntry(t *testing.T, key string) entry {
	t.Helper()
	s, verr := load(req(nil, false))
	if verr != nil {
		t.Fatal(verr)
	}
	e, ok := s.Entries[key]
	if !ok {
		t.Fatalf("no entry %q", key)
	}
	return e
}

// The reason the capability exists: the new value never appears in argv, so
// it never reaches shell history — which is the leak `kv set <key> <value>`
// has and this does not.
func TestEditStoresWhatTheEditorWroteAndRelabelsIt(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "chain", "value": "placeholder", "description": "staging ingress"}, false)
	cert := "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"
	editor := stubEditor(t, replaceWith(cert))

	v, err := cliEdit(t, "chain")
	if err != nil {
		t.Fatal(err)
	}
	if string(editor.seen) != "placeholder" {
		t.Errorf("the editor was opened on %q, not the stored value", editor.seen)
	}
	if got := text(t, runGet, map[string]any{"key": "chain"}, false); got != cert {
		t.Errorf("stored %q", got)
	}
	// The kind is a property of the value, and a certificate still filed as
	// a string is one `kv list --kind certificate` will not find.
	show := showPairs(t, "chain")
	if show["kind"] != "certificate" {
		t.Errorf("kind = %q after the value became a certificate", show["kind"])
	}
	// The description belongs to the entry, not to the value.
	if show["description"] != "staging ingress" {
		t.Errorf("description = %q", show["description"])
	}
	if strings.Contains(v.(view.Text).Body, cert) {
		t.Error("the confirmation printed the new value")
	}
}

// vim writes a final newline whether or not the buffer had one. A bearer
// token that came back one byte longer produced 401s that read exactly like
// the token had been revoked, while the store, `kv show` and the byte count
// all agreed the value was fine.
func TestEditDropsTheNewlineTheEditorAddedButKeepsTheOneYouStored(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "token", "value": "old-token"}, false)
	stubEditor(t, replaceWith("ghp_new\n"))

	if _, err := cliEdit(t, "token"); err != nil {
		t.Fatal(err)
	}
	if got := text(t, runGet, map[string]any{"key": "token"}, false); got != "ghp_new" {
		t.Errorf("stored %q, want the token without the editor's newline", got)
	}

	// …and a value that ends in a newline on purpose keeps it: an
	// authorized_keys line without its terminator is a broken file, not a
	// tidier one.
	text(t, runSet, map[string]any{"key": "authorized", "value": "ssh-ed25519 AAAA old\n"}, false)
	stubEditor(t, replaceWith("ssh-ed25519 AAAA new\n"))
	if _, err := cliEdit(t, "authorized"); err != nil {
		t.Fatal(err)
	}
	if got := text(t, runGet, map[string]any{"key": "authorized"}, false); got != "ssh-ed25519 AAAA new\n" {
		t.Errorf("stored %q, want the trailing newline preserved", got)
	}
}

// The plaintext exists on a filesystem for as long as the edit takes, which
// is the whole risk of the feature. Nothing of it — not the file, not the
// .swp beside it — may outlive the command.
func TestEditLeavesNoPlaintextBehind(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "token", "value": "s3cr3t"}, false)
	var swap string
	editor := stubEditor(t, func(path string, seen []byte) []byte {
		// What vim leaves beside the file while you are typing, and emacs
		// after you stop. Removing the file alone would leave this one.
		swap = path + ".swp"
		_ = os.WriteFile(swap, seen, 0o600)
		return []byte("rotated")
	})
	if _, err := cliEdit(t, "token"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(swap); err == nil {
		t.Errorf("the editor's swap copy is still at %s", swap)
	}
	if _, err := os.Stat(editor.path); err == nil {
		t.Errorf("the plaintext is still at %s", editor.path)
	}
	if _, err := os.Stat(filepath.Dir(editor.path)); err == nil {
		t.Errorf("the edit directory survived at %s", filepath.Dir(editor.path))
	}
}

// A DER certificate, a PKCS#12 bundle or a JKS keystore opened in a text
// editor comes back re-encoded and cannot be told from the original by size
// — so it is refused rather than mangled, and the round trip that does
// preserve bytes is named.
func TestEditRefusesAValueAnEditorWouldMangle(t *testing.T) {
	setup(t)
	der := string([]byte{0x30, 0x82, 0x01, 0xff, 0x00, 0xfe})
	text(t, runSet, map[string]any{"key": "der", "value": der, "kind": "certificate"}, false)
	editor := stubEditor(t, replaceWith("whatever"))

	_, err := cliEdit(t, "der")
	ve := view.AsError(err, "z")
	if ve.Code != "kv.edit.binary" {
		t.Fatalf("editing a binary value = %+v", ve)
	}
	if editor.calls != 0 {
		t.Error("the editor was opened on bytes it cannot round-trip")
	}
	if !strings.Contains(ve.Hint, "--file") {
		t.Errorf("hint does not name the round trip that works: %q", ve.Hint)
	}
	if got := text(t, runGet, map[string]any{"key": "der"}, false); got != der {
		t.Errorf("the refused edit changed the value: %q", got)
	}
}

// An editor is a person at a keyboard, which over MCP is nobody. Decided
// before the store is opened: a passphrase that could not work would report
// kv.wrongpass if load() were reached first.
func TestEditIsRefusedWithoutATerminalBeforeAnythingIsDecrypted(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "token", "value": "s3cr3t"}, false)
	editor := stubEditor(t, replaceWith("rotated"))

	_, err := runEdit(context.Background(), plugin.NewRequest(
		map[string]any{"key": "token", "passphrase": "not the passphrase"}, false, true).
		WithSurface(plugin.SurfaceMCP))
	ve := view.AsError(err, "z")
	if ve.Code != "kv.edit.noterminal" {
		t.Fatalf("MCP edit = %+v, want a refusal", ve)
	}
	if editor.calls != 0 {
		t.Error("an editor was started with nobody to type into it")
	}
}

// Opening a value to look at it and quitting must not age the entry: `kv
// list`'s Updated column is the one place a token that has been sitting
// there for fourteen months is visible, and a write here would answer
// "just now" to the only question that column exists to answer.
func TestEditThatChangesNothingDoesNotAgeTheEntry(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "token", "value": "s3cr3t"}, false)
	before := storedEntry(t, "token")
	stubEditor(t, nil) // opened, read, quit

	v, err := cliEdit(t, "token")
	if err != nil {
		t.Fatal(err)
	}
	if body := v.(view.Text).Body; !strings.Contains(body, "unchanged") {
		t.Errorf("edit with no change = %q", body)
	}
	if got := storedEntry(t, "token").Updated; !got.Equal(before.Updated) {
		t.Errorf("Updated moved from %v to %v", before.Updated, got)
	}
}

// An editor exits 0 having written nothing far more often than anybody means
// to store an empty secret: `:q!` after a `ggdG`, a crash between truncate
// and write. Storing that silently loses the secret with no history to get
// it back from.
func TestEditRefusesAnEmptyResult(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "token", "value": "s3cr3t"}, false)
	stubEditor(t, replaceWith("   \n"))

	_, err := cliEdit(t, "token")
	ve := view.AsError(err, "z")
	if ve.Code != "kv.edit.empty" {
		t.Fatalf("empty edit = %+v", ve)
	}
	if got := text(t, runGet, map[string]any{"key": "token"}, false); got != "s3cr3t" {
		t.Errorf("the emptied value was stored anyway: %q", got)
	}
}

func TestEditDryRunOpensNothing(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "token", "value": "s3cr3t"}, false)
	editor := stubEditor(t, replaceWith("rotated"))

	v, err := runEdit(context.Background(), plugin.NewRequest(
		map[string]any{"key": "token", "passphrase": "correct horse battery staple"}, true, false).
		WithSurface(plugin.SurfaceCLI))
	if err != nil {
		t.Fatal(err)
	}
	if editor.calls != 0 {
		t.Error("a dry run started the editor")
	}
	if body := v.(view.Text).Body; !strings.HasPrefix(body, "would open") {
		t.Errorf("dry run = %q", body)
	}
	if got := text(t, runGet, map[string]any{"key": "token"}, false); got != "s3cr3t" {
		t.Errorf("a dry run changed the value: %q", got)
	}
}

// The buffer is named after the key so the editor's status line means
// something, and the key is whatever somebody typed — a name with a slash in
// it must not decide where the plaintext gets written.
func TestEditFilenameCannotLeaveTheDirectory(t *testing.T) {
	for _, key := range []string{"../../etc/passwd", "/etc/shadow", "..", "a/b", ""} {
		got := editFilename(key, "string")
		if strings.ContainsAny(got, `/\`) || got == "." || got == ".." || got == "" {
			t.Errorf("editFilename(%q) = %q", key, got)
		}
	}
	// The extension is what makes an editor pick a syntax mode, which is the
	// difference between a JSON credential opening with brace matching and
	// opening as one long line.
	if got := editFilename("creds", "json"); got != "creds.json" {
		t.Errorf("json buffer = %q", got)
	}
}

// $EDITOR routinely carries flags, and a windowed editor without its wait
// flag returns before you have saved. Treating the whole string as a program
// name looked for an executable literally called "code --wait".
func TestEditorCommandKeepsTheFlagsPeopleHaveInTheirEnvironment(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "code --wait")
	if got := editorCommand(); len(got) != 2 || got[0] != "code" || got[1] != "--wait" {
		t.Errorf("editorCommand() = %q", got)
	}
	// $VISUAL wins where both are set, which is what the convention says.
	t.Setenv("VISUAL", "nvim")
	if got := editorCommand(); got[0] != "nvim" {
		t.Errorf("editorCommand() = %q, want $VISUAL", got)
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if got := editorCommand(); got[0] != "vi" {
		t.Errorf("editorCommand() = %q, want the editor POSIX guarantees", got)
	}
}

// kv.edit decrypts the whole store, blocks for as long as somebody is in vim,
// and then writes that snapshot back. Written back whole, every other key's
// changes made during the edit were silently gone the moment the editor
// exited — with a success message on both terminals. Every other write in
// this plugin closes that window in milliseconds; this one holds it open for
// human time, which is exactly how long it takes to run one more command.
func TestEditKeepsWhatAnotherCommandWroteWhileTheEditorWasOpen(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "token", "value": "original"}, false)

	// A second key arrives while the editor is up — the ordinary case of
	// having two terminals open.
	stubEditor(t, func(path string, seen []byte) []byte {
		text(t, runSet, map[string]any{"key": "meanwhile", "value": "from the other terminal"}, false)
		return []byte("edited")
	})
	if _, err := cliEdit(t, "token"); err != nil {
		t.Fatal(err)
	}

	if got := text(t, runGet, map[string]any{"key": "token"}, false); got != "edited" {
		t.Errorf("the edit itself was lost: token = %q", got)
	}
	if got := text(t, runGet, map[string]any{"key": "meanwhile"}, false); got != "from the other terminal" {
		t.Errorf("the concurrent write was lost: meanwhile = %q", got)
	}
}

// The one case re-reading cannot resolve: the same key changed underneath.
// One of the two values is about to be lost whatever happens, and only the
// person knows which — so refuse and say so, rather than picking.
func TestEditRefusesWhenTheSameKeyChangedUnderneath(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "token", "value": "original"}, false)

	stubEditor(t, func(path string, seen []byte) []byte {
		text(t, runSet, map[string]any{"key": "token", "value": "rotated elsewhere"}, false)
		return []byte("edited")
	})
	_, err := cliEdit(t, "token")
	if err == nil {
		t.Fatal("the edit overwrote a value that had changed underneath it")
	}
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "kv.edit.conflict" {
		t.Fatalf("want kv.edit.conflict, got %v", err)
	}
	// And it means "nothing was changed": the value that landed while the
	// editor was open is still there.
	if got := text(t, runGet, map[string]any{"key": "token"}, false); got != "rotated elsewhere" {
		t.Errorf("the refusal still wrote something: token = %q", got)
	}
}
