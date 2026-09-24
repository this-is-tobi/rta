package codec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"os"
	"strings"

	"github.com/this-is-tobi/rta/builtin/internal/pipein"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Checking a JWS signature against a key the person supplies.
//
// Offline and deliberately narrow. The key comes from the caller — a JWK, a
// key set, PEM, a certificate — and never from a URL: an issuer's jwks_uri is
// a destination, and a capability that fetches a destination its caller names
// is not a free read. The person fetches the key set; this checks against it.
//
// **The algorithm is decided by the key, never by the token.** A verifier that
// lets the header choose can be handed an HS256 token and use an RSA public
// key — which is public — as the HMAC secret, the algorithm-confusion attack
// that has broken JWT libraries more than once. Here each key admits only the
// algorithms of its own type, an HMAC signature is checked only with a secret
// given as one, and the mismatch is refused by name rather than as a failed
// check, because it is the thing somebody debugging needs to be told.
//
// A signature that does not match is an error, so a script can branch on the
// exit code; the claims are one run without the key away.

// candidate is one key a signature may be checked with, and how to name it.
type candidate struct {
	pub    crypto.PublicKey
	secret []byte
	kid    string
	alg    string
	use    string
	label  string
	// readings are the other ways the secret file's contents may have been
	// meant, tried in order when they do not match as they are.
	readings []reading
}

// reading is one other way to read a secret, and how to name it when it is
// the one that matched.
type reading struct {
	secret []byte
	label  string
}

// maxSecretFile bounds what --secret-file reads. An HMAC key is the size of
// its hash, 32 to 64 bytes, and one somebody chose is a line: a file larger
// than this is the wrong file rather than a longer secret.
const maxSecretFile = 64 << 10

// secretFrom reads the shared secret --secret-file names, and the two other
// forms it is most often handed over in by mistake: with the line break an
// editor or `echo` leaves at the end, and as base64 of the bytes rather than
// the bytes.
func secretFrom(path string) (candidate, *view.Error) {
	f, err := os.Open(plugin.ExpandHome(path))
	if err != nil {
		return candidate{}, view.Errorf("codec.jwt.secret", "reading the secret file: %v", err)
	}
	defer f.Close()
	raw, err := pipein.ReadFrom(f, maxSecretFile)
	switch {
	case errors.Is(err, pipein.ErrTooLarge):
		return candidate{}, view.Errorf("codec.jwt.secret", "%s holds more than the %s a shared secret could be",
			quote(path), format.Bytes(maxSecretFile)).
			WithHint("--secret-file names a file holding the secret and nothing else")
	case err != nil:
		return candidate{}, view.Errorf("codec.jwt.secret", "reading the secret file: %v", err)
	case raw == "":
		return candidate{}, view.Errorf("codec.jwt.secret", "the secret file %s is empty", quote(path))
	}
	if what := publicKeyIn([]byte(raw)); what != "" {
		return candidate{}, keyAsSecret(path, what)
	}
	if decoded, derr := decodeAnyBase64(strings.Join(strings.Fields(raw), "")); derr == nil {
		if what := publicKeyIn(decoded); what != "" {
			return candidate{}, keyAsSecret(path, what+", in base64")
		}
	}
	// An oct JWK is how a shared secret is written as a key, and --key sends
	// one here, so its k is the secret rather than the JSON around it.
	if doc, err := decodeObject([]byte(strings.TrimSpace(raw))); err == nil && doc.str("kty") == "oct" {
		secret := octSecret(doc)
		if secret == nil {
			return candidate{}, view.Errorf("codec.jwt.secret", "the oct key in %s has no k that decodes", quote(path))
		}
		return candidate{secret: secret, label: "the oct key in " + quote(path)}, nil
	}
	c := candidate{secret: []byte(raw), label: "the secret in " + quote(path)}
	trimmed := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
	if trimmed != raw && trimmed != "" {
		c.readings = append(c.readings, reading{[]byte(trimmed), c.label + ", without its final line break"})
	}
	if decoded, derr := decodeAnyBase64(strings.TrimSpace(raw)); derr == nil && len(decoded) > 0 {
		c.readings = append(c.readings, reading{decoded, c.label + ", read as base64"})
	}
	return c, nil
}

