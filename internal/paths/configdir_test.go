package paths

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// isolateConfig points every source OwnConfigDir consults at nothing, so each
// test below names the ones it means.
func isolateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
}

// The configuration lives where the data does: $XDG_CONFIG_HOME/rta, else
// ~/.config/rta, on every OS. macOS used to answer ~/Library/Application
// Support, which ignored XDG_CONFIG_HOME while the data directory beside it
// honoured XDG_DATA_HOME.
func TestOwnConfigDirIsXDGElseDotConfigOnEveryOS(t *testing.T) {
	for _, c := range []struct {
		name string
		xdg  string
		home string
		want string
	}{
		{"XDG_CONFIG_HOME wins", "/xdg", "/home/ada", "/xdg/rta"},
		{"the default is ~/.config", "", "/home/ada", "/home/ada/.config/rta"},
		{"a relative XDG_CONFIG_HOME is ignored, as the specification says", "conf", "/home/ada", "/home/ada/.config/rta"},
		{"no home and no XDG names no directory", "", "", ""},
		{"a relative home names no directory", "", "ada", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			isolateConfig(t)
			t.Setenv("XDG_CONFIG_HOME", c.xdg)
			t.Setenv("HOME", c.home)
			if got := OwnConfigDir(); got != c.want {
				t.Errorf("OwnConfigDir() = %q, want %q", got, c.want)
			}
		})
	}
}

// The file is config.yaml in that directory, RTA_CONFIG overrides it, and a
// machine with no directory at all falls back to the working-directory file —
// the one config.LoadFile refuses to honour.
func TestConfigFileFollowsTheDirectoryThenTheOverrideThenTheWorkingDirectory(t *testing.T) {
	isolateConfig(t)
	t.Setenv("HOME", "/home/ada")
	if got, want := ConfigFile(), "/home/ada/.config/rta/config.yaml"; got != want {
		t.Errorf("ConfigFile() = %q, want %q", got, want)
	}
	t.Setenv("RTA_CONFIG", "/etc/rta.yaml")
	if got := ConfigFile(); got != "/etc/rta.yaml" {
		t.Errorf("ConfigFile() = %q, want the RTA_CONFIG override", got)
	}
	isolateConfig(t)
	if got, want := ConfigFile(), filepath.Join(".", ".rta.yaml"); got != want {
		t.Errorf("ConfigFile() with no config directory = %q, want %q", got, want)
	}
}

// What is left at the old macOS location needs a name, since nothing reads it
// now. Where the platform's own directory is the new one (Linux) there is
// nothing to name, a directory that is gone is not worth a row, and
// RTA_CONFIG moves the file out of the question entirely.
func TestLegacyConfigDirIsOnlyAPlaceRtaNoLongerReadsThatStillExists(t *testing.T) {
	isolateConfig(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := filepath.Join(home, "Library", "Application Support", "rta")
	was := userConfigDir
	t.Cleanup(func() { userConfigDir = was })
	asMacOS := func() (string, error) { return filepath.Join(home, "Library", "Application Support"), nil }

	userConfigDir = asMacOS
	if got := LegacyConfigDir(); got != "" {
		t.Errorf("LegacyConfigDir() = %q for a directory that is not there", got)
	}
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := LegacyConfigDir(); got != old {
		t.Errorf("LegacyConfigDir() = %q, want %q", got, old)
	}

	userConfigDir = func() (string, error) { return filepath.Join(home, ".config"), nil }
	if got := LegacyConfigDir(); got != "" {
		t.Errorf("LegacyConfigDir() = %q where the platform's own directory is the new one", got)
	}
	userConfigDir = func() (string, error) { return "", errors.New("no home") }
	if got := LegacyConfigDir(); got != "" {
		t.Errorf("LegacyConfigDir() = %q on a platform with none", got)
	}

	userConfigDir = asMacOS
	t.Setenv("RTA_CONFIG", filepath.Join(home, "elsewhere.yaml"))
	if got := LegacyConfigDir(); got != "" {
		t.Errorf("LegacyConfigDir() = %q with RTA_CONFIG set: the old directory never held that file", got)
	}
}
