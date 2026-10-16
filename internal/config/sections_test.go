package config

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// Sections group the tiles of a dashboard under a header that folds. What the
// file can say about them, where, and what a write does to it.

const sectionsYAML = `dashboard:
  width: 2
  sections:
    databases:
      collapsed: true
      width: 3
    pg · staging:
      collapsed: true
  add:
  - id: pg.overview
    profile: staging/orders
    section: databases
  - id: pg.overview
    profile: staging/analytics
    section: databases
  - id: sys.overview
`

func TestATileNamesItsSectionAndTheBlockStatesHowItStarts(t *testing.T) {
	configAt(t)
	cfg, err := Parse([]byte(sectionsYAML))
	if err != nil {
		t.Fatal(err)
	}
	add := cfg.Dashboard.Add
	if len(add) != 3 || add[0].Section != "databases" || add[1].Section != "databases" || add[2].Section != "" {
		t.Errorf("the entries = %+v: a tile that names no section is in none, one that names it is in it", add)
	}
	want := map[string]DashboardSection{
		"databases":    {Collapsed: true, Width: 3},
		"pg · staging": {Collapsed: true},
	}
	if !reflect.DeepEqual(cfg.Dashboard.Sections, want) {
		t.Errorf("sections = %+v, want %+v", cfg.Dashboard.Sections, want)
	}
	if got := cfg.Dashboard.SectionOf("databases"); got.Width != 3 || !got.Collapsed {
		t.Errorf("SectionOf(databases) = %+v", got)
	}
	if got := cfg.Dashboard.SectionOf("nowhere"); got != (DashboardSection{}) {
		t.Errorf("a section the block does not state starts as %+v, want open at the dashboard's width", got)
	}
	if got := cfg.TrustedDashboard(); len(got.Sections) != 2 {
		t.Errorf("a named config draws %+v", got)
	}
}

// A view and a profile's own screen are the same type, so they take the keys
// by construction; this is the proof that `sections:` and `section:` are read in
// every place a screen can be stated, and that each one's are its own.
func TestViewsAndAProfilesOwnScreenTakeSectionsBecauseTheyAreTheSameType(t *testing.T) {
	configAt(t)
	cfg, err := Parse([]byte(`dashboard:
  sections:
    block-only:
      collapsed: true
  views:
    ops:
      sections:
        ops-group:
          width: 2
      add:
      - id: kube.overview
        profile: prod
        section: ops-group
profiles:
  staging:
    dashboard:
      sections:
        own-group:
          collapsed: true
      add:
      - id: pg.overview
        profile: staging
        section: own-group
`))
	if err != nil {
		t.Fatal(err)
	}
	if reflect.TypeOf(cfg.Dashboard.Views["ops"]) != reflect.TypeOf(*cfg.Profiles["staging"].Dashboard.Inline) {
		t.Fatal("a view and a profile's own screen are not one type, so one of them can lack a key the other has")
	}
	ops, _ := cfg.Block("ops")
	if got := ops.SectionOf("ops-group"); got.Width != 2 || ops.Add[0].Section != "ops-group" {
		t.Errorf("ops = %+v", ops)
	}
	if got := ops.SectionOf("block-only"); got != (DashboardSection{}) {
		t.Errorf("a view took the dashboard block's section %+v: it replaces the block, it does not overlay it", got)
	}
	staging, ok := cfg.Block("staging")
	if !ok || !staging.SectionOf("own-group").Collapsed || staging.Add[0].Section != "own-group" {
		t.Errorf("staging's own screen = %+v, %v", staging, ok)
	}
	base, _ := cfg.Block("")
	if !base.SectionOf("block-only").Collapsed || base.SectionOf("ops-group").Width != 0 {
		t.Errorf("the block = %+v", base)
	}
	// And what a writer reads out and puts back keeps them: a write that went
	// through Block and SetBlock without them would erase the states a person
	// wrote the moment a tile was added.
	cfg.SetBlock("ops", ops)
	cfg.SetBlock("staging", staging)
	if got, _ := cfg.Block("ops"); got.SectionOf("ops-group").Width != 2 {
		t.Errorf("a view's sections did not survive Block and SetBlock: %+v", got)
	}
	if got, _ := cfg.Block("staging"); !got.SectionOf("own-group").Collapsed {
		t.Errorf("a profile's own screen lost its sections to Block and SetBlock: %+v", got)
	}
}

