package codec

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// codec.jwt and codec.jwk are free reads: any agent may hand them any bytes,
// and several thousand lines of JOSE, JWK and PEM parsing sit between those
// bytes and the page. What they promise, held against arbitrary input: no
// panic — the CLI and the TUI recover none, and a one-line PEM whose footer
// came first crashed both — and every refusal is a *view.Error under the
// capability's own code, which is what the caller is told to act on.
func FuzzJWT(f *testing.F) {
	a1 := "eyJ0eXAiOiJKV1QiLA0KICJhbGciOiJIUzI1NiJ9." +
		"eyJpc3MiOiJqb2UiLA0KICJleHAiOjEzMDA4MTkzODAsDQogImh0dHA6Ly9leGFtcGxlLmNvbS9pc19yb290Ijp0cnVlfQ." +
		"dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	rs := seg(`{"alg":"RS256","kid":"2011-04-29"}`) + "." + seg(`{"sub":"a"}`) + "." + seg("sig")
	for _, s := range []struct{ token, key string }{
		{a1, ""}, {rs, rfc7638Key}, {rs, `{"keys":[` + rfc7638Key + `]}`}, {rs, rfc8037Public},
		// The crashers: a one-line PEM whose footer is found before the
		// end of its header, inside the header's own dashes or ahead of it.
		{"a.b.c", "-----BEGIN PUBLIC KEY-----END PUBLIC KEY-----"},
		{"a.b.c", "-----BEGIN CERTIFICATE-----END CERTIFICATE-----"},
		{"a.b.c", "-----END PUBLIC KEY----- -----BEGIN PUBLIC KEY----- AAAA"},
		{rs, "AB -----END PUBLIC KEY----- -----BEGIN PUBLIC KEY----- MIIB -----END PUBLIC KEY-----"},
		{rs, "-----BEGIN PUBLIC KEY-----\nMIIB\n-----END PUBLIC KEY-----"},
		{`{"payload":"e30","signatures":[{"protected":"eyJhbGciOiJIUzI1NiJ9","signature":"c2ln"}]}`, rfc8037Public},
		{`{"payload":"e30","protected":"eyJhbGciOiJFUzI1NiJ9","signature":""}`, ""},
		{seg(`{"alg":"dir","enc":"A128GCM"}`) + "..aXY.Yw.dGFn", ""},
		{"", ""}, {".", "{"}, {"{", "-----BEGIN "},
	} {
		f.Add(s.token, s.key)
	}
	f.Fuzz(func(t *testing.T, token, key string) {
		values := map[string]any{"token": token}
		if key != "" {
			values["key"] = key
		}
		_, err := runJWT(context.Background(), req(values))
		refusedByName(t, err, "codec.jwt.")
	})
}

func FuzzJWK(f *testing.F) {
	for _, key := range []string{
		rfc7638Key, rfc8037Public, rfc8037Private, `{"keys":[` + rfc7638Key + `,` + rfc8037Public + `]}`,
		`{"kty":"oct","k":"c2VjcmV0"}`, `{"kty":"EC","crv":"P-256","x":"","y":""}`, `{"kty":"RSA","n":"AQ","e":"AQ"}`,
		"-----BEGIN PUBLIC KEY-----END PUBLIC KEY-----", "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----",
		"a.b.c", "", "{", `{"keys":{}}`,
	} {
		f.Add(key)
	}
	f.Fuzz(func(t *testing.T, key string) {
		_, err := runJWK(context.Background(), req(map[string]any{"key": key}))
		refusedByName(t, err, "codec.jwk.")
	})
}

func refusedByName(t *testing.T, err error, prefix string) {
	t.Helper()
	if err == nil {
		return
	}
	var verr *view.Error
	if !errors.As(err, &verr) || !strings.HasPrefix(verr.Code, prefix) {
		t.Fatalf("refused with %T %v, want a *view.Error coded %s…", err, err, prefix)
	}
}
