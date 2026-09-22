package operator

import (
	"io"
	"strings"
	"testing"
)

// The one spelling both ends sign: lowercased host, no trailing slash, no
// query or fragment. Plaintext is refused anywhere but loopback, because the
// answer — a grant listing decisions get made on — is what an on-path
// attacker could rewrite.
func TestAServerURLIsCanonicalisedTheSameWayOnBothEnds(t *testing.T) {
	for _, tc := range []struct{ raw, want, code string }{
		{"https://RTA.Example.com/api/", "https://rta.example.com/api", ""},
		{"https://rta.example.com/?q=1#frag", "https://rta.example.com", ""},
		{"  https://rta.example.com  ", "https://rta.example.com", ""},
		{"http://localhost:8443/", "http://localhost:8443", ""},
		{"http://127.0.0.1:8443", "http://127.0.0.1:8443", ""},
		{"http://[::1]:8443", "http://[::1]:8443", ""},
		{"http://rta.example.com", "", "core.operator.insecure"},
		{"http://10.0.0.5:8443", "", "core.operator.insecure"},
		{"ftp://rta.example.com", "", "core.operator.insecure"},
		{"https://rta.example.com:not-a-port", "", "core.operator.server"},
	} {
		got, verr := CanonicalServerURL("prod", tc.raw)
		switch {
		case tc.code == "" && (verr != nil || got != tc.want):
			t.Errorf("%q: got %q, %v; want %q", tc.raw, got, verr, tc.want)
		case tc.code != "" && (verr == nil || verr.Code != tc.code):
			t.Errorf("%q: got %q, %v; want refusal %s", tc.raw, got, verr, tc.code)
		}
	}
}

func TestLoopbackIsLocalhostAndTheLoopbackAddresses(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "127.0.0.1": true, "127.1.2.3": true, "::1": true,
		"10.0.0.5": false, "rta.example.com": false, "": false,
	} {
		if got := isLoopback(host); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", host, got, want)
		}
	}
}

// A roster read is a table, not a stream: a hostile server's answer stops at
// eight megabytes rather than becoming a memory bill.
func TestAServersAnswerIsBounded(t *testing.T) {
	big := strings.NewReader(strings.Repeat("x", 9<<20))
	n, err := io.Copy(io.Discard, bounded(big))
	if err != nil || n != 8<<20 {
		t.Errorf("read %d bytes, %v; want exactly 8 MiB", n, err)
	}
}

func TestARosterLabelIsCheckedBeforeItIsPrinted(t *testing.T) {
	if verr := CheckLabel(""); verr == nil || verr.Code != "core.operator.label" {
		t.Errorf("empty label: %v, want core.operator.label", verr)
	}
	if verr := CheckLabel("ops-laptop"); verr != nil {
		t.Errorf("a plain label was refused: %v", verr)
	}
}
