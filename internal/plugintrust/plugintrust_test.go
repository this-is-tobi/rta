package plugintrust

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const (
	digestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	// digestC and digestD share an 8-character prefix and diverge after it —
	// two real, distinct digests a short prefix cannot tell apart, which is
	// exactly the fixture an ambiguous-prefix test needs and digestA/digestB
	// (no shared prefix at all) cannot provide.
	digestC = "cafe000011111111111111111111111111111111111111111111111111111111"
	digestD = "cafe000022222222222222222222222222222222222222222222222222222222"
)

// isolated points the record at a directory of its own, so nothing here can
// read or write the developer's own list of what may run on their machine.
func isolated(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
}

func TestTrustRoundTrips(t *testing.T) {
	isolated(t)
	if Load().Trusts(digestA) {
		t.Fatal("an empty record trusted something")
	}
	if verr := Add(digestA, "pg", "/usr/local/bin/rta-plugin-pg"); verr != nil {
		t.Fatal(verr)
	}
	s := Load()
	if !s.Trusts(digestA) {
		t.Error("what was just trusted is not trusted")
	}
	if s.Trusts(digestB) {
		t.Error("trusting one artifact trusted another")
	}
	e := s.Entries()
	if len(e) != 1 || e[0].Label() != "pg" || e[0].At.IsZero() {
		t.Errorf("entry = %+v, want the name and the moment recorded", e)
	}
}

// The empty digest is the load-bearing zero value: every failure path in
// Identify hands back an empty Identity, and a caller that could not hash a
// file must not get "yes" by default.
func TestTheEmptyDigestIsNeverTrusted(t *testing.T) {
	isolated(t)
	if verr := Add("", "", ""); verr == nil {
		t.Error("an artifact with no digest was accepted")
	}
	if Load().Trusts("") {
		t.Error("the empty digest is trusted")
	}
	// Even with entries present — the check is on the argument, not on the
	// record being empty.
	if verr := Add(digestA, "pg", ""); verr != nil {
		t.Fatal(verr)
	}
	if Load().Trusts("") {
		t.Error("the empty digest is trusted once something else is")
	}
}

// It is a list of what this machine will execute. Nobody else's business to
// read on a shared box, and nobody else's business at all to write.
func TestTheRecordIsNotWorldReadable(t *testing.T) {
	isolated(t)
	if verr := Add(digestA, "pg", ""); verr != nil {
		t.Fatal(verr)
	}
	info, err := os.Stat(Path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %04o, want 0600", perm)
	}
}

// Trusting the same artifact twice is a person re-running a command after
// moving a binary, not an error to teach them about.
func TestTrustingTwiceRefreshesRatherThanDuplicates(t *testing.T) {
	isolated(t)
	for _, path := range []string{"/opt/one/rta-plugin-pg", "/opt/two/rta-plugin-pg"} {
		if verr := Add(digestA, "pg", path); verr != nil {
			t.Fatal(verr)
		}
	}
	e := Load().Entries()
	if len(e) != 1 {
		t.Fatalf("entries = %d, want one per artifact", len(e))
	}
	if e[0].Path != "/opt/two/rta-plugin-pg" {
		t.Errorf("path = %q, want where it was last seen", e[0].Path)
	}
}

// An operator taking a plugin back has a name in their head, and every digest
// that name ever had is a thing they want gone.
func TestUntrustByNameTakesEveryDigestThatNameHad(t *testing.T) {
	isolated(t)
	for _, d := range []string{digestA, digestB} {
		if verr := Add(d, "pg", ""); verr != nil {
			t.Fatal(verr)
		}
	}
	if verr := Add("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		"s3", ""); verr != nil {
		t.Fatal(verr)
	}
	n, verr := Remove("pg")
	if verr != nil {
		t.Fatal(verr)
	}
	if n != 2 {
		t.Errorf("withdrew %d, want both of pg's artifacts", n)
	}
	s := Load()
	if s.Trusts(digestA) || s.Trusts(digestB) {
		t.Error("a withdrawn artifact is still trusted")
	}
	if s.Len() != 1 {
		t.Errorf("removing pg took %d other entries with it", 1-s.Len())
	}
}

