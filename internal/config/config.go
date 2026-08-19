// Package config loads rta's configuration. Zero config is a valid config
// (PROJECT.md §4.6): everything works without a file, and rta init writes
// one interactively when the user wants persistent choices.
//
// v0 keeps loading deliberately small (goccy-yaml, already a dependency,
// plus one env override). When profiles land (M2) the internals move to
// koanf layering behind this same API.
package config

import (
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Tile configures one dashboard pane: a capability and optional inputs.
type Tile struct {
	ID   string         `yaml:"id" json:"id"`
	With map[string]any `yaml:"with,omitempty" json:"with,omitempty"`
	// Span is how many grid columns this tile occupies, overriding what the
	// capability's declared MinWidth works out to. 0 leaves that decision to
	// the capability, which is right almost always — this is for the person
	// who wants their task list twice as wide as everything else because
	// that is the one they read.
	Span int `yaml:"span,omitempty" json:"span,omitempty"`
}

// Dashboard configures the landing screen.
//
// With none of these set the dashboard builds itself: one tile per
// registered plugin, so nothing a plugin offers is invisible — including
// plugins installed later. Hidden and Order adjust that automatic set
// without freezing it, which is why they are separate from Tiles: a plugin
// added next month still appears. Tiles is the escape hatch for people who
// want to state the whole dashboard themselves, and it replaces the
// automatic set entirely.
type Dashboard struct {
	// Tiles states the dashboard exactly. When set, Hidden and Order are
	// not consulted — the list is already both.
	Tiles []Tile `yaml:"tiles,omitempty" json:"tiles,omitempty"`
	// Hidden lists capability IDs to leave out of the automatic set.
	Hidden []string `yaml:"hidden,omitempty" json:"hidden,omitempty"`
	// Order lists capability IDs to place first, in this order. Anything
	// not named keeps its natural position after them.
	Order []string `yaml:"order,omitempty" json:"order,omitempty"`
	// Columns fixes the grid width instead of deriving it from the terminal.
	// 0 means automatic, which is what almost everybody wants: the dashboard
	// was two columns at every size, so a 200-cell terminal drew two
	// 100-cell tiles of a six-line summary and called it a screen.
	Columns int `yaml:"columns,omitempty" json:"columns,omitempty"`
}

// Config is the persisted configuration.
type Config struct {
	// Output is the default --output format when the flag is not given.
	Output    string    `yaml:"output,omitempty" json:"output,omitempty"`
	Dashboard Dashboard `yaml:"dashboard,omitempty" json:"dashboard,omitempty"`
}

// Path returns the config file location. RTA_CONFIG overrides it (tests,
// portable setups).
func Path() string {
	if p := os.Getenv("RTA_CONFIG"); p != "" {
		return p
	}
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, "rta", "config.yaml")
	}
	return filepath.Join(".", ".rta.yaml")
}

// LoadFile reads the config file alone (missing file = defaults), without
// applying environment overrides. Anything that reads the config in order to
// write it back must start here: Load would fold this session's RTA_* into
// the value, and saving that would bake one shell's environment into the
// file for every future run.
func LoadFile() (Config, error) {
	var cfg Config
	data, err := os.ReadFile(Path())
	switch {
	case os.IsNotExist(err):
		// Zero-config mode.
	case err != nil:
		return cfg, view.Errorf("config.unreadable", "reading %s: %v", Path(), err)
	default:
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, view.Errorf("config.invalid", "parsing %s: %v", Path(), err).
				WithHint("fix the file or re-create it with `rta init`")
		}
	}
	return cfg, nil
}

// Load reads the config file and applies env overrides. Precedence:
// flags > env (RTA_*) > file > defaults; the flag layer is cobra's,
// everything else is resolved here.
func Load() (Config, error) {
	cfg, err := LoadFile()
	if err != nil {
		return cfg, err
	}
	if out := os.Getenv("RTA_OUTPUT"); out != "" {
		cfg.Output = out
	}
	return cfg, nil
}

// Write persists cfg to Path(), creating parent directories.
func Write(cfg Config) error {
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return view.Errorf("config.mkdir", "creating %s: %v", filepath.Dir(path), err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return view.Errorf("config.encode", "encoding config: %v", err)
	}
	header := "# rta configuration — created by `rta init`.\n" +
		"# Everything here is optional: rta works with no config at all.\n"
	if err := os.WriteFile(path, append([]byte(header), data...), 0o644); err != nil {
		return view.Errorf("config.write", "writing %s: %v", path, err)
	}
	return nil
}
