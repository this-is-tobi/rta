// Package config loads rta's configuration. Zero config is a valid config:
// everything works without a file, and rta init writes
// one interactively when the user wants persistent choices.
//
// v0 keeps loading deliberately small (goccy-yaml, already a dependency,
// plus one env override). When profiles land (M2) the internals move to
// koanf layering behind this same API.
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
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
		if err := refuseAnchors(data); err != nil {
			return cfg, view.Errorf("config.invalid", "parsing %s: %v", Path(), err).
				WithHint("fix the file or re-create it with `rta init`")
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, view.Errorf("config.invalid", "parsing %s: %v", Path(), err).
				WithHint("fix the file or re-create it with `rta init`")
		}
	}
	// Stamped here, once, rather than asked at each point of use: a profile
	// that came from the working-directory fallback carries trusted=false for
	// the rest of its life, and nothing downstream has to remember to check
	// where the file was. The field is unexported so the answer can only come
	// from this line — a config file cannot declare itself trustworthy.
	if len(cfg.Profiles) > 0 {
		trusted := trustedPath()
		// Read back the raw profiles block to find keys no field claimed.
		// goccy drops an unrecognised key without a word, and a profile is
		// where that costs the most: `plguin: pg` is one keystroke from a
		// working profile and otherwise indistinguishable from one.
		var raw struct {
			Profiles map[string]map[string]any `yaml:"profiles"`
		}
		_ = yaml.Unmarshal(data, &raw)
		for name, p := range cfg.Profiles {
			p.trusted = trusted
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

// refuseAnchors rejects a config that uses a YAML anchor or alias
// (&name / *name) before yaml.Unmarshal ever gets to decode it.
//
// Config has no legitimate use for either: it is a flat, known set of
// fields (Output, Dashboard, Plugins, Theme), none of which benefit from
// YAML's own value-reuse syntax. What they enable instead is a "billion
// laughs" bomb — a handful of nested anchor/alias pairs, each fanning out
// 10x into the next, expands a few hundred bytes of syntactically valid
// YAML into 10^9+ decoded values, exhausting memory. And LoadFile runs at
// the start of essentially every rta command, including read-only ones, so
// simply having such a file at the config path (RTA_CONFIG, or the default
// path — both routinely shared between operators and machines) denies
// service on every subsequent invocation, not just once.
//
// Rather than pick a "safe" nesting depth or alias count a cleverer bomb
// could still clear, this refuses the syntax outright — checked by parsing
// alone, never decoding: goccy's parser builds a graph proportional to the
// bytes on disk (an AliasNode holds the name it references, not a
// dereferenced copy of the anchor's subtree), so walking that graph costs
// exactly what parsing it already did, before anything is substituted.
func refuseAnchors(data []byte) error {
	file, err := parser.ParseBytes(data, 0)
	if err != nil {
		// yaml.Unmarshal will hit and report the identical parse failure
		// with its own message; this just isn't it.
		return nil
	}
	v := &anchorVisitor{}
	for _, doc := range file.Docs {
		ast.Walk(v, doc)
		if v.found {
			// The matched node is deliberately not echoed into the message:
			// an AnchorNode's own String() walks the value it anchors, and
			// while that is bounded by what is actually written in the
			// file (never a dereferenced, expanded copy), there is no
			// reason to stringify attacker-controlled YAML into an error
			// message when naming the rule is enough.
			return errAnchorsNotSupported
		}
	}
	return nil
}

var errAnchorsNotSupported = errors.New(
	"uses a YAML anchor or alias (&name / *name), which this file does not support")

type anchorVisitor struct{ found bool }

func (v *anchorVisitor) Visit(n ast.Node) ast.Visitor {
	if v.found {
		return nil
	}
	switch n.(type) {
	case *ast.AnchorNode, *ast.AliasNode:
		v.found = true
		return nil
	}
	return v
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

// Write persists cfg to Path(), creating parent directories. Atomically:
// the dashboard saves the arrangement on every tile move, so this is the
// file rta rewrites most often and the one a torn write would cost the
// user a `config.invalid` on every subsequent run.
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
