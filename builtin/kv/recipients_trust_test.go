package kv

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// kv.recipients is plaintext on purpose: "who can read this?" has to be
// answerable without unlocking anything. The cost of that is a file with no
// cryptographic tie to the store, writable by anyone who can write the data
// directory without ever holding a key — a writer that cannot read,
// pointed at the one file in kv that decides who the next write encrypts to.
//
// writeKeys has guarded the ordinary-write case for a while, by comparing the
// file against the ciphertext's own embedded record. These are the two ways
// past that guard, both found by audit and both proven before they were
// fixed: one where the comparison is never reached, and one where there is
// nothing yet to compare against.

// plant writes a recipients file directly, the way something that is not rta
// would.
func plant(t *testing.T, specs ...string) {
	t.Helper()
	if err := os.WriteFile(recipientsPath(), []byte(strings.Join(specs, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRekeyWillNotBuildOnATamperedRecipientsFile(t *testing.T) {
	// The attack: append one line to kv.recipients and wait. The operator runs
	// any `kv rekey` for their own reasons — adding a colleague, rotating a
	// key — and because re-key started from the file rather than from the
	// ciphertext, every secret is re-encrypted to the planted reader as well,
	// with nothing on screen to say so.
	//
	// Re-key never reached writeKeys' mismatch guard because it computes its
	// own recipient set, which made it the way around it.
	setupWithConfig(t)
	keys := t.TempDir()
	victim, _ := writeSSHKeypair(t, keys, "id_ed25519")
	bare := func(values map[string]any) plugin.Request {
		return plugin.NewRequest(values, false, false)
	}
	if _, err := runSet(context.Background(), bare(map[string]any{
		"key": "prod-token", "value": "super-secret", "identity": victim,
	})); err != nil {
		t.Fatal(err)
	}
	stored, verr := loadRecipients()
	if verr != nil || len(stored) != 1 {
		t.Fatalf("recipients = %v (%v)", stored, verr)
	}

	// Mallory appends her own public key. No decrypt, no key, no rta command.
	_, mallory := writeSSHKeypair(t, keys, "mallory")
	mal, err := os.ReadFile(mallory)
	if err != nil {
		t.Fatal(err)
	}
	plant(t, stored[0], strings.TrimSpace(string(mal)))

	// The operator re-keys for an unrelated reason.
	_, err = runRekey(context.Background(), bare(map[string]any{
		"generate": true, "identity": victim,
	}))
	if err == nil {
		after, _ := loadRecipients()
		t.Fatalf("a hand-edited recipients file was adopted as the base of a re-key — "+
			"the store is now readable by %d keys: %v", len(after), after)
	}
	if ve := view.AsError(err, "z"); ve.Code != "kv.recipients.mismatch" {
		t.Fatalf("code = %q, want kv.recipients.mismatch (%v)", ve.Code, err)
	}
}

func TestRekeyOnlyStaysTheWayOutOfAMismatch(t *testing.T) {
	// The other half, and the reason the refusal above is conditional. Both
	// mismatch hints — this one and writeKeys' — tell the operator to run
	// `kv rekey --only --recipient <the set it should be>`. That has to keep
	// working when the file is wrong, or a tampered file would be unfixable
	// and the advice would be a dead end.
	//
	// It is safe precisely because --only discards the stored set: nothing
	// untrusted reaches the new recipients.
	setupWithConfig(t)
	keys := t.TempDir()
	victim, _ := writeSSHKeypair(t, keys, "id_ed25519")
	bare := func(values map[string]any) plugin.Request {
		return plugin.NewRequest(values, false, false)
	}
	if _, err := runSet(context.Background(), bare(map[string]any{
		"key": "prod-token", "value": "super-secret", "identity": victim,
	})); err != nil {
		t.Fatal(err)
	}
	_, mallory := writeSSHKeypair(t, keys, "mallory")
	mal, _ := os.ReadFile(mallory)
	stored, _ := loadRecipients()
	plant(t, stored[0], strings.TrimSpace(string(mal)))

	if _, err := runRekey(context.Background(), bare(map[string]any{
		"only": true, "recipient": []string{victim}, "identity": victim,
	})); err != nil {
		t.Fatalf("the documented recovery is refused: %v", err)
	}
	after, verr := loadRecipients()
	if verr != nil || len(after) != 1 {
		t.Fatalf("recipients = %v (%v), want only the operator's own key back", after, verr)
	}
	// And the secret is still the operator's to read.
	v, err := runGet(context.Background(), bare(map[string]any{"key": "prod-token", "identity": victim}))
	if err != nil || v.(view.Text).Body != "super-secret" {
		t.Fatalf("value = %v (%v)", v, err)
	}
}

func TestAPlantedRecipientsFileCannotClaimAStoreThatDoesNotExist(t *testing.T) {
	// The first-write case, where the mismatch guard cannot help: there is no
	// ciphertext, so there is no embedded record to compare against.
	//
	// kv's own doc says a passphrase store needs no init — that is the whole
	// point of it. So an operator who has never run `kv init` types `kv set`,
	// and with a planted recipients file their first secret is encrypted to
	// the planted key and to nothing else: handed to whoever planted it, and
	// unreadable by the person who wrote it.
	//
	// Nothing legitimate produces a recipients file with no store: saveTo
	// writes it only after the ciphertext lands, and kv.init refuses when one
	// is already there. So refusing costs a real operator nothing.
	setup(t)
	keys := t.TempDir()
	_, mallory := writeSSHKeypair(t, keys, "mallory")
	mal, err := os.ReadFile(mallory)
	if err != nil {
		t.Fatal(err)
	}
	plant(t, strings.TrimSpace(string(mal)))

	_, err = runSet(context.Background(), req(map[string]any{
		"key": "first-secret", "value": "hunter2",
	}, false))
	if err == nil {
		t.Fatal("the first write adopted a planted recipients file — the operator's own secret " +
			"is now encrypted to somebody else's key and not to theirs")
	}
	if ve := view.AsError(err, "z"); ve.Code != "kv.recipients.orphan" {
		t.Fatalf("code = %q, want kv.recipients.orphan (%v)", ve.Code, err)
	}
	// Nothing was written: a refusal here must not leave a half-made store.
	if fileExists(storePath()) {
		t.Fatal("a store was created despite the refusal")
	}
}

func TestAnOrdinaryFirstWriteStillNeedsNoSetup(t *testing.T) {
	// The control for the refusal above. `kv set` with no store and no
	// recipients file is the documented zero-setup path, and it has to stay
	// exactly as frictionless as it was.
	setup(t)
	if _, err := runSet(context.Background(), req(map[string]any{
		"key": "first-secret", "value": "hunter2",
	}, false)); err != nil {
		t.Fatalf("the no-setup passphrase path broke: %v", err)
	}
	v, err := runGet(context.Background(), req(map[string]any{"key": "first-secret"}, false))
	if err != nil || v.(view.Text).Body != "hunter2" {
		t.Fatalf("value = %v (%v)", v, err)
	}
}
