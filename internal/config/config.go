// Package config loads rta's configuration. Zero config is a valid config:
// everything works without a file, and rta init writes one interactively when
// the user wants persistent choices.
//
// Loading is deliberately small — goccy-yaml, already a dependency, plus the
// RTA_* environment overrides — and stays so on purpose: the file's shape is
// the product's own, its precedence rules are stated in this package where a
// reader can check them, and a layering framework would decide those rules
// for it.
package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/filelock"
	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/internal/shellquote"
	"github.com/this-is-tobi/rta/internal/yamlguard"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Tile configures one dashboard pane: a capability and optional inputs.
type Tile struct {
	ID   string         `yaml:"id" json:"id"`
	With map[string]any `yaml:"with,omitempty" json:"with,omitempty"`
	// Profile pins the tile to one configured connection — `prod`, or
	// `staging/analytics` for a labeled instance — the way --profile does on
	// the CLI. Empty, the tile follows whatever environment is switched on,
	// which is what every tile did before this field existed and is still
	// right for a tile about *here*: switch to staging and the pg tile is
	// about staging. Named, the tile is about that connection whatever is
	// switched on, which is what lets one capability sit on the dashboard
	// twice, once per cluster, with the name on each tile.
	//
	// A field of its own rather than a key under With, because a profile is
	// the host's question and not the capability's: With fills declared
	// inputs, and nothing declares an input called profile. A `profile:`
	// written under `with:` reached the handler as a key it never read, so
	// the tile refreshed against the switched-on environment regardless —
	// while enter on that same tile, which goes through the form's own
	// picker, ran against the named one. The screen and the page disagreed
	// about where the numbers came from, in the direction that hides it.
	Profile string `yaml:"profile,omitempty" json:"profile,omitempty"`
	// Span is how many grid columns this tile occupies, overriding what the
	// capability's declared MinWidth works out to. 0 leaves that decision to
	// the capability, which is right almost always — this is for the person
	// who wants their task list twice as wide as everything else because
	// that is the one they read.
	Span int `yaml:"span,omitempty" json:"span,omitempty"`
}

// Key is how Order refers to a tile and how `rta dashboard rm` addresses
// one: the capability alone, or capability@profile for a tile pinned to a
// connection. Two tiles of one capability against two profiles are two
// keys, which is what lets each be moved and removed on its own. Two
// entries that share a key — the same capability against the same
// connection, differing only in With — are one tile to those operations.
func (t Tile) Key() string { return TileKey(t.ID, t.Profile) }

// TileKey spells a tile's key from its parts; see Tile.Key.
func TileKey(id, profile string) string {
	if profile == "" {
		return id
	}
	return id + "@" + profile
}

// AddArgs spells this entry as the arguments `rta dashboard add` takes to
// write it again. The receipts and the TUI's footer name the way back as
// the exact command, and the whole entry has to be in it: one that took an
// automatic tile's place is refused without the --span or --set that made
// it more than that tile. A list-shaped input is one --set per element,
// which is how the flag states a list.
//
// Each argument is one shell word. Joined as they came, a value holding a
// space, a ';' or a '$(' made a line that, pasted, passed a stray argument
// or ran a second command rather than put the tile back, and one holding an
// escape sequence printed as nothing at all.
func (t Tile) AddArgs() string {
	args := []string{t.ID}
	if t.Profile != "" {
		args = append(args, "--profile", t.Profile)
	}
	if t.Span > 0 {
		args = append(args, "--span", strconv.Itoa(t.Span))
	}
	keys := make([]string, 0, len(t.With))
	for k := range t.With {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := t.With[k].(type) {
		case []any:
			for _, each := range v {
				args = append(args, "--set", fmt.Sprintf("%s=%v", k, each))
			}
		case []string:
			for _, each := range v {
				args = append(args, "--set", k+"="+each)
			}
		default:
			args = append(args, "--set", fmt.Sprintf("%s=%v", k, v))
		}
	}
	for i, arg := range args {
		args[i] = shellquote.Arg(arg)
	}
	return strings.Join(args, " ")
}

