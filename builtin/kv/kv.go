// Package kv is the built-in encrypted local store: the place for the
// tokens, passwords, certificates and keys that otherwise end up in shell
// history, a dotfile, or a chat message. The whole store is one file,
// encrypted with filippo.io/age — under a passphrase by default, or to a set
// of age/SSH public keys when you would rather use a key you already own
// (see crypt.go for why gpg keys are deliberately not among them).
//
// Values are not only strings. A secret is often a file — a certificate, a
// private key, a kubeconfig — so kv.set reads one with --file and kv.get
// writes it back with --out, and the store records what kind of thing each
// value is. Each entry also carries a description, because six months later
// "api-token-2" tells you nothing about which API.
//
// # On safety classes
//
// kv.get, kv.copy, kv.edit, kv.env, kv.set, kv.rename and kv.init are Write;
// kv.list, kv.show, kv.recipients and kv.status are Read; kv.rm and kv.rekey
// are Destructive.
//
// Classifying kv.get as Write is deliberate and is the one place the safety
// model is read as "blast radius" rather than "does it mutate". It has no
// side effects, so the letter of the model would make it Read — exposed to
// any MCP client by default. But its whole purpose is revealing a secret,
// and from an AI-safety standpoint "read" there means "leak". Write makes an
// operator's --allow-write a precondition; a grant (internal/grant) then
// makes it per-key and time-boxed. kv.list stays Read because it is genuinely safe:
// it returns names, kinds, sizes and descriptions, never a value nor a
// preview of one.
//
// kv.copy and kv.edit are the same act reached by another route — a value on
// the clipboard, or in an editor's buffer, has been revealed — so they carry
// kv.get's classification exactly rather than a cheaper one earned by not
// printing anything. Both also refuse the surfaces they cannot honestly
// serve: the clipboard and the terminal belong to whoever is sitting at the
// machine, which over MCP is nobody.
//
// kv.set stays Write and takes a grant on top, which is that argument run
// backwards: overwriting a key destroys the secret that was in it exactly as
// kv.rm does, and kv.rm is Destructive. Losing a token and revealing one are
// the same size of mistake, so they ask a person the same question.
//
// kv.rekey is Destructive for the same reason read the other way: it can take
// away every reader's access to everything at once, which is blast radius
// whatever the word "destructive" suggests about deleting.
package kv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
	"golang.org/x/term"

	"github.com/this-is-tobi/rule-them-all/builtin/internal/itemstore"
	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
	"github.com/this-is-tobi/rule-them-all/internal/filelock"
	"github.com/this-is-tobi/rule-them-all/internal/stdio"
	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

const (
	storeFile     = "kv.age"
	passphraseEnv = "RTA_KV_PASSPHRASE"
	identityEnv   = "RTA_KV_IDENTITY"
)

// Key material fields, shared by every capability that touches the store.
// Both are Local: they unlock the store rather than travel through it, so
// they never appear in an MCP tool schema and are stripped from anything an
// agent sends. An MCP server unlocks the store from its own environment,
// which is the operator's decision to make when they launch it — not a
// question to put in front of a model.
var (
	passphraseField = plugin.Field{
		Name: "passphrase", Type: plugin.Secret, Local: true, EnvFallback: true,
		Help: fmt.Sprintf("store passphrase (or set %s — preferred, keeps it out of shell history)", passphraseEnv),
	}
	identityField = plugin.Field{
		Name: "identity", Type: plugin.Path, Local: true, EnvFallback: true,
		Help: fmt.Sprintf("private key to unlock with, e.g. ~/.ssh/id_ed25519 (or set %s)", identityEnv),
		// The keys this machine has, offered before the filesystem is walked
		// for them: one of these is nearly always the answer.
		Suggest: suggestIdentities,
	}
)

// unlockFields are the inputs every store operation accepts.
func unlockFields(extra ...plugin.Field) []plugin.Field {
	return append(extra, passphraseField, identityField)
}

