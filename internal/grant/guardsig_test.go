package grant

import (
	"os"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/guard"
)

// enableGuard turns the guard on in this test's data directory and returns
// the signer, paying a lowered KDF cost once per call rather than the
// production work factor the suite has no business exercising.
func enableGuard(t *testing.T) guard.Signer {
	t.Helper()
	old := guard.ScryptWorkFactor
	guard.ScryptWorkFactor = 10
	t.Cleanup(func() { guard.ScryptWorkFactor = old })
	s, verr := guard.Enable("test passphrase")
	if verr != nil {
		t.Fatal(verr)
	}
	return s
}

func stamped(target string) Grant {
	now := time.Now()
	return Grant{Target: target, Issued: now, Expires: now.Add(time.Hour)}
}

// **With the guard on, asking rta to write a grant stops being enough.**
// The seal's own comment concedes it stamps authentic anything rta itself
// writes; the guard's refusal here is the difference between detection and
// prevention for exactly that path.
func TestGuardOnRefusesAnUnsignedIssue(t *testing.T) {
	setup(t)
	enableGuard(t)
	verr := Issue(stamped("kv.get"), true)
	if verr == nil {
		t.Fatal("an unsigned grant was issued under the guard")
	}
	if verr.Code != "core.grant.guard.unsigned" {
		t.Fatalf("code = %s, want core.grant.guard.unsigned", verr.Code)
	}
	if grants, _ := Load(); len(grants) != 0 {
		t.Fatalf("%d grants stored after a refusal", len(grants))
	}
}

func TestASignedGrantIssuesAndLoads(t *testing.T) {
	setup(t)
	s := enableGuard(t)
	g := stamped("kv.get")
	SignWith(s, &g)
	if verr := Issue(g, true); verr != nil {
		t.Fatal(verr)
	}
	grants, verr := Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(grants) != 1 || grants[0].Target != "kv.get" {
		t.Fatalf("loaded %+v", grants)
	}
}

// The signature covers authority and not consumption: spending a use rewrites
// Uses under Reserve's lock with no passphrase in reach, and that rewrite
// must not read as forgery.
func TestSpendingAUseKeepsTheSignatureValid(t *testing.T) {
	setup(t)
	s := enableGuard(t)
	g := stamped("kv.get")
	g.MaxUses = 3
	SignWith(s, &g)
	if verr := Issue(g, true); verr != nil {
		t.Fatal(verr)
	}
	if verr := Mutate(func(stored []Grant) ([]Grant, bool) {
		for i := range stored {
			stored[i].Uses++
			stored[i].Recent = append(stored[i].Recent, time.Now())
		}
		return stored, true
	}); verr != nil {
		t.Fatal(verr)
	}
	grants, verr := Load()
	if verr != nil {
		t.Fatalf("a spent use reads as forgery: %v", verr)
	}
	if len(grants) != 1 || grants[0].Uses != 1 {
		t.Fatalf("loaded %+v", grants)
	}
}

// Rewriting a signed grant's authority — here its target — kills the whole
// file, not the row: dropping the row would make the one moment worth
// noticing present as the ordinary case.
func TestTamperedAuthorityRefusesTheWholeFile(t *testing.T) {
	setup(t)
	s := enableGuard(t)
	g := stamped("kv.get")
	SignWith(s, &g)
	if verr := Issue(g, true); verr != nil {
		t.Fatal(verr)
	}
	// Save re-seals with the on-disk key, which is exactly what a same-uid
	// attacker can do; the guard signature is what they cannot re-make.
	g.Target = "kv.rm"
	if verr := Save([]Grant{g}); verr != nil {
		t.Fatal(verr)
	}
	_, verr := Load()
	if verr == nil {
		t.Fatal("a rewritten authority still loaded")
	}
	if verr.Code != "core.grant.guard.forged" {
		t.Fatalf("code = %s, want core.grant.guard.forged", verr.Code)
	}
}

// Deleting the guard state with signed grants behind it is the rollback an
// agent could type; it must refuse by name, not read as "guard was never on".
func TestDeletingTheGuardOrphansSignedGrants(t *testing.T) {
	setup(t)
	s := enableGuard(t)
	g := stamped("kv.get")
	SignWith(s, &g)
	if verr := Issue(g, true); verr != nil {
		t.Fatal(verr)
	}
	if err := os.Remove(guard.Path()); err != nil {
		t.Fatal(err)
	}
	_, verr := Load()
	if verr == nil {
		t.Fatal("signed grants loaded with no guard state beside them")
	}
	if verr.Code != "core.grant.guard.orphaned" {
		t.Fatalf("code = %s, want core.grant.guard.orphaned", verr.Code)
	}
}

// The mirror invariant: with the guard off, a grant carrying a signature is
// not a stored fact rta ever produces, so Issue refuses it outright.
func TestGuardOffRefusesASignedIssue(t *testing.T) {
	setup(t)
	g := stamped("kv.get")
	g.Sig = "c29tZXRoaW5n"
	verr := Issue(g, true)
	if verr == nil {
		t.Fatal("a signature-carrying grant issued with no guard")
	}
	if verr.Code != "core.grant.guard.off" {
		t.Fatalf("code = %s, want core.grant.guard.off", verr.Code)
	}
}
