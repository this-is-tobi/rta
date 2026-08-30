// Key material: how the store gets locked, and what can unlock it.
//
// Two modes, and the store's own recipients file decides which is in force:
//
//	passphrase  one scrypt-derived key. Zero setup and nothing to lose but
//	            the passphrase. This is the default and stays the default.
//	keys        encrypted to a set of age or SSH public keys. Reading needs
//	            the matching private key — one you already have, already
//	            back up, and probably already carry between machines.
//
// GPG keys are deliberately absent. The age format has no OpenPGP recipient
// type, so supporting them would mean shelling out to the gpg binary on
// every read: a hidden external dependency, a second trust model, and a
// second set of failure modes inside a store whose value is that it has
// almost none. A gpg plugin is the honest home for
// gpg-native workflows; this store speaks age, and age speaks SSH.
package kv

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rsa"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"golang.org/x/crypto/ssh"

	"github.com/this-is-tobi/rule-them-all/builtin/internal/itemstore"
	"github.com/this-is-tobi/rule-them-all/builtin/internal/sshkeys"
	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// recipientsFile records who can decrypt the store. It is plaintext by
// design: it holds only public keys, and it has to be readable *without*
// unlocking the store — otherwise adding a second recipient would require
// already being able to read what you are re-encrypting.
const recipientsFile = "kv.recipients"

func recipientsPath() string { return filepath.Join(itemstore.DataDir(), recipientsFile) }

// loadRecipients returns the configured recipient specs, in file order.
func loadRecipients() ([]string, *view.Error) {
	data, err := os.ReadFile(recipientsPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, view.Errorf("kv.recipients.unreadable", "reading %s: %v", recipientsPath(), err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out, nil
}

func saveRecipients(specs []string) *view.Error {
	dir := itemstore.DataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return view.Errorf("kv.recipients.write", "creating %s: %v", dir, err)
	}
	body := "# Public keys that can decrypt kv.age. Public: safe to read, safe to commit.\n" +
		strings.Join(specs, "\n") + "\n"
	// Atomically, and at exactly 0644 however the file started out: this
	// names the keys that can open the store, and half of it names half of
	// them — a store nothing left alive can decrypt.
	if err := atomicfile.Write(recipientsPath(), []byte(body), 0o644); err != nil {
		return view.Errorf("kv.recipients.write", "writing %s: %v", recipientsPath(), err)
	}
	return nil
}

// parseRecipient accepts what a person actually has to hand: an age
// recipient string, an SSH public key line, or the path to a file holding
// either (~/.ssh/id_ed25519.pub being the overwhelmingly common case). It
// returns the recipient and the canonical spec to record for it.
func parseRecipient(spec string) (age.Recipient, string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, "", errors.New("empty recipient")
	}
	// A path: read it and retry on its contents.
	if data, err := os.ReadFile(expandHome(spec)); err == nil {
		line := firstMeaningfulLine(string(data))
		if line == "" {
			return nil, "", fmt.Errorf("%s holds no public key", spec)
		}
		// A private key names a recipient perfectly well — its public half —
		// and pointing at the key you have is the obvious thing to try. Only
		// that half is ever read or recorded.
		if pub, err := publicHalf(spec, line); err != nil || pub != "" {
			if err != nil {
				return nil, "", err
			}
			r, _, err := parseRecipient(pub)
			return r, pub, err
		}
		r, _, err := parseRecipient(line)
		return r, line, err
	}
	if strings.HasPrefix(spec, "age1") {
		r, err := age.ParseX25519Recipient(spec)
		if err != nil {
			return nil, "", err
		}
		return r, spec, nil
	}
	r, err := agessh.ParseRecipient(spec)
	if err != nil {
		return nil, "", fmt.Errorf("not an age or SSH public key: %s", redactedSpec(spec))
	}
	return r, spec, nil
}

// redactedSpec is what an error may safely show of a caller-supplied
// recipient spec: the spec itself, truncated, unless it looks like a
// private key — in which case echoing any of it at all would leak secret
// material into an error message that reaches the terminal, shell history,
// or any log capturing command output. Found by review:
// a mistyped `--recipient AGE-SECRET-KEY-1...` — a private identity pasted
// where a public recipient was expected — used to be echoed here verbatim,
// truncated but real, in exactly the class of place parseIdentity's own
// errors (path only, never content) were already careful to avoid.
func redactedSpec(spec string) string {
	if isPrivateKey(spec) {
		return "<a private key, not shown — recipients take the public half>"
	}
	return truncate(spec, 32)
}

