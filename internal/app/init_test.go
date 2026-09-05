package app

import (
	"reflect"
	"sort"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
)

// initOwns names the parts of the file `rta init` decides. initCarries names
// the parts it must leave exactly as it found them.
//
// Every field of config.Config has to be in one of them, which is the point:
// this test is not really about Plugins. It is about the next field somebody
// adds, because that is how Plugins was lost — a block was added, and
// the wizard, which assembled a fresh Config and wrote it, deleted the block
// on every run without anybody typing a line about it.
var (
	initOwns = []string{"Output", "Dashboard"}
	// Profiles is carried, never decided. `rta init` asks about output and the
	// dashboard; a connection is something an operator writes deliberately,
	// and losing one to a re-run of the wizard would silently repoint every
	// grant that names it.
	initCarries = []string{"Plugins", "Profiles", "Theme"}
	// initDerives names fields that are not part of the file at all: the
	// loader computes them, no YAML tag writes them, and the wizard neither
	// decides nor carries them because there is nothing on disk to carry.
	// `trusted` is provenance — which file this configuration came from —
	// and a config that could round-trip its own trust through `rta init`
	// would be a config declaring itself trustworthy, which is the one thing
	// the unexported field exists to prevent.
	initDerives = []string{"trusted"}
)

func TestInitKeepsEveryPartOfTheFileItDoesNotOwn(t *testing.T) {
	declared := []string{}
	rt := reflect.TypeOf(config.Config{})
	for i := 0; i < rt.NumField(); i++ {
		declared = append(declared, rt.Field(i).Name)
	}
	classified := append(append(append([]string{}, initOwns...), initCarries...), initDerives...)
	sort.Strings(declared)
	sort.Strings(classified)
	if !reflect.DeepEqual(declared, classified) {
		t.Fatalf("config.Config fields are %v, classified %v\n"+
			"a new field is something `rta init` decides, something it must carry "+
			"through untouched, or something the loader derives that is not in the "+
			"file at all — until it is named here, the wizard writes a config "+
			"assembled without it and the operator's value is gone",
			declared, classified)
	}

	// Everything set to something recognisable, so "carried" means the value
	// arrived rather than that both sides were zero.
	current := config.Config{
		Output: "yaml",
		Dashboard: config.Dashboard{
			Hidden:  []string{"net.info"},
			Order:   []string{"todo.list"},
			Columns: 3,
		},
		Plugins: map[string]map[string]any{
			"pg@919b9ed08761": {"host": "db.internal", "port": 15433},
		},
	}

	// The automatic-dashboard path, which is the one most people take.
	got := initConfig(current, "json", nil)
	if !reflect.DeepEqual(got.Plugins, current.Plugins) {
		t.Errorf("plugins = %v, want %v — `rta init` must not touch a block it does "+
			"not ask about", got.Plugins, current.Plugins)
	}
	if got.Output != "json" {
		t.Errorf("output = %q, want the answer given", got.Output)
	}
	if !reflect.DeepEqual(got.Dashboard.Hidden, current.Dashboard.Hidden) ||
		!reflect.DeepEqual(got.Dashboard.Order, current.Dashboard.Order) {
		t.Errorf("the arrangement made from inside the app was lost: %+v", got.Dashboard)
	}
	if got.Dashboard.Columns != current.Dashboard.Columns {
		t.Errorf("columns = %d, want %d — the wizard does not ask about it",
			got.Dashboard.Columns, current.Dashboard.Columns)
	}

	// And the fixed-set path.
	got = initConfig(current, "pretty", []string{"todo.list"})
	if !reflect.DeepEqual(got.Plugins, current.Plugins) {
		t.Errorf("plugins lost on the fixed-tiles path: %v", got.Plugins)
	}
	if got.Output != "" {
		t.Errorf("output = %q, want empty — pretty is what an absent key already means",
			got.Output)
	}
	if len(got.Dashboard.Tiles) != 1 || got.Dashboard.Tiles[0].ID != "todo.list" {
		t.Errorf("tiles = %v, want the chosen set", got.Dashboard.Tiles)
	}
}

// The other half, and the reason config.LoadFile exists: a wizard that reads
// through Load folds this shell's RTA_* into what it writes, so a variable
// exported for one command becomes a line in the file forever.
func TestInitDoesNotBakeThisShellsEnvironmentIntoTheFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", dir+"/config.yaml")
	if err := config.Write(config.Config{
		Plugins: map[string]map[string]any{"pg@919b9ed08761": {"host": "db.internal"}},
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_OUTPUT", "json")

	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Output != "" {
		t.Fatalf("LoadFile returned output=%q; it must not read the environment", onDisk.Output)
	}
	// The wizard seeds its form from this, so an operator is offered what the
	// file says rather than what one `export` said.
	if got := initConfig(onDisk, "pretty", nil); got.Output != "" {
		t.Errorf("output = %q, want empty — RTA_OUTPUT leaked into the written file",
			got.Output)
	}
}
