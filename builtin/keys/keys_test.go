package keys

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/ssh"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// capabilityByID looks one up by name rather than by position. The index
// version carried its own staleness check and duly fired the day a
// capability was inserted above it, which is a guard doing a lookup's job.
// pairIn reads one labelled value out of a KeyValue view.
func pairIn(t *testing.T, kv view.KeyValue, key string) string {
	t.Helper()
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	t.Fatalf("no %q in %v", key, kv.Pairs)
	return ""
}

func capabilityByID(t *testing.T, id string) plugin.Capability {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no capability %q", id)
	return plugin.Capability{}
}

func req(values map[string]any) plugin.Request {
	return plugin.NewRequest(values, false, false)
}

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatal(err)
	}
}

// writeEd25519Keypair writes an ed25519 keypair in the format ssh-keygen
// would, optionally passphrase-protected. Mirrors builtin/kv's
// writeSSHKeypair test helper.
func writeEd25519Keypair(t *testing.T, dir, name, passphrase string) (private, public string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	private = filepath.Join(dir, name)
	if err := os.WriteFile(private, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	public = private + ".pub"
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + name + "@test\n"
	if err := os.WriteFile(public, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return private, public
}

func writeRSAKeypair(t *testing.T, dir, name, passphrase string) (private string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	private = filepath.Join(dir, name)
	if err := os.WriteFile(private, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return private
}

// errCode names the failure a call produced, and "" when it did not fail.
//
// The empty string rather than a dereference: view.AsError returns nil for a
// nil error, so reading .Code straight off it turns "this call unexpectedly
// succeeded" — the exact thing these 27 assertions exist to catch — into a
// segfault with a stack trace instead of `code = "", want keys.restore.words`.
// Found when a 1-in-256 flake in the checksum test below landed on it.
func errCode(err error) string {
	verr := view.AsError(err, "keys.test")
	if verr == nil {
		return ""
	}
	return verr.Code
}

// --- keys.backup / keys.restore: the round trip ------------------------

// The whole point: what keys.restore writes is bit-for-bit the key
// keys.backup read, provable by comparing fingerprints rather than trusting
// the library's own say-so.
func TestBackupRestoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	private, _ := writeEd25519Keypair(t, dir, "id_ed25519", "")

	v, err := runBackup(context.Background(), req(map[string]any{"key": private}))
	if err != nil {
		t.Fatal(err)
	}
	sections := v.(view.Sections)
	backupPairs := sections.Items[0].View.(view.KeyValue).Pairs
	var words, originalFP string
	for _, p := range backupPairs {
		switch p.Key {
		case "Words":
			words = p.Value
		case "Fingerprint":
			originalFP = p.Value
		}
	}
	if strings.Count(words, " ") != 23 {
		t.Fatalf("got %d words, want 24: %q", strings.Count(words, " ")+1, words)
	}

	out := filepath.Join(dir, "restored")
	rv, err := runRestore(context.Background(), req(map[string]any{"out": out, "words": words}))
	if err != nil {
		t.Fatal(err)
	}
	restoredPairs := rv.(view.KeyValue).Pairs
	var restoredFP string
	for _, p := range restoredPairs {
		if p.Key == "Fingerprint" {
			restoredFP = p.Value
		}
	}
	if restoredFP != originalFP {
		t.Errorf("restored fingerprint %q != original %q", restoredFP, originalFP)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("private key mode = %v, want 0600", info.Mode().Perm())
	}
	pubInfo, err := os.Stat(out + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	if pubInfo.Mode().Perm() != 0o644 {
		t.Errorf("public key mode = %v, want 0644", pubInfo.Mode().Perm())
	}

	// Round-trips all the way: the restored private key file itself parses
	// to the same fingerprint, not just the string this package printed.
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ssh.ParseRawPrivateKey(data)
	if err != nil {
		t.Fatal(err)
	}
	priv, err := asEd25519(raw)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := fingerprint(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	if fp != originalFP {
		t.Errorf("fingerprint of the file on disk %q != original %q", fp, originalFP)
	}
}

// The key material round-trips exactly (proven above by fingerprint); the
// PEM file on disk does not. Found live, against the real rta binary and
// real ssh-keygen, before it shipped as a "bit-for-bit" claim in this
// capability's own Description — OpenSSH's private-key
// container writes a random per-encode nonce (the "checkint" pair)
// alongside the key material, which differs on every encode regardless of
// input. Pinning both halves of this — same fingerprint, different bytes —
// so a future change cannot silently make either claim false without a
// test noticing.
func TestRestoredKeyMaterialIsIdenticalButThePemBytesAreNot(t *testing.T) {
	dir := t.TempDir()
	original, _ := writeEd25519Keypair(t, dir, "id_ed25519", "")
	originalBytes, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}

	v, err := runBackup(context.Background(), req(map[string]any{"key": original}))
	if err != nil {
		t.Fatal(err)
	}
	words := ""
	for _, p := range v.(view.Sections).Items[0].View.(view.KeyValue).Pairs {
		if p.Key == "Words" {
			words = p.Value
		}
	}

	out := filepath.Join(dir, "restored")
	if _, err := runRestore(context.Background(), req(map[string]any{"out": out, "words": words})); err != nil {
		t.Fatal(err)
	}
	restoredBytes, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	if strings.TrimSpace(string(originalBytes)) == strings.TrimSpace(string(restoredBytes)) {
		t.Error("original and restored PEM bytes are identical — if OpenSSH's marshaling stopped writing a " +
			"random nonce, the Description's own 'not byte-identical' claim would now be false")
	}

	origRaw, err := ssh.ParseRawPrivateKey(originalBytes)
	if err != nil {
		t.Fatal(err)
	}
	origPriv, err := asEd25519(origRaw)
	if err != nil {
		t.Fatal(err)
	}
	restoredRaw, err := ssh.ParseRawPrivateKey(restoredBytes)
	if err != nil {
		t.Fatal(err)
	}
	restoredPriv, err := asEd25519(restoredRaw)
	if err != nil {
		t.Fatal(err)
	}
	if !origPriv.Equal(restoredPriv) {
		t.Error("original and restored key material differ — the words did not encode the same key")
	}
}

func TestBackupCarriesTheCommentForward(t *testing.T) {
	dir := t.TempDir()
	private, _ := writeEd25519Keypair(t, dir, "id_ed25519", "")

	v, err := runBackup(context.Background(), req(map[string]any{"key": private}))
	if err != nil {
		t.Fatal(err)
	}
	pairs := v.(view.Sections).Items[0].View.(view.KeyValue).Pairs
	var comment string
	for _, p := range pairs {
		if p.Key == "Comment" {
			comment = p.Value
		}
	}
	if !strings.Contains(comment, "id_ed25519@test") {
		t.Errorf("comment pair = %q, want it to mention id_ed25519@test", comment)
	}
}

func TestRestoreAppliesAGivenComment(t *testing.T) {
	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	words, err := toMnemonic(priv.Seed())
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "restored")
	if _, err := runRestore(context.Background(), req(map[string]any{
		"out": out, "words": words, "comment": "me@laptop",
	})); err != nil {
		t.Fatal(err)
	}
	pub, err := os.ReadFile(out + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(pub)), "me@laptop") {
		t.Errorf(".pub = %q, want it to end with the comment", pub)
	}
}

func TestRestoreAppliesAPassphraseToTheWrittenKey(t *testing.T) {
	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	words, err := toMnemonic(priv.Seed())
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "restored")
	if _, err := runRestore(context.Background(), req(map[string]any{
		"out": out, "words": words, "new-passphrase": "s3cret",
	})); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ssh.ParseRawPrivateKey(data); !isLocked(err) {
		t.Fatalf("expected the written key to be passphrase-protected, parse error = %v", err)
	}
	raw, err := ssh.ParseRawPrivateKeyWithPassphrase(data, []byte("s3cret"))
	if err != nil {
		t.Fatalf("could not unlock with the passphrase it was restored with: %v", err)
	}
	restored, err := asEd25519(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Equal(priv) {
		t.Errorf("restored key does not match the original")
	}
}

// A regression test for a real bug review caught:
// keys.backup's --passphrase and keys.restore's --new-passphrase are
// different secrets — one unlocks the key being read, the other locks the
// key being written — and used to share a field name, so both resolved
// from the identical RTA_KEYS_PASSPHRASE environment variable
// (plugin.LocalEnvVar namespaces by plugin, not by field). An operator who
// exported it once to script a backup would silently get an encrypted
// restore later, in the same shell, despite never asking for one and
// despite the field's own Help text promising "omit for none". Renaming
// the restore field to new-passphrase gives it a distinct env var; this
// test drives the real resolution path (plugin.Resolve, not the req()
// helper, which bypasses Local/env entirely) to prove the two no longer
// collide.
func TestRestorePassphraseDoesNotCollideWithBackupsEnvVar(t *testing.T) {
	t.Setenv(plugin.LocalEnvVar("keys.backup", "passphrase"), "leaked-from-a-backup-earlier")

	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	words, err := toMnemonic(priv.Seed())
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "restored")

	restoreCap := capabilityByID(t, "keys.restore")
	resolved := plugin.Resolve(restoreCap, plugin.Inputs{Caller: map[string]any{"out": out, "words": words}})
	if got := resolved["new-passphrase"]; got != nil && got != "" {
		t.Fatalf("new-passphrase resolved to %q from the backup capability's env var — collision is back", got)
	}

	if _, err := runRestore(context.Background(), plugin.NewRequest(resolved, false, false)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ssh.ParseRawPrivateKey(data); err != nil {
		t.Errorf("restored key is passphrase-protected (parse error %v) — RTA_KEYS_PASSPHRASE leaked into the restore", err)
	}
}

// --- keys.backup: passphrase resolution ---------------------------------

func TestBackupOfALockedKeyWithTheRightPassphraseSupplied(t *testing.T) {
	dir := t.TempDir()
	private, _ := writeEd25519Keypair(t, dir, "id_ed25519", "hunter2")

	_, err := runBackup(context.Background(), req(map[string]any{"key": private, "passphrase": "hunter2"}))
	if err != nil {
		t.Fatal(err)
	}
}

func TestBackupOfALockedKeyWithNoPassphraseAndNoPromptRefuses(t *testing.T) {
	dir := t.TempDir()
	private, _ := writeEd25519Keypair(t, dir, "id_ed25519", "hunter2")

	// SurfaceMCP both refuses the whole capability *and* cannot prompt; use
	// SurfaceUnknown (a direct call, as tests make) which is allowed through
	// refuseMCP but still cannot reach a terminal from a test binary.
	_, err := runBackup(context.Background(), req(map[string]any{"key": private}))
	if errCode(err) != "keys.key.locked" {
		t.Errorf("code = %q, want keys.key.locked", errCode(err))
	}
}

func TestBackupPromptsForALockedKeysPassphraseWhenAPersonCouldAnswer(t *testing.T) {
	dir := t.TempDir()
	private, _ := writeEd25519Keypair(t, dir, "id_ed25519", "hunter2")

	old := canPrompt
	canPrompt = func(plugin.Request) bool { return true }
	t.Cleanup(func() { canPrompt = old })
	oldPrompt := promptKeyPassphrase
	promptKeyPassphrase = func(string) (string, error) { return "hunter2", nil }
	t.Cleanup(func() { promptKeyPassphrase = oldPrompt })

	_, err := runBackup(context.Background(), req(map[string]any{"key": private}))
	if err != nil {
		t.Fatal(err)
	}
}

func TestBackupGivesUpAfterRepeatedWrongPassphrases(t *testing.T) {
	dir := t.TempDir()
	private, _ := writeEd25519Keypair(t, dir, "id_ed25519", "hunter2")

	old := canPrompt
	canPrompt = func(plugin.Request) bool { return true }
	t.Cleanup(func() { canPrompt = old })
	tries := 0
	oldPrompt := promptKeyPassphrase
	promptKeyPassphrase = func(string) (string, error) { tries++; return "wrong", nil }
	t.Cleanup(func() { promptKeyPassphrase = oldPrompt })

	_, err := runBackup(context.Background(), req(map[string]any{"key": private}))
	if errCode(err) != "keys.key.locked" {
		t.Errorf("code = %q, want keys.key.locked", errCode(err))
	}
	if tries != keyPassphraseTries {
		t.Errorf("prompted %d times, want %d", tries, keyPassphraseTries)
	}
}

// --- keys.backup: unsupported keys and bad paths ------------------------

func TestBackupRejectsAnRSAKeyByName(t *testing.T) {
	dir := t.TempDir()
	private := writeRSAKeypair(t, dir, "id_rsa", "")

	_, err := runBackup(context.Background(), req(map[string]any{"key": private}))
	if errCode(err) != "keys.backup.unsupported" {
		t.Errorf("code = %q, want keys.backup.unsupported", errCode(err))
	}
}

func TestBackupOfAMissingFileErrorsClearly(t *testing.T) {
	_, err := runBackup(context.Background(), req(map[string]any{"key": "/no/such/key"}))
	if errCode(err) != "keys.backup.unreadable" {
		t.Errorf("code = %q, want keys.backup.unreadable", errCode(err))
	}
}

// --- keys.restore: refuses to clobber ------------------------------------

func TestRestoreRefusesToOverwriteAnExistingPrivateKey(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "existing")
	if err := os.WriteFile(out, []byte("already here"), 0o600); err != nil {
		t.Fatal(err)
	}
	words := freshWords(t)

	_, err := runRestore(context.Background(), req(map[string]any{"out": out, "words": words}))
	if errCode(err) != "keys.restore.exists" {
		t.Errorf("code = %q, want keys.restore.exists", errCode(err))
	}
	if data, _ := os.ReadFile(out); string(data) != "already here" {
		t.Errorf("existing file was modified: %q", data)
	}
}