// publicKeyIn names the key or certificate b holds, or returns "" when it
// holds none, for the check that keeps a public key from being used as an
// HMAC secret.
//
// That guard used to look only at which input the material arrived in, and
// whatever came in as the secret was HMAC bytes. So the text of a public key
// PEM, or the bare base64 SPKI Keycloak's admin console shows a realm's key
// as, checked an HS256 token forged with it — VERIFIED, "proves whoever
// signed it holds that key", about a key everyone holds. The algorithm-
// confusion attack, reached through the one door fits does not watch.
func publicKeyIn(b []byte) string {
	s := strings.TrimSpace(string(b))
	switch {
	case strings.Contains(s, "-----BEGIN"):
		return "PEM key material"
	case strings.HasPrefix(s, "{"):
		doc, err := decodeObject([]byte(s))
		if err != nil {
			return ""
		}
		if asymmetric(doc) {
			return "an " + doc.str("kty") + " JWK" // RSA, EC and OKP all take "an"
		}
		list, _ := doc.values["keys"].([]any)
		for _, item := range list {
			if o, ok := asObject(item); ok && asymmetric(o) {
				return "a key set"
			}
		}
		return ""
	}
	if _, err := x509.ParsePKIXPublicKey(b); err == nil {
		return "a DER public key"
	}
	if _, err := x509.ParsePKCS1PublicKey(b); err == nil {
		return "a DER public key"
	}
	if _, err := x509.ParseCertificate(b); err == nil {
		return "a DER certificate"
	}
	return ""
}

func asymmetric(o object) bool {
	switch o.str("kty") {
	case "RSA", "EC", "OKP":
		return true
	}
	return false
}

func keyAsSecret(path, what string) *view.Error {
	return view.Errorf("codec.jwt.alg", "the secret file %s holds %s, not a shared secret: a public key used as "+
		"an HMAC secret is the algorithm-confusion attack — anyone holding the key can sign with it — so it is never tried",
		quote(path), what).
		WithHint("a public key or certificate goes in --key, which checks only the signatures its own type makes")
}

// keysFrom reads the verification material a person supplied, and says what
// it noticed on the way for the page to carry. A private key is accepted and
// only its public half used: it is the caller's own machine, and refusing
// would only send them off to extract the half by hand.
func keysFrom(raw string, s plugin.Surface) ([]candidate, []string, *view.Error) {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, "{"):
		return jwkCandidates(raw, s)
	case strings.Contains(raw, "-----BEGIN"):
		keys, verr := pemCandidates(raw)
		return keys, nil, verr
	}
	// A key's DER in bare base64 is how Keycloak's console shows a realm's
	// key. This hint used to send it to the secret, which is where a key
	// verifies a forgery.
	if der, err := decodeAnyBase64(strings.Join(strings.Fields(raw), "")); err == nil {
		if what := publicKeyIn(der); what != "" {
			return nil, nil, view.Errorf("codec.jwt.key", "the key is %s in base64, without the PEM armour --key reads", what).
				WithHint("put -----BEGIN PUBLIC KEY----- and -----END PUBLIC KEY----- on the lines around it " +
					"(CERTIFICATE for a certificate)")
		}
	}
	return nil, nil, view.Errorf("codec.jwt.key", "the key is not a JWK, a key set or PEM").
		WithHint("pass the issuer's key set as it is served, or a PEM public key or certificate; for an HMAC signature, " +
			secretHint(s))
}

// secretHint says where an HMAC secret goes, on the surface asking. Only the
// CLI has a --secret-file flag and only the TUI a box for it. An agent has
// neither, since the input is Local, and a hint naming a flag its schema does
// not have is one it can only guess at.
func secretHint(s plugin.Surface) string {
	switch s {
	case plugin.SurfaceMCP:
		return "a shared secret is taken only from the person at the terminal, in a file, never from an agent"
	case plugin.SurfaceTUI:
		return "name a file holding the shared secret in the secret-file box"
	}
	return "pass a file holding the shared secret with --secret-file"
}

