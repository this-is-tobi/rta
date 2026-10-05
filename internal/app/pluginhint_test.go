package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/view"
)

func codedError(t *testing.T, err error) *view.Error {
	t.Helper()
	var verr *view.Error
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want a coded error", err)
	}
	return verr
}

// `rta pg` was told it meant "pkg", `rta docker` "doctor" and `rta qdrant` "grant", and `rta mongo`
// that mongo is probably a plugin from the first-party index, which has none.
func TestTheRootNamesAFirstPartyPluginWhereACommandWasTyped(t *testing.T) {
	withFailedPlugins(t)
	run := session(t, registry.New())
	for word, want := range map[string]string{
		"pg":       "pg is a first-party plugin — `rta plugin install pg` installs it",
		"docker":   "docker is a first-party plugin — `rta plugin install docker` installs it",
		"qdrant":   "qdrant is a first-party plugin — `rta plugin install qdrant` installs it",
		"postgres": "postgres is the first-party plugin pg — `rta plugin install pg` installs it",
	} {
		_, _, err := run(word)
		verr := codedError(t, err)
		if strings.Contains(verr.Message, "closest") {
			t.Errorf("rta %s offered a neighbour: %q", word, verr.Message)
		}
		if verr.Hint != want {
			t.Errorf("rta %s: hint = %q, want %q", word, verr.Hint, want)
		}
	}

	_, _, err := run("mongo")
	if hint := codedError(t, err).Hint; strings.Contains(hint, "plugin") {
		t.Errorf("rta mongo was pointed at a plugin that does not exist: %q", hint)
	}
}

// `rta explain pg.query` and `rta dashboard add pg.query` said the capability was unknown and left
// the person to work out that the plugin was not installed.
func TestAnUnknownCapabilityOfAFirstPartyPluginNamesTheInstall(t *testing.T) {
	run := session(t, registry.New())
	for _, args := range [][]string{
		{"explain", "pg.query"},
		{"dashboard", "add", "pg.query"},
	} {
		_, _, err := run(args...)
		if hint := codedError(t, err).Hint; !strings.Contains(hint, "`rta plugin install pg`") {
			t.Errorf("rta %s: hint = %q, want the install", strings.Join(args, " "), hint)
		}
	}
}
