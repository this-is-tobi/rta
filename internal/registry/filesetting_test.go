package registry

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A setting a profile may read a file for, for each call, is given that file
// by a plugin's own process, which the wire carries the content to. A
// built-in runs in this one, where nothing writes it, so a built-in declaring
// one is refused rather than handed a setting it would read as empty.
func TestOnlyAPluginInItsOwnProcessDeclaresAFileSetting(t *testing.T) {
	withCA := func(name string) plugin.Plugin {
		p := testPlugin(name)
		p.Capabilities[0].Inputs = []plugin.Field{{Name: "ca-file", Type: plugin.String, Help: "CA",
			Local: true, Config: "ca-file", File: plugin.FileCertificates}}
		return p
	}
	r := New()
	if err := r.Register(withCA("acme")); err == nil || !strings.Contains(err.Error(), `"ca-file"`) {
		t.Errorf("a built-in: %v", err)
	}
	if err := r.RegisterFrom(withCA("acme"), Origin{Path: "/usr/local/bin/rta-plugin-acme", Digest: "aaaaaaaaaaaaaaaa"}); err != nil {
		t.Errorf("a plugin: %v", err)
	}
}
