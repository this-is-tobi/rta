package audit

import (
	"context"
	"errors"
	stdnet "net"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// "Can somebody send mail as us?" is a question with an awkward manual
// answer — four records under three different names, each with its own
// grammar, two of which are only meaningful in terms of the others — and it
// is asked far more often than it is answered properly.
//
// It is named for the question rather than the mechanism, the same way
// audit.web is: these are all DNS lookups, but nobody reaches for them while
// debugging DNS. `net dns` is where "what does this name resolve to" lives.
//
// Every check is a single TXT or MX lookup of a name derived from the domain
// by rule, so the whole audit is a handful of queries against records that
// are published for the world to read. Nothing is enumerated or guessed at —
// which is also the reason DKIM needs a selector handed to it (see below).

// Groups order the detail page and name its sections.
var (
	grpSenderAuth = group{"sender-auth", "sender authentication"}
	grpMailTLS    = group{"transport", "transport security"}
	grpRouting    = group{"routing", "routing"}
)

var mailGroupOrder = []group{grpSenderAuth, grpMailTLS, grpRouting}

// spfLookupLimit is RFC 7208 §4.6.4's cap on DNS-querying mechanisms in one
// SPF evaluation. Past it, evaluation returns permerror and the policy stops
// being applied at all — the record is published, looks correct, and does
// nothing, which is the worst of the three possible states.
const spfLookupLimit = 10

func runMail(ctx context.Context, req plugin.Request) (view.View, error) {
	domain, err := mailDomain(req.String("domain"))
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(req.Int("timeout")) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	res := &stdnet.Resolver{}
	if err := requireDomain(ctx, res, domain); err != nil {
		return nil, err
	}
	r := gradeMail(lookupMail(ctx, res, domain, strings.TrimSpace(req.String("selector"))))

	if req.Bool("detail") {
		summary := append([]view.Pair{{Key: "domain", Value: domain}}, r.grade()...)
		summary = append(summary, mailDeeper(domain)...)
		return detailPage(ctx, req, r, mailGroupOrder, view.KeyValue{Pairs: summary}), nil
	}
	return r.table(true), nil
}

// mailDomain accepts what people have to hand: a domain, an address they were
// looking at, or a URL they copied from the browser.
func mailDomain(raw string) (string, *view.Error) {
	d := strings.TrimSpace(raw)
	if i := strings.LastIndex(d, "@"); i >= 0 {
		d = d[i+1:]
	}
	if i := strings.Index(d, "://"); i >= 0 {
		d = d[i+3:]
	}
	d = strings.TrimSuffix(strings.TrimSpace(strings.Trim(d, ".")), "/")
	if i := strings.IndexAny(d, "/:"); i >= 0 {
		d = d[:i]
	}
	if d == "" || !strings.Contains(d, ".") {
		return "", view.Errorf("audit.mail.baddomain", "not a domain: %q", raw).
			WithHint("pass a domain like example.com, or an address at it")
	}
	return strings.ToLower(d), nil
}

// mailFacts is everything the audit read out of DNS, gathered in one place so
// that deciding what it means is a pure function of it.
//
// The split is not ceremony: grading is where the judgement lives, it is the
// part that will be argued with and adjusted, and tying it to a live resolver
// would mean every test of "is p=none really a failure" needed a DNS server
// and a domain configured to be wrong. Now the whole grade is exercised from
// a literal.
type mailFacts struct {
	domain   string
	selector string

	apexTXT  []string // TXT at the domain itself, where SPF lives
	apexErr  error
	dkimName string // the full selector._domainkey.domain name, for the report
	dkim     []string
	dkimErr  error
	dmarc    []string
	dmarcErr error
	sts      []string
	stsErr   error
	rpt      []string
	rptErr   error
	mx       []*stdnet.MX
	mxErr    error
}

// lookupMail performs every query the audit makes: one TXT at the apex, one
// per policy name, one MX. Names are derived from the domain by rule, never
// guessed — the DKIM selector is the single thing that cannot be, which is
// why it is asked for rather than searched.
func lookupMail(ctx context.Context, res *stdnet.Resolver, domain, selector string) mailFacts {
	f := mailFacts{domain: domain, selector: selector}
	f.apexTXT, f.apexErr = txt(ctx, res, domain)
	f.dmarc, f.dmarcErr = txt(ctx, res, "_dmarc."+domain)
	f.sts, f.stsErr = txt(ctx, res, "_mta-sts."+domain)
	f.rpt, f.rptErr = txt(ctx, res, "_smtp._tls."+domain)
	if selector != "" {
		f.dkimName = selector + "._domainkey." + domain
		f.dkim, f.dkimErr = txt(ctx, res, f.dkimName)
	}
	if f.mx, f.mxErr = res.LookupMX(ctx, domain); f.mxErr != nil && notFound(f.mxErr) {
		f.mxErr = nil // "this domain receives no mail" is an answer, not a failure
	}
	return f
}

// gradeMail turns the facts into findings. Pure: same records in, same report
// out, no clock and no network.
func gradeMail(f mailFacts) *report {
	r := &report{}
	auditSPF(r, f)
	auditDKIM(r, f)
	auditDMARC(r, f)
	auditMailTransport(r, f)
	auditMailRouting(r, f)
	return r
}

// txt looks up TXT records, telling "there is no such name" apart from "the
// lookup failed". The difference matters: the first is a finding about the
// domain, the second is a finding about the network, and reporting one as
// the other is how an audit tells a confident lie.
func txt(ctx context.Context, res *stdnet.Resolver, name string) ([]string, error) {
	got, err := res.LookupTXT(ctx, name)
	if err != nil {
		if notFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return got, nil
}

// pick returns the records carrying the given version tag. Every one of these
// record types shares the convention, and every name they live under can hold
// unrelated TXT records too (domain verification tokens, mostly).
func pick(records []string, prefix string) []string {
	var out []string
	for _, rec := range records {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rec)), prefix) {
			out = append(out, strings.TrimSpace(rec))
		}
	}
	return out
}

