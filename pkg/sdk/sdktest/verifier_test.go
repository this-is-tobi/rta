package sdktest

import (
	"crypto/x509"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The diagnostics a plugin asks of a handshake's error read the system's own
// verifier, so a revoked certificate and one Apple's policy refuses were
// reachable only on a Mac. Injected, the system's verdict reaches the public
// entry points from any machine CI runs on, in the words it comes in, and the
// machine's own system is back once the test is over.
func TestVerifierSystemInjectsTheSystemsVerdict(t *testing.T) {
	day := 24 * time.Hour
	issued := time.Date(2025, time.March, 1, 0, 0, 0, 0, time.UTC)
	tenYears := &x509.Certificate{NotBefore: issued, NotAfter: issued.Add(3650 * day)}

	t.Run("on a mac", func(t *testing.T) {
		VerifierSystem(t, "darwin")
		revoked := SystemVerdict("db.internal", VerdictRevoked)
		if !plugin.CertRevoked(revoked) || plugin.CertUntrusted(revoked) {
			t.Errorf("a revoked verdict: revoked %v, untrusted %v, want true and false",
				plugin.CertRevoked(revoked), plugin.CertUntrusted(revoked))
		}
		if !plugin.CertUntrusted(SystemVerdict("db.internal", VerdictNotTrusted)) {
			t.Error("a not-trusted verdict is not read as untrusted")
		}
		hint := plugin.CertPolicyHint(SystemVerdict("db.internal", VerdictNotStandardsCompliant, tenYears))
		if !strings.Contains(hint, "825 days") || strings.Contains(strings.ToLower(hint), "ca file") {
			t.Errorf("policy hint = %q, want the validity period named and no CA file", hint)
		}
		if got := plugin.SurfaceCLI.CAHint("ca-file"); !strings.Contains(got, "macOS makes") {
			t.Errorf("CAHint = %q, want it to say what macOS checks", got)
		}
	})

	t.Run("on any other system the same errors are no verdict", func(t *testing.T) {
		VerifierSystem(t, "linux")
		if plugin.CertRevoked(SystemVerdict("db.internal", VerdictRevoked)) ||
			plugin.CertPolicyHint(SystemVerdict("db.internal", VerdictNotStandardsCompliant, tenYears)) != "" {
			t.Error("a verdict worded as Apple's was read as one on linux")
		}
	})

	// The two subtests have ended: the machine's own system is read again.
	want := runtime.GOOS == "darwin" || runtime.GOOS == "ios"
	if got := plugin.CertRevoked(SystemVerdict("db.internal", VerdictRevoked)); got != want {
		t.Errorf("after the tests, a revoked verdict reads as %v on %s, want %v", got, runtime.GOOS, want)
	}
}
