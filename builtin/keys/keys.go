// Package keys absorbs melt (PROJECT.md §3.3, §7 Wave 1, D70, D72): back up
// an ed25519 SSH private key as 24 memorizable BIP39 seed words, and restore
// it from them.
//
// Built-in rather than external, reclassified from Wave 3 alongside debug by
// D70: it reads ~/.ssh directly, and internal/pluginhost/denyset.go's tier2
// denies a confined plugin read access there on every platform confinement
// actually covers — deliberately, per the function's own comment. kv
// --identity already reads ~/.ssh/id_ed25519 today for exactly this reason.
//
// "Absorbing melt" means the idea, not the module: melt.go itself is a
// twenty-line wrapper around github.com/tyler-smith/go-bip39, so this
// package calls go-bip39 directly (D72) rather than adding a dependency on a
// dependency — the same call builtin/debug made absorbing sequin as
// charmbracelet/x/ansi rather than as sequin itself. The SSH-specific half —
// parsing an OpenSSH private key, prompting for its passphrase, marshaling a
// restored one back out — has no library to absorb; melt's own version of it
// lives in an unexported cmd/melt/main.go, so sshkey.go mirrors
// builtin/kv/crypt.go's already-established pattern for the same operations
// instead.
package keys

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Plugin returns the keys plugin declaration.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "keys",
		Summary: "Back up an SSH private key as memorizable words, and restore it",
		Capabilities: []plugin.Capability{
			{
				ID: "keys.list", Summary: "List the SSH private keys in ~/.ssh, and whether each can be backed up",
				Safety: plugin.Read, Idempotent: true,
				Description: "Reads only public data: a key's .pub sibling for its type, fingerprint and " +
					"comment, and whether the private key itself is passphrase-protected — the same check " +
					"`rta doctor` uses, which parses far enough to see a key is locked without ever supplying " +
					"a passphrase. Never decrypts anything. Backup-eligible means ed25519: `keys.backup` has " +
					"nothing to encode as words for an RSA or ECDSA key, which carries no single seed.",
				// Not NoPreview for either of pkg/plugin's own two reasons —
				// this reaches nothing off the box and ~/.ssh is not a
				// recursive scan — but for a third: an unencrypted key's
				// bytes are read into memory to answer "what type is this"
				// when no .pub sibling exists (describeKey, sshkey.go), and
				// doing that on every five-second dashboard tick widens a
				// private key's exposure window for a tile nobody is
				// necessarily looking at. A person naming this capability
				// explicitly, or opening it from search, still gets it.
				NoPreview: true,
				Run:       runList,
			},
			{
				ID: "keys.backup", Summary: "Back up an SSH private key as 24 BIP39 seed words",
				Safety: plugin.Write, Idempotent: true,
				Description: "The words ARE the private key — anyone holding them can restore it and use it " +
					"exactly as if they had the file, with no passphrase of their own to guess. Classified as " +
					"a write for the same reason `kv get` is: revealing key material is the sensitive act, not " +
					"changing anything. Refuses SurfaceMCP outright rather than only requiring a grant — the " +
					"same precedent grant.allow/grant.revoke set, for the same reason `share.secret.set/get` " +
					"will: once melted, the words work forever until the underlying key is rotated, with no " +
					"per-call log and nothing a grant's expiry can take back. ed25519 only: the algorithm has " +
					"a single 32-byte seed to encode; RSA and ECDSA keys do not.",
				Inputs: []plugin.Field{
					{Name: "key", Type: plugin.Path, Positional: true, Required: true,
						Help:    "private key to back up, e.g. ~/.ssh/id_ed25519",
						Suggest: suggestPrivateKeys},
					{Name: "passphrase", Type: plugin.Secret, Local: true, EnvFallback: true,
						Help: fmt.Sprintf("passphrase for the key, if it has one (or set %s)",
							plugin.LocalEnvVar("keys.backup", "passphrase"))},
				},
				Run: runBackup,
			},
			{
				ID: "keys.restore", Summary: "Reconstruct an SSH private key from its BIP39 seed words",
				Safety: plugin.Write, Idempotent: true,
				Description: "Writes <out> and <out>.pub, refusing to touch either if it already exists — a " +
					"restored key is never written over an existing one, the same discipline `kv init " +
					"--generate` uses for a fresh identity. Deterministic: an ed25519 private key is entirely " +
					"derived from its 32-byte seed, so the restored key is cryptographically identical to the " +
					"one `keys.backup` read — same fingerprint, always, provably so by comparing the one this " +
					"prints against the original's. The on-disk file itself is not byte-identical: OpenSSH's own " +
					"container writes a random per-encode nonce alongside the key material, which is the " +
					"harmless difference to expect, not a sign anything went wrong. A " +
					"comment (the user@host after the key material in a .pub file) is never encoded in the " +
					"words and is lost on backup; pass --comment to put one back. --new-passphrase locks the " +
					"key being written here — a different secret from keys.backup's --passphrase, which unlocks " +
					"the key being read there, named differently on purpose and, unlike --passphrase, never " +
					"read from the environment: a restore is a one-off action, not a standing credential for " +
					"the session, so this always has to be typed explicitly rather than inherited from " +
					"whatever a scripted backup earlier in the same shell happened to leave set. " +
					"Refuses SurfaceMCP outright, the same gate keys.backup sets and for the same reason.",
				Inputs: []plugin.Field{
					{Name: "out", Type: plugin.Path, Positional: true, Required: true,
						Help: "where to write the restored private key (and <out>.pub)"},
					{Name: "words", Type: plugin.Secret,
						Help: "24 BIP39 seed words, space-separated — omit to paste at a masked prompt, or pipe them in"},
					{Name: "new-passphrase", Type: plugin.Secret, Local: true,
						Help: "encrypt the restored key with this passphrase; omit for none — " +
							"typed explicitly every time, not read from the environment, since a restore is a " +
							"one-off choice rather than a standing credential for the session"},
					{Name: "comment", Type: plugin.String,
						Help: "comment for the restored public key, e.g. me@laptop"},
				},
				Run: runRestore,
			},
		},
	}
}

