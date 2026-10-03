package mcp

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/paths"
)

// The probes are open, so whoever can reach the address reads the reason
// readiness fails with, and a reason that names the data directory is the
// layout of the server's state for every one of them. It says what is wrong
// with the storage to the person describing the pod, who knows where it is,
// and nothing more to anyone else.
func TestTheReadinessReasonDoesNotNameTheDataDirectory(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	dir := paths.Data()
	h := observe(t, ObserveConfig{Ready: func() error {
		return errors.New("the data directory " + dir + " is not writable: read-only file system")
	}})

	for _, path := range []string{"/readyz", "/healthz"} {
		res := get(t, h, path, "")
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("%s answered %d, want 503", path, res.StatusCode)
		}
		if strings.Contains(string(body), dir) {
			t.Errorf("%s names the data directory: %s", path, body)
		}
		if !strings.Contains(string(body), "<data dir> is not writable") {
			t.Errorf("%s no longer says what is wrong: %s", path, body)
		}
	}
}