// Plugin returns the kv plugin declaration.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "kv",
		Summary: "Encrypted local store for secrets, certificates and key files",
		Capabilities: []plugin.Capability{
			{
				ID: "kv.list", Summary: "List stored keys (names and metadata only, never values)",
				Safety: plugin.Read, Idempotent: true,
				Detailed: true,
				Description: "Never returns a value or a preview of one — only key names, what kind of " +
					"thing each is, its size, its description and when it changed. With --detail: " +
					"the source filename of anything stored from disk.\n\n" +
					"--match is the \"which one was it called?\" filter: a substring of the name or " +
					"the description, case-insensitive, so `kv list --match aws` finds " +
					"`prod-deploy-key` when the description is the only place the word AWS appears.",
				Inputs: unlockFields([]plugin.Field{
					{Name: "kind", Type: plugin.String, Options: kinds,
						Help: "only entries of this kind"},
					{Name: "match", Type: plugin.String,
						Help: "only keys whose name or description contains this (case-insensitive)"},
				}...),
				Run: runList,
			},
			{
				ID: "kv.get", Summary: "Reveal a stored value", Safety: plugin.Write, Idempotent: true,
				NeedsGrant: true, Scope: "key",
				Description: "Writes the value to stdout with no quoting and no framing, so " +
					"`rta kv get gh-token | gh auth login --with-token` is a pipe and not a " +
					"transformation. The pretty renderer terminates the line, so a value stored " +
					"without a trailing newline gains one on the way out; for the byte-exact copy " +
					"use --out, which writes the stored bytes to a file with 0600 instead, and is " +
					"how a certificate or key goes back to disk without passing through your " +
					"scrollback. To use a secret without seeing it at all, `kv copy` puts it on the " +
					"clipboard and prints nothing.\n\n" +
					"Classified as a write because revealing a secret is the sensitive act. An MCP " +
					"agent therefore needs --allow-write, and on top of that a per-key grant issued " +
					"by a person (`rta grant allow kv.get <key> --ttl 15m`). --out is a person's flag " +
					"only, since a grant authorizes revealing a value, not choosing where on this " +
					"machine it gets written; an MCP caller always gets the value back in the response.",
				Inputs: unlockFields([]plugin.Field{
					{Name: "key", Type: plugin.String, Positional: true, Required: true, Help: "key to reveal",
						Suggest: suggestKeys},
					// Local, like the passphrase and identity above: --out
					// names a path on *this* machine, and a grant on kv.get
					// authorizes revealing one value, not letting the caller
					// choose which of this host's files gets overwritten with
					// it. Without this, the grant model's own per-key scoping
					// was cosmetic — "reveal db-password" was reachable as
					// "overwrite ~/.bashrc with db-password's contents",
					// destroying whatever was there first, no confirmation,
					// no undo. CLI and TUI keep --out exactly as before: there
					// is a person there, and it is their machine.
					{Name: "out", Type: plugin.Path, Local: true,
						Help: "write the value to this file (0600) instead of printing it"},
				}...),
				Run: runGet,
			},
			{
				ID: "kv.copy", Summary: "Copy a value to the clipboard without displaying it",
				Safety: plugin.Write, Idempotent: true,
				NeedsGrant: true, Scope: "key",
				Description: "The value goes to this machine's clipboard and nowhere else: not to the " +
					"screen, not into scrollback, not into shell history — because getting a secret " +
					"somewhere is nearly always a paste rather than a read, and the reading is the " +
					"part that leaves a copy behind.\n\n" +
					"Classified with `kv get`, not below it: a value on the clipboard has been " +
					"revealed, and every process running as you can read it. Printing nothing buys " +
					"a smaller audience, not a different act.\n\n" +
					"Refused over MCP however the grants read. The clipboard is not a return value — " +
					"it is a shared channel on somebody else's desk that the caller cannot read back, " +
					"so an agent gains nothing here it could not get from `kv get`, while gaining the " +
					"ability to silently replace the address you copied a moment ago.\n\n" +
					"Nothing clears the clipboard afterwards, and this says so rather than pretending " +
					"otherwise: a command that has printed its answer and exited cannot come back in " +
					"45 seconds to undo it.",
				Inputs: unlockFields([]plugin.Field{
					{Name: "key", Type: plugin.String, Positional: true, Required: true, Help: "key to copy",
						Suggest: suggestKeys},
				}...),
				Run: runCopy,
			},
			{
				ID: "kv.env", Summary: "Print stored values as shell exports", Safety: plugin.Write, Idempotent: true,
				NeedsGrant: true, Scope: "key",
				Description: "For `eval \"$(rta kv env db-password)\"` — loads secrets into a shell " +
					"session without ever writing them to a file. Key names become environment " +
					"names (db-password → DB_PASSWORD). --format dotenv writes .env syntax instead. " +
					"Same grant requirement as kv.get: this reveals values. Naming no key means " +
					"every key, which is a wider ask and needs a grant for kv.env itself rather " +
					"than one per key.",
				Inputs: unlockFields([]plugin.Field{
					{Name: "key", Type: plugin.StringSlice, Positional: true, Help: "keys to export (default: all)",
						Suggest: suggestKeys},
					{Name: "prefix", Type: plugin.String, Help: "prepend this to every variable name, e.g. APP_"},
					{Name: "format", Type: plugin.String, Default: "export", Options: []string{"export", "dotenv"},
						Help: "shell exports, or .env syntax"},
				}...),
				Run: runEnv,
			},
			{
				ID: "kv.set", Summary: "Set (or overwrite) a stored value", Safety: plugin.Write, Idempotent: true,
				NeedsGrant: true, Scope: "key",
				Description: "The value comes from the argument or from --file. The kind (certificate, " +
					"private key, json, file, string) is detected from the content unless --kind says " +
					"otherwise. Writing an entry never changes who can read the store: that is " +
					"`kv rekey`, which is destructive for the reason this is not.\n\n" +
					"Setting a key that already exists replaces the secret in it, and nothing keeps " +
					"the old one — no history, no backup, no undo. That is the same loss as `kv rm`, " +
					"which an agent may only reach with --allow-destructive and a per-key grant, so " +
					"this asks a person the same question rather than a cheaper one for the same " +
					"damage. CLI and TUI are unaffected: grants gate MCP calls, and at a terminal " +
					"the person is already there.",
				Inputs: unlockFields([]plugin.Field{
					{Name: "key", Type: plugin.String, Positional: true, Required: true, Help: "key to set",
						Suggest: suggestKeys},
					{Name: "value", Type: plugin.Secret, Positional: true, Help: "value to store"},
					// Local, like --out on kv.get and for the mirror-image
					// reason: --file names a path on *this* machine, and a
					// grant to write one key does not say which of the host's
					// files may be read into it. Without this, "store the
					// staging token" was reachable as "store ~/.aws/credentials
					// under the name staging-token", the agent picking the path
					// and kv.set's own answer confirming the size and detected
					// kind of whatever it found. An MCP caller sends the value;
					// a person at a terminal keeps --file exactly as before.
					{Name: "file", Type: plugin.Path, Local: true, Help: "read the value from this file instead"},
					{Name: "description", Type: plugin.String, Help: "what this is for — shown by kv list"},
					{Name: "kind", Type: plugin.String, Options: kinds,
						Help: "override the kind detected from the content"},
				}...),
				Run: runSet,
			},
			{
				ID: "kv.edit", Summary: "Open a stored value in $EDITOR and re-encrypt it on save",
				Safety: plugin.Write, NeedsGrant: true, Scope: "key",
				Description: "For changing a secret you have to look at while you change it: one line " +
					"of a kubeconfig, one field of a JSON credential, a certificate chain gaining an " +
					"intermediate. `kv set` can do all of that too, and puts the entire new value in " +
					"your shell history on the way — which is the exact leak this store exists to " +
					"close.\n\n" +
					"The plaintext is written to a file mode 0600 inside a directory mode 0700 " +
					"(/dev/shm where there is one, so it never reaches a disk at all), and the whole " +
					"directory is removed afterwards — including the swap and backup files editors " +
					"leave beside what they are editing.\n\n" +
					"$VISUAL, then $EDITOR, then vi. An editor that returns before you have saved " +
					"loses the edit, so a windowed one needs its wait flag: EDITOR='code --wait'.\n\n" +
					"Refused anywhere there is no terminal to hand over — an editor is a person at a " +
					"keyboard, which over MCP is nobody. Binary values are refused too: a DER " +
					"certificate opened in a text editor comes back mangled, so those take the " +
					"`kv get --out` / `kv set --file` round trip that preserves bytes.",
				Inputs: unlockFields([]plugin.Field{
					{Name: "key", Type: plugin.String, Positional: true, Required: true, Help: "key to edit",
						Suggest: suggestKeys},
				}...),
				Run: runEdit,
			},
			{
				ID: "kv.rename", Summary: "Rename a key, keeping its value and its history",
				Safety: plugin.Write, NeedsGrant: true, Scope: "key",
				Description: "Renaming used to mean `kv get` piped into `kv set` and then `kv rm`: two " +
					"grants for an operation that reveals nothing, and the secret itself sitting in " +
					"shell history at the join. This moves the entry inside the store — the value is " +
					"never decrypted into anything but memory, and its description, kind, source and " +
					"timestamps travel with it.\n\n" +
					"A name that is already taken is refused rather than overwritten: renaming onto " +
					"an existing key would destroy the secret in it, which is `kv rm`'s question and " +
					"is asked with `kv rm`'s answer.",
				Inputs: unlockFields([]plugin.Field{
					{Name: "key", Type: plugin.String, Positional: true, Required: true, Help: "key to rename",
						Suggest: suggestKeys},
					{Name: "new-name", Type: plugin.String, Positional: true, Required: true, Help: "what to call it instead"},
				}...),
				Run: runRename,
			},
			{
				ID: "kv.rm", Summary: "Remove a stored key permanently", Safety: plugin.Destructive,
				Scope: "key",
				Description: "The entry and its value are gone when this returns. There is no history, " +
					"no backup and no undo — a store keeps one version of each secret, which is the " +
					"whole reason overwriting one with `kv set` asks the same question this does.\n\n" +
					"Who can open the store does not change: removing the last entry leaves an empty " +
					"store locked to the same keys, not an unlocked one. To rename rather than " +
					"replace, `kv rename` moves an entry without its value ever leaving memory.",
				Inputs: unlockFields([]plugin.Field{
					{Name: "key", Type: plugin.String, Positional: true, Required: true, Help: "key to remove",
						Suggest: suggestKeys},
				}...),
				Run: runRemove,
			},
			{
				ID: "kv.show", Summary: "Show everything about one entry except its value",
				Safety: plugin.Read, Idempotent: true,
				Description: "The detail page for a stored key: what kind of thing it is, what it is " +
					"for, how big it is, where it came from and when it changed. Deliberately not the " +
					"value — that is `kv get` (prints it) or `kv copy` (does not), both writes for " +
					"exactly that reason. This stays Read because everything on it is metadata you " +
					"can safely put on a screen.",
				Inputs: unlockFields([]plugin.Field{
					{Name: "key", Type: plugin.String, Positional: true, Required: true, Help: "key to describe",
						Suggest: suggestKeys},
				}...),
				Run: runShow,
			},
			{
				ID: "kv.recipients", Summary: "List the public keys that can decrypt the store",
				Safety: plugin.Read, Idempotent: true,
				Description: "Answers \"who can read this?\" without unlocking anything — recipients " +
					"are public keys, so the list is plaintext by design. An empty list means the " +
					"store is passphrase-encrypted.",
				Run: runRecipients,
			},
			{
				ID: "kv.init", Summary: "Set up how the store is encrypted", Safety: plugin.Write,
				Idempotent: true,
				Description: "Chooses the lock once, so nothing has to be repeated afterwards. " +
					"--generate makes a dedicated age key for this store and uses it: no passphrase " +
					"to type, no flags to remember, and — unlike your SSH login key — a key whose " +
					"loss costs you this store and nothing else. --identity locks it to a key you " +
					"already have (age or SSH). --recipient adds other readers.\n\n" +
					"A passphrase store needs no init: that is what you get by default.",
				Inputs: unlockFields([]plugin.Field{
					{Name: "generate", Type: plugin.Bool,
						Help: "create a dedicated age key for this store and lock it to that"},
					// Local, exactly as kv.set's `file` is and for the same
					// recorded reason: parseRecipient accepts a *path* and
					// reads it, so this input reaches the filesystem — and
					// the path gate only hooks Field.Path, which a
					// StringSlice can never be. An agent supplying
					// ["~/.zshrc"] got the file's first meaningful line back
					// in the parse error, plus a classifier for every other
					// outcome (holds no public key / is a private key /
					// permission denied / is a directory). Key management is
					// not a thing to put in front of a model at all, which is
					// the shorter reason and the one that would have avoided
					// this without anybody noticing the path.
					{Name: "recipient", Type: plugin.StringSlice, Local: true,
						Help: "also let this age/SSH public key read the store, repeatable"},
				}...),
				Run: runInit,
			},
			{
				ID: "kv.rekey", Summary: "Change which keys can open the store", Safety: plugin.Destructive,
				Description: "The store is decrypted and written back under a new set of keys, so this " +
					"is the one command that changes who can read what you already stored.\n\n" +
					"Adding is the default: `--generate` makes a dedicated age key and leaves the " +
					"existing readers alone, which is how a store locked to your SSH key gains a key " +
					"that needs no passphrase — after which both open it and the new one is found " +
					"without a flag. `--only` makes the set exclusive instead: `--generate --only` " +
					"switches the lock from one key to the other, and naming the readers you keep is " +
					"how a reader is removed.\n\n" +
					"--identity never changes the set: it says which private key is here, which is " +
					"what opens the store and the only way to prove a key is yours — a public key on " +
					"its own shows nothing of the sort.\n\n" +
					"Two things are refused rather than confirmed. Reading comes first, so a store you " +
					"cannot open is a store you cannot re-key. And a key you hold has to survive the " +
					"change: handing the store to somebody else and locking yourself out of it in the " +
					"same keystroke is not a thing to be sure about.\n\n" +
					"Old copies of the file stay readable by the old keys — re-keying changes the lock, " +
					"it does not reach into backups.",
				Inputs: unlockFields([]plugin.Field{
					{Name: "generate", Type: plugin.Bool,
						Help: "create a dedicated age key for this store and add it"},
					// Local, matching kv.init and kv.set. ADR 0014 recorded
					// this as a known gap on the grounds that Destructive
					// already forces a grant — true, and a grant is a
					// capability-level TTL window rather than a decision per
					// recipient, so it authorises the act and not the file
					// this reads. No kv capability takes a recipient from a
					// remote caller now, which is a rule rather than three
					// separate judgements.
					{Name: "recipient", Type: plugin.StringSlice, Local: true,
						Help: "also let this key read the store: an age/SSH public key, or a path to one — " +
							"including a private key, whose public half is all that is read; repeatable"},
					{Name: "only", Type: plugin.Bool,
						Help: "lock it to exactly these keys, dropping every other reader"},
				}...),
				Run: runRekey,
			},
			{
				ID: "kv.status", Summary: "Where the store is and what can open it",
				Safety: plugin.Read, Idempotent: true,
				Detailed: true,
				Description: "Everything about the store that can be known without unlocking it: " +
					"whether it exists, how big it is, when it last changed, whether it is locked " +
					"with a passphrase or to keys, and whether a key is available in this " +
					"environment. With --detail it also lists who can open the store and what is " +
					"in it — names, kinds and sizes, never a value or a preview of one — when a " +
					"key is already at hand; when none is, it says so instead of asking, so the " +
					"compact answer never turns into a passphrase prompt nobody expected.",
				Inputs: unlockFields(),
				Run:    runStatus,
			},
		},
	}
}

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
	Value       []byte    `json:"value"`
	Description string    `json:"description,omitempty"`
	Kind        string    `json:"kind,omitempty"`
	Filename    string    `json:"filename,omitempty"` // set when the value came from disk
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
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
	identities, verr := readKeys(req)
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
			// answer to "who can decrypt this". Found by review (PROJECT.md
			// D74).
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

