package app

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// caRegistry holds one plugin, in a process of its own as every plugin is,
// whose CA setting is declared a file of certificates and whose older twin of
// it is not.
func caRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	if err := reg.RegisterFrom(plugin.Plugin{
		Name: "db", Summary: "db plugin",
		Capabilities: []plugin.Capability{{
			ID: "db.status", Summary: "status", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "ca-file", Type: plugin.String, Config: "ca-file", Local: true, File: plugin.FileCertificates, Help: "CA"},
				{Name: "sslrootcert", Type: plugin.String, Config: "sslrootcert", Local: true, Help: "CA"},
			},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		}},
	}, registry.Origin{Path: "/usr/local/bin/rta-plugin-db", Digest: "aaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// The card an operator is sent to says which setting a profile can read a CA
// for, from where the CA is kept, and only that one.
func TestExplainCardSaysWhichSettingTakesACAFromASecret(t *testing.T) {
	out, _, err := run(t, caRegistry(t), "explain", "db.status")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "a file of certificates, which a profile's secrets: can read for each call") != 1 {
		t.Errorf("the card does not say it of ca-file alone:\n%s", out)
	}
}

// Completing a `secrets:` mapping marks the setting that takes certificates,
// the other mapping somebody comes to the flag for.
func TestSecretCompletionMarksTheCASetting(t *testing.T) {
	root := NewRoot(caRegistry(t), "test")
	cmd, _, err := root.Find([]string{"profile", "set"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("plugin", "db"); err != nil {
		t.Fatal(err)
	}
	got, _ := completeSecretInputs(cmd, nil, "")
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "ca-file=\tcertificates, read into a file for each call") ||
		strings.Contains(joined, "sslrootcert=\tcertificates") {
		t.Errorf("completion: %q", got)
	}
}
