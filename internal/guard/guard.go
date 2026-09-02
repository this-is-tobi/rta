// Package guard holds the operator passphrase gate for authority-expanding
// actions: a signing key that exists only wrapped under a passphrase, and the
// verification key beside it in the clear.
//
// **What it changes, exactly.** The grant seal (internal/seal) stops a writer
// that cannot read the data directory, and its own comment concedes the rest:
// same-uid means no on-disk secret is a secret, so an agent that can run
// commands simply asks rta to write a real grant, and the seal stamps it
// authentic. The guard's key is the one secret that is *not* on disk — it is
// wrapped under a passphrase that lives in the operator's head — so the
// ordinary attack, literally running `rta grant allow`, stops working: rta
// cannot produce a valid signature without being told the passphrase, and
// neither can the agent, however it invokes the binary. That is prevention
// for the path that actually happens, which is more than the origin field's
// detection and is claimed for exactly as much as it covers.
//
// **What it does not change.** A same-uid process can still tamper with the
// files — against something running as you there is nowhere tamper-proof to
// stand (docs/19-the-boundary.md owns that argument). Every *inconsistent*
// rewrite is refused loudly and fails closed: a signed grant with no guard
// state beside it, an unsigned grant while the guard is on, a signature the
// key does not verify — each kills the whole file's authority, never part of
// it. The cheapest defeat is worth naming plainly rather than hiding behind
// "custom tooling": deleting BOTH files makes the machine look like the
// guard was never enabled, and nothing on disk can contradict that, because
// anything that could is a file the same attacker deletes. Two answers, each
// covering what it can: a running MCP server takes a Pin of the guard state
// at startup and refuses every grant-gated call if the guard it started
// under disappears or changes key — the attacker talks *through* that
// process, so its memory is the one place a same-uid rm cannot reach. Across
// restarts, no on-disk defense exists; the harness-side deny list
// (`rta audit agents --fix`) and the operator noticing a guard they enabled
// reading "off" are what remain, and the docs say so.
//
// **No environment channel, by design.** The kv store accepts its passphrase
// from RTA_KV_PASSPHRASE because a store that cannot open unattended is a
// store some setups cannot use; the doctor then warns that an MCP server
// started from that environment inherits the unlock. The guard exists for
// the opposite trade: issuance is rare, always attended, and the entire
// point is that nothing an agent inherits can satisfy it. A passphrase
// arrives through a prompt or a Secret form field, and never through the
// environment.
package guard

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/filelock"

	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
	"github.com/this-is-tobi/rule-them-all/internal/passkey"
	"github.com/this-is-tobi/rule-them-all/internal/seal"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

const stateFile = "guard.json"

// ScryptWorkFactor overrides age's default passphrase hardening, exactly as
// builtin/kv/crypt.go does for the store: the default work factor is the
// point in production and a tax in a test loop. Zero keeps age's default.
// Exported because the grant and capability suites enable the guard in their
// own processes; it shapes only what Enable writes in *this* process, so
// nothing that sets it changes what an existing state file costs to unlock.
var ScryptWorkFactor = 0

// state is the on-disk shape. The public key is in the clear — verification
// must work for a server nobody is standing at — and the private key exists
// only inside the age ciphertext, which is what makes the passphrase
// load-bearing rather than decorative.
type state struct {
	Created time.Time `json:"created"`
	// PublicKey is the ed25519 verification key, base64.
	PublicKey string `json:"publicKey"`
	// Key is the ed25519 signing key, age-encrypted under the operator
	// passphrase (scrypt), base64 of the ciphertext.
	Key string `json:"key"`
}

// Path is where the guard state lives: beside the grant file it governs, for
// the same reason grants.key lives there — they stand and fall together, and
// a backup that carries one without the other is a machine that fails closed.
func Path() string { return seal.Path(stateFile) }

// signContext domain-separates guard signatures from any other ed25519 use
// this key could ever be put to. Versioned so a future change to what is
// signed cannot be replayed against an older reader.
const signContext = "rta.guard.grant.v1\x00"

// Signer signs authority bytes with the unwrapped key. It exists as a type
// rather than a bare closure so a caller holding one has, provably, already
// presented the passphrase — the only way to construct it is Unlock or
// Enable.
type Signer struct {
	priv ed25519.PrivateKey
}

// Sign returns the base64 signature over msg.
func (s Signer) Sign(msg []byte) string {
	return base64.StdEncoding.EncodeToString(
		ed25519.Sign(s.priv, append([]byte(signContext), msg...)))
}

// Pin is the guard state as one process observed it at startup, held in
// memory where a same-uid rm cannot reach. An MCP server takes one before
// serving and checks it before honouring any grant: the rollback that
// deletes the guard's files mid-session then reads as tampering to the one
// process the attacker is actually talking through, instead of succeeding
// silently. It only ever subtracts — a Pin taken with the guard off checks
// nothing, so enabling mid-session still works and still strengthens.
type Pin struct {
	enabled     bool
	fingerprint string
}

