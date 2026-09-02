// Package passkey is the one shape the guard and the operator identity
// share: an ed25519 signing key that exists on disk only inside age
// ciphertext under a passphrase (scrypt), and a passphrase that arrives
// through a prompt or a masked Secret field — never through the environment,
// and never on argv.
//
// It exists because internal/guard's own comment names the failure mode of
// copying this logic instead: "two implementations of 'how the passphrase
// arrives' is how one of them grows an env fallback someday." The guard and
// the operator key make the same trade for the same reason — nothing an
// agent inherits may satisfy them — so the channel discipline lives once,
// here, and each caller supplies only its own words and error codes. The kv
// store is deliberately not a caller: its env fallback is its design, and
// folding it in would put the one place an unlock may be inherited next to
// the two places it must never be.
package passkey

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"filippo.io/age"
	"golang.org/x/term"

	"github.com/this-is-tobi/rule-them-all/internal/stdio"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Fingerprint names a verification key in eight hex characters: enough to
// notice a key that changed, short enough for a table cell. It lives here
// because both keys that wear it must compute it identically — the operator
// fingerprint a client prints is the string a server's roster looks up, so
// two implementations would be an interop bug waiting for its first rotation.
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:4])
}

// ErrPassphrase marks an unwrap that failed on the passphrase itself rather
// than on the ciphertext's encoding. age cannot tell a wrong passphrase from
// corrupt ciphertext, but a stored key that base64-decoded and then failed
// to open is overwhelmingly the former, and the callers owe the operator
// "wrong passphrase" rather than "corrupt file" for it.
var ErrPassphrase = errors.New("that is not the passphrase")

// Wrap encrypts priv under passphrase, returning base64 of the age
// ciphertext. workFactor overrides age's scrypt default when positive — the
// default is the point in production and a tax in a test loop, the same
// knob builtin/kv/crypt.go and the guard expose.
func Wrap(priv ed25519.PrivateKey, passphrase string, workFactor int) (string, error) {
	rec, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return "", err
	}
	if workFactor > 0 {
		rec.SetWorkFactor(workFactor)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, rec)
	if err == nil {
		_, err = w.Write(priv)
	}
	if err == nil {
		err = w.Close()
	}
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// Unwrap decrypts a Wrap-produced ciphertext. A failure that means "wrong
// passphrase" wraps ErrPassphrase; anything else means the stored bytes are
// not what Wrap wrote.
func Unwrap(cipher, passphrase string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(cipher)
	if err != nil {
		return nil, err
	}
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, err
	}
	r, err := age.Decrypt(bytes.NewReader(raw), id)
	if err != nil {
		return nil, ErrPassphrase
	}
	priv, err := io.ReadAll(r)
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return nil, ErrPassphrase
	}
	return ed25519.PrivateKey(priv), nil
}

// PromptText is the wording one caller's prompt carries: the flow is shared,
// the words are not, so a refusal names the key it is actually about.
type PromptText struct {
	// Subject is the key's name as the subject of a sentence: "the guard",
	// "the operator key".
	Subject string
	// Prompt is what the terminal asks, e.g. "Guard passphrase: ".
	Prompt string
	// Codes is the dotted error-code prefix, e.g. "core.guard.passphrase";
	// refusals append .argv, .required, .read, .empty, .mismatch.
	Codes string
	// Empty is the whole refusal for an empty answer, in the caller's words.
	Empty string
}

// Prompt reads a passphrase for a gated capability: the request's own Secret
// field first (the TUI's masked input), then a terminal prompt when a person
// is there to answer one — echo off, prompt to stderr so an eval'd or
// redirected command never captures it, read from the real stdin because
// main repoints os.Stdin at /dev/null before spawning plugins. confirm asks
// twice, for the moments where a typo becomes a passphrase nobody knows.
//
// A non-empty field value is refused on the CLI surface outright: it
// travelled on argv, which every process running as you can read while the
// command runs and which shell history keeps when it ends. The TUI's masked
// field and the prompt are the channels that land nowhere.
func Prompt(req plugin.Request, confirm bool, text PromptText) (string, *view.Error) {
	if p := req.String("passphrase"); p != "" {
		if req.Surface() == plugin.SurfaceCLI {
			return "", view.Errorf(text.Codes+".argv",
				"the passphrase must not travel on the command line — argv is readable by "+
					"every process you run, and shell history keeps it").
				WithHint("run again without --passphrase and answer the prompt")
		}
		return p, nil
	}
	if req.Surface() != plugin.SurfaceCLI || !term.IsTerminal(int(stdio.Real().Fd())) {
		return "", view.Errorf(text.Codes+".required",
			"%s needs its passphrase, and there is no terminal to ask at", text.Subject).
			WithHint("run this at a terminal, or fill the passphrase field in the TUI form")
	}
	read := func(prompt string) (string, *view.Error) {
		fmt.Fprint(os.Stderr, prompt)
		secret, err := term.ReadPassword(int(stdio.Real().Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", view.Errorf(text.Codes+".read", "reading the passphrase: %v", err)
		}
		return string(secret), nil
	}
	p, verr := read(text.Prompt)
	if verr != nil {
		return "", verr
	}
	if strings.TrimSpace(p) == "" {
		return "", view.Errorf(text.Codes+".empty", "%s", text.Empty)
	}
	if confirm {
		again, verr := read("Once more: ")
		if verr != nil {
			return "", verr
		}
		if p != again {
			return "", view.Errorf(text.Codes+".mismatch",
				"the two answers differ — nothing was changed")
		}
	}
	return p, nil
}
