// Package config loads rta's configuration. Zero config is a valid config:
// everything works without a file, and rta init writes
// one interactively when the user wants persistent choices.
//
// v0 keeps loading deliberately small (goccy-yaml, already a dependency,
// plus one env override). When profiles land (M2) the internals move to
// koanf layering behind this same API.
package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/filelock"
	"github.com/this-is-tobi/rta/internal/yamlguard"
	"github.com/this-is-tobi/rta/pkg/view"
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
	// Plugins holds each plugin's own settings, so an operator states a
	// connection once instead of retyping it on every invocation.
	//
	// The key is not a namespace. It is a namespace for a built-in and
	// `<namespace>@<digest>` for anything on $PATH, because a plugin's
	// namespace is something the plugin declares about itself and $PATH order
	// decides who gets to declare it first. internal/pluginconf carries the
	// whole argument and does the matching; this is untyped here because what
	// a key means is decided by the capability that declared it, and rta must
	// not need to know a plugin's schema in order to hand it its own file.
	Plugins map[string]map[string]any `yaml:"plugins,omitempty" json:"plugins,omitempty"`
	// Profiles are named connections an operator switches between with
	// --profile and issues agent grants against, keyed by profile name.
	//
	// Top level, and deliberately NOT under Plugins. The TUI's plugin-config
	// form writes a namespace's section back wholesale, so a profiles block
	// living inside one would be deleted the first time somebody edited that
	// plugin's config — and the deletion fails *open* for a connection: pg
	// falls back to its declared localhost:5432 while a credential still
	// resolves, so "connect to prod" silently becomes "connect to whatever is
	// on localhost, with the prod password". The placement is a security
	// constraint rather than tidiness.
	Profiles map[string]Profile `yaml:"profiles,omitempty" json:"profiles,omitempty"`
	// Theme overrides the built-in palette. Keys are the names
	// internal/render/theme.Fields lists ("primary", "good", "label", …),
	// each a "#rrggbb" string.
	//
	// Untyped for the same reason Plugins is: what a key means and whether a
	// value is valid is internal/render/theme.Apply's decision, not this
	// package's, and config stays the leaf package it already was — loaded
	// before anything has decided whether this run even has a renderer to
	// color, and read by `rta mcp serve` too, which colors nothing at all.
	Theme map[string]string `yaml:"theme,omitempty" json:"theme,omitempty"`

	// trusted records that this file is one somebody named, rather than the
	// ./.rta.yaml fallback. Unexported and set by the loader, exactly as
	// Profile.trusted is: a config file must not be able to declare itself
	// trustworthy, so `trusted: true` in a hostile file is an unclaimed key
	// rather than a self-issued grant.
	//
	// On the file rather than per block, because provenance is a fact about
	// the file. LoadFile asks once and three readers get the same answer:
	// profiles (internal/profile's Lookup and Check), plugin sections
	// (internal/pluginconf.Resolve) and the dashboard (TrustedDashboard
	// below). Stamping each block separately is how the second one came to be
	// forgotten for a whole release.
	trusted bool
}

// Trusted reports whether this configuration came from a path somebody named.
func (c Config) Trusted() bool { return c.trusted }

