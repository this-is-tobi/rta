package kv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"golang.org/x/term"

	"github.com/this-is-tobi/rta/builtin/internal/itemstore"
	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/filelock"
	"github.com/this-is-tobi/rta/internal/stdio"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The store on disk: one encrypted file, loaded whole and saved atomically
// under a lock, and every way a passphrase or identity reaches the unlock.

// kinds are what a stored value can be. They are a label for humans and a
// filter for kv.list, never a change in how the value is handled.
var kinds = []string{"string", "json", "certificate", "private-key", "public-key", "ssh-key", "file"}

// entry is one stored secret and what we know about it.
type entry struct {
	// Value is []byte, not string: encoding/json base64-encodes a []byte and
	// gets every byte back. A string field does not — encoding/json replaces
	// any byte sequence that is not valid UTF-8 with U+FFFD, which silently
	// destroyed anything binary (a DER certificate, a PKCS#12 bundle, a JKS
	// keystore) the moment it was stored, with no error at set, get, list or
	// show. There is nothing to detect after the fact: the damage happens
	// before encryption, so the original bytes are gone from the store.
	Value       []byte `json:"value"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Filename    string `json:"filename,omitempty"` // set when the value came from disk
	// Origin is how this entry came to exist: "typed", "agent",
	// "file:<name>", "profile:<name>".
	//
	// Separate from Filename because they answer different questions and only
	// one of them was ever being asked. Filename is an input to detectKind on
	// edit — a ".pem" is a certificate whatever the bytes look like — and it
	// is empty for anything not read off disk, which is most of the store. It
	// was also what the "Source" column printed, so that column was blank for
	// every secret somebody typed or a profile form created, and their real
	// provenance lived in the description or nowhere.
	//
	// It is worth recording rather than inferring because one of its values
	// cannot be inferred later at all: "agent" says an MCP caller wrote this,
	// which is exactly the fact an operator auditing their own store wants and
	// the one nothing else in the entry preserves.
	//
	// Empty on every entry written before this field existed; the surfaces
	// fall back to Filename there rather than claiming to know.
	Origin  string    `json:"origin,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	// Previous is what this key held before each write over it, most recent
	// first, capped at maxRevisions (history.go). Inside the same encrypted
	// document as the live value, so keeping a past is no more exposure than
	// keeping a present.
	Previous []revision `json:"previous,omitempty"`
}

// Origin reports where an entry came from, for the surfaces.
//
// The fallback is what keeps an existing store honest: before Origin existed
// the only provenance recorded was a filename, so an entry that has one came
// from a file and an entry that has neither says nothing rather than guessing.
func (e entry) origin() string {
	if e.Origin != "" {
		return e.Origin
	}
	if e.Filename != "" {
		return "file:" + e.Filename
	}
	return ""
}

// store is the plaintext shape, only ever held in memory — on disk it exists
// solely as age-encrypted bytes.
type store struct {
	// Version identifies the on-disk format. Absent means "written before
	// this field existed", which is the one case decodeStore has to work out
	// for itself (legacy.go).
	Version int              `json:"version,omitempty"`
	Entries map[string]entry `json:"entries"`
	// Recipients is the store's own record of who it was last encrypted to —
	// written every time the store is, inside the ciphertext. kv.recipients
	// (crypt.go) holds the same information in plaintext beside the store,
	// because who can read it has to be answerable without unlocking
	// anything — but that also means it is a file with no cryptographic
	// relationship to the store at all, editable by anyone who can write to
	// the data directory without ever holding a key. This copy is what an
	// ordinary write checks kv.recipients against before trusting it:
	// producing THIS value required successfully decrypting the store,
	// which only someone holding a real key can do. Empty on a store
	// written before this field existed — nothing to check yet, and the
	// next ordinary write starts one.
	Recipients []string `json:"recipients,omitempty"`
	// Removed is what `kv rm` set aside: one entry per name, whole, until
	// `kv restore` brings it back or `kv rm --purge` destroys it. Beside
	// Entries rather than a flag on an entry, so that every read of the
	// store keeps treating Entries as the store and nothing has to remember
	// to skip a tombstone.
	Removed map[string]removedEntry `json:"removed,omitempty"`
}

func storePath() string { return filepath.Join(itemstore.DataDir(), storeFile) }

