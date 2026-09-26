package codec

import (
	"context"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // x5t is defined as a SHA-1 thumbprint (RFC 7517 §4.8); this compares one and protects nothing
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"

	"github.com/this-is-tobi/rta/builtin/internal/timefmt"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A JSON Web Key (RFC 7517), read for what it is rather than what it spells.
//
// The members of a JWK are base64url numbers, and none of what somebody
// debugging a key set wants to know is written in one: how large the key is,
// whether it is even a valid point on its curve, what its RFC 7638 thumbprint
// is — the value a DPoP token's cnf.jkt and a pinned key are compared against
// — and above all whether it is private. A key set is served to anyone who
// asks, and one carrying a d member is handing out the signing key.
//
// Nothing private is ever printed. The facts are computed from the members;
// the members themselves stay in the input.

// jwkFacts is what a JWK says about its key, computed rather than copied.
type jwkFacts struct {
	kty, crv, alg, use, kid string
	ops                     []string
	size                    string
	thumbprint              string
	private                 bool
	// secrets are the private members the key holds, for the line saying so.
	secrets  []string
	problems []string
	// pub is the public key the members make, when they make a valid one.
	pub crypto.PublicKey
	// cert is x5c's first certificate, and chain how many x5c holds.
	cert  *x509.Certificate
	chain int
}

func (k *jwkFacts) problem(format string, args ...any) {
	k.problems = append(k.problems, fmt.Sprintf(format, args...))
}

// member decodes one base64url member of the key, noting a problem when it is
// missing or malformed. The length of what it decodes is most of what a key
// has to be checked by.
func (k *jwkFacts) member(o object, name string) []byte {
	s := o.str(name)
	if s == "" {
		k.problem("it has no %s", name)
		return nil
	}
	raw, dialect, err := decodeSegment(s)
	if err != nil {
		k.problem("its %s is not base64url", name)
		return nil
	}
	form, loose := splitDialect(dialect)
	if form != "" {
		k.problem("its %s is %s base64, where RFC 7518 §6 requires unpadded base64url", name, form)
	}
	if loose {
		k.problem("its %s ends in a character whose unused bits are not zero, which a strict decoder refuses (RFC 4648 §3.5)", name)
	}
	return raw
}

func readJWK(o object) jwkFacts {
	k := jwkFacts{kty: o.str("kty"), crv: o.str("crv"), alg: o.str("alg"), use: o.str("use"), kid: o.str("kid")}
	if ops, ok := o.values["key_ops"].([]any); ok {
		for _, op := range ops {
			if s, ok := op.(string); ok {
				k.ops = append(k.ops, s)
			}
		}
	}
	switch k.kty {
	case "RSA":
		k.rsa(o)
	case "EC":
		k.ec(o)
	case "OKP":
		k.okp(o)
	case "oct":
		k.size = fmt.Sprintf("%d-bit shared secret", len(k.member(o, "k"))*8)
		k.private = true
	case "":
		k.problem("it names no kty, which RFC 7517 §4.1 requires")
	default:
		k.problem("its kty %s is not one RFC 7518 or RFC 8037 defines", quote(k.kty))
	}
	k.thumbprint = thumbprint(o, k.kty)
	k.certificate(o)
	return k
}

// privateMembers records which of names o holds, and marks the key private
// when it holds any.
func (k *jwkFacts) privateMembers(o object, names ...string) {
	for _, n := range names {
		if o.has(n) {
			k.secrets = append(k.secrets, n)
		}
	}
	k.private = len(k.secrets) > 0
}

// rsa reads an RSA public key. RFC 7518 §6.3.1.1 forbids a leading zero octet
// in n, and a key that has one reads as a different size to different
// libraries, so it is named rather than silently trimmed.
//
// Private whatever the member: the primes give the signing key away as surely
// as d does, since n = p·q settles it, and a JWK publishing p and q without d
// was reported as holding nothing private.
func (k *jwkFacts) rsa(o object) {
	k.privateMembers(o, "d", "p", "q", "dp", "dq", "qi", "oth")
	k.size = "RSA"
	n, e := k.member(o, "n"), k.member(o, "e")
	if n == nil || e == nil {
		return
	}
	if len(n) > 1 && n[0] == 0 {
		k.problem("its n starts with a zero byte, which RFC 7518 §6.3.1.1 forbids")
	}
	modulus, exponent := new(big.Int).SetBytes(n), new(big.Int).SetBytes(e)
	bits := modulus.BitLen()
	k.size = fmt.Sprintf("%d-bit RSA", bits)
	if modulus.Bit(0) == 0 {
		k.problem("its n is even, which no RSA modulus is, so no signature verifies against it")
	} else if size := rsaSizeProblem(bits); size != "" {
		k.problems = append(k.problems, size)
	}
	// No key is made, so nothing downstream can spend the square of it.
	if bits > maxRSABits {
		return
	}
	if !exponent.IsInt64() || exponent.Int64() < 3 || exponent.Int64() > 1<<31-1 {
		k.problem("its exponent is not one a verifier can use")
		return
	}
	if exponent.Int64() != 65537 {
		k.size += fmt.Sprintf(", exponent %d", exponent.Int64())
	}
	// crypto/rsa refuses the key inside the check, and codec.jwt, which
	// sends somebody here to find out what is wrong with a key, said so;
	// this page found nothing.
	if exponent.Bit(0) == 0 {
		k.problem("its exponent is even, which no RSA key has, so no signature verifies against it")
		return
	}
	k.pub = &rsa.PublicKey{N: modulus, E: int(exponent.Int64())}
}

// rsaSizeProblem is what an RSA modulus of this many bits has wrong with its
// size, or "". One sentence for a JWK and a PEM key alike: only readJWK said
// anything, so the same 1024-bit key was qualified as a JWK and a bare
// VERIFIED as PEM, though a strict library refuses it either way.
func rsaSizeProblem(bits int) string {
	switch {
	case bits > maxRSABits:
		return fmt.Sprintf("its modulus is %d bits, over the %d a verifier accepts", bits, maxRSABits)
	case bits < 1024:
		return fmt.Sprintf("its modulus is %d bits, under the 1024 a verifier accepts and the 2048 RFC 7518 §3.3 "+
			"requires: a key that small can be factored, and anyone who does signs as its issuer", bits)
	case bits < 2048:
		return fmt.Sprintf("its modulus is %d bits, under the 2048 RFC 7518 §3.3 requires", bits)
	}
	return ""
}

// ecCurves are the curves RFC 7518 §6.2.1.1 names, with the coordinate size
// §6.2.1.2 fixes for each: a coordinate is always that long, leading zeros
// included, so a shorter one is a producer that trimmed them.
var ecCurves = map[string]struct {
	curve elliptic.Curve
	size  int
}{
	"P-256": {elliptic.P256(), 32},
	"P-384": {elliptic.P384(), 48},
	"P-521": {elliptic.P521(), 66},
}

func (k *jwkFacts) ec(o object) {
	k.privateMembers(o, "d")
	k.size = "EC " + visible(k.crv)
	x, y := k.member(o, "x"), k.member(o, "y")
	c, known := ecCurves[k.crv]
	switch {
	case k.crv == "":
		k.problem("it names no crv, which an EC key requires")
		return
	// RFC 8812. The standard library has no such curve, so neither the point
	// nor an ES256K signature can be checked, and codec.jwt's refusal of the
	// key sends somebody here to find out why.
	case k.crv == "secp256k1":
		k.problem("rta has no secp256k1, so it checks neither its point nor an ES256K signature against it")
		return
	case !known:
		k.problem("its curve %s is not one RFC 7518 names", quote(k.crv))
		return
	case x == nil || y == nil:
		return
	case len(x) != c.size || len(y) != c.size:
		k.problem("its x and y are %d and %d bytes, and %s takes %d each (RFC 7518 §6.2.1.2)",
			len(x), len(y), k.crv, c.size)
		return
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(c.curve, append(append([]byte{4}, x...), y...))
	if err != nil {
		k.problem("its x and y are not a point on %s, so no signature can verify against it", k.crv)
		return
	}
	k.pub = pub
}

// okpCurves are RFC 8037's curves and their key sizes. The X curves agree
// keys and never sign, which is worth saying about a key in a set a verifier
// reads — unless its use already says it is for encryption.
var okpCurves = map[string]int{"Ed25519": 32, "Ed448": 57, "X25519": 32, "X448": 56}

func (k *jwkFacts) okp(o object) {
	k.privateMembers(o, "d")
	k.size = visible(k.crv)
	x := k.member(o, "x")
	size, known := okpCurves[k.crv]
	switch {
	case k.crv == "":
		k.problem("it names no crv, which an OKP key requires")
	case !known:
		k.problem("its curve %s is not one RFC 8037 names", quote(k.crv))
	case x == nil:
	case len(x) != size:
		k.problem("its x is %d bytes, and %s takes %d", len(x), k.crv, size)
	case k.crv == "Ed25519":
		k.pub = ed25519.PublicKey(x)
	case k.crv == "X25519":
		if _, err := ecdh.X25519().NewPublicKey(x); err != nil {
			k.problem("its x is not an X25519 public key")
		}
	}
	switch {
	case (k.crv == "X25519" || k.crv == "X448") && k.use != "enc":
		k.problem("it is a key-agreement key (RFC 8037 §3.2), which cannot verify a signature")
	case k.crv == "Ed448":
		k.problem("rta does not check Ed448 signatures, so codec.jwt cannot verify against it")
	}
}

// thumbprintMembers are the members RFC 7638 §3.2 hashes for each key type,
// already in the lexicographic order §3.3 requires.
//
// Not oct, though §3.2 defines one for it. A shared secret's thumbprint is an
// unsalted SHA-256 of the secret and nothing else, so printing it handed out
// a dictionary-attack target for every HMAC secret somebody chose — `hunter2`
// falls out of a ten-line script — on a page that promises nothing private
// is printed. And it pins nothing: cnf.jkt and an ACME account name
// asymmetric keys.
var thumbprintMembers = map[string][]string{
	"RSA": {"e", "kty", "n"},
	"EC":  {"crv", "kty", "x", "y"},
	"OKP": {"crv", "kty", "x"},
}

// thumbprint is the RFC 7638 SHA-256 thumbprint, or "" when the key lacks a
// member it is computed from. It hashes the members exactly as the key spells
// them, which is what makes two parties' thumbprints of one key agree.
func thumbprint(o object, kty string) string {
	names, ok := thumbprintMembers[kty]
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, name := range names {
		v, ok := o.values[name].(string)
		if !ok {
			return ""
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(compactJSON(name) + ":" + compactJSON(v))
	}
	b.WriteByte('}')
	sum := sha256.Sum256([]byte(b.String()))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// certificate reads x5c (RFC 7517 §4.7) and checks it against the key beside
// it: its first certificate must hold the same key, and x5t / x5t#S256 must be
// thumbprints of that certificate. A chain that disagrees with its own key is
// the misconfiguration a verifier trusting either one would never report.
//
// x5c is standard base64 with padding — the one member of a JWK that is not
// base64url — and producers get it wrong in both directions, so both are read.
func (k *jwkFacts) certificate(o object) {
	v, present := o.values["x5c"]
	if !present {
		return
	}
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		k.problem("its x5c is not a list of certificates")
		return
	}
	first, _ := list[0].(string)
	der, err := base64.StdEncoding.DecodeString(first)
	if err != nil {
		var derr error
		if der, _, derr = decodeSegment(first); derr != nil {
			k.problem("the first certificate in its x5c is not base64")
			return
		}
		// Named by what the text holds rather than by which decoder took
		// it: the unpadded decoders try the URL alphabet first, so a
		// certificate that had only lost its padding was said to be in the
		// wrong alphabet.
		if strings.ContainsAny(first, "-_") {
			k.problem("its x5c is base64url, where RFC 7517 §4.7 requires standard base64")
		} else {
			k.problem("its x5c is unpadded, where RFC 7517 §4.7 requires padded standard base64")
		}
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		k.problem("the first certificate in its x5c does not parse: %v", err)
		return
	}
	k.cert, k.chain = cert, len(list)
	if eq, ok := k.pub.(interface{ Equal(crypto.PublicKey) bool }); ok && !eq.Equal(cert.PublicKey) {
		k.problem("the first certificate in its x5c holds a different key from this JWK, which RFC 7517 §4.7 forbids")
	}
	sha1Sum, sha256Sum := sha1.Sum(der), sha256.Sum256(der) //nolint:gosec // see the import
	for _, t := range []struct{ member, want string }{
		{"x5t", base64.RawURLEncoding.EncodeToString(sha1Sum[:])},
		{"x5t#S256", base64.RawURLEncoding.EncodeToString(sha256Sum[:])},
	} {
		if got := o.str(t.member); got != "" && got != t.want {
			k.problem("its %s is not the thumbprint of the first certificate in its x5c", t.member)
		}
	}
}

// describe is the key in a few words: "2048-bit RSA", "EC P-256", "Ed25519".
func (k jwkFacts) describe() string {
	if k.size != "" {
		return k.size
	}
	if k.kty != "" {
		return visible(k.kty)
	}
	return "unknown"
}

func (k jwkFacts) privateLine() string {
	switch {
	case k.kty == "oct":
		return "yes — a shared secret: whoever holds this JWK can sign as its issuer"
	case k.private:
		return "yes — it holds the private key (" + strings.Join(k.secrets, ", ") +
			"); only the public half belongs in a set anyone can fetch"
	}
	return "no"
}

func runJWK(_ context.Context, req plugin.Request) (view.View, error) {
	raw, verr := joseInput(req, "key", "codec.jwk", "key",
		"pass it as an argument, or pipe it: `curl -s https://issuer.example/.well-known/jwks.json | rta codec jwk`")
	if verr != nil {
		return nil, verr
	}
	doc, err := decodeObject([]byte(unwrapToken(raw)))
	if err != nil {
		hint := "a JWK is a JSON object with a kty member, and a key set one with a keys list"
		switch {
		case strings.HasPrefix(raw, "-----BEGIN"):
			hint = pemHint(raw, req.Surface())
		case strings.Count(raw, ".") == 2 || strings.Count(raw, ".") == 4:
			hint = "that looks like a token — `rta codec jwt` reads those"
		}
		return nil, view.Errorf("codec.jwk.invalid", "not a JSON Web Key: %v", err).WithHint(hint)
	}
	if keys, ok := doc.values["keys"]; ok {
		list, ok := keys.([]any)
		if !ok {
			return nil, view.Errorf("codec.jwk.invalid", "the keys member of a key set is not a list")
		}
		return keySetView(doc, list), nil
	}
	if !doc.has("kty") {
		return nil, view.Errorf("codec.jwk.invalid", "a JSON object, but not a key: it has neither kty nor keys").
			WithHint("a JWK is a JSON object with a kty member, and a key set one with a keys list")
	}
	return keyView(doc), nil
}

// pemHint says where PEM handed to codec.jwk goes, by what its first block
// holds. Every block was told that `rta cert inspect` reads a certificate,
// and a public key, the PEM somebody pastes here for its thumbprint, is none:
// cert inspect refuses it, and takes a file or a host, not pasted text.
func pemHint(raw string, s plugin.Surface) string {
	block, _ := pem.Decode([]byte(repairPEM(raw)))
	switch {
	case block == nil:
		return "that is PEM, and codec.jwk reads JSON Web Keys"
	case block.Type == "CERTIFICATE":
		return "that is a PEM certificate — saved to a file, `rta cert inspect <file>` reads it"
	case strings.HasSuffix(block.Type, "KEY"):
		where := "`rta codec jwt --key` takes a PEM key as it is"
		if s == plugin.SurfaceTUI || s == plugin.SurfaceMCP {
			where = "codec.jwt takes a PEM key as it is, in " + inputName(s, "key")
		}
		return "that is a PEM key, and codec.jwk reads JSON Web Keys — " + where + ", to verify a token with"
	}
	return "that is PEM, and codec.jwk reads JSON Web Keys"
}

func keyView(o object) view.View {
	k := readJWK(o)
	p := &page{}
	p.ambiguous(o, "key", "RFC 7517 §4")
	kv := view.KeyValue{Pairs: []view.Pair{{Key: "type", Value: k.describe()}}}
	for _, m := range []struct{ key, value string }{
		{"kid", k.kid}, {"alg", k.alg}, {"use", k.use}, {"key_ops", strings.Join(k.ops, ", ")},
	} {
		if m.value != "" {
			kv.Pairs = append(kv.Pairs, view.Pair{Key: m.key, Value: visible(m.value)})
		}
	}
	switch {
	case k.thumbprint != "":
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "thumbprint", Value: k.thumbprint + "  (RFC 7638, SHA-256)"})
	case k.kty == "oct":
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "thumbprint",
			Value: "not shown — for a shared secret it is a hash of the secret itself"})
	}
	kv.Pairs = append(kv.Pairs, view.Pair{Key: "private", Value: k.privateLine()})
	p.add("key", "key", kv)
	if k.cert != nil {
		p.add("certificate", "certificate", certificateView(k))
	}
	for _, pr := range k.problems {
		p.note("%s%s.", strings.ToUpper(pr[:1]), pr[1:])
	}
	return p.notesPage()
}

