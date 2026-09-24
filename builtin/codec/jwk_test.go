package codec

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// RFC 7638 §3.1's example key and the thumbprint the RFC gives for it: the
// independent answer this implementation has to reproduce, since two parties
// computing one key's thumbprint differently is the failure the RFC exists to
// prevent.
const (
	rfc7638Key        = `{"kty":"RSA","n":"0vx7agoebGcQSuuPiLJXZptN9nndrQmbXEps2aiAFbWhM78LhWx4cbbfAAtVT86zwu1RK7aPFFxuhDR1L6tSoc_BJECPebWKRXjBZCiFV4n3oknjhMstn64tZ_2W-5JsGY4Hc5n9yBXArwl93lqt7_RN5w6Cf0h4QyQ5v-65YGjQR0_FDW2QvzqY368QQMicAtaSqzs8KJZgnYb9c7d0zgdAZHzu6qMQvRL5hajrn1n91CbOpbISD08qNLyrdkt-bFTWhAI4vMQFh6WeZu0fM4lFd2NcRwr3XPksINHaQ-G_xBniIqbw0Ls1jF44-csFCur-kEgU8awapJzKnqDKgw","e":"AQAB","alg":"RS256","kid":"2011-04-29"}`
	rfc7638Thumbprint = "NzbLsXh8uDCcd-6MNwXF4W_7noWXFZAfHkxZsRGC9Xs"

	// RFC 8037 Appendix A.2 and A.3.
	rfc8037Public     = `{"kty":"OKP","crv":"Ed25519","x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"}`
	rfc8037Private    = `{"kty":"OKP","crv":"Ed25519","d":"nWGxne_9WmC6hEr0kuwsxERJxWl7MmkZcDusAxyuf2A","x":"11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"}`
	rfc8037Thumbprint = "kPrK_qmxVWaYVA9wwBF6Iuo3vVzz7TxHCTwXBygrS4k"
)

func jwk(t *testing.T, key string) view.Sections {
	t.Helper()
	v, err := runJWK(context.Background(), req(map[string]any{"key": key}))
	if err != nil {
		t.Fatalf("reading %s: %v", key, err)
	}
	return v.(view.Sections)
}

func notes(s view.Sections) string {
	if v, ok := findSection(s, "notes"); ok {
		return v.(view.Text).Body
	}
	return ""
}

func TestTheThumbprintIsTheOneTheRFCsGive(t *testing.T) {
	for _, tc := range []struct{ key, kind, thumbprint string }{
		{rfc7638Key, "2048-bit RSA", rfc7638Thumbprint},
		{rfc8037Public, "Ed25519", rfc8037Thumbprint},
	} {
		key := section(t, jwk(t, tc.key), "key").(view.KeyValue)
		if got := pairValue(key, "type"); got != tc.kind {
			t.Errorf("type = %q, want %q", got, tc.kind)
		}
		if got := pairValue(key, "thumbprint"); !strings.HasPrefix(got, tc.thumbprint+" ") {
			t.Errorf("thumbprint = %q, want %s", got, tc.thumbprint)
		}
		if got := pairValue(key, "private"); got != "no" {
			t.Errorf("private = %q, want no", got)
		}
	}
}

// A private key is named as one, and never printed: the whole point of
// pasting it here is to find out, and the answer must not leak it further.
func TestAPrivateKeyIsNamedAndNeverPrinted(t *testing.T) {
	s := jwk(t, rfc8037Private)
	if got := pairValue(section(t, s, "key").(view.KeyValue), "private"); !strings.HasPrefix(got, "yes") {
		t.Errorf("private = %q", got)
	}
	var printed []string
	view.MapStrings(s, func(v string) string { printed = append(printed, v); return v })
	if joined := strings.Join(printed, "\n"); strings.Contains(joined, "nWGxne_9WmC6hEr0kuwsxERJxWl7MmkZcDusAxyuf2A") {
		t.Errorf("the private member was printed:\n%s", joined)
	}

	shared := section(t, jwk(t, `{"kty":"oct","k":"`+seg(strings.Repeat("s", 32))+`"}`), "key").(view.KeyValue)
	if got := pairValue(shared, "type"); got != "256-bit shared secret" {
		t.Errorf("oct type = %q", got)
	}
	if got := pairValue(shared, "private"); !strings.Contains(got, "can sign as its issuer") {
		t.Errorf("oct private = %q", got)
	}
}

// An RSA key's primes give its signing key away as surely as d does, and a
// JWK publishing p and q without d was reported as holding nothing private.
func TestAnRSAKeyWithItsPrimesIsPrivate(t *testing.T) {
	key := rsaKey()
	b := func(n *big.Int) string { return base64.RawURLEncoding.EncodeToString(n.Bytes()) }
	primes := fmt.Sprintf(`{"kty":"RSA","n":%q,"e":"AQAB","p":%q,"q":%q}`, b(key.N), b(key.Primes[0]), b(key.Primes[1]))
	if got := pairValue(section(t, jwk(t, primes), "key").(view.KeyValue), "private"); !strings.HasPrefix(got, "yes — it holds the private key (p, q)") {
		t.Errorf("private = %q, want the primes named", got)
	}
	if n := notes(jwk(t, `{"keys":[`+primes+`]}`)); !strings.Contains(n, "(p, q)") {
		t.Errorf("key set notes = %q, want the private key named", n)
	}
}

