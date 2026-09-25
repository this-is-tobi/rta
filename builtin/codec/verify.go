package codec

import (
	"context"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"os"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"

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
	// ops is the JWK's key_ops, and problems what codec.jwk would note about
	// it. Both used to stay behind in readJWK: a key limited to encrypt
	// verified signatures, and one whose x5c holds a different key — the
	// state two verifiers disagree about, since some take x5c over n and e —
	// gave a bare VERIFIED.
	ops      []string
	problems []string
	// private is set for a JWK that holds its private half, whose key_ops
	// says what that half may do.
	private bool
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
	defer func() { _ = f.Close() }()
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
	text := asText(raw)
	// Whitespace alone is no secret either. `echo "$SECRET" > f` with SECRET
	// unset writes one line break, and that byte was the HMAC key: a token
	// made with it VERIFIED, and a real one was told it had been changed
	// after it was signed.
	if trimText(text) == "" {
		return candidate{}, view.Errorf("codec.jwt.secret", "the secret file %s holds only whitespace", quote(path)).
			WithHint(`a file written with echo "$VAR" while VAR is unset holds a line break and nothing else`)
	}
	// Every reading the file may be taken in is checked, not only the bytes
	// as they are: DER with a line break after it parses as no key at all,
	// and the reading without the break is the key, which then checked an
	// HS256 token forged with it. And the file as the text it was saved as,
	// which is where a key saved with a byte-order mark or in UTF-16 is one.
	trimmed := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
	for _, b := range []string{raw, trimmed, text} {
		if what := publicKeyIn([]byte(b)); what != "" {
			return candidate{}, keyAsSecret(path, what)
		}
	}
	if decoded, derr := decodeAnyBase64(strings.Join(strings.Fields(trimText(text)), "")); derr == nil {
		if what := publicKeyIn(decoded); what != "" {
			return candidate{}, keyAsSecret(path, what+", in base64")
		}
	}
	// An oct JWK is how a shared secret is written as a key, and --key sends
	// one here, so its k is the secret rather than the JSON around it.
	//
	// And what it declares about itself comes with it, as it does with a
	// key given to --key: left behind, an AES key wrap key, or one declared
	// for HS512, gave a bare VERIFIED on an HS256 token that a library
	// honouring the JWK refuses. Private, as a secret is, so key_ops
	// ["sign"] admits the check.
	//
	// A key set is refused rather than chosen from. Its JSON text was the
	// HMAC key, so a token made with the set's own k was told it did not
	// match and had been changed after it was signed. A set holding a public
	// key was refused above; this one holds shared secrets, and the file
	// takes the one the token was signed with.
	doc, err := decodeObject([]byte(trimText(text)))
	switch {
	case err == nil && doc.str("kty") == "oct":
		secret := octSecret(doc)
		if secret == nil {
			return candidate{}, view.Errorf("codec.jwt.secret", "the oct key in %s has no k that decodes", quote(path))
		}
		k := readJWK(doc)
		return candidate{secret: secret, label: "the oct key in " + quote(path), use: k.use, alg: k.alg, ops: k.ops,
			problems: k.problems, private: true}, nil
	case err == nil && doc.has("keys"):
		return candidate{}, view.Errorf("codec.jwt.secret", "the secret file %s holds a key set, and it takes one shared secret",
			quote(path)).
			WithHint(`put the one oct key the token was signed with in the file, as {"kty":"oct","k":…}, and not the set`)
	}
	c := candidate{secret: []byte(raw), label: "the secret in " + quote(path)}
	if trimmed != raw {
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
	s := trimText(string(b))
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
	// The leading element alone: x509 refuses DER with anything after it,
	// and a key saved with two line breaks or a space after it was no key
	// to this guard and the HMAC secret to the check.
	der := b
	var element asn1.RawValue
	if _, err := asn1.Unmarshal(b, &element); err == nil {
		der = element.FullBytes
	}
	if _, err := x509.ParsePKIXPublicKey(der); err == nil {
		return "a DER public key"
	}
	if _, err := x509.ParsePKCS1PublicKey(der); err == nil {
		return "a DER public key"
	}
	if _, err := x509.ParseCertificate(der); err == nil {
		return "a DER certificate"
	}
	return ""
}

// byteOrderMark is U+FEFF in UTF-8, which some editors write at the start of
// a file and strings.TrimSpace does not take off.
const byteOrderMark = string(rune(0xfeff))

// trimText takes off what surrounds a key pasted or saved as text: white
// space, and a byte-order mark.
func trimText(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == 0xfeff })
}

