package app

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A `plugin dev` run drops the operator's section for the build it made
// (registerDev), and the password exported for the database that section named
// stays out of a call nobody aimed: it would go to the plugin's own default.
// Aimed on the command line, the call is the person's, and the password goes
// with it.
func TestADevRunSendsTheAmbientCredentialOnlyWhereItIsAimed(t *testing.T) {
	origin := registry.Origin{Path: "/usr/local/bin/rta-plugin-pg", Digest: pgNewDigest}
	reg := registry.New()
	if err := reg.RegisterFrom(plugin.Plugin{Name: "pg", Summary: "pg", Capabilities: []plugin.Capability{{
		ID: "pg.status", Summary: "status", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "address", Type: plugin.String, Default: "localhost:5432", Config: "address", Local: true,
				Endpoint: plugin.EndpointAddress, Help: "host:port"},
			{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true, Help: "password"},
		},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			return view.Text{Body: req.String("address") + " password=" + strconv.FormatBool(req.String("password") != "")}, nil
		},
	}}}, origin); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_PG_PASSWORD", "for-db-internal")
	t.Cleanup(func() { SetPluginConfig(nil, nil) })
	resolver, problems := pluginconf.Resolve(trustedConfig(t, config.Config{Plugins: map[string]map[string]any{
		"pg@" + origin.Short(): {"address": "db.internal:5432"},
	}}), reg.Origin)
	SetPluginConfig(resolver.Without("pg"), problems)

	out, errOut, err := run(t, reg, "pg", "status")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "localhost:5432 password=false") {
		t.Errorf("a dev call aimed nowhere carried the operator's password: %q", out)
	}
	out, errOut, err = run(t, reg, "pg", "status", "--address", "db.dev.internal:5432")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "db.dev.internal:5432 password=true") {
		t.Errorf("a dev call aimed on the command line lost the password: %q", out)
	}
}