func auditSPF(r *report, f mailFacts) {
	if f.apexErr != nil {
		r.add(grpSenderAuth, "spf", stInfo, "lookup failed: "+f.apexErr.Error(), refSpoofing)
		return
	}
	spf := pick(f.apexTXT, "v=spf1")
	switch {
	case len(spf) == 0:
		r.add(grpSenderAuth, "spf", stFail,
			"no SPF record — any host on the internet can send mail claiming to be this domain",
			refSpoofing)
		return
	case len(spf) > 1:
		// RFC 7208 §4.5: more than one is a permerror, and a permerror means
		// receivers apply no policy at all. Two "correct" records are worse
		// than one, and worse than none, because they look like protection.
		r.add(grpSenderAuth, "spf", stFail,
			plural(len(spf), "SPF record")+" published — RFC 7208 makes this a permanent error, "+
				"so receivers apply no SPF policy at all", refSpoofing)
		return
	}

	record := spf[0]
	status, detail := gradeSPFAll(record)
	r.add(grpSenderAuth, "spf", status, detail, refSpoofing)

	// The lookup cap is counted over the mechanisms this record states
	// directly. Anything reached through an include: adds its own, which is
	// why the finding says "at least" rather than pretending to a total it
	// cannot see without walking the tree — and walking it would make this a
	// crawler, which the plugin does not do.
	if n := spfLookups(record); n > spfLookupLimit {
		r.add(grpSenderAuth, "spf-lookups", stFail,
			"at least "+strconv.Itoa(n)+" DNS-querying mechanisms, over RFC 7208's limit of "+
				strconv.Itoa(spfLookupLimit)+" — evaluation returns permerror and the policy is not applied",
			refSpoofing)
	} else if n > spfLookupLimit-3 {
		r.add(grpSenderAuth, "spf-lookups", stWarn,
			"at least "+strconv.Itoa(n)+" of RFC 7208's "+strconv.Itoa(spfLookupLimit)+
				" DNS-querying mechanisms used directly; each include: adds its own",
			refSpoofing)
	}
}

// gradeSPFAll grades the record's final disposition — the part that decides
// what a receiver does with mail from an unlisted host. Everything before it
// only says who is allowed.
func gradeSPFAll(record string) (string, string) {
	switch {
	case spfHasMechanism(record, "+all"), spfHasMechanism(record, "all") && !spfHasQualifiedAll(record):
		return stFail, "ends in +all — this authorises the entire internet to send as the domain, " +
			"which is worse than publishing nothing: " + clip(record)
	case spfHasMechanism(record, "-all"):
		return stOK, "ends in -all (hard fail): " + clip(record)
	case spfHasMechanism(record, "~all"):
		return stOK, "ends in ~all (soft fail) — fine alongside an enforcing DMARC policy: " + clip(record)
	case spfHasMechanism(record, "?all"):
		return stWarn, "ends in ?all (neutral), which asks receivers to treat unlisted senders " +
			"exactly as if no SPF record existed: " + clip(record)
	case strings.Contains(strings.ToLower(record), "redirect="):
		return stInfo, "delegates its policy with redirect=: " + clip(record)
	}
	return stWarn, "no all mechanism — unlisted senders get no verdict, which receivers treat as neutral: " +
		clip(record)
}

