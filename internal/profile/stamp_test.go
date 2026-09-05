package profile

import (
	"reflect"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

func profileWith(set map[string]any, secrets map[string]string, ttl string) config.Profile {
	return config.Profile{
		TTL:     ttl,
		Plugins: map[string]config.Connection{"pg@abcd": {Set: set, Secrets: secrets}},
	}
}

// Two reads of one file have to stamp the same, or a long-running surface
// re-resolves on every tick — which for an environment carrying a `secrets:`
// reference is a key derivation every five seconds.
func TestStampIsStableAcrossReads(t *testing.T) {
	p := profileWith(
		map[string]any{"host": "db.internal", "port": 5432, "opts": map[string]any{"b": 2, "a": 1}},
		map[string]string{"password": "kv:prod-db", "token": "kv:prod-token"},
		"2h",
	)
	first := Stamp(p)
	for i := 0; i < 20; i++ {
		if got := Stamp(p); got != first {
			t.Fatalf("stamp %d differs from the first: %q vs %q", i, got, first)
		}
	}
}

// And every part of what a resolution reads has to move it, or the cache it
// keys outlives the thing it describes. Each case is one edit an operator
// makes in the profiles pane.
func TestStampMovesWithEveryStatedThing(t *testing.T) {
	base := profileWith(
		map[string]any{"host": "db.internal"},
		map[string]string{"password": "kv:prod-db"},
		"2h",
	)
	for _, tc := range []struct {
		what string
		p    config.Profile
	}{
		{"a changed value", profileWith(
			map[string]any{"host": "db-2.internal"}, map[string]string{"password": "kv:prod-db"}, "2h")},
		{"an added key", profileWith(
			map[string]any{"host": "db.internal", "region": "eu-west-1"},
			map[string]string{"password": "kv:prod-db"}, "2h")},
		{"a removed key", profileWith(
			nil, map[string]string{"password": "kv:prod-db"}, "2h")},
		{"a re-pointed secret", profileWith(
			map[string]any{"host": "db.internal"}, map[string]string{"password": "kv:other"}, "2h")},
		{"a changed ttl", profileWith(
			map[string]any{"host": "db.internal"}, map[string]string{"password": "kv:prod-db"}, "30m")},
		{"a re-pinned plugin", config.Profile{TTL: "2h", Plugins: map[string]config.Connection{
			// What the operator does after rebuilding a plugin, and the case
			// the report came from.
			"pg@9f8e": {Set: map[string]any{"host": "db.internal"},
				Secrets: map[string]string{"password": "kv:prod-db"}},
		}}},
		{"a second plugin", config.Profile{TTL: "2h", Plugins: map[string]config.Connection{
			"pg@abcd": {Set: map[string]any{"host": "db.internal"},
				Secrets: map[string]string{"password": "kv:prod-db"}},
			"s3@1234": {Set: map[string]any{"endpoint": "https://s3.internal"}},
		}}},
		{"a cluster coordinate", config.Profile{TTL: "2h", Plugins: map[string]config.Connection{
			"pg@abcd": {Set: map[string]any{"host": "db.internal"},
				Secrets: map[string]string{"password": "kv:prod-db"},
				Kube:    "homelab/databases/svc/postgres:5432"},
		}}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			if Stamp(tc.p) == Stamp(base) {
				t.Errorf("%s did not change the stamp, so a cache keyed on it would not notice", tc.what)
			}
		})
	}
}

// A value cannot impersonate another profile's stamp by containing the
// separator the parts are joined with.
func TestStampPartsCannotBeForgedBySeparator(t *testing.T) {
	a := profileWith(map[string]any{"host": "a", "port": "b"}, nil, "")
	b := profileWith(map[string]any{"host": "a\nport\nb"}, nil, "")
	if Stamp(a) == Stamp(b) {
		t.Error("two different profiles stamp the same")
	}
}

