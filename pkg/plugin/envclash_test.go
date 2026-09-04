package plugin

import "testing"

// The collision itself, spelled out. Two different connections, one variable.
func TestTwoProfilesCanDeriveTheSameCredentialVariable(t *testing.T) {
	a := ProfileEnvVar("staging-db", "password")
	b := ProfileEnvVar("staging", "db-password")
	if a != b {
		t.Fatalf("the premise no longer holds: %q vs %q", a, b)
	}
	if a != "RTA_PROFILE_STAGING_DB_PASSWORD" {
		t.Errorf("spelling changed: %q", a)
	}
	if !ProfileEnvAmbiguous("staging-db", "staging") {
		t.Error("the pair that collides is not reported as ambiguous")
	}
}

// Ambiguity is a property of a pair, not of a name. A dash in a profile name
// is ordinary and must stay usable on its own — reporting it in isolation
// would make the check noise, and noise is what gets switched off.
func TestADashInAProfileNameIsNotOnItsOwnAProblem(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"staging-db", "staging", true},
		{"staging", "staging-db", true},
		{"prod", "prod-eu-west", true},
		{"staging-db", "staging-db", false},
		{"staging-db", "prod-db", false},
		{"staging", "stagingdb", false},
		{"staging", "production", false},
		{"a", "a-b", true},
		{"db", "database", false},
	} {
		if got := ProfileEnvAmbiguous(tc.a, tc.b); got != tc.want {
			t.Errorf("ProfileEnvAmbiguous(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// The claim the rule rests on: no two distinct legal profile names share a
// token, so equality is not a case the check has to handle. envToken maps `-`
// to `_` and leaves `[a-z0-9]` alone, which is one-to-one over the alphabet
// profile names are drawn from.
func TestDistinctProfileNamesDeriveDistinctTokens(t *testing.T) {
	names := []string{"a", "a-b", "ab", "a-b-c", "abc", "a0", "a-0", "0a", "staging",
		"staging-db", "stagingdb", "prod", "prod-eu", "prodeu"}
	seen := map[string]string{}
	for _, n := range names {
		tok := envToken(n)
		if prev, ok := seen[tok]; ok {
			t.Errorf("%q and %q both derive %q", prev, n, tok)
		}
		seen[tok] = n
	}
}

// Whenever a pair is unambiguous, no input name can bring them back together:
// the check has to be sufficient, not merely suggestive. Walked over the input
// grammar's own shapes rather than argued.
func TestAnUnambiguousPairStaysUnambiguousUnderEveryInputName(t *testing.T) {
	inputs := []string{"password", "db-password", "user", "a", "a-b", "ssl-mode",
		"db", "b-password", "eu-west-token"}
	profiles := []string{"staging", "staging-db", "stagingdb", "prod", "prod-eu", "a", "a-b"}

	for _, p1 := range profiles {
		for _, p2 := range profiles {
			if p1 == p2 || ProfileEnvAmbiguous(p1, p2) {
				continue
			}
			for _, i1 := range inputs {
				for _, i2 := range inputs {
					if ProfileEnvVar(p1, i1) == ProfileEnvVar(p2, i2) {
						t.Errorf("%q/%q and %q/%q both derive %s, and the pair reads as clean",
							p1, i1, p2, i2, ProfileEnvVar(p1, i1))
					}
				}
			}
		}
	}
}
