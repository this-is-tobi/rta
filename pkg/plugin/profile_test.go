package plugin

import "testing"

// Tunnellable is what every surface that offers a `kube:` or `ssh:` coordinate
// has to ask, and the two shapes it separates are both real: a database plugin
// declares a host and a port a forward fills, and a plugin that reaches its
// service through a CLI of its own declares neither while still having
// configuration.
func TestTunnellableSeparatesTheTwoShapes(t *testing.T) {
	dialled := []Capability{{ID: "db.status", Summary: "s", Safety: Read,
		Inputs: []Field{
			{Name: "host", Type: String, Config: "host", Local: true, Endpoint: EndpointHost},
			{Name: "port", Type: Int, Config: "port", Local: true, Endpoint: EndpointPort},
		}}}
	if !Tunnellable(dialled) {
		t.Error("a plugin declaring a host and a port cannot be reached by a forward")
	}

	// Shaped like plugins/cnpg: configurable, and nothing a forward can fill.
	viaCLI := []Capability{{ID: "cnpg.list", Summary: "s", Safety: Read,
		Inputs: []Field{
			{Name: "context", Type: String, Config: "context", Local: true},
			{Name: "namespace", Type: String},
		}}}
	if Tunnellable(viaCLI) {
		t.Error("a plugin with no endpoint input at all was called tunnellable")
	}
	if !Profilable(viaCLI[0]) {
		t.Error("having no endpoint input made it unprofilable, which is a different question")
	}

	if Tunnellable(nil) {
		t.Error("no capabilities at all was called tunnellable")
	}
}

// An endpoint role a profile could never fill does not make a plugin
// tunnellable: ProfileFillable is the other half of the condition, and a
// forward that could not be written anywhere is a forward opened and ignored.
func TestTunnellableNeedsTheRoleToBeFillable(t *testing.T) {
	// Scope is never profile-filled — a grant is checked against the record
	// named in the call.
	scoped := []Capability{{ID: "x.get", Summary: "s", Safety: Read, Scope: "addr",
		Inputs: []Field{
			{Name: "addr", Type: String, Config: "addr", Local: true, Endpoint: EndpointAddress},
		}}}
	if Tunnellable(scoped) {
		t.Error("an input a profile may not fill was counted as one a forward can")
	}
}

// The plugin part of a capability ID, pinned directly: internal/policy used
// to derive this by hand rather than calling Namespace, and disagreed with
// it for a leading-dot ID — its own i > 0 check treated index 0, where the
// dot actually is, as "no dot found" and returned the string unchanged.
// Every caller of Namespace relies on this exact table, which is what makes
// a hand-rolled second copy of it a bug waiting to happen rather than a
// harmless duplication.
func TestNamespace(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		{"kv.get", "kv"},
		{"vault.kv.get", "vault"},
		{"standalone", "standalone"},
		{".vault", ""},
		{"", ""},
	} {
		if got := Namespace(tc.id); got != tc.want {
			t.Errorf("Namespace(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}