// emptyList says why the list is empty, which is a different sentence for an
// empty store and for a filter that matched nothing. Answering both with
// "no keys stored yet" sent people off to re-add a secret that was there all
// along, one `--kind json` away.
func emptyList(stored int, kind, match string) string {
	if stored == 0 {
		return "No keys stored yet — add one with: rta kv set <key> <value>"
	}
	var narrowed []string
	if kind != "" {
		narrowed = append(narrowed, "of kind "+kind)
	}
	if match != "" {
		narrowed = append(narrowed, fmt.Sprintf("matching %q", match))
	}
	return fmt.Sprintf("No key %s. The store holds %s — `rta kv list` shows every one.",
		strings.Join(narrowed, " "), plural(stored, "key"))
}

// plural counts a noun, matching builtin/audit's helper of the same name.
// "locked to 2 key(s)" is the shape of message that gets written once and
// read every day, and a store whose status line cannot count is not the
// thing to look careless about.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if len(noun) > 1 && strings.HasSuffix(noun, "y") &&
		!strings.ContainsRune("aeiou", rune(noun[len(noun)-2])) {
		return fmt.Sprintf("%d %sies", n, noun[:len(noun)-1])
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// runList never touches a value: only key names, kinds, sizes, descriptions
// and timestamps leave this function, by construction — see the package doc.
func runList(_ context.Context, req plugin.Request) (view.View, error) {
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	kindFilter := strings.TrimSpace(req.String("kind"))
	// Matched against the description as well as the name, because the name
	// is the half you have forgotten: "which one was the deploy key for the
	// staging cluster" is answerable from what you wrote down at the time
	// and not from `prod-deploy-key`.
	match := strings.ToLower(strings.TrimSpace(req.String("match")))
	detail := req.Bool("detail")

	names := make([]string, 0, len(s.Entries))
	for k, e := range s.Entries {
		if kindFilter != "" && e.Kind != kindFilter {
			continue
		}
		if match != "" && !strings.Contains(strings.ToLower(k), match) &&
			!strings.Contains(strings.ToLower(e.Description), match) {
			continue
		}
		names = append(names, k)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return view.Text{Body: emptyList(len(s.Entries), kindFilter, req.String("match"))}, nil
	}

	cols := []view.Column{
		{Name: "Key"},
		{Name: "Kind"},
		{Name: "Size", Kind: view.KindBytes},
		{Name: "Description"},
		{Name: "Updated", Kind: view.KindDuration},
	}
	if detail {
		cols = append(cols, view.Column{Name: "Source"}, view.Column{Name: "Created", Kind: view.KindDuration})
	}
	t := view.Table{Columns: cols}
	for _, k := range names {
		e := s.Entries[k]
		row := []string{k, e.Kind, format.Bytes(uint64(len(e.Value))), e.Description, itemstore.Age(e.Updated)}
		if detail {
			row = append(row, e.Filename, itemstore.Age(e.Created))
		}
		t.Rows = append(t.Rows, row)
	}
	t.Total = len(t.Rows)
	return t, nil
}

// runShow describes one entry without revealing it. Size is the closest it
// comes to the value, and a byte count tells you a token is a token without
// telling you which one.
func runShow(_ context.Context, req plugin.Request) (view.View, error) {
	key := req.String("key")
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	e, ok := s.Entries[key]
	if !ok {
		return nil, notFound(key)
	}
	pairs := []view.Pair{
		{Key: "key", Value: key},
		{Key: "kind", Value: e.Kind},
		{Key: "size", Value: format.Bytes(uint64(len(e.Value)))},
	}
	if e.Description != "" {
		pairs = append(pairs, view.Pair{Key: "description", Value: e.Description})
	}
	if e.Filename != "" {
		pairs = append(pairs, view.Pair{Key: "source", Value: e.Filename})
	}
	pairs = append(pairs,
		view.Pair{Key: "updated", Value: itemstore.Age(e.Updated)},
		view.Pair{Key: "created", Value: itemstore.Age(e.Created)},
		// Both ways out, with the one that shows nothing first: the page
		// you are on exists because you did not want the value on screen,
		// and offering only `kv get` from it was an odd thing to end on.
		view.Pair{Key: "copy", Value: "rta kv copy " + key},
		view.Pair{Key: "reveal", Value: "rta kv get " + key},
	)
	return view.KeyValue{Pairs: pairs}, nil
}

func runGet(_ context.Context, req plugin.Request) (view.View, error) {
	key := req.String("key")
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	e, ok := s.Entries[key]
	if !ok {
		return nil, notFound(key)
	}
	out := req.String("out")
	if out == "" {
		return view.Text{Body: string(e.Value)}, nil
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would write %q (%s) to %s", key, format.Bytes(uint64(len(e.Value))), out)}, nil
	}
	// A secret leaving the store for the filesystem lands readable by its
	// owner and nobody else, whatever the umask says — and whatever mode the
	// file already had: os.WriteFile only applies its perm argument to a file
	// it creates, so overwriting an existing world-readable file left it
	// world-readable while this printed "mode 0600" beside it. Same
	// temp-file-plus-rename-plus-explicit-chmod discipline as the store
	// itself, which also makes the write atomic. Exactly the bytes that were
	// stored, too: a round trip, not an editorial pass deciding whether
	// something needed a trailing newline.
	if verr := writeOut(expandHome(out), e.Value); verr != nil {
		return nil, verr
	}
	return view.Text{Body: fmt.Sprintf("wrote %q to %s (%s, mode 0600)", key, out, format.Bytes(uint64(len(e.Value))))}, nil
}

// writeOut writes a secret to a caller-chosen path at exactly mode 0600,
// regardless of what — if anything — was there before.
func writeOut(path string, data []byte) *view.Error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return view.Errorf("kv.out.unwritable", "creating %s: %v", dir, err)
	}
	if err := atomicfile.Write(path, data, 0o600); err != nil {
		return view.Errorf("kv.out.unwritable", "writing %s: %v", path, err)
	}
	return nil
}

