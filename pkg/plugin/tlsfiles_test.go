package plugin

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// caRequest is a request whose ca-file setting is path, on the CLI.
func caRequest(path string) Request {
	return NewRequest(map[string]any{"ca-file": path}, false, false).WithSurface(SurfaceCLI)
}

// An empty setting is the system's roots, and a PEM certificate, a ~ path
// included, is a pool holding it.
func TestCAPoolReadsTheCertificateTheSettingNames(t *testing.T) {
	if pool, verr := CAPool(caRequest(""), "x", "ca-file"); pool != nil || verr != nil {
		t.Fatalf("empty setting: %v, %v", pool, verr)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	cert, _ := issue(t, nil, nil, time.Hour, true)
	if err := os.WriteFile(filepath.Join(home, "ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, verr := CAPool(caRequest("~/ca.pem"), "x", "ca-file")
	if verr != nil {
		t.Fatal(verr)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, DNSName: "db.internal", CurrentTime: time.Now()}); err != nil {
		t.Fatalf("the pool does not hold the certificate: %v", err)
	}
}

// A file that does not read is refused under the namespace's code, with the
// setting as the input to correct when the path is what is wrong; one that
// holds no certificate is refused as invalid.
func TestCAPoolRefusesWhatIsNoCertificate(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.pem")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxTLSFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	for _, tc := range []struct {
		name, path, code, input, says string
	}{
		{"nothing there", filepath.Join(dir, "none.pem"), "x.tls.ca.unreadable", "ca-file", "no such file"},
		{"a directory", dir, "x.tls.ca.unreadable", "ca-file", "is a directory"},
		{"past the cap", big, "x.tls.ca.unreadable", "", "larger than"},
		{"a private key", keyFile, "x.tls.ca.invalid", "ca-file", "holds no PEM certificate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, verr := CAPool(caRequest(tc.path), "x", "ca-file")
			if verr == nil || verr.Code != tc.code || verr.Input != tc.input || !strings.Contains(verr.Message, tc.says) {
				t.Fatalf("got %+v, want %s on %q saying %q", verr, tc.code, tc.input, tc.says)
			}
			if !strings.Contains(verr.Hint, "--ca-file") {
				t.Errorf("hint %q does not name the setting", verr.Hint)
			}
		})
	}
}

// A named pipe where the CA file was is refused at once, not waited on: the
// setting is read before anything is dialled, where no timeout runs yet.
func TestCAPoolDoesNotWaitOnAPipe(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "ca.pem")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		_, verr := CAPool(caRequest(fifo), "x", "ca-file")
		if verr == nil {
			done <- "accepted"
			return
		}
		done <- verr.Code + ": " + verr.Message
	}()
	select {
	case got := <-done:
		if !strings.HasPrefix(got, "x.tls.ca.unreadable: ") || !strings.Contains(got, "not a regular file") {
			t.Fatalf("got %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("CAPool waited on a named pipe")
	}
}