// TrustedDashboard is the arrangement to actually draw: the stated one when
// somebody named this config file, and the empty one otherwise — which is not
// a blank screen but the automatic dashboard, one tile per registered plugin,
// exactly what a machine with no dashboard: block already gets.
//
// buildTiles already refuses a tile that is not plugin.Read, because a tile
// runs on load and again on a timer with no form and no confirmation. But
// http.get IS Read and takes a caller-chosen URL, so `{id: http.get, with:
// {url: …}}` in a cloned repository's ./.rta.yaml is a beacon that starts the
// moment somebody opens the TUI in that directory. `hidden:` is the same
// hazard pointed the other way, and is why this refuses the whole block
// rather than only `tiles:`: it can take the agent tile off the screen, and
// that tile is where a person notices a parked consent request before its
// clock runs out.
func (c Config) TrustedDashboard() Dashboard {
	if !c.trusted {
		return Dashboard{}
	}
	return c.Dashboard
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

// parseHint turns the YAML parser's own message into a next step.
//
// A repeated mapping key earns its own sentence because the general advice is
// actively wrong for it: the file is not corrupt, and re-creating it with
// `rta init` would throw away every profile in it to fix one duplicated line.
//
// It is also the one parse error rta's own writers cannot produce — a
// profile's plugins: block is a Go map, so marshalling it can only ever emit a
// key once. Reaching this means the file was edited by hand, and in practice
// for one reason: a connection is keyed by plugin namespace and pin, so
// somebody adding a second database of the same kind copies the block and gets
// a second `pg@<digest>:` that collides with the first instead of joining it.
// Saying so is the difference between a fix and an afternoon.
func parseHint(err error) string {
	if strings.Contains(err.Error(), "already defined") {
		return "a profile holds one connection per plugin, keyed `<plugin>@<digest>`, so a " +
			"second connection for the same plugin replaces that key rather than adding to " +
			"it — give each one its own profile instead of repeating the key"
	}
	return "fix the file or re-create it with `rta init`"
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
		if err := yamlguard.RefuseAnchors(data); err != nil {
			return cfg, view.Errorf("config.invalid", "parsing %s: %v", Path(), err).
				WithHint(parseHint(err))
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, view.Errorf("config.invalid", "parsing %s: %v", Path(), err).
				WithHint(parseHint(err))
		}
	}
	// Stamped here, once, rather than asked at each point of use: a file that
	// came from the working-directory fallback carries trusted=false for the
	// rest of its life, and nothing downstream has to remember to check where
	// the file was. The field is unexported so the answer can only come from
	// this line — a config file cannot declare itself trustworthy.
	//
	// Outside the profiles branch, because provenance is a fact about the
	// file rather than about one block. Scoping it to profiles is precisely
	// what left `plugins:` and `dashboard:` honoured from a file nobody
	// named.
	cfg.trusted = trustedPath()
	if len(cfg.Profiles) > 0 {
		// Read back the raw profiles block to find keys no field claimed.
		// goccy drops an unrecognised key without a word, and a profile is
		// where that costs the most: `plguin: pg` is one keystroke from a
		// working profile and otherwise indistinguishable from one.
		var raw struct {
			Profiles map[string]map[string]any `yaml:"profiles"`
		}
		_ = yaml.Unmarshal(data, &raw)
		for name, p := range cfg.Profiles {
			p.trusted = cfg.trusted
			p.unknown = unclaimed(raw.Profiles[name], profileKeys)
			// One level down, where a migration lands: the single-plugin shape
			// put `set:` and `secrets:` directly under the profile, and those
			// same words are legal under a plugin entry. Reading both levels is
			// what tells "you have not migrated this yet" apart from "you
			// misspelled a key inside pg".
			nested, _ := raw.Profiles[name]["plugins"].(map[string]any)
			for key, conn := range p.Plugins {
				fields, _ := nested[key].(map[string]any)
				conn.unknown = unclaimed(fields, connectionKeys)
				p.Plugins[key] = conn
			}
			cfg.Profiles[name] = p
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

// lockFile is the sentinel beside the config file, named after the file
// rather than the directory so that RTA_CONFIG pointing somewhere else does
// not queue behind the default path.
const lockFile = ".lock"

// Mutate applies f to the configuration under a lock and writes the result,
// so a read-modify-write cannot lose another writer's.
//
// **Every writer has to use this, and the reason is measured.** Config is
// edited by nine places — five in the profile forms, the plugin and theme
// forms, the dashboard arrangement, `rta init` — and each of them was doing
// LoadFile, mutate, Write with nothing in between stopping a second writer
// from doing the same and one of them silently losing. That was survivable
// while every writer was a keystroke in a form: a person cannot press two
// keys in two processes at once.
//
// `rta profile set` ends that. It is built to be scripted, and a script that
// states four environments states them in parallel as readily as in sequence.
// Eight concurrent writes to one config lost between one and three of them on
// three runs out of five, with all eight reporting success — which for this
// file means a profile an operator believes exists, and therefore a
// `--profile staging` that quietly reaches the base configuration instead.
//
// The identical shape has been fixed twice already, in internal/grant and in
// builtin/kv, and internal/filelock exists because of it. This is the third
// resource to need it, and it takes that same lock rather than growing a
// third copy of the argument.
//
// f receives the file as it is *now*, never a copy read earlier, and returns
// what to write plus whether to write it — so a caller that decides mid-edit
// to refuse returns false, nothing is written, and the lock is still released
// on the ordinary path.
func Mutate(f func(Config) (Config, bool)) error {
	release, err := lock()
	if err != nil {
		return err
	}
	defer release()

	// LoadFile, not Load: Load folds this session's RTA_* over the file, and
	// writing that back would bake one shell's environment into the file for
	// every future run.
	cfg, err := LoadFile()
	if err != nil {
		return err
	}
	next, save := f(cfg)
	if !save {
		return nil
	}
	return write(next)
}

// lock serializes access to the config file.
func lock() (func(), error) {
	path := Path()
	release, err := filelock.Acquire(filepath.Join(filepath.Dir(path), filepath.Base(path)+lockFile),
		filelock.DefaultStale, filelock.DefaultRetry, filelock.DefaultTimeout)
	if err != nil {
		return nil, view.Errorf("config.lock", "acquiring the config file lock: %v", err)
	}
	return release, nil
}

// Write replaces the whole file with cfg, under the same lock.
//
// **This is not the writer to reach for.** It states the entire config, so a
// caller that read the file, changed part of it and calls this has already
// lost whatever another writer did in between — a lock cannot help with a
// value decided before it was taken. Everything that modifies part of the
// configuration goes through Mutate, and no production caller is left here;
// what remains are tests and any caller genuinely stating the whole file with
// nothing to merge.
//
// It takes the lock regardless, so such a caller cannot interleave with a
// Mutate already in flight and truncate its result.
func Write(cfg Config) error {
	release, err := lock()
	if err != nil {
		return err
	}
	defer release()
	return write(cfg)
}

// write persists cfg to Path(), creating parent directories. Atomically: the
// dashboard saves the arrangement on every tile move, so this is the file rta
// rewrites most often and the one a torn write would cost the user a
// `config.invalid` on every subsequent run.
func write(cfg Config) error {
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
	// A rewrite must not change a file's permissions, so an existing config
	// keeps whatever mode it has; a new one gets the mode rta has always
	// asked for.
	perm := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	if err := atomicfile.Write(path, append([]byte(header), data...), perm); err != nil {
		return view.Errorf("config.write", "writing %s: %v", path, err)
	}
	return nil
}
