package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"

	"github.com/this-is-tobi/rta/pkg/view"
)

// Dashboard views: more than one landing screen, and which one a profile shows.
//
// **Why a view and not a second dashboard: block per profile.** The dashboard
// is the one thing rta draws that is about *where you are*, and a person who
// works against three environments wants three different screens — the staging
// one with staging's cluster and database, the production one with the
// production tiles and nothing else — while a profile is, by rule, a set of
// connections and not an arrangement. So the arrangement is named on its own
// (`dashboard: views: <name>:`), and a profile points at one
// (`profiles.<name>.dashboard: <view>`); switching to the profile draws it.
// A view is not tied to the profile that selects it: its tiles pin themselves
// to whichever profiles they name (Tile.Profile), so a view for the databases
// can show every database of every environment, and several profiles can
// select it.
//
// **A view replaces the dashboard: block while it is drawn; it does not
// overlay it.** An overlay would need a rule for every field — does a view's
// `hidden:` add to the default's, does its `tiles:` replace or extend — and
// each answer is one more thing to hold in mind to know what a screen shows.
// A view is a whole arrangement, the same five fields the block has, and the
// one that is drawn is the one whose fields apply.

// DefaultView is the name that addresses the `dashboard:` block itself, for the
// commands that take a view: not a view of its own, and not a name one can be
// given.
const DefaultView = "default"

// viewName is what a view may be called: the grammar a profile name has, so
// that one name works in a flag, a key and a sentence.
var viewName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ValidViewName reports whether name can be a view's: lower case, digits and
// hyphens, starting with a letter, at most 32 long, and not the word that
// addresses the block.
func ValidViewName(name string) bool { return name != DefaultView && viewName.MatchString(name) }

// View is one named arrangement of the landing screen: the five fields of the
// `dashboard:` block, and no views of its own.
type View struct {
	Tiles   []Tile   `yaml:"tiles,omitempty" json:"tiles,omitempty"`
	Add     []Tile   `yaml:"add,omitempty" json:"add,omitempty"`
	Hidden  []string `yaml:"hidden,omitempty" json:"hidden,omitempty"`
	Order   []string `yaml:"order,omitempty" json:"order,omitempty"`
	Columns int      `yaml:"columns,omitempty" json:"columns,omitempty"`
}

// Dashboard is v as the arrangement every reader of one takes.
func (v View) Dashboard() Dashboard {
	return Dashboard{Tiles: v.Tiles, Add: v.Add, Hidden: v.Hidden, Order: v.Order, Columns: v.Columns}
}

// View is d as a view, without the views it names.
func (d Dashboard) View() View {
	return View{Tiles: d.Tiles, Add: d.Add, Hidden: d.Hidden, Order: d.Order, Columns: d.Columns}
}

// ProfileDashboard is what a profile's `dashboard:` says: the name of a view
// that lives under `dashboard: views:`, or the screen itself, written in the
// profile.
//
//	profiles:
//	  prod:
//	    dashboard: ops              # the view called ops, shared by whoever names it
//	  staging:
//	    dashboard:                  # staging's own screen, with the keys a view has
//	      add:
//	      - id: kube.metrics.node
//
// **Why the screen can be written in place when views exist to be named.** A
// view earns its name when several environments show it. The commoner case is
// one environment with a screen of its own, and for that the name is a second
// stanza to keep beside the profile and a word that has to agree in both: a
// person arranging staging's screen wants to open staging's entry and find it
// there, and to move the entry — to another file, to another machine — and have
// the screen go with it. A profile is already one unit that lives in one file
// (layers.go), so a block in it is stated exactly where its environment is, and
// is merged, written and refused by the same rules the rest of the entry is.
//
// **A block is a view named after its profile, and nothing more.** Every
// mechanism there is for views — `rta dashboard views` and `--view`, the TUI
// drawing it when the environment is switched on and `V` cycling to it, the
// writers that add, hide and remove a tile — already works on a name, so the
// block is given one, the profile's, instead of being taught to each of them
// as a second kind of screen. The Config methods below are where the two kinds
// are read as one (stated says how a name is resolved); a writer asks where the
// block lives and the answer is the profile's entry rather than a `views:`
// entry that would then state the same name twice.
//
// Exactly one of the two is set. The block wins in MarshalYAML if a caller sets
// both, which the loader cannot produce: a value is a string or a mapping.
type ProfileDashboard struct {
	// Name is the view this profile selects, "" when it names none.
	Name string
	// Inline is the profile's own screen, nil when it states none. An empty one
	// is a screen with nothing arranged, which draws the automatic dashboard:
	// the way to say that this environment does not show the `dashboard:` block.
	Inline *View

	// bad is what the loader found where a name or a block belongs, so that the
	// refusal can name the profile and the file, which the decoder, handed one
	// value, does not know.
	bad *badDashboard
}

