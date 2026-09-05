package tui

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// fakeForward puts a kubectl on $PATH that reports a forward onto a real
// listener, and returns the port that listener is on.
//
// A real listener rather than a made-up number, for internal/tunnel's own
// reason: a resolver that parsed a port nothing was listening on would pass
// every assertion made against the string.
func fakeForward(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\necho 'Forwarding from 127.0.0.1:%d -> 5432'\nwhile true; do sleep 1; done\n", port)
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return port
}

// reachedHost records where a capability's handler was actually pointed.
func reachedTile(t *testing.T, seen *string) tile {
	t.Helper()
	return tile{cap: plugin.Capability{
		ID: "db.status", Summary: "status", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Default: "localhost", Config: "host",
				Local: true, Endpoint: plugin.EndpointHost, Help: "host"},
			{Name: "port", Type: plugin.Int, Default: 5432, Config: "port",
				Local: true, Endpoint: plugin.EndpointPort, Min: 1, Max: 65535, Help: "port"},
		},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			*seen = fmt.Sprintf("%s:%d", req.String("host"), req.Int("port"))
			return view.Text{Body: "ok"}, nil
		},
	}}
}

// A dashboard tile under a `kube:` connection runs through the forward, like
// every other call does.
//
// **This is the failure the whole feature exists to remove, arriving through
// the one path that did not open a tunnel.** tileCmd ran the handler with the
// cached bind values and never dialled, so a pg tile under a cluster profile
// ran against localhost:5432 while the badge said homelab — and on a machine
// with a local PostgreSQL that is real data from the wrong database, refreshed
// every five seconds, with nothing anywhere saying so.
//
// A dashboard that lies is worse than one that costs, which is what decides
// the trade-off here: one forward per tunnelled plugin per refresh is the
// price the TUI pays per call.
func TestATileUnderAClusterConnectionRunsThroughTheForward(t *testing.T) {
	port := fakeForward(t)
	var seen string
	ti := reachedTile(t, &seen)
	conn := config.Connection{Kube: "homelab/databases/svc/postgres:5432"}

	msg := tileCmd(0, ti, nil, "homelab", nil, conn)().(tileMsg)
	if msg.err != nil {
		t.Fatalf("tile: %v", msg.err)
	}
	want := fmt.Sprintf("127.0.0.1:%d", port)
	if seen != want {
		t.Errorf("the tile reached %s, want %s — a tile under a cluster connection "+
			"ran against the plugin's own default while the badge said otherwise", seen, want)
	}
}

