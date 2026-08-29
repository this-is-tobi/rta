package net

import "testing"

// net.info publishes "Proxy credentials are masked" in its own Description.
// The first version leaned on url.Parse, which does not read the schemeless
// form as a URL at all — `bob:s3cret@proxy.corp:3128` parses with scheme
// "bob" and no User, so the guard passed the value through untouched and the
// password went to the screen underneath the promise.
//
// It is not a malformed value: golang.org/x/net/http/httpproxy re-parses a
// schemeless proxy with http:// prepended, so the credential works, and rta's
// own http plugin reaches through it. A value that authenticates is a value
// that has to be masked.
func TestProxyCredentialsAreMaskedInEveryFormThatWorks(t *testing.T) {
	for _, tc := range []struct{ name, in, wantGone string }{
		{"schemeless, the form that leaked", "bob:s3cret@proxy.corp:3128", "s3cret"},
		{"http scheme", "http://bob:s3cret@proxy.corp:3128", "s3cret"},
		{"https scheme", "https://bob:s3cret@proxy.corp:3128", "s3cret"},
		{"socks5", "socks5://bob:s3cret@proxy.corp:1080", "s3cret"},
		{"username only", "http://bob@proxy.corp:3128", "bob"},
		{"schemeless username only", "bob@proxy.corp:3128", "bob"},
		{"password with punctuation", "http://u:p%40ss:word@proxy.corp:3128", "word"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := maskProxy(tc.in)
			if contains(got, tc.wantGone) {
				t.Fatalf("credential survived: %q -> %q", tc.in, got)
			}
			if !contains(got, "***") {
				t.Fatalf("nothing marks the value as masked: %q -> %q", tc.in, got)
			}
			// The host is the useful half and has to stay.
			if !contains(got, "proxy.corp") {
				t.Fatalf("the host was masked too: %q -> %q", tc.in, got)
			}
		})
	}
}

func TestAProxyWithNoCredentialIsLeftAlone(t *testing.T) {
	// Masking that fires on everything hides the answer somebody asked for.
	for _, in := range []string{
		"http://proxy.corp:3128",
		"proxy.corp:3128",
		"https://proxy.internal",
		"socks5://127.0.0.1:1080",
	} {
		if got := maskProxy(in); got != in {
			t.Errorf("%q became %q", in, got)
		}
	}
}

func TestAnAtSignAfterThePathIsNotACredential(t *testing.T) {
	// Reading the first @ anywhere would mask the host instead of a secret.
	in := "http://proxy.corp:3128/path@notuserinfo"
	if got := maskProxy(in); got != in {
		t.Errorf("%q became %q", in, got)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