func TestUntrustByDigestPrefixTakesExactlyOne(t *testing.T) {
	isolated(t)
	for _, d := range []string{digestA, digestB} {
		if verr := Add(d, "pg", ""); verr != nil {
			t.Fatal(verr)
		}
	}
	n, verr := Remove(digestA[:12])
	if verr != nil {
		t.Fatal(verr)
	}
	if n != 1 {
		t.Fatalf("withdrew %d, want exactly the one named", n)
	}
	if !Load().Trusts(digestB) {
		t.Error("a digest prefix took an artifact it did not name")
	}
	// Short enough to be ambiguous is refused rather than guessed at: this is
	// a security control, and "a" matching every digest starting with 'a' is
	// not something to do quietly.
	if n, _ := Remove("a"); n != 0 {
		t.Errorf("a one-character prefix withdrew %d approvals", n)
	}
}

// A prefix long enough to pass the minDigestPrefix floor can still name more
// than one real, distinct artifact once the trust store holds enough of
// them — the doc comment's "one artifact and nothing else" promise is about
// the outcome, not about the length typed. Removing both silently would be
// the same failure a one-character prefix already refuses, just reached a
// different way; refusing and naming both is what keeps the promise.
func TestUntrustByAnAmbiguousPrefixIsRefused(t *testing.T) {
	isolated(t)
	for _, d := range []string{digestC, digestD} {
		if verr := Add(d, "", ""); verr != nil {
			t.Fatal(verr)
		}
	}
	prefix := digestC[:minDigestPrefix] // "cafe0000" — where digestC and digestD diverge
	if digestD[:minDigestPrefix] != prefix {
		t.Fatalf("test fixture bug: digestC and digestD do not share %q", prefix)
	}

	n, verr := Remove(prefix)
	if n != 0 {
		t.Errorf("an ambiguous prefix withdrew %d approvals, want none", n)
	}
	if verr == nil {
		t.Fatal("an ambiguous prefix was accepted rather than refused")
	}
	s := Load()
	if !s.Trusts(digestC) || !s.Trusts(digestD) {
		t.Error("an ambiguous prefix removed an artifact anyway")
	}

	// The full digest is never ambiguous, collision aside, and must still
	// work even though its own 12-character prefix is shared with another
	// trusted artifact.
	n, verr = Remove(digestC)
	if verr != nil {
		t.Fatal(verr)
	}
	if n != 1 {
		t.Fatalf("withdrew %d for the full digest, want exactly one", n)
	}
	if !Load().Trusts(digestD) {
		t.Error("removing one full digest took an unrelated artifact that merely shared a prefix")
	}
}

func TestUntrustingWhatIsNotThereChangesNothing(t *testing.T) {
	isolated(t)
	if verr := Add(digestA, "pg", ""); verr != nil {
		t.Fatal(verr)
	}
	n, verr := Remove("weather")
	if verr != nil {
		t.Fatal(verr)
	}
	if n != 0 {
		t.Errorf("withdrew %d approvals for a plugin that was never trusted", n)
	}
	if !Load().Trusts(digestA) {
		t.Error("removing an unknown name dropped a real entry")
	}
}

// A write must never rewrite a record it could not read.
//
// Load is deliberately fail-closed — an unreadable record trusts nothing —
// and read() failing open in the same direction is the opposite mistake: an
// empty list handed to a caller that writes it back with one entry in it,
// destroying every prior approval and reporting success. A hand-edited file,
// a file another user owns in a writable directory, or a future format change
// would each have done it silently.
func TestAWriteRefusesToClobberAnUnreadableRecord(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(t *testing.T, path string)
	}{
		{"corrupt", func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"truncated", func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolated(t)
			tc.write(t, Path())
			before, err := os.ReadFile(Path())
			if err != nil {
				t.Fatal(err)
			}
			if verr := Add(digestA, "pg", ""); verr == nil {
				t.Fatal("a write over an unreadable record was accepted")
			}
			after, err := os.ReadFile(Path())
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Errorf("the record was rewritten anyway:\n%q\n%q", before, after)
			}
			if _, verr := Remove("pg"); verr == nil {
				t.Error("a removal over an unreadable record was accepted")
			}
		})
	}

	// An absent file is not a failure: that is a machine with nothing
	// trusted yet, and the first `rta plugin trust` has to work.
	t.Run("absent", func(t *testing.T) {
		isolated(t)
		if verr := Add(digestA, "pg", ""); verr != nil {
			t.Fatalf("the first approval on a clean machine failed: %v", verr)
		}
		if !Load().Trusts(digestA) {
			t.Error("the first approval did not take")
		}
	})
}

