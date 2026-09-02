package operator

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"sync"
	"time"
)

// signContext domain-separates operator envelopes from every other use this
// key could be put to — including the grant signatures a roster key will
// later make, which carry the guard's own context. Versioned so a change to
// the envelope's framing cannot be replayed against an older reader.
const signContext = "rta.operator.v1\x00"

// Envelope is one signed operator request: who claims to be asking, the
// server-issued nonce that makes it single-use, the verb, and the verb's
// payload as the exact bytes the signature covers. Payload is a RawMessage
// on purpose — re-marshalling JSON is not byte-stable, and the signature is
// over bytes, so the bytes must travel untouched from signer to verifier.
type Envelope struct {
	Fingerprint string          `json:"fingerprint"`
	Nonce       string          `json:"nonce"`
	Verb        string          `json:"verb"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	Sig         string          `json:"sig"`
}

// message frames what an envelope signature covers. NUL separators are
// unambiguous because no field to the left of one can contain NUL: the
// server URL has been through url.Parse, the nonce is base64url from Issue,
// and a verb is a dotted lowercase word — only the payload, which comes last
// and needs no terminator, is free-form.
//
// The server segment is the anti-relay binding, and its shape matters: the
// *client* signs the URL it resolved from remotes.yaml, and the *server*
// reconstructs the message from its own --operators-url, never from
// anything in the envelope. A nonce alone does not scope a signature to a
// server, because "a nonce from one server never matches another's" holds
// only for honest servers — a hostile one in the operator's remotes.yaml
// can fetch a victim server's challenge, present it as its own, and relay
// the resulting envelope to a server that trusts the same operator key. With
// the URL inside the signed bytes and the verifier supplying its own, the
// relayed envelope names the wrong server and verifies nowhere but where
// the operator actually aimed it.
func message(server, nonce, verb string, payload []byte) []byte {
	b := make([]byte, 0, len(signContext)+len(server)+1+len(nonce)+1+len(verb)+1+len(payload))
	b = append(b, signContext...)
	b = append(b, server...)
	b = append(b, 0)
	b = append(b, nonce...)
	b = append(b, 0)
	b = append(b, verb...)
	b = append(b, 0)
	b = append(b, payload...)
	return b
}

// Sign seals one request into its envelope, bound to the one server it is
// for: server is the canonical URL the caller resolved and is about to dial,
// and the nonce came from that server's own challenge.
func (s Signer) Sign(server, nonce, verb string, payload []byte) Envelope {
	return Envelope{
		Fingerprint: s.fingerprint,
		Nonce:       nonce,
		Verb:        verb,
		Payload:     payload,
		Sig:         base64.StdEncoding.EncodeToString(ed25519.Sign(s.priv, message(server, nonce, verb, payload))),
	}
}

// Verify reports whether env carries a valid signature by an enrolled key
// over this server's own identity, and whose — the label and the role the
// roster enrolled that key under, so the dispatch downstream gates verbs
// on what the signature actually proved, never on anything the envelope
// claimed. server is the verifier's canonical URL from its own
// configuration — deliberately a parameter and not an envelope field, so a
// relayed envelope cannot bring the identity it was signed for along with
// it. It checks the signature only — the nonce is the caller's to consume
// first, because single-use is a property of the store, not of the math.
// False carries no reason on purpose: the reason goes to the server's
// stderr, never to an unauthenticated caller.
func (r Roster) Verify(env Envelope, server string) (string, Role, bool) {
	raw, err := base64.StdEncoding.DecodeString(env.Sig)
	if err != nil {
		return "", "", false
	}
	msg := message(server, env.Nonce, env.Verb, env.Payload)
	for _, e := range r.keys[env.Fingerprint] {
		if ed25519.Verify(e.key, msg, raw) {
			return e.label, e.role, true
		}
	}
	// An unknown fingerprint costs one verify against a throwaway key, the
	// same work a wrong signature costs against a real one — otherwise
	// response timing tells an unauthenticated caller which fingerprints are
	// enrolled, the one thing the uniform refusal body exists to withhold.
	if len(r.keys[env.Fingerprint]) == 0 {
		ed25519.Verify(timingDummy, msg, raw)
	}
	return "", "", false
}

// timingDummy is the key Verify burns a check against when a fingerprint
// names no enrolled key; see the comment at its use.
var timingDummy = func() ed25519.PublicKey {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return pub
}()

// Nonces is a server's single-use challenge store, in memory beside the
// roster: what makes a captured envelope worthless a second time. Issuance
// is unauthenticated — a challenge proves nothing and grants nothing — so
// the store is sized and evicted for an unauthenticated caller's worst
// behaviour: flooding it evicts the flood's own oldest entries first, which
// degrades an operator's in-flight challenge to a retry, never the server to
// a refusal-of-service that locks the one human out.
type Nonces struct {
	mu   sync.Mutex
	ttl  time.Duration
	cap  int
	live map[string]time.Time
	// order holds nonces oldest-first for eviction; consumed ones stay as
	// harmless stale entries until GC reaches them, so eviction is O(1)
	// amortized instead of a scan.
	order []string
}

// NonceTTL is how long a challenge waits to be spent. It bounds the gap
// between a client fetching the challenge and sending the signed call —
// seconds, in practice, since the client unlocks the key before asking.
const NonceTTL = 2 * time.Minute

// nonceCap bounds the live set. 4096 outstanding challenges is three orders
// of magnitude past human-paced use; past it, the oldest go first.
const nonceCap = 4096

// NewNonces builds a store; zero ttl means NonceTTL.
func NewNonces(ttl time.Duration) *Nonces {
	if ttl <= 0 {
		ttl = NonceTTL
	}
	return &Nonces{ttl: ttl, cap: nonceCap, live: map[string]time.Time{}}
}

// Issue mints a fresh challenge.
func (n *Nonces) Issue() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	nonce := base64.RawURLEncoding.EncodeToString(raw)
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	for len(n.order) > 0 {
		head := n.order[0]
		exp, ok := n.live[head]
		if ok && now.Before(exp) && len(n.live) < n.cap {
			break
		}
		n.order = n.order[1:]
		delete(n.live, head)
	}
	n.live[nonce] = now.Add(n.ttl)
	n.order = append(n.order, nonce)
	// order can outgrow live: Consume deletes from the map and leaves its
	// stale entry for GC to reach. The loop above stops at the first live
	// head, so an issue-and-consume flood would otherwise grow order without
	// bound for a TTL window. Past the cap the oldest go regardless of
	// liveness — evicting a live challenge is the documented degradation
	// (a retry), an unbounded slice is a memory bill.
	for len(n.order) > n.cap {
		head := n.order[0]
		n.order = n.order[1:]
		delete(n.live, head)
	}
	return nonce, nil
}

// Consume spends a challenge: true exactly once per Issue, within the TTL.
// It is called before signature verification, not after — burning the nonce
// on first sight means a captured challenge cannot be brute-forced against
// at leisure, and costs a legitimate caller nothing but a fresh challenge.
func (n *Nonces) Consume(nonce string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	exp, ok := n.live[nonce]
	if !ok {
		return false
	}
	delete(n.live, nonce)
	return time.Now().Before(exp)
}
