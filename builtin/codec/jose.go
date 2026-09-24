package codec

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/builtin/internal/pipein"
	"github.com/this-is-tobi/rta/builtin/internal/timefmt"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The JOSE family, read for inspection.
//
// A JWT is a claims set carried in one of two envelopes — signed (JWS, RFC
// 7515) or encrypted (JWE, RFC 7516) — and each envelope has a compact form
// of dot-separated parts and a JSON form of named members. codec.jwt used to
// read one of those four, the signed compact one, and told a person holding an
// encrypted ID token that it was "not a JWT", which RFC 7519 §3 says it is.
// Keycloak issues one the moment a client asks for ID token encryption.
//
// What this reads, and deliberately does not do:
//
//   - A JWS, compact or JSON (flattened or general, RFC 7515 §7.2): every
//     header, the claims, or the payload when it is not a claims set — a JWS
//     may carry anything, a detached one (Appendix F) carries nothing, and an
//     unencoded one (RFC 7797) carries its payload as it is.
//   - A JWE, compact or JSON: its headers and the size of every part. Not its
//     content. Decrypting takes the recipient's key — a private key, a shared
//     key or a password, depending on alg (keyHeld) — and a decoder is not
//     the place to hand one to.
//   - A token nested in another (cty "JWT", RFC 7519 §5.2), when the outer one
//     is signed and so the inner one is there to read.
//
// It checks nothing unless handed a key (verify.go), and says which in every
// result. What it always does is name what a strict parser would refuse — a
// padded or standard-alphabet segment, a member named twice, an empty
// signature — because that is what somebody debugging a rejected token came
// to find out.

// maxPipedToken bounds what codec.jwt and codec.jwk read from a pipe. A token
// in an Authorization header is a few kilobytes and a key set a few dozen, so
// this is generous for either and still a bound.
const maxPipedToken = 1 << 20

// maxNesting bounds how deep codec.jwt follows a token whose payload is
// another token. Real nesting is one level — sign, then encrypt, and the
// encrypted one is opaque anyway — and the bound is what stops a hostile chain
// of wrappers from being followed as far as it likes.
const maxNesting = 3

func runJWT(_ context.Context, req plugin.Request) (view.View, error) {
	raw, verr := joseInput(req, "token", "codec.jwt", "token",
		"pass it as an argument, or pipe it: `pbpaste | rta codec jwt` keeps a live token out of your shell history")
	if verr != nil {
		return nil, verr
	}
	check, verr := verifierFrom(req)
	if verr != nil {
		return nil, verr
	}
	token := unwrapToken(raw)
	var v view.View
	if strings.HasPrefix(token, "{") {
		v, verr = decodeJSONSerialization(token, check)
	} else {
		v, verr = decodeCompact(token, 0, check)
	}
	// Through a variable rather than returned straight: a nil *view.Error
	// handed back as an error is an interface that is not nil, and every
	// token would have "failed" with no message.
	if verr != nil {
		return nil, verr
	}
	return v, nil
}

// verifierFrom builds the signature check a call asked for with --key or
// --secret-file, or returns nil when it asked for none — the ordinary case,
// where the page says nothing was verified.
func verifierFrom(req plugin.Request) (*verifier, *view.Error) {
	key, secretFile := strings.TrimSpace(req.String("key")), req.String("secret-file")
	if key == "" && secretFile == "" {
		return nil, nil
	}
	v := &verifier{surface: req.Surface()}
	if secretFile != "" {
		secret, verr := secretFrom(secretFile)
		if verr != nil {
			return nil, verr
		}
		v.secret = &secret
	}
	if key != "" {
		keys, notes, verr := keysFrom(key, req.Surface())
		if verr != nil {
			return nil, verr
		}
		v.keys, v.notes = keys, notes
	}
	return v, nil
}

// joseInput reads a JOSE input from the argument, or from a pipe when the
// argument is empty. A pipe is the channel worth offering for a token in
// particular: an argument lands in the shell's history and, for as long as the
// call runs, in a process table every user on the machine can read.
func joseInput(req plugin.Request, field, code, what, hint string) (string, *view.Error) {
	raw := req.String(field)
	if raw == "" {
		piped, err := pipein.Read(req, maxPipedToken)
		switch {
		case errors.Is(err, pipein.ErrTooLarge):
			return "", view.Errorf(code+".stdin", "stdin holds more than the %s a %s can be",
				format.Bytes(maxPipedToken), what)
		case err != nil:
			return "", view.Errorf(code+".stdin", "reading stdin: %v", err)
		}
		raw = piped
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		// The pipe is the CLI's alone, so only the CLI is told about it.
		switch req.Surface() {
		case plugin.SurfaceTUI:
			hint = "paste it into the " + field + " box"
		case plugin.SurfaceMCP:
			hint = "pass it as the " + field + " argument"
		}
		return "", view.Errorf(code+".empty", "no %s to read", what).WithHint(hint)
	}
	return raw, nil
}

// unwrapToken removes what surrounds a token copied from where it was found
// rather than on its own: the header line it was sent in (`Authorization:
// Bearer eyJ…`, or a DPoP proof's own `DPoP:` header), the scheme in front of
// it (RFC 6750's Bearer, RFC 9449's DPoP), and the quotes of the JSON or YAML
// value it was copied out of. The caller already holds the token, so being
// forgiving about its wrapping costs nothing — the argument codec.b64 makes
// about base64 dialects.
//
// The quotes come off at every layer, not only around the token: a YAML or
// JSON value is as often the whole `"Bearer eyJ…"` as the token alone, and
// with the quotes taken last the scheme inside them survived, to be joined
// onto the token as BearereyJ… and decoded into garbage.
func unwrapToken(s string) string {
	s = unquote(s)
	if name, rest, ok := strings.Cut(s, ":"); ok {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "authorization", "dpop":
			s = unquote(rest)
		}
	}
	for _, scheme := range []string{"bearer", "dpop"} {
		if len(s) > len(scheme) && strings.EqualFold(s[:len(scheme)], scheme) &&
			(s[len(scheme)] == ' ' || s[len(scheme)] == '\t') {
			s = unquote(s[len(scheme):])
			break
		}
	}
	return s
}

