package config

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stagingDropIn is a drop-in that holds one environment whole, with its screen
// written in the entry: the shape a person reaches for first, and the one the
// loader used to refuse with a sentence about Go types.
const stagingDropIn = `# staging, and the screen it shows
profiles:
  staging:
    note: shared # keep me
    plugins:
      db:
        set:
          host: s.example
    # the screen
    dashboard:
      # the nodes first
      add:
      - id: kube.metrics.node # per node
      - id: cnpg.storage
        profile: staging/orders
      hidden:
      - kube.metrics.node@prod/auth
      order:
      - note.list
      - sys.overview
`

func TestAProfileStatesItsDashboardInItsOwnEntry(t *testing.T) {
	layout(t, map[string]string{
		"config.yaml":              "output: json\n",
		"config.d/20-staging.yaml": stagingDropIn,
	})
	cfg, err := LoadFile()
	if err != nil {
		t.Fatalf("the drop-in was refused: %v", err)
	}
	p := cfg.Profiles["staging"]
	if p.Dashboard.Name != "" || p.Dashboard.Inline == nil {
		t.Fatalf("dashboard = %+v, want a block and no name", p.Dashboard)
	}
	block := p.Dashboard.Inline
	if len(block.Add) != 2 || block.Add[1].ID != "cnpg.storage" || block.Add[1].Profile != "staging/orders" {
		t.Errorf("add = %+v", block.Add)
	}
	if !slices.Equal(block.Hidden, []string{"kube.metrics.node@prod/auth"}) ||
		!slices.Equal(block.Order, []string{"note.list", "sys.overview"}) {
		t.Errorf("hidden = %v, order = %v", block.Hidden, block.Order)
	}
	if got := p.UnknownKeys(); len(got) != 0 {
		t.Errorf("the block's key is reported as unknown: %v", got)
	}
	if cfg.Output != "json" {
		t.Errorf("the rest of the configuration was lost with it: output = %q", cfg.Output)
	}
}