// TakePin snapshots the current guard state.
func TakePin() Pin { return Pin{enabled: Enabled(), fingerprint: Fingerprint()} }

// Check refuses when the guard this Pin saw has been weakened: disabled, or
// its key replaced. Nil when the Pin saw no guard — the off→on direction is
// an operator strengthening their machine, not an attack.
func (p Pin) Check() *view.Error {
	if !p.enabled {
		return nil
	}
	if !Enabled() || Fingerprint() != p.fingerprint {
		return view.Errorf("core.guard.pinned",
			"the guard this server started under is gone or changed key — refusing every "+
				"grant-gated call until an operator looks").
			WithHint("if you disabled the guard yourself, restart the server; if you did not, " +
				"something else removed it — `rta doctor` and `rta agent log` are where to look")
	}
	return nil
}

// Enabled reports whether the guard is on: the state file exists. Existence
// alone, deliberately — a file that exists but does not parse still reads as
// enabled, because Enabled=true only ever *refuses* things, and a corrupt
// state must not read as "the guard was never set up". Verify then fails on
// the corruption, which lands closed.
func Enabled() bool {
	_, err := os.Stat(Path())
	return err == nil
}

// load reads and parses the state, refusing corruption loudly.
func load() (state, *view.Error) {
	var st state
	data, err := os.ReadFile(Path())
	if err != nil {
		return st, view.Errorf("core.guard.off", "the guard is not enabled").
			WithHint("rta grant guard on")
	}
	if err := json.Unmarshal(data, &st); err != nil || st.PublicKey == "" || st.Key == "" {
		return st, view.Errorf("core.guard.corrupt",
			"%s does not parse as guard state — it has been modified or truncated", Path()).
			WithHint("no grant is honoured while this stands; `rm " + Path() + " " + grantsHint() +
				"` starts clean, then `rta grant guard on` again")
	}
	return st, nil
}

// Fingerprint names the verification key in eight hex characters, for the
// status and doctor surfaces: enough to notice a key that changed, short
// enough to sit in a table cell.
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

// Created reports when the guard was enabled, zero when it is not.
func Created() time.Time {
	st, verr := load()
	if verr != nil {
		return time.Time{}
	}
	return st.Created
}

// Verifier returns a verify function bound to the state as it stands,
// reading it once. The grant store checks every stored row on every read —
// which is the path of every gated MCP call — and re-opening the state per
// row would put N file reads where one belongs. A corrupt state is an error
// here rather than a silent false, so the reader can refuse loudly instead
// of reporting a tampered file as "no grants issued".
func Verifier() (func(msg []byte, sig string) bool, *view.Error) {
	st, verr := load()
	if verr != nil {
		return nil, verr
	}
	pub, err := base64.StdEncoding.DecodeString(st.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, view.Errorf("core.guard.corrupt",
			"%s carries an unreadable verification key", Path()).
			WithHint("no grant is honoured while this stands; `rm " + Path() + " " + grantsHint() +
				"` starts clean, then `rta grant guard on` again")
	}
	key := ed25519.PublicKey(pub)
	return func(msg []byte, sig string) bool {
		raw, err := base64.StdEncoding.DecodeString(sig)
		if err != nil {
			return false
		}
		return ed25519.Verify(key, append([]byte(signContext), msg...), raw)
	}, nil
}

// Verify reports whether sig is this guard's signature over msg. False when
// the guard is off or its state is corrupt — the callers treat false as
// "not honoured", so every failure mode lands closed.
func Verify(msg []byte, sig string) bool {
	v, verr := Verifier()
	if verr != nil {
		return false
	}
	return v(msg, sig)
}

// PassphraseField is the input a guard-gated capability declares so the
// passphrase can arrive through the TUI's masked form field. Local and
// Secret, and — unlike kv's passphraseField — with NO EnvFallback: the guard
// exists so that nothing an agent's environment inherits can satisfy it.
// Beside PromptSecret because the two are one contract: the prompt reads
// exactly the field this declares.
var PassphraseField = plugin.Field{
	Name: "passphrase", Type: plugin.Secret, Local: true,
	Help: "the guard passphrase — omit it at a terminal and answer the prompt instead: " +
		"a flag lands in shell history, a prompt lands nowhere",
}

// PromptSecret reads the passphrase for a guard-gated capability: the
// request's own Secret field first (the TUI's masked input), then a prompt
// when a person is there to answer one — builtin/kv's promptPassphrase
// shape: echo off, prompt to stderr so an eval'd or redirected command never
// captures it, read from the real stdin because main repoints os.Stdin at
// /dev/null before spawning plugins. confirm asks twice, for the one moment
// (enabling) where a typo becomes a passphrase nobody knows.
//
// It lives in internal/passkey rather than beside one capability because
// more than one key gates on it — grant issuance, the consent prompt's
// --ttl branch, the operator identity — and two implementations of "how the
// passphrase arrives" is how one of them grows an env fallback someday.
// There deliberately is none: nothing an agent's environment inherits may
// satisfy the guard.
func PromptSecret(req plugin.Request, confirm bool) (string, *view.Error) {
	return passkey.Prompt(req, confirm, passkey.PromptText{
		Subject: "the guard",
		Prompt:  "Guard passphrase: ",
		Codes:   "core.guard.passphrase",
		Empty:   "an empty passphrase is not a guard",
	})
}

