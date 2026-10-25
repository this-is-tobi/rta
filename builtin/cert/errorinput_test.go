package cert

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// refusedInput is the code of err and the input it names, held to the
// declaration of the capability that answered it: a name the capability does
// not declare is one no surface acts on (plugin.Capability.ErrorInput), so it
// fails here rather than passing as a stamp that does nothing.
func refusedInput(t *testing.T, id string, err error) (code, input string) {
	t.Helper()
	verr := view.AsError(err, "test")
	if verr == nil {
		t.Fatalf("%s answered no error", id)
	}
	if verr.Input != "" {
		var declared bool
		for _, c := range Plugin().Capabilities {
			if c.ID == id {
				_, declared = c.ErrorInput(verr)
			}
		}
		if !declared {
			t.Errorf("%s: %s names the input %q, which %s does not declare", id, verr.Code, verr.Input, id)
		}
	}
	return verr.Code, verr.Input
}

// A refusal of the one input a call got wrong names it, so the form the call
// came from opens again on that box and an agent knows the argument to send
// again: a target that is no file, no host or no certificate, an include the
// chain cannot answer, an out that cannot be written. A failure that is not
// the value's — a host that does not answer — names none.
func TestARefusalOfOneInputNamesIt(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	leaf := write("leaf.pem", string(selfSigned(t, "leaf")))
	cut := write("cut.pem", "-----BEGIN CERTIFICATE-----\nMIIB\n")
	garbled := write("garbled.pem", "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n")
	notes := write("notes.txt", "not a certificate\n")
	big := write("big.pem", "")
	if err := os.Truncate(big, maxPEMBytes+1); err != nil {
		t.Fatal(err)
	}
	certs := filepath.Join(dir, "certs")
	if err := os.Mkdir(certs, 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	_ = ln.Close()
	missing := filepath.Join(dir, "missing.pem")

	runs := map[string]func(context.Context, plugin.Request) (view.View, error){
		"cert.inspect": runInspect, "cert.chain": runChain, "cert.pem": runPEM, "cert.tls": runTLS,
	}
	for _, c := range []struct {
		name, id string
		values   map[string]any
		surface  plugin.Surface
		code     string
		input    string
	}{
		{"a file that is not there", "cert.inspect", map[string]any{"target": missing},
			plugin.SurfaceCLI, "cert.file.notfound", "target"},
		{"over MCP, a file that is not there", "cert.chain", map[string]any{"target": missing},
			plugin.SurfaceMCP, "cert.file.notfound", "target"},
		{"a URL with no host", "cert.inspect", map[string]any{"target": "https://"},
			plugin.SurfaceCLI, "cert.target.invalid", "target"},
		{"a URL of another scheme", "cert.chain", map[string]any{"target": "ftp://example.invalid"},
			plugin.SurfaceCLI, "cert.target.invalid", "target"},
		{"an address that does not split", "cert.inspect", map[string]any{"target": "a:b:c"},
			plugin.SurfaceCLI, "cert.target.invalid", "target"},
		{"a file name given a port", "cert.inspect", map[string]any{"target": "leaf.pem:443"},
			plugin.SurfaceCLI, "cert.target.notahost", "target"},
		{"a directory", "cert.inspect", map[string]any{"target": certs},
			plugin.SurfaceTUI, "cert.file.notafile", "target"},
		{"a file past any bundle's size", "cert.inspect", map[string]any{"target": big},
			plugin.SurfaceCLI, "cert.file.toolarge", "target"},
		{"a bundle cut off inside a block", "cert.chain", map[string]any{"target": cut},
			plugin.SurfaceCLI, "cert.file.truncated", "target"},
		{"a block that is no certificate", "cert.pem", map[string]any{"target": garbled},
			plugin.SurfaceCLI, "cert.parse.failed", "target"},
		{"a file holding no certificate", "cert.inspect", map[string]any{"target": notes},
			plugin.SurfaceCLI, "cert.file.empty", "target"},
		{"a file where a handshake is needed", "cert.tls", map[string]any{"target": leaf},
			plugin.SurfaceTUI, "cert.tls.filetarget", "target"},
		{"issuers of a chain of one", "cert.pem", map[string]any{"target": leaf, "include": "issuers"},
			plugin.SurfaceTUI, "cert.chain.leafonly", "include"},
		{"an include there is none of", "cert.pem", map[string]any{"target": leaf, "include": "everything"},
			plugin.SurfaceCLI, "cert.include.invalid", "include"},
		{"out naming a directory", "cert.pem", map[string]any{"target": leaf, "out": certs},
			plugin.SurfaceTUI, "cert.out.isdir", "out"},
		{"out under a file", "cert.pem", map[string]any{"target": leaf, "out": filepath.Join(leaf, "chain.pem")},
			plugin.SurfaceCLI, "cert.out.unwritable", "out"},
		{"a host that refuses the connection", "cert.inspect", map[string]any{"target": closed, "timeout": "2s"},
			plugin.SurfaceCLI, "cert.dial.failed", ""},
	} {
		_, err := runs[c.id](context.Background(), req(c.values).WithSurface(c.surface))
		code, input := refusedInput(t, c.id, err)
		if code != c.code || input != c.input {
			t.Errorf("%s: %s naming %q, want %s naming %q (%v)", c.name, code, input, c.code, c.input, err)
		}
	}
}

// cert.expiry turns what is wrong with each host into its row, and its own
// failure at a terminal is about all of them: it names no input, since no one
// box holds a list's fix, and the target the row is about is not an input of
// its own.
func TestExpiryNamesNoInputForAHostItCouldNotCheck(t *testing.T) {
	_, err := runExpiry(context.Background(), req(map[string]any{
		"targets": []string{"ftp://example.invalid"}, "warn-days": 30, "timeout": "2s",
	}).WithSurface(plugin.SurfaceCLI))
	if code, input := refusedInput(t, "cert.expiry", err); code != "cert.expiry.failed" || input != "" {
		t.Errorf("an unreachable host at a terminal: %s naming %q, want cert.expiry.failed naming none", code, input)
	}
}