// isPrivateKey reports whether a file's first meaningful line begins a private
// key rather than a public one.
func isPrivateKey(line string) bool {
	return strings.HasPrefix(line, "AGE-SECRET-KEY-") ||
		(strings.HasPrefix(line, "-----BEGIN") && strings.Contains(line, "PRIVATE KEY"))
}

// publicHalf turns a path to a private key into the recipient spec for it,
// returning "" when the file is not a private key at all.
//
// An SSH key is answered from its .pub sibling when there is one: it is the
// same key, it carries the comment, and reading it cannot ask anybody for a
// passphrase — which matters, because a recipient is not a thing that should
// ever prompt.
func publicHalf(path, firstLine string) (string, error) {
	if !isPrivateKey(firstLine) {
		return "", nil
	}
	if strings.HasPrefix(firstLine, "AGE-SECRET-KEY-") {
		id, err := age.ParseX25519Identity(firstLine)
		if err != nil {
			return "", fmt.Errorf("%s: %v", path, err)
		}
		return id.Recipient().String(), nil
	}
	data, err := os.ReadFile(expandHome(path) + ".pub")
	if err != nil {
		return "", fmt.Errorf("%s is a private key and has no .pub beside it — name the public key instead", path)
	}
	if line := firstMeaningfulLine(string(data)); line != "" {
		return line, nil
	}
	return "", fmt.Errorf("%s.pub holds no public key", path)
}

// identity is a private key plus the public spec naming it, so a write can
// always keep the writer among the readers.
type identity struct {
	age  age.Identity
	spec string
}

// parseIdentities loads the private key(s) at path: every AGE-SECRET-KEY-
// line in an age identity file, or the one key in an SSH private key. An
// encrypted SSH key is unlocked with the same passphrase input the store's
// passphrase mode uses — one field, whichever thing needs unlocking.
//
// Plural because age's own identity-file convention allows several keys in
// one file (age-keygen >> identities.txt is the documented way to
// accumulate them), and a version of this function that read only the
// first line — found by review — silently dropped every key
// after it: a store actually protected by the second key in the file
// reported kv.wrongkey with the correct key sitting right there on disk.
func parseIdentities(req plugin.Request, path string) ([]identity, *view.Error) {
	data, err := os.ReadFile(expandHome(path))
	if err != nil {
		return nil, view.Errorf("kv.identity.unreadable", "reading %s: %v", path, err).
			WithHint("--identity takes a private key file, e.g. ~/.ssh/id_ed25519")
	}
	// An age identity file: one or more "AGE-SECRET-KEY-1…" lines, comments
	// and blank lines allowed. Checked by the first meaningful line the same
	// way as before — an SSH private key's first line is never this prefix —
	// but every such line in the file is then parsed, not just that one.
	if strings.HasPrefix(firstMeaningfulLine(string(data)), "AGE-SECRET-KEY-") {
		var ids []identity
		for _, line := range meaningfulLines(string(data)) {
			if !strings.HasPrefix(line, "AGE-SECRET-KEY-") {
				continue
			}
			id, err := age.ParseX25519Identity(line)
			if err != nil {
				return nil, view.Errorf("kv.identity.invalid", "parsing %s: %v", path, err)
			}
			ids = append(ids, identity{age: id, spec: id.Recipient().String()})
		}
		return ids, nil
	}

	raw, err := ssh.ParseRawPrivateKey(data)
	if isLocked(err) {
		var verr *view.Error
		if raw, verr = unlockSSHKey(req, path, data); verr != nil {
			return nil, verr
		}
		err = nil
	}
	if err != nil {
		return nil, view.Errorf("kv.identity.invalid", "parsing %s: %v", path, err).
			WithHint("supported: age identity files, ssh-ed25519 and ssh-rsa private keys")
	}

	ageID, err := ageIdentityFromSSH(raw)
	if err != nil {
		return nil, view.Errorf("kv.identity.invalid", "%s: %v", path, err).
			WithHint("age supports ssh-ed25519 and ssh-rsa keys")
	}
	// The authorized-keys line is exactly the spec --recipient accepts, so
	// the key that wrote the store can be recorded as one of its readers.
	signer, err := ssh.NewSignerFromKey(raw)
	if err != nil {
		return nil, view.Errorf("kv.identity.invalid", "%s: %v", path, err)
	}
	spec := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	// The .pub sibling carries the comment ("me@laptop"), which is the only
	// human-readable part of a key and the one thing `kv recipients` can show
	// a person who is trying to work out whose key is whose.
	if data, err := os.ReadFile(expandHome(path) + ".pub"); err == nil {
		if line := firstMeaningfulLine(string(data)); sameKey(line, spec) {
			spec = line
		}
	}
	return []identity{{age: ageID, spec: spec}}, nil
}