// asText is the secret file as the text an editor or a shell saved: without
// a UTF-8 byte-order mark, and decoded when its mark says it is UTF-16. Older
// Notepad puts the mark in front, and `>` in Windows PowerShell 5.1 writes
// UTF-16, and a public key saved either way is text no parser takes for a key:
// it was HMAC bytes to the check, and VERIFIED a token forged with them. An odd
// length is not UTF-16 whatever its first two bytes say, and is left as it is.
func asText(raw string) string {
	b := []byte(raw)
	var order binary.ByteOrder
	switch {
	case strings.HasPrefix(raw, byteOrderMark):
		return raw[len(byteOrderMark):]
	case len(b)%2 != 0 || len(b) < 2:
		return raw
	case b[0] == 0xff && b[1] == 0xfe:
		order = binary.LittleEndian
	case b[0] == 0xfe && b[1] == 0xff:
		order = binary.BigEndian
	default:
		return raw
	}
	units := make([]uint16, 0, len(b)/2-1)
	for i := 2; i < len(b); i += 2 {
		units = append(units, order.Uint16(b[i:]))
	}
	return string(utf16.Decode(units))
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
	// With its byte-order mark, a key set was "not a JWK, a key set or PEM",
	// told to go to --secret-file if it was a shared secret — where the same
	// file then verified an HS256 token forged with it.
	raw = trimText(raw)
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
		// A raw public key — an Ed25519 x, an EC point — is bytes nothing
		// can tell from a random secret of the same length, so the secret
		// file's guard cannot catch it and this hint is all there is. Sent
		// to the secret, a token HMAC'd with the key's bytes verified.
		return nil, nil, view.Errorf("codec.jwt.key", "the key is not a JWK, a key set or PEM").
			WithHint(`pass the issuer's key set as it is served, or a PEM public key or certificate; a raw public key ` +
				`goes in as a JWK, {"kty":"OKP","crv":"Ed25519","x":…} or kty EC with crv, x and y, and never as a ` +
				`shared secret, since anyone holding a public key can sign with it`)
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

// octSetHint is secretHint for a set of shared secrets given to --key. A
// secret file takes the one key and not the set, and the hint sent the whole
// set there, where its JSON text was the HMAC key.
func octSetHint(s plugin.Surface) string {
	switch s {
	case plugin.SurfaceMCP:
		return secretHint(s)
	case plugin.SurfaceTUI:
		return `name a file holding the one oct key, {"kty":"oct","k":…} and not the set, in the secret-file box`
	}
	return `pass a file holding the one oct key, {"kty":"oct","k":…} and not the set, with --secret-file`
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
	var notes, secrets []string
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
			secrets = append(secrets, label+" is a shared secret (kty oct), which --key does not take")
			continue
		}
		// A key the members do not make stays a candidate, with nothing to
		// check against and its problems for a reason. Dropped, a key under
		// the token's own kid was reported as missing, with the hint that
		// the issuer had rotated its keys — about a key in the set, whose
		// producer had trimmed a leading zero from a coordinate, as about
		// one key in 128 comes out when a producer does.
		out = append(out, candidate{pub: k.pub, kid: k.kid, alg: k.alg, use: k.use, ops: k.ops,
			problems: k.problems, private: k.private, label: label})
	}
	var unusable []string
	for _, c := range out {
		if c.pub != nil {
			return out, notes, nil
		}
		unusable = append(unusable, c.unusable())
	}
	// Worded from what the set held. Every refusal used to list the keys
	// the members do not make, and a set of shared secrets alone, or of
	// nothing, listed none: "no usable key in what was given: ", with the
	// note saying where a secret goes dropped for a hint to run codec.jwk.
	switch {
	case len(out) == 0 && len(secrets) > 0:
		return nil, nil, view.Errorf("codec.jwt.key", "the key set holds only shared secrets (kty oct), and --key takes only public keys").
			WithHint(octSetHint(s))
	case len(out) == 0:
		return nil, nil, view.Errorf("codec.jwt.key", "the key set holds no keys")
	}
	unusable = append(unusable, secrets...)
	return nil, nil, view.Errorf("codec.jwt.key", "no usable key in what was given: %s", strings.Join(unusable, "; ")).
		WithHint("`rta codec jwk` says what is wrong with each one")
}