// A connection stamp is what a grant is bound to, so the bar is higher than a
// cache key's: no two distinct connections may share one.
func TestConnStampDistinguishesWhatAGrantMustNotConflate(t *testing.T) {
	base := config.Connection{
		Set:     map[string]any{"host": "staging.internal", "port": 5432},
		Secrets: map[string]string{"password": "kv:staging-db"},
	}
	stamp := func(key string, c config.Connection) string { return ConnStamp(key, c) }
	origin := stamp("pg@1b6093cf90ce", base)

	for _, tc := range []struct {
		name string
		key  string
		conn config.Connection
	}{
		{"a repointed host", "pg@1b6093cf90ce", config.Connection{
			Set:     map[string]any{"host": "prod.internal", "port": 5432},
			Secrets: map[string]string{"password": "kv:staging-db"},
		}},
		// The redirect that matters most: same host, different credential.
		{"a different credential", "pg@1b6093cf90ce", config.Connection{
			Set:     map[string]any{"host": "staging.internal", "port": 5432},
			Secrets: map[string]string{"password": "kv:prod-db"},
		}},
		// The artifact pin lives in the key, so re-pinning after a rebuild is
		// itself a different connection — it resolves through another binary.
		{"a re-pinned artifact", "pg@ffffffffffff", base},
		// %v printed 5432 for the integer and for the string alike, so a
		// connection could be retyped under a live grant without moving the
		// stamp. Length-prefixing defends the joins between parts and does
		// nothing about a collapse inside one.
		{"a port that became a string", "pg@1b6093cf90ce", config.Connection{
			Set:     map[string]any{"host": "staging.internal", "port": "5432"},
			Secrets: map[string]string{"password": "kv:staging-db"},
		}},
		{"a value that became a list", "pg@1b6093cf90ce", config.Connection{
			Set:     map[string]any{"host": []any{"staging.internal"}, "port": 5432},
			Secrets: map[string]string{"password": "kv:staging-db"},
		}},
		{"a key removed", "pg@1b6093cf90ce", config.Connection{
			Set:     map[string]any{"host": "staging.internal"},
			Secrets: map[string]string{"password": "kv:staging-db"},
		}},
		// The gap a docs sweep found the day ssh: shipped: the field was in
		// the file, resolved by every call, and absent from this stamp — so a
		// repointed tunnel kept a standing grant's pin matching.
		{"an ssh target added", "pg@1b6093cf90ce", config.Connection{
			Set:     map[string]any{"host": "staging.internal", "port": 5432},
			Secrets: map[string]string{"password": "kv:staging-db"},
			SSH:     "bastion.internal/staging.internal:5432",
		}},
		// The same gap, one axis over: tunnelTLS: true changes what an
		// EndpointURL input resolves to (https instead of http) with
		// everything else — key included — held identical to base. A scheme
		// flip is a repoint in exactly the sense this stamp exists to catch.
		{"tunnelTLS flipped on, everything else unchanged", "pg@1b6093cf90ce", config.Connection{
			Set:       map[string]any{"host": "staging.internal", "port": 5432},
			Secrets:   map[string]string{"password": "kv:staging-db"},
			TunnelTLS: true,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stamp(tc.key, tc.conn); got == origin {
				t.Error("this connection shares a stamp with the one a grant was issued against")
			}
		})
	}

	// A repointed ssh destination is the redirect a grant must see, and the
	// two schemes must not collapse into each other on equal spec strings —
	// the labels are load-bearing, not decoration.
	sshA := config.Connection{SSH: "bastion.internal/db-a.internal:5432"}
	sshB := config.Connection{SSH: "bastion.internal/db-b.internal:5432"}
	if stamp("pg@1b6093cf90ce", sshA) == stamp("pg@1b6093cf90ce", sshB) {
		t.Error("a repointed ssh destination kept the stamp — a standing grant would not notice")
	}
	sameSpec := "x/y:5432"
	if stamp("pg@1b6093cf90ce", config.Connection{Kube: sameSpec}) ==
		stamp("pg@1b6093cf90ce", config.Connection{SSH: sameSpec}) {
		t.Error("a kube: and an ssh: line with the same spelling share a stamp")
	}

	// And it is stable: two reads of the same file must agree, or every grant
	// on it is refused at random.
	for range 20 {
		if stamp("pg@1b6093cf90ce", base) != origin {
			t.Fatal("the same connection stamped differently twice")
		}
	}
}

