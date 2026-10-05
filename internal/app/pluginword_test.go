package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func TestAFirstPartyWordIsAnsweredWithTheInstall(t *testing.T) {
	withFailedPlugins(t)
	for _, c := range []struct{ word, want string }{
		{"pg", "pg is a first-party plugin — `rta plugin install pg` installs it"},
		{"docker", "docker is a first-party plugin — `rta plugin install docker` installs it"},
		{"qdrant", "qdrant is a first-party plugin — `rta plugin install qdrant` installs it"},
		{"postgres", "postgres is the first-party plugin pg — `rta plugin install pg` installs it"},
		{"k8s", "k8s is the first-party plugin kube — `rta plugin install kube` installs it"},
	} {
		if got, ok := pluginWordHint(nil, c.word); !ok || got != c.want {
			t.Errorf("pluginWordHint(%q) = %q, %v, want %q", c.word, got, ok, c.want)
		}
	}
	for _, word := range []string{"mongo", "revoke", "pkg", "install", "version"} {
		if got, ok := pluginWordHint(nil, word); ok {
			t.Errorf("pluginWordHint(%q) = %q, want no answer for a word that is no plugin", word, got)
		}
	}
}

// A plugin that is installed and failed to start is not one to install: the
// answer for it is where to read why.
func TestAWordNamingAPluginThatFailedToStartSaysSo(t *testing.T) {
	withFailedPlugins(t, helloFailed)
	got, ok := pluginWordHint(nil, "hello")
	if !ok || !strings.Contains(got, "installed and failed to start") || !strings.Contains(got, "`rta plugin list`") {
		t.Errorf("pluginWordHint(hello) = %q, %v", got, ok)
	}

	pg := helloFailed
	pg.Name = "pg"
	withFailedPlugins(t, pg)
	if got, _ := pluginWordHint(nil, "pg"); strings.Contains(got, "plugin install") {
		t.Errorf("a first-party plugin that is installed and broken was told to install it: %q", got)
	}
}

func coded(t *testing.T, err error) *view.Error {
	t.Helper()
	var verr *view.Error
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want a coded error", err)
	}
	return verr
}

// `rta k8s` with kube installed is a way of asking for `rta kube`, and `explain
// pg.qury` with pg installed is a typo: telling either to install the plugin
// is the one answer that is wrong, so a plugin that is running gets none.
func TestAPluginThatIsRunningIsNotToldToInstallIt(t *testing.T) {
	withFailedPlugins(t)
	root := &cobra.Command{Use: "rta"}
	root.AddCommand(&cobra.Command{Use: "kube"})
	if got, ok := pluginWordHint(commandsOf(root), "k8s"); ok {
		t.Errorf("pluginWordHint(k8s) with kube a command = %q, want none", got)
	}
	if got, ok := pluginWordHint(commandsOf(root), "pg"); !ok || !strings.Contains(got, "`rta plugin install pg`") {
		t.Errorf("pluginWordHint(pg) = %q, %v, want the install of the plugin that is not there", got, ok)
	}

	pg := []plugin.Capability{{ID: "pg.status"}}
	if got, ok := missingPluginHint(pg, "pg.qury"); ok {
		t.Errorf("missingPluginHint(pg.qury) with pg registered = %q, want none", got)
	}
	if got, ok := missingPluginHint(pg, "redis.get"); !ok || !strings.Contains(got, "`rta plugin install redis`") {
		t.Errorf("missingPluginHint(redis.get) = %q, %v, want the install", got, ok)
	}
	withFailedPlugins(t, helloFailed)
	if got, ok := missingPluginHint(pg, "hello.greet"); !ok || !strings.Contains(got, "failed to start") {
		t.Errorf("missingPluginHint(hello.greet) = %q, %v, want the failure said", got, ok)
	}
}

// The help of `rta profile set` shows `--plugin pg`, and on a machine without
// the plugin the first thing that happened was "not a registered plugin" and a
// pointer at `plugin list`, which cannot show what is not there.
func TestAProfileNamingAFirstPartyPluginThatIsNotInstalledNamesTheInstall(t *testing.T) {
	withFailedPlugins(t)
	run := session(t, registry.New())
	_, _, err := run("profile", "set", "staging", "--plugin", "pg", "--set", "host=db.internal")
	verr := coded(t, err)
	if verr.Code != "core.profile.unusable" || !strings.Contains(verr.Hint, "`rta plugin install pg`") {
		t.Errorf("err = %v (hint %q), want the install of pg", err, verr.Hint)
	}

	_, _, err = run("profile", "set", "staging", "--plugin", "ghost", "--set", "host=db.internal")
	if hint := coded(t, err).Hint; !strings.Contains(hint, "`rta plugin list`") || strings.Contains(hint, "first-party") {
		t.Errorf("a plugin rta has never heard of was answered with %q, want the generic pointer", hint)
	}
}