// A shared secret's RFC 7638 thumbprint is an unsalted hash of the secret,
// and a dictionary recovers a chosen one from it — `hunter2` here. It pins
// nothing either, so it is not printed, alone or in a set.
func TestASharedSecretHasNoThumbprintPrinted(t *testing.T) {
	oct := `{"kty":"oct","k":"aHVudGVyMg"}`
	const hash = "JAWo9jT4QrQgM1qv0-BHyY_bVAyApQ_peTR12J-XpL0"
	s := jwk(t, oct)
	if got := pairValue(section(t, s, "key").(view.KeyValue), "thumbprint"); !strings.HasPrefix(got, "not shown") {
		t.Errorf("thumbprint = %q, want it withheld", got)
	}
	set := jwk(t, `{"keys":[`+oct+`]}`)
	var printed []string
	for _, page := range []view.Sections{s, set} {
		view.MapStrings(page, func(v string) string { printed = append(printed, v); return v })
	}
	if joined := strings.Join(printed, "\n"); strings.Contains(joined, hash) {
		t.Errorf("the secret's hash was printed:\n%s", joined)
	}
}

func ecKey(t *testing.T) (*ecdsa.PrivateKey, string, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	point, err := priv.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return priv, base64.RawURLEncoding.EncodeToString(point[1:33]), base64.RawURLEncoding.EncodeToString(point[33:])
}

// A point off its curve verifies nothing, and a coordinate with its leading
// zeros trimmed is read differently by different libraries. Both are facts
// about the key that only computing them reveals.
func TestAnECKeyIsCheckedAsAPoint(t *testing.T) {
	_, x, y := ecKey(t)
	good := fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q}`, x, y)
	if n := notes(jwk(t, good)); n != "" {
		t.Errorf("a valid key drew notes: %q", n)
	}
	offCurve := fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q}`, x, x)
	if n := notes(jwk(t, offCurve)); !strings.Contains(n, "not a point on P-256") {
		t.Errorf("off-curve notes = %q", n)
	}
	// 32 bytes leave two bits of a coordinate's last character unused.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	loose := x[:len(x)-1] + string(alphabet[strings.IndexByte(alphabet, x[len(x)-1])^1])
	if n := notes(jwk(t, fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q}`, loose, y))); !strings.Contains(n, "Its x ends in a character whose unused bits") {
		t.Errorf("non-canonical notes = %q", n)
	}
	short := fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q}`, seg(strings.Repeat("x", 31)), y)
	if n := notes(jwk(t, short)); !strings.Contains(n, "31 and 32 bytes, and P-256 takes 32 each") {
		t.Errorf("short-coordinate notes = %q", n)
	}
}

// codec.jwt refuses these keys with "`rta codec jwk` says what is wrong with
// each one", and codec.jwk said nothing about any of them. An X key declared
// for encryption is what it should be, and draws nothing.
func TestAKeyRTACannotVerifyWithSaysWhy(t *testing.T) {
	for want, key := range map[string]string{
		"key-agreement key": fmt.Sprintf(`{"kty":"OKP","crv":"X25519","x":%q}`, seg(strings.Repeat("\x09", 32))),
		"Ed448":             fmt.Sprintf(`{"kty":"OKP","crv":"Ed448","x":%q}`, seg(strings.Repeat("e", 57))),
		"secp256k1":         fmt.Sprintf(`{"kty":"EC","crv":"secp256k1","x":%q,"y":%q}`, seg(strings.Repeat("x", 32)), seg(strings.Repeat("y", 32))),
	} {
		if n := notes(jwk(t, key)); !strings.Contains(n, want) {
			t.Errorf("%s: notes = %q, want the reason named", key, n)
		}
	}
	enc := fmt.Sprintf(`{"kty":"OKP","crv":"X25519","use":"enc","x":%q}`, seg(strings.Repeat("\x09", 32)))
	if n := notes(jwk(t, enc)); n != "" {
		t.Errorf("an X25519 key for encryption drew notes: %q", n)
	}
}

