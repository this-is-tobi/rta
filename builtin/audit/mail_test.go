package audit

import (
	"context"
	"errors"
	stdnet "net"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// find returns the finding for a check, so a test can say what it means
// rather than indexing into a slice whose order is not the point.
func find(r *report, check string) (finding, bool) {
	for _, f := range r.findings {
		if f.check == check {
			return f, true
		}
	}
	return finding{}, false
}

func mustFind(t *testing.T, r *report, check string) finding {
	t.Helper()
	f, ok := find(r, check)
	if !ok {
		var got []string
		for _, f := range r.findings {
			got = append(got, f.check)
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

// The all mechanism is the whole point of an SPF record: everything before it
// says who may send, and it alone says what to do about everybody else.
func TestSPFGradedByHowItEnds(t *testing.T) {
	cases := []struct {
		name   string
		record string
		status string
		says   string
	}{
		{"hard fail", "v=spf1 include:_spf.example.com -all", stOK, "-all"},
		{"soft fail", "v=spf1 mx ~all", stOK, "~all"},
		{"neutral", "v=spf1 mx ?all", stWarn, "neutral"},
		{"pass all", "v=spf1 +all", stFail, "entire internet"},
		{"bare all", "v=spf1 mx all", stFail, "entire internet"},
		{"no all at all", "v=spf1 ip4:192.0.2.0/24", stWarn, "no all mechanism"},
		{"redirect", "v=spf1 redirect=_spf.example.com", stInfo, "redirect="},
		{"case is not significant", "V=SPF1 MX -ALL", stOK, "-all"},
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
	if bare != stFail {
		t.Errorf("an unqualified all should be a failure, got %q", bare)
	}
}

// Past ten DNS-querying mechanisms the whole evaluation is a permerror, which
// means the record is published, looks right, and is not applied at all.
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
	if f := mustFind(t, r, "spf-lookups"); f.status != stFail {
		t.Errorf("11 lookups graded %q, want %q", f.status, stFail)
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
	if f.status != stFail {
		t.Errorf("two SPF records graded %q, want %q", f.status, stFail)
	}
	if !strings.Contains(f.detail, "permanent error") {
		t.Errorf("detail should say why two is worse than one: %q", f.detail)
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
	if f := mustFind(t, r, "spf"); f.status != stOK {
		t.Errorf("SPF lost among unrelated TXT records: %+v", f)
	}
}

func TestDMARCPolicyGrading(t *testing.T) {
	cases := []struct {
		name   string
		record string
		status string
	}{
		{"reject", "v=DMARC1; p=reject; rua=mailto:a@d.test", stOK},
		{"quarantine", "v=DMARC1; p=quarantine; rua=mailto:a@d.test", stWarn},
		{"none", "v=DMARC1; p=none; rua=mailto:a@d.test", stFail},
		{"no policy tag", "v=DMARC1; rua=mailto:a@d.test", stFail},
		{"case insensitive", "v=DMARC1; P=Reject; rua=mailto:a@d.test", stOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gradeMail(mailFacts{domain: "d.test", dmarc: []string{tc.record}})
			if f := mustFind(t, r, "dmarc"); f.status != tc.status {
				t.Errorf("graded %q as %q, want %q (%s)", tc.record, f.status, tc.status, f.detail)
			}
		})
	}

	// A missing record is the failure the whole capability exists to catch.
	r := gradeMail(mailFacts{domain: "d.test"})
	if f := mustFind(t, r, "dmarc"); f.status != stFail {
		t.Errorf("a missing DMARC record graded %q", f.status)
	}
}

// A rollout left half-finished is the common way a domain ends up believing
// it is protected while most spoofed mail still lands.
func TestDMARCPartialRolloutIsCalledOut(t *testing.T) {
	r := gradeMail(mailFacts{domain: "d.test", dmarc: []string{"v=DMARC1; p=reject; pct=10; rua=mailto:a@d.test"}})
	f := mustFind(t, r, "dmarc-coverage")
	if f.status != stWarn || !strings.Contains(f.detail, "pct=10") {
		t.Errorf("pct=10 not reported as partial coverage: %+v", f)
	}
	full := gradeMail(mailFacts{domain: "d.test", dmarc: []string{"v=DMARC1; p=reject; pct=100; rua=mailto:a@d.test"}})
	if _, ok := find(full, "dmarc-coverage"); ok {
		t.Error("pct=100 is full coverage and should not be a finding")
	}
}

func TestDMARCWithoutReportingIsAWarning(t *testing.T) {
	r := gradeMail(mailFacts{domain: "d.test", dmarc: []string{"v=DMARC1; p=reject"}})
	if f := mustFind(t, r, "dmarc-reporting"); f.status != stWarn {
		t.Errorf("a policy nobody reports on graded %q", f.status)
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
	if f.status != stInfo {
		t.Errorf("an unchecked selector graded %q, want %q", f.status, stInfo)
	}
	if !strings.Contains(f.detail, "--selector") {
		t.Errorf("the finding should say how to check it: %q", f.detail)
	}
}

func TestDKIMKeyGrading(t *testing.T) {
	base := mailFacts{domain: "d.test", selector: "s1", dkimName: "s1._domainkey.d.test"}

	missing := base
	if f := mustFind(t, gradeMail(missing), "dkim"); f.status != stFail {
		t.Errorf("a missing key graded %q", f.status)
	}

	good := base
	good.dkim = []string{"v=DKIM1; k=rsa; p=MIGfMA0GCSq"}
	if f := mustFind(t, gradeMail(good), "dkim"); f.status != stOK {
		t.Errorf("a published key graded %q: %s", f.status, f.detail)
	}

	// RFC 6376 §3.6.1: an empty p= revokes the key. The record is present and
	// well-formed, and every signature made with it fails.
	revoked := base
	revoked.dkim = []string{"v=DKIM1; k=rsa; p="}
	f := mustFind(t, gradeMail(revoked), "dkim")
	if f.status != stFail || !strings.Contains(f.detail, "revoke") {
		t.Errorf("a revoked key was not caught: %+v", f)
	}

	// Resolvers hand back a long record as several strings meant to be joined.
	split := base
	split.dkim = []string{"v=DKIM1; k=rsa; ", "p=MIGfMA0GCSq"}
	if f := mustFind(t, gradeMail(split), "dkim"); f.status != stOK {
		t.Errorf("a split key was not reassembled: %+v", f)
	}
}

// RFC 7505's null MX is a domain stating that it accepts no mail. It is a
// hardening measure, and grading it as an omission would teach people to
// undo it.
func TestNullMXIsGradedAsHardening(t *testing.T) {
	r := gradeMail(mailFacts{domain: "d.test", mx: []*stdnet.MX{{Host: "."}}})
	f := mustFind(t, r, "mx")
	if f.status != stOK || !strings.Contains(f.detail, "7505") {
		t.Errorf("null MX not recognised: %+v", f)
	}
}

// A lookup that failed is not a finding about the domain. Reporting "no SPF
// record" because the resolver timed out is the audit lying with confidence.
func TestFailedLookupsAreNotGradedAsFindings(t *testing.T) {
	boom := errors.New("server misbehaving")
	r := gradeMail(mailFacts{
		domain: "d.test", selector: "s1", dkimName: "s1._domainkey.d.test",
		apexErr: boom, dmarcErr: boom, dkimErr: boom, stsErr: boom, rptErr: boom,
	})
	for _, check := range []string{"spf", "dmarc", "dkim", "mta-sts", "tls-rpt"} {
		f := mustFind(t, r, check)
		if f.status != stInfo {
			t.Errorf("%s: a failed lookup graded %q, want %q", check, f.status, stInfo)
		}
		if !strings.Contains(f.detail, "failed") {
			t.Errorf("%s: detail should say the lookup failed: %q", check, f.detail)
		}
	}
	if status, _ := r.worst(); status != stOK {
		t.Errorf("a run where every lookup failed graded the domain %q", status)
	}
}

// Every finding has to land in a section the detail page renders, or it is
// computed, counted in the tally, and then silently dropped from the page.
func TestEveryMailFindingLandsInADeclaredGroup(t *testing.T) {
	declared := map[group]bool{}
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
		for _, got := range gradeMail(f).findings {
			if !declared[got.group] {
				t.Errorf("finding %q is in group %q, which the detail page never renders",
					got.check, got.group)
			}
			if got.ref.cwe == "" {
				t.Errorf("finding %q cites no control", got.check)
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
		if len(r.findings) == 0 {
			t.Errorf("record %q produced no findings at all", j)
		}
		if _, _ = r.worst(); false {
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
	page, ok := detailPage(context.Background(), plugin.Request{}, r, mailGroupOrder,
		view.KeyValue{Pairs: append([]view.Pair{{Key: "domain", Value: "d.test"}}, r.grade()...)}).(view.Sections)
	if !ok {
		t.Fatal("the detail view is not a sectioned page")
	}
	// By id, not by heading: the heading is presentation and free to be
	// reworded, which is the whole reason view.Section carries both.
	ids := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.Key())
	}
	for _, want := range []string{"summary", grpSenderAuth.id, grpRouting.id, "references"} {
		if !contains(ids, want) {
			t.Errorf("the page has no %q section; got %v", want, ids)
		}
	}
	// An empty group gets no heading: a section with nothing under it reads
	// as a check that failed to run rather than one with no subject.
	if contains(ids, grpMailTLS.id) {
		for _, item := range page.Items {
			if item.Key() == grpMailTLS.id {
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
