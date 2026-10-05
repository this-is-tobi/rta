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
// and from an AI-safety standpoint "read" there means "leak". Write makes a
// grant (internal/grant) the precondition, and a scope makes that grant
// per-key. kv.list stays Read because it is genuinely safe:
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
		Help: "the passphrase this store is locked with",
	}
	// kv.set is the one operation that makes a store, and says what that commits
	// to because its box is the one place a store is born that does not ask
	// twice: a terminal's prompt repeats itself for a store not made yet
	// (askPassphrase), while a form — the TUI's, or a --passphrase flag — hands
	// over one answer and nothing can ask again. Every other operation opens a
	// store that exists, and a sentence about one not made yet would only be
	// read as something it has to do with it.
	newStorePassphraseField = plugin.Field{
		Name: "passphrase", Type: plugin.Secret, Local: true, EnvFallback: true,
		Help: "the store's passphrase; a store not made yet is locked with it, and nothing recovers it",
	}
	identityField = plugin.Field{
		Name: "identity", Type: plugin.Path, Local: true, EnvFallback: true,
		Help: "private key to unlock with, e.g. ~/.ssh/id_ed25519",
		// The keys this machine has, offered before the filesystem is walked
		// for them: one of these is nearly always the answer.
		Suggest: suggestIdentities,
	}
)

// unlockFields are the inputs every store operation accepts.
func unlockFields(extra ...plugin.Field) []plugin.Field {
	return append(extra, passphraseField, identityField)
}

