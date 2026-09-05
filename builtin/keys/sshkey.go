package keys

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"

	"github.com/this-is-tobi/rta/builtin/internal/sshkeys"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/stdio"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// expandHome resolves a leading ~ the shell would have, for a Path input the
// host does not shell-expand on the caller's behalf. Duplicated from
// builtin/kv/crypt.go's function of the same name rather than shared: two
// built-ins, ten lines, no third caller yet to justify the seam.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// isLocked reports whether a key failed to parse only because it is
// passphrase-protected — mirrors builtin/kv/crypt.go's function of the same
// name and the same reasoning.
func isLocked(err error) bool {
	var locked *ssh.PassphraseMissingError
	return errors.As(err, &locked)
}

// keyPassphraseTries is how many attempts a person gets before this gives
// up, the same count builtin/kv allows for a store's own identity.
const keyPassphraseTries = 3

// promptKeyPassphrase asks for a private key's own passphrase at the
// terminal, naming the file. Overridable in tests.
var promptKeyPassphrase = func(path string) (string, error) {
	fmt.Fprintf(os.Stderr, "Passphrase for %s: ", path)
	secret, err := term.ReadPassword(int(stdio.Real().Fd()))
	fmt.Fprintln(os.Stderr)
	return string(secret), err
}

// promptWords asks for the BIP39 seed phrase at the terminal, masked like a
// passphrase: the words reconstruct the private key exactly, so they get the
// same "never lands on the screen or in scrollback" treatment. Overridable in
// tests.
var promptWords = func() (string, error) {
	fmt.Fprint(os.Stderr, "Seed words: ")
	secret, err := term.ReadPassword(int(stdio.Real().Fd()))
	fmt.Fprintln(os.Stderr)
	return string(secret), err
}

// readPipedWords returns "" — not an error — whenever there is nothing to
// read rather than something merely absent: a non-CLI surface (moot today,
// since keys.restore is HumanOnly and the TUI never reaches this
// function through its own masked form field) or a CLI call with a real
// terminal behind it, where reading would block on a person who is never
// going to send EOF. Mirrors builtin/debug's readPipedStdin.
func readPipedWords(req plugin.Request) (string, *view.Error) {
	if req.Surface() != plugin.SurfaceCLI {
		return "", nil
	}
	f := stdio.Real()
	if term.IsTerminal(int(f.Fd())) {
		return "", nil
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", view.Errorf("keys.restore.stdin", "reading stdin: %v", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// canPrompt reports whether this request can reach a person at a terminal —
// mirrors builtin/kv's function of the same name. Only a CLI request can:
// MCP has no terminal at the other end (moot here regardless, since every
// capability in this package is HumanOnly), and the TUI owns
// the screen and asks through a masked form field instead.
// A var, like builtin/kv's canPrompt, so a test can force the prompt path
// deterministically rather than depend on whether `go test` itself has a
// terminal on the other end of stdio.Real().
var canPrompt = func(req plugin.Request) bool {
	return req.Surface() == plugin.SurfaceCLI && term.IsTerminal(int(stdio.Real().Fd()))
}

// unlockKey parses an SSH private key, prompting for its passphrase when it
// is protected and there is somebody at a terminal to ask — the same
// judgment call builtin/kv makes for the identity it unlocks the store with,
// applied here to the key being backed up instead of the key doing the
// unlocking.
//
// A locked key that is not ed25519 is rejected before the first prompt, not
// after the passphrase is typed. The OpenSSH private-key container carries
// its own public key in cleartext regardless of encryption — PassphraseMissingError.PublicKey
// exposes it with no decryption at all — so the type is already knowable
// before asking anybody for anything. Found by review: the
// original version prompted up to three times for an RSA key's real
// passphrase, then rejected it as unsupported no matter what was typed.
func unlockKey(req plugin.Request, path string, data []byte) (any, *view.Error) {
	raw, err := ssh.ParseRawPrivateKey(data)
	if err == nil {
		return raw, nil
	}
	var locked *ssh.PassphraseMissingError
	if !errors.As(err, &locked) {
		return nil, view.Errorf("keys.key.invalid", "parsing %s: %v", path, err).
			WithHint("expected an SSH private key, e.g. ~/.ssh/id_ed25519")
	}
	if locked.PublicKey != nil && locked.PublicKey.Type() != ssh.KeyAlgoED25519 {
		return nil, view.Errorf("keys.backup.unsupported", "%s: unsupported key type %s", path, locked.PublicKey.Type()).
			WithHint("word-based backup supports ed25519 keys only (`ssh-keygen -t ed25519` makes one) — " +
				"RSA and ECDSA keys have no single seed to encode")
	}
	try := func(pass string) (any, bool) {
		if pass == "" {
			return nil, false
		}
		raw, err := ssh.ParseRawPrivateKeyWithPassphrase(data, []byte(pass))
		return raw, err == nil
	}
	if raw, ok := try(req.String("passphrase")); ok {
		return raw, nil
	}
	if canPrompt(req) {
		for range keyPassphraseTries {
			pass, err := promptKeyPassphrase(path)
			if err != nil || pass == "" {
				break
			}
			if raw, ok := try(pass); ok {
				return raw, nil
			}
			fmt.Fprintln(os.Stderr, "Wrong passphrase.")
		}
		return nil, view.Errorf("keys.key.locked", "could not unlock %s", path).
			WithHint("that is the key's own passphrase — pass --passphrase or set " +
				plugin.LocalEnvVar("keys.backup", "passphrase"))
	}
	return nil, view.Errorf("keys.key.locked", "%s is passphrase-protected", path).
		WithHint("set " + plugin.LocalEnvVar("keys.backup", "passphrase") + " or pass --passphrase")
}

// probeKey reads a private key file and reports what can be learned without
// its passphrase: whether it is locked, and — either way — its public key,
// if one could be determined. An unlocked key answers from a real parse.
// A locked key answers from the OpenSSH container's own embedded, cleartext
// public key (PassphraseMissingError.PublicKey), which every ed25519 key
// carries — there is no other on-disk format for one — and which
// `ssh-keygen` has written for RSA/ECDSA keys since the OpenSSH format
// became the default in 2018. Never attempts a passphrase; a locked key
// whose container predates that field (nil) reports unknown rather than
// guessing.
func probeKey(path string) (locked bool, pub ssh.PublicKey) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	raw, err := ssh.ParseRawPrivateKey(data)
	if err == nil {
		if pk, ok := publicKeyOf(raw); ok {
			if sshPub, err := ssh.NewPublicKey(pk); err == nil {
				return false, sshPub
			}
		}
		return false, nil
	}
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		return true, missing.PublicKey
	}
	return false, nil
}

