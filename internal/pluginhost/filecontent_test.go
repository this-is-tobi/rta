package pluginhost

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A CA the host read for a call, from where a profile says it is kept, reaches
// a plugin's own process as a file: the handler reads the path it was handed,
// finds the certificates in it, is told the file was written for this call,
// and the file is gone once the call returns. End to end, through the wire,
// with no part of it standing in for another.
func TestACertificateTheHostReadReachesAPluginProcessAsAFile(t *testing.T) {
	bin := filepath.Join(t.TempDir(), BinaryName("file"))
	if out, err := exec.Command("go", "build", "-o", bin, "./testdata/fileplugin").CombinedOutput(); err != nil {
		t.Fatalf("building the test plugin: %v\n%s", err, out)
	}
	h := New()
	t.Cleanup(h.CloseAll)
	c, err := h.Open(context.Background(), bin)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Unknown) != 0 || len(c.Declared.Capabilities) != 1 ||
		c.Declared.Capabilities[0].Inputs[0].File != plugin.FileCertificates {
		t.Fatalf("the declaration came back as %+v (unknown %v)", c.Declared.Capabilities, c.Unknown)
	}

	certificate := "-----BEGIN CERTIFICATE-----\nMIIBdemo\n-----END CERTIFICATE-----\n"
	v, err := c.Declared.Capabilities[0].Run(context.Background(), plugin.NewRequest(
		map[string]any{"ca-file": plugin.NewFileContent([]byte(certificate), "kube:pg-ca/ca.crt")}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	path, rest, _ := strings.Cut(v.(view.Text).Body, "\n")
	temporary, content, _ := strings.Cut(rest, "\n")
	if temporary != "true" || content != certificate || filepath.Base(path) != "ca-file.pem" {
		t.Errorf("the handler was handed %q, temporary %s, holding %q", path, temporary, content)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the file outlived the call: %v", err)
	}
}