func spfHasMechanism(record, mech string) bool {
	for _, f := range strings.Fields(strings.ToLower(record)) {
		if f == mech {
			return true
		}
	}
	return false
}

func spfHasQualifiedAll(record string) bool {
	for _, q := range []string{"-all", "~all", "?all", "+all"} {
		if spfHasMechanism(record, q) {
			return true
		}
	}
	return false
}

// spfLookups counts the mechanisms that cost a DNS query, per RFC 7208 §4.6.4.
func spfLookups(record string) int {
	n := 0
	for _, f := range strings.Fields(strings.ToLower(record)) {
		f = strings.TrimLeft(f, "+-~?")
		name, _, _ := strings.Cut(f, ":")
		name, _, _ = strings.Cut(name, "=")
		switch name {
		case "include", "a", "mx", "ptr", "exists", "redirect":
			n++
		}
	}
	return n
}

// DKIM is the one record here that cannot be found from the domain alone: the
// selector is chosen by whoever signs, and there is no record listing them.
// Guessing at a list of popular selectors would be enumeration, which this
// plugin does not do — and a miss would be reported as "no DKIM" when the
// truth is "not at the names I tried", which is a confident lie.
func auditDKIM(r *report, f mailFacts) {
	if f.selector == "" {
		r.add(grpSenderAuth, "dkim", stInfo,
			"not checked — DKIM selectors cannot be discovered from the domain; "+
				"pass --selector, taking the s= tag from a DKIM-Signature header on a message you received",
			refSpoofing)
		return
	}
	name := f.dkimName
	if f.dkimErr != nil {
		r.add(grpSenderAuth, "dkim", stInfo, "lookup of "+name+" failed: "+f.dkimErr.Error(), refSpoofing)
		return
	}
	records := f.dkim
	if len(records) == 0 {
		r.add(grpSenderAuth, "dkim", stFail,
			"no DKIM key at "+name+" — messages signed with this selector cannot be verified", refSpoofing)
		return
	}
	// A DKIM record may be split across strings; the resolver hands them back
	// separately and they are meant to be concatenated.
	joined := strings.Join(records, "")
	if !strings.Contains(strings.ToLower(joined), "v=dkim1") && !strings.Contains(joined, "p=") {
		r.add(grpSenderAuth, "dkim", stWarn,
			"a TXT record exists at "+name+" but does not look like a DKIM key", refSpoofing)
		return
	}
	// An empty p= is the documented way to revoke a key (RFC 6376 §3.6.1),
	// so a record can be present and still mean "this key is dead".
	if p := dkimTag(joined, "p"); p == "" {
		r.add(grpSenderAuth, "dkim", stFail,
			"the key at "+name+" has an empty p= tag, which revokes it — signatures made with it will not verify",
			refSpoofing)
		return
	}
	r.add(grpSenderAuth, "dkim", stOK, "public key published at "+name, refSpoofing)
}

func dkimTag(record, tag string) string {
	for _, part := range strings.Split(record, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), tag) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func auditDMARC(r *report, f mailFacts) {
	name := "_dmarc." + f.domain
	if f.dmarcErr != nil {
		r.add(grpSenderAuth, "dmarc", stInfo, "lookup of "+name+" failed: "+f.dmarcErr.Error(), refSpoofing)
		return
	}
	dmarc := pick(f.dmarc, "v=dmarc1")
	if len(dmarc) == 0 {
		r.add(grpSenderAuth, "dmarc", stFail,
			"no DMARC record at "+name+" — receivers have no instruction for mail that fails SPF and DKIM, "+
				"and SPF alone does not cover the address a reader actually sees", refSpoofing)
		return
	}
	if len(dmarc) > 1 {
		r.add(grpSenderAuth, "dmarc", stFail,
			plural(len(dmarc), "DMARC record")+" at "+name+" — RFC 7489 requires receivers to ignore "+
				"the domain's policy entirely when more than one is published", refSpoofing)
		return
	}

	record := dmarc[0]
	switch policy := strings.ToLower(dkimTag(record, "p")); policy {
	case "reject":
		r.add(grpSenderAuth, "dmarc", stOK, "p=reject — failing mail is refused: "+clip(record), refSpoofing)
	case "quarantine":
		r.add(grpSenderAuth, "dmarc", stWarn,
			"p=quarantine — failing mail is delivered to spam rather than refused: "+clip(record), refSpoofing)
	case "none":
		r.add(grpSenderAuth, "dmarc", stFail,
			"p=none — monitoring only, so spoofed mail is still delivered to the inbox: "+clip(record),
			refSpoofing)
	default:
		r.add(grpSenderAuth, "dmarc", stFail,
			"no usable p= tag, which makes the record invalid: "+clip(record), refSpoofing)
	}

	// pct= applies the policy to a sample. It exists for staged rollouts, and
	// a rollout left half-finished is the common way a domain ends up
	// believing it is protected while most spoofed mail still lands.
	if pct := dkimTag(record, "pct"); pct != "" && pct != "100" {
		r.add(grpSenderAuth, "dmarc-coverage", stWarn,
			"pct="+pct+" — the policy is applied to that percentage of mail; the rest is delivered as if "+
				"there were no policy", refSpoofing)
	}
	if dkimTag(record, "rua") == "" {
		r.add(grpSenderAuth, "dmarc-reporting", stWarn,
			"no rua= address — nothing reports back, so a policy that is breaking legitimate mail "+
				"or failing to stop spoofing looks identical to one that is working", refSpoofing)
	}
}

