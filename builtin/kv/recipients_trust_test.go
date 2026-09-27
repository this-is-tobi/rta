package kv

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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

// --- a recipient is the key it spells, never a file named after it ---------
//
// An age recipient is also a perfectly good relative file name, and the
// recipients file is public on purpose. parseRecipient read a spec as a path
// first, and every keys-mode write re-parses the recorded specs, so a file
// named after the store's own recipient in the directory rta happened to run
// in — a cloned repository, the project an MCP server was started in — was
// read in the key's place, and the next ordinary write re-encrypted every
// secret to whatever key the file held. Nothing on screen changed: the
// recorded spec, and the store's embedded copy of it, still read as the
// operator's own key.

// ageKey writes a fresh age identity file and returns its path and the
// recipient it opens for.
func ageKey(t *testing.T, dir, name string) (path, recipient string) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, id.Recipient().String()
}

// shadow puts a file named after recipient into a fresh working directory,
// holding body, and moves the test into that directory.
func shadow(t *testing.T, recipient, body string) {
	t.Helper()
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, recipient), []byte(body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)
}

func TestARecordedRecipientIsNeverReadAsAPath(t *testing.T) {
	keys := t.TempDir()
	_, victim := ageKey(t, keys, "victim")
	_, attacker := ageKey(t, keys, "attacker")
	shadow(t, victim, attacker)

	recipients, verr := recipientsFor([]string{victim})
	if verr != nil {
		t.Fatal(verr)
	}
	if got := recipients[0].(*age.X25519Recipient).String(); got != victim {
		t.Fatalf("the recorded recipient %s was read as the file named after it: encrypting to %s", victim, got)
	}
}

func TestAFileNamedAfterTheRecipientCannotRelockTheStore(t *testing.T) {
	setupWithConfig(t)
	keys := t.TempDir()
	victimKey, victim := ageKey(t, keys, "victim")
	attackerKey, attacker := ageKey(t, keys, "attacker")

	text(t, runInit, map[string]any{"identity": victimKey}, false)
	text(t, runSet, map[string]any{"key": "db-password", "value": "VICTIM-ONLY", "identity": victimKey}, false)

	shadow(t, victim, attacker)
	text(t, runSet, map[string]any{"key": "unrelated", "value": "v", "identity": victimKey}, false)

	v, err := runGet(context.Background(), req(map[string]any{"key": "db-password", "identity": victimKey}, false))
	if err != nil || v.(view.Text).Body != "VICTIM-ONLY" {
		t.Fatalf("the operator lost their own store to a file in the working directory: %v (%v)", v, err)
	}
	_, err = runGet(context.Background(), req(map[string]any{"key": "db-password", "identity": attackerKey}, false))
	if ve := view.AsError(err, "z"); ve.Code != "kv.wrongkey" {
		t.Fatalf("the planted key reads the store: %v (%+v)", v, ve)
	}
}

// The same reading, one level down: a --recipient path whose file holds a
// recipient was answered by re-parsing that line, which could itself be read
// as a path.
func TestARecipientFilesContentsAreNeverFollowedAsAPath(t *testing.T) {
	keys := t.TempDir()
	_, colleague := ageKey(t, keys, "colleague")
	_, attacker := ageKey(t, keys, "attacker")
	pub := filepath.Join(keys, "colleague.txt")
	if err := os.WriteFile(pub, []byte(colleague+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shadow(t, colleague, attacker)

	r, spec, err := parseRecipient(pub)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.(*age.X25519Recipient).String(); got != colleague || spec != colleague {
		t.Fatalf("%s holds %s, and it was read as %s (recorded %s)", pub, colleague, got, spec)
	}
}

func TestRekeyReadsANamedRecipientAsTheKeyItSpells(t *testing.T) {
	setupWithConfig(t)
	keys := t.TempDir()
	victimKey, _ := ageKey(t, keys, "victim")
	attackerKey, attacker := ageKey(t, keys, "attacker")
	_, colleague := ageKey(t, keys, "colleague")

	text(t, runInit, map[string]any{"identity": victimKey}, false)
	text(t, runSet, map[string]any{"key": "db-password", "value": "VICTIM-ONLY", "identity": victimKey}, false)

	shadow(t, colleague, attacker)
	if _, err := runRekey(context.Background(), req(map[string]any{
		"recipient": []string{colleague}, "identity": victimKey,
	}, false)); err != nil {
		t.Fatal(err)
	}
	after, verr := loadRecipients()
	if verr != nil || !slices.Contains(after, colleague) || slices.Contains(after, attacker) {
		t.Fatalf("recipients = %v (%v), want the colleague's key the call spelled", after, verr)
	}
	_, err := runGet(context.Background(), req(map[string]any{"key": "db-password", "identity": attackerKey}, false))
	if ve := view.AsError(err, "z"); ve.Code != "kv.wrongkey" {
		t.Fatalf("the planted key reads the store after a re-key: %+v", ve)
	}
}

// And the lockout guard is not fooled by it: a spelled recipient proves
// nothing about who holds its private half, even when a file of that name
// holds a private key.
func TestANamedRecipientIsNotProofOfHoldingItsKey(t *testing.T) {
	setupWithConfig(t)
	keys := t.TempDir()
	victimKey, _ := ageKey(t, keys, "victim")
	_, colleague := ageKey(t, keys, "colleague")
	planted, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	text(t, runInit, map[string]any{"identity": victimKey}, false)
	text(t, runSet, map[string]any{"key": "k", "value": "v", "identity": victimKey}, false)

	shadow(t, colleague, planted.String())
	_, err = runRekey(context.Background(), req(map[string]any{
		"only": true, "recipient": []string{colleague}, "identity": victimKey,
	}, false))
	if err == nil {
		after, _ := loadRecipients()
		t.Fatalf("a file named after the recipient passed for holding its key; recipients now %v", after)
	}
	if ve := view.AsError(err, "z"); ve.Code != "kv.rekey.lockout" {
		t.Fatalf("code = %q, want kv.rekey.lockout (%v)", ve.Code, err)
	}
}