// A tile whose connection names no cluster opens nothing and runs where it
// always did — the overwhelmingly common case, and the one a per-refresh dial
// must not slow down or break.
func TestATileWithoutAClusterConnectionIsUnaffected(t *testing.T) {
	// A kubectl that fails loudly if anything runs it.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kubectl"),
		[]byte("#!/bin/sh\necho 'must not run' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var seen string
	ti := reachedTile(t, &seen)
	msg := tileCmd(0, ti, nil, "", map[string]any{"host": "db.internal", "port": 6543},
		config.Connection{})().(tileMsg)
	if msg.err != nil {
		t.Fatalf("tile: %v", msg.err)
	}
	if seen != "db.internal:6543" {
		t.Errorf("the tile reached %s, want db.internal:6543", seen)
	}
}

// A forward that cannot be opened makes the tile report the failure, rather
// than quietly falling back to the plugin's default host.
//
// The tile is where a fallback would be least visible: nobody typed anything,
// so there is no command to look at, and the number on screen would simply be
// somebody else's.
func TestATileReportsAForwardItCouldNotOpen(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kubectl"),
		[]byte("#!/bin/sh\necho 'Error from server (NotFound): services \"postgres\" not found' >&2\nexit 1\n"),
		0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var seen string
	ti := reachedTile(t, &seen)
	conn := config.Connection{Kube: "homelab/databases/svc/postgres:5432"}
	msg := tileCmd(0, ti, nil, "homelab", nil, conn)().(tileMsg)

	if msg.err == nil {
		t.Fatalf("a tile whose forward failed reported success, having reached %q", seen)
	}
	if seen != "" {
		t.Errorf("the handler ran anyway, against %s", seen)
	}
}

// The credential form offers every input a connection can fill, not only the
// Secrets — credentials first.
//
// `user` from a cluster Secret's `username` key is an ordinary thing to want:
// it works in the file, because internal/profile.Fill gates on
// ProfileFillable, and it was unreachable from this screen because the form
// listed only Secret-typed inputs. A feature that works in YAML and cannot be
// reached from the editor is a feature most people do not have.
func TestTheCredentialFormOffersEveryInputAConnectionCanFill(t *testing.T) {
	form := credentialForm(t, "password")
	if _, asked := form.bindings[credInputField]; !asked {
		t.Fatal("no input picker, so the non-Secret inputs cannot be chosen")
	}
	opts := optionsOf(form, credInputField)
	if len(opts) < 2 {
		t.Fatalf("the picker offers %v, and the plugin declares a fillable host as well", opts)
	}
	if opts[0] != "password" {
		t.Errorf("the picker leads with %q; credentials come first because they are why "+
			"somebody opens this, and a picker leading with `host` invites filling the wrong one", opts[0])
	}
	if !slices.Contains(opts, "host") {
		t.Errorf("a fillable non-Secret input is not offered: %v", opts)
	}
}

// optionsOf reads a built form field's declared options.
func optionsOf(form *capForm, name string) []string {
	for _, f := range form.fields {
		if f.Name == name {
			return f.Options
		}
	}
	return nil
}

// The cluster is offered as a source only when the connection names one.
//
// A Secret is read from the namespace the coordinate already gives, so without
// a coordinate there is nowhere to read from — the same dead end the store
// option avoids by not appearing when the store is empty.
func TestTheClusterSourceAppearsOnlyWithACoordinate(t *testing.T) {
	plain := credentialForm(t, "password")
	if slices.Contains(optionsOf(plain, credSourceField), credSourceKube) {
		t.Error("a connection naming no cluster offered to read a Secret from one")
	}

	m := credentialModel(t, "password")
	// Point the open connection at a cluster, the way the plugin form would.
	for i, row := range m.profiles {
		if row.name != "staging" {
			continue
		}
		for j := range row.conns {
			m.profiles[i].conns[j].conn.Kube = "homelab/databases/svc/postgres:5432"
		}
	}
	withCluster := openCredentialForm(t, m)
	if !slices.Contains(optionsOf(withCluster, credSourceField), credSourceKube) {
		t.Errorf("a connection naming a cluster did not offer to read a Secret from it: %v",
			optionsOf(withCluster, credSourceField))
	}
}

// The plugin form carries the cluster coordinate, both ways.
//
// Without it, the two things this feature is for — reach a service through a
// forward, take the credential from a Secret beside it — were expressible only
// by hand-editing the config file. The form offered `set.*` and nothing else,
// so somebody looking at the editor would conclude rta could not do it.
func TestTheConnectionFormCarriesTheClusterCoordinate(t *testing.T) {
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{
			"db": {Set: map[string]any{"host": "staging.internal"}},
		}},
	}})
	m.profileOpen = "staging"

	next, _ := m.startConnForm("db")
	nm := next.(Model)
	if _, offered := nm.form.bindings[profileKubeField]; !offered {
		t.Fatal("the connection editor does not ask for a coordinate")
	}
	*nm.form.bindings[profileKubeField] = "homelab/databases/svc/postgres:5432"
	next, _ = nm.saveConnForm()

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.Profiles["staging"].Plugins["db"].Kube; got != "homelab/databases/svc/postgres:5432" {
		t.Fatalf("kube = %q, want the coordinate that was typed", got)
	}
	// And it comes back into the form, or editing a host would silently drop
	// the coordinate — the same hazard the credential mapping already has.
	again, _ := next.(Model).startConnForm("db")
	if got := *again.(Model).form.bindings[profileKubeField]; got != "homelab/databases/svc/postgres:5432" {
		t.Errorf("reopened with kube = %q; a coordinate that does not come back is one "+
			"that gets dropped by editing anything else", got)
	}
}

// A coordinate that is not a coordinate is refused at the keystroke, not saved
// and reported later.
//
// This is the one screen where somebody types one, so it is where the typo
// should be caught. `rta doctor` finding it afterwards means the profile was
// saved broken and the operator has moved on to something else.
func TestTheConnectionFormRefusesACoordinateThatIsNotOne(t *testing.T) {
	m := profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"db": {}}},
	}})
	m.profileOpen = "staging"

	next, _ := m.startConnForm("db")
	nm := next.(Model)
	*nm.form.bindings[profileKubeField] = "homelab/databases/postgres:5432" // three segments
	after, _ := nm.saveConnForm()

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.Profiles["staging"].Plugins["db"].Kube; got != "" {
		t.Errorf("a malformed coordinate was written to the file: %q", got)
	}
	if flash := after.(Model).flash; !strings.Contains(flash, "coordinate") {
		t.Errorf("flash = %q, want it to say what is wrong", flash)
	}
}