// lockStore serializes kv's own read-modify-write writes (set, rename,
// remove, rekey) across processes and goroutines, the same way
// internal/grant locks grants.json — via the same internal/filelock
// mechanism grant's own lock was extracted into.
//
// Every one of those writes decrypts the whole store, mutates one thing in
// memory, and re-encrypts and writes the whole thing back, with nothing
// between the load and the save stopping a second writer from doing the
// same and one of them losing its change with no error on either side. The
// MCP bridge dispatches every tools/call in its own goroutine, so two calls
// each doing their own load..save is the ordinary case for an agent
// pipelining requests, not an exotic one — "the window is only
// milliseconds" is not a defense against that.
//
// kv.edit is deliberately not wrapped in this: it holds an external editor
// open for as long as somebody is looking at it, and serializing every
// other write behind that would be a worse regression than the race it
// already narrows for itself by re-reading right before its own save (see
// edit.go) — though that final re-read-and-save step does take this lock
// too, closing the residual window that re-read alone left.
func lockStore() (release func(), verr *view.Error) {
	path := filepath.Join(itemstore.DataDir(), "kv.lock")
	release, err := filelock.Acquire(path, filelock.DefaultStale, filelock.DefaultRetry, filelock.DefaultTimeout)
	if err != nil {
		return nil, view.Errorf("kv.store.lock", "acquiring the store lock: %v", err)
	}
	return release, nil
}

// detectKind reads the value well enough to label it. It looks only at
// structure, never at content: nothing here is logged or shown.
func detectKind(value, filename string) string {
	trimmed := strings.TrimSpace(value)
	switch {
	case strings.Contains(trimmed, "-----BEGIN CERTIFICATE-----"):
		return "certificate"
	case strings.Contains(trimmed, "PRIVATE KEY-----"):
		return "private-key"
	case strings.Contains(trimmed, "PUBLIC KEY-----"):
		return "public-key"
	case strings.HasPrefix(trimmed, "ssh-") || strings.HasPrefix(trimmed, "ecdsa-sha2-"):
		return "ssh-key"
	case (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed)):
		return "json"
	case filename != "":
		return "file"
	default:
		return "string"
	}
}

// lookupPassphrase returns a passphrase if one was supplied, without
// prompting or failing — for the places where it is optional.
func lookupPassphrase(req plugin.Request) string {
	if p := req.String("passphrase"); p != "" {
		return p
	}
	return os.Getenv(passphraseEnv)
}

// promptPassphrase is overridable in tests; it reads from the terminal.
var promptPassphrase = func() (string, error) {
	fmt.Fprint(os.Stderr, "Passphrase: ")
	// The prompt goes to stderr, never stdout: `eval "$(rta kv env x)"` must
	// not eval it, and a redirected `kv get > file` must not contain it.
	secret, err := term.ReadPassword(int(stdio.Real().Fd()))
	fmt.Fprintln(os.Stderr)
	return string(secret), err
}

// promptKeyPassphrase asks for a private key's own passphrase, naming the file
// so it is clear which secret is wanted: the key's, not the store's. Also
// overridable in tests.
var promptKeyPassphrase = func(path string) (string, error) {
	fmt.Fprintf(os.Stderr, "Passphrase for %s: ", path)
	secret, err := term.ReadPassword(int(stdio.Real().Fd()))
	fmt.Fprintln(os.Stderr)
	return string(secret), err
}

// canPrompt reports whether this request can reach a person at a terminal.
// Only a CLI request can: MCP has no terminal at the other end, and the TUI
// owns the screen (it asks with a masked form field instead).
var canPrompt = func(req plugin.Request) bool {
	// stdio.Real, not os.Stdin: after main takes fd 0 away from the children
	// os.Stdin is /dev/null, which is not a terminal — so this would answer
	// "no person here" on every CLI run and the passphrase prompt would never
	// appear.
	return req.Surface() == plugin.SurfaceCLI && term.IsTerminal(int(stdio.Real().Fd()))
}

// prompted caches what the terminal prompt returned. A single command often
// both reads and rewrites the store (kv set, kv rm), and being asked for the
// same passphrase twice in a row reads as a failure. The cache lives as long
// as the process, which for the CLI is exactly one command.
var prompted string