func certFor(t *testing.T, priv *ecdsa.PrivateKey) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "signing.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// A chain that disagrees with the key beside it is the misconfiguration a
// verifier trusting either half would never report.
func TestACertificateChainIsCheckedAgainstItsKey(t *testing.T) {
	priv, x, y := ecKey(t)
	der := certFor(t, priv)
	sum := sha256.Sum256(der)
	matching := fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q,"x5c":[%q],"x5t#S256":%q}`,
		x, y, base64.StdEncoding.EncodeToString(der), base64.RawURLEncoding.EncodeToString(sum[:]))
	s := jwk(t, matching)
	if got := pairValue(section(t, s, "certificate").(view.KeyValue), "subject"); got != "CN=signing.example" {
		t.Errorf("subject = %q", got)
	}
	if n := notes(s); n != "" {
		t.Errorf("a consistent key drew notes: %q", n)
	}

	// Padding lost is named as that, and the URL alphabet as that. An ECDSA
	// signature varies in length, so the certificate is made until its
	// encoding is padded and holds a character the alphabets spell apart.
	padded := der
	for i := 0; len(padded)%3 == 0 || !strings.ContainsAny(base64.StdEncoding.EncodeToString(padded), "+/"); i++ {
		if i == 100 {
			t.Fatal("no certificate whose encoding is padded and differs between the alphabets")
		}
		padded = certFor(t, priv)
	}
	for want, x5c := range map[string]string{
		"x5c is unpadded":  strings.TrimRight(base64.StdEncoding.EncodeToString(padded), "="),
		"x5c is base64url": base64.RawURLEncoding.EncodeToString(padded),
	} {
		key := fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q,"x5c":[%q]}`, x, y, x5c)
		if n := notes(jwk(t, key)); !strings.Contains(n, want) {
			t.Errorf("notes = %q, want %q", n, want)
		}
	}

	other, _, _ := ecKey(t)
	mismatched := fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q,"x5c":[%q],"x5t":"wrong"}`,
		x, y, base64.StdEncoding.EncodeToString(certFor(t, other)))
	n := notes(jwk(t, mismatched))
	for _, want := range []string{"holds a different key", "x5t is not the thumbprint"} {
		if !strings.Contains(n, want) {
			t.Errorf("notes = %q, want %q", n, want)
		}
	}
}

// A key set as an issuer serves it: one row per key, and what a person
// debugging "kid not found" or a leaked signing key needs said out loud.
//
// Two RSA keys under one kid are ambiguous; an RSA and an EC key under one
// kid are the alternatives RFC 7517 §4.5 itself gives as the legitimate case,
// so only the first is named.
func TestAKeySetListsEveryKeyAndNamesWhatIsWrongWithIt(t *testing.T) {
	_, x, y := ecKey(t)
	secondRSA := strings.Replace(rfc7638Key, `"alg":"RS256"`, `"alg":"RS384"`, 1)
	set := fmt.Sprintf(`{"keys":[%s,{"kty":"EC","crv":"P-256","x":%q,"y":%q,"kid":"2011-04-29","use":"sig"},%s,%s,"junk"]}`,
		rfc7638Key, x, y, secondRSA, rfc8037Private)
	s := jwk(t, set)
	table := section(t, s, "keys").(view.Table)
	if table.Total != 4 {
		t.Errorf("rows = %d, want 4 (the junk entry is a note, not a row)", table.Total)
	}
	if table.Rows[0][4] != rfc7638Thumbprint {
		t.Errorf("first thumbprint = %q", table.Rows[0][4])
	}
	if len(table.Columns) != 6 {
		t.Errorf("columns = %v, want no certificate column when no key has a chain", table.Columns)
	}
	n := notes(s)
	for _, want := range []string{
		`Kid "2011-04-29" names 2 RSA keys`,
		"Key 4: it holds the private key",
		"Key 5 is not a JSON object",
	} {
		if !strings.Contains(n, want) {
			t.Errorf("notes = %q, want %q", n, want)
		}
	}
	if strings.Contains(n, "EC keys") {
		t.Errorf("notes = %q, flagged an RSA and an EC key sharing a kid", n)
	}
}

// A single key's repeated member was named and a key set's was not: a
// parser that keeps the first keys member reads an empty set here, and one
// that keeps the last reads a key.
func TestAKeySetThatNamesAMemberTwiceIsNamed(t *testing.T) {
	_, x, y := ecKey(t)
	key := fmt.Sprintf(`{"kty":"EC","crv":"P-256","x":%q,"y":%q}`, x, y)
	for want, set := range map[string]string{
		"keys":        `{"keys":[],"keys":[` + key + `]}`,
		"keys[0].kty": `{"keys":[` + strings.Replace(key, `"kty":"EC"`, `"kty":"RSA","kty":"EC"`, 1) + `]}`,
	} {
		if n := notes(jwk(t, set)); !strings.Contains(n, "The key set names "+want+" more than once") {
			t.Errorf("%s: notes = %q, want the repeat named", want, n)
		}
	}
}

func TestSomethingElseIsSentWhereItBelongs(t *testing.T) {
	for input, want := range map[string]string{
		"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----": "rta cert inspect",
		buildJWT(t, `{"alg":"HS256"}`, `{"sub":"a"}`):                  "rta codec jwt",
		`{"iss":"not a key"}`: "kty",
	} {
		_, err := runJWK(context.Background(), req(map[string]any{"key": input}))
		verr := view.AsError(err, "test")
		if err == nil || !strings.Contains(verr.Hint, want) {
			t.Errorf("%.30q: got %v (hint %q), want a hint naming %q", input, err, verr.Hint, want)
		}
	}
}