// A profile that names a view is read as it always was, and a block that says
// nothing is a screen of its own with nothing arranged on it, not an absence.
func TestADashboardThatIsANameOrAnEmptyBlockIsKept(t *testing.T) {
	configAt(t)
	cfg, err := Parse([]byte("profiles:\n  a:\n    dashboard: ops\n  b:\n    dashboard: {}\n  c:\n    dashboard:\n  d: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Profiles["a"].Dashboard; got.Name != "ops" || got.Inline != nil {
		t.Errorf("a name = %+v", got)
	}
	if got := cfg.Profiles["b"].Dashboard; got.Name != "" || got.Inline == nil {
		t.Errorf("an empty block = %+v, want a block", got)
	}
	for _, name := range []string{"c", "d"} {
		if got := cfg.Profiles[name].Dashboard; !got.IsZero() {
			t.Errorf("%s: nothing written = %+v", name, got)
		}
	}
}

func TestADashboardThatIsNeitherANameNorABlockIsRefusedNamingProfileAndFile(t *testing.T) {
	for what, tc := range map[string]struct{ value, says string }{
		"a list":   {"[ops, dbs]", "a list"},
		"a number": {"3", "a number"},
		"a bool":   {"true", "true or false"},
	} {
		dir := layout(t, map[string]string{
			"config.yaml": "output: json\n",
			"config.d/20-staging.yaml": "profiles:\n  staging:\n    plugins:\n      db: {}\n    dashboard: " +
				tc.value + "\n",
		})
		_, err := LoadFile()
		if err == nil {
			t.Errorf("%s: loaded", what)
			continue
		}
		msg := err.Error()
		file := filepath.Join(dir, "config.d", "20-staging.yaml")
		for _, want := range []string{file, "profile staging", "dashboard is " + tc.says, "[5:16]", "the name of a view or a block"} {
			if !strings.Contains(msg, want) {
				t.Errorf("%s: %q lacks %q", what, msg, want)
			}
		}
		if strings.Contains(msg, "cannot unmarshal") || strings.Contains(msg, "Go struct") {
			t.Errorf("%s: the decoder's own sentence came through: %q", what, msg)
		}
	}
}

// The block is a part of the entry, so it is stated in the file the entry is
// in and refused beside a second statement of the entry, exactly as the rest
// of it is — never merged with it, and never decided by the order of the files.
func TestTheBlockMergesAndIsRefusedWithTheEntryItIsIn(t *testing.T) {
	layout(t, map[string]string{
		"config.yaml": "profiles:\n  prod:\n    plugins:\n      db: {}\n    dashboard:\n      columns: 3\n",
		"config.d/20-staging.yaml": "profiles:\n  staging:\n    plugins:\n      db: {}\n    dashboard:\n" +
			"      hidden: [sys.overview]\n",
	})
	cfg, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Profiles["prod"].Dashboard.Inline; got == nil || got.Columns != 3 {
		t.Errorf("prod's block = %+v", got)
	}
	if got := cfg.Profiles["staging"].Dashboard.Inline; got == nil || !slices.Equal(got.Hidden, []string{"sys.overview"}) {
		t.Errorf("staging's block = %+v", got)
	}
	if Where("profiles", "staging") != filepath.Join(filepath.Dir(Path()), "config.d", "20-staging.yaml") {
		t.Errorf("the entry is in %s", Where("profiles", "staging"))
	}

	layout(t, map[string]string{
		"config.yaml":              "profiles:\n  staging:\n    plugins:\n      db: {}\n",
		"config.d/20-staging.yaml": "profiles:\n  staging:\n    dashboard:\n      columns: 3\n",
	})
	if _, err := LoadFile(); err == nil || !strings.Contains(err.Error(), "profile staging is stated in both") {
		t.Errorf("a screen split from its entry: %v", err)
	}
}

// What a write puts back is the entry, in the file that holds it, with the
// person's comments inside the block as well as around it.
func TestAWriteToTheBlockGoesBackIntoTheEntryWithItsComments(t *testing.T) {
	dir := layout(t, map[string]string{
		"config.yaml":              "output: json\n",
		"config.d/20-staging.yaml": stagingDropIn,
	})
	mainBefore := read(t, filepath.Join(dir, "config.yaml"))
	err := Mutate(func(c Config) (Config, bool) {
		p := c.Profiles["staging"]
		block := *p.Dashboard.Inline
		block.Add = append(slices.Clone(block.Add), Tile{ID: "sys.overview"})
		p.Dashboard.Inline = &block
		c.Profiles["staging"] = p
		return c, true
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "config.yaml")); got != mainBefore {
		t.Errorf("another file was written:\n%s", got)
	}
	got := read(t, filepath.Join(dir, "config.d", "20-staging.yaml"))
	for _, want := range []string{
		"# staging, and the screen it shows", "note: shared # keep me", "# the screen", "# the nodes first",
		"- id: kube.metrics.node # per node", "- id: sys.overview", "profile: staging/orders",
		"- kube.metrics.node@prod/auth",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the drop-in lost %q:\n%s", want, got)
		}
	}
	again, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if block := again.Profiles["staging"].Dashboard.Inline; block == nil || len(block.Add) != 3 {
		t.Errorf("after the write: %+v", block)
	}
}