// unquote trims s and takes off one pair of matching quotes around it.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// compactForm drops the whitespace a base64url segment picks up when it is
// copied across a wrapped line. A segment has none of its own, so nothing
// removed here was part of it.
//
// Segment by segment, never the whole token: an unencoded payload (RFC 7797)
// is carried as it is, and §5.2 allows it a space. Run over the whole token,
// this showed "hello world" as "helloworld" and checked the signature over
// text the token does not hold, so a valid one read as tampered with.
func compactForm(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// decodeCompact reads a compact token. check, when set, verifies its
// signature; it is never handed to a nested token, whose signature the key
// given for the outer one says nothing about. A JWS's payload segment is
// left as it came, for decodeJWS, which alone knows whether it is encoded.
func decodeCompact(token string, depth int, check *verifier) (view.View, *view.Error) {
	parts := strings.Split(token, ".")
	for i := range parts {
		if len(parts) != 3 || i != 1 {
			parts[i] = compactForm(parts[i])
		}
	}
	switch len(parts) {
	case 3:
		return decodeJWS(parts, depth, check)
	case 5:
		if check != nil {
			return nil, encryptedNotSigned(check)
		}
		return decodeJWE(parts)
	}
	got := fmt.Sprintf("%d dot-separated parts", len(parts))
	if len(parts) == 1 {
		got = "no dots at all"
	}
	return nil, view.Errorf("codec.jwt.invalid", "not a JOSE token: it has %s", got).
		WithHint("a signed token (JWS) is header.payload.signature, and an encrypted one (JWE) has five parts")
}

// decodeSegment decodes one base64url part, forgiving the dialects a strict
// parser refuses and naming which one it met, so the page can say so. A token
// a library rejected for being padded is exactly the kind somebody pastes here
// to find out why.
//
// Strict decoding first, so a last character whose unused bits are not zero
// is named too (RFC 4648 §3.5). The lenient decoders read such a string as
// the same bytes as the canonical one, so a signature with its last letter
// changed still VERIFIED, silently, while a deny list or a replay cache keyed
// on the token's text counts the two as different tokens, and a strict
// library such as golang-jwt with WithStrictDecoding refuses one of them.
func decodeSegment(s string) ([]byte, string, error) {
	for _, d := range segmentDialects {
		if raw, err := d.strict.DecodeString(s); err == nil {
			return raw, d.name, nil
		}
	}
	for _, d := range segmentDialects {
		if raw, err := d.enc.DecodeString(s); err == nil {
			if d.name == "" {
				return raw, nonCanonical, nil
			}
			return raw, d.name + ", " + nonCanonical, nil
		}
	}
	_, err := base64.RawURLEncoding.DecodeString(s)
	return nil, "", err
}

// nonCanonical ends the dialect of a segment only a lenient decoder reads.
const nonCanonical = "non-canonical"

var segmentDialects = []struct {
	enc, strict *base64.Encoding
	name        string
}{
	{base64.RawURLEncoding, base64.RawURLEncoding.Strict(), ""},
	{base64.URLEncoding, base64.URLEncoding.Strict(), "padded"},
	{base64.RawStdEncoding, base64.RawStdEncoding.Strict(), "standard-alphabet"},
	{base64.StdEncoding, base64.StdEncoding.Strict(), "padded, standard-alphabet"},
}

// splitDialect separates the alphabet and padding a segment was written in
// from whether its last character is canonical.
func splitDialect(dialect string) (form string, loose bool) {
	form, loose = strings.CutSuffix(dialect, nonCanonical)
	return strings.TrimSuffix(form, ", "), loose
}

func decodeHeader(seg, what string) (object, string, *view.Error) {
	raw, dialect, err := decodeSegment(seg)
	if err != nil {
		return object{}, "", view.Errorf("codec.jwt.invalid", "decoding the %s: %v", what, err)
	}
	header, err := decodeObject(raw)
	if err != nil {
		return object{}, "", view.Errorf("codec.jwt.invalid", "decoding the %s: %v", what, err)
	}
	return header, dialect, nil
}

// page collects the sections a decode produces and what it noticed on the way,
// so the verification section at the end can say all of it in one place.
type page struct {
	sections []view.Section
	notes    []string
	escaped  []string
	// jsonForm is set for the JSON serialization, whose payload is never a
	// JWT's claims set however it is shaped.
	jsonForm bool
}

func (p *page) add(id, title string, v view.View) {
	p.sections = append(p.sections, view.Section{ID: id, Title: title, View: v})
}

func (p *page) note(format string, args ...any) {
	p.notes = append(p.notes, fmt.Sprintf(format, args...))
}

// render is keyValueOf for a page: what it had to escape, and what in the
// object two parsers can read differently, are recorded against the page.
func (p *page) render(o object, what, rfc string) view.KeyValue {
	r := keyValueOf(o)
	p.escaped = append(p.escaped, r.escaped...)
	p.ambiguous(o, what, rfc)
	return r.kv
}

// ambiguous notes what in a decoded object two parsers can read differently:
// a member given twice, and text encoding/json replaced rather than refused.
func (p *page) ambiguous(o object, what, rfc string) {
	if len(o.dupes) > 0 {
		names := make([]string, len(o.dupes))
		for i, d := range o.dupes {
			names[i] = visible(d)
		}
		p.note("The %s names %s more than once. %s lets a parser refuse that or keep the last value, "+
			"so two libraries can read this token differently; shown is the last.",
			what, strings.Join(names, ", "), rfc)
	}
	if o.replaced != "" {
		line := fmt.Sprintf("The %s %s. RFC 8259 §8 lets a parser refuse that, and a strict one does; shown here "+
			"is U+FFFD in its place, so two values that differ can read the same", what, o.replaced)
		if len(o.dupes) > 0 {
			line += ", and a name said to repeat may be two different names"
		}
		p.note("%s.", line)
	}
}

func (p *page) dialect(what, dialect string) {
	form, loose := splitDialect(dialect)
	if form != "" {
		p.note("The %s is %s base64. RFC 7515 §2 requires unpadded base64url, and a strict parser refuses anything else.",
			what, form)
	}
	if loose {
		p.note("The %s ends in a character whose unused bits are not zero, which a strict decoder refuses (RFC 4648 §3.5). "+
			"A lenient one reads the same bytes from it as from the canonical spelling, so to a deny list or a replay "+
			"cache keyed on the text this is a different token.", what)
	}
}

// finish adds the verification section — the leading verdict first, then
// everything noticed — and returns the page.
func (p *page) finish(lead ...string) view.View {
	p.add("verification", "verification", view.Text{Body: strings.Join(p.paragraphs(lead...), "\n\n")})
	return view.Sections{Items: p.sections}
}

// notesPage is finish for a page with no verdict to lead with: what was
// noticed goes under notes, and a page that noticed nothing has no such
// section at all.
func (p *page) notesPage() view.View {
	if paras := p.paragraphs(); len(paras) > 0 {
		p.add("notes", "notes", view.Text{Body: strings.Join(paras, "\n\n")})
	}
	return view.Sections{Items: p.sections}
}

func (p *page) paragraphs(lead ...string) []string {
	var paras []string
	for _, l := range lead {
		if l != "" {
			paras = append(paras, l)
		}
	}
	paras = append(paras, p.notes...)
	if len(p.escaped) > 0 {
		paras = append(paras, "Some values hold control or invisible characters, so they are shown quoted, "+
			"with those characters escaped: "+strings.Join(p.escaped, ", ")+".")
	}
	return paras
}

// notOverThisPayload rewords a signature that does not match an empty
// payload. A detached one (RFC 7515 Appendix F) was signed with a payload
// that is not in the token, so the check over the empty string fails however
// good the signature is, and the mismatch's hint — changed after it was
// signed, or signed with another key — sent somebody holding the issuer's
// right key off looking for tampering. Open Banking and FAPI message
// signatures travel detached, and they are what somebody checks with the
// issuer's key. certain is for the JSON form, which says it is detached by
// leaving the member out; a compact token's empty segment may be either.
func notOverThisPayload(verr *view.Error, certain bool) *view.Error {
	if verr.Code != "codec.jwt.signature" {
		return verr
	}
	hint := "a detached payload's signature can be checked only with the payload it was made over, which codec.jwt does not take"
	if certain {
		return view.Errorf("codec.jwt.detached", "the payload is detached (RFC 7515 Appendix F): the signature was made "+
			"over one that is not here, so it cannot be checked").WithHint(hint)
	}
	return view.Errorf("codec.jwt.detached", "%s, over an empty payload: the payload segment is empty, so either the "+
		"payload is detached (RFC 7515 Appendix F) and the signature was made over one that is not here, or the "+
		"signature is bad", verr.Message).WithHint(hint)
}

// encryptedNotSigned refuses a check of a token that carries no signature.
// The hint names what asked for the check: it said "without --key" whatever
// was given, and to somebody who had passed only --secret-file, dropping
// --key changes nothing.
func encryptedNotSigned(check *verifier) *view.Error {
	var given []string
	if len(check.keys) > 0 {
		given = append(given, "--key")
	}
	if check.secret != nil {
		given = append(given, "--secret-file")
	}
	return view.Errorf("codec.jwt.encrypted", "this token is encrypted, not signed: there is no signature here to verify").
		WithHint("without " + strings.Join(given, " and ") + " it shows the header, and its cty says whether a signed token is sealed inside")
}

func decodeJWS(parts []string, depth int, check *verifier) (view.View, *view.Error) {
	header, dialect, verr := decodeHeader(parts[0], "header")
	if verr != nil {
		return nil, verr
	}
	if !unencoded(header) {
		parts[1] = compactForm(parts[1])
	}
	lead := verdict(header, parts[2])
	if check != nil {
		// The signing input is the two segments exactly as the token carries
		// them (RFC 7515 §5.2), never re-encoded: a padded header was signed
		// padded.
		if lead, verr = check.check(header, parts[0]+"."+parts[1], parts[2]); verr != nil {
			if parts[1] == "" {
				return nil, notOverThisPayload(verr, false)
			}
			return nil, verr
		}
	}
	p := &page{}
	if check != nil {
		p.notes = append(p.notes, check.notes...)
	}
	p.dialect("header", dialect)
	p.add("header", "header", p.render(header, "header", "RFC 7515 §4"))
	p.critical(header, object{}, "")
	claims, verr := p.payload(header, parts[1], depth)
	if verr != nil {
		return nil, verr
	}
	p.signatureSegment(parts[2])
	p.embeddedKey(header)
	return p.finish(lead, window(claims)), nil
}

// payload adds the section a JWS payload calls for and returns its claims,
// when it is a claims set at all.
func (p *page) payload(header object, seg string, depth int) (object, *view.Error) {
	if seg == "" {
		p.add("payload", "payload", view.Text{Body: p.emptyPayload(header)})
		return object{}, nil
	}
	var raw []byte
	b64, set := header.values["b64"].(bool)
	switch {
	case unencoded(header):
		raw = []byte(seg)
		p.note("Its header sets b64 to false (RFC 7797), so the payload is carried as it is rather than encoded.")
	// RFC 7797 §6 requires b64 in crit because a parser that does not know
	// the extension ignores it: without crit, the one segment reads as two
	// payloads under the same valid signature, and this page showed the
	// literal one — "not a JWT" — while such a library read {"sub":"admin"}.
	case set && !b64:
		var dialect string
		var err error
		if raw, dialect, err = decodeSegment(seg); err != nil {
			raw = []byte(seg)
			p.note("Its header sets b64 to false without listing b64 in crit, which RFC 7797 §6 requires, and the " +
				"payload is not base64url: a parser that honours b64 takes the segment as the payload, as shown, " +
				"and one that ignores it cannot read the token.")
			break
		}
		p.dialect("payload", dialect)
		p.note("Its header sets b64 to false without listing b64 in crit, which RFC 7797 §6 requires, so parsers " +
			"read it two ways: one that ignores b64 decodes the payload from base64url, as shown, and one that " +
			"honours it takes the segment itself as the payload.")
	default:
		var dialect string
		var err error
		if raw, dialect, err = decodeSegment(seg); err != nil {
			return object{}, view.Errorf("codec.jwt.invalid", "decoding the payload: %v", err)
		}
		p.dialect("payload", dialect)
	}
	if claims, err := decodeObject(raw); err == nil {
		// A JWT is carried in the compact form only (RFC 7519 §1), so the
		// object a JSON serialization carries — an ACME request's, say — is
		// a payload that happens to be JSON rather than a claims set.
		if p.jsonForm {
			p.add("payload", "payload", p.render(claims, "payload", "RFC 7515 §7.2"))
		} else {
			p.add("claims", "claims", p.render(claims, "claims set", "RFC 7519 §4"))
		}
		return claims, nil
	}
	nested := ctyIsJWT(header.str("cty"))
	if nested && depth < maxNesting {
		if inner, verr := decodeCompact(string(raw), depth+1, nil); verr == nil {
			p.add("nested", "nested token", inner)
			p.note("Its cty says the payload is itself a JWT, decoded above as the nested token; its own verification says what that one carries.")
			return object{}, nil
		}
	}
	p.add("payload", "payload", payloadView(raw))
	switch {
	// Left undecoded by the bound, not because it is not a token: saying it
	// was not a JWT told somebody the one thing about it that is false.
	case nested && depth >= maxNesting:
		p.note("Its cty says the payload is itself a JWT, shown undecoded: codec.jwt follows only %d levels of nesting.", maxNesting)
	case !p.jsonForm:
		p.note("The payload is not a JSON object, so this is a JWS but not a JWT: RFC 7519 requires a claims set, " +
			"and a JWS may carry anything.")
	}
	return object{}, nil
}

// detachedPayload is the payload section of a JWS whose payload is not in it.
const detachedPayload = "Detached: the payload travels separately from the token (RFC 7515 Appendix F), " +
	"so only the header and the signature are here."

// emptyPayload says what an empty payload is, which depends on the form. The
// JSON serialization detaches a payload by leaving the member out (RFC 7515
// Appendix F), so an empty one is the empty string — and that is what every
// ACME POST-as-GET signs (RFC 8555 §6.3), the main user of the JSON form,
// which this page used to call detached even beside a VERIFIED over it. A
// compact token's empty segment is either, and it cannot say which.
func (p *page) emptyPayload(header object) string {
	const detachedJSON = " A detached payload (RFC 7515 Appendix F) leaves the payload member out rather than emptying it."
	switch {
	case !p.jsonForm:
		return "The payload segment is empty: either the payload is detached and travels separately from the token " +
			"(RFC 7515 Appendix F), or it is the empty string. The token cannot say which."
	case header.has("url") && header.has("nonce"):
		return "Empty: an ACME POST-as-GET (RFC 8555 §6.3), which reads a resource by signing the empty string." + detachedJSON
	}
	return "Empty: the payload is the empty string." + detachedJSON
}

// unencoded reports whether a JWS carries its payload as it is (RFC 7797):
// b64 false in the protected header, with b64 listed in its crit.
func unencoded(protected object) bool {
	b64, set := protected.values["b64"].(bool)
	return set && !b64 && slices.Contains(critNames(protected), "b64")
}

// critNames is a header's crit, the names in it that are strings.
func critNames(o object) []string {
	list, _ := o.values["crit"].([]any)
	var out []string
	for _, v := range list {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// critical notes what a signature's crit says a verifier has to understand
// (RFC 7515 §4.1.11), which nothing used to read: a token listing an
// extension rta does not implement came back VERIFIED, bare, though the RFC
// makes it invalid to any recipient that does not understand it.
//
// A note and not a refusal. The signature is still a fact worth checking —
// Open Banking's detached signatures list extensions of their own in crit —
// and the note sits under the verdict, so the VERIFIED it qualifies is never
// read alone.
func (p *page) critical(protected, unprotected object, of string) {
	if v, ok := protected.values["crit"]; ok {
		list, isList := v.([]any)
		names := critNames(protected)
		if !isList || len(list) == 0 || len(names) != len(list) {
			p.note("The crit of the protected header%s is not a non-empty list of names, which RFC 7515 §4.1.11 "+
				"requires: a verifier refuses the token.", of)
		}
		var unknown []string
		for _, n := range names {
			if n != "b64" {
				unknown = append(unknown, quote(n))
			}
		}
		if len(unknown) > 0 {
			p.note("The crit of the protected header%s lists %s, which rta does not implement. RFC 7515 §4.1.11 "+
				"makes a JWS invalid to a recipient that does not understand what its crit lists, whatever its "+
				"signature says, so a verifier without %s refuses it.", of, strings.Join(unknown, ", "),
				format.Plural(len(unknown), "that extension", "those extensions"))
		}
	}
	for _, name := range []string{"crit", "b64"} {
		if unprotected.has(name) {
			p.note("The unprotected header%s carries %s, which has effect only in the protected header "+
				"(RFC 7515 §4.1.11, RFC 7797 §3), so it is ignored here and a verifier may refuse the token for it.", of, name)
		}
	}
}

// ctyIsJWT reports whether a cty names a JWT. RFC 7515 §4.1.10 reads a cty
// with no slash as though "application/" came before it, so "JWT", the
// spelling RFC 7519 §5.2 asks for, and "application/jwt" are one media type,
// and media types ignore case. Compared with "JWT" alone, the second read as
// a JWS carrying text, with a note that it was not a JWT.
func ctyIsJWT(cty string) bool {
	const prefix = "application/"
	if len(cty) > len(prefix) && strings.EqualFold(cty[:len(prefix)], prefix) {
		cty = cty[len(prefix):]
	}
	return strings.EqualFold(cty, "JWT")
}

// payloadView shows a payload that is not a claims set: as text when it is
// text, quoted where it holds something invisible, and as a dump when it is
// not text at all, because printing binary at a terminal shows nothing a
// reader can use.
func payloadView(raw []byte) view.View {
	if utf8.Valid(raw) {
		return view.Text{Body: visible(string(raw))}
	}
	return view.Text{Body: format.Dump(raw, maxDump)}
}

func (p *page) signatureSegment(seg string) {
	if seg == "" {
		return
	}
	_, dialect, err := decodeSegment(seg)
	if err != nil {
		p.note("The signature is not base64url (%v), so no verifier could even read it.", err)
		return
	}
	p.dialect("signature", dialect)
}

// embeddedKey says what a header's own jwk member is (RFC 7515 §4.1.3), which
// is how an ACME request and a DPoP proof (RFC 9449) carry the key they are
// signed with.
func (p *page) embeddedKey(header object) {
	key, ok := asObject(header.values["jwk"])
	if !ok {
		return
	}
	k := readJWK(key)
	line := "The header carries its own key (jwk): " + k.describe()
	if k.thumbprint != "" {
		line += ", thumbprint " + k.thumbprint
	}
	p.note("%s. A signature checked against a key the token supplies proves only that its signer holds that key — "+
		"the thumbprint is what a cnf.jkt claim or an ACME account is compared against.", line)
	if k.private {
		p.note("That key includes its private half, so whoever produced this token has published the key it signs with.")
	}
}

// verdict opens the verification section: what there was to check, and that
// nothing checked it.
//
// Every token used to get the same sentence — the signature was not checked —
// including one that has no signature. An unsecured JWT (RFC 7519 §6: alg
// "none", empty signature segment) is the classic forgery, the shape a
// verifier that takes its algorithm from the token's own header can be talked
// into accepting, and "not checked" said of it implies there was something to
// check. The comparison ignores case because "None" and "NONE" are what that
// attack sends, to slip past a filter that compares against "none" exactly.
//
// An empty signature under any other alg is a token whose header still claims
// one: stripped on the way, or cut off when it was copied.
func verdict(header object, signature string) string {
	alg := visible(header.str("alg"))
	switch {
	case signature == "" && alg != "" && !strings.EqualFold(alg, "none"):
		return "UNSIGNED — the header claims " + alg + " but the signature segment is empty: stripped, " +
			"or lost when the token was copied. There was nothing to check, and no verifier should accept it."
	case signature == "":
		claimed := "does not claim one"
		if raw := header.str("alg"); raw != "" {
			claimed = "says alg " + quote(raw)
		}
		return "UNSIGNED — there is no signature and the header " + claimed + ", so there was nothing to " +
			"check. Anyone can write a token like this, and a verifier that accepts one accepts any claims at all."
	}
	return "NOT VERIFIED — the signature was not checked. This is a debugging view of what the " +
		"token claims, not proof of who issued it."
}

// window states what the token's own dates say about whether it is live right
// now. That is the question somebody decoding a token in a hurry actually has,
// and a column of ten-digit integers answers it worse than anything else on
// the screen — the reader has to know today's epoch to subtract from.
//
// Phrased throughout as what the token says rather than what is so. These
// dates are the token's own word even when its signature checks: anybody can
// mint a token claiming to be valid until 2099, and this sentence renders
// beneath the verdict — NOT VERIFIED, or VERIFIED, which proves who signed it
// and not that its dates are true. A reader who takes "still valid" as
// authentication has been told otherwise in the same paragraph.
//
// The two sentences after the first are the rejections that are hardest to
// read off the numbers: an iat in the future, which is two clocks disagreeing
// and which a verifier allowing no skew refuses, and an exp before its own
// iat, a token that was dead when it was issued.
func window(claims object) string {
	exp, hasExp := numericDate(claims, "exp")
	nbf, hasNbf := numericDate(claims, "nbf")
	iat, hasIat := numericDate(claims, "iat")
	now := time.Now()
	var out []string
	switch {
	// RFC 7519 §4.1.4: the current time must be *before* exp, so an instant
	// equal to it is already too late.
	case hasExp && !now.Before(exp):
		out = append(out, "Its own dates say it is expired: exp is "+timefmt.Stamp(exp)+".")
	case hasNbf && now.Before(nbf):
		out = append(out, "Its own dates say it is not usable yet: nbf is "+timefmt.Stamp(nbf)+".")
	case hasExp:
		out = append(out, "Its own dates say it is unexpired: exp is "+timefmt.Stamp(exp)+".")
	}
	if hasIat && iat.After(now) {
		out = append(out, "Its iat is in the future, "+timefmt.Stamp(iat)+": the issuer's clock and this "+
			"one disagree, and a verifier that allows no clock skew refuses a token issued after the moment it checks.")
	}
	if hasIat && hasExp && exp.Before(iat) {
		out = append(out, "Its exp is before its iat, so it expired before it was issued.")
	}
	if names := notNumbers(claims); len(names) > 0 {
		list := names[0]
		if n := len(names); n > 1 {
			list = strings.Join(names[:n-1], ", ") + " and " + names[n-1]
		}
		out = append(out, fmt.Sprintf("Its %s %s, which RFC 7519 §2 requires of a NumericDate: some verifiers "+
			"refuse the token for it, and others convert it.", list,
			format.Plural(len(names), "is not a JSON number", "are not JSON numbers")))
	}
	return strings.Join(out, " ")
}

func decodeJWE(parts []string) (view.View, *view.Error) {
	header, dialect, verr := decodeHeader(parts[0], "header")
	if verr != nil {
		return nil, verr
	}
	p := &page{}
	p.dialect("header", dialect)
	p.add("header", "header", p.render(header, "header", "RFC 7516 §4"))
	content := view.KeyValue{}
	if verr := p.encryptedKey(&content, header, parts[1], "encrypted key"); verr != nil {
		return nil, verr
	}
	for i, name := range []string{"initialization vector", "ciphertext", "authentication tag"} {
		if verr := p.part(&content, name, parts[i+2]); verr != nil {
			return nil, verr
		}
	}
	p.compression(&content, header)
	p.add("content", "content", content)
	return p.finish(sealed(p, header, 1)), nil
}

// part adds one encrypted part's size to the content listing. The bytes are
// meaningless without the key, so their size is all there is to say — and it
// is what tells a truncated token from a whole one.
func (p *page) part(kv *view.KeyValue, name, seg string) *view.Error {
	raw, dialect, err := decodeSegment(seg)
	if err != nil {
		return view.Errorf("codec.jwt.invalid", "decoding the %s: %v", name, err)
	}
	p.dialect(name, dialect)
	size := "none"
	if len(raw) > 0 {
		size = format.CountOf(len(raw), "byte")
	}
	kv.Pairs = append(kv.Pairs, view.Pair{Key: name, Value: size})
	return nil
}

// encryptedKey adds the wrapped content key's size, and says why it is empty
// when it is. RFC 7516 §5.1: with alg "dir" or bare "ECDH-ES" the content key
// is agreed rather than carried, so an empty part is correct for those and a
// missing key for everything else. name is how a note refers to the part,
// which for a JSON JWE says whose key it is.
func (p *page) encryptedKey(kv *view.KeyValue, header object, seg, name string) *view.Error {
	raw, dialect, err := decodeSegment(seg)
	if err != nil {
		return view.Errorf("codec.jwt.invalid", "decoding the %s: %v", name, err)
	}
	p.dialect(name, dialect)
	alg := visible(header.str("alg"))
	value := format.CountOf(len(raw), "byte")
	if len(raw) == 0 {
		value = "none"
		if alg == "dir" || alg == "ECDH-ES" {
			value = "none — alg " + alg + " agrees the content key rather than carrying it"
		} else if alg != "" {
			p.note("The %s is empty, though alg %s carries the content key in it: the token is incomplete.", name, alg)
		}
	}
	kv.Pairs = append(kv.Pairs, view.Pair{Key: "encrypted key", Value: value})
	return nil
}

func (p *page) compression(kv *view.KeyValue, header object) {
	if zip := header.str("zip"); zip != "" {
		value := visible(zip)
		if zip == "DEF" {
			value = "DEF — the plaintext was deflated before it was encrypted"
		}
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "compression", Value: value})
	}
}

// sealed is a JWE's verification section. Nothing was checked here either, but
// the useful sentence is a different one: what is hidden, and from whom.
func sealed(p *page, header object, recipients int) string {
	key, noun := keyHeld(header.str("alg"))
	who := "whoever holds " + key
	if recipients > 1 {
		who = fmt.Sprintf("each of its %d recipients", recipients)
	} else if kid := header.str("kid"); kid != "" {
		who += " (kid " + visible(kid) + ")"
	}
	if header.str("enc") == "" {
		p.note("The header names no enc, which RFC 7516 §4.1.2 requires: whatever produced this left out the content encryption algorithm.")
	}
	if ctyIsJWT(header.str("cty")) {
		p.note("Its cty says the sealed content is itself a JWT — usually a signed one, encrypted afterwards, " +
			"so its claims and its signature are both inside.")
	}
	return "ENCRYPTED — the claims are sealed for " + who + ", and nothing here reads them: decrypting takes " +
		"that " + noun + ", and a decoder is not the place to hand it one. What is shown travels in the clear."
}

// keyHeld names what a JWE's key management algorithm (RFC 7518 §4.1) needs
// in order to decrypt: a private key for the RSA and ECDH families, a key both
// sides share for direct encryption and the AES key wraps, a password for
// PBES2. "The recipient's private key" said of a shared-key token sends
// somebody looking for a key pair that does not exist.
func keyHeld(alg string) (who, noun string) {
	switch {
	case strings.HasPrefix(alg, "RSA"), strings.HasPrefix(alg, "ECDH-ES"):
		return "the recipient's private key", "key"
	case alg == "dir", strings.HasPrefix(alg, "A") && strings.HasSuffix(alg, "KW"):
		return "the shared key it was encrypted with", "key"
	case strings.HasPrefix(alg, "PBES2"):
		return "the password it was encrypted with", "password"
	}
	return "the recipient's key", "key"
}

// decodeJSONSerialization reads the JSON form of a JWS or a JWE (RFC 7515 §7.2,
// RFC 7516 §7.2), flattened or general. It is rarer than the compact form and
// not rare at all where it is used: every ACME request (RFC 8555) is a
// flattened JWS.
func decodeJSONSerialization(input string, check *verifier) (view.View, *view.Error) {
	doc, err := decodeObject([]byte(input))
	if err != nil {
		return nil, view.Errorf("codec.jwt.invalid", "reading the JSON serialization: %v", err)
	}
	p := &page{jsonForm: true}
	p.ambiguous(doc, "JSON serialization", "RFC 7515 §7.2")
	switch {
	case doc.has("ciphertext"):
		if check != nil {
			return nil, encryptedNotSigned(check)
		}
		return p.jsonJWE(doc)
	// A signature without a payload is a detached JWS (RFC 7515 Appendix F),
	// which deletes the member rather than emptying it, and it was refused
	// as neither a JWS nor a JWE.
	case doc.has("payload"), doc.has("signature"), doc.has("signatures"):
		return p.jsonJWS(doc, check)
	case doc.has("kty") || doc.has("keys"):
		return nil, view.Errorf("codec.jwt.notatoken", "this is a JSON Web Key, not a token").
			WithHint("`rta codec jwk` reads keys and key sets")
	}
	return nil, view.Errorf("codec.jwt.invalid", "a JSON object, but neither a JWS nor a JWE: it has no payload, signature or ciphertext").
		WithHint("the JSON forms carry `payload` and `signature(s)`, or `ciphertext` and `iv`")
}

// signer is one signature of a JSON JWS, or one recipient of a JSON JWE: the
// header it was made under, split the way the serialization splits it.
type signer struct {
	protected object
	// protectedSeg is the protected header as the serialization carries it,
	// which is what the signature covers (RFC 7515 §5.2).
	protectedSeg string
	unprotected  object
	shared       bool // an unprotected header is present
	value        string
}

// merged is the header a verifier would work from: the union of both halves,
// which RFC 7515 §7.2.1 requires to be disjoint.
func (s signer) merged() object {
	out := object{values: map[string]any{}}
	for k, v := range s.unprotected.values {
		out.values[k] = v
	}
	for k, v := range s.protected.values {
		out.values[k] = v
	}
	return out
}

func (p *page) readSigner(m object, protectedWhat, valueName string) (signer, *view.Error) {
	var s signer
	if prot, ok := m.values["protected"]; ok {
		seg, isString := prot.(string)
		if !isString {
			return signer{}, view.Errorf("codec.jwt.invalid", "the %s is not a string", protectedWhat)
		}
		hdr, dialect, verr := decodeHeader(seg, protectedWhat)
		if verr != nil {
			return signer{}, verr
		}
		p.dialect(protectedWhat, dialect)
		s.protected, s.protectedSeg = hdr, seg
	}
	if u, ok := m.values["header"]; ok {
		if s.unprotected, s.shared = asObject(u); !s.shared {
			return signer{}, view.Errorf("codec.jwt.invalid", "an unprotected header is not a JSON object")
		}
	}
	s.value = m.str(valueName)
	return s, nil
}

// flattenedBeside notes the members of the flattened syntax a document holds
// beside the list of the general one. The two syntaxes exclude each other,
// and one reader takes the list while another takes the flattened members:
// a flattened alg-none signature beside a signatures list holding an RS256
// one was dropped without a word, and the page showed only the RS256 header
// another library would never read.
func (p *page) flattenedBeside(doc object, list, rfc string, members ...string) {
	var beside []string
	for _, m := range members {
		if doc.has(m) {
			beside = append(beside, m)
		}
	}
	if len(beside) > 0 {
		p.note("It carries a %s list and, beside it, %s, the %s of the flattened syntax, which %s forbids: a "+
			"parser that reads those sees a different header from the ones shown, which are the list's.",
			list, strings.Join(beside, ", "), format.Plural(len(beside), "member", "members"), rfc)
	}
}

// headerLevel is one of the headers the JSON serialization splits a signer's
// or a recipient's header across, and how a sentence names it.
type headerLevel struct {
	name string
	o    object
}

// disjoint notes the names more than one level of one header carries. RFC
// 7515 §7.2.1 and RFC 7516 §7.2.1 require the levels disjoint, because
// nothing says which a reader takes when two disagree: protected over shared
// over per-recipient is what headerOf does, and another library may do the
// reverse. reader is who has to choose.
func (p *page) disjoint(rfc, reader string, levels ...headerLevel) {
	count := map[string]int{}
	for _, l := range levels {
		for name := range l.o.values {
			count[name]++
		}
	}
	var both []string
	for name, n := range count {
		if n > 1 {
			both = append(both, name)
		}
	}
	if len(both) == 0 {
		return
	}
	sort.Strings(both)
	var where []string
	for _, l := range levels {
		for _, name := range both {
			if l.o.has(name) {
				where = append(where, l.name)
				break
			}
		}
	}
	for i := range both {
		both[i] = visible(both[i])
	}
	subject := where[0] + " and " + where[1] + " both"
	if len(where) > 2 {
		subject = strings.Join(where[:len(where)-1], ", ") + " and " + where[len(where)-1] + " all"
	}
	p.note("%s%s name %s, which %s forbids: %s has to choose one, and which one is not written down.",
		strings.ToUpper(subject[:1]), subject[1:], strings.Join(both, ", "), rfc, reader)
}

func (p *page) jsonJWS(doc object, check *verifier) (view.View, *view.Error) {
	payload, detached := "", !doc.has("payload")
	if !detached {
		var ok bool
		if payload, ok = doc.values["payload"].(string); !ok {
			return nil, view.Errorf("codec.jwt.invalid", "the payload member is not a string")
		}
	}
	var signers []signer
	if list, general := doc.values["signatures"].([]any); general {
		for i, item := range list {
			m, ok := asObject(item)
			if !ok {
				return nil, view.Errorf("codec.jwt.invalid", "signature %d is not a JSON object", i+1)
			}
			s, verr := p.readSigner(m, fmt.Sprintf("protected header of signature %d", i+1), "signature")
			if verr != nil {
				return nil, verr
			}
			signers = append(signers, s)
		}
		p.flattenedBeside(doc, "signatures", "RFC 7515 §7.2.2", "protected", "header", "signature")
	} else {
		s, verr := p.readSigner(doc, "protected header", "signature")
		if verr != nil {
			return nil, verr
		}
		signers = append(signers, s)
	}
	if len(signers) == 0 {
		return nil, view.Errorf("codec.jwt.invalid", "a JWS with an empty signatures list")
	}

	unprotected := false
	for i, s := range signers {
		if len(signers) == 1 {
			p.add("header", "protected header", p.render(s.protected, "protected header", "RFC 7515 §4"))
			if s.shared {
				p.add("unprotected", "unprotected header", p.render(s.unprotected, "unprotected header", "RFC 7515 §4"))
			}
		} else {
			parts := []view.Section{{ID: "header", Title: "protected header",
				View: p.render(s.protected, fmt.Sprintf("protected header of signature %d", i+1), "RFC 7515 §4")}}
			if s.shared {
				parts = append(parts, view.Section{ID: "unprotected", Title: "unprotected header",
					View: p.render(s.unprotected, fmt.Sprintf("unprotected header of signature %d", i+1), "RFC 7515 §4")})
			}
			p.add(fmt.Sprintf("signature-%d", i+1), fmt.Sprintf("signature %d", i+1), view.Sections{Items: parts})
		}
		unprotected = unprotected || s.shared
		of := ""
		if len(signers) > 1 {
			of = fmt.Sprintf(" of signature %d", i+1)
		}
		p.disjoint("RFC 7515 §7.2.1", "a verifier",
			headerLevel{"the protected header" + of, s.protected}, headerLevel{"its unprotected header", s.unprotected})
		p.critical(s.protected, s.unprotected, of)
		p.signatureSegment(s.value)
		p.embeddedKey(s.merged())
	}
	// RFC 7797 §3: b64 lives in the protected header, and every signature
	// has to agree about it, so the first one speaks for the payload.
	var claims object
	if detached {
		p.add("payload", "payload", view.Text{Body: detachedPayload})
	} else {
		var verr *view.Error
		if claims, verr = p.payload(signers[0].protected, payload, 0); verr != nil {
			return nil, verr
		}
	}
	if unprotected {
		p.note("An unprotected header is not covered by the signature: anything in it could have been changed on the way without the signature noticing.")
	}

	if check != nil {
		lead, others, verr := verifyEach(check, signers, payload, detached)
		if verr != nil {
			return nil, verr
		}
		p.notes = append(p.notes, check.notes...)
		return p.finish(append(append([]string{lead}, others...), window(claims))...), nil
	}
	lead := verdict(signers[0].merged(), signers[0].value)
	if len(signers) > 1 {
		lead = fmt.Sprintf("NOT VERIFIED — none of its %d signatures was checked. This is a debugging view "+
			"of what the token claims, not proof of who issued it.", len(signers))
		for i, s := range signers {
			if v := verdict(s.merged(), s.value); strings.HasPrefix(v, "UNSIGNED") {
				p.note("Signature %d: %s", i+1, v)
			}
		}
	}
	return p.finish(lead, window(claims)), nil
}

// verifyEach checks every signature of a JSON JWS with the key given. One
// that matches is enough to say the token was signed by a holder of that
// key; which of several signers a verifier requires is its own policy (RFC
// 7515 §7.2), not something the token decides. It returns the verdict to
// lead with and a line for every other signature.
//
// Every signature, not the first that matches. The page shows each one's
// header, and the header of one that did not verify — a role: admin, say —
// sat beside "Signature 2 of 2: VERIFIED" with nothing saying signature 1
// failed. And when none matched, only signature 1's error came back, with no
// number, hiding the mismatch that mattered behind an alg rta cannot check.
func verifyEach(check *verifier, signers []signer, payload string, detached bool) (string, []string, *view.Error) {
	leads := make([]string, len(signers))
	errs := make([]*view.Error, len(signers))
	good := -1
	for i, s := range signers {
		leads[i], errs[i] = check.check(s.merged(), s.protectedSeg+"."+payload, s.value)
		if errs[i] != nil && detached {
			errs[i] = notOverThisPayload(errs[i], true)
		}
		if errs[i] == nil && good < 0 {
			good = i
		}
	}
	n := len(signers)
	if n == 1 {
		return leads[0], nil, errs[0]
	}
	if good < 0 {
		best, parts := errs[0], make([]string, n)
		for i, verr := range errs {
			parts[i] = fmt.Sprintf("signature %d of %d: %s", i+1, n, verr.Message)
			if specificity(verr.Code) > specificity(best.Code) {
				best = verr
			}
		}
		return "", nil, view.Errorf(best.Code, "%s", strings.Join(parts, "; ")).WithHint(best.Hint)
	}
	var others []string
	for i := range signers {
		switch {
		case i == good:
		case errs[i] == nil:
			others = append(others, fmt.Sprintf("Signature %d of %d: %s", i+1, n, leads[i]))
		default:
			others = append(others, fmt.Sprintf("Signature %d of %d was not verified: %s.", i+1, n, errs[i].Message))
		}
	}
	return fmt.Sprintf("Signature %d of %d: %s", good+1, n, leads[good]), others, nil
}

// specificity orders refusal codes by how much they say about the token, for
// the one code a JWS with several failed signatures is refused under: a
// signature checked and found wrong says more than a key of the wrong type,
// which says more than an algorithm nothing here checks.
func specificity(code string) int {
	switch code {
	case "codec.jwt.signature":
		return 6
	case "codec.jwt.detached":
		return 5
	case "codec.jwt.key":
		return 4
	case "codec.jwt.nokey":
		return 3
	case "codec.jwt.alg":
		return 2
	case "codec.jwt.unsigned":
		return 1
	}
	return 0
}

func (p *page) jsonJWE(doc object) (view.View, *view.Error) {
	top, verr := p.readSigner(doc, "protected header", "encrypted_key")
	if verr != nil {
		return nil, verr
	}
	shared, hasShared := asObject(doc.values["unprotected"])
	if doc.has("unprotected") && !hasShared {
		return nil, view.Errorf("codec.jwt.invalid", "the shared unprotected header is not a JSON object")
	}

	var recipients []signer
	if list, general := doc.values["recipients"].([]any); general {
		for i, item := range list {
			m, ok := asObject(item)
			if !ok {
				return nil, view.Errorf("codec.jwt.invalid", "recipient %d is not a JSON object", i+1)
			}
			r, verr := p.readSigner(m, fmt.Sprintf("header of recipient %d", i+1), "encrypted_key")
			if verr != nil {
				return nil, verr
			}
			recipients = append(recipients, r)
		}
		p.flattenedBeside(doc, "recipients", "RFC 7516 §7.2.2", "header", "encrypted_key")
	} else {
		recipients = append(recipients, signer{unprotected: top.unprotected, shared: top.shared, value: top.value})
	}
	if len(recipients) == 0 {
		return nil, view.Errorf("codec.jwt.invalid", "a JWE with an empty recipients list")
	}

	p.add("header", "protected header", p.render(top.protected, "protected header", "RFC 7516 §4"))
	if hasShared {
		p.add("unprotected", "shared unprotected header", p.render(shared, "shared unprotected header", "RFC 7516 §4"))
	}
	// Every header that applies to one recipient, which is what decides the
	// algorithm its key is wrapped with: each recipient may use a different
	// one, and RFC 7516 §7.2.1 makes the three levels disjoint.
	headerOf := func(r signer) object {
		merged := r.merged()
		for k, v := range shared.values {
			merged.values[k] = v
		}
		for k, v := range top.protected.values {
			merged.values[k] = v
		}
		return merged
	}
	unprotected := hasShared
	for i, r := range recipients {
		kv := p.render(r.unprotected, "recipient header", "RFC 7516 §4")
		id, title := "recipient", "recipient"
		own := "the recipient's header"
		if len(recipients) > 1 {
			id, title = fmt.Sprintf("recipient-%d", i+1), fmt.Sprintf("recipient %d", i+1)
			own = "the header of " + title
		}
		p.disjoint("RFC 7516 §7.2.1", "a recipient", headerLevel{"the protected header", top.protected},
			headerLevel{"the shared unprotected header", shared}, headerLevel{own, r.unprotected})
		unprotected = unprotected || r.shared
		if verr := p.encryptedKey(&kv, headerOf(r), r.value, "encrypted key of the "+title); verr != nil {
			return nil, verr
		}
		p.add(id, title, kv)
	}
	merged := headerOf(recipients[0])

	content := view.KeyValue{}
	for _, m := range []struct{ member, name string }{
		{"iv", "initialization vector"}, {"ciphertext", "ciphertext"}, {"tag", "authentication tag"},
		{"aad", "additional authenticated data"},
	} {
		if !doc.has(m.member) {
			continue
		}
		if verr := p.part(&content, m.name, doc.str(m.member)); verr != nil {
			return nil, verr
		}
	}
	p.compression(&content, merged)
	p.add("content", "content", content)
	// Any recipient's, not the first's: the second one's header was as
	// unprotected when the first had none.
	if unprotected {
		p.note("Unprotected headers are not covered by the authentication tag: anything in them could have been changed on the way.")
	}
	return p.finish(sealed(p, merged, len(recipients))), nil
}