// The profile's TTL is in Stamp and must not be in ConnStamp.
//
// It is the operator's activation window for `rta use`. It never reaches Bind,
// Fill or Resolve and cannot change where a call goes — so a grant pinned to a
// stamp carrying it would be revoked by editing `ttl: 8h` to `4h`, and the
// safety direction is backwards too, since the selection only ever subtracts.
// The TUI badge does have to notice, which is why Stamp keeps it.
func TestTheActivationWindowMovesTheProfileStampAndNotTheConnectionStamp(t *testing.T) {
	conn := config.Connection{Set: map[string]any{"host": "staging.internal"}}
	short := config.Profile{TTL: "4h", Plugins: map[string]config.Connection{"pg@abcd": conn}}
	long := config.Profile{TTL: "8h", Plugins: map[string]config.Connection{"pg@abcd": conn}}

	if Stamp(short) == Stamp(long) {
		t.Error("the TUI cannot see an activation window change")
	}
	if ConnStamp("pg@abcd", conn) != ConnStamp("pg@abcd", conn) {
		t.Error("ConnStamp is not deterministic")
	}
	// Which is the property that matters: the same connection under either
	// window is the same connection.
	sk, sc, _ := short.For("pg")
	lk, lc, _ := long.For("pg")
	if ConnStamp(sk, sc) != ConnStamp(lk, lc) {
		t.Error("editing a profile's ttl would revoke every live grant on it")
	}
}

// One plugin's entry moving must not revoke a grant on a sibling.
//
// A grant's Target is always one namespace and resolution reads only
// p.For(ns), so an edit to an environment's s3 entry provably cannot affect a
// live pg grant. Stamping the whole environment would have revoked it anyway.
func TestEditingOnePluginsConnectionLeavesASiblingsGrantAlone(t *testing.T) {
	before := config.Config{Profiles: map[string]config.Profile{"staging": {Plugins: map[string]config.Connection{
		"pg@abcd": {Set: map[string]any{"host": "staging.internal"}},
		"s3@ef01": {Set: map[string]any{"endpoint": "https://s3.staging"}},
	}}}}
	after := config.Config{Profiles: map[string]config.Profile{"staging": {Plugins: map[string]config.Connection{
		"pg@abcd": {Set: map[string]any{"host": "staging.internal"}},
		"s3@ef01": {Set: map[string]any{"endpoint": "https://s3.prod"}},
	}}}}

	if ConnStampFor(before, "staging", "pg") != ConnStampFor(after, "staging", "pg") {
		t.Error("editing the s3 entry revoked a pg grant it cannot affect")
	}
	if ConnStampFor(before, "staging", "s3") == ConnStampFor(after, "staging", "s3") {
		t.Error("editing the s3 entry did not move the s3 stamp")
	}
}

// Nothing to stamp answers empty, and empty is what the gate refuses.
func TestAMissingConnectionStampsEmpty(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{"staging": {Plugins: map[string]config.Connection{
		"pg@abcd": {Set: map[string]any{"host": "staging.internal"}},
	}}}}
	for _, tc := range []struct{ name, profile, ns string }{
		{"no name", "", "pg"},
		{"a profile that was deleted or renamed", "gone", "pg"},
		{"a plugin this environment says nothing about", "staging", "s3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConnStampFor(cfg, tc.profile, tc.ns); got != "" {
				t.Errorf("stamp = %q, want empty so the gate refuses", got)
			}
		})
	}
}

