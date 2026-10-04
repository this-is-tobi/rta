package app

import (
	"strings"
	"testing"
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
		if got, ok := pluginWordHint(c.word); !ok || got != c.want {
			t.Errorf("pluginWordHint(%q) = %q, %v, want %q", c.word, got, ok, c.want)
		}
	}
	for _, word := range []string{"mongo", "revoke", "pkg", "install", "version"} {
		if got, ok := pluginWordHint(word); ok {
			t.Errorf("pluginWordHint(%q) = %q, want no answer for a word that is no plugin", word, got)
		}
	}
}

// A plugin that is installed and failed to start is not one to install: the
// answer for it is where to read why.
func TestAWordNamingAPluginThatFailedToStartSaysSo(t *testing.T) {
	withFailedPlugins(t, helloFailed)
	got, ok := pluginWordHint("hello")
	if !ok || !strings.Contains(got, "installed and failed to start") || !strings.Contains(got, "`rta plugin list`") {
		t.Errorf("pluginWordHint(hello) = %q, %v", got, ok)
	}

	pg := helloFailed
	pg.Name = "pg"
	withFailedPlugins(t, pg)
	if got, _ := pluginWordHint("pg"); strings.Contains(got, "plugin install") {
		t.Errorf("a first-party plugin that is installed and broken was told to install it: %q", got)
	}
}
