package audit

import (
	stdnet "net"
	"reflect"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/findings"
)

// gradeMail is a pure function from a third party's zone — attacker-chosen
// bytes, every record of them — to a report an operator reads and a model
// acts on. It is the best-shaped fuzz target in the repository: no network,
// no clock, and since the DKIM grader started decoding keys it feeds hostile
// bytes to base64 and crypto/x509 as well. One string, split on newlines
// into records, fills every name at once — a zone that answers every query
// with the same junk — because a grader that survives that survives any
// one name being wrong.
func FuzzGradeMail(f *testing.F) {
	for _, seed := range []string{
		"", " ", ";", ";;;", "=", "p=", "v=", "v=spf1", "v=spf1 ", "v=DMARC1",
		"v=DMARC1;", "v=DMARC1; p", "v=DMARC1; p=", "v=DMARC1; ;; p=reject",
		"v=spf1 include:", "v=spf1 -", "v=spf1 ~", "v=spf1 +", "v=spf1 all all all",
		"v=dkim1", "v=DKIM1;;", strings.Repeat("a", 4096),
		"v=spf1 " + strings.Repeat("include:x ", 200), "\x00", "v=spf1 \x00 -all",
		"v=SPF1 REDIRECT=x", "v=DMARC1; pct=", "v=DMARC1; pct=abc; p=reject",
		"v=spf1 mx -all\nv=DMARC1; p=reject; rua=mailto:a@d.test",
		"v=DKIM1; k=rsa; p=" + dkimRSA2048, "v=DKIM1; k=ed25519; p=" + dkimEd25519,
		"v=DKIM1; k=rsa; t=y; p=" + dkimRSA512, "v=DKIM1; p=MIGfMA0GCSq",
		"v=STSv1; id=1\nv=TLSRPTv1; rua=mailto:a@d.test",
	} {
		f.Add(seed)
	}
	declared := map[findings.Group]bool{}
	for _, g := range mailGroupOrder {
		declared[g] = true
	}
	statuses := map[string]bool{findings.OK: true, findings.Warn: true, findings.Fail: true, findings.Info: true}
	f.Fuzz(func(t *testing.T, zone string) {
		records := strings.Split(zone, "\n")
		facts := mailFacts{
			domain: "d.test", selector: "s", dkimName: "s._domainkey.d.test",
			apexTXT: records, dmarc: records, dkim: records, sts: records, rpt: records,
			mx: []*stdnet.MX{{Host: records[0]}},
		}
		r := gradeMail(facts)
		if len(r.Findings) == 0 {
			t.Fatalf("zone %q produced no findings at all", zone)
		}
		for _, got := range r.Findings {
			switch {
			case !declared[got.Group]:
				t.Fatalf("finding %q is in group %q, which the detail page never renders", got.Check, got.Group)
			case got.Check == "":
				t.Fatalf("a finding with no check name: %+v", got)
			case !statuses[got.Status]:
				t.Fatalf("finding %q has status %q, which is not one the renderer grades", got.Check, got.Status)
			case got.Ref.CWE == "":
				t.Fatalf("finding %q cites no control", got.Check)
			}
		}
		// Pure means the same facts grade the same twice, which is the
		// property that lets every grading rule be tested from a literal.
		if again := gradeMail(facts); !reflect.DeepEqual(r.Findings, again.Findings) {
			t.Fatalf("grading is not a function of its facts:\n%+v\n%+v", r.Findings, again.Findings)
		}
	})
}

// mailDomain decides which domain a grant, a consent prompt and a ledger row
// all describe — from a string those three quote verbatim. Its properties
// are the boundary's: what it accepts is a name checkDomain would accept,
// lowercased, stable under a second pass, and the host of the authority a
// reader of the argument sees rather than a string found somewhere after it.
// That last one is the bug this parser had, expressed as an invariant:
// "internal.corp" is a verbatim substring of
// "https://example.com/blog?ref=x@internal.corp", so substring alone would
// have passed the defect.
func FuzzMailDomain(f *testing.F) {
	for _, seed := range []string{
		"example.com", "EXAMPLE.COM", "  example.com  ", "example.com.", "someone@example.com",
		"https://example.com/path", "https://example.com:8443", "mail.corp.example.com",
		"http://good.com/x@evil.internal", "https://example.com/blog?ref=x@internal.corp",
		"example.com/x@evil.internal", "example.com\\@evil.internal",
		"https://example.com@evil.internal", "https://user:pass@evil.internal",
		"", "   ", "localhost", "@", "http://", "not a domain", "192.0.2.1", "[2001:db8::1]",
		"bücher.de", "a.\x1b[31m", strings.Repeat("a", 64) + ".example.com",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got, verr := mailDomain(raw)
		if verr != nil {
			if got != "" {
				t.Fatalf("mailDomain(%q) refused and still returned %q", raw, got)
			}
			return
		}
		if _, verr := checkDomain(got, raw); verr != nil {
			t.Fatalf("mailDomain(%q) = %q, which checkDomain refuses: %v", raw, got, verr)
		}
		if got != strings.ToLower(got) {
			t.Fatalf("mailDomain(%q) = %q is not lowercase", raw, got)
		}
		if again, verr := mailDomain(got); verr != nil || again != got {
			t.Fatalf("mailDomain is not idempotent: %q -> %q -> %q (%v)", raw, got, again, verr)
		}
		auth := strings.ToLower(raw)
		if i := strings.Index(auth, "://"); i >= 0 {
			auth = auth[i+3:]
		}
		if i := strings.IndexAny(auth, "/?#\\"); i >= 0 {
			auth = auth[:i]
		}
		if !strings.Contains(auth, got) {
			t.Fatalf("mailDomain(%q) = %q, which is not in the authority %q a reader sees", raw, got, auth)
		}
	})
}
