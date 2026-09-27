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
		// url.Parse, which net/http reads the variable with, splits the
		// userinfo at the last `@`, so a raw `@` in a password works — and
		// cut at the first one, the rest of the password was printed.
		{"password with a raw @", "http://bob:s3cr@tpass@proxy.corp:3128", "tpass"},
		{"schemeless password with a raw @", "bob:P@ss@proxy.corp:3128", "ss"},
		// A `://` further along is in the path, not the end of a scheme: net/http
		// reads this as bob with s3cret, and taking the first `://` for the
		// scheme's split the value after the credential.
		{"schemeless, with :// in the path", "bob:s3cret@proxy.corp:3128/x://y", "s3cret"},
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

// A raw `/`, `?` or `#` in a password ends the authority where url.Parse
// looks for it — and the userinfo scan, reading the same authority, found no
// `@` in it and printed the value whole. Whether url.Parse then refuses the
// value (`bob:s3/…`, whose port is not a number) or reads it as a proxy at
// bob:2024 with a path, the password in it is the operator's, and net.info
// promises to mask it: the value is masked up to its last `@`.
func TestAPasswordHoldingARawSlashQuestionMarkOrHashIsMasked(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"a slash in the password", "http://bob:s3/cr3t@proxy.corp:3128"},
		{"a question mark in the password", "http://bob:s3?cr3t@proxy.corp:3128"},
		{"a hash in the password", "https://bob:s3#cr3t@proxy.corp:3128"},
		{"schemeless, a slash in the password", "bob:s3/cr3t@proxy.corp:3128"},
		{"schemeless, a hash in the password", "bob:s3#cr3t@proxy.corp:3128"},
		// url.Parse reads each of these, as a proxy at bob on a port with a
		// path, a query or a fragment after it and no userinfo at all.
		{"digits, then a slash", "http://bob:2024/s3cr3t@proxy.corp:3128"},
		{"schemeless, digits, then a slash", "bob:2024/s3cr3t@proxy.corp:3128"},
		{"digits, then a question mark", "http://bob:8080?s3cr3t@proxy.corp:3128"},
		{"digits, then a hash", "socks5://bob:8080#s3cr3t@proxy.corp:3128"},
		{"a slash first", "http://bob:/s3cr3t@proxy.corp:3128"},
		// "bob:x" is no scheme — a scheme has no colon — so the `://` after
		// it is inside the password, and taking it for a scheme's printed
		// the username and the password's first letter as one.
		{"a :// in the password", "bob:x://s3cr3t@proxy.corp:3128"},
		{"a raw @ and a slash in the password", "http://bob:s3@c:r/3t@proxy.corp:3128"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := maskProxy(tc.in)
			if contains(got, "bob") || contains(got, "s3") || contains(got, "cr3t") || contains(got, "3t@") {
				t.Fatalf("credential survived: %q -> %q", tc.in, got)
			}
			if !contains(got, "***@proxy.corp:3128") {
				t.Fatalf("not masked up to the host: %q -> %q", tc.in, got)
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

// An `@` past the authority cannot be told from one after a password holding
// a raw `/`, `?` or `#` — `http://proxy.corp:3128/path@x` has the shape of
// `http://bob:2024/s3cr3t@proxy.corp:3128` — and a proxy's path, query and
// fragment are never read, so it is masked up to like any other: the host
// is lost to the mask in a value that has one, and no password is shown in
// a value that looks like one. The first `@` was once read anywhere, and
// masked the host of a value holding two while it printed the rest.
func TestAnAtSignPastTheAuthorityIsMaskedUpTo(t *testing.T) {
	for in, want := range map[string]string{
		"http://proxy.corp:3128/path@notuserinfo":   "http://***@notuserinfo",
		"http://proxy.corp:3128?who=a@b":            "http://***@b",
		"http://proxy.corp:3128#a@b":                "http://***@b",
		"proxy.corp:3128/a://b@c":                   "***@c",
		"http://bob:s3cr@tpass@proxy.corp:3128/a@b": "http://***@b",
	} {
		if got := maskProxy(in); got != want {
			t.Errorf("%q became %q, want %q", in, got, want)
		}
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
