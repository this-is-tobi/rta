package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A handler that rewrites the list it is handed reaches no later call, from
// either place a server keeps one across calls: the base plugins: section it
// read at startup, and a profile's set: in the parse its Loader keeps. Each
// call sees the file's list, three times running.
func TestAHandlerCannotRewriteWhatTheNextCallIsHanded(t *testing.T) {
	for name, tc := range map[string]struct {
		yaml string
		args map[string]any
	}{
		"the base section": {"plugins:\n  pg:\n    tags: [a, b]\n", map[string]any{"sql": "x"}},
		"a profile's set:": {"profiles:\n  staging:\n    plugins:\n      pg:\n        set:\n" +
			"          host: staging.internal\n          tags: [a, b]\n",
			map[string]any{"profile": "staging", "sql": "x"}},
	} {
		t.Run(name, func(t *testing.T) {
			var seen []string
			c := plugin.Capability{
				ID: "pg.query", Summary: "q", Safety: plugin.Read,
				Inputs: []plugin.Field{
					{Name: "host", Type: plugin.String, Default: "localhost", Config: "host", Local: true},
					{Name: "tags", Type: plugin.StringSlice, Config: "tags"},
					{Name: "sql", Type: plugin.String},
				},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					seen = append(seen, strings.Join(req.StringSlice("tags"), ","))
					if l, ok := req.Values()["tags"].([]any); ok && len(l) > 0 {
						l[0] = "SCRIBBLED"
					}
					return view.Text{Body: "ok"}, nil
				},
			}
			reg := registry.New()
			if err := reg.Register(plugin.Plugin{Name: "pg", Summary: "pg", Capabilities: []plugin.Capability{c}}); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			t.Setenv("RTA_DATA_DIR", dir)
			t.Setenv("RTA_CONFIG", dir+"/config.yaml")
			if err := writeFile(dir+"/config.yaml", tc.yaml); err != nil {
				t.Fatal(err)
			}
			loader := &config.Loader{}
			cfg, err := loader.Load()
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := tc.args["profile"]; ok {
				now := time.Now()
				if verr := grant.Save([]grant.Grant{{Target: "pg", Profile: "staging",
					ProfilePin: profile.ConnStampFor(cfg, "staging", "pg"), Issued: now, Expires: now.Add(time.Hour)}}); verr != nil {
					t.Fatal(verr)
				}
			}
			resolver, _ := pluginconf.Resolve(cfg, reg)
			server := NewServer(reg, "test", Options{Origin: reg.Origin, Config: resolver.For, Profiles: cfg, Reload: loader.Load})
			session := serve(t, server, sdk.NewClient(&sdk.Implementation{Name: "agent", Version: "0"}, nil))
			for range 3 {
				res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: "pg_query", Arguments: tc.args})
				if err != nil || res.IsError {
					t.Fatalf("call refused: %v %s", err, contentText(t, res))
				}
			}
			if got := strings.Join(seen, " "); got != "a,b a,b a,b" {
				t.Errorf("the handler saw %q, want the file's list on every call", got)
			}
		})
	}
}
