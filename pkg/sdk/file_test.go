package sdk

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk/wire"
	"github.com/this-is-tobi/rta/pkg/view"
	rtav1 "github.com/this-is-tobi/rta/proto/rta/v1"
)

// A CA the host read for a call reaches the handler as the path of a file
// holding it, private to the call, and the file is gone once the call
// returns, Request.Temporary saying so: in a call, a prefill and a live
// suggestion alike, the three ways values reach a plugin.
func TestACertificateTheHostReadReachesTheHandlerAsAFile(t *testing.T) {
	certificate := "-----BEGIN CERTIFICATE-----\nMIIBdemo\n-----END CERTIFICATE-----\n"
	type seen struct {
		path, content     string
		temporary, other  bool
		fileMode, dirMode os.FileMode
	}
	var got seen
	look := func(req plugin.Request) {
		got.path = req.String("ca-file")
		got.temporary, got.other = req.Temporary("ca-file"), req.Temporary("host")
		if info, err := os.Stat(got.path); err == nil {
			got.fileMode = info.Mode().Perm()
		}
		if info, err := os.Stat(filepath.Dir(got.path)); err == nil {
			got.dirMode = info.Mode().Perm()
		}
		content, _ := os.ReadFile(got.path)
		got.content = string(content)
	}
	p := plugin.Plugin{Name: "demo", Summary: "d", Capabilities: []plugin.Capability{{
		ID: "demo.status", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Help: "host", Local: true, Config: "host"},
			{Name: "ca-file", Type: plugin.String, Help: "CA", Local: true, Config: "ca-file", File: plugin.FileCertificates,
				Suggest: func(_ context.Context, req plugin.Request) []string { look(req); return nil }},
		},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			look(req)
			return view.Text{Body: "ok"}, nil
		},
		Prefill: func(_ context.Context, req plugin.Request) (map[string]any, error) {
			look(req)
			return nil, nil
		},
	}}}
	values := wire.ValuesToProto(map[string]any{
		"host": "db.internal", "ca-file": plugin.NewFileContent([]byte(certificate), "kube:pg-ca/ca.crt"),
	})
	check := func(what string) {
		t.Helper()
		if got.content != certificate || !got.temporary || got.other || got.fileMode != 0o600 || got.dirMode != 0o700 ||
			filepath.Base(got.path) != "ca-file.pem" {
			t.Errorf("%s: the handler saw %+v", what, got)
		}
		if _, err := os.Stat(filepath.Dir(got.path)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: what was written outlived the call: %v", what, err)
		}
		got = seen{}
	}

	s := newServer(p)
	resp, err := s.Call(context.Background(), &rtav1.CallRequest{CapabilityId: "demo.status", Values: values})
	if err != nil || resp.GetError() != nil {
		t.Fatalf("call: %v %+v", err, resp.GetError())
	}
	check("a call")
	if _, err := s.Prefill(context.Background(), &rtav1.PrefillRequest{CapabilityId: "demo.status", Values: values}); err != nil {
		t.Fatal(err)
	}
	check("a prefill")
	if _, err := s.Suggest(context.Background(),
		&rtav1.SuggestRequest{CapabilityId: "demo.status", Field: "ca-file", Values: values}); err != nil {
		t.Fatal(err)
	}
	check("a suggestion")
}

// A file is written inside the call's own directory whatever the input it is
// for is called: a name is the host's to send, and a name that climbs out is
// written where the rest are.
func TestAFileIsWrittenInsideTheCallsDirectory(t *testing.T) {
	out, written, remove, err := writeFiles(map[string]any{"../escape": plugin.NewFileContent([]byte("x"), "")})
	defer remove()
	if err != nil || len(written) != 1 {
		t.Fatalf("err %v, written %v", err, written)
	}
	path, _ := out["../escape"].(string)
	if filepath.Base(path) != "escape.pem" || !strings.HasPrefix(filepath.Base(filepath.Dir(path)), "rta-file-") {
		t.Errorf("written at %q", path)
	}
}

// A file that cannot be written refuses the call, naming why, rather than
// running the handler with the setting empty — which for a plugin whose TLS
// a CA turns on would be a connection with none.
func TestACertificateThatCannotBeWrittenRefusesTheCall(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "gone"))
	ran := false
	p := plugin.Plugin{Name: "demo", Summary: "d", Capabilities: []plugin.Capability{{
		ID: "demo.status", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "ca-file", Type: plugin.String, Help: "CA", Local: true, Config: "ca-file",
			File: plugin.FileCertificates}},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			ran = true
			return view.Text{Body: "ok"}, nil
		},
	}}}
	resp, err := newServer(p).Call(context.Background(), &rtav1.CallRequest{CapabilityId: "demo.status",
		Values: wire.ValuesToProto(map[string]any{"ca-file": plugin.NewFileContent([]byte("x"), "")})})
	if err != nil || ran || resp.GetError().GetCode() != "plugin.file.unwritable" {
		t.Errorf("err %v, ran %v, refusal %+v", err, ran, resp.GetError())
	}
}