// creatingFields are unlockFields for the operation that can make the store.
func creatingFields(extra ...plugin.Field) []plugin.Field {
	return append(extra, newStorePassphraseField, identityField)
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
					"thing each is, its size, its description, when it changed and — once any " +
					"value has been replaced — how many earlier values `kv.history` still keeps " +
					"for each. With `detail`: the source filename of anything stored from disk.\n\n" +
					"`folder` is one folder of the tree `kv.tree` draws, `db/` for `db/password` and " +
					"`db/user`: only the keys under it.\n\n" +
					"`match` is the \"which one was it called?\" filter: a substring of the name or " +
					"the description, case-insensitive, so a `match` of aws finds " +
					"`prod-deploy-key` when the description is the only place the word AWS appears.",
				Inputs: unlockFields([]plugin.Field{
					{Name: "folder", Type: plugin.String, Positional: true, Suggest: suggestFolders,
						Help: "only the keys in this folder, e.g. db/"},
					{Name: "kind", Type: plugin.String, Options: kinds,
						Help: "only entries of this kind"},
					{Name: "match", Type: plugin.String,
						Help: "only keys whose name or description contains this (case-insensitive)"},
					{Name: "removed", Type: plugin.Bool,
						Help: "list what `kv.rm` set aside instead — restorable until purged"},
				}...),
				// `v` reveals, and the argument for it is the argument that was originally
				// made against it, followed through.
				//
				// This list used to offer no reveal, because "a secret shown because a key
				// was pressed on a list is a secret shown by accident" — `kv get` asks for
				// it by name, which is the point at which you meant to. The reasoning is
				// right and the conclusion did not follow, because it measured the wrong
				// thing. **The friction that makes a reveal deliberate is not the typing;
				// it is the unlock.** Every kv action opens the unlock form on the way —
				// the passphrase and identity are inputs like any other, so there is
				// always something left to ask — and an operator who pressed `v` by
				// accident is looking at a form naming the entry, not at its value. The
				// value then arrives on its own result page, titled with the entry it
				// belongs to, rather than in a cell of a list somebody was scrolling —
				// which is why kv.get declares no Flash.
				//
				// `c` was the tell. Copying is the same act with a smaller audience — the
				// catalogue classifies it identically for exactly that reason, "a value on
				// the clipboard has been revealed" — and it has been a row action here
				// since the beginning. The old argument (no scrollback, no screen share,
				// undone by the next copy) is real; what it does not support is making the
				// *other* half unreachable from the screen an operator is already on,
				// which sent people to a second terminal for a secret they had already
				// unlocked the store for.
				//
				// What stays refused is the thing actually worth refusing: nothing on this
				// screen puts a value in a row. `kv list` shows names, kinds and
				// descriptions, and the entry's page shows its metadata; a value appears
				// only where somebody asked for that one entry.
				//
				// kv.edit is still absent, for an unrelated reason: it hands the terminal
				// to $EDITOR, and the terminal is what the TUI is drawing on. `D` unfolds
				// the detail columns the list keeps compact.
				Actions: []plugin.Action{
					{Key: "enter", Label: "show", Target: "kv.show", Source: plugin.ActionRow},
					{Key: "v", Label: "reveal", Target: "kv.get", Source: plugin.ActionRow},
					{Key: "c", Label: "copy", Target: "kv.copy", Source: plugin.ActionRow},
					{Key: "a", Label: "add", Target: "kv.set"},
					{Key: "s", Label: "set", Target: "kv.set", Source: plugin.ActionRow},
					{Key: "m", Label: "rename", Target: "kv.rename", Source: plugin.ActionRow},
					{Key: "x", Label: "remove", Target: "kv.rm", Source: plugin.ActionRow},
				},
				Toggles: []plugin.Toggle{{Key: "D", Label: "detail", Input: "detail"}},
				Run:     runList,
			},
			{
				ID: "kv.get", Summary: "Reveal a stored value", Safety: plugin.Write, Idempotent: true,
				NeedsGrant: true, Scope: "key",
				Description: "Classified as a write, because revealing a secret is the " +
					"sensitive act: an agent needs a grant naming the key it may read, issued by a person. A " +
					"grant authorizes revealing a value, not choosing where on this machine it gets written, " +
					"so the value comes back in the response.",
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
				Flash:  true,
				Safety: plugin.Write, Idempotent: true, HumanOnly: true,
				Description: "The value goes to this machine's clipboard and nowhere else: not to the " +
					"screen, not into scrollback, not into shell history — because getting a secret " +
					"somewhere is nearly always a paste rather than a read, and the reading is the " +
					"part that leaves a copy behind.\n\n" +
					"Classified with `kv.get`, not below it: a value on the clipboard has been " +
					"revealed, and every process running as you can read it. Printing nothing buys " +
					"a smaller audience, not a different act.\n\n" +
					"Refused over MCP however the grants read. The clipboard is not a return value — " +
					"it is a shared channel on somebody else's desk that the caller cannot read back, " +
					"so an agent gains nothing here it could not get from `kv.get`, while gaining the " +
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
				Description: "Output for a shell to evaluate, which loads secrets into its session " +
					"without ever writing them to a file. Key names become environment " +
					"names (db-password → DB_PASSWORD). A `format` of dotenv writes .env syntax instead. " +
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
				Flash:      true,
				NeedsGrant: true, Scope: "key",
				Description: "The value is given as `value`; a call with none is refused unless " +
					"its `description` or `kind` relabel an entry that already exists, which leaves the " +
					"secret and both timestamps untouched, so correcting what something is for does not reset " +
					"the age kv.list reports. The kind (certificate, private key, json, file, string) is " +
					"detected from the content unless `kind` says otherwise. Writing never changes who can " +
					"read the store: that is `kv.rekey`. Setting a key that already exists replaces the " +
					"secret and keeps the old one, the last " + strconv.Itoa(maxRevisions) + " values listed by `kv.history` and brought back " +
					"by `kv.restore` with a `revision`. It still needs a per-key grant: an agent that can " +
					"overwrite a secret can break what reads it, undo or not.",
				Inputs: creatingFields([]plugin.Field{
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
					{Name: "description", Type: plugin.String, Help: "what this is for — shown by kv.list"},
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
					"intermediate. `kv.set` can do all of that too, and puts the entire new value in " +
					"your shell history on the way — which is the exact leak this store exists to " +
					"close.\n\n" +
					"The plaintext is written to a file mode 0600 inside a directory mode 0700 " +
					"(/dev/shm where there is one, so it never reaches a disk at all), and the whole " +
					"directory is removed afterwards — including the swap and backup files editors " +
					"leave beside what they are editing.\n\n" +
					"$VISUAL, then $EDITOR, then vi. An editor that returns before you have saved " +
					"loses the edit, so a windowed one needs its wait flag: `EDITOR='code --wait'`.\n\n" +
					"Refused anywhere there is no terminal to hand over — an editor is a person at a " +
					"keyboard, which over MCP is nobody. Binary values are refused too: a DER " +
					"certificate opened in a text editor comes back mangled, so those take the " +
					"round trip that preserves bytes: `kv.get` with `out`, then `kv.set` with `file`.",
				Inputs: unlockFields([]plugin.Field{
					{Name: "key", Type: plugin.String, Positional: true, Required: true, Help: "key to edit",
						Suggest: suggestKeys},
				}...),
				Run: runEdit,
			},
			{
				ID: "kv.rename", Summary: "Rename a key, keeping its value and its history",
				Flash:  true,
				Safety: plugin.Write, NeedsGrant: true, Scope: "key",
				// The name it moves to as well as the key it moves: read
				// grants are scoped by key name, so a rename checked only at
				// its source moved a secret out from under one grant and in
				// under another — a rename grant for one prod key plus a
				// read grant for scratch/ read that prod key.
				ScopeAlso: []string{"new-name"},
				Description: "The entry moves inside the store, the " +
					"value is never decrypted into anything but memory, and its description, kind, source and " +
					"timestamps travel with it: the one way to rename that reveals nothing, where kv.get, " +
					"kv.set and kv.rm took two grants and put the secret in the open. A name already taken is " +
					"refused rather than overwritten, since that would destroy the secret in it, which is " +
					"`kv.rm`'s question. A grant has to cover both names, `key` and `new-name`, because a " +
					"key's name decides which grants can read it; a folder grant covers a move inside the " +
					"folder.",
				Inputs: unlockFields([]plugin.Field{
					{Name: "key", Type: plugin.String, Positional: true, Required: true, Help: "key to rename",
						Suggest: suggestKeys},
					{Name: "new-name", Type: plugin.String, Positional: true, Required: true, Help: "what to call it instead"},
				}...),
				Run: runRename,
			},
			{
				ID: "kv.rm", Summary: "Remove a stored key, keeping it restorable until purged", Safety: plugin.Destructive,
				Flash: true,
				Scope: "key",
				Description: "The key leaves the listing and every read of it, but the entry is kept " +
					"aside whole — value, history and all — inside the same encrypted store, where " +
					"`kv.list` shows it with `removed` and `kv.restore` brings it back. That is the undo a " +
					"mis-click needs. `purge` is the removal that has none: the entry and everything it " +
					"ever held are gone when it returns, and it also finishes off a key removed " +
					"earlier.\n\n" +
					"Destructive either way, because a removed key is still a key nothing can read until " +
					"somebody notices. Who can open the store does not change: removing the last entry " +
					"leaves an empty store locked to the same keys, not an unlocked one. To rename " +
					"rather than replace, `kv.rename` moves an entry without its value ever leaving memory.",
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
					"value — that is `kv.get`, a write for exactly that reason. This stays Read " +
					"because everything on it is metadata.",
				Inputs: unlockFields([]plugin.Field{
					{Name: "key", Type: plugin.String, Positional: true, Required: true, Help: "key to describe",
						Suggest: suggestKeys},
				}...),
				// The detail page acts on the entry it is already showing.
				Actions: []plugin.Action{
					{Key: "v", Label: "reveal", Target: "kv.get", Source: plugin.ActionSelf},
					{Key: "c", Label: "copy", Target: "kv.copy", Source: plugin.ActionSelf},
					{Key: "s", Label: "set", Target: "kv.set", Source: plugin.ActionSelf},
					{Key: "m", Label: "rename", Target: "kv.rename", Source: plugin.ActionSelf},
					{Key: "x", Label: "remove", Target: "kv.rm", Source: plugin.ActionSelf},
					{Key: "a", Label: "add", Target: "kv.set"},
				},
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
				Description: "Done once. `generate` makes a dedicated age key for this " +
					"store and uses it: no passphrase to type, and unlike an SSH login key, one whose loss " +
					"costs this store and nothing else. A passphrase store needs no init, which is what the " +
					"store is by default. Locking it to a key that already exists, or adding readers, is the " +
					"operator's to do.",
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
				Description: "The store is decrypted and written back under a new set of keys, " +
					"the one operation that changes who can read what is already stored. Adding is the " +
					"default: `generate` makes a dedicated age key and leaves the existing readers alone, so " +
					"a store locked to an SSH key gains one that needs no passphrase. `only` makes the set " +
					"exclusive instead, and with `generate` switches the lock from one key to the other. Two " +
					"things are refused rather than confirmed: a store that cannot be opened, and a change " +
					"that locks out every key the operator holds. Old copies of the file stay readable by the " +
					"old keys; re-keying does not reach into backups.",
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
					"environment. With `detail` it also lists who can open the store and what is " +
					"in it — names, kinds and sizes, never a value or a preview of one — when a " +
					"key is already at hand; when none is, it says so instead of asking, so the " +
					"compact answer never turns into a passphrase prompt nobody expected.",
				Inputs: unlockFields(),
				// The kv tile is `kv status`, which is about the store rather than any
				// entry — so its actions are the two things you want from there: the
				// list, and a new secret.
				Actions: []plugin.Action{
					{Key: "s", Label: "secrets", Target: "kv.list"},
					{Key: "a", Label: "add", Target: "kv.set"},
				},
				Run: runStatus,
			},
		},
	}
}