// resolvePassphrase prefers the explicit flag, then the environment
// variable, then a terminal prompt — so the recommended path (env var) never
// reaches a CLI argument, and the interactive path never needs one either.
// Surfaces that cannot prompt get the coded error: a refusal they can act
// on, rather than a process blocked forever on a read nobody will answer.
func resolvePassphrase(req plugin.Request) (string, *view.Error) {
	if p := lookupPassphrase(req); p != "" {
		return p, nil
	}
	if prompted != "" {
		return prompted, nil
	}
	if canPrompt(req) {
		p, err := promptPassphrase()
		if err == nil && p != "" {
			prompted = p
			return p, nil
		}
	}
	return "", view.Errorf("kv.passphrase.missing", "no passphrase provided").
		WithHint(fmt.Sprintf("set %s or pass --passphrase", passphraseEnv))
}

// load decrypts the store. A missing file is an empty store — first use
// needs no setup step. A decrypt failure (wrong key or a corrupted file —
// age cannot tell them apart, and neither can we) is one coded error.
func load(req plugin.Request) (store, *view.Error) {
	data, err := os.ReadFile(storePath())
	if os.IsNotExist(err) {
		return store{Entries: map[string]entry{}}, nil
	}
	if err != nil {
		return store{}, view.Errorf("kv.store.unreadable", "reading %s: %v", storePath(), err)
	}
	identities, verr := readKeys(req, data)
	if verr != nil {
		return store{}, verr
	}
	r, err := age.Decrypt(bytes.NewReader(data), identities...)
	if err != nil {
		return store{}, wrongKey(req)
	}
	plaintext, err := io.ReadAll(r)
	if err != nil {
		return store{}, wrongKey(req)
	}
	s, verr := decodeStore(plaintext)
	if verr != nil {
		return store{}, verr
	}
	if s.Entries == nil {
		s.Entries = map[string]entry{}
	}
	return s, nil
}

// wrongKey names the failure in terms of whatever the caller actually tried.
func wrongKey(req plugin.Request) *view.Error {
	if identityPath(req) != "" {
		return view.Errorf("kv.wrongkey", "that key cannot decrypt the store").
			WithHint("`rta kv recipients` lists the public keys it was encrypted to")
	}
	return view.Errorf("kv.wrongpass", "could not decrypt the store").
		WithHint("wrong passphrase, or the store file is corrupt")
}

// save encrypts and writes the store atomically: tmp file + rename, same
// discipline as the plaintext itemstore, so a crash mid-write can never
// leave a half-written store behind.
func save(req plugin.Request, s store) *view.Error {
	recipients, specs, current, verr := writeKeys(req, s.Recipients)
	if verr != nil {
		return verr
	}
	// Keep the embedded record in step with whatever this write actually
	// commits to, so the next write has something trustworthy to check
	// kv.recipients against.
	s.Recipients = current
	return saveTo(s, recipients, specs)
}

// saveTo is save with the recipients already decided — the re-key path, where
// the new set is computed rather than derived from the flags of a write.
// specs nil means the recorded set is unchanged.
func saveTo(s store, recipients []age.Recipient, specs []string) *view.Error {
	// Every write stamps the current format, so a store only ever has to be
	// guessed at once — the write that follows the guess settles it. That is
	// also what makes this write the point of no return for a store whose
	// format was inferred, so the original is kept aside first.
	if needsBackup(s.Version) {
		backupUnstamped()
	}
	s.Version = storeVersion
	plaintext, err := json.Marshal(s)
	if err != nil {
		return view.Errorf("kv.store.encode", "encoding store: %v", err)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipients...)
	if err != nil {
		return view.Errorf("kv.store.encrypt", "encrypting store: %v", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return view.Errorf("kv.store.encrypt", "encrypting store: %v", err)
	}
	if err := w.Close(); err != nil {
		return view.Errorf("kv.store.encrypt", "finalizing store: %v", err)
	}
	if verr := writeAtomic(buf.Bytes()); verr != nil {
		return verr
	}
	// Only once the store is safely on disk in the new form: otherwise a
	// failed write would leave a recipients file describing a store that
	// nothing can open.
	if specs != nil {
		if verr := saveRecipients(specs); verr != nil {
			// The two writes are not atomic together, only each on its own —
			// so a failure here is not "nothing happened": the ciphertext
			// above already committed to the new recipient set, and only the
			// plaintext record of it failed to update. `rta kv recipients`
			// itself does not detect this (it reads the plaintext file, not
			// the ciphertext's own embedded record), so the hint has to say
			// so explicitly rather than leave an operator trusting a stale
			// answer to "who can decrypt this". Found by review.
			return verr.WithHint("the store WAS re-encrypted to the new key set; only recording that " +
				"failed — `rta kv recipients` may now be stale until the next successful write. " +
				"Retry, or `rta kv rekey --only --recipient <the set it should be>` to reconcile")
		}
	}
	return nil
}

func writeAtomic(data []byte) *view.Error {
	dir := itemstore.DataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return view.Errorf("kv.store.mkdir", "creating %s: %v", dir, err)
	}
	// 0600, applied before the file exists under its real name: the
	// ciphertext is age-encrypted, but a store nobody else can open is still
	// a store nobody else needs to copy.
	if err := atomicfile.Write(storePath(), data, 0o600); err != nil {
		return view.Errorf("kv.store.write", "writing store: %v", err)
	}
	return nil
}

