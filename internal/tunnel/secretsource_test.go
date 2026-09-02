package tunnel

import "testing"

// Reading a Secret needs a cluster and a namespace and nothing else — the
// service and port of a full coordinate are the forward's business. That is
// what lets a connection reaching its service directly still keep its
// credentials in a cluster.
func TestASecretSourceComesFromEitherFieldAndNeedsOnlyTwoSegments(t *testing.T) {
	for _, tc := range []struct {
		what        string
		target      Target
		wantCtx, ns string
	}{
		{"a coordinate, whose service half is discarded",
			Target{Kube: "homelab/databases/svc/postgres:5432"}, "homelab", "databases"},
		{"a secret source, with no forward anywhere",
			Target{SecretsFrom: "homelab/databases"}, "homelab", "databases"},
		// config.Check refuses both together long before this; the ordering
		// only decides what happens if that ever stops holding, and keeping the
		// coordinate means the answer does not silently move.
		{"both, where the coordinate wins",
			Target{Kube: "a/b/svc/c:1", SecretsFrom: "x/y"}, "a", "b"},
	} {
		t.Run(tc.what, func(t *testing.T) {
			kctx, ns, verr := tc.target.secretSource()
			if verr != nil {
				t.Fatalf("secretSource: %v", verr)
			}
			if kctx != tc.wantCtx || ns != tc.ns {
				t.Errorf("= %q/%q, want %q/%q", kctx, ns, tc.wantCtx, tc.ns)
			}
		})
	}
}

// The two parsers must not accept each other's shapes. A service and port in
// `secrets-from:` would be a forward somebody believes they declared; a bare
// namespace in `kube:` would be a forward with nowhere to go.
func TestTheTwoCoordinateShapesStayApart(t *testing.T) {
	if _, _, verr := parseKubeNamespace("homelab/databases/svc/postgres:5432"); verr == nil {
		t.Error("a full coordinate was accepted as a secret source")
	}
	if _, _, _, _, _, verr := parseKube("homelab/databases"); verr == nil {
		t.Error("a bare namespace was accepted as a forward coordinate")
	}
	// The argv guard both halves share: these become `--context <v>` and
	// `--namespace <v>` in kubectl's own arguments.
	if _, _, verr := parseKubeNamespace("-kubeconfig=/tmp/mine/ns"); verr == nil {
		t.Error("a flag-shaped context was accepted")
	}
	if _, _, verr := parseKubeNamespace("homelab/"); verr == nil {
		t.Error("an empty namespace was accepted")
	}
}
