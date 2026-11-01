package pluginhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk/spelling"
	"github.com/this-is-tobi/rta/pkg/view"
	rtav1 "github.com/this-is-tobi/rta/proto/rta/v1"
)

// countingStub is a plugin process that only counts the calls it is sent.
type countingStub struct {
	rtav1.PluginServiceClient
	calls int
}

func (s *countingStub) Call(context.Context, *rtav1.CallRequest, ...grpc.CallOption) (*rtav1.CallResponse, error) {
	s.calls++
	return &rtav1.CallResponse{}, nil
}

// Under strict a plugin is started once for many calls and may open only what
// it was started with, so the file a call names arrives too late to be
// allowed. The call is refused before the plugin is sent it, naming the
// setting, on the input that named the file, rather than reaching the plugin
// and failing there with the kernel's "permission denied", which reads as a
// directory to chmod. What passes is a file already there in a location the
// grant made readable, and every call when strict is off.
func TestStrictRefusesAFileACallNamesBeforeThePluginIsSentIt(t *testing.T) {
	granted := t.TempDir()
	config := filepath.Join(granted, "config")
	elsewhere := filepath.Join(t.TempDir(), "dump.sql")
	for _, f := range []string{config, elsewhere} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A link in the granted location that leads out of it is what the kernel
	// refuses at the open, so it is refused here too.
	leadsOut := filepath.Join(granted, "dump.sql")
	if err := os.Symlink(elsewhere, leadsOut); err != nil {
		t.Fatal(err)
	}
	inputs := []plugin.Field{
		{Name: "out", Type: plugin.Path, Local: true},
		{Name: "file", Type: plugin.Path, Local: true, Positional: true},
		{Name: "note", Type: plugin.String},
	}
	for _, tc := range []struct {
		why    string
		strict bool
		values map[string]any
		// refused is the input the refusal is on, and "" for a call the
		// plugin is sent.
		refused string
	}{
		{"a file already there in the granted location", true, map[string]any{"file": config}, ""},
		{"a file the call would create there", true, map[string]any{"out": filepath.Join(granted, "new")}, "out"},
		{"a file outside it", true, map[string]any{"file": elsewhere}, "file"},
		{"a link in it that leads out", true, map[string]any{"file": leadsOut}, "file"},
		{"a string that reads like a path", true, map[string]any{"note": elsewhere}, ""},
		{"no file at all", true, map[string]any{}, ""},
		{"a file outside it, with strict off", false, map[string]any{"file": elsewhere}, ""},
	} {
		stub := &countingStub{}
		c := &Client{stub: stub, deny: DenySet{Strict: tc.strict, Readable: []string{granted}}}
		dst := plugin.Capability{ID: "db.restore", Inputs: inputs}
		c.attach(&dst, &rtav1.Capability{})
		_, err := dst.Run(context.Background(), plugin.NewRequest(tc.values, false, false).WithSurface(plugin.SurfaceCLI))
		var verr *view.Error
		if tc.refused == "" {
			if err != nil || stub.calls != 1 {
				t.Errorf("%s: the plugin was sent %d calls, and the call failed with %v", tc.why, stub.calls, err)
			}
			continue
		}
		if !errors.As(err, &verr) || verr.Code != "plugin.confine.file" || verr.Input != tc.refused || !verr.Refusal ||
			stub.calls != 0 {
			t.Errorf("%s: the plugin was sent %d calls, and the call failed with %+v", tc.why, stub.calls, err)
		}
	}
}