// jwkCandidates reads a JWK or a key set. A shared secret (kty oct) is not
// taken from it, and that is the line Local draws for --secret-file: --key is
// an ordinary input an agent may fill, so an oct JWK in it was the HMAC
// secret an agent is never to be invited to supply, one JSON wrapper away. A
// lone oct key is refused, pointing at where a secret goes; one in a set is
// skipped with a note, since the rest of the set is what an issuer serves.
func jwkCandidates(raw string, s plugin.Surface) ([]candidate, []string, *view.Error) {
	doc, err := decodeObject([]byte(raw))
	if err != nil {
		return nil, nil, view.Errorf("codec.jwt.key", "the key is not valid JSON: %v", err)
	}
	if doc.str("kty") == "oct" {
		return nil, nil, view.Errorf("codec.jwt.key", "the key is a shared secret (kty oct), and --key takes only public keys").
			WithHint(secretHint(s))
	}
	var keys []object
	if list, set := doc.values["keys"].([]any); set {
		for _, item := range list {
			if o, ok := asObject(item); ok {
				keys = append(keys, o)
			}
		}
	} else {
		keys = []object{doc}
	}
	var out []candidate
	var notes []string
	for i, o := range keys {
		k := readJWK(o)
		label := k.describe()
		if k.kid != "" {
			label += ", kid " + quote(k.kid)
		} else if len(keys) > 1 {
			label += fmt.Sprintf(", key %d of the set", i+1)
		}
		if k.kty == "oct" {
			notes = append(notes, fmt.Sprintf("The key set's %s is a shared secret (kty oct), which --key does not "+
				"take, so it was not tried: %s.", label, secretHint(s)))
			continue
		}
		if k.pub != nil {
			out = append(out, candidate{pub: k.pub, kid: k.kid, alg: k.alg, use: k.use, label: label})
		}
	}
	if len(out) == 0 {
		return nil, nil, view.Errorf("codec.jwt.key", "no usable key in what was given").
			WithHint("`rta codec jwk` says what is wrong with each one")
	}
	return out, notes, nil
}

// pemCandidates reads every PEM block in raw. Newlines are restored first: a
// key pasted into a one-line box, or through a shell that joined its lines,
// arrives with its header, body and footer run together.
func pemCandidates(raw string) ([]candidate, *view.Error) {
	rest := []byte(repairPEM(raw))
	var out []candidate
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		pub, err := publicFromPEM(block)
		if err != nil {
			return nil, view.Errorf("codec.jwt.key", "reading the %s block: %v", strings.ToLower(block.Type), err)
		}
		if pub != nil {
			out = append(out, candidate{pub: pub, label: describePublic(pub) + " from PEM"})
		}
	}
	if len(out) == 0 {
		return nil, view.Errorf("codec.jwt.key", "no public key, private key or certificate in the PEM given")
	}
	return out, nil
}

// repairPEM puts back the line breaks a PEM block needs around its body when
// they were lost, and leaves an intact one alone.
func repairPEM(s string) string {
	if strings.Contains(s, "\n") {
		return s
	}
	var b strings.Builder
	for {
		begin := strings.Index(s, "-----BEGIN ")
		if begin < 0 {
			break
		}
		headEnd := strings.Index(s[begin+11:], "-----")
		if headEnd < 0 {
			break
		}
		head := s[begin : begin+11+headEnd+5]
		kind := strings.TrimSuffix(strings.TrimPrefix(head, "-----BEGIN "), "-----")
		foot := "-----END " + kind + "-----"
		end := strings.Index(s, foot)
		if end < 0 {
			break
		}
		body := strings.Join(strings.Fields(s[begin+len(head):end]), "")
		b.WriteString(head + "\n" + body + "\n" + foot + "\n")
		s = s[end+len(foot):]
	}
	return b.String()
}