// refuseMCP is the gate keys.backup and keys.restore both open with —
// mirrors builtin/grant's runAllow/runRevoke exactly, including leaving
// NeedsGrant unset: a grant that could never be exercised over the one
// surface a grant exists to gate would be a standing entry in `grant list`
// that means nothing.
func refuseMCP(req plugin.Request, id string) *view.Error {
	if req.Surface() != plugin.SurfaceMCP {
		return nil
	}
	return view.Errorf("keys.human", "%s can only be run by a person at a terminal", id).
		WithHint("SSH key material leaving this machine as words has no revocation and no per-call log — " +
			"ask the operator to run it themselves")
}

func runList(_ context.Context, _ plugin.Request) (view.View, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, view.Errorf("keys.list.home", "resolving the home directory: %v", err)
	}
	dir := filepath.Join(home, ".ssh")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return view.Text{Body: "No ~/.ssh directory found."}, nil
		}
		return nil, view.Errorf("keys.list.read", "reading %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "id_") || strings.HasSuffix(name, ".pub") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return view.Text{Body: "No private keys found in ~/.ssh."}, nil
	}
	t := view.Table{Columns: []view.Column{
		{Name: "Key"}, {Name: "Type"}, {Name: "Locked"}, {Name: "Backup-eligible"}, {Name: "Fingerprint"},
	}}
	for _, name := range names {
		t.Rows = append(t.Rows, describeKey(filepath.Join(dir, name)))
	}
	t.Total = len(t.Rows)
	return t, nil
}

