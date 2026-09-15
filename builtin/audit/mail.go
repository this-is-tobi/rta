package audit

import (
	"context"
	"errors"
	stdnet "net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/pkg/findings"
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
	grpSenderAuth = findings.Group{ID: "sender-auth", Title: "sender authentication"}
	grpMailTLS    = findings.Group{ID: "transport", Title: "transport security"}
	grpRouting    = findings.Group{ID: "routing", Title: "routing"}
)

var mailGroupOrder = []findings.Group{grpSenderAuth, grpMailTLS, grpRouting}

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
	selector := strings.TrimSpace(req.String("selector"))
	if verr := checkSelector(selector); verr != nil {
		return nil, verr
	}
	timeout := time.Duration(req.Int("timeout")) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	res := &stdnet.Resolver{}
	if err := requireDomain(ctx, res, domain); err != nil {
		return nil, err
	}
	r := gradeMail(lookupMail(ctx, res, domain, selector))

	if req.Bool("detail") {
		summary := append([]view.Pair{{Key: "domain", Value: domain}}, r.Grade()...)
		summary = append(summary, mailDeeper(domain)...)
		return r.Page(ctx, req, mailGroupOrder, view.KeyValue{Pairs: summary}), nil
	}
	return r.Table(true), nil
}

// mailDomain accepts what people have to hand: a domain, an address they were
// looking at, or a URL they copied from the browser.
//
// The order of the cuts is the whole correctness of this function, and it
// used to be wrong in a way that mattered beyond this file.
//
// `@` was taken first, with LastIndex over the entire argument, before the
// path was cut — so `http://good.com/x@evil.internal` audited evil.internal.
// That is not a parsing curiosity: the grant gate, the consent prompt and the
// ledger row all name the caller's argument verbatim (internal/mcp's
// ReserveNaming runs on the undecoded values, and the same values reach
// plugin.Resolve afterwards), while the lookups went to whatever came out
// here. The boundary recorded one destination and queried another, and
// because a scope ending in "/" covers any prefix under it, a grant for
// `example.com/` covered `example.com/x@evil.internal` and so authorised an
// audit of evil.internal.
//
// So: scheme, then authority, then userinfo strictly inside that authority,
// then port — URL order, which is the order a reader of the string applies
// too. audit.web does not have this bug because url.Parse ends the authority
// at the first `/` for it.
func mailDomain(raw string) (string, *view.Error) {
	d := strings.TrimSpace(raw)
	scheme := false
	if i := strings.Index(d, "://"); i >= 0 {
		d, scheme = d[i+3:], true
	}
	// The authority ends at the first `/`, `?` or `#`; everything after it is
	// path or query and cannot name the host.
	if i := strings.IndexAny(d, "/?#"); i >= 0 {
		d = d[:i]
	}
	// Only now can an `@` be userinfo. A credential in front of the host is
	// refused rather than trimmed: `https://example.com@evil.internal` reads
	// as example.com to whoever approves it, and a value that has to be read
	// twice to find its host has no business being the thing a grant names.
	if i := strings.LastIndex(d, "@"); i >= 0 {
		if scheme {
			return "", view.Errorf("audit.mail.baddomain",
				"%q carries credentials before the host, so the domain it audits is not the one it reads as", raw).
				WithHint("pass the domain itself — the part after the @ — or an address at it")
		}
		d = d[i+1:]
	}
	// A bracketed IPv6 literal keeps its colons; anything else is host:port.
	if !strings.HasPrefix(d, "[") {
		if i := strings.Index(d, ":"); i >= 0 {
			d = d[:i]
		}
	}
	d = strings.Trim(strings.TrimSpace(d), ".")
	if d == "" || !strings.Contains(d, ".") {
		return "", view.Errorf("audit.mail.baddomain", "not a domain: %q", raw).
			WithHint("pass a domain like example.com, or an address at it")
	}
	return strings.ToLower(d), nil
}

