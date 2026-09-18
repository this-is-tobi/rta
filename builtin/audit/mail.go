package audit

import (
	"context"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
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
	f := lookupMail(ctx, res, domain, selector)
	if verr := requireDomain(ctx, res, f); verr != nil {
		return nil, verr
	}
	r := gradeMail(f)

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
	// The authority ends at the first `/`, `?`, `#` or `\`; everything after
	// it is path or query and cannot name the host. The backslash is there
	// because browsers read it as a slash in every special scheme, so it is
	// where a reader of the string stops too.
	if i := strings.IndexAny(d, "/?#\\"); i >= 0 {
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
	return checkDomain(strings.ToLower(strings.Trim(strings.TrimSpace(d), ".")), raw)
}

// A domain and a DKIM selector are the two halves of one DNS name and have
// the same shape: dot-separated RFC 1035 labels, letters, digits and hyphens,
// none over 63 — "google", "selector1", "20161025", or a hierarchical one
// like "foo.bar" (RFC 6376 §3.6.2.1 allows the selector itself to be
// multi-label). One grammar for both halves, so they cannot drift apart
// again: the selector was held to it and the domain to "contains a dot",
// which is how an IP literal got graded as a mail domain and an
// internationalised name got reported as one that does not exist rather
// than one rta declined to ask about.
var labelSeqRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

// checkDomain is the domain half's grammar, applied to what mailDomain cut
// out of the argument. raw is the argument as typed, for the message: the
// value a person recognises is the one they handed over, not the piece
// that failed.
func checkDomain(d, raw string) (string, *view.Error) {
	bad := func(hint string) (string, *view.Error) {
		return "", view.Errorf("audit.mail.baddomain", "not a domain: %q", raw).WithHint(hint)
	}
	switch {
	case d == "" || !strings.Contains(d, "."):
		return bad("pass a domain like example.com, or an address at it")
	case stdnet.ParseIP(strings.Trim(d, "[]")) != nil:
		// Before the grammar, on purpose: 192.0.2.1 is four labels of
		// digits and passes it, and would then be graded as a mail domain
		// — "no SPF record, anyone can send as this domain" about an
		// address. A bracketed IPv6 literal fails the grammar on its own
		// and would get the character-set answer, which is the wrong one.
		return bad("an address is not a mail domain — pass the name it is published under")
	case len(d) > 253:
		return bad("a DNS name is at most 253 characters")
	case !labelSeqRe.MatchString(d):
		// No IDNA: golang.org/x/net/idna would pull x/text's tables into
		// the binary to improve one message, and the punycode form is what
		// the zone is published under anyway. A label over 63 characters
		// lands here too, which the hint states rather than re-stating as
		// a character-set problem.
		return bad("dot-separated labels of letters, digits and hyphens, each at most 63 — " +
			"for an internationalised domain, pass its xn-- form")
	}
	return d, nil
}

// checkSelector refuses a selector that is not a usable DNS label sequence
// before it is concatenated into dkimName. Same grammar as the domain half,
// its own code: the two halves arrive as two inputs and the refusal names
// the one that was wrong.
func checkSelector(v string) *view.Error {
	if v == "" {
		return nil
	}
	if len(v) > 253 || !labelSeqRe.MatchString(v) {
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

// settled reports whether the facts already answer "does this domain
// exist" — or show that the question cannot be answered from here. A
// successful answer at the apex or an MX settles it; so does a failed
// lookup of either, because a failure is not an absent name and the rows
// report it as what it is, the rule txt was written for. Only an empty,
// successful pair leaves the question open.
func (f mailFacts) settled() bool {
	return f.apexErr != nil || f.mxErr != nil || len(f.apexTXT) > 0 || len(f.mx) > 0
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
	// One entry is one TXT record. This used to join them, on the belief
	// that the resolver hands a long key back as several strings — it does
	// not: net.Resolver.LookupTXT concatenates the character-strings of one
	// record itself, so the join only ever glued distinct records together.
	// The case that broke was key rotation, an old revoked key beside a new
	// one: joined, the first p= was the good one and the selector graded
	// ok. RFC 6376 §3.6.2.2 makes the result undefined when a selector
	// holds more than one record, which SPF and DMARC already grade and
	// DKIM alone hid. Warn rather than fail: undefined is not broken, and a
	// rotation passes through this state on purpose.
	if len(records) > 1 {
		r.Add(grpSenderAuth, "dkim", findings.Warn,
			findings.Plural(len(records), "TXT record")+" at "+name+" — RFC 6376 makes the result "+
				"undefined when a selector holds more than one, so which key a verifier reads is not "+
				"something this can predict", refSpoofing)
		return
	}
	record := records[0]
	if !strings.Contains(strings.ToLower(record), "v=dkim1") && !strings.Contains(record, "p=") {
		r.Add(grpSenderAuth, "dkim", findings.Warn,
			"a TXT record exists at "+name+" but does not look like a DKIM key", refSpoofing)
		return
	}
	// An empty p= is the documented way to revoke a key (RFC 6376 §3.6.1),
	// so a record can be present and still mean "this key is dead". A
	// record with no p= at all is merely incomplete, and citing the
	// revocation mechanism about it would be a specific claim applied to
	// the wrong condition.
	p, present := recordTag(record, "p")
	switch {
	case !present:
		r.Add(grpSenderAuth, "dkim", findings.Fail,
			"the record at "+name+" has no p= tag, which makes it unusable — there is no key in it to verify with",
			refSpoofing)
		return
	case p == "":
		r.Add(grpSenderAuth, "dkim", findings.Fail,
			"the key at "+name+" has an empty p= tag, which revokes it — signatures made with it will not verify",
			refSpoofing)
		return
	}
	// A key in testing mode authenticates nothing: RFC 6376 §3.6.1 has
	// verifiers treat a testing signer's mail no differently from unsigned
	// mail, even when the signature fails. It is DKIM's p=none, and it is
	// checked before the key itself because no key size rescues it.
	if flags, _ := recordTag(record, "t"); dkimFlag(flags, "y") {
		r.Add(grpSenderAuth, "dkim", findings.Fail,
			"the key at "+name+" carries t=y, the testing flag — verifiers treat mail signed with it "+
				"exactly as unsigned, even when the signature fails, so it protects nothing until the "+
				"flag is dropped", refSpoofing)
		return
	}
	bits, alg, ok := dkimKeyBits(record)
	switch {
	case alg != "rsa" && alg != "ed25519":
		r.Add(grpSenderAuth, "dkim", findings.Warn,
			"the key at "+name+" declares k="+alg+", which is not a key type this reads (rsa and "+
				"ed25519 are) — a verifier that does not know it either treats the mail as unsigned",
			refSpoofing)
	case !ok:
		r.Add(grpSenderAuth, "dkim", findings.Warn,
			"the p= at "+name+" does not decode as an "+alg+" key, so a verifier cannot use it",
			refSpoofing)
	case alg == "rsa" && bits < 1024:
		r.Add(grpSenderAuth, "dkim", findings.Fail,
			strconv.Itoa(bits)+"-bit RSA key at "+name+" — RFC 8301 has verifiers refuse signatures "+
				"under 1024 bits, and a key this short can be factored: it is published, reads as "+
				"correct, and can be forged", refSpoofing)
	case alg == "rsa" && bits < 2048:
		r.Add(grpSenderAuth, "dkim", findings.Warn,
			strconv.Itoa(bits)+"-bit RSA key at "+name+" — RFC 8301's floor, below the 2048 it has "+
				"signers use", refSpoofing)
	default:
		r.Add(grpSenderAuth, "dkim", findings.OK,
			strconv.Itoa(bits)+"-bit "+alg+" key published at "+name, refSpoofing)
	}
}

// dkimFlag reports whether a t= tag carries one flag: a colon-separated
// list per RFC 6376 §3.6.1, "y" alone or "y:s".
func dkimFlag(flags, want string) bool {
	for _, f := range strings.Split(flags, ":") {
		if strings.EqualFold(strings.TrimSpace(f), want) {
			return true
		}
	}
	return false
}

// dkimKeyBits reads the key out of p= and reports its size, because
// presence is not protection: RFC 8301 §3.2 is the bar, and a 512-bit key
// is a record that is published, reads as correct, and can be forged — the
// state this whole capability exists to name.
//
// Two encodings, because DKIM has two. RFC 6376 §3.6.1's RSA p= is a base64
// SubjectPublicKeyInfo, so crypto/x509 reads it — the same parse a
// certificate's key gets, already linked by audit.web. RFC 8463 §4.2's
// Ed25519 p= is the raw 32-octet key with no ASN.1 around it, so
// ParsePKIXPublicKey refuses every valid one, and parsing it as SPKI would
// grade the newest correct thing a signer can publish as a record that does
// not decode. Whitespace is stripped first: a long key is published across
// character-strings and operators hand-wrap it. alg comes back even when
// the key does not, so the finding can name what it tried to read it as.
func dkimKeyBits(record string) (bits int, alg string, ok bool) {
	alg = "rsa" // RFC 6376 §3.6.1: k= is optional and defaults to rsa
	if k, present := recordTag(record, "k"); present && k != "" {
		alg = strings.ToLower(k)
	}
	p, _ := recordTag(record, "p")
	raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(p), ""))
	if err != nil {
		return 0, alg, false
	}
	switch alg {
	case "ed25519":
		return 256, alg, len(raw) == ed25519.PublicKeySize
	case "rsa":
		key, err := x509.ParsePKIXPublicKey(raw)
		if err != nil {
			return 0, alg, false
		}
		rsaKey, isRSA := key.(*rsa.PublicKey)
		if !isRSA {
			return 0, alg, false
		}
		return rsaKey.N.BitLen(), alg, true
	}
	return 0, alg, false
}

// recordTag reads one tag out of a `tag=value;` list — DKIM, DMARC and
// TLS-RPT records share the grammar. present tells an absent tag from an
// empty one, which for p= is the difference between an incomplete record
// and a revoked key.
func recordTag(record, tag string) (value string, present bool) {
	for _, part := range strings.Split(record, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), tag) {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// dmarcPct is the percentage of mail a DMARC policy is applied to, and
// whether the tag is usable at all.
//
// Absent means 100 per RFC 7489 §6.3, which is the common case and the one
// that must not read as zero. A value that is not an integer in 0-100 is a
// syntax error, and the RFC has a receiver discard a record it cannot parse
// rather than apply a default — so "usable" is a distinct answer from "100".
// An empty pct= is the syntax error, not the default: the grammar wants one
// to three digits, and reading `pct=` as 100 handed a record the RFC
// discards the best grade it can get.
func dmarcPct(record string) (int, bool) {
	raw, present := recordTag(record, "pct")
	if !present {
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
		// Kept at fail, with the inheritance named rather than guessed at.
		// RFC 7489 §6.6.3 sends a receiver that finds nothing at
		// _dmarc.<subdomain> to the organizational domain's record, where
		// sp= (or p=, when there is no sp=) decides what applies here — so
		// this row has to say so, or a correctly protected subdomain of a
		// p=reject domain reads as unprotected. What it must not do is
		// soften itself on a label count: "more than two labels" reads
		// example.co.uk, corp.com.au and x.com.br as subdomains of
		// something, and they are organizational domains with nothing to
		// inherit. Telling those they might be covered is the false
		// all-clear this file exists to remove, and telling the two cases
		// apart needs the Public Suffix List — a table too large and too
		// perishable to carry in this binary for one row. Nor is the parent
		// queried: walking up sends queries for names outside the domain
		// the grant named.
		r.Add(grpSenderAuth, "dmarc", findings.Fail,
			"no DMARC record at "+name+" — receivers have no instruction for mail that fails SPF and DKIM, "+
				"and SPF alone does not cover the address a reader actually sees; if this is a subdomain, "+
				"its organizational domain's sp= (or p=) applies instead, a lookup this did not make",
			refSpoofing)
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
		pctRaw, _ := recordTag(record, "pct")
		r.Add(grpSenderAuth, "dmarc", findings.Fail,
			"pct="+pctRaw+" is not a percentage, and RFC 7489 has receivers discard a "+
				"record they cannot parse — so this domain has no policy at all: "+findings.Clip(record),
			refSpoofing)
		return
	}

	policy, _ := recordTag(record, "p")
	policy = strings.ToLower(policy)
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
	if rua, _ := recordTag(record, "rua"); rua == "" {
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
		// DANE (RFC 7672) is the other standard answer to the same problem,
		// and Go's resolver has no way to ask for a TLSA record, so its
		// absence is named as unchecked rather than asserted.
		r.Add(grpMailTLS, "mta-sts", findings.Warn,
			"no MTA-STS policy — unless the domain publishes DANE TLSA records, which this cannot "+
				"query, a sending server has no instruction to require TLS, so an attacker on the path "+
				"can strip it and the mail is delivered in the clear", refCleartext)
	default:
		// The TXT record is a marker, not the policy. RFC 8461 puts the mode
		// (enforce, testing, or none — which is how MTA-STS is switched off),
		// the mx patterns and max_age in a file at
		// https://mta-sts.<domain>/.well-known/mta-sts.txt, which this does
		// not fetch: that would be the first body this capability reads from
		// a caller-influenced host, the line audit.web draws against
		// http.get, and it is a decision of its own. So "ok, senders are
		// told to require TLS" was an assertion about a document nobody
		// read, and it read as ok for a domain whose policy says mode:
		// none. Info is what a marker earns.
		r.Add(grpMailTLS, "mta-sts", findings.Info,
			"a policy is advertised at _mta-sts."+f.domain+" — its mode (enforce, testing or none) "+
				"is in the policy file, which this does not fetch", refCleartext)
	}

	rpt := pick(f.rpt, "v=tlsrptv1")
	switch {
	case f.rptErr != nil:
		r.Add(grpMailTLS, "tls-rpt", findings.Info, "lookup failed: "+f.rptErr.Error(), refCleartext)
	case len(rpt) == 0:
		r.Add(grpMailTLS, "tls-rpt", findings.Info,
			"no TLS-RPT record — failed TLS deliveries to this domain are not reported to anybody",
			refCleartext)
	default:
		// RFC 8460 §3 makes rua= required: a record without it names nowhere
		// to send a report, so it is the marker of a policy with no effect —
		// the same check dmarc-reporting makes, for the same reason.
		if rua, _ := recordTag(rpt[0], "rua"); rua == "" {
			r.Add(grpMailTLS, "tls-rpt", findings.Warn,
				"a TLS-RPT record with no rua= address — it names nowhere to send a report, so failed "+
					"TLS deliveries are reported to nobody", refCleartext)
			break
		}
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
//
// Decided from the facts the audit already gathered, with one address
// lookup only when they leave it open. It used to run first — an address,
// then MX, then the apex TXT — and lookupMail then asked for the apex TXT
// and the MX again: up to nine queries where six suffice, against a third
// party's authoritative servers, for a capability whose Description
// promises a handful of lookups. This optimises the ordinary path: a real
// domain now costs its six queries and no more. A typo pays six empty
// answers before its refusal where it used to pay three, and a typo is the
// rare case.
func requireDomain(ctx context.Context, res *stdnet.Resolver, f mailFacts) *view.Error {
	if f.settled() {
		return nil
	}
	_, err := res.LookupHost(ctx, f.domain)
	switch {
	case err == nil:
		return nil
	case !notFound(err):
		return view.Errorf("audit.mail.resolver", "resolving %q: %v", f.domain, err).
			WithHint("the lookup failed rather than coming back empty — check your resolver, or --timeout")
	}
	return view.Errorf("audit.mail.nxdomain", "%q does not exist in DNS", f.domain).
		WithHint("check the spelling — every check would otherwise report its record as missing")
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