// badDashboard is a `dashboard:` that is neither of the two things it can be.
type badDashboard struct {
	what      string
	line, col int
}

// UnmarshalYAML reads a string as the name of a view and a mapping as the
// screen. Anything else is kept for the loader to refuse in its own words
// (refuseBadDashboards) instead of becoming the decoder's sentence about Go
// types, which names neither the profile nor what would have been accepted.
func (d *ProfileDashboard) UnmarshalYAML(node ast.Node) error {
	*d = ProfileDashboard{}
	switch n := node.(type) {
	case *ast.NullNode:
	case *ast.StringNode:
		d.Name = n.Value
	case *ast.MappingNode, *ast.MappingValueNode:
		var v View
		if err := yaml.NodeToValue(node, &v); err != nil {
			return err
		}
		d.Inline = &v
	default:
		bad := &badDashboard{what: describeNode(node)}
		if tok := node.GetToken(); tok != nil {
			bad.line, bad.col = tok.Position.Line, tok.Position.Column
		}
		d.bad = bad
	}
	return nil
}

// describeNode is what a YAML value is, in the words a person who wrote it
// would use.
func describeNode(node ast.Node) string {
	switch node.(type) {
	case *ast.SequenceNode:
		return "a list"
	case *ast.IntegerNode, *ast.FloatNode, *ast.InfinityNode, *ast.NanNode:
		return "a number"
	case *ast.BoolNode:
		return "true or false"
	case *ast.LiteralNode:
		return "a block of text"
	default:
		return "not a name or a mapping"
	}
}

// MarshalYAML writes the block when there is one and the name otherwise, which
// is what keeps a profile that names a view written the way it always was.
func (d ProfileDashboard) MarshalYAML() (any, error) {
	if d.Inline != nil {
		return *d.Inline, nil
	}
	return d.Name, nil
}

// MarshalJSON is MarshalYAML's twin, for the callers that print a profile as
// JSON.
func (d ProfileDashboard) MarshalJSON() ([]byte, error) {
	v, _ := d.MarshalYAML()
	return json.Marshal(v)
}

// IsZero says the profile states no screen, so that `omitempty` leaves the key
// out of what a write produces — an empty name and a nil block are the same
// absence, and an empty block is not.
func (d ProfileDashboard) IsZero() bool { return d.Name == "" && d.Inline == nil }

// refuseBadDashboards is the loader's refusal of a profile whose `dashboard:`
// is neither a name nor a block, naming the file, the profile and the spot, the
// first in profile-name order so that the same file always gives the same one.
func refuseBadDashboards(path string, profiles map[string]Profile) error {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		bad := profiles[name].Dashboard.bad
		if bad == nil {
			continue
		}
		spot := ""
		if bad.line > 0 {
			spot = fmt.Sprintf("[%d:%d] ", bad.line, bad.col)
		}
		return view.Errorf("config.invalid",
			"parsing %s: %sprofile %s: dashboard is %s, and takes the name of a view or a block that states the screen",
			path, spot, name, bad.what).
			WithHint("write `dashboard: <view>` to show one of `dashboard: views:`, or the screen itself under it, " +
				"with the keys a view has: tiles, add, hidden, order and columns")
	}
	return nil
}

// viewKind is where a view's arrangement is stated.
type viewKind int

const (
	// noView: nothing states it.
	noView viewKind = iota
	// namedView: under `dashboard: views:`.
	namedView
	// profileView: in the entry of the profile it is named after.
	profileView
)

// stated is the arrangement a view name addresses and where it is stated.
//
// **One name, one place, and the named view wins the name.** A profile's own
// screen is addressed by the profile's name, so a `views:` entry of the same
// name is a second statement of it. The name keeps meaning the view it always
// meant, and the block in the profile is not addressable (ShadowedScreen says
// why, and ViewFor draws the `dashboard:` block for that profile rather than
// guess which of the two a person meant). Letting the block win instead would
// have a view that exists, is listed and is selected by name stop being the
// one that is drawn the day somebody writes a profile of that name — the quiet
// kind of change this package spends its comments avoiding.
//
// `default` is the `dashboard:` block's name and so is nobody's screen: a
// profile called that can state a block, but cannot have it addressed.
func (c Config) stated(name string) (View, viewKind) {
	if name == "" || name == DefaultView {
		return View{}, noView
	}
	if v, ok := c.Dashboard.Views[name]; ok {
		return v, namedView
	}
	if block := c.Profiles[name].Dashboard.Inline; block != nil {
		return *block, profileView
	}
	return View{}, noView
}

