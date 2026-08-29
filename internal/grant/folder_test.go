package grant

import (
	"testing"
	"time"
)

func folderGrant(target, scope string) Grant {
	now := time.Now()
	return Grant{Target: target, Scope: scope, Issued: now, Expires: now.Add(time.Hour)}
}

// **The folder boundary, in both directions.**
//
// A grant that covers too little is an annoyance. One that covers too much is
// the thing this package exists to prevent, so the table carries the
// near-misses rather than only the happy path: the two classic prefix-boundary
// bugs (a sibling that merely starts with the same letters, and a hostname
// that extends the granted one) and the traversal that a server would resolve
// back out of the folder.
func TestAFolderScopeCoversItsRecordsAndNothingElse(t *testing.T) {
	cases := []struct {
		name  string
		scope string // what the grant names
		call  string // what the call names
		want  bool
	}{
		{"a record in the folder", "prod/", "prod/db-password", true},
		{"another record in the folder", "prod/", "prod/api-key", true},
		{"a record in a subfolder", "prod/", "prod/eu/db-password", true},
		{"a record created later", "prod/", "prod/not-yet-invented", true},

		// The whole reason the trailing slash is required rather than
		// inferred. Without it, "prod" covers both of these.
		{"a sibling folder", "prod/", "staging/db-password", false},
		{"a key that merely starts the same", "prod/", "prod-adjacent", false},
		{"the folder's own name as a record", "prod/", "prod", false},

		// The same bug in the shape it actually gets exploited: a host that
		// extends the granted one. This is why the separator has to be part of
		// the prefix and not checked afterwards.
		{"a hostname extending the granted one", "https://api.example.com/",
			"https://api.example.com.evil.com/x", false},
		{"a path under the granted host", "https://api.example.com/",
			"https://api.example.com/v1/things", true},

		// A server resolves this back out of the folder, so covering it would
		// authorize exactly what the operator scoped away from.
		{"a traversal out of the folder", "https://api.example.com/v1/",
			"https://api.example.com/v1/../admin", false},
		{"a traversal in a store key", "prod/", "prod/../staging/db-password", false},
		{"a dot segment", "prod/", "prod/./db-password", false},

		// An exact scope is untouched by any of this.
		{"an exact scope still matches exactly", "prod/db-password", "prod/db-password", true},
		{"an exact scope does not become a prefix", "prod", "prod/db-password", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := folderGrant("kv.get", tc.scope)
			if got := g.covers("kv.get", tc.call, Caller{}); got != tc.want {
				t.Errorf("grant on %q covering a call on %q = %v, want %v",
					tc.scope, tc.call, got, tc.want)
			}
		})
	}
}

// A folder grant is still a grant: the target and the profile are matched the
// way they always were, so widening the *record* does not widen anything else.
func TestAFolderScopeDoesNotWidenTheTargetOrTheProfile(t *testing.T) {
	g := folderGrant("kv.get", "prod/")
	if g.covers("kv.set", "prod/db-password", Caller{}) {
		t.Error("a grant for kv.get covered kv.set")
	}
	if g.covers("kv.get", "prod/db-password", Caller{Profile: "staging"}) {
		t.Error("a grant naming no profile covered a call on one")
	}

	scoped := folderGrant("kv.get", "prod/")
	scoped.Profile = "staging"
	if scoped.covers("kv.get", "prod/db-password", Caller{}) {
		t.Error("a grant for the staging profile covered a call naming none")
	}
	if !scoped.covers("kv.get", "prod/db-password", Caller{Profile: "staging"}) {
		t.Error("a folder grant stopped covering its own profile")
	}
}

// A traversal call is not unreachable, it is merely not *inferred*. The
// operator can still authorize it by naming the whole strange string, where
// nothing is being decided on their behalf.
func TestATraversalScopeIsStillReachableByAnExactGrant(t *testing.T) {
	const weird = "prod/../staging/db-password"
	if !folderGrant("kv.get", weird).covers("kv.get", weird, Caller{}) {
		t.Error("an exact grant stopped covering the exact string it names")
	}
}

func TestCheckScopeRefusesWhatCannotMeanWhatItLooksLike(t *testing.T) {
	for _, tc := range []struct {
		scope string
		code  string
	}{
		{"prod/", ""},
		{"prod/eu/", ""},
		{"prod/db-password", ""},
		{"", ""},
		{"/", "grant.scope.root"},
		{"prod/../staging/", "grant.scope.traversal"},
		{"prod/./", "grant.scope.traversal"},
		{"../", "grant.scope.traversal"},
	} {
		t.Run(tc.scope, func(t *testing.T) {
			verr := CheckScope(tc.scope)
			switch {
			case tc.code == "" && verr != nil:
				t.Errorf("CheckScope(%q) refused: %v", tc.scope, verr)
			case tc.code != "" && verr == nil:
				t.Errorf("CheckScope(%q) allowed a scope that cannot mean what it looks like", tc.scope)
			case tc.code != "" && verr.Code != tc.code:
				t.Errorf("CheckScope(%q) = %s, want %s", tc.scope, verr.Code, tc.code)
			}
			if verr != nil && verr.Hint == "" {
				t.Errorf("CheckScope(%q) refused with no hint", tc.scope)
			}
		})
	}
}

// Covering is covers() made visible, and revoke reports what is still allowed
// through it — so a folder grant left standing has to be findable, or a revoke
// would report a target as closed while the folder still opens it.
func TestCoveringFindsAFolderGrant(t *testing.T) {
	grants := []Grant{folderGrant("kv.get", "prod/")}
	if Covering(grants, "kv.get", "prod/db-password", Caller{}) == nil {
		t.Error("a standing folder grant was invisible to Covering, so a revoke would " +
			"report the record closed while it is still open")
	}
	if Covering(grants, "kv.get", "staging/db-password", Caller{}) != nil {
		t.Error("Covering reported a folder grant as covering a record outside it")
	}
}
