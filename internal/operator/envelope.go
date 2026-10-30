package operator

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
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
// over this server's own identity, and whose — the roster row that made
// it, so the dispatch downstream gates verbs on what the signature
// actually proved (label, role, expiry), never on anything the envelope
// claimed. Expiry is deliberately proved here and enforced downstream,
// like the role: an expired key is still an *identified* caller, owed the
// real reason and a ledger row for the attempt, where a failed signature
// is a stranger owed nothing. server is the verifier's canonical URL from
// its own configuration — deliberately a parameter and not an envelope
// field, so a relayed envelope cannot bring the identity it was signed for
// along with it. It checks the signature only — the nonce is the caller's
// to consume first, because single-use is a property of the store, not of
// the math. False carries no reason on purpose: the reason goes to the
// server's stderr, never to an unauthenticated caller.
func (r Roster) Verify(env Envelope, server string) (Identity, bool) {
	raw, err := base64.StdEncoding.DecodeString(env.Sig)
	if err != nil {
		return Identity{}, false
	}
	msg := message(server, env.Nonce, env.Verb, env.Payload)
	for _, e := range r.keys[env.Fingerprint] {
		if ed25519.Verify(e.key, msg, raw) {
			return Identity{Label: e.label, Role: e.role, Expires: e.expires}, true
		}
	}
	// An unknown fingerprint costs one verify against a throwaway key, the
	// same work a wrong signature costs against a real one — otherwise
	// response timing tells an unauthenticated caller which fingerprints are
	// enrolled, the one thing the uniform refusal body exists to withhold.
	if len(r.keys[env.Fingerprint]) == 0 {
		ed25519.Verify(timingDummy, msg, raw)
	}
	return Identity{}, false
}

// timingDummy is the key Verify burns a check against when a fingerprint
// names no enrolled key; see the comment at its use.
//
// A constant, not a key generated when the package loads: generating one cost
// about 0.7 ms of every rta start — measured with GODEBUG=inittrace=1 — for a
// key whose private half nobody ever needs. This is the public half of the
// all-zero seed, so anyone can sign for it, and that is harmless because the
// verdict of that verify is thrown away: what Verify wants from it is the
// time a real point decompression and check take, and nothing else.
//
// It has to stay a valid curve point. ed25519.Verify returns at once for a
// key that does not decode, which would make an unknown fingerprint answer
// faster than a wrong signature and reopen exactly the channel the burn
// closes; TestTheTimingDummyIsAPointVerifyDecodes pins that.
var timingDummy = ed25519.PublicKey{
	0x3b, 0x6a, 0x27, 0xbc, 0xce, 0xb6, 0xa4, 0x2d, 0x62, 0xa3, 0xa8, 0xd0, 0x2a, 0x6f, 0x0d, 0x73,
	0x65, 0x32, 0x15, 0x77, 0x1d, 0xe2, 0x43, 0xa6, 0x3a, 0xc0, 0x48, 0xa1, 0x8b, 0x59, 0xda, 0x29,
}

// Nonces is a server's single-use challenge store: what makes a captured
// envelope worthless a second time.
//
// **A challenge is minted, not stored.** It carries its own expiry and random
// bytes under a MAC keyed by a secret this process made and never shows, so
// issuing one writes nothing down and checking one (Fresh) needs nothing but
// the key. The store holds a nonce only once a call carrying it has proved an
// enrolled key (Spend), and only until it expires. Issuance is
// unauthenticated — a challenge proves nothing and grants nothing — and it
// used to put every challenge in one shared pool of 4096, evicting the oldest:
// a stranger asking for challenges faster than an operator's round trip
// evicted the operator's challenge, and kept doing so for every retry, which
// was a refusal-of-service of the remote incident path (lock.add,
// grant.revoke) for anybody who could reach the route. Now a stranger's
// challenges cost a MAC each and nothing on the heap, and what can fill the
// store is calls an enrolled key signed.
//
// The nonce used to burn before the signature was checked, so that a captured
// challenge could not be tried against at leisure. Against ed25519 there is
// nothing to try: a signature is not guessed. What single use is for is
// replay, and a nonce recorded after the signature verifies refuses a replay
// as surely as one burned before it.
type Nonces struct {
	ttl time.Duration
	key [32]byte

	mu    sync.Mutex
	spent map[string]time.Time
}

