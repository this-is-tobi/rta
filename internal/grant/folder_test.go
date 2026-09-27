package grant

import (
	"strings"
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

		// The same climb spelled so that no segment is literally "..", which
		// the target reads back as one: Go's client sends an escape as
		// written, and a server decodes it before resolving the path.
		{"an escaped traversal", "https://api.example.com/v1/",
			"https://api.example.com/v1/%2e%2e/admin", false},
		{"an escaped traversal in capitals", "https://api.example.com/v1/",
			"https://api.example.com/v1/%2E%2E/admin", false},
		{"half an escaped traversal", "https://api.example.com/v1/",
			"https://api.example.com/v1/.%2e/admin", false},
		{"the other half escaped", "https://api.example.com/v1/",
			"https://api.example.com/v1/%2e./admin", false},
		{"an escaped dot segment", "prod/", "prod/%2e/x", false},
		{"a traversal joined by an escaped slash", "prod/", "prod/..%2fstaging/x", false},
		{"a fully escaped traversal and slash", "https://api.example.com/v1/",
			"https://api.example.com/v1/%2e%2e%2fadmin", false},
		{"a traversal behind a backslash", "https://api.example.com/v1/",
			"https://api.example.com/v1/..\\admin", false},
		{"a traversal behind an escaped backslash", "https://api.example.com/v1/",
			"https://api.example.com/v1/..%5cadmin", false},
		{"a traversal escaped twice", "https://api.example.com/v1/",
			"https://api.example.com/v1/%252e%252e/admin", false},
		{"a traversal a forgiving decoder builds", "https://api.example.com/v1/",
			"https://api.example.com/v1/%%32%65%%32%65/admin", false},
		{"a traversal in IIS's escape", "https://api.example.com/v1/",
			"https://api.example.com/v1/%u002e%u002e/admin", false},
		{"a traversal with a path parameter", "https://api.example.com/v1/",
			"https://api.example.com/v1/..;/admin", false},
		{"a traversal ending the path before a query", "https://api.example.com/v1/",
			"https://api.example.com/v1/..?page=1", false},
		{"a traversal with a trailing space", "https://api.example.com/v1/",
			"https://api.example.com/v1/..%20/admin", false},
		{"a traversal cut short by NUL", "https://api.example.com/v1/",
			"https://api.example.com/v1/..%00.json", false},
		{"a traversal in overlong UTF-8", "https://api.example.com/v1/",
			"https://api.example.com/v1/%c0%ae%c0%ae/admin", false},
		{"a traversal in fullwidth dots", "prod/",
			"prod/" + string(rune(0xFF0E)) + string(rune(0xFF0E)) + "/staging", false},
		{"a traversal in a vertical two-dot leader", "prod/",
			"prod/" + string(rune(0xFE30)) + "/staging", false},
		{"a traversal behind an overlong slash", "prod/", "prod/..%c0%afstaging/x", false},
		{"a traversal behind a three-byte overlong slash", "prod/", "prod/..%e0%80%afstaging/x", false},
		{"a traversal behind an overlong backslash", "https://api.example.com/v1/",
			"https://api.example.com/v1/..%c1%9cadmin", false},
		{"a traversal in overlong dots and slash", "prod/", "prod/%c0%ae%c0%ae%c0%afstaging/x", false},
		{"a traversal behind a fullwidth solidus", "prod/",
			"prod/.." + string(rune(0xFF0F)) + "staging/x", false},
		{"a traversal behind a fullwidth reverse solidus", "prod/",
			"prod/.." + string(rune(0xFF3C)) + "staging/x", false},
		{"a traversal behind a small reverse solidus", "prod/",
			"prod/.." + string(rune(0xFE68)) + "staging/x", false},
		{"a traversal in fullwidth escapes", "prod/",
			"prod/" + string(rune(0xFF05)) + "2e" + string(rune(0xFF05)) + "2e/staging", false},
		{"a traversal behind a division slash", "prod/",
			"prod/.." + string(rune(0x2215)) + "staging/x", false},
		{"a traversal behind an escaped fraction slash", "prod/", "prod/..%e2%81%84staging/x", false},
		{"a traversal behind a set minus", "https://api.example.com/v1/",
			"https://api.example.com/v1/.." + string(rune(0x2216)) + "admin", false},
		{"a traversal behind a yen sign", "https://api.example.com/v1/",
			"https://api.example.com/v1/.." + string(rune(0xA5)) + "admin", false},
		{"a traversal behind an escaped won sign", "https://api.example.com/v1/",
			"https://api.example.com/v1/..%e2%82%a9admin", false},
		{"a traversal padded with a plus", "https://api.example.com/v1/",
			"https://api.example.com/v1/..+/admin", false},
		{"a traversal joined by a grapheme joiner", "prod/",
			"prod/." + string(rune(0x034F)) + "./staging", false},
		{"a traversal padded with a variation selector", "prod/",
			"prod/.." + string(rune(0xFE0F)) + "/staging", false},
		{"a traversal padded with an escaped filler", "prod/", "prod/..%e3%85%a4/staging", false},

		// None of that makes an escape or a dot suspicious in itself.
		{"an escaped space", "https://api.example.com/v1/",
			"https://api.example.com/v1/a%20b", true},
		{"an escaped percent in a query", "https://api.example.com/v1/",
			"https://api.example.com/v1/search?q=100%25", true},
		{"a percent that starts no escape", "prod/", "prod/50%off", true},
		{"a dotfile", "prod/", "prod/.env", true},
		{"a dotted name", "prod/", "prod/v1.2/notes", true},
		{"a name its decomposition changes", "prod/", "prod/" + string(rune(0xFB01)) + "le.txt", true},
		{"a name beyond ASCII", "prod/", "prod/caf" + string(rune(0xE9)) + "/menu", true},
		{"an escaped name beyond ASCII", "prod/", "prod/caf%c3%a9/menu", true},
		{"a plus in a name", "prod/", "prod/c++/notes", true},
		{"a yen sign in a name", "prod/", "prod/" + string(rune(0xA5)) + "100/receipt", true},
		{"a variation selector in a name", "prod/", "prod/a" + string(rune(0xFE0F)) + "/notes", true},

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
		{"https://h/v1/%2e%2e/", "grant.scope.traversal"},
		{"prod/%2E/", "grant.scope.traversal"},
		{"prod/..%2fstaging/", "grant.scope.traversal"},
		{"prod/..%c0%afstaging/", "grant.scope.traversal"},
		{"prod/.." + string(rune(0xFF0F)) + "staging/", "grant.scope.traversal"},
		{"prod/.." + string(rune(0xA5)) + "staging/", "grant.scope.traversal"},
		{"prod/..+/", "grant.scope.traversal"},
		{"https://h/v1/a%20b/", ""},
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

// Issue refuses what CheckScope refuses, whichever path built the grant:
// answering a parked call with --ttl issues the record the agent named,
// and never passed through the check grant.allow makes.
func TestIssueRefusesAScopeCheckScopeRefuses(t *testing.T) {
	setup(t)
	for _, scope := range []string{"/", "prod/%2e%2e/"} {
		verr := Issue(folderGrant("kv.get", scope), true)
		if verr == nil || !strings.HasPrefix(verr.Code, "grant.scope.") {
			t.Errorf("Issue on %q = %v, want a grant.scope refusal", scope, verr)
		}
	}
	if grants, _ := Load(); len(grants) != 0 {
		t.Fatalf("a refused scope was stored: %+v", grants)
	}
}