// A value may not impersonate its neighbours.
//
// canonical() joined list elements with \x1f and a map's key and value with
// \x1e, and trusted no value to contain those bytes — a rule the values get to
// break. YAML writes either from a double-quoted \xNN escape, nothing upstream
// rejects a container under a fillable key, and both documents load and
// resolve. So two distinct connections shared one ConnStamp, which is the exact
// property the pin a grant is bound to must not lose: the second document in
// each pair below has a different shape, and in the map case is missing a key
// entirely.
func TestNoConnectionCanImpersonateAnother(t *testing.T) {
	stamp := func(set map[string]any) string {
		return ConnStamp("pg@abcd", config.Connection{Set: set})
	}
	for _, tc := range []struct {
		name string
		a, b map[string]any
	}{
		{
			"a list element carrying the element separator",
			map[string]any{"hosts": []any{"a", "b"}},
			map[string]any{"hosts": []any{"a\x1fstr:b"}},
		},
		{
			"a map value carrying the pair separator, dropping a key",
			map[string]any{"opts": map[string]any{"a": "b", "c": "d"}},
			map[string]any{"opts": map[string]any{"a": "b\x1fstr:c\x1estr:d"}},
		},
		{
			// The same shape one level down, since canonical recurses.
			"a nested list inside a map",
			map[string]any{"opts": map[string]any{"h": []any{"a", "b"}}},
			map[string]any{"opts": map[string]any{"h": []any{"a\x1fstr:b"}}},
		},
		{
			// And the plain length case: two lists whose concatenations agree
			// only if nothing records where each element ends.
			"elements regrouped",
			map[string]any{"hosts": []any{"ab", "c"}},
			map[string]any{"hosts": []any{"a", "bc"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if stamp(tc.a) == stamp(tc.b) {
				t.Error("two distinct connections share one stamp, so a grant issued " +
					"against one is honoured against the other")
			}
		})
	}
}

// ConnStamp enumerates Connection's fields by hand, and this session proved
// what that costs: `SSH` was in the file, resolved by every call, and absent
// from the stamp — so a repointed tunnel kept a standing grant's pin
// matching. A hand enumeration cannot notice its own omission, so this test
// counts for it: a field added to config.Connection fails here by name until
// somebody decides, explicitly, whether a grant must see it change.
func TestConnStampIsConfrontedWithEveryConnectionField(t *testing.T) {
	// Every exported field, with the decision recorded: true means ConnStamp
	// writes it, and a false entry documents why not rather than being absent.
	decided := map[string]bool{
		"Set":         true,
		"Secrets":     true, // the reference, never the value — see ConnStamp's doc
		"Kube":        true,
		"SSH":         true,
		"SecretsFrom": true, // repoints which credential authenticates — see ConnStamp's doc
		"TunnelTLS":   true, // a scheme flip is a repoint, same as Kube/SSH — see ConnStamp's doc
	}
	rt := reflect.TypeOf(config.Connection{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		if _, ok := decided[f.Name]; !ok {
			t.Errorf("config.Connection.%s is not accounted for in ConnStamp — decide "+
				"whether a grant must see it change, write it into the stamp or record "+
				"why not, and add it here", f.Name)
		}
		delete(decided, f.Name)
	}
	for name := range decided {
		t.Errorf("ConnStamp's ledger lists %s, which config.Connection no longer has", name)
	}
}

// **The badge is what this stamp exists to keep current, so a recolour has to
// move it.** The TUI caches the environment's name, deadline and colour rather
// than reading them at paint time, and refreshes them only when the stamp
// changes — so a colour left out of the stamp would show the old one until
// something unrelated happened to the profile.
//
// ConnStamp is the other half of the rule and must NOT move: it is what a
// grant is bound to, and recolouring an environment is not a change to what
// any agent may reach.
func TestARecolourMovesTheBadgeStampAndNotTheGrantStamp(t *testing.T) {
	plain := config.Profile{
		TTL:     "1h",
		Plugins: map[string]config.Connection{"pg": {Set: map[string]any{"host": "db"}}},
	}
	red := plain
	red.Color = "#FF6B7A"

	if Stamp(plain) == Stamp(red) {
		t.Error("recolouring an environment did not change Stamp, so the TUI would keep " +
			"painting the old colour until something else about the profile moved")
	}
	if ConnStamp("pg", plain.Plugins["pg"]) != ConnStamp("pg", red.Plugins["pg"]) {
		t.Error("recolouring moved ConnStamp, which is what a grant is bound to — an " +
			"operator changing a colour would silently invalidate consent")
	}
}
