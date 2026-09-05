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
// stand (docs/30-boundary/10-the-boundary.md owns that argument). Every *inconsistent*
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
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/internal/filelock"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/passkey"
	"github.com/this-is-tobi/rta/internal/seal"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

const stateFile = "guard.json"

// ScryptWorkFactor overrides age's default passphrase hardening, exactly as
// builtin/kv/crypt.go does for the store: the default work factor is the
// point in production and a tax in a test loop. Zero keeps age's default.
// Exported because the grant and capability suites enable the guard in their
// own processes; it shapes only what Enable writes in *this* process, so
// nothing that sets it changes what an existing state file costs to unlock.
var ScryptWorkFactor = 0

// state is the on-disk shape, in one of two modes.
//
// Local — PublicKey and Key set: the guard as first built. The public key is
// in the clear — verification must work for a server nobody is standing at —
// and the private key exists only inside the age ciphertext, which is what
// makes the passphrase load-bearing rather than decorative.
//
// Remote — Operators set, no key material at all: the guard for a machine
// whose humans are elsewhere. Every listed public key may sign grants (the
// remote operator channel's issuance path), and *nothing on this machine
// can*: there is no ciphertext to steal, no passphrase to phish out of a
// server, and `rta grant allow` at the machine's own shell has no key to
// unlock — which on a remote server is the boundary working, not a gap.
//
// The two modes are exclusive: a state carrying both a local key and an
// operator list reads as corrupt rather than as a hybrid nobody designed.
type state struct {
	Created time.Time `json:"created"`
	// PublicKey is the local ed25519 verification key, base64.
	PublicKey string `json:"publicKey,omitempty"`
	// Key is the local ed25519 signing key, age-encrypted under the operator
	// passphrase (scrypt), base64 of the ciphertext.
	Key string `json:"key,omitempty"`
	// Operators are the remote keys this machine honours grant signatures
	// from, imported from a roster file by `rta grant guard remote`.
	Operators []OperatorKey `json:"operators,omitempty"`
	// Server is this machine's canonical URL, remote mode only: the value
	// every honoured grant's signed authority must carry in its own Server
	// field. Without it, one operator key enrolled on two servers would make
	// a grant signed for staging byte-for-byte valid on prod — an agent on
	// either machine could transplant the row, and remote mode's "nothing
	// on this machine can mint" claim would be false the day a fleet shares
	// a roster. The client anchors the same value independently: it refuses
	// to sign authority naming any server but the one it dialed.
	Server string `json:"server,omitempty"`
}

