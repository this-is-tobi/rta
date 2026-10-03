package plugin

import (
	"crypto/x509"
	"strings"
	"testing"
)

// Seven plugins worded the refusal for a certificate checked for the end of a
// forward by hand, differing only in the code, the input that names the server
// and what answers it. One helper words it, naming the way on — the name the
// certificate is for, never a mode that checks less — beside the profile that
// holds the forward and not the 127.0.0.1 end that closed with the call.
func TestForwardNameRefusalNamesTheWayOnAndTheProfile(t *testing.T) {
	cert := &x509.Certificate{DNSNames: []string{"db.internal"}}
	hostErr := x509.HostnameError{Certificate: cert, Host: "127.0.0.1"}
	req := NewRequest(nil, false, false).WithProfile("prod", TunnelKube)

	verr := ForwardNameRefusal(req, "pg.tls.forward", "localhost:5432", "server", hostErr)
	if verr.Code != "pg.tls.forward" {
		t.Errorf("code = %q, want the plugin's own", verr.Code)
	}
	want := "the certificate behind profile prod (through its kube: forward) is for db.internal, " +
		"not for 127.0.0.1, where the forward ends"
	if verr.Message != want {
		t.Errorf("message = %q, want %q", verr.Message, want)
	}
	for _, part := range []string{"the name the server answers as", "--tls-server-name", "checked as strictly as the host it replaces"} {
		if !strings.Contains(verr.Hint, part) {
			t.Errorf("hint %q lacks %q", verr.Hint, part)
		}
	}
	for _, bad := range []string{"sslmode", "verify-ca", "ca-file"} {
		if strings.Contains(verr.Hint, bad) {
			t.Errorf("hint %q points at %q, which checks less or opens no forward", verr.Hint, bad)
		}
	}

	direct := ForwardNameRefusal(NewRequest(nil, false, false), "etcd.tls.forward", "etcd.lab:2379", "member", hostErr)
	if !strings.Contains(direct.Message, "behind etcd.lab:2379 is for") || !strings.Contains(direct.Hint, "the member answers as") {
		t.Errorf("without a profile: %q / %q", direct.Message, direct.Hint)
	}
}
