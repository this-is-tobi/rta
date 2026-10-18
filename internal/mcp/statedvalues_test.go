package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// statedFixture serves one capability shaped like a service plugin's: a
// connection the operator states in the config or a profile (host, port, a URL),
// declared Local so that no caller can choose where it goes, and a refusal for
// each way a connection fails, written as the plugins write them, with the
// address in the message and in the hint. Built-ins carry no connection, so the
// capability is declared here.
type statedFixture struct {
	session  *sdk.ClientSession
	profiles config.Config
}

func newStatedFixture(t *testing.T, yaml string) *statedFixture {
	t.Helper()
	c := plugin.Capability{
		ID: "pg.query", Summary: "run a query", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Default: "localhost", Config: "host", Local: true},
			{Name: "port", Type: plugin.Int, Default: 5432, Config: "port", Local: true},
			{Name: "address", Type: plugin.String, Config: "address", Local: true},
			{Name: "sslmode", Type: plugin.String, Options: []string{"disable", "require", "verify-full"},
				Default: "disable", Config: "sslmode", Local: true},
			{Name: "sql", Type: plugin.String},
		},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			host, port, sf := req.String("host"), req.Int("port"), req.Surface()
			switch req.String("sql") {
			case "dns":
				return nil, view.Errorf("pg.host.unknown", "no address for %q", host).WithHint(sf.DNSHint(host))
			case "refused":
				return nil, view.Errorf("pg.conn.refused", "nothing is listening on %s:%d", host, port).
					WithHint("is the server up, and is the port right? `" + sf.Call("net.port",
						plugin.Arg{Name: "host", Value: host, Positional: true},
						plugin.Arg{Name: "ports", Value: port}) + "` answers the second")
			case "plain":
				return nil, fmt.Errorf("dial tcp %s:%d: connect: connection refused", host, port)
			case "url":
				return nil, fmt.Errorf("Get %q: dial tcp: lookup vault.prod.internal: no such host",
					req.String("address")+"/v1/sys/health")
			case "reached":
				return nil, view.Errorf("pg.tls.unsupported", "%s does not offer TLS",
					req.Reached(fmt.Sprintf("%s:%d", host, port)))
			case "prose":
				return nil, view.Errorf("pg.conn.failed", "could not connect").
					WithHint("check that " + host + " is reachable from this machine, and that sslmode " +
						req.String("sslmode") + " is what the server speaks")
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
	if err := writeFile(dir+"/config.yaml", yaml); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", dir+"/config.yaml")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	resolver, _ := pluginconf.Resolve(cfg, reg.Origin)
	server := NewServer(reg, "test", Options{Origin: reg.Origin, Config: resolver.For, Profiles: cfg, Reload: config.Load})
	st, ct := sdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := sdk.NewClient(&sdk.Implementation{Name: "agent", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return &statedFixture{session: session, profiles: cfg}
}

// grantProfile lets the agent name a profile, as a person's grant for that exact
// connection does.
func (f *statedFixture) grantProfile(t *testing.T, name string) {
	t.Helper()
	now := time.Now()
	if verr := grant.Save([]grant.Grant{{
		Target: "pg", Profile: name, ProfilePin: profile.ConnStampFor(f.profiles, name, "pg"),
		Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
}

// refusal is the error an agent was handed: its code, message and hint, as the
// envelope carries them.
func (f *statedFixture) refusal(t *testing.T, args map[string]any) (code, message, hint string) {
	t.Helper()
	res, err := f.session.CallTool(context.Background(), &sdk.CallToolParams{Name: "pg_query", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("the call succeeded, want a refusal: %s", contentText(t, res))
	}
	var e struct{ Code, Message, Hint string }
	if err := json.Unmarshal([]byte(contentText(t, res)), &e); err != nil {
		t.Fatalf("not an error envelope: %v", err)
	}
	return e.Code, e.Message, e.Hint
}

const deadHostProfile = `
plugins:
  pg:
    sslmode: verify-full
profiles:
  deadhost:
    plugins:
      pg:
        set:
          host: db.prod.internal.invalid
          port: 1
          address: https://vault.prod.internal:8200
`

// An error is written for the person at a terminal, and says where the
// connection went: "no address for db.prod.internal.invalid", with a hint to ask
// DNS for it, "nothing is listening on 10.1.2.3:1". Over MCP that is the
// operator's network, one refusal at a time, which is what the data and config
// directories are already kept from an agent for. The values the operator stated
// for the call's connection are named by the profile they came from instead, in
// the message and in the hint, and a hint that is a call probing one is dropped:
// the agent would otherwise be handed the name to ask DNS about.
func TestAnErrorNamesTheProfileNotTheAddressItSets(t *testing.T) {
	f := newStatedFixture(t, deadHostProfile)
	f.grantProfile(t, "deadhost")
	for _, c := range []struct {
		sql, message, hint string
	}{
		{"dns", `no address for profile "deadhost"`, ""},
		{"refused", `nothing is listening on profile "deadhost"`, ""},
		{"plain", `dial tcp profile "deadhost": connect: connection refused`, ""},
		{"url", `Get "profile "deadhost"/v1/sys/health": dial tcp: lookup profile "deadhost": no such host`, ""},
		// A message that names the profile beside the address, as plugin.Request.Reached
		// phrases it, reads it once.
		{"reached", `profile "deadhost" does not offer TLS`, ""},
		{"prose", "could not connect",
			`check that profile "deadhost" is reachable from this machine, and that sslmode verify-full is what the server speaks`},
	} {
		t.Run(c.sql, func(t *testing.T) {
			_, message, hint := f.refusal(t, map[string]any{"profile": "deadhost", "sql": c.sql})
			if message != c.message || hint != c.hint {
				t.Errorf("the agent was told message %q, hint %q; want message %q, hint %q", message, hint, c.message, c.hint)
			}
			for _, leaked := range []string{"db.prod.internal", "10.1.2.3", "vault.prod", "net_dns", "net_port"} {
				if strings.Contains(message+hint, leaked) {
					t.Errorf("the agent was handed %q: message %q, hint %q", leaked, message, hint)
				}
			}
		})
	}
}

// And the ledger keeps what the agent was not told.
func TestTheLedgerKeepsTheAddressTheAgentWasNotTold(t *testing.T) {
	f := newStatedFixture(t, deadHostProfile)
	f.grantProfile(t, "deadhost")
	f.refusal(t, map[string]any{"profile": "deadhost", "sql": "dns"})
	entries, err := agentlog.Read(1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ledger = %v, %v", entries, err)
	}
	if want := `no address for "db.prod.internal.invalid"`; entries[0].Reason != want {
		t.Errorf("the row's reason = %q, want the message as the plugin wrote it: %q", entries[0].Reason, want)
	}
}

// With no profile in play the values come from the base config, and the agent is
// told which input held them.
func TestAnErrorNamesTheInputWhenNoProfileIsInPlay(t *testing.T) {
	f := newStatedFixture(t, `
plugins:
  pg:
    host: base.internal
    port: 6543
    sslmode: verify-full
`)
	for _, c := range []struct{ sql, message, hint string }{
		{"dns", `no address for <host>`, ""},
		{"refused", `nothing is listening on <host>:<port>`, ""},
		{"prose", "could not connect",
			`check that <host> is reachable from this machine, and that sslmode verify-full is what the server speaks`},
	} {
		t.Run(c.sql, func(t *testing.T) {
			_, message, hint := f.refusal(t, map[string]any{"sql": c.sql})
			if message != c.message || hint != c.hint {
				t.Errorf("the agent was told message %q, hint %q; want message %q, hint %q", message, hint, c.message, c.hint)
			}
		})
	}
}

// A default is nobody's statement, and so is a value from a closed set: neither
// says where the operator's connection is, and masking them would rewrite the
// vocabulary of the plugin's own hints. What the operator did not state is shown
// as it always was, and so is the hint that probes it.
func TestWhatTheOperatorDidNotStateIsLeftAsItWas(t *testing.T) {
	f := newStatedFixture(t, `
plugins:
  pg:
    sslmode: verify-full
`)
	_, message, hint := f.refusal(t, map[string]any{"sql": "dns"})
	if message != `no address for "localhost"` || !strings.Contains(hint, "net_dns") {
		t.Errorf("a call on the defaults was told message %q, hint %q, want both as the plugin wrote them", message, hint)
	}
	_, _, hint = f.refusal(t, map[string]any{"sql": "prose"})
	if !strings.Contains(hint, "check that localhost is reachable") || !strings.Contains(hint, "sslmode verify-full") {
		t.Errorf("hint = %q, want the default host and the closed-set value as they were", hint)
	}
}

// What is named is a value: a host, an address, a word. Nothing that is only
// punctuation (a dot or a slash is a word of no sentence), nothing blank, and a
// value is named where it stands as a word of its own and not inside a longer
// one, in one pass, so that a name written in a value's place is not read as a
// value in turn.
func TestOnlyAValueIsNamed(t *testing.T) {
	c := plugin.Capability{
		ID: "pg.query", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Config: "host", Local: true},
			{Name: "database", Type: plugin.String, Config: "database", Local: true},
			{Name: "root", Type: plugin.String, Config: "root", Local: true},
			{Name: "blank", Type: plugin.String, Config: "blank", Local: true},
		},
	}
	run := plugin.ResolveRequest(c, plugin.Inputs{
		ProfileName: "prod",
		Config:      map[string]any{"host": "db", "database": "prod", "root": "/", "blank": "  "},
	}, false, false).WithSurface(plugin.SurfaceMCP)
	stated := statedFor(c, run)
	for in, want := range map[string]string{
		"failed. see /tmp and a/b":       "failed. see /tmp and a/b",
		"database db not found":          `database profile "prod" not found`,
		"dbus and db-admin and mydb":     "dbus and db-admin and mydb",
		`no such database "prod": db.`:   `no such database profile "prod": profile "prod".`,
		"on db:5432":                     `on profile "prod":5432`,
		"on prod, via db, then db.local": `on profile "prod", via profile "prod", then db.local`,
	} {
		if got := stated.mask(in); got != want {
			t.Errorf("mask(%q) = %q, want %q", in, got, want)
		}
	}
	if statedFor(c, plugin.ResolveRequest(c, plugin.Inputs{}, false, false)) != nil {
		t.Error("a call that stated nothing has values to name")
	}
}