// isLocked reports whether a key failed to parse only because it is
// passphrase-protected.
func isLocked(err error) bool {
	var locked *ssh.PassphraseMissingError
	return errors.As(err, &locked)
}

// keyPassphraseTries is how many attempts a person gets at a mistyped
// passphrase before we stop asking, the same count ssh itself allows.
const keyPassphraseTries = 3

// keyPassphrases caches what unlocked a key, by path. A single command often
// both reads and rewrites the store, and being asked for the same passphrase
// twice in a row reads as a failure rather than a step. It is keyed by path
// and kept apart from the store passphrase: they are different secrets, and
// silently trying one as the other would produce a confusing failure.
var keyPassphrases = map[string]string{}

// unlockSSHKey decrypts a passphrase-protected private key, asking for the
// passphrase when there is somebody there to ask.
//
// This is what ssh-agent cannot do for us. An agent signs; it never decrypts,
// and it will not hand out the key material age needs to derive a shared
// secret — age's own README says "ssh-agent is not supported". So having the
// key loaded in your agent does not help here, and the honest response to a
// locked key is the one ssh gives: ask.
//
// Every other surface gets the coded error instead. An MCP server, a pipe and
// a completion keystroke have nobody at the other end, and a process blocked
// forever on a read nobody will answer is worse than a refusal you can act on.
func unlockSSHKey(req plugin.Request, path string, data []byte) (any, *view.Error) {
	try := func(pass string) (any, bool) {
		if pass == "" {
			return nil, false
		}
		raw, err := ssh.ParseRawPrivateKeyWithPassphrase(data, []byte(pass))
		return raw, err == nil
	}
	supplied := lookupPassphrase(req)
	if raw, ok := try(supplied); ok {
		return raw, nil
	}
	if raw, ok := try(keyPassphrases[path]); ok {
		return raw, nil
	}
	// A supplied passphrase that does not fit the key is not the end of it:
	// there is one --passphrase and two things it could be unlocking, and
	// migrating a passphrase store to a key needs both at once. The prompt
	// names the file, so which one is being asked for is never in doubt.
	if canPrompt(req) {
		for range keyPassphraseTries {
			pass, err := promptKeyPassphrase(path)
			if err != nil || pass == "" {
				break
			}
			if raw, ok := try(pass); ok {
				keyPassphrases[path] = pass
				return raw, nil
			}
			fmt.Fprintln(os.Stderr, "Wrong passphrase.")
		}
		return nil, view.Errorf("kv.identity.locked", "could not unlock %s", path).
			WithHint("that is the key's own passphrase, not the store's — or use a key that needs none: rta kv init --generate")
	}
	if supplied != "" {
		return nil, view.Errorf("kv.identity.locked", "wrong passphrase for %s", path).
			WithHint("this is the passphrase of the key itself, not of the store")
	}
	return nil, view.Errorf("kv.identity.locked", "%s is passphrase-protected", path).
		WithHint(fmt.Sprintf(
			"ssh-agent cannot unlock it — an agent signs, it does not decrypt. "+
				"Set %s to the key's passphrase, or use a key that needs "+
				"none: rta kv init --generate", passphraseEnv))
}

// ageIdentityFromSSH mirrors agessh.ParseIdentity's type switch, but over an
// already-decrypted key (ParseRawPrivateKey returns inconsistent types).
func ageIdentityFromSSH(raw any) (age.Identity, error) {
	switch k := raw.(type) {
	case *ed25519.PrivateKey:
		return agessh.NewEd25519Identity(*k)
	case ed25519.PrivateKey:
		return agessh.NewEd25519Identity(k)
	case *rsa.PrivateKey:
		return agessh.NewRSAIdentity(k)
	}
	return nil, fmt.Errorf("unsupported SSH key type %T", raw)
}

