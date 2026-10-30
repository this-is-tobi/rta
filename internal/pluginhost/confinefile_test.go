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
