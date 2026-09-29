package net

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// underRoot is the request the MCP bridge hands a handler under a root that
// holds none of the files a test points net at.
func underRoot(t *testing.T) func(values map[string]any) plugin.Request {
	t.Helper()
	g, err := pathguard.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return func(values map[string]any) plugin.Request {
		return plugin.NewRequest(values, false, true).WithSurface(plugin.SurfaceMCP).
			WithConfinement(g.Derived).WithBounds(g.Bounds())
	}
}

// The system's own hosts file and resolv.conf lie outside every root, and a
// call that names no file reads them all the same: nobody chose them, and
// reading them is what the capability is for. Under the call's bounds they
// were judged as paths it had reached and refused, so over MCP neither list
// could read the file it lists. A file the caller names is still held to the
// root.
func TestTheSystemFileIsReadUnderARootAndANamedOneIsHeldToIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ")
	}
	hosts := hostsFixture(t, sampleHosts)
	stub := filepath.Join(t.TempDir(), "stub-resolv.conf")
	if err := os.WriteFile(stub, []byte("nameserver 192.0.2.53\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.Symlink(stub, linked); err != nil {
		t.Fatal(err)
	}
	orig := resolvConf
	resolvConf = linked
	t.Cleanup(func() { resolvConf = orig })
	bounded := underRoot(t)
	ctx := context.Background()

	v, err := runHostsList(ctx, bounded(nil))
	if err != nil {
		t.Fatalf("net.hosts.list with no file under a root: %v", err)
	}
	if tbl, ok := v.(view.Table); !ok || len(tbl.Rows) == 0 {
		t.Errorf("net.hosts.list listed nothing from the system file: %#v", v)
	}
	v, err = runResolverList(ctx, bounded(nil))
	if err != nil {
		t.Fatalf("net.resolver.list with no file under a root: %v", err)
	}
	got := pairs(t, v)
	if got["nameserver"] != "192.0.2.53" {
		t.Errorf("nameserver = %q, want the system file's", got["nameserver"])
	}
	// The system's file is asked about as itself, link and all, as at a
	// terminal: a resolv.conf linked elsewhere is one something else writes.
	if !strings.Contains(got["managed by"], "a symlink to "+stub) {
		t.Errorf("managed by = %q, want the link the system file is", got["managed by"])
	}
	if _, err := runHostsAdd(ctx, bounded(map[string]any{
		"ip": "192.0.2.7", "hostname": []string{"bounded.test"}})); err != nil {
		t.Fatalf("net.hosts.add with no file under a root: %v", err)
	}
	if !strings.Contains(hostsContent(t, hosts), "192.0.2.7 bounded.test") {
		t.Errorf("the system file was not edited:\n%s", hostsContent(t, hosts))
	}

	for id, run := range map[string]plugin.Handler{"net.hosts.list": runHostsList, "net.resolver.list": runResolverList} {
		named := hosts
		if id == "net.resolver.list" {
			named = stub
		}
		if v, err := run(ctx, bounded(map[string]any{"file": named})); err == nil {
			t.Errorf("%s read a file the caller named outside the root: %#v", id, v)
		}
	}
}
