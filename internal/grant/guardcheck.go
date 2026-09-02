package grant

import (
	"encoding/json"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/guard"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// What the guard signs, and what it deliberately does not.
//
// A signature over the whole Grant would break on the first spent use:
// Reserve rewrites Uses and Recent under its lock on every counted MCP call,
// and the server holding no passphrase is the design, not an oversight. So
// the signature covers the *authority* — what may happen, to what, for whom,
// until when — and leaves the consumption bookkeeping to the seal that
// already covers the file. The split is honest about what each layer can
// promise: without the passphrase no new authority can be minted, while
// tampering with a counter still takes reading grants.key and re-sealing,
// exactly today's bar, detection and not prevention.
//
// authority mirrors Grant's own field set and json rules — omitempty where
// Grant says omitempty — so the bytes are deterministic for a fixed grant
// and a field added to Grant tomorrow does not silently join the signed set
// without a decision here.
type authority struct {
	Target     string `json:"target"`
	Scope      string `json:"scope,omitempty"`
	Profile    string `json:"profile,omitempty"`
	ProfilePin string `json:"profilePin,omitempty"`
	Agent      string `json:"agent,omitempty"`
	// time.Time marshals as RFC3339Nano wall-clock: the monotonic reading a
	// fresh time.Now() carries never reaches JSON, so the bytes signed at
	// issuance and the bytes verified after a reload are identical.
	Issued     time.Time `json:"issued"`
	Expires    time.Time `json:"expires"`
	From       string    `json:"from,omitempty"`
	Note       string    `json:"note,omitempty"`
	TTL        string    `json:"ttl,omitempty"`
	MaxUses    int       `json:"maxUses,omitempty"`
	RateMax    int       `json:"rateMax,omitempty"`
	RateWindow string    `json:"rateWindow,omitempty"`
}

// AuthorityBytes is the byte form a guard signature covers for g.
func AuthorityBytes(g Grant) []byte {
	b, err := json.Marshal(authority{
		Target: g.Target, Scope: g.Scope,
		Profile: g.Profile, ProfilePin: g.ProfilePin, Agent: g.Agent,
		Issued: g.Issued, Expires: g.Expires,
		From: g.From, Note: g.Note, TTL: g.TTL,
		MaxUses: g.MaxUses, RateMax: g.RateMax, RateWindow: g.RateWindow,
	})
	if err != nil {
		// Strings, ints and times through MarshalJSON cannot fail; if this
		// ever does, empty bytes verify against nothing, which lands closed.
		return nil
	}
	return b
}

// SignWith stamps g with the guard's signature over its authority. The only
// way to hold a Signer is to have presented the passphrase, so a signed
// grant is, transitively, a consented one.
func SignWith(s guard.Signer, g *Grant) { g.Sig = s.Sign(AuthorityBytes(*g)) }

// guardIssuable is Issue's backstop: with the guard on, a grant that was not
// signed with its passphrase is refused before it is written, whoever the
// caller is. Enforced here rather than trusted to the capabilities that
// remember to prompt, so a third issuing path added tomorrow cannot skip the
// guard by never having heard of it — loadAll would refuse the whole file on
// the next read anyway, but "your entire grant file is not honoured" is the
// wrong first message for a caller who simply forgot a passphrase.
func guardIssuable(g Grant) *view.Error {
	if guard.Enabled() {
		// An empty authority form never verifies as a matter of policy, not
		// luck: ed25519 would happily sign and verify the bare domain prefix,
		// so a marshal failure that returned nil bytes would give every
		// affected grant the same "valid" signature. Unreachable through any
		// ordinary path (it takes a year outside [0,9999]), refused anyway.
		if len(AuthorityBytes(g)) == 0 {
			return view.Errorf("core.grant.guard.unsigned",
				"this grant's authority cannot be encoded for signing")
		}
		if !guard.Verify(AuthorityBytes(g), g.Sig) {
			return view.Errorf("core.grant.guard.unsigned",
				"the guard is on, and this grant was not signed with its passphrase").
				WithHint("re-run and enter the guard passphrase when asked")
		}
		return nil
	}
	if g.Sig != "" {
		return view.Errorf("core.grant.guard.off",
			"this grant carries a guard signature, and the guard is not enabled")
	}
	return nil
}

// guardChecked enforces the invariant between the guard state and the stored
// grants, and it is deliberately all-or-nothing: one bad row refuses the
// whole file, the same stance the seal takes, because dropping the row would
// make the one moment worth noticing present as the ordinary case.
//
//   - Guard on: every row must carry a signature its key verifies.
//   - Guard off: no row may carry one. Disable clears the file before it
//     removes the state, so signed rows with no guard beside them mean the
//     state was removed by something other than rta — the delete-the-guard
//     rollback, refused by name.
func guardChecked(grants []Grant) *view.Error {
	if !guard.Enabled() {
		for _, g := range grants {
			if g.Sig != "" {
				return view.Errorf("core.grant.guard.orphaned",
					"%s holds guard-signed grants with no guard state beside them — "+
						"the guard was removed by something other than rta", Path()).
					WithHint("no grant is honoured until this is resolved; `rm " + Path() +
						"` clears every grant, and `rta grant guard on` re-arms the guard")
			}
		}
		return nil
	}
	verify, verr := guard.Verifier()
	if verr != nil {
		return verr
	}
	for _, g := range grants {
		// The same nil-bytes refusal guardIssuable applies, for the same
		// reason: empty authority must never verify.
		ab := AuthorityBytes(g)
		if len(ab) == 0 || !verify(ab, g.Sig) {
			return view.Errorf("core.grant.guard.forged",
				"%s holds a grant the guard never signed — written by something "+
					"without the passphrase", Path()).
				WithHint("no grant is honoured until this is resolved; `rm " + Path() +
					"` clears every grant, and any that were legitimate can be re-issued")
		}
	}
	return nil
}