func TestRestoreRefusesToOverwriteAnExistingPublicKey(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "fresh")
	if err := os.WriteFile(out+".pub", []byte("already here"), 0o644); err != nil {
		t.Fatal(err)
	}
	words := freshWords(t)

	_, err := runRestore(context.Background(), req(map[string]any{"out": out, "words": words}))
	if errCode(err) != "keys.restore.exists" {
		t.Errorf("code = %q, want keys.restore.exists", errCode(err))
	}
	if _, err := os.Stat(out); err == nil {
		t.Errorf("private key was written even though its .pub sibling already existed")
	}
}

// --- keys.restore: malformed words ----------------------------------------

func TestRestoreRejectsAWordCountThatIsNotA32ByteSeed(t *testing.T) {
	// A genuine, checksum-valid 12-word BIP39 phrase — grammatically legal,
	// but 128 bits of entropy where an ed25519 seed needs 256.
	entropy := make([]byte, 16)
	words, err := toMnemonic(entropy)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	_, err = runRestore(context.Background(), req(map[string]any{
		"out": filepath.Join(dir, "out"), "words": words,
	}))
	if errCode(err) != "keys.restore.words" {
		t.Errorf("code = %q, want keys.restore.words", errCode(err))
	}
}

func TestRestoreRejectsAWordWithABrokenChecksum(t *testing.T) {
	broken := brokenChecksum(t, freshWords(t))

	dir := t.TempDir()
	_, err := runRestore(context.Background(), req(map[string]any{
		"out": filepath.Join(dir, "out"), "words": broken,
	}))
	if errCode(err) != "keys.restore.words" {
		t.Errorf("code = %q, want keys.restore.words", errCode(err))
	}
}

func TestRestoreDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	words := freshWords(t)

	values := map[string]any{"out": out, "words": words}
	v, err := runRestore(context.Background(), plugin.NewRequest(values, true, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(view.Text); !ok {
		t.Errorf("dry-run view = %T, want view.Text", v)
	}
	if _, err := os.Stat(out); err == nil {
		t.Errorf("dry-run wrote %s", out)
	}
}

// --- resolveWords -----------------------------------------------------

func TestResolveWordsPrefersTheExplicitFlag(t *testing.T) {
	got, verr := resolveWords(req(map[string]any{"words": "explicit words here"}))
	if verr != nil {
		t.Fatal(verr)
	}
	if got != "explicit words here" {
		t.Errorf("got %q", got)
	}
}

func TestResolveWordsOnANonCliSurfaceWithNothingSuppliedErrorsRatherThanBlocking(t *testing.T) {
	// Mirrors builtin/debug's equivalent test: proves readPipedWords short-
	// circuits on surface before ever touching stdio.Real()/term.IsTerminal.
	r := req(map[string]any{}).WithSurface(plugin.SurfaceMCP)
	_, verr := resolveWords(r)
	if verr == nil || verr.Code != "keys.restore.nowords" {
		t.Errorf("code = %v, want keys.restore.nowords", verr)
	}
}

func TestReadPipedWordsNeverTouchesStdinOnANonCliSurface(t *testing.T) {
	got, verr := readPipedWords(req(map[string]any{}).WithSurface(plugin.SurfaceMCP))
	if verr != nil || got != "" {
		t.Errorf("got %q, %v; want \"\", nil", got, verr)
	}
}

// --- MCP refusal -----------------------------------------------------------

// Key material leaving this machine as words has no revocation and no
// per-call log, and a key an agent generated is a credential nobody watched
// being made: none of the three is a tool. keys.list stays one.
func TestKeyMaterialNeverMovesForAnAgent(t *testing.T) {
	want := map[string]bool{"keys.backup": true, "keys.add": true, "keys.restore": true}
	for _, c := range Plugin().Capabilities {
		if c.HumanOnly != want[c.ID] {
			t.Errorf("%s: HumanOnly = %t, want %t", c.ID, c.HumanOnly, want[c.ID])
		}
	}
}

// --- keys.list --------------------------------------------------------

func TestListWithNoSSHDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	v, err := runList(context.Background(), req(nil))
	if err != nil {
		t.Fatal(err)
	}
	text, ok := v.(view.Text)
	if !ok || !strings.Contains(text.Body, "No ~/.ssh directory found") {
		t.Errorf("got %#v", v)
	}
}