func runBackup(_ context.Context, req plugin.Request) (view.View, error) {
	if verr := refuseMCP(req, "keys.backup"); verr != nil {
		return nil, verr
	}
	path := req.String("key")
	full := expandHome(path)
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, view.Errorf("keys.backup.unreadable", "reading %s: %v", path, err).
			WithHint("pass the private key file, e.g. ~/.ssh/id_ed25519")
	}
	raw, verr := unlockKey(req, path, data)
	if verr != nil {
		return nil, verr
	}
	priv, err := asEd25519(raw)
	if err != nil {
		return nil, view.Errorf("keys.backup.unsupported", "%s: %v", path, err).
			WithHint("word-based backup supports ed25519 keys only (`ssh-keygen -t ed25519` makes one) — " +
				"RSA and ECDSA keys have no single seed to encode")
	}
	words, err := toMnemonic(priv.Seed())
	if err != nil {
		return nil, view.Errorf("keys.backup.encode", "%v", err)
	}
	fp, err := fingerprint(priv.Public())
	if err != nil {
		return nil, view.Errorf("keys.backup.fingerprint", "%v", err)
	}
	pairs := []view.Pair{
		{Key: "Fingerprint", Value: fp},
		{Key: "Words", Value: words},
	}
	if c := pubComment(full); c != "" {
		pairs = append(pairs, view.Pair{Key: "Comment", Value: c + " (not encoded — pass --comment when restoring to reapply it)"})
	}
	return view.Sections{Items: []view.Section{
		{ID: "backup", Title: "backup", View: view.KeyValue{Pairs: pairs}},
		{ID: "warning", Title: "warning", View: view.Text{
			Body: "These 24 words ARE the private key: anyone who has them can restore it and use it exactly " +
				"as if they had the file. Treat them accordingly — do not paste them into chat, a note-taking " +
				"app, or anywhere else the key file itself would not go. Before trusting a written-down copy, " +
				"verify the fingerprint above against `ssh-keygen -lf " + path + "`.",
		}},
	}}, nil
}

func runRestore(_ context.Context, req plugin.Request) (view.View, error) {
	if verr := refuseMCP(req, "keys.restore"); verr != nil {
		return nil, verr
	}
	out := expandHome(req.String("out"))
	pub := out + ".pub"
	if fileExists(out) {
		return nil, view.Errorf("keys.restore.exists", "%s already exists", out).
			WithHint("name a new file — a restored key is never written over an existing one")
	}
	if fileExists(pub) {
		return nil, view.Errorf("keys.restore.exists", "%s already exists", pub).
			WithHint("name a new file — a restored key is never written over an existing one")
	}

	words, verr := resolveWords(req)
	if verr != nil {
		return nil, verr
	}
	seed, err := fromMnemonic(words)
	if err != nil {
		return nil, view.Errorf("keys.restore.words", "%v", err).
			WithHint("check the words are typed correctly, in order and space-separated, and came from `rta keys backup`")
	}
	if len(seed) != ed25519.SeedSize {
		return nil, view.Errorf("keys.restore.words",
			"decoded %d bytes of entropy, want %d — this is not a 24-word backup made by `rta keys backup`",
			len(seed), ed25519.SeedSize).
			WithHint("`rta keys backup` always makes exactly 24 words")
	}
	priv := ed25519.NewKeyFromSeed(seed)

	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would write %s and %s", out, pub)}, nil
	}

	fp, verr := publishRestoredKey(out, priv, []byte(req.String("new-passphrase")), req.String("comment"))
	if verr != nil {
		return nil, verr
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "Private key", Value: out},
		{Key: "Public key", Value: pub},
		{Key: "Fingerprint", Value: fp},
	}}, nil
}

// resolveWords gets the seed phrase from wherever it was offered: the flag,
// a pipe, or a masked terminal prompt — the same layered resolution
// builtin/kv uses for a passphrase, plus the stdin fallback builtin/debug
// uses for bulk text, since typing a 24-word phrase as a bare CLI argument
// puts the private key it reconstructs into shell history.
func resolveWords(req plugin.Request) (string, *view.Error) {
	if w := req.String("words"); w != "" {
		return w, nil
	}
	piped, verr := readPipedWords(req)
	if verr != nil {
		return "", verr
	}
	if piped != "" {
		return piped, nil
	}
	if canPrompt(req) {
		words, err := promptWords()
		if err == nil && words != "" {
			return words, nil
		}
	}
	return "", view.Errorf("keys.restore.nowords", "no seed words provided").
		WithHint("pass --words, pipe them in, or type them at the prompt")
}