func certificateView(k jwkFacts) view.KeyValue {
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "subject", Value: visible(k.cert.Subject.String())},
		{Key: "issuer", Value: visible(k.cert.Issuer.String())},
		{Key: "not after", Value: timefmt.Stamp(k.cert.NotAfter)},
		{Key: "chain", Value: format.CountOf(k.chain, "certificate")},
	}}
}

// keySetView is handed the whole document as well as its list, because what
// a strict parser refuses is in the document: `{"keys":[],"keys":[K]}` is an
// empty set to a parser that keeps the first, and a kty given twice sits in
// keys[i], and neither was named while the list was all this saw.
func keySetView(doc object, list []any) view.View {
	p := &page{}
	p.ambiguous(doc, "key set", "RFC 7517 §5")
	t := view.Table{Columns: []view.Column{
		{Name: "Kid"}, {Name: "Type"}, {Name: "Alg"}, {Name: "Use"}, {Name: "Thumbprint"},
		{Name: "Private"}, {Name: "Certificate expires"},
	}}
	// A kid shared by two keys of the same type is ambiguous to a verifier;
	// RFC 7517 §4.5 names one of different types — an RSA and an EC key
	// offered as alternatives — as the legitimate case, so the type is part
	// of what has to repeat.
	type kidOf struct{ kid, kty string }
	var kids []kidOf
	perKid := map[kidOf]int{}
	certs := false
	for i, item := range list {
		o, ok := asObject(item)
		if !ok {
			p.note("Key %d is not a JSON object.", i+1)
			continue
		}
		k := readJWK(o)
		label := fmt.Sprintf("%d", i+1)
		if k.kid != "" {
			label += " (kid " + quote(k.kid) + ")"
			id := kidOf{k.kid, k.kty}
			if perKid[id] == 0 {
				kids = append(kids, id)
			}
			perKid[id]++
		}
		certs = certs || k.cert != nil
		private := "no"
		if k.private {
			private = "yes"
			p.note("Key %s: %s.", label, strings.TrimPrefix(k.privateLine(), "yes — "))
		}
		expires := ""
		if k.cert != nil {
			expires = timefmt.Stamp(k.cert.NotAfter)
		}
		row := []string{visible(k.kid), k.describe(), visible(k.alg), visible(k.use), k.thumbprint, private, expires}
		if row[0] != k.kid || row[2] != k.alg || row[3] != k.use {
			p.escaped = append(p.escaped, "key "+label)
		}
		t.Rows = append(t.Rows, row)
		for _, pr := range k.problems {
			p.note("Key %s: %s.", label, pr)
		}
	}
	t.Total = len(t.Rows)
	if !certs {
		t.Columns = t.Columns[:len(t.Columns)-1]
		for i := range t.Rows {
			t.Rows[i] = t.Rows[i][:len(t.Rows[i])-1]
		}
	}
	for _, id := range kids {
		if n := perKid[id]; n > 1 {
			p.note("Kid %s names %d %s keys, so a verifier choosing a key by kid cannot tell them apart "+
				"(RFC 7517 §4.5 asks for distinct kids).", quote(id.kid), n, visible(id.kty))
		}
	}
	if len(list) == 0 {
		p.note("The set holds no keys, so nothing can verify against it.")
	}
	p.add("keys", "keys", t)
	return p.notesPage()
}
