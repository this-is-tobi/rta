package plugintrust

import (
	"slices"
	"testing"
)

// The paths an operator names are the third decision on the digest: recorded
// for the artifact named and no other, kept when the same bytes are trusted
// again, replaced rather than added to, and refused for bytes not trusted.
func TestPathsRoundTripAgainstTheDigest(t *testing.T) {
	isolated(t)
	if verr := AllowPaths(digestA, []string{"/srv/certs"}); verr == nil {
		t.Fatal("paths were recorded for an artifact nobody trusted")
	}
	if verr := Add(digestA, "pg", "/usr/local/bin/rta-plugin-pg"); verr != nil {
		t.Fatal(verr)
	}
	if got := Load().Paths(digestA); len(got) != 0 {
		t.Errorf("a freshly trusted artifact may read %v — trust is not a grant", got)
	}
	if verr := AllowPaths(digestA, []string{"/srv/certs", "/opt/ca.pem"}); verr != nil {
		t.Fatal(verr)
	}
	if got := Load().Paths(digestA); !slices.Equal(got, []string{"/srv/certs", "/opt/ca.pem"}) {
		t.Errorf("Paths = %v", got)
	}
	if got := Load().Paths(digestB); len(got) != 0 {
		t.Errorf("naming a path for one artifact named it for another: %v", got)
	}
	// The paths and the needs are separate decisions on one entry.
	if verr := Allow(digestA, []string{"kubeconfig"}); verr != nil {
		t.Fatal(verr)
	}
	if got := Load().Paths(digestA); len(got) != 2 {
		t.Errorf("giving a need dropped the paths: %v", got)
	}
	if verr := Add(digestA, "pg", "/elsewhere/rta-plugin-pg"); verr != nil {
		t.Fatal(verr)
	}
	if got := Load().Paths(digestA); len(got) != 2 {
		t.Errorf("trusting the same bytes again dropped the paths: %v", got)
	}
	if verr := AllowPaths(digestA, nil); verr != nil {
		t.Fatal(verr)
	}
	if got := Load().Paths(digestA); got != nil {
		t.Errorf("Paths after taking them all back = %v", got)
	}
	if got := Load().Allowed(digestA); !slices.Equal(got, []string{"kubeconfig"}) {
		t.Errorf("taking the paths back took the need with them: %v", got)
	}
}

// An image decides which bytes run, never what of the operator's machine they
// read: a path in the system root's record is dropped where it is read.
func TestTheSystemRecordCannotCarryAPath(t *testing.T) {
	isolated(t)
	systemRecord(t, Entry{Digest: digestA, Name: "pg", Paths: []string{"/home/someone"}})
	if got := Load().Paths(digestA); len(got) != 0 {
		t.Fatalf("Paths = %v, want nothing the operator did not name", got)
	}
}