// selectorRe is a DKIM selector's shape: one or more RFC 1035 labels,
// dot-separated — "google", "selector1", "20161025", or a hierarchical one
// like "foo.bar" (RFC 6376 §3.6.2.1 allows the selector itself to be
// multi-label). Letters, digits and hyphens only, the same character set
// mailDomain already reduces the domain half of this same name to.
var selectorRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

// checkSelector refuses a selector that is not a usable DNS label sequence
// before it is concatenated into dkimName — "only trimmed" before this,
// unlike domain, which mailDomain already holds to its own character set.
func checkSelector(v string) *view.Error {
	if v == "" {
		return nil
	}
	if len(v) > 253 || !selectorRe.MatchString(v) {
		return view.Errorf("audit.mail.badselector", "%q is not a usable DKIM selector", v).
			WithHint("selectors are letters, digits, hyphens and dots — the s= tag of a DKIM-Signature header")
	}
	return nil
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
func gradeMail(f mailFacts) *findings.Report {
	r := &findings.Report{}
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

func auditSPF(r *findings.Report, f mailFacts) {
	if f.apexErr != nil {
		r.Add(grpSenderAuth, "spf", findings.Info, "lookup failed: "+f.apexErr.Error(), refSpoofing)
		return
	}
	spf := pick(f.apexTXT, "v=spf1")
	switch {
	case len(spf) == 0:
		r.Add(grpSenderAuth, "spf", findings.Fail,
			"no SPF record — any host on the internet can send mail claiming to be this domain",
			refSpoofing)
		return
	case len(spf) > 1:
		// RFC 7208 §4.5: more than one is a permerror, and a permerror means
		// receivers apply no policy at all. Two "correct" records are worse
		// than one, and worse than none, because they look like protection.
		r.Add(grpSenderAuth, "spf", findings.Fail,
			findings.Plural(len(spf), "SPF record")+" published — RFC 7208 makes this a permanent error, "+
				"so receivers apply no SPF policy at all", refSpoofing)
		return
	}

	record := spf[0]
	status, detail := gradeSPFAll(record)
	r.Add(grpSenderAuth, "spf", status, detail, refSpoofing)

	// The lookup cap is counted over the mechanisms this record states
	// directly. Anything reached through an include: adds its own, which is
	// why the finding says "at least" rather than pretending to a total it
	// cannot see without walking the tree — and walking it would make this a
	// crawler, which the plugin does not do.
	if n := spfLookups(record); n > spfLookupLimit {
		r.Add(grpSenderAuth, "spf-lookups", findings.Fail,
			"at least "+strconv.Itoa(n)+" DNS-querying mechanisms, over RFC 7208's limit of "+
				strconv.Itoa(spfLookupLimit)+" — evaluation returns permerror and the policy is not applied",
			refSpoofing)
	} else if n > spfLookupLimit-3 {
		r.Add(grpSenderAuth, "spf-lookups", findings.Warn,
			"at least "+strconv.Itoa(n)+" of RFC 7208's "+strconv.Itoa(spfLookupLimit)+
				" DNS-querying mechanisms used directly; each include: adds its own",
			refSpoofing)
	}
}

// spfAll is the qualifier of the first all mechanism a receiver reaches, and
// whether there is one at all.
//
// The first one is the only one that matters, and grading by membership got
// this wrong in the direction that hides the worst record there is: SPF
// evaluates mechanisms left to right and all matches every host, so
// `v=spf1 all -all` is decided by the bare all — which carries the default +
// qualifier per RFC 7208 §4.6.2 and authorises the entire internet. Asking
// whether "-all" appeared anywhere graded exactly that record ok, "ends in
// -all (hard fail)", and appending -all to a record that already ended in
// all is an ordinary editing mistake rather than an exotic one.
func spfAll(record string) (string, bool) {
	for _, f := range strings.Fields(strings.ToLower(record)) {
		qualifier := "+"
		if len(f) > 0 && strings.ContainsRune("+-~?", rune(f[0])) {
			qualifier, f = f[:1], f[1:]
		}
		if f == "all" {
			return qualifier, true
		}
	}
	return "", false
}

// gradeSPFAll grades the record's final disposition — the part that decides
// what a receiver does with mail from an unlisted host. Everything before it
// only says who is allowed.
func gradeSPFAll(record string) (string, string) {
	qualifier, found := spfAll(record)
	if !found {
		if strings.Contains(strings.ToLower(record), "redirect=") {
			return findings.Info, "delegates its policy with redirect=: " + findings.Clip(record)
		}
		return findings.Warn, "no all mechanism — unlisted senders get no verdict, which receivers treat as neutral: " +
			findings.Clip(record)
	}
	switch qualifier {
	case "+":
		return findings.Fail, "the first all mechanism a receiver reaches is +all — this authorises the " +
			"entire internet to send as the domain, which is worse than publishing nothing: " + findings.Clip(record)
	case "-":
		return findings.OK, "ends in -all (hard fail): " + findings.Clip(record)
	case "~":
		return findings.OK, "ends in ~all (soft fail) — fine alongside an enforcing DMARC policy: " + findings.Clip(record)
	}
	return findings.Warn, "ends in ?all (neutral), which asks receivers to treat unlisted senders " +
		"exactly as if no SPF record existed: " + findings.Clip(record)
}

// spfLookups counts the mechanisms that cost a DNS query, per RFC 7208 §4.6.4.
//
// It stops at the first all for the reason gradeSPFAll grades on it: nothing
// past it is ever evaluated, so nothing past it can cost a lookup. The cut at
// "/" is for the CIDR-only forms the grammar allows — a/24, mx/24, a//64 —
// which used to leave the name as "a/24" and miss the table, undercounting in
// the direction that suppresses the finding: past ten querying mechanisms
// evaluation is a permerror and no policy is applied at all.
func spfLookups(record string) int {
	n := 0
	for _, f := range strings.Fields(strings.ToLower(record)) {
		f = strings.TrimLeft(f, "+-~?")
		if f == "all" {
			break
		}
		name, _, _ := strings.Cut(f, ":")
		name, _, _ = strings.Cut(name, "=")
		name, _, _ = strings.Cut(name, "/")
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
func auditDKIM(r *findings.Report, f mailFacts) {
	if f.selector == "" {
		r.Add(grpSenderAuth, "dkim", findings.Info,
			"not checked — DKIM selectors cannot be discovered from the domain; "+
				"pass --selector, taking the s= tag from a DKIM-Signature header on a message you received",
			refSpoofing)
		return
	}
	name := f.dkimName
	if f.dkimErr != nil {
		r.Add(grpSenderAuth, "dkim", findings.Info, "lookup of "+name+" failed: "+f.dkimErr.Error(), refSpoofing)
		return
	}
	records := f.dkim
	if len(records) == 0 {
		r.Add(grpSenderAuth, "dkim", findings.Fail,
			"no DKIM key at "+name+" — messages signed with this selector cannot be verified", refSpoofing)
		return
	}
	// A DKIM record may be split across strings; the resolver hands them back
	// separately and they are meant to be concatenated.
	joined := strings.Join(records, "")
	if !strings.Contains(strings.ToLower(joined), "v=dkim1") && !strings.Contains(joined, "p=") {
		r.Add(grpSenderAuth, "dkim", findings.Warn,
			"a TXT record exists at "+name+" but does not look like a DKIM key", refSpoofing)
		return
	}
	// An empty p= is the documented way to revoke a key (RFC 6376 §3.6.1),
	// so a record can be present and still mean "this key is dead".
	if p := dkimTag(joined, "p"); p == "" {
		r.Add(grpSenderAuth, "dkim", findings.Fail,
			"the key at "+name+" has an empty p= tag, which revokes it — signatures made with it will not verify",
			refSpoofing)
		return
	}
	r.Add(grpSenderAuth, "dkim", findings.OK, "public key published at "+name, refSpoofing)
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

// dmarcPct is the percentage of mail a DMARC policy is applied to, and
// whether the tag is usable at all.
//
// Absent means 100 per RFC 7489 §6.3, which is the common case and the one
// that must not read as zero. A value that is not an integer in 0-100 is a
// syntax error, and the RFC has a receiver discard a record it cannot parse
// rather than apply a default — so "usable" is a distinct answer from "100".
func dmarcPct(record string) (int, bool) {
	raw := dkimTag(record, "pct")
	if raw == "" {
		return 100, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > 100 {
		return 0, false
	}
	return n, true
}

func auditDMARC(r *findings.Report, f mailFacts) {
	name := "_dmarc." + f.domain
	if f.dmarcErr != nil {
		r.Add(grpSenderAuth, "dmarc", findings.Info, "lookup of "+name+" failed: "+f.dmarcErr.Error(), refSpoofing)
		return
	}
	dmarc := pick(f.dmarc, "v=dmarc1")
	if len(dmarc) == 0 {
		r.Add(grpSenderAuth, "dmarc", findings.Fail,
			"no DMARC record at "+name+" — receivers have no instruction for mail that fails SPF and DKIM, "+
				"and SPF alone does not cover the address a reader actually sees", refSpoofing)
		return
	}
	if len(dmarc) > 1 {
		r.Add(grpSenderAuth, "dmarc", findings.Fail,
			findings.Plural(len(dmarc), "DMARC record")+" at "+name+" — RFC 7489 requires receivers to ignore "+
				"the domain's policy entirely when more than one is published", refSpoofing)
		return
	}

	record := dmarc[0]

	// pct= decides how much of the domain's mail the policy is applied to at
	// all, so it belongs in the policy verdict rather than only beside it. It
	// used to be compared as a string and never folded in, which graded
	// `p=reject; pct=0` ok, "failing mail is refused" — for a domain whose
	// policy is applied to none of its mail. That is p=none by another
	// spelling, and p=none is graded fail a few lines down, so the record
	// with the misleading tag scored better than the honest one. An abandoned
	// staged rollout leaves domains in exactly that state.
	pct, pctOK := dmarcPct(record)
	if !pctOK {
		r.Add(grpSenderAuth, "dmarc", findings.Fail,
			"pct="+dkimTag(record, "pct")+" is not a percentage, and RFC 7489 has receivers discard a "+
				"record they cannot parse — so this domain has no policy at all: "+findings.Clip(record),
			refSpoofing)
		return
	}

	policy := strings.ToLower(dkimTag(record, "p"))
	if pct == 0 && (policy == "reject" || policy == "quarantine") {
		r.Add(grpSenderAuth, "dmarc", findings.Fail,
			"p="+policy+" with pct=0 — the policy is applied to none of the domain's mail, which is "+
				"p=none written the long way: "+findings.Clip(record), refSpoofing)
		return
	}

	switch policy {
	case "reject":
		if pct < 100 {
			r.Add(grpSenderAuth, "dmarc", findings.Warn,
				"p=reject, but pct="+strconv.Itoa(pct)+" applies it to a sample: "+findings.Clip(record), refSpoofing)
			break
		}
		r.Add(grpSenderAuth, "dmarc", findings.OK, "p=reject — failing mail is refused: "+findings.Clip(record), refSpoofing)
	case "quarantine":
		r.Add(grpSenderAuth, "dmarc", findings.Warn,
			"p=quarantine — failing mail is delivered to spam rather than refused: "+findings.Clip(record), refSpoofing)
	case "none":
		r.Add(grpSenderAuth, "dmarc", findings.Fail,
			"p=none — monitoring only, so spoofed mail is still delivered to the inbox: "+findings.Clip(record),
			refSpoofing)
	default:
		r.Add(grpSenderAuth, "dmarc", findings.Fail,
			"no usable p= tag, which makes the record invalid: "+findings.Clip(record), refSpoofing)
	}

	// pct= applies the policy to a sample. It exists for staged rollouts, and
	// a rollout left half-finished is the common way a domain ends up
	// believing it is protected while most spoofed mail still lands.
	if pct < 100 {
		r.Add(grpSenderAuth, "dmarc-coverage", findings.Warn,
			"pct="+strconv.Itoa(pct)+" — the policy is applied to that percentage of mail; the rest is delivered as if "+
				"there were no policy", refSpoofing)
	}
	if dkimTag(record, "rua") == "" {
		r.Add(grpSenderAuth, "dmarc-reporting", findings.Warn,
			"no rua= address — nothing reports back, so a policy that is breaking legitimate mail "+
				"or failing to stop spoofing looks identical to one that is working", refSpoofing)
	}
}

// MTA-STS and TLS-RPT are about the hop between mail servers. Without them,
// SMTP's opportunistic TLS can be stripped by anything on the path, and the
// sending server has no way to know it was supposed to insist.
func auditMailTransport(r *findings.Report, f mailFacts) {
	switch {
	case f.stsErr != nil:
		r.Add(grpMailTLS, "mta-sts", findings.Info, "lookup failed: "+f.stsErr.Error(), refCleartext)
	case len(pick(f.sts, "v=stsv1")) == 0:
		r.Add(grpMailTLS, "mta-sts", findings.Warn,
			"no MTA-STS policy — a sending server has no instruction to require TLS, so an attacker on "+
				"the path can strip it and the mail is delivered in the clear", refCleartext)
	default:
		r.Add(grpMailTLS, "mta-sts", findings.OK, "policy published — senders are told to require TLS", refCleartext)
	}

	switch {
	case f.rptErr != nil:
		r.Add(grpMailTLS, "tls-rpt", findings.Info, "lookup failed: "+f.rptErr.Error(), refCleartext)
	case len(pick(f.rpt, "v=tlsrptv1")) == 0:
		r.Add(grpMailTLS, "tls-rpt", findings.Info,
			"no TLS-RPT record — failed TLS deliveries to this domain are not reported to anybody",
			refCleartext)
	default:
		r.Add(grpMailTLS, "tls-rpt", findings.OK, "senders report TLS delivery failures", refCleartext)
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

func auditMailRouting(r *findings.Report, f mailFacts) {
	mx := f.mx
	// A lookup that failed is not an answer about the domain. lookupMail
	// already nils mxErr for the not-found case, so a non-nil one here is a
	// SERVFAIL, a refused query, or the deadline expiring partway through the
	// six-to-nine sequential lookups — and reporting it as "this domain does
	// not receive mail" is the confident lie mailFacts' own doc warns about.
	// Every other lookup in this file already says so; mx was the one left.
	if f.mxErr != nil {
		r.Add(grpRouting, "mx", findings.Info, "lookup failed: "+f.mxErr.Error(), refSpoofing)
		return
	}
	if len(mx) == 0 {
		r.Add(grpRouting, "mx", findings.Info, "no MX records — this domain does not receive mail", refSpoofing)
		return
	}
	// RFC 7505: a single "." host is the explicit statement that a domain
	// accepts no mail. It is a hardening measure, not an omission, and
	// grading it as one would train people to undo it.
	if len(mx) == 1 && strings.TrimSuffix(mx[0].Host, ".") == "" {
		r.Add(grpRouting, "mx", findings.OK,
			"null MX (RFC 7505) — the domain states explicitly that it accepts no mail", refSpoofing)
		return
	}
	hosts := make([]string, 0, len(mx))
	for _, m := range mx {
		hosts = append(hosts, strings.TrimSuffix(m.Host, "."))
	}
	r.Add(grpRouting, "mx", findings.Info, findings.Plural(len(hosts), "mail exchanger")+": "+strings.Join(hosts, ", "),
		refSpoofing)
}