// MTA-STS and TLS-RPT are about the hop between mail servers. Without them,
// SMTP's opportunistic TLS can be stripped by anything on the path, and the
// sending server has no way to know it was supposed to insist.
func auditMailTransport(r *report, f mailFacts) {
	switch {
	case f.stsErr != nil:
		r.add(grpMailTLS, "mta-sts", stInfo, "lookup failed: "+f.stsErr.Error(), refCleartext)
	case len(pick(f.sts, "v=stsv1")) == 0:
		r.add(grpMailTLS, "mta-sts", stWarn,
			"no MTA-STS policy — a sending server has no instruction to require TLS, so an attacker on "+
				"the path can strip it and the mail is delivered in the clear", refCleartext)
	default:
		r.add(grpMailTLS, "mta-sts", stOK, "policy published — senders are told to require TLS", refCleartext)
	}

	switch {
	case f.rptErr != nil:
		r.add(grpMailTLS, "tls-rpt", stInfo, "lookup failed: "+f.rptErr.Error(), refCleartext)
	case len(pick(f.rpt, "v=tlsrptv1")) == 0:
		r.add(grpMailTLS, "tls-rpt", stInfo,
			"no TLS-RPT record — failed TLS deliveries to this domain are not reported to anybody",
			refCleartext)
	default:
		r.add(grpMailTLS, "tls-rpt", stOK, "senders report TLS delivery failures", refCleartext)
	}
}

// requireDomain refuses to grade a name that does not exist. Every check
// below reads an absent record as a finding — "no SPF record", "no DMARC
// record" — which is exactly right for a real domain and a confident lie
// about a typo. A misspelling would otherwise come back as a full report
// card, in the shape of an answer, about nothing.
//
// A domain is taken to exist if it publishes anything at all: an address,
// a mail exchanger, or a TXT record. Mail-only domains have no address
// records and would fail a naive resolve, and a domain with no mail is
// precisely the case the audit still has something to say about.
func requireDomain(ctx context.Context, res *stdnet.Resolver, domain string) *view.Error {
	if _, err := res.LookupHost(ctx, domain); err == nil {
		return nil
	} else if !notFound(err) {
		return view.Errorf("audit.mail.resolver", "resolving %s: %v", domain, err).
			WithHint("the lookup failed rather than coming back empty — check your resolver, or --timeout")
	}
	if mx, err := res.LookupMX(ctx, domain); err == nil && len(mx) > 0 {
		return nil
	}
	if recs, err := res.LookupTXT(ctx, domain); err == nil && len(recs) > 0 {
		return nil
	}
	return view.Errorf("audit.mail.nxdomain", "%s does not exist in DNS", domain).
		WithHint("check the spelling — every check below would otherwise report its record as missing")
}

// notFound distinguishes an empty answer from a broken lookup.
func notFound(err error) bool {
	var dnsErr *stdnet.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

func auditMailRouting(r *report, f mailFacts) {
	mx := f.mx
	if f.mxErr != nil || len(mx) == 0 {
		r.add(grpRouting, "mx", stInfo, "no MX records — this domain does not receive mail", refSpoofing)
		return
	}
	// RFC 7505: a single "." host is the explicit statement that a domain
	// accepts no mail. It is a hardening measure, not an omission, and
	// grading it as one would train people to undo it.
	if len(mx) == 1 && strings.TrimSuffix(mx[0].Host, ".") == "" {
		r.add(grpRouting, "mx", stOK,
			"null MX (RFC 7505) — the domain states explicitly that it accepts no mail", refSpoofing)
		return
	}
	hosts := make([]string, 0, len(mx))
	for _, m := range mx {
		hosts = append(hosts, strings.TrimSuffix(m.Host, "."))
	}
	r.add(grpRouting, "mx", stInfo, plural(len(hosts), "mail exchanger")+": "+strings.Join(hosts, ", "),
		refSpoofing)
}