// envName turns a prefix and a key into an environment variable name:
// prefix "APP_", key "db-password" becomes APP_DB_PASSWORD. A leading digit
// gets a guard underscore, since a shell will not accept a name starting
// with one.
//
// Both halves go through the identical character whitelist. Key alone used
// to be filtered while prefix was written verbatim — found by review
// (PROJECT.md D74): a prefix containing a newline broke `kv env`'s output
// into extra lines, one of which could be a live command substitution,
// directly against the eval "$(rta kv env …)" usage this capability's own
// Description recommends. Filtering the whole name together, rather than
// filtering prefix and key separately and concatenating the results, is
// what keeps a boundary-straddling injection (a prefix ending mid-escape)
// from reopening the same hole a piece at a time.
func envName(prefix, key string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(prefix + key) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	name := sb.String()
	if name != "" && name[0] >= '0' && name[0] <= '9' {
		name = "_" + name
	}
	return name
}

// shellQuote wraps a value so a shell reads it back byte for byte. Single
// quotes protect everything except a single quote, which is spliced in.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func runEnv(_ context.Context, req plugin.Request) (view.View, error) {
	syntax := strings.ToLower(strings.TrimSpace(req.String("format")))
	if syntax == "" {
		syntax = "export"
	}
	if syntax != "export" && syntax != "dotenv" {
		return nil, view.Errorf("kv.env.badformat", "unknown format %q", syntax).
			WithHint("use export (for eval) or dotenv (for a .env file)")
	}

	keys := req.StringSlice("key")
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	if len(keys) == 0 {
		for k := range s.Entries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			return view.Text{Body: "# no keys stored"}, nil
		}
	}

	prefix := req.String("prefix")
	var sb strings.Builder
	for _, k := range keys {
		e, ok := s.Entries[k]
		if !ok {
			return nil, notFound(k)
		}
		if syntax == "export" {
			sb.WriteString("export ")
		}
		sb.WriteString(envName(prefix, k))
		sb.WriteString("=")
		sb.WriteString(shellQuote(string(e.Value)))
		sb.WriteString("\n")
	}
	return view.Text{Body: strings.TrimRight(sb.String(), "\n")}, nil
}