// asEd25519 narrows a raw parsed key to the one type word-based backup
// supports. ssh.ParseRawPrivateKey returns *ed25519.PrivateKey for a key in
// the OpenSSH container format — the only format ed25519 keys are ever
// stored in — and ed25519.PrivateKey (no pointer) for one that came from a
// PKCS8 "PRIVATE KEY" block instead, which only the unencrypted parse path
// can produce (ParseRawPrivateKeyWithPassphrase does not decrypt that
// format). Both forms are handled, mirroring builtin/kv's ageIdentityFromSSH.
func asEd25519(raw any) (ed25519.PrivateKey, error) {
	switch k := raw.(type) {
	case *ed25519.PrivateKey:
		return *k, nil
	case ed25519.PrivateKey:
		return k, nil
	default:
		return nil, fmt.Errorf("unsupported key type %T", raw)
	}
}

// publicKeyOf extracts the public half of whatever ssh.ParseRawPrivateKey
// returned, without knowing which concrete key type it is. Every key type
// x/crypto/ssh can parse exposes Public() crypto.PublicKey — ed25519 and
// ecdsa on a value receiver, rsa on a pointer receiver — and Go's method sets
// put a value-receiver method on the pointer type too, so this one assertion
// covers every case ssh.ParseRawPrivateKey can hand back.
func publicKeyOf(raw any) (crypto.PublicKey, bool) {
	k, ok := raw.(interface{ Public() crypto.PublicKey })
	if !ok {
		return nil, false
	}
	return k.Public(), true
}

// fingerprint renders a public key the way `ssh-keygen -lf` does, so a
// person can check a backup or a restore against a command they already
// trust rather than against this package's own say-so.
func fingerprint(pub crypto.PublicKey) (string, error) {
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("deriving the public key: %w", err)
	}
	return ssh.FingerprintSHA256(sshPub), nil
}

// pubComment reads the comment off a private key's .pub sibling — the
// user@host an authorized_keys line carries after the key material itself.
// Public data only; never touches the private key.
func pubComment(privPath string) string {
	data, err := os.ReadFile(privPath + ".pub")
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return ""
	}
	return strings.Join(fields[2:], " ")
}