func TestListReportsAnEd25519KeyAsBackupEligible(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeEd25519Keypair(t, sshDir, "id_ed25519", "")

	v, err := runList(context.Background(), req(nil))
	if err != nil {
		t.Fatal(err)
	}
	table := v.(view.Table)
	if table.Total != 1 {
		t.Fatalf("got %d rows, want 1: %+v", table.Total, table.Rows)
	}
	row := table.Rows[0]
	if row[1] != "ssh-ed25519" || row[2] != "no" || row[3] != "yes" || row[4] == "-" {
		t.Errorf("got %v", row)
	}
}

func TestListReportsAnRSAKeyAsNotEligible(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeRSAKeypair(t, sshDir, "id_rsa", "")

	v, err := runList(context.Background(), req(nil))
	if err != nil {
		t.Fatal(err)
	}
	row := v.(view.Table).Rows[0]
	if row[1] != "ssh-rsa" || row[3] != "no" {
		t.Errorf("got %v", row)
	}
}

// Even with no .pub sibling, the type and fingerprint are still knowable
// without decrypting anything: the OpenSSH container a locked key is
// stored in carries its own public key in cleartext
// (ssh.PassphraseMissingError.PublicKey), and probeKey uses exactly that.
// Found by review — the original version of this test
// asserted "unknown" as the correct answer, on a premise (verified false by
// the review) that no public data was available here at all.
func TestListReportsALockedKeyAsLockedButStillIdentifiesItFromTheContainersOwnPublicKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	private, pub := writeEd25519Keypair(t, sshDir, "id_ed25519", "hunter2")
	if err := os.Remove(pub); err != nil {
		t.Fatal(err)
	}
	_ = private

	v, err := runList(context.Background(), req(nil))
	if err != nil {
		t.Fatal(err)
	}
	row := v.(view.Table).Rows[0]
	if row[2] != "yes" {
		t.Errorf("locked column = %q, want yes", row[2])
	}
	if row[1] != "ssh-ed25519" {
		t.Errorf("type = %q, want ssh-ed25519 (recoverable from the container's own embedded public key, no .pub needed)", row[1])
	}
	if row[3] != "yes" {
		t.Errorf("backup-eligible = %q, want yes", row[3])
	}
	if row[4] == "-" {
		t.Errorf("fingerprint = %q, want a real fingerprint", row[4])
	}
}

func TestListSkipsPubFilesAndNonKeyEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeEd25519Keypair(t, sshDir, "id_ed25519", "")
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte("Host *\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	v, err := runList(context.Background(), req(nil))
	if err != nil {
		t.Fatal(err)
	}
	table := v.(view.Table)
	if table.Total != 1 {
		t.Errorf("got %d rows, want 1 (config/known_hosts must be skipped): %+v", table.Total, table.Rows)
	}
}

// --- mnemonic.go --------------------------------------------------------

func TestToMnemonicProducesTwentyFourWordsForAFullSeed(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	words, err := toMnemonic(seed)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(strings.Fields(words)); n != 24 {
		t.Errorf("got %d words, want 24", n)
	}
}

func TestFromMnemonicRoundTripsWithToMnemonic(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	words, err := toMnemonic(seed)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fromMnemonic(words)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(seed) {
		t.Errorf("round trip did not preserve the seed")
	}
}

func TestFromMnemonicRejectsGarbage(t *testing.T) {
	if _, err := fromMnemonic("not a real mnemonic phrase at all"); err == nil {
		t.Error("expected an error")
	}
}

// --- sshkey.go: small helpers --------------------------------------------

func TestExpandHomeExpandsATildePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := expandHome("~/id_ed25519"); got != filepath.Join(home, "id_ed25519") {
		t.Errorf("got %q", got)
	}
	if got := expandHome("/already/absolute"); got != "/already/absolute" {
		t.Errorf("got %q, want it unchanged", got)
	}
}