// valueToStore resolves the value and where it came from.
func valueToStore(req plugin.Request) (value []byte, filename string, err error) {
	if path := req.String("file"); path != "" {
		data, err := os.ReadFile(expandHome(path))
		if err != nil {
			return nil, "", view.Errorf("kv.file.unreadable", "reading %s: %v", path, err)
		}
		// Exactly what was on disk. Trimming a trailing newline was a
		// convenience for text, and it cost every other file its last bytes —
		// a certificate's final DER byte is not whitespace to be tidied away.
		return data, filepath.Base(path), nil
	}
	raw := req.String("value")
	if raw == "" {
		return nil, "", view.Errorf("kv.set.novalue", "no value given").
			WithHint("pass a value, or --file to read one from disk")
	}
	return []byte(raw), "", nil
}

func runSet(_ context.Context, req plugin.Request) (view.View, error) {
	key := strings.TrimSpace(req.String("key"))
	if key == "" {
		return nil, view.Errorf("kv.set.nokey", "key is empty")
	}
	if verr := refuseSilentIdentity(req); verr != nil {
		return nil, verr
	}
	value, filename, err := valueToStore(req)
	if err != nil {
		return nil, err
	}
	kind := strings.TrimSpace(req.String("kind"))
	if kind == "" {
		kind = detectKind(string(value), filename)
	} else if !contains(kinds, kind) {
		return nil, view.Errorf("kv.set.badkind", "unknown kind %q", kind).
			WithHint("use one of: " + strings.Join(kinds, ", "))
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would set %q (%s, %s)", key, kind, format.Bytes(uint64(len(value))))}, nil
	}

	unlock, verr := lockStore()
	if verr != nil {
		return nil, verr
	}
	defer unlock()
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	now := time.Now()
	previous, existed := s.Entries[key]
	e := entry{
		Value: value, Kind: kind, Filename: filename,
		Description: req.String("description"), Created: now, Updated: now,
	}
	if existed {
		e.Created = previous.Created
		// An edit that says nothing about the description keeps the old one:
		// re-setting a rotated token should not silently erase what it is for.
		if e.Description == "" {
			e.Description = previous.Description
		}
	}
	s.Entries[key] = e
	if verr := save(req, s); verr != nil {
		return nil, verr
	}

	verb := "set"
	if existed {
		verb = "updated"
	}
	msg := fmt.Sprintf("%s %q (%s, %s)", verb, key, kind, format.Bytes(uint64(len(value))))
	if specs := req.StringSlice("recipient"); len(specs) > 0 {
		msg += "\nstore re-encrypted — `rta kv recipients` lists who can read it"
	}
	return view.Text{Body: msg}, nil
}