// describeKey reports what can be learned about a private key without ever
// decrypting it: type, fingerprint and comment from its .pub sibling when
// there is one, or — only when the key itself turns out to be unencrypted —
// from the private key file directly, since parsing a key that needs no
// passphrase exposes nothing a person could not already read themselves. A
// locked key with no .pub sibling is reported as locked and nothing more:
// asking for its passphrase to answer a routine listing question is not
// this capability's business.
func describeKey(path string) []string {
	locked, probed := probeKey(path)
	var keyType, fp string
	// The .pub sibling wins when it parses: it is the only source with a
	// comment, and matches what the key's owner actually put there. Anything
	// else — no sibling, or one that exists but will not parse — falls back
	// to probeKey's answer, which is safe and available either way (real
	// parse when unlocked, the container's own embedded public key when
	// locked). A corrupt .pub used to leave a perfectly good key reported as
	// unknown; found by review.
	if data, err := os.ReadFile(path + ".pub"); err == nil {
		if parsed, _, _, _, err := ssh.ParseAuthorizedKey(data); err == nil {
			keyType, fp = parsed.Type(), ssh.FingerprintSHA256(parsed)
		}
	}
	if keyType == "" && probed != nil {
		keyType, fp = probed.Type(), ssh.FingerprintSHA256(probed)
	}
	eligible := "no"
	switch {
	case keyType == ssh.KeyAlgoED25519:
		eligible = "yes"
	case keyType == "":
		eligible = "unknown"
	}
	lockedCell := "no"
	if locked {
		lockedCell = "yes"
	}
	if keyType == "" {
		keyType = "unknown"
	}
	if fp == "" {
		fp = "-"
	}
	return []string{path, keyType, lockedCell, eligible, fp}
}

// suggestComment offers what a public key's comment almost always is:
// <user>@<host> for the machine the key is being restored onto.
//
// The comment is not encoded in the seed words and is lost on backup — the
// restore output says so — so it is always being retyped from memory, and it
// is the one field here whose right answer this process already knows. Nothing
// is read but the process's own identity, so it costs nothing on a keystroke.
func suggestComment(_ context.Context, _ plugin.Request) []string {
	u, err := user.Current()
	if err != nil || u.Username == "" {
		return nil
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return nil
	}
	// The short form, matching what ssh-keygen writes: a fully-qualified name
	// in a key comment is noise nobody types.
	host, _, _ = strings.Cut(host, ".")
	return []string{u.Username + "@" + host + "\tthis user on this machine"}
}

// suggestPrivateKeys offers the private keys this machine already has: id_*
// files in ~/.ssh that are not themselves a .pub file. Reads directory
// entries only, never a key's contents — a completion must stay silent and
// side-effect free, and parsing a key would be enough to trigger a
// passphrase prompt on a keystroke.
func suggestPrivateKeys(_ context.Context, _ plugin.Request) []string {
	return sshkeys.PrivateKeys(sshDir())
}

// sshDir is ~/.ssh, or empty when there is no home to resolve — which every
// caller here treats as "no keys", the same answer an unreadable directory
// gives.
func sshDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh")
}

// publishRestoredKey writes the reconstructed private key and its .pub
// sibling, refusing to overwrite either.
//
// atomicfile.Publish rather than a hand-rolled O_EXCL write: its own doc
// comment names exactly this discipline as "the discipline a key file
// needs". The private key is written first; if the .pub write then loses a
// race that the earlier existence check missed, the mismatch is reported by
// name rather than silently discarded, since by that point the private key
// really is on disk and pretending otherwise would be the lie.
func publishRestoredKey(privPath string, priv ed25519.PrivateKey, passphrase []byte, comment string) (fp string, verr *view.Error) {
	var block *pem.Block
	var err error
	if len(passphrase) == 0 {
		block, err = ssh.MarshalPrivateKey(priv, comment)
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, comment, passphrase)
	}
	if err != nil {
		return "", view.Errorf("keys.restore.marshal", "encoding the private key: %v", err)
	}
	privBytes := pem.EncodeToMemory(block)

	sshPub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		return "", view.Errorf("keys.restore.marshal", "deriving the public key: %v", err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	if comment != "" {
		line += " " + comment
	}
	pubBytes := []byte(line + "\n")

	written, err := atomicfile.Publish(privPath, privBytes, 0o600)
	if err != nil {
		return "", view.Errorf("keys.restore.write", "writing %s: %v", privPath, err)
	}
	if !bytes.Equal(written, privBytes) {
		return "", view.Errorf("keys.restore.exists", "%s already exists", privPath)
	}

	pubPath := privPath + ".pub"
	writtenPub, err := atomicfile.Publish(pubPath, pubBytes, 0o644)
	if err != nil {
		return "", view.Errorf("keys.restore.write", "writing %s: %v", pubPath, err)
	}
	if !bytes.Equal(writtenPub, pubBytes) {
		// "Restore again" alone cannot fix this: privPath is already
		// written and durable (Link, not Rename — nothing here rolls it
		// back), so a retry at the same out immediately hits runRestore's
		// own fileExists(out) guard before ever reaching this function
		// again. The hint has to name both files, not just the stray one.
		// Found by review: the original text sent the
		// operator toward an instruction that could not succeed as given.
		return "", view.Errorf("keys.restore.exists", "%s already exists — %s was already written and is not removed automatically",
			pubPath, privPath).
			WithHint(fmt.Sprintf("move or remove both %s and %s, or restore to a different path, then try again", privPath, pubPath))
	}
	return ssh.FingerprintSHA256(sshPub), nil
}