// Enable generates the keypair, wraps the private half under passphrase, and
// writes the state. It returns the Signer so the enabling command can sign
// in the same breath, without a second prompt.
//
// It refuses over existing state rather than rotating silently: a new key
// invalidates every signature the old one made, and that is a decision the
// operator takes by disabling first, not a side effect of running enable
// twice.
func Enable(passphrase string) (Signer, *view.Error) {
	unlock, verr := transitionLock()
	if verr != nil {
		return Signer{}, verr
	}
	defer unlock()
	if _, err := os.Stat(Path()); err == nil {
		return Signer{}, view.Errorf("core.guard.exists", "the guard is already enabled").
			WithHint("`rta grant guard off` first, if you mean to rotate the passphrase")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Signer{}, view.Errorf("core.guard.keygen", "generating the guard key: %v", err)
	}
	wrapped, err := passkey.Wrap(priv, passphrase, ScryptWorkFactor)
	if err != nil {
		return Signer{}, view.Errorf("core.guard.wrap", "wrapping the guard key: %v", err)
	}
	data, err := json.MarshalIndent(state{
		Created:   time.Now().UTC(),
		PublicKey: base64.StdEncoding.EncodeToString(pub),
		Key:       wrapped,
	}, "", "  ")
	if err != nil {
		return Signer{}, view.Errorf("core.guard.write", "encoding guard state: %v", err)
	}
	// 0600 enforced for the reason grants.json states: what this file holds
	// decides what is honoured, and a permissive umask must not widen it.
	if err := atomicfile.Write(Path(), data, 0o600); err != nil {
		return Signer{}, view.Errorf("core.guard.write", "writing %s: %v", Path(), err)
	}
	return Signer{priv: priv}, nil
}

// Unlock decrypts the signing key with the passphrase. A wrong passphrase is
// its own error code so the surfaces can say "wrong passphrase" rather than
// "corrupt file" — age cannot tell those apart from the ciphertext alone,
// but a state file that parsed and then failed to open is overwhelmingly the
// former.
func Unlock(passphrase string) (Signer, *view.Error) {
	st, verr := load()
	if verr != nil {
		return Signer{}, verr
	}
	priv, err := passkey.Unwrap(st.Key, passphrase)
	if err != nil {
		if errors.Is(err, passkey.ErrPassphrase) {
			return Signer{}, view.Errorf("core.guard.passphrase", "that is not the guard's passphrase").
				WithHint("forgotten? `rm " + Path() + " " + grantsHint() + "` starts clean — every grant " +
					"is cleared, and grants last a day at most, so what is lost is re-issued in minutes")
		}
		return Signer{}, view.Errorf("core.guard.corrupt",
			"%s carries an unreadable key", Path())
	}
	return Signer{priv: priv}, nil
}

// Disable removes the state after proving the passphrase. Deleting the file
// without it is possible for anything running as you — and is exactly the
// rewrite loadAll refuses when signed grants are left behind — so this path
// exists to make the *legitimate* way off cost what turning it on promised:
// the secret.
func Disable(passphrase string) *view.Error {
	unlock, verr := transitionLock()
	if verr != nil {
		return verr
	}
	defer unlock()
	if _, verr := Unlock(passphrase); verr != nil {
		return verr
	}
	if err := os.Remove(Path()); err != nil {
		return view.Errorf("core.guard.write", "removing %s: %v", Path(), err)
	}
	return nil
}

// transitionLock serializes Enable/Disable against each other across
// processes. Without it two concurrent enables both pass the exists check,
// both write, and the loser holds a signer whose signatures then read as
// forged — a false alarm this lock is cheaper than. Same constants as the
// grant store's own lock, same reasoning.
func transitionLock() (func(), *view.Error) {
	release, err := filelock.Acquire(Path()+".lock",
		15*time.Second, 5*time.Millisecond, 10*time.Second)
	if err != nil {
		return nil, view.Errorf("core.guard.locked",
			"another rta is changing the guard state: %v", err)
	}
	return release, nil
}

// grantsHint names the grant file for recovery hints. Spelled here rather
// than importing internal/grant (which imports this package): the two files
// live beside each other by construction, and a recovery that removes the
// guard state but not the grants it signed strands the operator on a second
// error — the orphaned-grants refusal — whose hint is the one that works.
func grantsHint() string { return seal.Path("grants.json") }