// runRename moves an entry to another name inside the store.
//
// Nothing about the secret changes, and that is the point: the value is never
// decrypted into anything but this process's memory, so unlike the get-set-rm
// dance it replaces, no part of it reaches a shell's history or another
// command's argv.
//
// The timestamps travel unchanged, including Updated. A name is not a
// rotation, and `kv list`'s Updated column is the one place you can see that
// a token has been sitting there for fourteen months — a rename that reset it
// would quietly answer "yesterday" to the only question that column exists
// to answer.
func runRename(_ context.Context, req plugin.Request) (view.View, error) {
	from := strings.TrimSpace(req.String("key"))
	to := strings.TrimSpace(req.String("new-name"))
	if from == "" || to == "" {
		return nil, view.Errorf("kv.rename.noname", "rename needs a key and a new name").
			WithHint("rta kv rename <key> <new-name>")
	}
	if from == to {
		return nil, view.Errorf("kv.rename.samename", "%q is already its name", from)
	}
	if verr := refuseSilentIdentity(req); verr != nil {
		return nil, verr
	}
	unlock, verr := lockStore()
	if verr != nil {
		return nil, verr
	}
	defer unlock()
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	e, ok := s.Entries[from]
	if !ok {
		return nil, notFound(from)
	}
	// Refused, never confirmed. The overwrite would destroy the secret under
	// the target name with no history and no undo, which is exactly what
	// kv.rm is Destructive for — and a grant scoped to the key being renamed
	// says nothing at all about the one being clobbered.
	if _, taken := s.Entries[to]; taken {
		return nil, view.Errorf("kv.rename.taken", "%q already exists", to).
			WithHint("renaming onto it would destroy the secret it holds — remove that first: rta kv rm " + to)
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would rename %q to %q (%s, %s)",
			from, to, e.Kind, format.Bytes(uint64(len(e.Value))))}, nil
	}
	delete(s.Entries, from)
	s.Entries[to] = e
	if verr := save(req, s); verr != nil {
		return nil, verr
	}
	return view.Text{Body: fmt.Sprintf("renamed %q to %q — anything still asking for %q will not find it",
		from, to, from)}, nil
}

func runRemove(_ context.Context, req plugin.Request) (view.View, error) {
	if verr := refuseSilentIdentity(req); verr != nil {
		return nil, verr
	}
	unlock, verr := lockStore()
	if verr != nil {
		return nil, verr
	}
	defer unlock()
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	key := req.String("key")
	e, ok := s.Entries[key]
	if !ok {
		return nil, notFound(key)
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would remove %q (%s)", key, e.Kind)}, nil
	}
	delete(s.Entries, key)
	if verr := save(req, s); verr != nil {
		return nil, verr
	}
	return view.Text{Body: fmt.Sprintf("removed %q", key)}, nil
}

// runInit chooses how the store is locked, once.
//
// It refuses to touch a store that already exists. Re-keying an existing
// store is a different operation — it has to decrypt everything first, which
// means proving you can — and `kv rekey` is that operation. Silently
// re-initialising would produce a recipients file describing a store none of
// those recipients can open.
func runInit(_ context.Context, req plugin.Request) (view.View, error) {
	if fileExists(storePath()) {
		return nil, view.Errorf("kv.init.exists", "a store already exists at %s", storePath()).
			WithHint("to change the lock on it: rta kv rekey --generate (add a key) or --generate --only (switch to it)")
	}
	if specs, verr := loadRecipients(); verr == nil && len(specs) > 0 {
		return nil, view.Errorf("kv.init.exists", "this store is already set up for keys").
			WithHint("`rta kv recipients` lists them; delete " + recipientsPath() + " to start over")
	}

	generate := req.Bool("generate")
	identity := strings.TrimSpace(req.String("identity"))
	if !generate && identity == "" && os.Getenv(identityEnv) == "" {
		return nil, view.Errorf("kv.init.nokey", "name a key, or generate one").
			WithHint("rta kv init --generate   (or --identity ~/.ssh/id_ed25519)")
	}

	var generated string
	if generate {
		if identity != "" {
			return nil, view.Errorf("kv.init.bothkeys", "--generate makes a key; --identity names one").
				WithHint("pick one")
		}
		generated = defaultIdentity()
		if req.DryRun {
			return view.Text{Body: "would generate a key at " + generated + " and lock the store to it"}, nil
		}
		if _, verr := generateIdentity(generated); verr != nil {
			return nil, verr
		}
	}
	if req.DryRun {
		return view.Text{Body: "would lock the store to " + identityPath(req)}, nil
	}

	// Writing the empty store is what commits the choice: save() resolves the
	// recipients and records them only once the ciphertext is safely on disk.
	if verr := save(req, store{Entries: map[string]entry{}}); verr != nil {
		return nil, verr
	}
	body := "the store is locked to keys — `rta kv recipients` lists them\n"
	switch {
	case generated != "":
		body += "\ngenerated a key at " + generated + " (mode 0600)\n" +
			"back it up: losing it loses every secret in the store, and nobody can help you\n" +
			"it is found automatically, so `rta kv set`/`get` need no flags"
	default:
		body += "\nusing " + identityPath(req) + "\n" +
			"set " + identityEnv + " to that path to skip --identity from now on"
	}
	return view.Text{Body: body}, nil
}

