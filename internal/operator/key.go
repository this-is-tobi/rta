// Package operator is the identity layer of the remote operator channel:
// how a human proves, over a network, that they are the person a server's
// roster names — without any secret the server holds, travels the wire, or
// sits where an agent could take it.
//
// The design is the guard, federated. An operator holds an ed25519 keypair
// whose private half exists only inside age ciphertext under a passphrase
// (internal/passkey, the guard's own mechanics); a server holds a roster of
// public keys and verifies. Every request that reaches a server is signed
// over a server-issued single-use nonce, so possession of anything on disk —
// the key file, a captured envelope, the roster itself — mints nothing:
//
//   - An agent on the operator's machine can read operator.json and call
//     every endpoint; it cannot sign, because signing takes the passphrase
//     and the passphrase arrives only through a prompt or a masked field.
//   - A captured envelope replays nowhere: its nonce is consumed on first
//     sight, and the signature covers the canonical URL of the one server it
//     was aimed at. The nonce alone would not be enough — "a nonce from one
//     server never matches another's" holds only for honest servers, and a
//     hostile server in the operator's own remotes.yaml could present a
//     victim server's challenge as its own and relay the signed result. The
//     URL inside the signed bytes, verified against the server's own
//     --operators-url and never against anything the envelope carries, is
//     what makes that relay verify nowhere.
//   - A compromised server can misenforce its own boundary — the bar a
//     compromised server already sets — but it cannot mint authority in any
//     operator's name, because verification keys are all it ever held.
//
// What this package deliberately is not: an authorization model. One role
// exists — operator — and a roster row's label is attribution for the audit
// trail, not a permission set. docs/23-team-policy.md refuses roles for
// policy with an argument that applies here unchanged; the day a read-only
// auditor row is wanted, it is an annotation on the roster, not a policy
// engine.
package operator

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
	"github.com/this-is-tobi/rule-them-all/internal/filelock"
	"github.com/this-is-tobi/rule-them-all/internal/passkey"
	"github.com/this-is-tobi/rule-them-all/internal/seal"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

const stateFile = "operator.json"

// ScryptWorkFactor overrides age's default passphrase hardening in tests,
// exactly as the guard's knob does; zero keeps the production default.
var ScryptWorkFactor = 0

// state is the on-disk shape — the guard's, verbatim, because it is the same
// trade: the public half in the clear, the private half load-bearing only
// through the passphrase. The file is deliberately separate from guard.json:
// the guard key answers for this machine's grants, the operator key for
// every server that enrolled it, and one passphrase-blast-radius per file is
// the point of not sharing.
type state struct {
	Created time.Time `json:"created"`
	// PublicKey is the ed25519 verification key, base64.
	PublicKey string `json:"publicKey"`
	// Key is the ed25519 signing key, age-encrypted under the operator
	// passphrase (scrypt), base64 of the ciphertext.
	Key string `json:"key"`
}

// Path is where the operator key lives, in the same data directory as every
// other file whose loss is recoverable and whose exposure is not a secret.
func Path() string { return seal.Path(stateFile) }

// Signer signs operator envelopes. Constructible only through Init or
// Unlock, so holding one proves the passphrase was presented — the property
// every server-side verification transitively rests on.
type Signer struct {
	priv        ed25519.PrivateKey
	fingerprint string
}

// Fingerprint names the key this Signer speaks for.
func (s Signer) Fingerprint() string { return s.fingerprint }

// Exists reports whether an operator key has been initialized. Existence
// alone, like guard.Enabled: a corrupt file must read as "there is a key and
// it is broken", never as "no key yet".
func Exists() bool {
	_, err := os.Stat(Path())
	return err == nil
}

func load() (state, *view.Error) {
	var st state
	data, err := os.ReadFile(Path())
	if err != nil {
		return st, view.Errorf("core.operator.off", "no operator key here yet").
			WithHint("rta operator init")
	}
	if err := json.Unmarshal(data, &st); err != nil || st.PublicKey == "" || st.Key == "" {
		return st, view.Errorf("core.operator.corrupt",
			"%s does not parse as an operator key — it has been modified or truncated", Path()).
			WithHint("`rm " + Path() + "` and `rta operator init` mint a fresh key; every server " +
				"roster naming the old one must then be updated, which is the recovery working as designed")
	}
	return st, nil
}