// The refusal names the input the way its reader gave it, and an agent is
// told whose the setting is in words that send it to no terminal.
func TestAStrictRefusalNamesTheInputAsItsReaderGaveIt(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "x.dump")
	inputs := []plugin.Field{{Name: "out", Type: plugin.Path}, {Name: "file", Type: plugin.Path, Positional: true}}
	for _, tc := range []struct {
		sf    plugin.Surface
		field string
		want  string
	}{
		{plugin.SurfaceCLI, "out", "--out names " + missing},
		{plugin.SurfaceCLI, "file", "<file> names " + missing},
		{plugin.SurfaceTUI, "out", "the out box names " + missing},
		{plugin.SurfaceMCP, "out", `the "out" argument names ` + missing},
	} {
		req := plugin.NewRequest(map[string]any{tc.field: missing}, false, false).WithSurface(tc.sf)
		got := DenySet{Strict: true}.namedFile("pg.backup", inputs, req)
		if got == nil || !strings.HasPrefix(got.Message, tc.want) || !strings.HasSuffix(got.Message, "does not let pg open") {
			t.Errorf("over %s, %s reads %+v", tc.sf, tc.field, got)
			continue
		}
		if hits := spelling.ForPlugin(plugin.Plugin{Name: "pg"}).Find(got.Hint, false); tc.sf == plugin.SurfaceMCP && len(hits) > 0 {
			t.Errorf("the hint an agent reads spells a terminal's: %q", hits)
		}
	}
}

// A file a plugin opens from an input that is not a Path, a CA file a profile
// fills, gets past namedFile, and the kernel's refusal came back as the
// plugin's own failure under the plugin's own hint. Where the path in it is
// one the launch's policy keeps from the plugin, the hint names the setting,
// and the code and message stay the plugin's. Where the policy allows the
// path, something else refused it, and nothing changes; nor for a server's
// own "permission denied", which names no path, or for a failure that is not
// the plugin's.
func TestAKernelRefusalUnderStrictNamesTheSetting(t *testing.T) {
	allows := func(path string) bool { return strings.HasPrefix(path, "/etc/") }
	const own = "--ca-file is a path on this machine"
	refused := func(msg string) error {
		return view.Errorf("prom.tls.ca.unreadable", "%s", msg).WithHint(own)
	}
	const inHome = "cannot read the CA file: open /home/op/my certs/ca.pem: permission denied"
	for _, tc := range []struct {
		why  string
		sf   plugin.Surface
		msg  string
		hint string
	}{
		{"a CA file in the home directory", plugin.SurfaceCLI, inHome,
			"`confine: strict` keeps prom from opening /home/op/my certs/ca.pem: a plugin under it opens"},
		{"the same, read by an agent", plugin.SurfaceMCP, inHome,
			"keeps prom from opening /home/op/my certs/ca.pem; that is the operator's configuration to change"},
		{"a tool off the policy's PATH", plugin.SurfaceCLI, "fork/exec /home/op/bin/pg_dump: permission denied",
			"keeps prom from opening /home/op/bin/pg_dump"},
		{"a file the policy allows", plugin.SurfaceCLI, "open /etc/shadow: permission denied", own},
		{"a server's own refusal", plugin.SurfaceCLI, "ERROR: permission denied for table users", own},
	} {
		got := explainDenied("prom.overview", tc.sf, refused(tc.msg), allows)
		var verr *view.Error
		if !errors.As(got, &verr) || !strings.Contains(verr.Hint, tc.hint) ||
			verr.Code != "prom.tls.ca.unreadable" || verr.Message != tc.msg {
			t.Errorf("%s: %+v", tc.why, got)
			continue
		}
		if hits := spelling.ForPlugin(plugin.Plugin{Name: "prom"}).Find(verr.Hint, false); tc.sf == plugin.SurfaceMCP && len(hits) > 0 {
			t.Errorf("%s: the hint an agent reads spells a terminal's: %q", tc.why, hits)
		}
	}
	transport := errors.New("the plugin process exited")
	if got := explainDenied("prom.overview", plugin.SurfaceCLI, transport, allows); got != transport {
		t.Errorf("a failure that is not the plugin's became %v", got)
	}
	if err := refused(inHome); (DenySet{}).deniedOpen("prom.overview", "", plugin.NewRequest(nil, false, false), err) != err {
		t.Errorf("without strict, the plugin's failure was changed")
	}
}