func publicFromPEM(block *pem.Block) (crypto.PublicKey, error) {
	switch block.Type {
	case "CERTIFICATE":
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		return cert.PublicKey, nil
	case "PUBLIC KEY":
		return x509.ParsePKIXPublicKey(block.Bytes)
	case "RSA PUBLIC KEY":
		return x509.ParsePKCS1PublicKey(block.Bytes)
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		if signer, ok := key.(crypto.Signer); ok {
			return signer.Public(), nil
		}
		return nil, errors.New("not a signing key")
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		return key.Public(), nil
	case "EC PRIVATE KEY":
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		return key.Public(), nil
	}
	return nil, nil // a block of another kind — EC PARAMETERS beside a key — is not a key
}

func describePublic(pub crypto.PublicKey) string {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return fmt.Sprintf("%d-bit RSA", k.N.BitLen())
	case *ecdsa.PublicKey:
		return "EC " + k.Curve.Params().Name
	case ed25519.PublicKey:
		return "Ed25519"
	}
	return fmt.Sprintf("%T", pub)
}

// octSecret is an oct JWK's k, for the one place that uses it as a secret:
// a secret file holding one.
func octSecret(o object) []byte {
	raw, _, err := decodeSegment(o.str("k"))
	if err != nil || len(raw) == 0 {
		return nil
	}
	return raw
}

// jwsAlg is one JWS algorithm (RFC 7518 §3.1, RFC 8037 §3.1, RFC 9864 §2.2):
// the hash it signs over and the key type that may check it.
type jwsAlg struct {
	hash  crypto.Hash
	check func(pub crypto.PublicKey, secret, input, sig []byte, h crypto.Hash) (bool, string)
	kind  string
}

var jwsAlgs = map[string]jwsAlg{
	"HS256": {crypto.SHA256, checkHMAC, "a shared secret"},
	"HS384": {crypto.SHA384, checkHMAC, "a shared secret"},
	"HS512": {crypto.SHA512, checkHMAC, "a shared secret"},
	"RS256": {crypto.SHA256, checkPKCS1, "an RSA key"},
	"RS384": {crypto.SHA384, checkPKCS1, "an RSA key"},
	"RS512": {crypto.SHA512, checkPKCS1, "an RSA key"},
	"PS256": {crypto.SHA256, checkPSS, "an RSA key"},
	"PS384": {crypto.SHA384, checkPSS, "an RSA key"},
	"PS512": {crypto.SHA512, checkPSS, "an RSA key"},
	"ES256": {crypto.SHA256, checkECDSA, "a P-256 key"},
	"ES384": {crypto.SHA384, checkECDSA, "a P-384 key"},
	"ES512": {crypto.SHA512, checkECDSA, "a P-521 key"},
	// RFC 8037's EdDSA names the family; RFC 9864 deprecates it for the
	// fully-specified Ed25519. Both mean the same check with an Ed25519 key.
	"EdDSA":   {0, checkEd25519, "an Ed25519 key"},
	"Ed25519": {0, checkEd25519, "an Ed25519 key"},
}

// ecCurveFor is the one curve each ECDSA algorithm is defined over (RFC 7518
// §3.4): an ES256 signature from a P-384 key is not an ES256 signature.
var ecCurveFor = map[string]string{"ES256": "P-256", "ES384": "P-384", "ES512": "P-521"}

// fits reports whether a key may check a signature made with alg, and why
// not when it may not. This is the algorithm-confusion guard: the key's type
// decides, and a public key never stands in for an HMAC secret.
func (c candidate) fits(alg string) (bool, string) {
	a := jwsAlgs[alg]
	switch {
	case c.use == "enc":
		return false, c.label + " is for encryption (use enc)"
	case c.alg != "" && c.alg != alg:
		return false, c.label + " is declared for " + visible(c.alg)
	case a.kind == "a shared secret":
		return c.secret != nil, c.label + " is not a shared secret"
	case c.secret != nil:
		return false, c.label + " is a shared secret"
	}
	switch k := c.pub.(type) {
	case *rsa.PublicKey:
		return a.kind == "an RSA key", c.label + " is not " + a.kind
	case *ecdsa.PublicKey:
		return k.Curve.Params().Name == ecCurveFor[alg], c.label + " is not " + a.kind
	case ed25519.PublicKey:
		return a.kind == "an Ed25519 key", c.label + " is not " + a.kind
	}
	return false, c.label + " is not " + a.kind
}