// ViewNames are the names of the views the configuration states, sorted: the
// ones under `dashboard: views:` and the profiles that write their screen in
// their own entry, which are views named after the profile.
func (c Config) ViewNames() []string {
	names := make([]string, 0, len(c.Dashboard.Views))
	for name := range c.Dashboard.Views {
		names = append(names, name)
	}
	for name := range c.Profiles {
		if _, kind := c.stated(name); kind == profileView {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// HasView reports whether a name addresses a stated view. Not the question of
// whether it is a name a view can be given (ValidViewName), which is for
// creating one: a profile's own screen has the profile's name, whatever that is.
func (c Config) HasView(name string) bool {
	_, kind := c.stated(name)
	return kind != noView
}

// InProfile reports whether a view is a profile's own screen — written in the
// profile's entry and not under `dashboard: views:` — which is what decides
// where a write to it goes and what a message calls it.
func (c Config) InProfile(view string) bool {
	_, kind := c.stated(view)
	return kind == profileView
}

// ViewFor is the view the profile that is switched on selects, or "" when
// there is none to draw: nothing is switched on, the profile names no view, or
// the view it names is not stated (which `rta doctor` says, and which draws the
// default, since a screen that fails to draw over a missing name is worse than
// the one every machine has). For a profile that writes its screen in its entry
// that is the profile's own name, unless the name cannot be had (stated).
func (c Config) ViewFor(profile string) string {
	d := c.Profiles[profile].Dashboard
	if d.Inline != nil {
		if c.InProfile(profile) {
			return profile
		}
		return ""
	}
	if c.HasView(d.Name) {
		return d.Name
	}
	return ""
}

// ShadowedScreen says why a profile's own screen is not drawn although it is
// written, in the two parts a problem is told in; both empty when it is drawn
// or there is none.
//
// Told as a problem of the profile and not refused: nothing about the profile's
// connections is in question, and a screen that fails over a name is a worse
// outage than the default one, so the `dashboard:` block draws until the two
// statements are one.
func (c Config) ShadowedScreen(profile string) (reason, hint string) {
	if c.Profiles[profile].Dashboard.Inline == nil {
		return "", ""
	}
	switch _, kind := c.stated(profile); {
	case profile == DefaultView:
		return "writes its dashboard in its entry, and `default` names the `dashboard:` block rather than a screen, " +
				"so the block in the entry is not drawn and the `dashboard:` block draws",
			"rename the profile, or state the screen under `dashboard: views:` and name it here"
	case kind == namedView:
		return "writes its dashboard in its entry, and `dashboard: views:` states a view called " + profile +
				" as well, so neither is drawn and the `dashboard:` block draws",
			"keep one: take the block out of the profile, or the view out of `dashboard: views:`, or rename the view"
	}
	return "", ""
}

// Block is the arrangement a view states, the `dashboard:` block's own for "" or
// DefaultView, without the views it holds; and false for a view that is not
// stated.
func (c Config) Block(view string) (Dashboard, bool) {
	if view == "" || view == DefaultView {
		d := c.Dashboard
		d.Views = nil
		return d, true
	}
	v, kind := c.stated(view)
	return v.Dashboard(), kind != noView
}

// SetBlock states the arrangement of a view — the `dashboard:` block's own for
// "" or DefaultView, whose views are kept — creating the view when it is not
// stated yet. A view that is a profile's own screen is stated in that profile's
// entry, which is where it lives and where the file that holds it is written;
// putting it under `views:` would state the name twice.
func (c *Config) SetBlock(view string, d Dashboard) {
	if view == "" || view == DefaultView {
		d.Views = c.Dashboard.Views
		c.Dashboard = d
		return
	}
	if c.InProfile(view) {
		profiles := make(map[string]Profile, len(c.Profiles))
		for name, p := range c.Profiles {
			profiles[name] = p
		}
		block := d.View()
		p := profiles[view]
		p.Dashboard = ProfileDashboard{Inline: &block}
		profiles[view] = p
		c.Profiles = profiles
		return
	}
	views := make(map[string]View, len(c.Dashboard.Views)+1)
	for name, v := range c.Dashboard.Views {
		views[name] = v
	}
	views[view] = d.View()
	c.Dashboard.Views = views
}

// TrustedDashboardFor is TrustedDashboard for a view: the arrangement to draw
// when the config is one somebody named, and the empty one — the automatic
// dashboard — when it is not.
func (c Config) TrustedDashboardFor(view string) Dashboard {
	if !c.trusted {
		return Dashboard{}
	}
	d, _ := c.Block(view)
	return d
}