// defaultIdentity is where `kv init --generate` puts the key it makes, and
// the last place identityPath looks.
//
// It lives beside the config rather than beside the store, so that a copy of
// the data directory — a backup, a synced folder, a container volume — is
// ciphertext and nothing else. Keeping the key next to what it opens is the
// mistake that makes an encrypted store an encrypted store in name only.
func defaultIdentity() string {
	return filepath.Join(filepath.Dir(config.Path()), "kv.identity")
}

// identityPath resolves the private key to unlock with: the flag first, then
// the environment, then the key this machine generated for itself.
//
// The env var is how an MCP server is handed a key-mode store's identity —
// the flag is Local, so it never arrives from a caller. The default file is
// what makes key mode usable without repeating yourself: set it up once,
// then `rta kv get x` needs no flags at all.
func identityPath(req plugin.Request) string {
	if p := req.String("identity"); p != "" {
		return p
	}
	if p := os.Getenv(identityEnv); p != "" {
		return p
	}
	if p := defaultIdentity(); fileExists(p) {
		return p
	}
	return ""
}

// suggestIdentities offers the private keys this machine already has: the one
// `kv init --generate` wrote, then whatever is in ~/.ssh. Filesystem paths
// complete on their own, but the answer is nearly always one of these, and it
// is a name people do not reliably remember.
//
// It reads directory entries and nothing else. Parsing a key would be enough
// to make a tab keystroke ask for a passphrase, which is the one thing a
// completion must never do.
// suggestRecipients completes the public half: who may read the store.
//
// The mirror image of suggestIdentities, and deliberately the same walk — the
// .pub siblings it skips are exactly the files this one wants. `kv rekey`
// decides who keeps access to everything already stored, which makes it the
// worst field in rta to fat-finger, and a public key is not a string anybody
// retypes correctly.
//
// Directory entries only, never a parse: the same rule suggestIdentities
// holds. Reading a key to decide whether to offer it would be work done on
// every keystroke, and being wrong about the contents is not a reason to hide
// a path somebody can see.
func suggestRecipients(_ context.Context, _ plugin.Request) []string {
	var out []string
	if p := defaultIdentity(); fileExists(p) {
		// Its own public half, which is what age derives from it — offered as
		// the identity path because that is what runInit and runRekey accept.
		out = append(out, p+"\tthe key kv init --generate made")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return out
	}
	for _, p := range sshkeys.PublicKeys(filepath.Join(home, ".ssh")) {
		out = append(out, p+"\tSSH public key")
	}
	return out
}

func suggestIdentities(_ context.Context, _ plugin.Request) []string {
	var out []string
	if p := defaultIdentity(); fileExists(p) {
		out = append(out, p+"\tthe key kv init --generate made")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return out
	}
	// The private half of a key pair, and not config, known_hosts or an
	// authorized_keys file — decided by the file's own PEM preamble rather
	// than by its name, so a key called anything but id_* is still offered.
	for _, p := range sshkeys.PrivateKeys(filepath.Join(home, ".ssh")) {
		out = append(out, p+"\tSSH private key")
	}
	return out
}

// lockedKey reports whether this key file would need a passphrase before it
// could decrypt anything. It is asked on behalf of somebody who is not here:
// `rta doctor` reporting what an MCP server inherits, and `kv status` saying
// whether this shell can open the store without a question.
func lockedKey(path string) bool {
	data, err := os.ReadFile(expandHome(path))
	if err != nil {
		return false // unreadable is a different problem, reported elsewhere
	}
	if strings.HasPrefix(firstMeaningfulLine(string(data)), "AGE-SECRET-KEY-") {
		return false
	}
	_, err = ssh.ParseRawPrivateKey(data)
	return isLocked(err)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// generateIdentity writes a fresh age key for this store and returns its path
// and public recipient.
//
// A dedicated key rather than your SSH login key, which is the whole point:
// if id_ed25519 both logs you into production and decrypts your secrets, one
// leaked file costs you both. This one can only ever open this store.
func generateIdentity(path string) (spec string, verr *view.Error) {
	if fileExists(path) {
		return "", view.Errorf("kv.identity.exists", "%s already exists", path).
			WithHint("name that key instead of making another — `kv init --identity " + path +
				"`, or `kv rekey --recipient " + path + "` — or move it aside first")
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", view.Errorf("kv.identity.generate", "generating a key: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", view.Errorf("kv.identity.generate", "creating %s: %v", filepath.Dir(path), err)
	}
	body := "# age identity for rta kv. Whoever holds this file can read the store.\n" +
		"# Back it up: losing it loses every secret in the store.\n" +
		id.String() + "\n"
	// O_EXCL: never clobber a key, even in the race between the check above
	// and this write. A silently replaced key is a store nobody can open.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", view.Errorf("kv.identity.generate", "writing %s: %v", path, err)
	}
	_, werr := f.WriteString(body)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		return "", view.Errorf("kv.identity.generate", "writing %s: %v", path, firstErr(werr, cerr))
	}
	return id.Recipient().String(), nil
}

func firstErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// scryptWorkFactor overrides age's default passphrase hardening. Zero keeps
// age's own calibration (~1s per unlock), which is the right cost for a
// secret store and the only value shipped. Tests lower it: they encrypt and
// decrypt hundreds of times, and what they are checking is never the KDF.
var scryptWorkFactor = 0

// keyMode names how the store is currently locked.
type keyMode string

const (
	modePassphrase keyMode = "passphrase"
	modeKeys       keyMode = "keys"
)

// currentMode reports how the store on disk is locked right now.
func currentMode() (keyMode, []string, *view.Error) {
	specs, verr := loadRecipients()
	if verr != nil {
		return "", nil, verr
	}
	if len(specs) > 0 {
		return modeKeys, specs, nil
	}
	return modePassphrase, nil, nil
}

// readKeys resolves what can decrypt the store as it stands *right now*.
//
// The recipients file decides, not the flags: during a migration to key mode
// the caller supplies both a passphrase and an identity, and the store on
// disk is still the passphrase-encrypted one. Choosing on the presence of
// --identity would try the new key against the old file and fail.
//
// ciphertext is the store's own bytes, needed for the one case below where
// what to trust cannot be decided from currentMode's say-so alone.
func readKeys(req plugin.Request, ciphertext []byte) ([]age.Identity, *view.Error) {
	mode, _, verr := currentMode()
	if verr != nil {
		return nil, verr
	}
	if mode == modeKeys {
		path := identityPath(req)
		if path == "" {
			// currentMode decided this from kv.recipients, which is exactly
			// as writable as the rest of this file's own comments already
			// say it is: by anyone who can write to the data directory
			// without ever holding a key or a passphrase. Every other
			// attack that capability enables here is about widening who
			// can read the store; refusing outright at this point would
			// let it do the opposite — deny the legitimate passphrase
			// holder, on an unchanged, still passphrase-encrypted store,
			// with the officially-hinted recovery (`kv rekey`) blocked by
			// this identical check before its own logic ever runs.
			//
			// An explicit passphrase the caller supplied gets a real trial
			// against the actual ciphertext before refusing — not merely
			// constructed and trusted, which would accept literally any
			// string: a scrypt identity always builds successfully
			// regardless of whether the passphrase is right, so that check
			// alone would silently turn "wrong passphrase against a
			// genuinely key-mode store" into the same misleading answer
			// this fix exists to avoid, just in the other direction. Only a
			// passphrase that actually opens *this* ciphertext, right now,
			// is trusted instead of what kv.recipients claims; anything
			// else — including an ordinary wrong passphrase after a real
			// rekey — falls through to the refusal below exactly as before.
			if passphrase := lookupPassphrase(req); passphrase != "" {
				if id, err := age.NewScryptIdentity(passphrase); err == nil {
					if _, decErr := age.Decrypt(bytes.NewReader(ciphertext), id); decErr == nil {
						return []age.Identity{id}, nil
					}
				}
			}
			return nil, view.Errorf("kv.identity.required", "this store is encrypted to keys, not a passphrase").
				WithHint("pass --identity <your private key>, e.g. --identity ~/.ssh/id_ed25519 — `rta kv recipients` lists who can read it")
		}
		// The passphrase is optional here: it only matters if the key file
		// itself is locked, so a missing one is not an error yet.
		ids, verr := parseIdentities(req, path)
		if verr != nil {
			return nil, verr
		}
		out := make([]age.Identity, len(ids))
		for i, id := range ids {
			out[i] = id.age
		}
		return out, nil
	}
	passphrase, verr := resolvePassphrase(req)
	if verr != nil {
		return nil, verr
	}
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, view.Errorf("kv.passphrase.invalid", "%v", err)
	}
	return []age.Identity{id}, nil
}