// A profile that names a view is written as the name it is, and one that
// states nothing writes no key for it.
func TestADashboardIsWrittenAsTheNameOrTheBlockItIs(t *testing.T) {
	path := configAt(t)
	err := Mutate(func(c Config) (Config, bool) {
		c.Profiles = map[string]Profile{
			"named":  {Dashboard: ProfileDashboard{Name: "ops"}, Plugins: map[string]Connection{"db": {}}},
			"inline": {Dashboard: ProfileDashboard{Inline: &View{Columns: 2}}, Plugins: map[string]Connection{"db": {}}},
			"empty":  {Dashboard: ProfileDashboard{Inline: &View{}}, Plugins: map[string]Connection{"db": {}}},
			"none":   {Plugins: map[string]Connection{"db": {}}},
		}
		return c, true
	})
	if err != nil {
		t.Fatal(err)
	}
	text := read(t, path)
	for _, want := range []string{"dashboard: ops\n", "dashboard:\n      columns: 2\n", "dashboard: {}\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("the file lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "dashboard:") != 3 {
		t.Errorf("a profile with no screen wrote a key for one:\n%s", text)
	}
	cfg, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profiles["named"].Dashboard.Name != "ops" || cfg.Profiles["inline"].Dashboard.Inline.Columns != 2 ||
		cfg.Profiles["empty"].Dashboard.Inline == nil || !cfg.Profiles["none"].Dashboard.IsZero() {
		t.Errorf("read back: %+v", cfg.Profiles)
	}
}

func TestADashboardIsDescribedByItsNameOrItsTileCount(t *testing.T) {
	for want, d := range map[string]ProfileDashboard{
		"ops":             {Name: "ops"},
		"inline: 1 tile":  {Inline: &View{Add: []Tile{{ID: "sys.overview"}}}},
		"inline: 3 tiles": {Inline: &View{Tiles: []Tile{{ID: "a"}, {ID: "b"}}, Add: []Tile{{ID: "c"}}}},
		"inline: 0 tiles": {Inline: &View{Columns: 2}},
		"":                {},
	} {
		if got := d.Describe(); got != want {
			t.Errorf("%+v is described as %q, want %q", d, got, want)
		}
	}
}

func TestADashboardPrintsAsJSONTheWayItIsWritten(t *testing.T) {
	for dashboard, want := range map[ProfileDashboard]string{
		{Name: "ops"}:               `{"dashboard":"ops"}`,
		{Inline: &View{Columns: 2}}: `{"dashboard":{"columns":2}}`,
		{}:                          `{}`,
	} {
		raw, err := json.Marshal(Profile{Dashboard: dashboard})
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != want {
			t.Errorf("%+v printed %s, want %s", dashboard, raw, want)
		}
	}
}

func TestTheSchemaOffersAProfileEitherFormOfDashboard(t *testing.T) {
	profile := Schema()["$defs"].(map[string]any)["profile"].(map[string]any)
	dashboard := profile["properties"].(map[string]any)["dashboard"].(map[string]any)
	forms, ok := dashboard["oneOf"].([]any)
	if !ok || len(forms) != 2 {
		t.Fatalf("dashboard = %v", dashboard)
	}
	if name := forms[0].(map[string]any); name["type"] != "string" || name["pattern"] != viewName.String() {
		t.Errorf("the name form = %v", name)
	}
	if block := forms[1].(map[string]any); block["$ref"] != "#/$defs/view" {
		t.Errorf("the block form = %v: it is not held to the keys a view has", block)
	}
}

// The block is held to a view's keys by `rta config check` as a view under
// `dashboard: views:` is, while the rest of the entry stays the loader's to judge.
func TestAKeyAViewDoesNotHaveIsNamedInsideTheBlock(t *testing.T) {
	configAt(t)
	found := CheckText([]byte("profiles:\n  staging:\n    dashboard:\n      tile:\n      - id: sys.overview\n" +
		"      add:\n      - id: sys.overview\n        with: {}\n        spann: 2\n    plguin: pg\n"))
	var keys []string
	for _, f := range found {
		keys = append(keys, f.Key)
	}
	want := []string{"profiles.staging.dashboard.tile", "profiles.staging.dashboard.add[0].spann"}
	if !slices.Equal(keys, want) {
		t.Errorf("found %v, want %v: the entry's own typo is the loader's, the block's are this check's", keys, want)
	}
	if len(found) > 0 && !strings.Contains(found[0].Hint, "tiles") {
		t.Errorf("the hint does not say what it was meant to be: %q", found[0].Hint)
	}
	if got := CheckText([]byte("profiles:\n  staging:\n    dashboard: ops\n")); len(got) != 0 {
		t.Errorf("a name was reported: %v", got)
	}
}
