package codec

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
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
//     content. Decrypting takes the recipient's private key, and a decoder is
//     not the place to hand one to.
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
		v, verr = decodeCompact(compactForm(token), 0, check)
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
// --secret, or returns nil when it asked for none — the ordinary case, where
// the page says nothing was verified.
func verifierFrom(req plugin.Request) (*verifier, *view.Error) {
	key, secret := strings.TrimSpace(req.String("key")), req.String("secret")
	if key == "" && secret == "" {
		return nil, nil
	}
	v := &verifier{}
	if secret != "" {
		v.secret = []byte(secret)
	}
	if key != "" {
		keys, verr := keysFrom(key)
		if verr != nil {
			return nil, verr
		}
		v.keys = keys
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
func unwrapToken(s string) string {
	s = strings.TrimSpace(s)
	if name, rest, ok := strings.Cut(s, ":"); ok {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "authorization", "dpop":
			s = strings.TrimSpace(rest)
		}
	}
	for _, scheme := range []string{"bearer", "dpop"} {
		if len(s) > len(scheme) && strings.EqualFold(s[:len(scheme)], scheme) &&
			(s[len(scheme)] == ' ' || s[len(scheme)] == '\t') {
			s = strings.TrimSpace(s[len(scheme):])
			break
		}
	}
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}
	return s
}

