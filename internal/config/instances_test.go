package config

import (
	"reflect"
	"testing"
)

func TestSplitKey(t *testing.T) {
	for _, tc := range []struct{ key, ns, instance, pin string }{
		{"pg", "pg", "", ""},
		{"pg@1a2b3c4d5e6f", "pg", "", "1a2b3c4d5e6f"},
		{"pg/analytics@1a2b3c4d5e6f", "pg", "analytics", "1a2b3c4d5e6f"},
		{"pg/analytics", "pg", "analytics", ""},
		// Malformed, but the splitter still cuts deterministically; the
		// grammar is refused by validation, not by parsing.
		{"pg/a/b@x", "pg", "a/b", "x"},
	} {
		ns, instance, pin := SplitKey(tc.key)
		if ns != tc.ns || instance != tc.instance || pin != tc.pin {
			t.Errorf("SplitKey(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tc.key, ns, instance, pin, tc.ns, tc.instance, tc.pin)
		}
	}
}

func TestValidRef(t *testing.T) {
	for ref, want := range map[string]bool{
		"staging":           true,
		"staging/analytics": true,
		"staging/":          false,
		"/analytics":        false,
		"staging/a/b":       false,
		"staging/UPPER":     false,
		"Staging/x":         false,
		"":                  false,
	} {
		if got := ValidRef(ref); got != want {
			t.Errorf("ValidRef(%q) = %v, want %v", ref, got, want)
		}
	}
}

// multiInstance is a profile holding staging's three shapes at once: a
// defaulted namespace with a second instance (pg), a namespace with only
// labeled instances (s3), and a plain single entry (vault).
func multiInstance() Profile {
	return Profile{Plugins: map[string]Connection{
		"pg@aaaaaaaaaaaa":           {Set: map[string]any{"host": "main"}},
		"pg/analytics@aaaaaaaaaaaa": {Set: map[string]any{"host": "analytics"}},
		"s3/assets@bbbbbbbbbbbb":    {Set: map[string]any{"bucket": "assets"}},
		"s3/logs@bbbbbbbbbbbb":      {Set: map[string]any{"bucket": "logs"}},
		"vault@cccccccccccc":        {},
	}}
}

// The default rules, in order: an unlabeled entry is the default, a sole
// labeled entry is unambiguous, several labeled entries with no default
// resolve to nothing.
func TestForResolvesTheDefaultInstance(t *testing.T) {
	p := multiInstance()

	key, conn, ok := p.For("pg")
	if !ok || key != "pg@aaaaaaaaaaaa" || conn.Set["host"] != "main" {
		t.Errorf("For(pg) = (%q, %v, %v), want the unlabeled default", key, conn, ok)
	}
	if key, _, ok := p.For("vault"); !ok || key != "vault@cccccccccccc" {
		t.Errorf("For(vault) = (%q, _, %v), want the sole entry", key, ok)
	}
	if _, _, ok := p.For("s3"); ok {
		t.Error("For(s3) resolved despite two labeled instances and no default")
	}
	if _, _, ok := p.For("etcd"); ok {
		t.Error("For(etcd) resolved a namespace the profile does not cover")
	}

	// A sole labeled entry is the answer even though it is labeled.
	solo := Profile{Plugins: map[string]Connection{"pg/main@aaaaaaaaaaaa": {}}}
	if key, _, ok := solo.For("pg"); !ok || key != "pg/main@aaaaaaaaaaaa" {
		t.Errorf("For(pg) on a sole labeled entry = (%q, _, %v)", key, ok)
	}
}

func TestForInstance(t *testing.T) {
	p := multiInstance()
	if key, conn, ok := p.ForInstance("pg", "analytics"); !ok ||
		key != "pg/analytics@aaaaaaaaaaaa" || conn.Set["host"] != "analytics" {
		t.Errorf("ForInstance(pg, analytics) = (%q, %v, %v)", key, conn, ok)
	}
	if key, _, ok := p.ForInstance("pg", ""); !ok || key != "pg@aaaaaaaaaaaa" {
		t.Errorf("ForInstance(pg, \"\") = (%q, _, %v), want the unlabeled entry", key, ok)
	}
	if _, _, ok := p.ForInstance("pg", "missing"); ok {
		t.Error("ForInstance resolved a label that does not exist")
	}
}

// Ambiguity is a property of "no default among several", and Covers stays
// true for it — the profile plainly does cover the namespace.
func TestAmbiguousAndCovers(t *testing.T) {
	p := multiInstance()
	if p.Ambiguous("pg") {
		t.Error("pg is ambiguous despite an unlabeled default")
	}
	if !p.Ambiguous("s3") {
		t.Error("s3 is not ambiguous despite two labeled instances and no default")
	}
	if p.Ambiguous("vault") || p.Ambiguous("etcd") {
		t.Error("a sole or absent entry reported ambiguous")
	}
	for _, ns := range []string{"pg", "s3", "vault"} {
		if !p.Covers(ns) {
			t.Errorf("Covers(%s) = false", ns)
		}
	}
}

// Duplicates are per (namespace, instance): a stale pin beside its
// replacement is one, two instances of one plugin are not.
func TestDuplicatesArePerInstance(t *testing.T) {
	if dups := multiInstance().DuplicateNamespaces(); len(dups) != 0 {
		t.Errorf("multi-instance profile reported duplicates: %v", dups)
	}
	stale := Profile{Plugins: map[string]Connection{
		"pg@oldpin111111":           {},
		"pg@newpin222222":           {},
		"pg/analytics@aaaaaaaaaaaa": {},
		"pg/analytics@bbbbbbbbbbbb": {},
	}}
	want := []string{"pg", "pg/analytics"}
	if got := stale.DuplicateNamespaces(); !reflect.DeepEqual(got, want) {
		t.Errorf("DuplicateNamespaces() = %v, want %v", got, want)
	}
}

func TestNamespacesDeduplicatesInstances(t *testing.T) {
	want := []string{"pg", "s3", "vault"}
	if got := multiInstance().Namespaces(); !reflect.DeepEqual(got, want) {
		t.Errorf("Namespaces() = %v, want %v", got, want)
	}
}

func TestInstancesSortsTheDefaultFirst(t *testing.T) {
	p := multiInstance()
	if got := p.Instances("pg"); !reflect.DeepEqual(got, []string{"", "analytics"}) {
		t.Errorf("Instances(pg) = %v", got)
	}
	if got := p.Instances("s3"); !reflect.DeepEqual(got, []string{"assets", "logs"}) {
		t.Errorf("Instances(s3) = %v", got)
	}
	if got := p.Instances("etcd"); len(got) != 0 {
		t.Errorf("Instances(etcd) = %v", got)
	}
}
