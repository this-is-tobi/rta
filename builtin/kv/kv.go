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
	"fmt"
	"strconv"

	"github.com/this-is-tobi/rta/pkg/plugin"
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
					{Name: "removed", Type: plugin.Bool,
						Help: "list what `kv rm` set aside instead — restorable until purged"},
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
				Safety: plugin.Write, Idempotent: true, HumanOnly: true,
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
					{Name: "prefix", Type: plugin.String, Config: "env.prefix", Help: "prepend this to every variable name, e.g. APP_"},
					{Name: "format", Type: plugin.String, Config: "env.format", Default: "export", Options: []string{"export", "dotenv"},
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
					"With no value at all, --description and --kind relabel an entry that already " +
					"exists, leaving the secret and both timestamps untouched — so correcting what " +
					"something is for does not mean fetching and re-typing the secret itself, and " +
					"does not reset the age `kv list` reports for it.\n\n" +
					"Setting a key that already exists replaces the secret in it and keeps the old " +
					"one — the last " + strconv.Itoa(maxRevisions) + " values stay behind the key, " +
					"listed by `kv history` and brought back by `kv restore --revision`. So a paste " +
					"over the wrong key is a mistake you undo, not one you re-type from memory. Over " +
					"MCP it still needs a per-key grant: an agent that can overwrite a secret can " +
					"still break what reads it, undo or not.",
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
				Prefill: prefillSet,
				Run:     runSet,
			},
			{
				ID: "kv.edit", Summary: "Open a stored value in $EDITOR and re-encrypt it on save",
				Safety: plugin.Write, HumanOnly: true,
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
				ID: "kv.rm", Summary: "Remove a stored key, keeping it restorable until purged", Safety: plugin.Destructive,
				Scope: "key",
				Description: "The key leaves the listing and every read of it, but the entry is kept " +
					"aside whole — value, history and all — inside the same encrypted store, where " +
					"`kv list --removed` shows it and `kv restore` brings it back. That is the undo a " +
					"mis-click needs. --purge is the removal that has none: the entry and everything it " +
					"ever held are gone when it returns, and it also finishes off a key removed " +
					"earlier.\n\n" +
					"Destructive either way, because a removed key is still a key nothing can read until " +
					"somebody notices. Who can open the store does not change: removing the last entry " +
					"leaves an empty store locked to the same keys, not an unlocked one. To rename " +
					"rather than replace, `kv rename` moves an entry without its value ever leaving memory.",
				Inputs: unlockFields([]plugin.Field{
					{Name: "key", Type: plugin.String, Positional: true, Required: true, Help: "key to remove",
						Suggest: suggestKeys},
					{Name: "purge", Type: plugin.Bool,
						Help: "destroy it, history included — no restore"},
				}...),
				Run: runRemove,
			},
			historyCapability(),
			restoreCapability(),
			treeCapability(),
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
					{Name: "recipient", Type: plugin.StringSlice, Local: true, Suggest: suggestRecipients,
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
					// Local, matching kv.init and kv.set. It is on record
					// this as a known gap on the grounds that Destructive
					// already forces a grant — true, and a grant is a
					// capability-level TTL window rather than a decision per
					// recipient, so it authorises the act and not the file
					// this reads. No kv capability takes a recipient from a
					// remote caller now, which is a rule rather than three
					// separate judgements.
					{Name: "recipient", Type: plugin.StringSlice, Local: true, Suggest: suggestRecipients,
						Help: "also let this key read the store: an age/SSH public key, or a path to one — " +
							"including a private key, whose public half is all that is read; repeatable"},
					{Name: "only", Type: plugin.Bool,
						Help: "lock it to exactly these keys, dropping every other reader"},
				}...),
				Run: runRekey,
			},
			{
				ID: "kv.status", Summary: "Where the store is and what can open it",
				Safety: plugin.Read, HostSpecific: true, Idempotent: true,
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