func TestOnlyTheNamedConfigsSectionsAreDrawn(t *testing.T) {
	cfg := Config{Dashboard: Dashboard{
		Sections: map[string]DashboardSection{"a": {Collapsed: true}},
		Views:    map[string]View{"v": {Sections: map[string]DashboardSection{"b": {Collapsed: true}}}},
	}}
	for _, view := range []string{"", "v"} {
		if got := cfg.TrustedDashboardFor(view); len(got.Sections) != 0 {
			t.Errorf("a file nobody named folded %q: %+v", view, got.Sections)
		}
	}
}

func TestASectionNameIsAHeadingAPersonCanRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"databases", true},
		{"pg · staging", true},
		{"prod: orders (eu)", true},
		{"données", true},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false},
		{strings.Repeat("é", 64), true},
		{strings.Repeat("é", 65), false},
		{"", false},
		{" databases", false},
		{"databases ", false},
		{"two\nlines", false},
		{"tab\there", false},
		{"escape" + string(rune(0x1b)) + "[31m", false},
		{"reorder" + string(rune(0x202e)) + "text", false},
		{"zero" + string(rune(0x200b)) + "width", false},
	} {
		if got := ValidSectionName(tc.name); got != tc.ok {
			t.Errorf("ValidSectionName(%q) = %v, want %v", tc.name, got, tc.ok)
		}
	}
}

func TestAnAutomaticSectionIsNamedAfterThePluginAndTheProfile(t *testing.T) {
	if got := AutoSectionName("pg", "staging"); got != "pg · staging" {
		t.Errorf("AutoSectionName = %q", got)
	}
	if !ValidSectionName(AutoSectionName("pg", "staging")) {
		t.Error("the name the drawing gives a group is not a name `sections:` can state")
	}
}

// The section is where a tile sits and not which tile it is, so the key every
// writer addresses a tile by does not change, and the command that writes the
// entry again says where.
func TestASectionIsNotPartOfTheKeyAndIsInTheCommandThatWritesTheEntryAgain(t *testing.T) {
	a := Tile{ID: "pg.overview", Profile: "staging", Section: "databases"}
	b := Tile{ID: "pg.overview", Profile: "staging"}
	if a.Key() != b.Key() {
		t.Errorf("keys %q and %q: the section changed which tile this is", a.Key(), b.Key())
	}
	for _, tc := range []struct {
		tile Tile
		want string
	}{
		{a, "pg.overview --profile staging --section databases"},
		{Tile{ID: "pg.overview", Section: "pg · staging", Width: 6}, "pg.overview --width 6 --section 'pg · staging'"},
		{Tile{ID: "note.list"}, "note.list"},
	} {
		if got := tc.tile.AddArgs(); got != tc.want {
			t.Errorf("AddArgs = %q, want %q", got, tc.want)
		}
	}
}

// A drop-in is a place a screen is stated, and it is read, refused when stated
// twice and written back like any other: the view's sections go with the view,
// and a block that states only how its sections start is still a block.
func TestASectionIsStatedInTheFileThatStatesItsView(t *testing.T) {
	dir := layout(t, map[string]string{
		"config.yaml": "output: json\n",
		"config.d/10-ops.yaml": `# the ops screen
dashboard:
  views:
    ops:
      sections:
        databases:
          collapsed: true  # folded until I need them
      add:
      - id: pg.overview
        profile: staging/orders
        section: databases
`,
		"config.d/20-block.yaml": "dashboard:\n  sections:\n    group:\n      width: 2\n",
	})
	cfg, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := cfg.Block("ops"); !got.SectionOf("databases").Collapsed || got.Add[0].Section != "databases" {
		t.Errorf("the drop-in's view = %+v", got)
	}
	if got := cfg.Dashboard.SectionOf("group"); got.Width != 2 {
		t.Errorf("a block that states only sections is not read: %+v", cfg.Dashboard)
	}
	if got := Where("dashboard", ""); got != filepath.Join(dir, "config.d", "20-block.yaml") {
		t.Errorf("the block's file = %s: sections alone did not make it a unit", got)
	}
	if got := Where("views", "ops"); got != filepath.Join(dir, "config.d", "10-ops.yaml") {
		t.Errorf("the view's file = %s", got)
	}

	// A write adds a tile to the view, in the file that holds it, with the
	// section its entry names, and leaves the person's comment where it was.
	err = Mutate(func(c Config) (Config, bool) {
		block, _ := c.Block("ops")
		block.Add = append(slices.Clone(block.Add), Tile{ID: "pg.overview", Profile: "staging/analytics", Section: "databases"})
		c.SetBlock("ops", block)
		return c, true
	})
	if err != nil {
		t.Fatal(err)
	}
	drop := read(t, filepath.Join(dir, "config.d", "10-ops.yaml"))
	for _, want := range []string{"# the ops screen", "# folded until I need them", "collapsed: true", "profile: staging/analytics"} {
		if !strings.Contains(drop, want) {
			t.Errorf("the drop-in after the write lacks %q:\n%s", want, drop)
		}
	}
	if strings.Count(drop, "section: databases") != 2 {
		t.Errorf("the new entry did not carry its section:\n%s", drop)
	}
	if block := read(t, filepath.Join(dir, "config.d", "20-block.yaml")); !strings.Contains(block, "width: 2") {
		t.Errorf("a write to the view disturbed the block's file:\n%s", block)
	}
	if main := read(t, filepath.Join(dir, "config.yaml")); strings.Contains(main, "sections") {
		t.Errorf("a section landed in the config file:\n%s", main)
	}
}

