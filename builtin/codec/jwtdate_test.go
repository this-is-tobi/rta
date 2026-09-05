package codec

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// decode runs a token through runJWT and hands back its three sections.
func decode(t *testing.T, claims string) (view.KeyValue, string) {
	t.Helper()
	v, err := runJWT(context.Background(), req(map[string]any{
		"token": buildJWT(t, `{"alg":"HS256"}`, claims),
	}))
	if err != nil {
		t.Fatalf("decoding %s: %v", claims, err)
	}
	sections := v.(view.Sections)
	return sections.Items[1].View.(view.KeyValue), sections.Items[2].View.(view.Text).Body
}

// The whole point of the change: `exp 1516242622` is a correctly printed
// integer that nobody can date without knowing today's epoch to subtract from.
// Both halves have to be on the row — the raw number because that is what the
// token contains and what a bug report quotes, the date because that is the
// question being asked.
func TestARegisteredDateClaimIsDatedBesideItsRawNumber(t *testing.T) {
	claims, _ := decode(t, `{"exp":1516242622,"iat":1516239022,"nbf":1516239022}`)
	for _, name := range []string{"exp", "iat", "nbf"} {
		got := pairValue(claims, name)
		if !strings.HasPrefix(got, "15162") {
			t.Errorf("%s = %q, want the raw epoch number first", name, got)
		}
		if !strings.Contains(got, "2018-01-18T") {
			t.Errorf("%s = %q, want the decoded date beside it", name, got)
		}
		if !strings.Contains(got, "ago") {
			t.Errorf("%s = %q, want how long ago it was", name, got)
		}
	}
}

// The units of a number are the specification's business only for the claims
// the specification names. `ver`, a tenant id and a sequence number are all
// plausible ten-digit integers, and dating one of them would invent a fact
// rather than surface one — the reader has no way to tell an invented date
// from a real one, because both render identically.
func TestAnUnregisteredNumericClaimIsNotTurnedIntoADate(t *testing.T) {
	claims, _ := decode(t, `{"exp":1516242622,"ver":1516242622,"sid":1516242622}`)
	if got := pairValue(claims, "ver"); got != "1516242622" {
		t.Errorf("ver = %q, want the bare number — nothing says it is a time", got)
	}
	if got := pairValue(claims, "sid"); got != "1516242622" {
		t.Errorf("sid = %q, want the bare number — nothing says it is a time", got)
	}
	// The same number under the name the RFC does define still gets dated, so
	// this is proof of a rule rather than of the feature being off.
	if got := pairValue(claims, "exp"); !strings.Contains(got, "2018") {
		t.Errorf("exp = %q, want it dated", got)
	}
}

// A claim's type is the issuer's choice, not ours. An `exp` that arrived as a
// string, a bool or an object must render as what it is rather than crash or
// be coerced towards a date.
func TestADateClaimOfTheWrongTypeIsLeftAlone(t *testing.T) {
	claims, window := decode(t, `{"exp":"soon","nbf":true,"iat":{"n":1}}`)
	if got := pairValue(claims, "exp"); got != "soon" {
		t.Errorf("exp = %q, want it left as the string it is", got)
	}
	if got := pairValue(claims, "nbf"); got != "true" {
		t.Errorf("nbf = %q", got)
	}
	if strings.Contains(window, "expired") || strings.Contains(window, "unexpired") {
		t.Errorf("verification section drew a conclusion from an unusable exp: %q", window)
	}
}

// A number far outside any date a time.Time can hold has to come back as the
// number, not as whatever it overflows into. The bound is in timefmt; this is
// the assertion that codec honours the false it returns rather than ignoring
// it.
func TestAnAbsurdExpiryIsLeftAsANumber(t *testing.T) {
	claims, window := decode(t, `{"exp":1e300}`)
	if got := pairValue(claims, "exp"); !strings.HasPrefix(got, "1") || strings.Contains(got, "-01-") {
		t.Errorf("exp = %q, want the raw number and no date", got)
	}
	if window != "" && strings.Contains(window, "exp is") {
		t.Errorf("verification section dated an out-of-range exp: %q", window)
	}
}

// The three windows, each stated as something the token says rather than as
// something rta checked — and every one of them rendering underneath the line
// that says nothing was verified.
func TestTheVerificationSectionReportsTheWindowTheTokenClaims(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name   string
		claims string
		want   string
	}{
		{"expired", fmt.Sprintf(`{"exp":%d}`, now.Add(-time.Hour).Unix()), "expired"},
		{"unexpired", fmt.Sprintf(`{"exp":%d}`, now.Add(time.Hour).Unix()), "unexpired"},
		{"not yet usable", fmt.Sprintf(`{"nbf":%d,"exp":%d}`,
			now.Add(time.Hour).Unix(), now.Add(2*time.Hour).Unix()), "not usable yet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, window := decode(t, tc.claims)
			if !strings.Contains(window, tc.want) {
				t.Errorf("verification section = %q, want it to say %q", window, tc.want)
			}
			if !strings.Contains(strings.ToUpper(window), "NOT VERIFIED") {
				t.Errorf("the window was stated without the unverified line: %q", window)
			}
			// "Its own dates say" — never a bare assertion that the token is
			// good. A reader skimming for the word "valid" must not find rta
			// vouching for a signature it did not check.
			if !strings.Contains(window, "Its own dates say") {
				t.Errorf("window is not attributed to the token: %q", window)
			}
		})
	}
}

// An expired token whose nbf has also not arrived is broken in two ways at
// once, and expiry is the one that settles it: nothing makes it usable again,
// while "not usable yet" reads like waiting would help.
func TestExpiryIsReportedAheadOfAnUnreachedNotBefore(t *testing.T) {
	now := time.Now()
	_, window := decode(t, fmt.Sprintf(`{"exp":%d,"nbf":%d}`,
		now.Add(-time.Hour).Unix(), now.Add(time.Hour).Unix()))
	if !strings.Contains(window, "expired") {
		t.Errorf("verification section = %q, want expiry reported first", window)
	}
}

// A token carrying neither date says nothing about a window rather than
// guessing at one. The unverified line still has to be there on its own.
func TestATokenWithNoDatesGetsNoWindowSentence(t *testing.T) {
	_, window := decode(t, `{"sub":"agent"}`)
	if strings.Contains(window, "Its own dates") {
		t.Errorf("verification section invented a window: %q", window)
	}
	if !strings.Contains(strings.ToUpper(window), "NOT VERIFIED") {
		t.Errorf("lost the unverified line: %q", window)
	}
}