// writeKeys resolves what the store should be encrypted to after this write.
//
// The recipients file decides, not the flags. An ordinary write re-encrypts to
// exactly the keys already recorded, so `kv set` cannot change who can read
// the store — not even by naming a different identity, which used to quietly
// add that key to the readers. Changing the lock is `kv rekey`, and it is
// destructive for the reason this is not.
//
// The first write is the exception, because there is nothing yet to change:
// naming a key when no store exists is how somebody says "lock it with this",
// and asking them for a passphrase in reply asks for the very thing they were
// avoiding.
//
// embedded is the store's own record of who it was last encrypted to
// (store.Recipients, from the ciphertext just decrypted) — the one thing
// this function trusts more than kv.recipients itself. current is the
// canonical set this write commits to, for the caller to embed back into
// the store it is about to write, so the next write has something to check.
func writeKeys(req plugin.Request, embedded []string) (recipients []age.Recipient, specs, current []string, verr *view.Error) {
	mode, stored, verr := currentMode()
	if verr != nil {
		return nil, nil, nil, verr
	}
	if mode == modeKeys {
		// **A recipients file with no store behind it is not a lock anybody
		// chose.** Nothing rta does produces that state: saveTo writes the
		// recipients only once the ciphertext is on disk, and kv.init refuses
		// outright when a recipients file is already there. So the pairing
		// exists exactly one way — somebody wrote the file directly.
		//
		// It mattered because the mismatch guard below cannot fire on a first
		// write: there is no ciphertext yet, so `embedded` is nil and there is
		// nothing to compare. An operator who had never run `kv init` — the
		// path kv's own doc calls the one that needs no setup — would have
		// their first `kv set` create a store encrypted to a planted key and
		// to nothing else, locking them out of their own secret while handing
		// it to whoever planted it. Refusing costs a legitimate operator
		// nothing, because no legitimate operator is ever in this state.
		if !fileExists(storePath()) {
			return nil, nil, nil, view.Errorf("kv.recipients.orphan",
				"%s lists keys but there is no store for them to unlock", recipientsPath()).
				WithHint("rta never writes that file on its own — it is written with the store, " +
					"never before it. Check whether those keys are yours (`rta kv recipients`); " +
					"if they are not, delete the file and start over with `rta kv init`")
		}
		// kv.recipients has to be plaintext — the point of it is answering
		// "who can read this?" without unlocking anything — which also means
		// it is a file with no cryptographic tie to the store at all,
		// writable by anyone who can write to the data directory without
		// ever holding a key. embedded required a successful decrypt to
		// produce; kv.recipients did not. Trusting the file over the
		// ciphertext's own record is how a plaintext edit — no key, no
		// decrypt, nothing an audit of "who can open this" would catch —
		// turned an ordinary `kv set` into re-encrypting every existing
		// secret to an attacker's key, silently, on a write that named none
		// of them. embedded is empty for a store written before this field
		// existed: nothing to check yet, and this write starts the record.
		if embedded != nil && !equal(stored, embedded) {
			return nil, nil, nil, view.Errorf("kv.recipients.mismatch",
				"kv.recipients does not match who the store is actually encrypted to").
				WithHint("it may have been edited by hand or by something other than `kv rekey` — " +
					"compare `rta kv recipients` against what you expect, then `rta kv rekey --only " +
					"--recipient <the ones it should be>` to reconcile before writing again")
		}
		recipients, verr = recipientsFor(stored)
		return recipients, nil, stored, verr
	}

	identity := identityPath(req)
	if identity == "" || fileExists(storePath()) {
		passphrase, verr := resolvePassphrase(req)
		if verr != nil {
			return nil, nil, nil, verr
		}
		r, err := age.NewScryptRecipient(passphrase)
		if err != nil {
			return nil, nil, nil, view.Errorf("kv.passphrase.invalid", "%v", err)
		}
		if scryptWorkFactor > 0 {
			r.SetWorkFactor(scryptWorkFactor)
		}
		return []age.Recipient{r}, nil, nil, nil
	}

	// First write, with a key named: lock it to that key, plus anyone else
	// this call names. The identity's own public half is non-negotiable — it
	// is what makes the writer one of the readers.
	var want []string
	for _, spec := range req.StringSlice("recipient") {
		_, canonical, err := parseRecipient(spec)
		if err != nil {
			return nil, nil, nil, view.Errorf("kv.recipient.invalid", "%v", err).
				WithHint("--recipient takes an age recipient, an SSH public key, or a path to one")
		}
		want = mergeSpec(want, canonical)
	}
	ids, verr := parseIdentities(req, identity)
	if verr != nil {
		return nil, nil, nil, verr
	}
	// Every key in a multi-key identity file becomes a reader, not just the
	// first — the operator holds all of them, so all of them belong among
	// the keys this write cannot lock itself out from.
	for _, id := range ids {
		want = mergeSpec(want, id.spec)
	}
	recipients, verr = recipientsFor(want)
	return recipients, want, want, verr
}