// pemCandidates reads every PEM block in raw. Newlines are restored first: a
// key pasted into a one-line box, or through a shell that joined its lines,
// arrives with its header, body and footer run together.
//
// A private key this cannot open is passed over rather than refusing the
// rest, and named only when nothing else in the PEM can be used. A server.pem
// is a certificate beside its encrypted key, and refused whole it verified
// nothing, in either order, where the certificate alone verifies. A block
// that does not parse still refuses the PEM: that is a paste gone wrong, and
// the key the person meant may be the one it cut.
func pemCandidates(raw string) ([]candidate, *view.Error) {
	rest := []byte(repairPEM(raw))
	var out []candidate
	var sealed []string
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		pub, err := publicFromPEM(block)
		switch {
		case errors.Is(err, errEncryptedKey), errors.Is(err, errOpenSSHKey):
			sealed = append(sealed, fmt.Sprintf("reading the %s block: %v", strings.ToLower(block.Type), err))
			continue
		case err != nil:
			return nil, view.Errorf("codec.jwt.key", "reading the %s block: %v", strings.ToLower(block.Type), err)
		}
		if pub != nil {
			out = append(out, candidate{pub: pub, label: describePublic(pub) + " from PEM"})
		}
	}
	if len(out) == 0 && len(sealed) > 0 {
		return nil, view.Errorf("codec.jwt.key", "%s", strings.Join(sealed, "; "))
	}
	if len(out) == 0 {
		return nil, view.Errorf("codec.jwt.key", "no public key, private key or certificate in the PEM given").
			WithHint("the blocks read are PUBLIC KEY, RSA PUBLIC KEY, CERTIFICATE, and a private key that is not encrypted")
	}
	return out, nil
}

// repairPEM puts back the line breaks a PEM block needs around its body when
// they were lost, and leaves an intact one alone.
//
// The footer is looked for only after the header. Searched for from the
// start, it was found before the header in a paste that begins at the tail
// of the block before it, and inside the header's own closing dashes in an
// empty block, `-----BEGIN PUBLIC KEY-----END PUBLIC KEY-----`; the body was
// then sliced backwards, and the one-line paste this exists for crashed the
// CLI and the TUI.
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
		after := begin + len(head)
		rel := strings.Index(s[after:], foot)
		if rel < 0 {
			break
		}
		end := after + rel
		body := strings.Join(strings.Fields(s[after:end]), "")
		b.WriteString(head + "\n" + body + "\n" + foot + "\n")
		s = s[end+len(foot):]
	}
	return b.String()
}