// Dashboard configures the landing screen.
//
// With none of these set the dashboard builds itself: one tile per
// registered plugin, so nothing a plugin offers is invisible — including
// plugins installed later. Hidden, Order and Add adjust that automatic set
// without freezing it, which is why they are separate from Tiles: a plugin
// added next month still appears. Tiles is the escape hatch for people who
// want to state the whole dashboard themselves, and it replaces the
// automatic set entirely.
type Dashboard struct {
	// Tiles states the dashboard exactly. When set, Hidden and Order are
	// not consulted — the list is already both — and Add is appended after
	// it, though a person stating the dashboard can as well write a pinned
	// tile into this list directly.
	Tiles []Tile `yaml:"tiles,omitempty" json:"tiles,omitempty"`
	// Add joins named tiles to the automatic set. It is how a capability
	// the automatic dashboard leaves out gets on screen — every kube, pg,
	// s3 and vault capability declines to run unasked, and an entry here is
	// the asking — and how one capability appears more than once, each
	// entry pinned to its own Profile. Removing the entry is what takes such
	// a tile down: Hidden is about the automatic set, and an entry somebody
	// wrote is not hidden but withdrawn. `rta dashboard add` and `rm` write
	// this list; so does the TUI.
	Add []Tile `yaml:"add,omitempty" json:"add,omitempty"`
	// Hidden lists what to leave off the screen: a capability ID, which
	// hides an automatic tile and every panel it expanded into, or a tile
	// key (Tile.Key), which hides that one panel of an entry that expanded
	// into several connections. An added entry is withdrawn, not hidden.
	Hidden []string `yaml:"hidden,omitempty" json:"hidden,omitempty"`
	// Order lists tile keys (Tile.Key) to place first, in this order.
	// Anything not named keeps its natural position after them.
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
	// Roles are the operator's own bundles for `rta grant issue`: a name,
	// a list of grant lines, a default duration. A role here is the
	// operator's word for something they would otherwise type line by
	// line; the team's live in the policy file beside the ceiling that
	// bounds them. See internal/role.
	Roles map[string]Role `yaml:"roles,omitempty" json:"roles,omitempty"`

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

// Role is what one `rta grant issue` issues: grant lines in the grammar
// `rta grant allow` parses, and how long the grants last unless the
// operator says otherwise. Adding one to a file grants nothing — a person
// issues it, under the guard where there is one, and the ceiling caps each
// line; what it saves is one prompt per line.
type Role struct {
	Grants []string `yaml:"grants" json:"grants"`
	TTL    string   `yaml:"ttl,omitempty" json:"ttl,omitempty"`
	// Agent is who this role is issued to when `grant issue` is not told:
	// the morning's --agent, written once. Honoured from the operator's own
	// config only — a policy file naming a principal is refused, because
	// which agent receives a list is the one allow-shaped decision a
	// repository edit must not make.
	Agent string `yaml:"agent,omitempty" json:"agent,omitempty"`
}

// Trusted reports whether this configuration came from a path somebody named.
func (c Config) Trusted() bool { return c.trusted }

// Stamp returns p carrying this file's provenance, as the loader stamps every
// profile it reads from it.
//
// For a profile built in memory to be written into c: `rta profile set
// --dry-run` reports the card of a profile nothing wrote, and a new one is a
// zero Profile, untrusted, so the preview named a working-directory file
// beside a write that is honoured. The answer has to come from a Config the
// loader stamped rather than from a bool the caller passes, or any caller
// could assert what the unexported field exists to keep from being asserted.
func (c Config) Stamp(p Profile) Profile {
	p.trusted = c.trusted
	return p
}

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
func Path() string { return paths.ConfigFile() }

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
			p = cfg.Stamp(p)
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
//
// The overrides apply over a file that does not parse too, beside its error.
// The CLI carries on without a broken file rather than refusing to start, and
// it used to carry on without the environment as well: `RTA_OUTPUT=json`
// answered a script in pretty prose whenever the file had a typo in it. Every
// other caller stops at the error, so what comes back with it is only ever
// read by the one that does not.
func Load() (Config, error) {
	cfg, err := LoadFile()
	if out := os.Getenv("RTA_OUTPUT"); out != "" {
		cfg.Output = out
	}
	return cfg, err
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
	// Owner-only, the same as the data directory: this directory holds the
	// names of every environment, the `secrets:` references that point at
	// them, and remotes.yaml beside it — and plugin confinement already
	// denies it to plugins for exactly that reason (internal/pluginhost's
	// tier1). An existing directory keeps whatever mode it has.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
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