// Two files stating the dashboard block, one of them only to say how a section
// starts, is the refusal every unit gets.
func TestABlockOfOnlySectionsIsAUnitStatedOnce(t *testing.T) {
	layout(t, map[string]string{
		"config.yaml":        "dashboard:\n  sections:\n    a:\n      collapsed: true\n",
		"config.d/10-x.yaml": "dashboard:\n  width: 3\n",
	})
	if _, err := LoadFile(); err == nil || !strings.Contains(err.Error(), "stated in both") {
		t.Errorf("error = %v, want the dashboard block refused when two files state it", err)
	}
}

func TestAProfilesOwnScreenKeepsItsSectionsThroughAWrite(t *testing.T) {
	dir := layout(t, map[string]string{
		"config.yaml": "output: json\n",
		"config.d/20-staging.yaml": `profiles:
  staging:
    plugins:
      db: {}
    dashboard:
      sections:
        mine:
          collapsed: true
      add:
      - id: pg.overview
        profile: staging
        section: mine
`,
	})
	err := Mutate(func(c Config) (Config, bool) {
		block, _ := c.Block("staging")
		block.Hidden = append(slices.Clone(block.Hidden), "git.overview")
		c.SetBlock("staging", block)
		return c, true
	})
	if err != nil {
		t.Fatal(err)
	}
	drop := read(t, filepath.Join(dir, "config.d", "20-staging.yaml"))
	for _, want := range []string{"mine:", "collapsed: true", "section: mine", "- git.overview"} {
		if !strings.Contains(drop, want) {
			t.Errorf("the profile's entry after the write lacks %q:\n%s", want, drop)
		}
	}
	if strings.Contains(read(t, filepath.Join(dir, "config.yaml")), "views") {
		t.Error("the screen was written under views: as well")
	}
}

// A width that is not a number does not load, with the line it is on, as
// the dashboard's own does not: what a decoder can refuse is refused
// where it is read, and the one value it accepts that means nothing — a negative
// one — is the check's to name.
func TestASectionsWidthThatIsNotANumberDoesNotLoad(t *testing.T) {
	configAt(t)
	for _, tc := range []struct{ name, text string }{
		{"a word", "dashboard:\n  sections:\n    a:\n      width: many\n"},
		{"a view's", "dashboard:\n  views:\n    v:\n      sections:\n        a:\n          width: many\n"},
		{"the dashboard's", "dashboard:\n  width: many\n"},
	} {
		_, err := Parse([]byte(tc.text))
		if err == nil || !strings.Contains(err.Error(), "config.yaml") {
			t.Errorf("%s: err = %v, want a refusal naming the file", tc.name, err)
		}
	}
	cfg, err := Parse([]byte("dashboard:\n  sections:\n    a:\n      width: -2\n"))
	if err != nil || cfg.Dashboard.SectionOf("a").Width != -2 {
		t.Errorf("a negative count: %v, %+v", err, cfg.Dashboard.Sections)
	}
}