func notFound(key string) *view.Error {
	return view.Errorf("kv.notfound", "no key %q", key).
		WithHint("run `rta kv list` to see every key")
}

// Unlockable reports whether this environment alone can open the store: no
// flags, no prompt, nothing typed.
//
// It is the question `rta doctor` asks on behalf of AI agents. An MCP server
// inherits the environment it was launched from, so if the answer is yes, the
// agent's reach over the store is bounded by grants alone — and if key
// material is sitting in a file rather than in the environment, by nothing at
// all once the agent can read files.
func Unlockable() (bool, string) {
	if !fileExists(storePath()) {
		return false, "no store"
	}
	mode, _, verr := currentMode()
	if verr != nil {
		return false, "unreadable"
	}
	if mode == modeKeys {
		// A passphrase-protected key is not key material an inherited
		// environment can use: nobody is there to type its passphrase, so the
		// honest answer is no — unless the passphrase was inherited too.
		usable := func(p string) bool { return !lockedKey(p) || os.Getenv(passphraseEnv) != "" }
		// A set identityEnv wins unconditionally here, the same way
		// identityPath resolves it for a real decrypt — it is never a
		// fallback-if-usable check, so an existence guard cannot fall
		// through to defaultIdentity() either; a real kv.get with the same
		// environment would fail outright on a bad path, not quietly try
		// another key.
		//
		// The guard itself: lockedKey treats a file it cannot read —
		// missing, permission-denied, a stale path — as "not locked", since
		// its own job is only to distinguish locked from unlocked among
		// files that exist. Without it, a stale or typo'd RTA_KV_IDENTITY
		// read as "usable", which is what `rta doctor` uses this for —
		// telling an operator whether an MCP agent's inherited environment
		// can decrypt the store unattended — even though a real kv.get
		// against the same environment would fail with
		// kv.identity.unreadable. Found by review; mirrors
		// the guard LockedIdentity, two functions below, already had.
		if p := os.Getenv(identityEnv); p != "" {
			if !fileExists(expandHome(p)) {
				return false, identityEnv
			}
			return usable(p), identityEnv
		}
		if p := defaultIdentity(); fileExists(p) {
			return usable(p), p
		}
		return false, ""
	}
	if os.Getenv(passphraseEnv) != "" {
		return true, passphraseEnv
	}
	return false, ""
}

// LockedIdentity names a key that would open the store but is itself
// passphrase-protected.
//
// It is the difference between "there is no key here" and "there is a key here
// that nobody unattended can use", which read the same to Unlockable and not
// at all the same to somebody deciding what an agent can reach. A passphrase
// in the environment removes the distinction, because then it can be used.
func LockedIdentity() string {
	if !fileExists(storePath()) || os.Getenv(passphraseEnv) != "" {
		return ""
	}
	if mode, _, verr := currentMode(); verr != nil || mode != modeKeys {
		return ""
	}
	for _, p := range []string{os.Getenv(identityEnv), defaultIdentity()} {
		if p != "" && fileExists(expandHome(p)) && lockedKey(p) {
			return p
		}
	}
	return ""
}