// NonceTTL is how long a challenge waits to be spent. It bounds the gap
// between a client fetching the challenge and sending the signed call —
// seconds, in practice, since the client unlocks the key before asking.
const NonceTTL = 2 * time.Minute

// spentCap bounds the record of spent nonces. Only a call an enrolled key
// signed adds to it, so filling it takes thousands of signed calls inside one
// TTL; past it the one closest to expiry goes, which reopens that one call to
// replay by whoever captured it off TLS — where refusing instead would lock
// out the operator trying to lock the flooding key.
const spentCap = 4096

// nonceRandom is the challenge's random part, and nonceMAC its tag: an
// expiry of eight bytes, then these.
const (
	nonceRandom = 16
	nonceMAC    = 32
	nonceSize   = 8 + nonceRandom + nonceMAC
)

// NewNonces builds a store; zero ttl means NonceTTL.
func NewNonces(ttl time.Duration) *Nonces {
	if ttl <= 0 {
		ttl = NonceTTL
	}
	n := &Nonces{ttl: ttl, spent: map[string]time.Time{}}
	// No error to handle: since Go 1.24 a source that fails ends the
	// program, so there is no store left without its key.
	_, _ = rand.Read(n.key[:])
	return n
}

// Issue mints a fresh challenge, good for the store's TTL.
func (n *Nonces) Issue() (string, error) {
	raw := make([]byte, nonceSize)
	binary.BigEndian.PutUint64(raw, uint64(time.Now().Add(n.ttl).UnixNano()))
	if _, err := rand.Read(raw[8 : 8+nonceRandom]); err != nil {
		return "", err
	}
	copy(raw[8+nonceRandom:], n.tag(raw[:8+nonceRandom]))
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (n *Nonces) tag(body []byte) []byte {
	mac := hmac.New(sha256.New, n.key[:])
	mac.Write(body)
	return mac.Sum(nil)
}

// Fresh reports whether nonce is a challenge this store minted that has not
// expired, and when it does. It reads nothing but the nonce, so it runs before
// the signature check: an envelope a stranger made up costs a MAC.
func (n *Nonces) Fresh(nonce string) (time.Time, bool) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(nonce)
	if err != nil || len(raw) != nonceSize {
		return time.Time{}, false
	}
	if !hmac.Equal(raw[8+nonceRandom:], n.tag(raw[:8+nonceRandom])) {
		return time.Time{}, false
	}
	// Past the int64 range is no expiry Issue wrote, which the MAC already
	// says; held here too, so the conversion below cannot wrap.
	at := binary.BigEndian.Uint64(raw)
	if at > math.MaxInt64 {
		return time.Time{}, false
	}
	exp := time.Unix(0, int64(at))
	return exp, time.Now().Before(exp)
}

// Spend records the nonce of a call whose signature verified: true the first
// time, false for every replay of it inside its life. A nonce that is not
// Fresh never spends.
func (n *Nonces) Spend(nonce string) bool {
	exp, ok := n.Fresh(nonce)
	if !ok {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, seen := n.spent[nonce]; seen {
		return false
	}
	if len(n.spent) >= spentCap {
		now := time.Now()
		var soonest string
		for k, at := range n.spent {
			if !now.Before(at) {
				delete(n.spent, k)
			} else if soonest == "" || at.Before(n.spent[soonest]) {
				soonest = k
			}
		}
		if len(n.spent) >= spentCap {
			delete(n.spent, soonest)
		}
	}
	n.spent[nonce] = exp
	return true
}