// compactForm drops the whitespace a compact token picks up when it is copied
// across a wrapped line. The compact serialization has none of its own, so
// nothing removed here was part of it.
func compactForm(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// decodeCompact reads a compact token. check, when set, verifies its
// signature; it is never handed to a nested token, whose signature the key
// given for the outer one says nothing about.
func decodeCompact(token string, depth int, check *verifier) (view.View, *view.Error) {
	parts := strings.Split(token, ".")
	switch len(parts) {
	case 3:
		return decodeJWS(parts, depth, check)
	case 5:
		if check != nil {
			return nil, encryptedNotSigned()
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
func decodeSegment(s string) ([]byte, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err == nil {
		return raw, "", nil
	}
	for _, d := range []struct {
		enc  *base64.Encoding
		name string
	}{
		{base64.URLEncoding, "padded"},
		{base64.RawStdEncoding, "standard-alphabet"},
		{base64.StdEncoding, "padded, standard-alphabet"},
	} {
		if raw, derr := d.enc.DecodeString(s); derr == nil {
			return raw, d.name, nil
		}
	}
	return nil, "", err
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

// render is keyValueOf for a page: what it had to escape and any member it
// found named twice are recorded against the page.
func (p *page) render(o object, what, rfc string) view.KeyValue {
	r := keyValueOf(o)
	p.escaped = append(p.escaped, r.escaped...)
	p.duplicates(o, what, rfc)
	return r.kv
}

func (p *page) duplicates(o object, what, rfc string) {
	if len(o.dupes) == 0 {
		return
	}
	names := make([]string, len(o.dupes))
	for i, d := range o.dupes {
		names[i] = visible(d)
	}
	p.note("The %s names %s more than once. %s lets a parser refuse that or keep the last value, "+
		"so two libraries can read this token differently; shown is the last.",
		what, strings.Join(names, ", "), rfc)
}

func (p *page) dialect(what, dialect string) {
	if dialect != "" {
		p.note("The %s is %s base64. RFC 7515 §2 requires unpadded base64url, and a strict parser refuses anything else.",
			what, dialect)
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

func encryptedNotSigned() *view.Error {
	return view.Errorf("codec.jwt.encrypted", "this token is encrypted, not signed: there is no signature here to verify").
		WithHint("without --key it shows the header, and its cty says whether a signed token is sealed inside")
}

func decodeJWS(parts []string, depth int, check *verifier) (view.View, *view.Error) {
	header, dialect, verr := decodeHeader(parts[0], "header")
	if verr != nil {
		return nil, verr
	}
	lead := verdict(header, parts[2])
	if check != nil {
		// The signing input is the two segments exactly as the token carries
		// them (RFC 7515 §5.2), never re-encoded: a padded header was signed
		// padded.
		if lead, verr = check.check(header, parts[0]+"."+parts[1], parts[2]); verr != nil {
			return nil, verr
		}
	}
	p := &page{}
	p.dialect("header", dialect)
	p.add("header", "header", p.render(header, "header", "RFC 7515 §4"))
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
		p.add("payload", "payload", view.Text{Body: "Detached: the payload travels separately from the token " +
			"(RFC 7515 Appendix F), so only the header and the signature are here."})
		return object{}, nil
	}
	var raw []byte
	if b64, ok := header.values["b64"].(bool); ok && !b64 {
		raw = []byte(seg)
		p.note("Its header sets b64 to false (RFC 7797), so the payload is carried as it is rather than encoded.")
	} else {
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
	if strings.EqualFold(header.str("cty"), "JWT") && depth < maxNesting {
		if inner, verr := decodeCompact(compactForm(string(raw)), depth+1, nil); verr == nil {
			p.add("nested", "nested token", inner)
			p.note("Its cty says the payload is itself a JWT, decoded above as the nested token; its own verification says what that one carries.")
			return object{}, nil
		}
	}
	p.add("payload", "payload", payloadView(raw))
	if !p.jsonForm {
		p.note("The payload is not a JSON object, so this is a JWS but not a JWT: RFC 7519 requires a claims set, " +
			"and a JWS may carry anything.")
	}
	return object{}, nil
}

// payloadView shows a payload that is not a claims set: as text when it is
// text, quoted where it holds something invisible, and as a dump when it is
// not text at all, because printing binary at a terminal shows nothing a
// reader can use.
func payloadView(raw []byte) view.View {
	if utf8.Valid(raw) {
		return view.Text{Body: visible(string(raw))}
	}
	return view.Text{Body: dump(raw)}
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
// dates are exactly as unverified as the rest of it: anybody can mint a token
// claiming to be valid until 2099, and this sentence renders directly beneath
// the line saying nobody checked. A reader who takes "still valid" as
// authentication has been told otherwise twice in the same paragraph.
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
	if strings.EqualFold(header.str("cty"), "JWT") {
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
	p.duplicates(doc, "JSON serialization", "RFC 7515 §7.2")
	switch {
	case doc.has("ciphertext"):
		if check != nil {
			return nil, encryptedNotSigned()
		}
		return p.jsonJWE(doc)
	case doc.has("payload"):
		return p.jsonJWS(doc, check)
	case doc.has("kty") || doc.has("keys"):
		return nil, view.Errorf("codec.jwt.notatoken", "this is a JSON Web Key, not a token").
			WithHint("`rta codec jwk` reads keys and key sets")
	}
	return nil, view.Errorf("codec.jwt.invalid", "a JSON object, but neither a JWS nor a JWE: it has no payload and no ciphertext").
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
	var both []string
	for name := range s.unprotected.values {
		if s.protected.has(name) {
			both = append(both, visible(name))
		}
	}
	if len(both) > 0 {
		p.note("The %s and the unprotected header beside it both name %s, which RFC 7515 §7.2.1 forbids: "+
			"a verifier has to choose one, and which one is not written down.", protectedWhat, strings.Join(both, ", "))
	}
	return s, nil
}

func (p *page) jsonJWS(doc object, check *verifier) (view.View, *view.Error) {
	payload, ok := doc.values["payload"].(string)
	if !ok {
		return nil, view.Errorf("codec.jwt.invalid", "the payload member is not a string")
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
		p.signatureSegment(s.value)
		p.embeddedKey(s.merged())
	}
	// RFC 7797 §3: b64 lives in the protected header, and every signature
	// has to agree about it, so the first one speaks for the payload.
	claims, verr := p.payload(signers[0].protected, payload, 0)
	if verr != nil {
		return nil, verr
	}
	if unprotected {
		p.note("An unprotected header is not covered by the signature: anything in it could have been changed on the way without the signature noticing.")
	}

	if check != nil {
		lead, verr := verifyEach(check, signers, payload)
		if verr != nil {
			return nil, verr
		}
		return p.finish(lead, window(claims)), nil
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

// verifyEach checks every signature of a JSON JWS with the key given and
// reports the first that matches. One is enough to say the token was signed
// by a holder of that key; which of several signers a verifier requires is
// its own policy (RFC 7515 §7.2), not something the token decides.
func verifyEach(check *verifier, signers []signer, payload string) (string, *view.Error) {
	var first *view.Error
	for i, s := range signers {
		lead, verr := check.check(s.merged(), s.protectedSeg+"."+payload, s.value)
		if verr == nil {
			if len(signers) > 1 {
				lead = fmt.Sprintf("Signature %d of %d: %s", i+1, len(signers), lead)
			}
			return lead, nil
		}
		if first == nil {
			first = verr
		}
	}
	return "", first
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
	for i, r := range recipients {
		kv := p.render(r.unprotected, "recipient header", "RFC 7516 §4")
		id, title := "recipient", "recipient"
		if len(recipients) > 1 {
			id, title = fmt.Sprintf("recipient-%d", i+1), fmt.Sprintf("recipient %d", i+1)
		}
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
	if hasShared || recipients[0].shared {
		p.note("Unprotected headers are not covered by the authentication tag: anything in them could have been changed on the way.")
	}
	return p.finish(sealed(p, merged, len(recipients))), nil
}
