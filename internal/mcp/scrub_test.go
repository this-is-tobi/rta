package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// An error says where, for the person at a terminal, and over MCP that is the
// layout of the one place rta keeps what an agent must not read or move: the
// stores, the grants, the record and the file that unlocks the secrets. The
// operating system's own errors name them too, so it is the bridge that puts
// them right, and an agent is told what the places are called instead. The
// record is the operator's and keeps the error as it was.
func TestAnErrorDoesNotTellAnAgentWhereRtaKeepsItsState(t *testing.T) {
	identity := filepath.Join(t.TempDir(), "age-identity.txt")
	t.Setenv("RTA_KV_IDENTITY", identity)
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "vault", Summary: "vault", Capabilities: []plugin.Capability{{
			ID: "vault.read", Summary: "read the store", Safety: plugin.Read,
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return nil, view.Errorf("vault.unreadable", "reading %s: permission denied", filepath.Join(paths.Data(), "kv.age")).
					WithHint("the identity " + identity + " could not unlock " + paths.Data())
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := connectWithData(t, reg, Options{})

	res := callTool(t, s, "vault_read", map[string]any{})
	if !res.IsError {
		t.Fatal("the fixture's call was expected to fail")
	}
	text := res.Content[0].(*sdk.TextContent).Text
	for _, leaked := range []string{paths.Data(), identity} {
		if strings.Contains(text, leaked) {
			t.Errorf("the error an agent reads names %s: %s", leaked, text)
		}
	}
	if !strings.Contains(text, "<data dir>/kv.age") || !strings.Contains(text, "<identity file>") {
		t.Errorf("the places are not named by what they are: %s", text)
	}

	rows, err := agentlog.Read(1)
	if err != nil || len(rows) != 1 || !strings.Contains(rows[0].Reason, paths.Data()) {
		t.Errorf("the record does not keep the error as it was: %+v, %v", rows, err)
	}
}

// A place is replaced where it is a path of its own. A data directory called
// /data is an ordinary choice, and a bare substring would turn /database into
// <data dir>base and /srv/data/x into /srv<data dir>/x.
func TestAPlaceIsOnlyReplacedWhereItIsAPathOfItsOwn(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	t.Setenv("RTA_DATA_DIR", data)
	t.Setenv("RTA_KV_IDENTITY", "")
	for in, want := range map[string]string{
		"reading " + data + "/kv.age: denied":      "reading <data dir>/kv.age: denied",
		"cannot write to " + data + ".":            "cannot write to <data dir>.",
		"open '" + data + "': no such file":        "open '<data dir>': no such file",
		"in " + data + "base/x":                    "in " + data + "base/x",
		"in " + data + ".bak":                      "in " + data + ".bak",
		"in /srv" + data + "/x":                    "in /srv" + data + "/x",
		"twice " + data + "/a and " + data + "/b.": "twice <data dir>/a and <data dir>/b.",
	} {
		if got := withoutOperatorPaths(in); got != want {
			t.Errorf("%q became %q, want %q", in, got, want)
		}
	}
}

// What is not rta's own place is left as it was: a path the agent sent is the
// agent's.
func TestAnErrorKeepsThePathsThatAreNotRtas(t *testing.T) {
	t.Setenv("RTA_KV_IDENTITY", "")
	other := filepath.Join(os.TempDir(), "somewhere-else", "file.txt")
	if got := withoutOperatorPaths("reading " + other + ": no such file"); !strings.Contains(got, other) {
		t.Errorf("a path that is not rta's was rewritten: %s", got)
	}
}