// runRekey changes the lock on a store that already exists.
//
// It is the operation `kv init` cannot be: init decides the lock when there is
// nothing to lose, and this one re-encrypts secrets that are already there.
// The two irreversible halves — reading it, and keeping a key you hold — are
// checked before anything is written, in that order.
func runRekey(_ context.Context, req plugin.Request) (view.View, error) {
	if !fileExists(storePath()) {
		return nil, view.Errorf("kv.rekey.nostore", "no store yet — nothing to re-key").
			WithHint("`rta kv init --generate` sets one up")
	}
	generate := req.Bool("generate")
	adding := req.StringSlice("recipient")
	only := req.Bool("only")
	if !generate && len(adding) == 0 {
		return nil, view.Errorf("kv.rekey.nokey", "name a key, or generate one").
			WithHint("rta kv rekey --generate (a key made for this store), " +
				// The private key file, not its .pub: naming a public key
				// alone proves nothing about holding it, and the lockout
				// guard below needs proof.
				"or --recipient ~/.ssh/id_ed25519 (one you already have)")
	}

	unlock, verr := lockStore()
	if verr != nil {
		return nil, verr
	}
	defer unlock()
	// Read it first, with whatever opens it today. Generating a key changes
	// what `--identity`-less commands resolve to, so the order matters: a
	// store you cannot open is a store you cannot re-key, and finding that
	// out after writing a new key beside the config would be a mess to undo.
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	_, stored, verr := currentMode()
	if verr != nil {
		return nil, verr
	}

	var want []string
	if !only {
		want = append(want, stored...)
	}
	held := heldHere(req)
	for _, spec := range adding {
		_, canonical, err := parseRecipient(spec)
		if err != nil {
			return nil, view.Errorf("kv.recipient.invalid", "%v", err).
				WithHint("--recipient takes an age recipient, an SSH public key, or a path to either — " +
					"including the private key itself, whose public half is all that is read")
		}
		want = mergeSpec(want, canonical)
		// Naming a private key you have is proof you have it, which is what
		// the guard below is really asking about.
		if privateKeyFile(spec) {
			held = append(held, canonical)
		}
	}
	// --identity never changes the set either. It says which private key is here —
	// which is what opens the store now, and the only way to *prove* a key is
	// yours, since a public key on its own shows nothing of the sort. Letting
	// it also mean "and keep this reader" would make `--only --generate`
	// unable to switch anything, because opening the store would preserve the
	// very key the switch is leaving behind.
	//
	// A generated key is held by definition, so this can only fail when the
	// set was named entirely from keys nothing here can open.
	if !generate && !canRead(want, held) {
		return nil, view.Errorf("kv.rekey.lockout", "nothing you hold could open the store afterwards").
			WithHint("add --generate, or name the private half of a key you have: --identity ~/.ssh/id_ed25519")
	}
	if req.DryRun {
		return view.Text{Body: rekeyPreview(generate, only, want, stored)}, nil
	}

	var generated string
	if generate {
		generated = defaultIdentity()
		spec, verr := generateIdentity(generated)
		if verr != nil {
			return nil, verr
		}
		want = mergeSpec(want, spec)
	}

	recipients, verr := recipientsFor(want)
	if verr != nil {
		return nil, verr
	}
	// Rekey is the one operation allowed to change who the store is
	// encrypted to — set the embedded record to match what is actually
	// being committed here, the same as an ordinary write does for itself.
	s.Recipients = want
	if verr := saveTo(s, recipients, want); verr != nil {
		return nil, verr
	}
	return view.Text{Body: rekeySummary(generated, want, stored)}, nil
}

// dropped returns the recipients in stored that the new set leaves out.
func dropped(want, stored []string) []string {
	var out []string
	for _, spec := range stored {
		if !canRead([]string{spec}, want) {
			out = append(out, spec)
		}
	}
	return out
}

func rekeyPreview(generate, only bool, want, stored []string) string {
	body := "would re-encrypt the store to " + plural(len(want)+boolToInt(generate), "key")
	if generate {
		body += "\ngenerating a key at " + defaultIdentity()
	}
	if only {
		if gone := dropped(want, stored); len(gone) > 0 && !generate {
			body += "\ndropping " + plural(len(gone), "reader") + " — they could no longer open it"
		} else if len(stored) > 0 && generate {
			body += "\ndropping " + plural(len(stored), "reader") + " — only the new key would open it"
		}
	}
	return body
}

func rekeySummary(generated string, want, stored []string) string {
	body := plural(len(want), "key") + " can open the store — `rta kv recipients` lists them"
	if generated != "" {
		body += "\n\ngenerated a key at " + generated + " (mode 0600)\n" +
			"back it up: losing it loses every secret it is the only key to\n" +
			"it is found automatically, so nothing needs a flag"
	}
	if gone := dropped(want, stored); len(gone) > 0 {
		body += "\n\ndropped " + plural(len(gone), "reader") + ": they cannot open the store from now on.\n" +
			"copies made before now are unaffected — re-keying changes the lock, not the backups."
	}
	return body
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// runStatus reports on the store without opening it. Every fact here comes
// from the file's metadata or from the recipients list, which is public by
// construction — so this is the one kv capability that always works, and the
// one worth putting on a dashboard.
func runStatus(ctx context.Context, req plugin.Request) (view.View, error) {
	path := storePath()
	pairs := []view.Pair{{Key: "store", Value: path}}
	info, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		return view.KeyValue{Pairs: append(pairs, view.Pair{
			Key:   "state",
			Value: "no store yet — created by the first `rta kv set <key> <value>`",
		})}, nil
	case err != nil:
		return nil, view.Errorf("kv.store.unreadable", "reading %s: %v", path, err)
	}
	pairs = append(pairs,
		view.Pair{Key: "size", Value: format.Bytes(uint64(info.Size()))},
		view.Pair{Key: "changed", Value: itemstore.Age(info.ModTime())},
	)

	mode, specs, verr := currentMode()
	if verr != nil {
		return nil, verr
	}
	if mode == modeKeys {
		pairs = append(pairs, view.Pair{Key: "locked to",
			Value: plural(len(specs), "key") + " — see `rta kv recipients`"})
	} else {
		pairs = append(pairs, view.Pair{Key: "locked with", Value: "a passphrase"})
	}
	// Whether this shell can open it is the question behind the question: a
	// store you cannot unlock right now is the thing you want to find out
	// before you need the secret, not while you need it.
	pairs = append(pairs, view.Pair{Key: "unlocks here", Value: unlockAvailability(req, mode)})
	summary := view.KeyValue{Pairs: pairs}
	if !req.Bool("detail") {
		return summary, nil
	}
	return detailedStatus(ctx, req, summary, mode), nil
}