func TestFingerprintMatchesForTheSameKeyEveryTime(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a, err := fingerprint(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	b, err := fingerprint(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	if a != b || !strings.HasPrefix(a, "SHA256:") {
		t.Errorf("got %q and %q", a, b)
	}
}

func TestPubCommentReadsTheThirdField(t *testing.T) {
	dir := t.TempDir()
	private, _ := writeEd25519Keypair(t, dir, "id_ed25519", "")
	if got := pubComment(private); got != "id_ed25519@test" {
		t.Errorf("got %q", got)
	}
}

func TestPubCommentIsEmptyWithNoSibling(t *testing.T) {
	if got := pubComment("/no/such/key"); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestSuggestPrivateKeysListsIdFilesOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeEd25519Keypair(t, sshDir, "id_ed25519", "")
	if err := os.WriteFile(filepath.Join(sshDir, "config"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	got := suggestPrivateKeys(context.Background(), req(nil))
	if len(got) != 1 || !strings.HasSuffix(got[0], "id_ed25519") {
		t.Errorf("got %v", got)
	}
}

// freshWords makes a valid 24-word backup phrase for a throwaway ed25519
// key, for tests that need well-formed words but do not care whose key they
// encode.
// brokenChecksum swaps the last word of a valid mnemonic for one that makes
// the checksum wrong, and proves it did before handing it back.
//
// **The proof is the point.** This used to substitute a single fixed word
// ("abandon", BIP39 index 0) and assume the result was invalid. Usually it is —
// but the last word of a 24-word mnemonic carries 8 checksum bits over 3 bits
// of entropy, so about one substitution in 256 lands on a mnemonic that is
// still perfectly valid. Against a freshly generated key every run, that is a
// ~0.4% flake: restore succeeds, the test asserts a refusal code on a nil
// error, and the package panics rather than fails. Rare enough to look like
// infrastructure, frequent enough to keep coming back.
//
// Trying candidates until fromMnemonic actually rejects one makes it
// deterministic for any input: at most a handful of the 2048 words leave the
// checksum intact, so the loop settles immediately and — unlike a fixed
// choice — cannot silently stop testing the branch it names.
func brokenChecksum(t *testing.T, words string) string {
	t.Helper()
	fields := strings.Fields(words)
	last := len(fields) - 1
	// Every candidate is a real BIP39 word, so this exercises the checksum
	// branch rather than the "unknown word" one.
	for _, candidate := range bip39.GetWordList() {
		if candidate == fields[last] {
			continue
		}
		fields[last] = candidate
		if _, err := fromMnemonic(strings.Join(fields, " ")); err != nil {
			return strings.Join(fields, " ")
		}
	}
	t.Fatalf("no single-word substitution broke the checksum of %q", words)
	return ""
}

func freshWords(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	words, err := toMnemonic(priv.Seed())
	if err != nil {
		t.Fatal(err)
	}
	return words
}

// --- gaps a review pass found --------------------------

// The interactive word prompt (keys.restore's analogue of keys.backup's
// promptKeyPassphrase, tested above) had no test at all before this one —
// review found it via coverage profiling, not by reading the test names.
func TestResolveWordsPromptsAtATerminalWhenNothingElseIsSupplied(t *testing.T) {
	old := canPrompt
	canPrompt = func(plugin.Request) bool { return true }
	t.Cleanup(func() { canPrompt = old })
	want := freshWords(t)
	oldPrompt := promptWords
	promptWords = func() (string, error) { return want, nil }
	t.Cleanup(func() { promptWords = oldPrompt })

	got, verr := resolveWords(req(nil))
	if verr != nil {
		t.Fatal(verr)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveWordsTreatsAnExplicitEmptyStringTheSameAsOmitted(t *testing.T) {
	r := req(map[string]any{"words": ""}).WithSurface(plugin.SurfaceMCP)
	_, verr := resolveWords(r)
	if verr == nil || verr.Code != "keys.restore.nowords" {
		t.Errorf("code = %v, want keys.restore.nowords (same as omitting the flag)", verr)
	}
}

// unlockKey used to prompt for a locked key's real passphrase before ever
// checking whether the algorithm was even eligible — wasting a person's
// passphrase entry on a key that was always going to be rejected as
// unsupported. Verifies the fix by counting prompts, not just the outcome.
func TestBackupOfALockedRSAKeyNeverPromptsBeforeRejectingTheType(t *testing.T) {
	dir := t.TempDir()
	private := writeRSAKeypair(t, dir, "id_rsa", "hunter2")

	old := canPrompt
	canPrompt = func(plugin.Request) bool { return true }
	t.Cleanup(func() { canPrompt = old })
	tries := 0
	oldPrompt := promptKeyPassphrase
	promptKeyPassphrase = func(string) (string, error) { tries++; return "hunter2", nil }
	t.Cleanup(func() { promptKeyPassphrase = oldPrompt })

	_, err := runBackup(context.Background(), req(map[string]any{"key": private}))
	if errCode(err) != "keys.backup.unsupported" {
		t.Errorf("code = %q, want keys.backup.unsupported", errCode(err))
	}
	if tries != 0 {
		t.Errorf("prompted %d times for an RSA key's passphrase, want 0 — the type was knowable without it", tries)
	}
}

func TestBackupOfAnUnparseableFileErrorsAsInvalidRatherThanLocked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not_a_key")
	if err := os.WriteFile(path, []byte("this is not an SSH key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runBackup(context.Background(), req(map[string]any{"key": path}))
	if errCode(err) != "keys.key.invalid" {
		t.Errorf("code = %q, want keys.key.invalid", errCode(err))
	}
}

func TestListReportsAnUnparseableKeyFileAsUnknownRatherThanCrashing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A key by its own preamble and garbage inside it: something went wrong
	// with a real key, which is exactly what a listing must survive and say.
	corrupt := "-----BEGIN OPENSSH PRIVATE KEY-----\nnot base64 at all\n-----END OPENSSH PRIVATE KEY-----\n"
	if err := os.WriteFile(filepath.Join(sshDir, "id_garbage"), []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}

	v, err := runList(context.Background(), req(nil))
	if err != nil {
		t.Fatal(err)
	}
	row := v.(view.Table).Rows[0]
	if row[1] != "unknown" || row[2] != "no" || row[3] != "unknown" || row[4] != "-" {
		t.Errorf("got %v", row)
	}
}

// **And a file that is not a key at all is not a row.**
//
// It used to be one, reported as `unknown`, because the listing decided by
// name: anything called id_* counted. `id_rsa.old`, a scratch file, an
// editor's backup — each turned up looking like a key something had gone
// wrong with. The distinction matters in both directions, which is why the
// test above stayed: a corrupt key is news, a file that was never a key is
// not.
func TestListSkipsAFileThatIsNotAKeyHoweverItIsNamed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"id_notes":       "just some notes\n",
		"config":         "Host example\n  User me\n",
		"known_hosts":    "example.com ssh-ed25519 AAAA\n",
		"id_rsa.old.bak": "",
	} {
		if err := os.WriteFile(filepath.Join(sshDir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	v, err := runList(context.Background(), req(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, isTable := v.(view.Table); isTable {
		t.Errorf("a directory holding no keys produced a table of them: %v", v)
	}
}

// **A key called anything else is still a key.**
//
// `ssh-keygen -f ~/.ssh/work_ed25519` is an ordinary thing to type and a
// dotfiles repository symlinks keys under whatever names it likes — and a key
// this capability cannot see is a key `keys.backup` cannot be pointed at,
// which is the plugin's whole reason for existing.
func TestListFindsAKeyThatIsNotCalledIdSomething(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sshDir, "work_ed25519")
	writeEd25519Keypair(t, sshDir, "work_ed25519", "")

	v, err := runList(context.Background(), req(nil))
	if err != nil {
		t.Fatal(err)
	}
	tbl, isTable := v.(view.Table)
	if !isTable || len(tbl.Rows) != 1 {
		t.Fatalf("a key not named id_* was not listed: %v", v)
	}
	if tbl.Rows[0][0] != path {
		t.Errorf("listed %q, want %q", tbl.Rows[0][0], path)
	}
	// And the completion offers it, or the listing found something the
	// capability that acts on it cannot be pointed at.
	offered := suggestPrivateKeys(context.Background(), req(nil))
	if len(offered) != 1 || offered[0] != path {
		t.Errorf("keys.backup would not complete to it: %v", offered)
	}
}

// **Exposed is a column that comes and goes**, and its arrival is the
// finding: ssh refuses a private key other accounts can read, so this is at
// once a credential-exposure report and the answer to why that key stopped
// working.
func TestTheExposedColumnAppearsOnlyWhenAKeyIs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits mean nothing here; the ACL is the real answer")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeEd25519Keypair(t, sshDir, "id_ed25519", "")

	v, err := runList(context.Background(), req(nil))
	if err != nil {
		t.Fatal(err)
	}
	if has, _ := columnAt(v.(view.Table), "Exposed"); has {
		t.Error("a directory of well-guarded keys still grew the column")
	}

	// Two grades, not one: ssh refuses both, but "every account on this
	// machine" and "the group this file happens to be in" are different
	// sizes of problem, and builtin/audit already tells them apart.
	loose, _ := writeEd25519Keypair(t, sshDir, "id_shared", "")
	if err := os.Chmod(loose, 0o644); err != nil {
		t.Fatal(err)
	}
	team, _ := writeEd25519Keypair(t, sshDir, "id_team", "")
	if err := os.Chmod(team, 0o640); err != nil {
		t.Fatal(err)
	}
	v, err = runList(context.Background(), req(nil))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	has, at := columnAt(tbl, "Exposed")
	if !has {
		t.Fatalf("a world-readable key did not raise the column: %v", tbl.Columns)
	}
	graded := map[string]string{loose: "world (0644)", team: "group (0640)"}
	for _, row := range tbl.Rows {
		want, listed := graded[row[0]]
		if !listed {
			want = "—"
		}
		if row[at] != want {
			t.Errorf("%s: Exposed = %q, want %q", row[0], row[at], want)
		}
	}
}

func columnAt(t view.Table, name string) (bool, int) {
	for i, c := range t.Columns {
		if c.Name == name {
			return true, i
		}
	}
	return false, 0
}

// A corrupt .pub sibling used to leave a perfectly good, unencrypted
// private key reported as unknown, because the fallback to reading the
// private key itself only ran when .pub was entirely absent. describeKey
// now falls back whenever the .pub does not yield an answer, corrupt or
// missing alike.
func TestListFallsBackToThePrivateKeyWhenThePubSiblingIsCorrupt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	private, pub := writeEd25519Keypair(t, sshDir, "id_ed25519", "")
	if err := os.WriteFile(pub, []byte("not a valid authorized_keys line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = private

	v, err := runList(context.Background(), req(nil))
	if err != nil {
		t.Fatal(err)
	}
	row := v.(view.Table).Rows[0]
	if row[1] != "ssh-ed25519" || row[3] != "yes" || row[4] == "-" {
		t.Errorf("got %v, want the private key's real type/eligibility/fingerprint despite the corrupt .pub", row)
	}
}

// asEd25519 has two branches: *ed25519.PrivateKey (OpenSSH-format keys,
// exercised everywhere else in this file) and ed25519.PrivateKey, the value
// shape only a PKCS8 "PRIVATE KEY" block produces. Untested before this —
// review found the gap by noting every fixture in this file only ever
// produces the OpenSSH format.
func TestAsEd25519HandlesThePkcs8ValueTypeBranch(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ssh.ParseRawPrivateKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := raw.(ed25519.PrivateKey); !ok {
		t.Fatalf("ssh.ParseRawPrivateKey returned %T for a PKCS8 block, want the value type ed25519.PrivateKey — test assumption is stale", raw)
	}
	got, err := asEd25519(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(priv) {
		t.Errorf("asEd25519 did not preserve the key through the value-type branch")
	}
}

func TestRestoreWithAMissingParentDirectoryErrorsCleanly(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "does-not-exist", "restored")
	words := freshWords(t)

	_, err := runRestore(context.Background(), req(map[string]any{"out": out, "words": words}))
	if errCode(err) != "keys.restore.write" {
		t.Errorf("code = %q, want keys.restore.write", errCode(err))
	}
}

// publishRestoredKey's own bytes.Equal re-checks — its defence against a
// race the caller's fileExists pre-checks might lose — were never called
// with a real collision in place. These call it directly, bypassing
// runRestore's guard, the only way to reach them deliberately.
func TestPublishRestoredKeyDetectsAPrivateKeyCollision(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	if err := os.WriteFile(out, []byte("something else got here first"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	_, verr := publishRestoredKey(out, priv, nil, "")
	if verr == nil || verr.Code != "keys.restore.exists" {
		t.Errorf("code = %v, want keys.restore.exists", verr)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "something else got here first" {
		t.Errorf("the pre-existing file was overwritten")
	}
}

func TestPublishRestoredKeyDetectsAPubCollisionAfterThePrivateKeyIsAlreadyWritten(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	if err := os.WriteFile(out+".pub", []byte("something else got here first"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	_, verr := publishRestoredKey(out, priv, nil, "")
	if verr == nil || verr.Code != "keys.restore.exists" {
		t.Fatalf("code = %v, want keys.restore.exists", verr)
	}
	// The whole point of the fix: the message names both files, since the
	// private key really is already on disk by this point and "restore
	// again" alone cannot produce a matching pair.
	if !strings.Contains(verr.Hint, out) || !strings.Contains(verr.Hint, out+".pub") {
		t.Errorf("hint = %q, want it to name both %s and %s", verr.Hint, out, out+".pub")
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("private key was not written despite succeeding before the .pub collision: %v", err)
	}
}

// keys.add: a key that can always be melted, written where nothing was.

// **Generated here means backupable here.** ed25519 and nothing else, which
// is the same rule keys.backup enforces from the other end — a key this
// plugin generated and could not back up would be a plugin failing at its own
// job.
func TestANewKeyIsOneThisPluginCanBackUp(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "id_new")

	v, err := runAdd(context.Background(), req(map[string]any{"out": out, "comment": "me@laptop"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(out + ".pub"); statErr != nil {
		t.Errorf("no public key beside it: %v", statErr)
	}

	// The proof is the round trip, not the file's existence: back it up and
	// restore it, and the fingerprints have to match.
	backedUp, err := runBackup(context.Background(), req(map[string]any{"key": out}))
	if err != nil {
		t.Fatalf("a key this plugin generated could not be backed up: %v", err)
	}
	words := pairIn(t, backedUp.(view.Sections).Items[0].View.(view.KeyValue), "Words")
	again := filepath.Join(dir, "id_again")
	restored, err := runRestore(context.Background(), req(map[string]any{"out": again, "words": words}))
	if err != nil {
		t.Fatal(err)
	}
	a := pairIn(t, v.(view.KeyValue), "Fingerprint")
	b := pairIn(t, restored.(view.KeyValue), "Fingerprint")
	if a != b {
		t.Errorf("restored fingerprint %q, generated %q — the round trip is not the same key", b, a)
	}
}

// The private key is not readable by anybody else, which `keys.list` would
// otherwise report as Exposed the moment it was made.
func TestANewKeyIsWrittenTightlyEnoughForSSHToUseIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits mean nothing here")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "id_new")
	if _, err := runAdd(context.Background(), req(map[string]any{"out": out})); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("mode = %04o — ssh refuses a private key other accounts can read", mode)
	}
}

// **Refusing to overwrite is the sharpest rule here.** A restore that
// clobbered something could at least be redone from the words; this cannot,
// because what it would destroy is access to everything the old key
// authorised.
func TestANewKeyNeverOverwritesAnything(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "id_new")
	if err := os.WriteFile(out, []byte("something precious\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runAdd(context.Background(), req(map[string]any{"out": out}))
	if errCode(err) != "keys.add.exists" {
		t.Fatalf("code = %q, want keys.add.exists", errCode(err))
	}
	data, readErr := os.ReadFile(out)
	if readErr != nil || string(data) != "something precious\n" {
		t.Error("the file was touched anyway")
	}

	// And the same when only the .pub side is in the way, which is the half
	// somebody notices later.
	fresh := filepath.Join(dir, "id_fresh")
	if err := os.WriteFile(fresh+".pub", []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runAdd(context.Background(), req(map[string]any{"out": fresh})); errCode(err) != "keys.add.exists" {
		t.Errorf("an existing .pub did not stop it: %v", err)
	}
	if _, statErr := os.Stat(fresh); statErr == nil {
		t.Error("the private key was written beside a .pub that was already there")
	}
}

// --passphrase is never inherited from the environment, for the reason
// keys.restore's --new-passphrase is not: generating a key is a one-off act,
// not a standing credential for the session.
func TestTheNewKeysPassphraseIsNeverReadFromTheEnvironment(t *testing.T) {
	t.Setenv(plugin.LocalEnvVar("keys.backup", "passphrase"), "hunter2")
	newCap := capabilityByID(t, "keys.add")
	out := filepath.Join(t.TempDir(), "id_new")
	resolved := plugin.Resolve(newCap, plugin.Inputs{Caller: map[string]any{"out": out}})
	if got := resolved["passphrase"]; got != nil && got != "" {
		t.Fatalf("passphrase resolved to %q from the environment", got)
	}
	if _, err := runAdd(context.Background(), plugin.NewRequest(resolved, false, false)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ssh.ParseRawPrivateKey(data); err != nil {
		t.Errorf("the key is passphrase-protected (%v) — the environment leaked in", err)
	}
}

// **And there is no keys.rm**, which is a decision rather than an omission:
// deleting a key file is irreversible loss of access, `rm` is a command
// everybody already has, and the shell they run it in will ask.
func TestThereIsNoCapabilityThatDeletesAKey(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.Safety == plugin.Destructive {
			t.Errorf("%s is destructive — this plugin exists to make keys recoverable", c.ID)
		}
	}
}