// The dangerous direction is a lost untrust: an approval that resurrects
// itself is the one outcome a revocation must not have.
func TestConcurrentWritesDoNotResurrectAWithdrawnApproval(t *testing.T) {
	isolated(t)
	for _, d := range []string{digestA, digestB} {
		if verr := Add(d, "pg", ""); verr != nil {
			t.Fatal(verr)
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = Remove(digestA[:12])
	}()
	go func() {
		defer wg.Done()
		_ = Add("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", "s3", "")
	}()
	wg.Wait()

	s := Load()
	if s.Trusts(digestA) {
		t.Error("a withdrawn approval came back")
	}
	if !s.Trusts(digestB) {
		t.Error("an unrelated approval was lost")
	}
}

// Trusting one artifact under two names must not lose the first.
//
// One file can sit on $PATH twice — a copy, a symlink, a rename during a
// migration — and each is discovered under its own name. Recording only the
// most recent one made `rta plugin untrust <the other name>` answer "nothing
// trusted is called that", while the artifact stayed trusted and kept
// executing on the next `rta`, including the one a tab press runs.
//
// The failure direction is what makes it worth a field: the operator is told
// the revocation did not happen, which is the opposite of the truth about the
// approval, and they are told it about the exact name they typed.
func TestAnArtifactIsWithdrawnByAnyNameItWasTrustedUnder(t *testing.T) {
	isolated(t)
	for _, name := range []string{"weather", "forecast"} {
		if verr := Add(digestA, name, "/opt/bin/rta-plugin-"+name); verr != nil {
			t.Fatal(verr)
		}
	}
	e := Load().Entries()
	if len(e) != 1 {
		t.Fatalf("entries = %d, want one per artifact", len(e))
	}
	if !e[0].Knows("weather") || !e[0].Knows("forecast") {
		t.Errorf("names = %v, want both", e[0].Names)
	}
	if e[0].Label() != "forecast" {
		t.Errorf("label = %q, want the most recent name", e[0].Label())
	}

	n, verr := Remove("weather")
	if verr != nil {
		t.Fatal(verr)
	}
	if n != 1 {
		t.Fatalf("withdrew %d under the older name, want the artifact", n)
	}
	if Load().Trusts(digestA) {
		t.Error("the artifact is still trusted after being withdrawn by a name it carried")
	}
}

// A record written before names were a list keeps its label.
//
// Dropping it would reintroduce, as an upgrade, the very miss the list exists
// to remove: every already-trusted plugin would stop answering to the name it
// was trusted under, which is the name in the operator's head.
func TestALegacyRecordKeepsTheNameItWasTrustedUnder(t *testing.T) {
	isolated(t)
	legacy := `{"trusted":[{"digest":"` + digestA + `","name":"pg","path":"/usr/local/bin/rta-plugin-pg","at":"2026-01-02T03:04:05Z"}]}`
	if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	e := Load().Entries()
	if len(e) != 1 || e[0].Label() != "pg" {
		t.Fatalf("entries = %+v, want the legacy name read as a label", e)
	}
	n, verr := Remove("pg")
	if verr != nil {
		t.Fatal(verr)
	}
	if n != 1 {
		t.Errorf("withdrew %d, want the legacy entry", n)
	}
}

// Allowing a credential location round-trips, and attaches to the artifact
// rather than to the name.
func TestAllowRoundTripsAgainstTheDigest(t *testing.T) {
	isolated(t)
	if verr := Add(digestA, "cnpg", "/usr/local/bin/rta-plugin-cnpg"); verr != nil {
		t.Fatal(verr)
	}
	if got := Load().Allowed(digestA); len(got) != 0 {
		t.Errorf("a freshly trusted artifact is allowed %v — trust is not a grant", got)
	}
	if verr := Allow(digestA, []string{"kubeconfig"}); verr != nil {
		t.Fatal(verr)
	}
	if got := Load().Allowed(digestA); len(got) != 1 || got[0] != "kubeconfig" {
		t.Errorf("Allowed = %v, want [kubeconfig]", got)
	}
	if got := Load().Allowed(digestB); len(got) != 0 {
		t.Errorf("allowing one artifact allowed another: %v", got)
	}
}

// **Allowing bytes that are not even allowed to run is a record that means
// nothing**, and it would outlive the reason it was written: the artifact
// could be trusted later, by somebody who never saw this decision, and arrive
// with a credential grant already attached.
func TestAllowingAnUntrustedArtifactIsRefused(t *testing.T) {
	isolated(t)
	verr := Allow(digestA, []string{"kubeconfig"})
	if verr == nil {
		t.Fatal("an untrusted artifact was allowed a credential location")
	}
	if verr.Code != "plugin.allow.untrusted" {
		t.Errorf("code = %s, want plugin.allow.untrusted", verr.Code)
	}
	if got := Load().Allowed(digestA); len(got) != 0 {
		t.Errorf("the refusal still wrote something: %v", got)
	}
}

// **Re-trusting the same bytes must not silently revoke.** `rta plugin trust`
// on an artifact already trusted is a refresh — somebody moved the binary, or
// typed the command twice — and dropping the grant there would take a
// permission away with nothing said, at the moment the operator was
// reaffirming the artifact rather than reconsidering it.
func TestTrustingAgainKeepsWhatWasAllowed(t *testing.T) {
	isolated(t)
	if verr := Add(digestA, "cnpg", "/old/path"); verr != nil {
		t.Fatal(verr)
	}
	if verr := Allow(digestA, []string{"kubeconfig"}); verr != nil {
		t.Fatal(verr)
	}
	if verr := Add(digestA, "cnpg", "/new/path"); verr != nil {
		t.Fatal(verr)
	}
	if got := Load().Allowed(digestA); len(got) != 1 || got[0] != "kubeconfig" {
		t.Errorf("Allowed = %v after re-trusting the same bytes, want it kept", got)
	}
}

// And withdrawing trust takes the grant with it: the record is gone, so there
// is nothing left to be allowed. Trusting the artifact again starts from
// nothing, which is the same answer a rebuild gets.
func TestUntrustingTakesTheGrantWithIt(t *testing.T) {
	isolated(t)
	if verr := Add(digestA, "cnpg", "/p"); verr != nil {
		t.Fatal(verr)
	}
	if verr := Allow(digestA, []string{"kubeconfig"}); verr != nil {
		t.Fatal(verr)
	}
	if _, verr := Remove("cnpg"); verr != nil {
		t.Fatal(verr)
	}
	if verr := Add(digestA, "cnpg", "/p"); verr != nil {
		t.Fatal(verr)
	}
	if got := Load().Allowed(digestA); len(got) != 0 {
		t.Errorf("Allowed = %v after untrust and re-trust, want nothing carried over", got)
	}
}

// Allow states the whole grant rather than adding to it, so a location can be
// taken away without taking all of them.
func TestAllowReplacesRatherThanAccumulates(t *testing.T) {
	isolated(t)
	if verr := Add(digestA, "many", "/p"); verr != nil {
		t.Fatal(verr)
	}
	if verr := Allow(digestA, []string{"kubeconfig", "ssh"}); verr != nil {
		t.Fatal(verr)
	}
	if verr := Allow(digestA, []string{"ssh"}); verr != nil {
		t.Fatal(verr)
	}
	got := Load().Allowed(digestA)
	if len(got) != 1 || got[0] != "ssh" {
		t.Errorf("Allowed = %v, want only what the second call stated", got)
	}
	if verr := Allow(digestA, nil); verr != nil {
		t.Fatal(verr)
	}
	if got := Load().Allowed(digestA); len(got) != 0 {
		t.Errorf("Allowed = %v after withdrawing everything", got)
	}
}