// mergeSpec adds a recipient unless the same key is already in the set.
func mergeSpec(want []string, spec string) []string {
	for _, existing := range want {
		if sameKey(existing, spec) {
			return want
		}
	}
	return append(want, spec)
}

// recipientsFor turns recorded specs into the recipients to encrypt to.
func recipientsFor(specs []string) ([]age.Recipient, *view.Error) {
	var out []age.Recipient
	for _, spec := range specs {
		r, _, err := parseRecipient(spec)
		if err != nil {
			return nil, view.Errorf("kv.recipient.invalid", "recorded recipient %q: %v", redactedSpec(spec), err)
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, view.Errorf("kv.recipient.none", "no recipients to encrypt to")
	}
	return out, nil
}

// refuseSilentIdentity stops a write from quietly ignoring --identity.
//
// On a passphrase store the flag has nothing to unlock, and it used to be
// dropped on the floor: the store stayed passphrase-locked and nothing said
// so. It is checked before the store is read, so nobody types a passphrase to
// be told afterwards that the flag they gave does nothing.
func refuseSilentIdentity(req plugin.Request) *view.Error {
	if req.String("identity") == "" || !fileExists(storePath()) {
		return nil
	}
	mode, _, verr := currentMode()
	if verr != nil || mode == modeKeys {
		return verr
	}
	return view.Errorf("kv.identity.wrongmode", "this store is locked with a passphrase, not keys").
		WithHint("--identity cannot open it, and writing does not change the lock: rta kv rekey --only --identity <that key>")
}

// privateKeyFile reports whether a spec is a path to a private key on this
// machine — the file itself, not a public half of it.
func privateKeyFile(spec string) bool {
	data, err := os.ReadFile(expandHome(strings.TrimSpace(spec)))
	return err == nil && isPrivateKey(firstMeaningfulLine(string(data)))
}

// heldHere lists the public halves of the private keys this machine can
// actually use right now.
//
// It is what the lockout guard is checked against: a recipient set is only
// safe to commit to if something in it opens with a key that is here. Naming
// somebody else's key and nothing of your own hands them the store and takes
// it from you in the same keystroke.
func heldHere(req plugin.Request, extra ...string) []string {
	held := append([]string{}, extra...)
	if p := identityPath(req); p != "" {
		// A failure here is not this function's business: it means the key
		// cannot be used, which is exactly what "not held" means.
		if ids, verr := parseIdentities(req, p); verr == nil {
			for _, id := range ids {
				if id.spec != "" {
					held = append(held, id.spec)
				}
			}
		}
	}
	return held
}

// canRead reports whether any key in want is one of the keys held here.
func canRead(want, held []string) bool {
	for _, spec := range want {
		for _, mine := range held {
			if sameKey(spec, mine) {
				return true
			}
		}
	}
	return false
}

// sameKey compares two recipient specs by the key material alone. An
// authorized-keys line carries a trailing comment ("me@laptop") that is
// documentation, not identity: --recipient ~/.ssh/id_ed25519.pub and the
// public half derived from --identity are the same key wearing different
// labels, and recording both would encrypt to one person twice.
func sameKey(a, b string) bool {
	keyOf := func(spec string) string {
		if fields := strings.Fields(spec); len(fields) >= 2 {
			return fields[0] + " " + fields[1]
		}
		return spec
	}
	return keyOf(a) == keyOf(b)
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}

func firstMeaningfulLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	return ""
}

// meaningfulLines is firstMeaningfulLine's plural: every non-blank,
// non-comment line, in file order.
func meaningfulLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}