// detailedStatus is the full-page answer to "what is in this store and who
// can open it", composed from the capabilities that already answer each half
// — kv.recipients and kv.list — rather than reaching into the store again.
//
// kv.list is safe to embed precisely because of what it does not return: key
// names, kinds, sizes, descriptions and ages, never a value nor a preview of
// one. The inventory a person needs at a glance is exactly the part that is
// not the secret.
//
// The store still has to open for that section, which is the one thing the
// compact view promises never to need. So it is attempted with whatever key
// the caller already supplied and no more: an --identity, a passphrase in
// the environment, an unlocked key. Nothing here prompts, and a store that
// will not open without asking says so and stops, because a status page that
// blocks on a passphrase is not a status page.
func detailedStatus(ctx context.Context, req plugin.Request, summary view.KeyValue, mode keyMode) view.View {
	p := plugin.NewPage(ctx, req)
	p.PutAs("store", "store", summary)
	if mode == modeKeys {
		p.AddAs("recipients", "recipients", runRecipients, plugin.Read, nil)
	}
	v, err := p.Run(runList, plugin.Read, nil)
	if err != nil {
		ve := view.AsError(err, "kv.status.locked")
		p.PutAs("keys", "keys", view.Text{Body: "Locked — the inventory needs the store open, and nothing here " +
			"can open it without asking.\n\n" + ve.Message + "\n\nRun `rta kv list` once a key is at hand."})
		return p.View()
	}
	p.PutAs("keys", "keys", v)
	return p.View()
}

// unlockAvailability says whether a key is at hand, naming only where it came
// from — never any part of the key itself.
func unlockAvailability(req plugin.Request, mode keyMode) string {
	if mode == modeKeys {
		p := identityPath(req)
		switch {
		case p == "":
			return "no identity given (--identity, or set " + identityEnv + ")"
		case !lockedKey(p):
			return "yes — identity " + p
		case lookupPassphrase(req) != "" || keyPassphrases[p] != "":
			return "yes — identity " + p + ", unlocked"
		case canPrompt(req):
			// A locked key is not the same as no key, and the difference is a
			// question you can answer — which is worth distinguishing from the
			// case where nothing here can open the store at all.
			return "on request — identity " + p + " is passphrase-protected, you will be asked"
		}
		return "no — identity " + p + " is passphrase-protected (set " + passphraseEnv + ")"
	}
	if lookupPassphrase(req) != "" {
		return "yes — passphrase from the environment"
	}
	if canPrompt(req) {
		return "on request — you will be asked for the passphrase"
	}
	return "no passphrase available (set " + passphraseEnv + ")"
}

func runRecipients(_ context.Context, _ plugin.Request) (view.View, error) {
	specs, verr := loadRecipients()
	if verr != nil {
		return nil, verr
	}
	if len(specs) == 0 {
		return view.Text{Body: "The store is encrypted with a passphrase, not keys.\n\n" +
			// The private key path, not its .pub: --recipient reads either,
			// but only the private file also proves you hold it — which is
			// what the switch below needs, and a public key alone cannot show.
			"To switch to a key of your own:\n" +
			"  rta kv rekey --only --recipient ~/.ssh/id_ed25519\n" +
			"or to one made for the job, which needs no passphrase at all:\n" +
			"  rta kv rekey --only --generate"}, nil
	}
	t := view.Table{Columns: []view.Column{{Name: "Type"}, {Name: "Recipient"}, {Name: "Comment"}}}
	for _, spec := range specs {
		fields := strings.Fields(spec)
		switch {
		case strings.HasPrefix(spec, "age1"):
			t.Rows = append(t.Rows, []string{"age", spec, ""})
		case len(fields) >= 2:
			// An authorized-keys line: type, key, and an optional comment
			// that is usually the only human-readable part.
			t.Rows = append(t.Rows, []string{fields[0], truncate(fields[1], 24), strings.Join(fields[2:], " ")})
		default:
			t.Rows = append(t.Rows, []string{"?", truncate(spec, 24), ""})
		}
	}
	t.Total = len(t.Rows)
	return t, nil
}

// suggestKeys completes a key name from the store — but only when the store
// can be opened without asking anybody anything.
//
// This is the one completion that could otherwise do real harm: resolving a
// passphrase may prompt, and a prompt fired by the tab key would hang the
// shell mid-command-line on a question nobody expects. So availability is
// checked first, and a store that would need a prompt simply offers nothing.
//
// The surface is the backstop underneath that check: a completion request
// cannot prompt whatever it calls (plugin.SurfaceCompletion), so the worst a
// mistake here can cost is a missing suggestion.
func suggestKeys(_ context.Context, req plugin.Request) []string {
	mode, _, verr := currentMode()
	if verr != nil {
		return nil
	}
	switch mode {
	case modeKeys:
		if identityPath(req) == "" {
			return nil
		}
	default:
		if lookupPassphrase(req) == "" {
			return nil
		}
	}
	s, verr := load(req)
	if verr != nil {
		return nil
	}
	keys := make([]string, 0, len(s.Entries))
	for k, e := range s.Entries {
		// The description is exactly what a key name six months old lacks.
		note := e.Description
		if note == "" {
			note = e.Kind
		}
		keys = append(keys, k+"\t"+note)
	}
	sort.Strings(keys)
	return keys
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
		// kv.identity.unreadable. Found by review (PROJECT.md D74); mirrors
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