// publicFromPEM is the public key a PEM block holds, nil for a block that is
// not a key at all, or an error saying why the key cannot be read. An
// encrypted key or an OpenSSH one used to fall through as "not a key", and
// the refusal said the PEM held no private key when it held one this cannot
// open.
func publicFromPEM(block *pem.Block) (crypto.PublicKey, error) {
	if strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") {
		return nil, errEncryptedKey
	}
	switch block.Type {
	case "ENCRYPTED PRIVATE KEY":
		return nil, errEncryptedKey
	case "OPENSSH PRIVATE KEY":
		return nil, errOpenSSHKey
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

var (
	errEncryptedKey = errors.New("it is encrypted: decrypt it first, or pass its public key " +
		"(`openssl pkey -in <key> -pubout` asks for the passphrase and prints it)")
	errOpenSSHKey = errors.New("it is in OpenSSH's own format: `ssh-keygen -e -m PKCS8 -f <key>` prints its public key as PEM")
)

// describePublic names a key for a sentence. ParsePKIXPublicKey also returns
// X25519 and DSA keys, which no JWS algorithm here checks, and they were
// named by their Go type: "*ecdh.PublicKey from PEM is not an Ed25519 key".
func describePublic(pub crypto.PublicKey) string {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return fmt.Sprintf("%d-bit RSA", k.N.BitLen())
	case *ecdsa.PublicKey:
		return "EC " + k.Curve.Params().Name
	case ed25519.PublicKey:
		return "Ed25519"
	case *ecdh.PublicKey:
		return fmt.Sprint(k.Curve())
	}
	// By its type's name rather than a case: crypto/dsa is deprecated, and
	// importing it only to name its key would be the one use of it here.
	if fmt.Sprintf("%T", pub) == "*dsa.PublicKey" {
		return "DSA"
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

// maxRSABits is the largest RSA modulus a signature is checked against:
// OpenSSL's own ceiling, OPENSSL_RSA_MAX_MODULUS_BITS, so no key a real
// verifier accepts is lost. crypto/rsa sets none, and a check costs the
// square of the modulus: a key of a million bits, 350 KB of arguments to a
// free call, held a CPU core for 22 seconds, and the 4 MiB an MCP request may
// carry buys over half an hour. Refused by name in fits and in readJWK, so
// the refusal comes before any arithmetic.
const maxRSABits = 16384

// maxKeyChecks bounds the public-key checks one call makes, and
// maxSignatures the signatures of a JSON JWS that are checked at all. Every
// key that fits is tried against every signature, so the cost is their
// product whatever the size of each key: 200 keys without a kid against 200
// signatures, 290 KB of input, took 13 seconds. A token names the kid it was
// signed under, which leaves one key to try, and no issuer serves a set, or
// signs a JWS, that comes near either bound.
const (
	maxKeyChecks  = 32
	maxSignatures = 16
)

// fits reports whether a key may check a signature made with alg, and why
// not when it may not. This is the algorithm-confusion guard: the key's type
// decides, and a public key never stands in for an HMAC secret.
func (c candidate) fits(alg string) (bool, string) {
	a := jwsAlgs[alg]
	switch {
	case c.pub == nil && c.secret == nil:
		return false, c.unusable()
	case c.use == "enc":
		return false, c.label + " is for encryption (use enc)"
	case !c.opsAllowVerify():
		return false, c.label + " is limited by key_ops to " + visible(strings.Join(c.ops, ", "))
	case c.alg != "" && c.alg != alg:
		return false, c.label + " is declared for " + visible(c.alg)
	case a.kind == "a shared secret":
		return c.secret != nil, c.label + " is not a shared secret"
	case c.secret != nil:
		return false, c.label + " is a shared secret"
	}
	switch k := c.pub.(type) {
	// Refused by name before the check, because Go refuses both keys inside
	// it and the refusal came back as a plain mismatch: a correct signature
	// from a 512-bit key read as a token changed after it was signed, while
	// the real news was a key anyone can factor.
	case *rsa.PublicKey:
		switch {
		case a.kind != "an RSA key":
			return false, c.label + " is not " + a.kind
		case k.N.Bit(0) == 0:
			return false, c.label + " has an even modulus, which no RSA key has, so no signature verifies against it"
		case k.N.BitLen() < 1024:
			return false, c.label + " is under the 1024 bits a verifier accepts, and RFC 7518 §3.3 requires 2048"
		case k.N.BitLen() > maxRSABits:
			return false, fmt.Sprintf("%s is over the %d bits a verifier accepts", c.label, maxRSABits)
		}
		return true, ""
	case *ecdsa.PublicKey:
		return k.Curve.Params().Name == ecCurveFor[alg], c.label + " is not " + a.kind
	case ed25519.PublicKey:
		return a.kind == "an Ed25519 key", c.label + " is not " + a.kind
	}
	return false, c.label + " is not " + a.kind
}

// opsAllowVerify reports whether the key's key_ops, which RFC 7517 §4.3 has
// say what a key may do as use does, lets it check a signature. A private
// key's key_ops says what its private half may do, and WebCrypto exports
// every ECDSA and RSA signing key with ["sign"] alone: the public half of a
// key allowed to sign checks exactly the signatures it makes, and refusing it
// broke the promise keysFrom makes to take a private key.
func (c candidate) opsAllowVerify() bool {
	return len(c.ops) == 0 || slices.Contains(c.ops, "verify") || c.private && slices.Contains(c.ops, "sign")
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
	return rsaVerdict(rsa.VerifyPKCS1v15(pub.(*rsa.PublicKey), h, digest(h, input), sig))
}

// rsaVerdict keeps what crypto/rsa says about the key rather than only that
// the check failed: every error but ErrVerification is a refusal of the key,
// not a signature that does not match it.
func rsaVerdict(err error) (bool, string) {
	if err == nil || errors.Is(err, rsa.ErrVerification) {
		return err == nil, ""
	}
	return false, "crypto/rsa refuses the key: " + strings.TrimPrefix(err.Error(), "crypto/rsa: ")
}

// checkPSS uses the salt length RFC 7518 §3.5 fixes — the hash's own size —
// rather than accepting any, so a signature this calls good is one every
// conforming verifier calls good.
func checkPSS(pub crypto.PublicKey, _, input, sig []byte, h crypto.Hash) (bool, string) {
	return rsaVerdict(rsa.VerifyPSS(pub.(*rsa.PublicKey), h, digest(h, input), sig,
		&rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}))
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
	// ctx is the call's, looked at between keys: a caller that has gone —
	// an agent that timed out, a cancelled MCP request — used to leave the
	// checks running to the end.
	ctx    context.Context
	keys   []candidate
	secret *candidate
	// notes are what reading the key material noticed, for the page.
	notes   []string
	surface plugin.Surface
	// keyChecks counts the public-key checks made so far in the call, every
	// signature's together, against maxKeyChecks.
	keyChecks int
}

// check verifies one signature and returns the verdict that leads the
// verification section, or the reason it cannot be given.
func (v *verifier) check(header object, input string, sigSeg string) (string, *view.Error) {
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

	// What was tried and what was skipped are kept apart. One list used to
	// hold both, and a mismatch against the one key tried was worded with
	// the first key the kid filter let through, which was often a skipped
	// one: "does not match the secret given: the secret given is a shared
	// secret", about an RS256 token checked against an RSA key.
	kid := header.str("kid")
	var tried, reasons, skipped []string
	triedKey := false
	for _, c := range candidates {
		if kid != "" && c.kid != "" && c.kid != kid {
			continue
		}
		if ok, why := c.fits(alg); !ok {
			skipped = append(skipped, why)
			continue
		}
		if err := v.ctx.Err(); err != nil {
			return "", view.Errorf("codec.jwt.cancelled", "the signature check was stopped before it finished: %v", err)
		}
		// An HMAC costs what the input does, and is not counted.
		if c.secret == nil {
			if v.keyChecks == maxKeyChecks {
				return "", view.Errorf("codec.jwt.key", "checking this takes more than the %d key checks one call makes: "+
					"every key given that fits is tried against every signature", maxKeyChecks).
					WithHint("give the key the token was signed with — its kid is in the header, and `rta codec jwk` lists the kids in a set")
			}
			v.keyChecks++
		}
		tried = append(tried, c.label)
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
				return verifiedLine(candidate{label: r.label, secret: r.secret}, alg), nil
			}
		}
	}
	switch {
	case len(tried) > 0:
		what := tried[0]
		if len(tried) > 1 {
			what = fmt.Sprintf("any of the %d keys that could check it", len(tried))
		}
		msg := fmt.Sprintf("the %s signature does not match %s", alg, what)
		if len(reasons) > 0 {
			msg += ": " + strings.Join(reasons, "; ")
		}
		if len(skipped) > 0 {
			msg += " (not tried: " + strings.Join(skipped, "; ") + ")"
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
	case kid != "" && onlyUnusable(candidates, kid):
		var why []string
		for _, c := range candidates {
			if c.kid == kid {
				why = append(why, c.unusable())
			}
		}
		return "", view.Errorf("codec.jwt.key", "the key given under kid %s, the one the header names, cannot check "+
			"anything: %s", quote(kid), strings.Join(why, "; ")).
			WithHint("the key is in the set and its producer wrote it wrongly; `rta codec jwk` says the same of it")
	// Only keys that carry kids can be missing the right one: a PEM key or a
	// secret has none, and blaming a kid for them hides the real mismatch.
	case kid != "" && anyKid(candidates, "") && !anyKid(candidates, kid):
		return "", view.Errorf("codec.jwt.nokey", "no key given has kid %s, the one the header names", quote(kid)).
			WithHint("the issuer may have rotated its keys — fetch its current key set; `rta codec jwk` lists the kids in one")
	}
	need := a.kind
	if need == "an RSA key" {
		need += " of 2048 bits or more (RFC 7518 §3.3)"
	}
	return "", view.Errorf("codec.jwt.nokey", "no key given can check an %s signature: %s", alg, strings.Join(skipped, "; ")).
		WithHint("an " + alg + " signature needs " + need)
}

