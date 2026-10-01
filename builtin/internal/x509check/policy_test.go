package x509check

import (
	"crypto/x509"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A chain Apple's verifier refused for its validity-period rule says the
// rule and its fix beside the system's words, which were all its reader got
// ("not standards compliant"), and names no CA file. Only where that
// verifier answers: elsewhere Go's own checks make no such rule.
func TestAChainRefusedForItsValidityPeriodSaysTheRule(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Apple's verifier is the one that holds a certificate to its validity period")
	}
	open, closing := string(rune(0x201c)), string(rune(0x201d))
	issued := time.Date(2025, time.March, 1, 0, 0, 0, 0, time.UTC)
	leaf := &x509.Certificate{NotBefore: issued, NotAfter: issued.Add(3650 * 24 * time.Hour)}
	verdict := func(words string) error {
		return fmt.Errorf("x509: %s", open+"lab.internal"+closing+" "+words)
	}

	refused := verdict("certificate is not standards compliant")
	got := invalid([]*x509.Certificate{leaf}, refused)
	if !strings.HasPrefix(got, "INVALID: "+refused.Error()) || !strings.Contains(got, "at most 825 days") {
		t.Errorf("a ten-year certificate refused by the validity rule: %q, want the system's words and the rule", got)
	}
	if strings.Contains(got, "CA file") {
		t.Errorf("%q points at a CA file, which goes around the system's checks", got)
	}

	untrusted := verdict("certificate is not trusted")
	if got := invalid([]*x509.Certificate{leaf}, untrusted); got != "INVALID: "+untrusted.Error() {
		t.Errorf("another refusal: %q, want the system's words alone", got)
	}
}