func checkHMAC(_ crypto.PublicKey, secret, input, sig []byte, h crypto.Hash) (bool, string) {
	var newHash func() hash.Hash
	switch h {
	case crypto.SHA256:
		newHash = sha256.New
	case crypto.SHA384:
		newHash = sha512.New384
	default:
		newHash = sha512.New
	}
	mac := hmac.New(newHash, secret)
	mac.Write(input)
	return hmac.Equal(mac.Sum(nil), sig), ""
}

func digest(h crypto.Hash, input []byte) []byte {
	w := h.New()
	w.Write(input)
	return w.Sum(nil)
}

func checkPKCS1(pub crypto.PublicKey, _, input, sig []byte, h crypto.Hash) (bool, string) {
	return rsa.VerifyPKCS1v15(pub.(*rsa.PublicKey), h, digest(h, input), sig) == nil, ""
}

// checkPSS uses the salt length RFC 7518 §3.5 fixes — the hash's own size —
// rather than accepting any, so a signature this calls good is one every
// conforming verifier calls good.
func checkPSS(pub crypto.PublicKey, _, input, sig []byte, h crypto.Hash) (bool, string) {
	err := rsa.VerifyPSS(pub.(*rsa.PublicKey), h, digest(h, input), sig,
		&rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	return err == nil, ""
}

// checkECDSA reads the signature as RFC 7518 §3.4 lays it out: R and S, each
// the curve's size, concatenated. The most common way to get this wrong is to
// hand over the ASN.1 DER form other ECDSA APIs produce, and it is named as
// such, because "does not match" would send somebody looking for a wrong key.
func checkECDSA(pub crypto.PublicKey, _, input, sig []byte, h crypto.Hash) (bool, string) {
	k := pub.(*ecdsa.PublicKey)
	size := (k.Curve.Params().BitSize + 7) / 8
	if len(sig) != 2*size {
		var der struct{ R, S *big.Int }
		if rest, err := asn1.Unmarshal(sig, &der); err == nil && len(rest) == 0 {
			return false, "the signature is ASN.1 DER, where RFC 7518 §3.4 requires R and S concatenated"
		}
		return false, fmt.Sprintf("the signature is %d bytes, and %s takes %d", len(sig), k.Curve.Params().Name, 2*size)
	}
	r, s := new(big.Int).SetBytes(sig[:size]), new(big.Int).SetBytes(sig[size:])
	return ecdsa.Verify(k, digest(h, input), r, s), ""
}

func checkEd25519(pub crypto.PublicKey, _, input, sig []byte, _ crypto.Hash) (bool, string) {
	return ed25519.Verify(pub.(ed25519.PublicKey), input, sig), ""
}

// verifier checks one signature with what the person supplied. It is built
// once per call and handed to every signature the token carries.
type verifier struct {
	keys   []candidate
	secret *candidate
	// notes are what reading the key material noticed, for the page.
	notes   []string
	surface plugin.Surface
}

// check verifies one signature and returns the verdict that leads the
// verification section, or the reason it cannot be given.
func (v verifier) check(header object, input string, sigSeg string) (string, *view.Error) {
	alg := header.str("alg")
	switch {
	case strings.EqualFold(alg, "none"):
		// Even with a signature segment present: alg none says there is
		// nothing to check, and a verifier that checked anyway would be
		// deciding the algorithm itself.
		return "", view.Errorf("codec.jwt.unsigned", "there is no signature to verify: the header says alg %s", quote(alg)).
			WithHint("an unsigned token cannot be verified, only refused")
	case sigSeg == "":
		return "", view.Errorf("codec.jwt.unsigned", "there is no signature to verify: the signature segment is empty").
			WithHint("an unsigned token cannot be verified, only refused")
	}
	a, known := jwsAlgs[alg]
	if !known {
		return "", view.Errorf("codec.jwt.alg", "rta cannot check a %s signature", quote(alg)).
			WithHint("it checks HS, RS, PS and ES at 256, 384 and 512, and EdDSA with Ed25519")
	}
	sig, _, err := decodeSegment(sigSeg)
	if err != nil {
		return "", view.Errorf("codec.jwt.invalid", "decoding the signature: %v", err)
	}
	candidates := v.keys
	if v.secret != nil {
		candidates = append([]candidate{*v.secret}, candidates...)
	}
	if a.kind == "a shared secret" && !anySecret(candidates) {
		return "", view.Errorf("codec.jwt.alg", "an %s signature is made with a shared secret, and only a public key was given", alg).
			WithHint(secretHint(v.surface) + " — a public key checking an HMAC signature is the algorithm-confusion attack, so it is never tried")
	}

	kid := header.str("kid")
	var reasons []string
	tried, triedKey := 0, false
	for _, c := range candidates {
		if kid != "" && c.kid != "" && c.kid != kid {
			continue
		}
		if ok, why := c.fits(alg); !ok {
			reasons = append(reasons, why)
			continue
		}
		tried++
		triedKey = triedKey || c.secret == nil
		good, why := a.check(c.pub, c.secret, []byte(input), sig, a.hash)
		if good {
			return verifiedLine(c, alg), nil
		}
		if why != "" {
			reasons = append(reasons, why)
		}
		for _, r := range c.readings {
			if good, _ := a.check(nil, r.secret, []byte(input), sig, a.hash); good {
				return verifiedLine(candidate{label: r.label}, alg), nil
			}
		}
	}
	switch {
	case tried > 0:
		msg := fmt.Sprintf("the %s signature does not match %s", alg, triedWhat(tried, candidates, kid))
		if len(reasons) > 0 {
			msg += ": " + strings.Join(reasons, "; ")
		}
		// Worded from what was tried: a secret has no kid, and an HMAC
		// token rarely names one, so the key's hint sent somebody looking
		// for a kid that neither side has.
		hint := "the token was changed after it was signed, or signed with another secret — check the secret file: " +
			"it is read as it is, without a final line break, and as base64"
		if triedKey {
			hint = "the token was changed after it was signed, or signed with another key — without --key it decodes to show which kid it names"
		}
		return "", view.Errorf("codec.jwt.signature", "%s", msg).WithHint(hint)
	// Only keys that carry kids can be missing the right one: a PEM key or a
	// secret has none, and blaming a kid for them hides the real mismatch.
	case kid != "" && anyKid(candidates, "") && !anyKid(candidates, kid):
		return "", view.Errorf("codec.jwt.nokey", "no key given has kid %s, the one the header names", quote(kid)).
			WithHint("the issuer may have rotated its keys — fetch its current key set; `rta codec jwk` lists the kids in one")
	}
	return "", view.Errorf("codec.jwt.nokey", "no key given can check an %s signature: %s", alg, strings.Join(reasons, "; ")).
		WithHint("an " + alg + " signature needs " + a.kind)
}

func anySecret(cs []candidate) bool {
	for _, c := range cs {
		if c.secret != nil {
			return true
		}
	}
	return false
}

// anyKid reports whether a candidate carries kid, or, given "", any kid at
// all.
func anyKid(cs []candidate, kid string) bool {
	for _, c := range cs {
		if c.kid != "" && (kid == "" || c.kid == kid) {
			return true
		}
	}
	return false
}

func triedWhat(tried int, cs []candidate, kid string) string {
	if tried == 1 {
		for _, c := range cs {
			if kid == "" || c.kid == "" || c.kid == kid {
				return c.label
			}
		}
	}
	return fmt.Sprintf("any of the %d keys that could check it", tried)
}

func decodeAnyBase64(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if raw, err := enc.DecodeString(s); err == nil {
			return raw, nil
		}
	}
	return nil, errors.New("not base64")
}

// verifiedLine is the verdict for a signature that checked. It says exactly
// what was proven and no more: that the signer holds this key. Whether that
// key is the issuer's is a question about where the key came from, which
// nothing in the token can answer.
func verifiedLine(c candidate, alg string) string {
	return "VERIFIED — the " + alg + " signature matches " + c.label + ". That proves whoever signed it holds " +
		"that key; whether the key is the issuer you trust depends on where you got it, and the claims are " +
		"still the token's own word about itself."
}