// Fingerprint names the stored key, empty when there is none.
func Fingerprint() string {
	st, verr := load()
	if verr != nil {
		return ""
	}
	pub, err := base64.StdEncoding.DecodeString(st.PublicKey)
	if err != nil {
		return ""
	}
	return passkey.Fingerprint(pub)
}

// Created reports when the key was minted, zero when there is none.
func Created() time.Time {
	st, verr := load()
	if verr != nil {
		return time.Time{}
	}
	return st.Created
}

// RosterLine is the line an operator pastes into a server's roster file to
// enroll this key: "label base64-pubkey", the format LoadRoster reads.
func RosterLine(label string) (string, *view.Error) {
	st, verr := load()
	if verr != nil {
		return "", verr
	}
	return label + " " + st.PublicKey, nil
}

// Init mints the keypair and writes the state, refusing over an existing key
// rather than rotating silently — a new key un-enrolls this operator from
// every roster naming the old one, which is a decision, not a side effect.
func Init(passphrase string) (Signer, *view.Error) {
	release, err := filelock.Acquire(Path()+".lock",
		15*time.Second, 5*time.Millisecond, 10*time.Second)
	if err != nil {
		return Signer{}, view.Errorf("core.operator.locked",
			"another rta is changing the operator key: %v", err)
	}
	defer release()
	if _, err := os.Stat(Path()); err == nil {
		return Signer{}, view.Errorf("core.operator.exists", "an operator key already exists").
			WithHint("`rm " + Path() + "` first, if you mean to rotate it — every server roster " +
				"naming the old key must then be updated")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Signer{}, view.Errorf("core.operator.keygen", "generating the operator key: %v", err)
	}
	wrapped, err := passkey.Wrap(priv, passphrase, ScryptWorkFactor)
	if err != nil {
		return Signer{}, view.Errorf("core.operator.wrap", "wrapping the operator key: %v", err)
	}
	data, err := json.MarshalIndent(state{
		Created:   time.Now().UTC(),
		PublicKey: base64.StdEncoding.EncodeToString(pub),
		Key:       wrapped,
	}, "", "  ")
	if err != nil {
		return Signer{}, view.Errorf("core.operator.write", "encoding the operator key: %v", err)
	}
	if err := atomicfile.Write(Path(), data, 0o600); err != nil {
		return Signer{}, view.Errorf("core.operator.write", "writing %s: %v", Path(), err)
	}
	return Signer{priv: priv, fingerprint: passkey.Fingerprint(pub)}, nil
}

// Unlock decrypts the signing key with the passphrase.
func Unlock(passphrase string) (Signer, *view.Error) {
	st, verr := load()
	if verr != nil {
		return Signer{}, verr
	}
	priv, err := passkey.Unwrap(st.Key, passphrase)
	if err != nil {
		if errors.Is(err, passkey.ErrPassphrase) {
			return Signer{}, view.Errorf("core.operator.passphrase",
				"that is not the operator key's passphrase").
				WithHint("forgotten? `rm " + Path() + "` and `rta operator init` mint a fresh key; " +
					"every server roster naming the old one must then be updated")
		}
		return Signer{}, view.Errorf("core.operator.corrupt",
			"%s carries an unreadable key", Path())
	}
	pub, err := base64.StdEncoding.DecodeString(st.PublicKey)
	if err != nil {
		return Signer{}, view.Errorf("core.operator.corrupt",
			"%s carries an unreadable verification key", Path())
	}
	return Signer{priv: priv, fingerprint: passkey.Fingerprint(pub)}, nil
}

// PassphraseField is the input an operator-gated capability declares so the
// passphrase can arrive through the TUI's masked form field. Local, Secret,
// and with no EnvFallback, for the guard's reason verbatim: nothing an
// agent's environment inherits may satisfy it.
var PassphraseField = plugin.Field{
	Name: "passphrase", Type: plugin.Secret, Local: true,
	Help: "the operator key's passphrase — omit it at a terminal and answer the prompt instead: " +
		"a flag lands in shell history, a prompt lands nowhere",
}

// PromptSecret reads the operator passphrase — the shared passkey flow with
// this key's words, and the same argv refusal.
func PromptSecret(req plugin.Request, confirm bool) (string, *view.Error) {
	return passkey.Prompt(req, confirm, passkey.PromptText{
		Subject: "the operator key",
		Prompt:  "Operator passphrase: ",
		Codes:   "core.operator.passphrase",
		Empty:   "an empty passphrase is not an identity",
	})
}
