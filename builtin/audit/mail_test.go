package audit

import (
	"context"
	"errors"
	stdnet "net"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/findings"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// find returns the finding for a check, so a test can say what it means
// rather than indexing into a slice whose order is not the point.
func find(r *findings.Report, check string) (findings.Finding, bool) {
	for _, f := range r.Findings {
		if f.Check == check {
			return f, true
		}
	}
	return findings.Finding{}, false
}

func mustFind(t *testing.T, r *findings.Report, check string) findings.Finding {
	t.Helper()
	f, ok := find(r, check)
	if !ok {
		var got []string
		for _, f := range r.Findings {
			got = append(got, f.Check)
		}
		t.Fatalf("no %q finding; got %v", check, got)
	}
	return f
}

func TestMailDomainAcceptsWhatPeopleHaveToHand(t *testing.T) {
	cases := []struct{ in, want string }{
		{"example.com", "example.com"},
		{"EXAMPLE.COM", "example.com"},
		{"  example.com  ", "example.com"},
		{"example.com.", "example.com"}, // a fully-qualified name, trailing dot and all
		{"someone@example.com", "example.com"},
		{"https://example.com/path", "example.com"},
		{"https://example.com:8443", "example.com"},
		{"mail.corp.example.com", "mail.corp.example.com"},
	}
	for _, tc := range cases {
		got, err := mailDomain(tc.in)
		if err != nil {
			t.Errorf("mailDomain(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("mailDomain(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"", "   ", "localhost", "@", "http://", "not a domain"} {
		if got, err := mailDomain(bad); err == nil {
			t.Errorf("mailDomain(%q) accepted it as %q", bad, got)
		}
	}
}

// The domain that gets audited has to be the domain that got approved.
//
// `@` used to be taken first, with LastIndex over the whole argument and
// before the path was cut, so the host came off the end of a path or query.
// The gate does not re-derive it: internal/mcp reserves the grant, asks for
// consent and writes the ledger row from the caller's argument verbatim, so a
// value whose host is not where a reader thinks it is makes the boundary
// record one destination and query another. A folder scope makes it worse —
// `example.com/` covers `example.com/x@evil.internal`.
func TestTheAuditedDomainIsTheOneTheArgumentReadsAs(t *testing.T) {
	// A host hidden behind a path or a query is not the host.
	for _, tc := range []struct{ in, want string }{
		{"http://good.com/x@evil.internal", "good.com"},
		{"https://example.com/blog?ref=x@internal.corp", "example.com"},
		{"https://example.com/a@b/c@d", "example.com"},
		{"example.com/x@evil.internal", "example.com"},
		// A backslash ends the authority too: browsers read it as a slash
		// in every special scheme, so a reader of the string does as well.
		{"example.com\\@evil.internal", "example.com"},
	} {
		got, verr := mailDomain(tc.in)
		if verr != nil {
			t.Errorf("mailDomain(%q): %v", tc.in, verr)
			continue
		}
		if got != tc.want {
			t.Errorf("mailDomain(%q) = %q, want %q — the audit would go somewhere the argument does not name",
				tc.in, got, tc.want)
		}
	}
	// Credentials in front of a host are refused, not trimmed: the value
	// reads as example.com and resolves to evil.internal.
	for _, bad := range []string{
		"https://example.com@evil.internal",
		"https://user:pass@evil.internal",
	} {
		if got, verr := mailDomain(bad); verr == nil {
			t.Errorf("mailDomain(%q) accepted it as %q — it reads as a different host than it audits", bad, got)
		}
	}
}

// The domain half of the DKIM name used to be validated to "contains a dot"
// while the selector half was held to a label grammar, so an IP literal was
// graded as a mail domain and an internationalised name came back as "does
// not exist" when the truth was that rta never asked.
func TestMailDomainRefusesWhatIsNotADNSName(t *testing.T) {
	for _, bad := range []string{
		"192.0.2.1",
		"[2001:db8::1]",
		"https://[2001:db8::1]:8443/",
		strings.Repeat("a", 64) + ".example.com",  // a label past 63
		strings.Repeat("abcdefghij.", 25) + "com", // a name past 253
		"b\u00fccher.de",
		"a.\x1b[31m",
		"has space.com",
		"-leading.com",
		"trailing-.com",
		"double..dot.com",
		"under_score.com",
	} {
		got, verr := mailDomain(bad)
		if verr == nil {
			t.Errorf("mailDomain(%q) accepted it as %q — it would reach the resolver", bad, got)
			continue
		}
		if verr.Code != "audit.mail.baddomain" {
			t.Errorf("mailDomain(%q) refused with %s, want audit.mail.baddomain", bad, verr.Code)
		}
	}
	// An address is refused as an address, not as a character-set problem.
	if _, verr := mailDomain("192.0.2.1"); verr == nil || !strings.Contains(verr.Hint, "address") {
		t.Errorf("an IP literal's refusal should say it is an address: %v", verr)
	}
	// The selector is held to the same grammar, with its own code.
	if verr := checkSelector(strings.Repeat("a", 64)); verr == nil || verr.Code != "audit.mail.badselector" {
		t.Errorf("a 64-character selector label: %v", verr)
	}
}

// selector used to be only trimmed before reaching dkimName — held here to
// the same DNS-label discipline mailDomain already applies to the other
// half of that name.
func TestCheckSelectorAcceptsRealShapesAndRefusesTheRest(t *testing.T) {
	for _, good := range []string{"", "google", "selector1", "20161025", "foo.bar", "s-1.dkim"} {
		if verr := checkSelector(good); verr != nil {
			t.Errorf("checkSelector(%q) = %v, want nil", good, verr)
		}
	}
	for _, bad := range []string{
		" ",
		"has spaces",
		"has/slash",
		"has@at",
		".leadingdot",
		"trailingdot.",
		"double..dot",
		strings.Repeat("a", 254),
	} {
		if verr := checkSelector(bad); verr == nil {
			t.Errorf("checkSelector(%q) accepted it", bad)
		}
	}
}

// The all mechanism is the whole point of an SPF record: everything before it
// says who may send, and it alone says what to do about everybody else.
func TestSPFGradedByHowItEnds(t *testing.T) {
	cases := []struct {
		name   string
		record string
		status string
		says   string
	}{
		{"hard fail", "v=spf1 include:_spf.example.com -all", findings.OK, "-all"},
		{"soft fail", "v=spf1 mx ~all", findings.OK, "~all"},
		{"neutral", "v=spf1 mx ?all", findings.Warn, "neutral"},
		{"pass all", "v=spf1 +all", findings.Fail, "entire internet"},
		{"bare all", "v=spf1 mx all", findings.Fail, "entire internet"},
		{"no all at all", "v=spf1 ip4:192.0.2.0/24", findings.Warn, "no all mechanism"},
		{"redirect", "v=spf1 redirect=_spf.example.com", findings.Info, "redirect="},
		{"case is not significant", "V=SPF1 MX -ALL", findings.OK, "-all"},
		// SPF is evaluated left to right and all matches every host, so a
		// bare all decides the record however it ends. Grading by asking
		// whether "-all" appeared anywhere reported each of these ok.
		{"bare all before a hard fail", "v=spf1 all -all", findings.Fail, "entire internet"},
		{"bare all before a soft fail", "v=spf1 mx all ~all", findings.Fail, "entire internet"},
		{"neutral before a hard fail", "v=spf1 ?all -all", findings.Warn, "neutral"},
		{"an explicit pass before a hard fail", "v=spf1 +all -all", findings.Fail, "entire internet"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, detail := gradeSPFAll(tc.record)
			if status != tc.status {
				t.Errorf("graded %q as %q, want %q (%s)", tc.record, status, tc.status, detail)
			}
			if !strings.Contains(detail, tc.says) {
				t.Errorf("detail does not explain itself: %q", detail)
			}
		})
	}
}

// A bare "all" is a pass — the default qualifier is "+". It is the one SPF
// mistake that reads as harmless and authorises the whole internet, so it
// must not be graded as the missing-all warning.
func TestBareAllIsGradedAsPassNotAsMissing(t *testing.T) {
	bare, _ := gradeSPFAll("v=spf1 mx all")
	plus, _ := gradeSPFAll("v=spf1 mx +all")
	if bare != plus {
		t.Errorf("bare all graded %q but +all graded %q — they mean the same thing", bare, plus)
	}
	if bare != findings.Fail {
		t.Errorf("an unqualified all should be a failure, got %q", bare)
	}
}

// Past ten DNS-querying mechanisms the whole evaluation is a permerror, which
// means the record is published, looks right, and is not applied at all.
// Nothing past the first all is ever evaluated, and the CIDR-only forms the
// grammar allows used to miss the table entirely — both undercounts, in the
// direction that suppresses the finding.
func TestSPFLookupsStopAtTheFirstAllAndCountCIDRForms(t *testing.T) {
	for _, tc := range []struct {
		record string
		want   int
	}{
		{"v=spf1 a/24 -all", 1},
		{"v=spf1 mx/24 -all", 1},
		{"v=spf1 a//64 -all", 1},
		{"v=spf1 mx/24//64 -all", 1},
		{"v=spf1 -all include:ignored.example.com", 0},
		{"v=spf1 mx all include:ignored.example.com", 1},
	} {
		if got := spfLookups(tc.record); got != tc.want {
			t.Errorf("spfLookups(%q) = %d, want %d", tc.record, got, tc.want)
		}
	}
}

func TestSPFLookupsAreCounted(t *testing.T) {
	cases := []struct {
		record string
		want   int
	}{
		{"v=spf1 -all", 0},
		{"v=spf1 ip4:192.0.2.0/24 ip6:2001:db8::/32 -all", 0}, // ip4/ip6 cost nothing
		{"v=spf1 mx a -all", 2},
		{"v=spf1 include:a.com include:b.com -all", 2},
		{"v=spf1 redirect=other.com", 1},
		{"v=spf1 exists:%{i}.example.com ptr -all", 2},
		{"v=spf1 +include:a.com ~mx -all", 2}, // qualifiers do not change the cost
	}
	for _, tc := range cases {
		if got := spfLookups(tc.record); got != tc.want {
			t.Errorf("spfLookups(%q) = %d, want %d", tc.record, got, tc.want)
		}
	}

	over := "v=spf1 " + strings.Repeat("include:x.com ", 11) + "-all"
	r := gradeMail(mailFacts{domain: "d.test", apexTXT: []string{over}})
	if f := mustFind(t, r, "spf-lookups"); f.Status != findings.Fail {
		t.Errorf("11 lookups graded %q, want %q", f.Status, findings.Fail)
	}
}

// Two valid records are worse than one and worse than none: RFC 7208 makes it
// a permerror, so no policy is applied, while the domain looks protected.
func TestTwoSPFRecordsAreAFailure(t *testing.T) {
	r := gradeMail(mailFacts{domain: "d.test", apexTXT: []string{
		"v=spf1 include:a.com -all",
		"v=spf1 include:b.com -all",
	}})
	f := mustFind(t, r, "spf")
	if f.Status != findings.Fail {
		t.Errorf("two SPF records graded %q, want %q", f.Status, findings.Fail)
	}
	if !strings.Contains(f.Detail, "permanent error") {
		t.Errorf("detail should say why two is worse than one: %q", f.Detail)
	}
	// And it must not go on to grade the disposition of a record that will
	// never be evaluated.
	if _, ok := find(r, "spf-lookups"); ok {
		t.Error("graded the lookup count of a record receivers will not apply")
	}
}

// A name that holds SPF also holds verification tokens, site-ownership
// strings and whatever else has accumulated. Picking the wrong one would
// grade a Google verification string as an email policy.
func TestUnrelatedTXTRecordsAreIgnored(t *testing.T) {
	r := gradeMail(mailFacts{domain: "d.test", apexTXT: []string{
		"google-site-verification=abcdef",
		"MS=ms12345678",
		"v=spf1 mx -all",
		"atlassian-domain-verification=xyz",
	}})
	if f := mustFind(t, r, "spf"); f.Status != findings.OK {
		t.Errorf("SPF lost among unrelated TXT records: %+v", f)
	}
}

func TestDMARCPolicyGrading(t *testing.T) {
	cases := []struct {
		name   string
		record string
		status string
	}{
		{"reject", "v=DMARC1; p=reject; rua=mailto:a@d.test", findings.OK},
		{"quarantine", "v=DMARC1; p=quarantine; rua=mailto:a@d.test", findings.Warn},
		{"none", "v=DMARC1; p=none; rua=mailto:a@d.test", findings.Fail},
		{"no policy tag", "v=DMARC1; rua=mailto:a@d.test", findings.Fail},
		{"case insensitive", "v=DMARC1; P=Reject; rua=mailto:a@d.test", findings.OK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gradeMail(mailFacts{domain: "d.test", dmarc: []string{tc.record}})
			if f := mustFind(t, r, "dmarc"); f.Status != tc.status {
				t.Errorf("graded %q as %q, want %q (%s)", tc.record, f.Status, tc.status, f.Detail)
			}
		})
	}

	// A missing record is the failure the whole capability exists to catch.
	r := gradeMail(mailFacts{domain: "d.test"})
	if f := mustFind(t, r, "dmarc"); f.Status != findings.Fail {
		t.Errorf("a missing DMARC record graded %q", f.Status)
	}
}

// RFC 7489 §6.6.3 sends a receiver that finds nothing at _dmarc.<subdomain>
// to the organizational domain's record, where sp= decides what applies —
// so "no DMARC record, receivers have no instruction" was a fail reported
// about a correctly protected subdomain of a p=reject domain. The row says
// so now. What it must not do is soften itself on a label count: without
// the Public Suffix List, example.co.uk and mail.corp.example.com are the
// same shape, and telling the first it might be covered is a false
// all-clear.
func TestAMissingDMARCRecordNamesTheParentPolicyItMayInherit(t *testing.T) {
	for _, domain := range []string{"mail.corp.example.com", "example.co.uk", "example.com"} {
		f := mustFind(t, gradeMail(mailFacts{domain: domain}), "dmarc")
		if f.Status != findings.Fail {
			t.Errorf("%s: a missing record graded %q, want fail", domain, f.Status)
		}
		if !strings.Contains(f.Detail, "sp=") || !strings.Contains(f.Detail, "organizational") {
			t.Errorf("%s: the finding should name the inheritance it did not check: %q", domain, f.Detail)
		}
	}
}

// A rollout left half-finished is the common way a domain ends up believing
// it is protected while most spoofed mail still lands.
func TestDMARCPartialRolloutIsCalledOut(t *testing.T) {
	r := gradeMail(mailFacts{domain: "d.test", dmarc: []string{"v=DMARC1; p=reject; pct=10; rua=mailto:a@d.test"}})
	f := mustFind(t, r, "dmarc-coverage")
	if f.Status != findings.Warn || !strings.Contains(f.Detail, "pct=10") {
		t.Errorf("pct=10 not reported as partial coverage: %+v", f)
	}
	full := gradeMail(mailFacts{domain: "d.test", dmarc: []string{"v=DMARC1; p=reject; pct=100; rua=mailto:a@d.test"}})
	if _, ok := find(full, "dmarc-coverage"); ok {
		t.Error("pct=100 is full coverage and should not be a finding")
	}
}

// pct= decides how much mail the policy touches, so it has to reach the
// policy verdict. It was compared as a string and never folded in, which
// graded `p=reject; pct=0` ok, "failing mail is refused" — a better grade
// than the p=none four lines below it, for exactly the same protection.
func TestDMARCAppliedToNoMailIsNotAPolicy(t *testing.T) {
	for _, record := range []string{
		"v=DMARC1; p=reject; pct=0; rua=mailto:a@d.test",
		"v=DMARC1; p=quarantine; pct=0; rua=mailto:a@d.test",
	} {
		r := gradeMail(mailFacts{domain: "d.test", dmarc: []string{record}})
		f := mustFind(t, r, "dmarc")
		if f.Status != findings.Fail {
			t.Errorf("%q graded %q, want fail — the policy applies to no mail: %s", record, f.Status, f.Detail)
		}
	}
	// And a reject applied to a sample is not a clean bill of health either.
	r := gradeMail(mailFacts{domain: "d.test", dmarc: []string{"v=DMARC1; p=reject; pct=10; rua=mailto:a@d.test"}})
	if f := mustFind(t, r, "dmarc"); f.Status != findings.Warn {
		t.Errorf("p=reject pct=10 graded %q, want warn: %s", f.Status, f.Detail)
	}
}

// RFC 7489 has a receiver discard a record it cannot parse, so a malformed
// pct= means no policy at all — not "the policy is applied to abc percent".
func TestDMARCWithAnUnparseablePercentageHasNoPolicy(t *testing.T) {
	for _, record := range []string{
		"v=DMARC1; p=reject; pct=abc",
		"v=DMARC1; p=reject; pct=140",
		"v=DMARC1; p=reject; pct=-1",
		// An empty pct= is a syntax error under RFC 7489's grammar, which
		// wants one to three digits — not the absent tag whose default is
		// 100, which is what it used to be read as.
		"v=DMARC1; p=reject; pct=",
	} {
		r := gradeMail(mailFacts{domain: "d.test", dmarc: []string{record}})
		f := mustFind(t, r, "dmarc")
		if f.Status != findings.Fail {
			t.Errorf("%q graded %q, want fail: %s", record, f.Status, f.Detail)
		}
	}
}

func TestDMARCWithoutReportingIsAWarning(t *testing.T) {
	r := gradeMail(mailFacts{domain: "d.test", dmarc: []string{"v=DMARC1; p=reject"}})
	if f := mustFind(t, r, "dmarc-reporting"); f.Status != findings.Warn {
		t.Errorf("a policy nobody reports on graded %q", f.Status)
	}
	with := gradeMail(mailFacts{domain: "d.test", dmarc: []string{"v=DMARC1; p=reject; rua=mailto:a@d.test"}})
	if _, ok := find(with, "dmarc-reporting"); ok {
		t.Error("rua= is present; there is nothing to report")
	}
}

// DKIM is the one record that cannot be found from the domain alone. Guessing
// at popular selectors would report "no DKIM" when the truth is "not at the
// names I tried" — a confident lie, and enumeration besides.
func TestDKIMWithoutASelectorSaysSoRatherThanGuessing(t *testing.T) {
	f := mustFind(t, gradeMail(mailFacts{domain: "d.test"}), "dkim")
	if f.Status != findings.Info {
		t.Errorf("an unchecked selector graded %q, want %q", f.Status, findings.Info)
	}
	if !strings.Contains(f.Detail, "--selector") {
		t.Errorf("the finding should say how to check it: %q", f.Detail)
	}
}

func TestDKIMKeyGrading(t *testing.T) {
	base := mailFacts{domain: "d.test", selector: "s1", dkimName: "s1._domainkey.d.test"}

	missing := base
	if f := mustFind(t, gradeMail(missing), "dkim"); f.Status != findings.Fail {
		t.Errorf("a missing key graded %q", f.Status)
	}

	good := base
	good.dkim = []string{"v=DKIM1; k=rsa; p=" + dkimRSA2048}
	if f := mustFind(t, gradeMail(good), "dkim"); f.Status != findings.OK {
		t.Errorf("a published key graded %q: %s", f.Status, f.Detail)
	}

	// RFC 6376 §3.6.1: an empty p= revokes the key. The record is present and
	// well-formed, and every signature made with it fails.
	revoked := base
	revoked.dkim = []string{"v=DKIM1; k=rsa; p="}
	f := mustFind(t, gradeMail(revoked), "dkim")
	if f.Status != findings.Fail || !strings.Contains(f.Detail, "revoke") {
		t.Errorf("a revoked key was not caught: %+v", f)
	}

	// A record with no p= at all is incomplete, which is a different fact
	// from a revoked key and must not cite RFC 6376's revocation about it.
	absent := base
	absent.dkim = []string{"v=DKIM1; k=rsa"}
	f = mustFind(t, gradeMail(absent), "dkim")
	if f.Status != findings.Fail || strings.Contains(f.Detail, "revoke") || !strings.Contains(f.Detail, "no p=") {
		t.Errorf("a record with no p= tag: %+v — want a failure naming the missing tag, not a revocation", f)
	}
}

// One slice entry is one TXT record: net.Resolver.LookupTXT already joins
// the character-strings a long key is published across, so gluing entries
// together only ever concatenated distinct records. The case that broke was
// key rotation — an old revoked key beside a new one joined into a record
// whose first p= was the good one, and the selector graded ok. RFC 6376
// §3.6.2.2 makes the result undefined when a selector holds more than one
// record, which SPF and DMARC already grade and DKIM alone hid.
func TestTwoDKIMRecordsAreNotOneJoinedKey(t *testing.T) {
	base := mailFacts{domain: "d.test", selector: "s1", dkimName: "s1._domainkey.d.test"}
	two := base
	two.dkim = []string{"v=DKIM1; k=rsa; p=", "v=DKIM1; k=rsa; p=MIGfMA0GCSq"}
	f := mustFind(t, gradeMail(two), "dkim")
	if f.Status != findings.Warn {
		t.Errorf("two records at one selector graded %q, want %q: %s", f.Status, findings.Warn, f.Detail)
	}
	if !strings.Contains(f.Detail, "2 TXT records") || !strings.Contains(f.Detail, "undefined") {
		t.Errorf("the finding should count the records and say the outcome is undefined: %q", f.Detail)
	}
	// The reverse order must not grade differently: the point is that no
	// order is the one a verifier reads.
	two.dkim = []string{two.dkim[1], two.dkim[0]}
	if g := mustFind(t, gradeMail(two), "dkim"); g.Status != f.Status {
		t.Errorf("record order changed the grade: %q then %q", f.Status, g.Status)
	}
}

// RFC 8461 splits MTA-STS in two: the _mta-sts TXT record is a version and
// an id, and the policy — mode enforce, testing, or none, which is how it is
// switched off — lives in a file this never fetches. "ok, senders are told
// to require TLS" was an assertion about a document nobody read, and it read
// as ok for a domain whose policy says mode: none.
func TestMTASTSIsNotGradedOKFromTheTXTRecordAlone(t *testing.T) {
	r := gradeMail(mailFacts{domain: "d.test", sts: []string{"v=STSv1; id=20240101"}})
	f := mustFind(t, r, "mta-sts")
	if f.Status != findings.Info {
		t.Errorf("a marker record graded %q, want %q: %s", f.Status, findings.Info, f.Detail)
	}
	if !strings.Contains(f.Detail, "not fetch") || !strings.Contains(f.Detail, "mode") {
		t.Errorf("the finding should say the policy file, where the mode lives, was not read: %q", f.Detail)
	}
	// Absence stays a warning — the one way transport security still reaches
	// the report's grade — but it no longer asserts that nothing tells a
	// sender to require TLS: DANE does, and Go's resolver cannot ask for it.
	none := mustFind(t, gradeMail(mailFacts{domain: "d.test"}), "mta-sts")
	if none.Status != findings.Warn || !strings.Contains(none.Detail, "DANE") {
		t.Errorf("no MTA-STS record: %+v — want warn, naming DANE as the check this cannot make", none)
	}
}

// Public keys as a zone would publish them, generated once with openssl and
// pasted: a 2048-bit RSA key is what RFC 8301 has signers use, 1024 is its
// floor, 512 has been factored, and the Ed25519 key is RFC 8463's raw
// 32-byte form rather than an SPKI.
const (
	dkimRSA512  = "MFwwDQYJKoZIhvcNAQEBBQADSwAwSAJBALCdF7x2XhjkX7Xmbjjat3DGDp9nAMzf1X1HXqHj+LBgQk3Ya6J9kk/cvAFHf0U81VnfMHj9Tq4b3VgWaFtUxyMCAwEAAQ=="
	dkimRSA1024 = "MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDcI70vquxhKp8pw1QWeaaVidqnQi6N03ZXfufwSDO43Em9dpy+XKOWhzgeKXx5Nij2Ob0e2ImFOpEilLw/t1Za9QveusOduguCL1EVvMnbdTA5W8PurJtCR2WWjB3AVbzk4a4k1qrl0lHlEdIE/Bf+LWUORg7id/oz+vVHCKllYwIDAQAB"
	dkimRSA2048 = "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAuXr55gZII37aBB5V7M5/ZUGnuPJhNNcAeC8CbkZArPpA1OWkVnvZYugOOoDNFgo8H/HrofwG6tyXeMZk/x/Arle7oK9bWCyKb/VlmCFmF+u8BwxMkFXfSYVq/aZ4Opw5Ast4t2uuwnvzvocywZRiZxuSiyyuulVeJ3DebfVdRAOBtPByWJjOmCBmz4bEmSqslPV0S5VfDd+CaugqVkEZmyaV3Qol8Lw1pcr411umH65EYf3Ov8VW1ct3najyFHs/o82Z6yVN7DlQ5AXFmehOGbwacjrAP+Q2MZ2cLjskZSpbbsTfPk5SsU7j/mxAVyOyA8Id+Fy4/zr0c8AsCfDtKwIDAQAB"
	dkimEd25519 = "1gncM9lsi4RS0gn7M4ilOZ+o8NV6hRT48m8GheTMSoM="
)

// Presence is not protection. A record carrying t=y is RFC 6376 §3.6.1's
// testing flag — verifiers treat its mail exactly as unsigned, even when
// the signature fails — and a short RSA key is one RFC 8301 has verifiers
// refuse and that can be factored besides. Both used to grade ok, "public
// key published", which is the state this capability exists to name: a
// record that is published, reads as correct, and protects nothing.
func TestDKIMTestingModeAndAShortKeyAreNotAPass(t *testing.T) {
	base := mailFacts{domain: "d.test", selector: "s1", dkimName: "s1._domainkey.d.test"}
	for _, tc := range []struct {
		name, record, status, says string
	}{
		{"2048-bit rsa", "v=DKIM1; k=rsa; p=" + dkimRSA2048, findings.OK, "2048"},
		{"k= defaults to rsa", "v=DKIM1; p=" + dkimRSA2048, findings.OK, "2048"},
		{"a key hand-wrapped across lines", "v=DKIM1; p=" + dkimRSA2048[:100] + " \n\t" + dkimRSA2048[100:], findings.OK, "2048"},
		{"ed25519", "v=DKIM1; k=ed25519; p=" + dkimEd25519, findings.OK, "ed25519"},
		{"1024-bit rsa is the floor", "v=DKIM1; k=rsa; p=" + dkimRSA1024, findings.Warn, "1024"},
		{"512-bit rsa can be forged", "v=DKIM1; k=rsa; p=" + dkimRSA512, findings.Fail, "512"},
		{"testing flag", "v=DKIM1; k=rsa; t=y; p=" + dkimRSA2048, findings.Fail, "unsigned"},
		{"testing flag in a list", "v=DKIM1; t=s:y; p=" + dkimRSA2048, findings.Fail, "unsigned"},
		{"not a key", "v=DKIM1; k=rsa; p=MIGfMA0GCSq", findings.Warn, "not decode"},
		{"an rsa key under k=ed25519", "v=DKIM1; k=ed25519; p=" + dkimRSA2048, findings.Warn, "not decode"},
		{"a key type this does not read", "v=DKIM1; k=dsa; p=" + dkimRSA2048, findings.Warn, "k=dsa"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := base
			f.dkim = []string{tc.record}
			got := mustFind(t, gradeMail(f), "dkim")
			if got.Status != tc.status {
				t.Errorf("graded %q, want %q: %s", got.Status, tc.status, got.Detail)
			}
			if !strings.Contains(got.Detail, tc.says) {
				t.Errorf("detail does not say %q: %q", tc.says, got.Detail)
			}
		})
	}
}

// RFC 7505's null MX is a domain stating that it accepts no mail. It is a
// hardening measure, and grading it as an omission would teach people to
// undo it.
func TestNullMXIsGradedAsHardening(t *testing.T) {
	r := gradeMail(mailFacts{domain: "d.test", mx: []*stdnet.MX{{Host: "."}}})
	f := mustFind(t, r, "mx")
	if f.Status != findings.OK || !strings.Contains(f.Detail, "7505") {
		t.Errorf("null MX not recognised: %+v", f)
	}
}

// A lookup that failed is not a finding about the domain. Reporting "no SPF
// record" because the resolver timed out is the audit lying with confidence.
func TestFailedLookupsAreNotGradedAsFindings(t *testing.T) {
	boom := errors.New("server misbehaving")
	r := gradeMail(mailFacts{
		domain: "d.test", selector: "s1", dkimName: "s1._domainkey.d.test",
		apexErr: boom, dmarcErr: boom, dkimErr: boom, stsErr: boom, rptErr: boom, mxErr: boom,
	})
	for _, check := range []string{"spf", "dmarc", "dkim", "mta-sts", "tls-rpt", "mx"} {
		f := mustFind(t, r, check)
		if f.Status != findings.Info {
			t.Errorf("%s: a failed lookup graded %q, want %q", check, f.Status, findings.Info)
		}
		if !strings.Contains(f.Detail, "failed") {
			t.Errorf("%s: detail should say the lookup failed: %q", check, f.Detail)
		}
	}
	if status, _ := r.Worst(); status != findings.OK {
		t.Errorf("a run where every lookup failed graded the domain %q", status)
	}
}

// Every finding has to land in a section the detail page renders, or it is
// computed, counted in the tally, and then silently dropped from the page.
func TestEveryMailFindingLandsInADeclaredGroup(t *testing.T) {
	declared := map[findings.Group]bool{}
	for _, g := range mailGroupOrder {
		declared[g] = true
	}
	// A spread of facts wide enough to reach every check.
	for _, f := range []mailFacts{
		{domain: "d.test"},
		{domain: "d.test", selector: "s1", dkimName: "s1._domainkey.d.test",
			apexTXT: []string{"v=spf1 " + strings.Repeat("include:x.com ", 11) + "?all"},
			dmarc:   []string{"v=DMARC1; p=none; pct=5"},
			sts:     []string{"v=STSv1; id=1"}, rpt: []string{"v=TLSRPTv1; rua=mailto:a@d.test"},
			dkim: []string{"v=DKIM1; p=abc"}, mx: []*stdnet.MX{{Host: "mx.d.test."}}},
		{domain: "d.test", apexTXT: []string{"v=spf1 -all", "v=spf1 ~all"}},
	} {
		for _, got := range gradeMail(f).Findings {
			if !declared[got.Group] {
				t.Errorf("finding %q is in group %q, which the detail page never renders",
					got.Check, got.Group)
			}
			if got.Ref.CWE == "" {
				t.Errorf("finding %q cites no control", got.Check)
			}
		}
	}
}

// Every one of these records is attacker-influenced text from a third party's
// zone. None of them may panic the grader.
func TestMailGradersSurviveMalformedRecords(t *testing.T) {
	junk := []string{
		"", " ", ";", ";;;", "=", "p=", "v=", "v=spf1", "v=spf1 ", "v=DMARC1",
		"v=DMARC1;", "v=DMARC1; p", "v=DMARC1; p=", "v=DMARC1; ;; p=reject",
		"v=spf1 include:", "v=spf1 -", "v=spf1 ~", "v=spf1 +", "v=spf1 all all all",
		"v=dkim1", "v=DKIM1;;", strings.Repeat("a", 4096),
		"v=spf1 " + strings.Repeat("include:x ", 200), "\x00", "v=spf1 \x00 -all",
		"v=SPF1 REDIRECT=x", "v=DMARC1; pct=", "v=DMARC1; pct=abc; p=reject",
	}
	for _, j := range junk {
		one := []string{j}
		f := mailFacts{
			domain: "d.test", selector: "s", dkimName: "s._domainkey.d.test",
			apexTXT: one, dmarc: one, dkim: one, sts: one, rpt: one,
			mx: []*stdnet.MX{{Host: j}},
		}
		r := gradeMail(f) // must not panic
		if len(r.Findings) == 0 {
			t.Errorf("record %q produced no findings at all", j)
		}
		if _, _ = r.Worst(); false {
			t.Fatal("unreachable")
		}
	}
}

// The detail page is an arrangement of findings the compact table already
// holds, so the two can never disagree about what was found.
func TestMailDetailIsASectionedPage(t *testing.T) {
	f := mailFacts{
		domain: "d.test", selector: "s1", dkimName: "s1._domainkey.d.test",
		apexTXT: []string{"v=spf1 mx -all"},
		dmarc:   []string{"v=DMARC1; p=reject; rua=mailto:a@d.test"},
		dkim:    []string{"v=DKIM1; p=abc"},
		mx:      []*stdnet.MX{{Host: "mx.d.test."}},
	}
	r := gradeMail(f)
	page, ok := r.Page(context.Background(), plugin.Request{}, mailGroupOrder,
		view.KeyValue{Pairs: append([]view.Pair{{Key: "domain", Value: "d.test"}}, r.Grade()...)}).(view.Sections)
	if !ok {
		t.Fatal("the detail view is not a sectioned page")
	}
	// By id, not by heading: the heading is presentation and free to be
	// reworded, which is the whole reason view.Section carries both.
	ids := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.Key())
	}
	for _, want := range []string{"summary", grpSenderAuth.ID, grpRouting.ID, "references"} {
		if !contains(ids, want) {
			t.Errorf("the page has no %q section; got %v", want, ids)
		}
	}
	// An empty group gets no heading: a section with nothing under it reads
	// as a check that failed to run rather than one with no subject.
	if contains(ids, grpMailTLS.ID) {
		for _, item := range page.Items {
			if item.Key() == grpMailTLS.ID {
				if tbl, ok := item.View.(view.Table); ok && len(tbl.Rows) == 0 {
					t.Error("an empty group was given a heading")
				}
			}
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