// unusable says why a key the members do not make cannot check anything.
func (c candidate) unusable() string {
	why := "its members do not make a key"
	if len(c.problems) > 0 {
		why = strings.Join(c.problems, ", ")
	}
	return c.label + " cannot be used: " + why
}

// onlyUnusable reports whether kid names a key given and every key it names
// is one the members do not make.
func onlyUnusable(cs []candidate, kid string) bool {
	found := false
	for _, c := range cs {
		if c.kid == kid {
			if c.pub != nil || c.secret != nil {
				return false
			}
			found = true
		}
	}
	return found
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
//
// What is wrong with the key goes in the same paragraph, since it qualifies
// the verdict rather than the token.
func verifiedLine(c candidate, alg string) string {
	line := "VERIFIED — the " + alg + " signature matches " + c.label + ". That proves whoever signed it holds " +
		"that key; whether the key is the issuer you trust depends on where you got it, and the claims are " +
		"still the token's own word about itself."
	if len(c.problems) > 0 {
		line += " About that key: " + strings.Join(c.problems, "; ") + "."
	}
	// RFC 7518 §3.2 requires an HMAC key at least as long as its hash, and a
	// strict library refuses a shorter one: somebody asking why theirs
	// rejects a token was told VERIFIED and nothing else.
	if a := jwsAlgs[alg]; a.kind == "a shared secret" && len(c.secret) < a.hash.Size() {
		line += fmt.Sprintf(" The secret is %s, under the %d RFC 7518 §3.2 requires of an %s key, and a strict "+
			"library refuses it.", format.CountOf(len(c.secret), "byte"), a.hash.Size(), alg)
	}
	return line
}
