package pkg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The module proxy answers @latest for a module, and the package a binary
// was built from is usually not one: govulncheck is
// golang.org/x/vuln/cmd/govulncheck inside golang.org/x/vuln. Asked with
// the package path the proxy answered 404 — verified against the real one
// — which read as "not found" and never as outdated, so every tool laid
// out under cmd/ was reported current forever. The upgrade command the row
// prints is the other half: `go install` wants the package path, and the
// row used to print the binary's name there, which `go install` refuses.
func TestAGoToolBuiltFromACmdPackageIsCheckedAgainstItsModule(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if r.URL.Path == "/golang.org/x/vuln/@latest" {
			_, _ = w.Write([]byte(`{"Version":"v1.8.0"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := newRegistryClient()
	c.gomod = srv.URL

	gopath := t.TempDir()
	f := &fake{bins: map[string]bool{"go": true}, answers: map[string]fakeAnswer{
		"go env GOBIN":  {out: "\n"},
		"go env GOPATH": {out: gopath + "\n"},
	}}
	install(t, f)
	if err := os.MkdirAll(filepath.Join(gopath, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(gopath, "bin", "govulncheck")
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.answers["go version -m "+bin] = fakeAnswer{
		out: "govulncheck: go1.27\n\tpath\tgolang.org/x/vuln/cmd/govulncheck\n\tmod\tgolang.org/x/vuln\tv1.7.0\th1:abc\n"}

	l := collect(context.Background(), c, "go")
	if len(l.failed) != 0 {
		t.Fatalf("failed: %v", l.failed)
	}
	if len(l.rows) != 1 || l.rows[0].Name != "govulncheck" || l.rows[0].Current != "v1.7.0" || l.rows[0].Latest != "v1.8.0" {
		t.Fatalf("rows = %+v, want govulncheck v1.7.0 behind v1.8.0", l.rows)
	}
	for _, p := range asked {
		if strings.Contains(p, "/cmd/") {
			t.Errorf("the proxy was asked for the package path rather than the module: %s", p)
		}
	}
	if got := outdatedTable(l).Rows[0][5]; got != "go install golang.org/x/vuln/cmd/govulncheck@latest" {
		t.Errorf("upgrade column = %q, want the package path go install takes", got)
	}
}