// OperatorKey is one enrolled remote signer: a label for the surfaces that
// answer "who may issue here", and the base64 ed25519 verification key.
type OperatorKey struct {
	Label     string `json:"label"`
	PublicKey string `json:"publicKey"`
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

// SignerFor wraps an already-unwrapped private key in a Signer. It exists
// for exactly one caller: internal/operator's unlock path, where a remote
// operator's key — presented through the same passphrase discipline as the
// guard's own — signs grant authority for a server whose guard enrolls it.
// The invariant on Signer holds transitively: every path to a raw private
// key in this codebase costs a passphrase, and a new caller that does not is
// a review finding, not a convenience.
func SignerFor(priv ed25519.PrivateKey) Signer { return Signer{priv: priv} }

// Pin is the guard state as one process observed it at startup, held in
// memory where a same-uid rm cannot reach. An MCP server takes one before
// serving and checks it before honouring any grant: the rollback that
// deletes the guard's files mid-session then reads as tampering to the one
// process the attacker is actually talking through, instead of succeeding
// silently. It only ever subtracts — a Pin taken with the guard off checks
// nothing, so enabling mid-session still works and still strengthens.
type Pin struct {
	enabled bool
	digest  string
}

// TakePin snapshots the current guard state. It pins trustDigest — the
// full-width hash — and never the eight-hex display fingerprint: a 32-bit
// comparison is a target a same-uid attacker can collide offline (2³²
// keygens against a check that is the one mid-session defense), while the
// display constraint that justifies truncating for a table cell does not
// apply to a value held in memory.
func TakePin() Pin { return Pin{enabled: Enabled(), digest: trustDigest()} }

// Check refuses when the guard this Pin saw has been weakened: disabled, or
// its key replaced. Nil when the Pin saw no guard — the off→on direction is
// an operator strengthening their machine, not an attack.
func (p Pin) Check() *view.Error {
	if !p.enabled {
		return nil
	}
	if !Enabled() || trustDigest() != p.digest {
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
	if err := json.Unmarshal(data, &st); err != nil {
		return st, corruptState()
	}
	local := st.PublicKey != "" && st.Key != "" && len(st.Operators) == 0 && st.Server == ""
	remote := len(st.Operators) > 0 && st.PublicKey == "" && st.Key == "" && st.Server != ""
	if !local && !remote {
		return st, corruptState()
	}
	return st, nil
}

// BoundServer is the canonical URL grant signatures must be bound to:
// empty for a local guard (its key is unique to this machine, which is the
// binding), the enrolled URL in remote mode.
func BoundServer() string {
	st, verr := load()
	if verr != nil {
		return ""
	}
	return st.Server
}

func corruptState() *view.Error {
	return view.Errorf("core.guard.corrupt",
		"%s does not parse as guard state — it has been modified or truncated", Path()).
		WithHint("no grant is honoured while this stands; `rm " + Path() + " " + grantsHint() +
			"` starts clean, then `rta grant guard on` (or `... guard remote`) again")
}

// Remote reports whether the guard is in remote mode: grant signatures are
// honoured from enrolled operator keys, and no local key exists at all.
func (st state) remote() bool { return len(st.Operators) > 0 }

// Remote reports whether the enabled guard trusts remote operators instead
// of a local passphrase-wrapped key. False when the guard is off or corrupt
// — callers gate on Enabled first, and every corrupt path fails closed on
// its own.
func Remote() bool {
	st, verr := load()
	return verr == nil && st.remote()
}

// OperatorLabels names the enrolled remote signers, for status and doctor.
func OperatorLabels() []string {
	st, verr := load()
	if verr != nil {
		return nil
	}
	out := make([]string, 0, len(st.Operators))
	for _, op := range st.Operators {
		out = append(out, op.Label)
	}
	return out
}

// RemoteMatches reports whether the enabled remote guard trusts exactly
// the given signer set, by key bytes. It exists for the serve-time
// cross-check: the roster gates verbs per request from the file as it is
// now, while grant signatures answer to the set pinned here at enrollment
// — and a key the roster since demoted to role=read, rotated, or removed
// keeps its grant-signing trust until `grant guard remote` is re-run,
// with nothing on disk to testify that the two drifted apart. False when
// the guard is off, local, or unreadable: those states have their own
// checks, and this one only answers the drift question.
func RemoteMatches(ops []OperatorKey) bool {
	st, verr := load()
	if verr != nil || !st.remote() || len(ops) != len(st.Operators) {
		return false
	}
	want := make([]string, 0, len(ops))
	for _, op := range ops {
		want = append(want, op.PublicKey)
	}
	have := make([]string, 0, len(st.Operators))
	for _, op := range st.Operators {
		have = append(have, op.PublicKey)
	}
	sort.Strings(want)
	sort.Strings(have)
	for i := range want {
		if want[i] != have[i] {
			return false
		}
	}
	return true
}

// Fingerprint names the guard's trust in eight hex characters, for the
// status and doctor surfaces and the server Pin: enough to notice a key —
// or, in remote mode, the enrolled set — that changed, short enough to sit
// in a table cell. Remote mode hashes the sorted key set, so enrolling or
// removing any operator changes the fingerprint the same way a swapped
// local key does, and Pin.Check trips on both alike.
func Fingerprint() string {
	if material := trustMaterial(); material != nil {
		return passkey.Fingerprint(material)
	}
	return ""
}

// trustMaterial is the byte string that identifies what this guard trusts:
// the local verification key, or in remote mode the sorted enrolled set
// plus the bound server. One definition feeds both the display fingerprint
// (truncated) and the Pin's digest (full width), so the two can never
// disagree about what counts as a change.
func trustMaterial() []byte {
	st, verr := load()
	if verr != nil {
		return nil
	}
	if st.remote() {
		keys := make([]string, 0, len(st.Operators))
		for _, op := range st.Operators {
			keys = append(keys, op.PublicKey)
		}
		sort.Strings(keys)
		return []byte(st.Server + "\n" + strings.Join(keys, "\n"))
	}
	pub, err := base64.StdEncoding.DecodeString(st.PublicKey)
	if err != nil {
		return nil
	}
	return pub
}

// trustDigest is trustMaterial at full hash width, for the Pin. Empty when
// the guard is off or unreadable — which Pin.Check treats as a change, the
// closed direction.
func trustDigest() string {
	material := trustMaterial()
	if material == nil {
		return ""
	}
	sum := sha256.Sum256(material)
	return hex.EncodeToString(sum[:])
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
	var encoded []string
	if st.remote() {
		for _, op := range st.Operators {
			encoded = append(encoded, op.PublicKey)
		}
	} else {
		encoded = []string{st.PublicKey}
	}
	keys := make([]ed25519.PublicKey, 0, len(encoded))
	for _, e := range encoded {
		pub, err := base64.StdEncoding.DecodeString(e)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return nil, view.Errorf("core.guard.corrupt",
				"%s carries an unreadable verification key", Path()).
				WithHint("no grant is honoured while this stands; `rm " + Path() + " " + grantsHint() +
					"` starts clean, then `rta grant guard on` (or `... guard remote`) again")
		}
		keys = append(keys, ed25519.PublicKey(pub))
	}
	return func(msg []byte, sig string) bool {
		raw, err := base64.StdEncoding.DecodeString(sig)
		if err != nil {
			return false
		}
		full := append([]byte(signContext), msg...)
		for _, key := range keys {
			if ed25519.Verify(key, full, raw) {
				return true
			}
		}
		return false
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

// EnableRemote writes the guard in remote mode: the listed operator keys may
// sign grants, and nothing on this machine can. Refuses over existing state
// for Enable's reason — replacing the trusted set silently is a decision,
// not a side effect — and validates every key before anything is written,
// because a state file that half-parses is a machine that refuses every
// grant it holds.
func EnableRemote(ops []OperatorKey, server string) *view.Error {
	unlock, verr := transitionLock()
	if verr != nil {
		return verr
	}
	defer unlock()
	if _, err := os.Stat(Path()); err == nil {
		return view.Errorf("core.guard.exists", "the guard is already enabled").
			WithHint("`rta grant guard off` first, if you mean to change what it trusts")
	}
	if len(ops) == 0 {
		return view.Errorf("core.guard.remote.empty", "no operator keys to enroll — that would be a guard nobody can satisfy")
	}
	if strings.TrimSpace(server) == "" {
		return view.Errorf("core.guard.remote.server",
			"a remote guard needs this server's canonical URL — it is signed into every grant, "+
				"so a grant issued for this server verifies on no other")
	}
	for _, op := range ops {
		pub, err := base64.StdEncoding.DecodeString(op.PublicKey)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return view.Errorf("core.guard.remote.key", "%q does not carry an ed25519 public key", op.Label)
		}
	}
	data, err := json.MarshalIndent(state{
		Created:   time.Now().UTC(),
		Operators: ops,
		Server:    server,
	}, "", "  ")
	if err != nil {
		return view.Errorf("core.guard.write", "encoding guard state: %v", err)
	}
	if err := atomicfile.Write(Path(), data, 0o600); err != nil {
		return view.Errorf("core.guard.write", "writing %s: %v", Path(), err)
	}
	return nil
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
	if st.remote() {
		return Signer{}, remoteRefusal()
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

func remoteRefusal() *view.Error {
	return view.Errorf("core.guard.remote",
		"this machine's guard trusts remote operators only — there is no local key to unlock").
		WithHint("grants here are issued through the operator channel: `rta grant allow <capability> " +
			"--server <this server>` from an enrolled operator's own machine")
}

// UnlockPrompted is the guard-gated flows' one way to a Signer: refuse a
// remote guard before the prompt — nobody should type a passphrase that
// cannot exist on this machine — then read it through the shared channels
// and unlock. Three flows gate on it (grant allow, grant renew, the consent
// prompt's --ttl branch), and one function is what keeps a fourth from
// prompting first and discovering remote mode after.
func UnlockPrompted(req plugin.Request) (Signer, *view.Error) {
	if Remote() {
		return Signer{}, remoteRefusal()
	}
	pass, verr := PromptSecret(req, false)
	if verr != nil {
		return Signer{}, verr
	}
	return Unlock(pass)
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

// DisableRemote removes a remote-mode guard. There is no passphrase to
// prove — the secrets live with operators who are elsewhere, which is the
// mode's point — so the legitimate way off costs only presence at this
// machine's own terminal, and the capability layer enforces exactly that.
// Refused for a local guard: its off-switch must cost what turning it on
// promised, and this function must never become the way around Disable.
func DisableRemote() *view.Error {
	unlock, verr := transitionLock()
	if verr != nil {
		return verr
	}
	defer unlock()
	st, verr := load()
	if verr != nil {
		return verr
	}
	if !st.remote() {
		return view.Errorf("core.guard.passphrase.required",
			"this guard is locked by a passphrase, and disabling it costs exactly that").
			WithHint("rta grant guard off")
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
