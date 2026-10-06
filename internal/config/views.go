package config

import (
	"regexp"
	"sort"
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

// ViewNames are the names of the views the configuration states, sorted.
func (c Config) ViewNames() []string {
	names := make([]string, 0, len(c.Dashboard.Views))
	for name := range c.Dashboard.Views {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ViewFor is the view the profile that is switched on selects, or "" when
// there is none to draw: nothing is switched on, the profile names no view, or
// the view it names is not stated (which `rta doctor` says, and which draws the
// default, since a screen that fails to draw over a missing name is worse than
// the one every machine has).
func (c Config) ViewFor(profile string) string {
	name := c.Profiles[profile].Dashboard
	if _, ok := c.Dashboard.Views[name]; ok && name != "" {
		return name
	}
	return ""
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
	v, ok := c.Dashboard.Views[view]
	return v.Dashboard(), ok
}

// SetBlock states the arrangement of a view — the `dashboard:` block's own for
// "" or DefaultView, whose views are kept — creating the view when it is not
// stated yet.
func (c *Config) SetBlock(view string, d Dashboard) {
	if view == "" || view == DefaultView {
		d.Views = c.Dashboard.Views
		c.Dashboard = d
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