// A section's keys of the wrong shape are refused in the loader's own words, as a
// profile's dashboard is: the file, the spot, the key and what it takes, where the
// decoder's sentence is about Go types and quotes a line of the file. The first one
// in the file is the one named, wherever the section is stated.
func TestASectionKeyOfTheWrongShapeIsRefusedNamingTheFileTheSpotAndWhatItTakes(t *testing.T) {
	for what, tc := range map[string]struct{ text, spot, key, says, takes string }{
		"a word": {"dashboard:\n  sections:\n    one: {width: many}\n",
			"[3:", "dashboard.sections.one.width", "is text", "takes a whole number"},
		"a list": {"dashboard:\n  views:\n    v:\n      sections:\n        a:\n          width: [1]\n",
			"[6:", "dashboard.views.v.sections.a.width", "is a list", "takes a whole number"},
		"a bool in a profile's own screen": {"profiles:\n  staging:\n    dashboard:\n      sections:\n        mine:\n          width: true\n",
			"[6:", "profiles.staging.dashboard.sections.mine.width", "is true or false", "takes a whole number"},
		"a number too large": {"dashboard:\n  sections:\n    one:\n      width: 99999999999999999999\n",
			"[4:", "dashboard.sections.one.width", "is a number too large to be a width", "takes a whole number"},
		"sections as a list": {"dashboard:\n  sections: [a, b]\n",
			"[2:", "dashboard.sections", "is a list", "takes a mapping of section names to how each starts"},
		"sections as a word in a view": {"dashboard:\n  views:\n    v:\n      sections: abc\n",
			"[4:", "dashboard.views.v.sections", "is text", "takes a mapping of section names to how each starts"},
		"sections as a number in a profile's own screen": {"profiles:\n  staging:\n    dashboard:\n      sections: 3\n",
			"[4:", "profiles.staging.dashboard.sections", "is a number", "takes a mapping of section names to how each starts"},
		"a name that is not plain": {"dashboard:\n  sections:\n    \"two\\nlines\": {width: many}\n",
			"[3:", `dashboard.sections."two\nlines".width`, "is text", "takes a whole number"},
	} {
		t.Run(what, func(t *testing.T) {
			path := configAt(t)
			_, err := Parse([]byte(tc.text))
			if err == nil {
				t.Fatal("loaded")
			}
			msg := err.Error()
			for _, want := range []string{path, tc.spot, tc.key + " " + tc.says, tc.takes} {
				if !strings.Contains(msg, want) {
					t.Errorf("%q lacks %q", msg, want)
				}
			}
			if strings.Contains(msg, "cannot unmarshal") || strings.Contains(msg, "Go struct") || strings.Contains(msg, "was used where") {
				t.Errorf("the decoder's own sentence came through: %q", msg)
			}
			var verr *view.Error
			if !errors.As(err, &verr) || verr.Hint == "" || verr.Code != "config.invalid" {
				t.Errorf("err = %#v, want config.invalid with a hint", err)
			}
		})
	}
}

// What the decoder takes is not refused for it: a count in quotes, a decimal and a
// width left empty all load, and a file with another mistake in it is still
// refused for that one, in the decoder's words.
func TestTheSectionRefusalOnlyNamesWhatTheDecoderRefused(t *testing.T) {
	configAt(t)
	cfg, err := Parse([]byte("dashboard:\n  sections:\n    a: {width: \"3\"}\n    b: {width: 2.5}\n    c: {width: }\n    d:\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := []int{cfg.Dashboard.SectionOf("a").Width, cfg.Dashboard.SectionOf("b").Width, cfg.Dashboard.SectionOf("c").Width}; !slices.Equal(got, []int{3, 2, 0}) {
		t.Errorf("widths = %v, want 3, 2 and 0", got)
	}
	_, err = Parse([]byte("dashboard:\n  sections:\n    a: {width: 2}\n  width: many\n"))
	if err == nil || !strings.Contains(err.Error(), "cannot unmarshal") {
		t.Errorf("another mistake in the file: %v, want the decoder's refusal of it", err)
	}
}

// A key inside a section's entry that is not one is a setting that does nothing,
// and the check says so with the path a person would write.
func TestAMisspeltSectionKeyIsReportedWhereverTheSectionIsStated(t *testing.T) {
	text := `dashboard:
  sections:
    databases:
      collapse: true
  views:
    ops:
      sections:
        databases:
          widht: 2
profiles:
  staging:
    dashboard:
      sections:
        mine:
          collapsd: true
`
	got := map[string]bool{}
	for _, f := range CheckText([]byte(text)) {
		got[f.Key] = true
	}
	for _, key := range []string{
		"dashboard.sections.databases.collapse",
		"dashboard.views.ops.sections.databases.widht",
		"profiles.staging.dashboard.sections.mine.collapsd",
	} {
		if !got[key] {
			t.Errorf("%s is not reported; reported: %v", key, got)
		}
	}
}
