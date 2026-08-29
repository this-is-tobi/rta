package consent

import (
	"testing"
)

// **The agent is part of the call, not prose about it.**
//
// The package comment's claim is that rewriting a parked request changes only
// what the operator is shown: their approval carries the digest of the call
// they *read*, so a rewritten display matches nothing waiting. That claim is
// true only of fields inside the digest, and the name of the agent asking is
// exactly the kind of field somebody would rewrite — show the operator a
// request from their own desktop assistant, bind one from somewhere else.
func TestRewritingTheAgentBreaksTheDigest(t *testing.T) {
	base := Call{Cap: "kv.get", Safety: "write", Scopes: []string{"db"}, Agent: "desktop"}
	other := base
	other.Agent = "ci"
	if base.Digest() == other.Digest() {
		t.Fatal("two calls from different agents have the same digest, so rewriting " +
			"`agent` in a parked request would show one name and bind another")
	}
	// And the unnamed case is its own value rather than a wildcard: a request
	// from an unnamed server must not match one from a named agent.
	unnamed := base
	unnamed.Agent = ""
	if unnamed.Digest() == base.Digest() {
		t.Fatal("an unnamed request digests the same as a named one")
	}
}

// Honest() checks the display against the digest, so it has to notice this
// field too — that is the whole mechanism, not a second one.
func TestARequestWithARewrittenAgentIsNotHonest(t *testing.T) {
	c := Call{Cap: "kv.get", Safety: "write", Scopes: []string{"db"}, Agent: "desktop"}
	r := Request{
		Digest: c.Digest(), Cap: c.Cap, Safety: c.Safety,
		Scopes: c.Scopes, Agent: c.Agent,
	}
	if !r.Honest() {
		t.Fatal("a request built from its own call does not agree with itself")
	}
	r.Agent = "ci"
	if r.Honest() {
		t.Fatal("a request whose agent was rewritten still reports itself honest")
	}
}
