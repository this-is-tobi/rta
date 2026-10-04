package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// handWritten is a config somebody has lived in: a header of their own, a
// reason above a role, a note on a line, a key that is a typo nobody has
// noticed yet, blocks in an order of their choosing, a flow-style role, and a
// closing remark.
const handWritten = `# my own header

# Default output
output: json   # line comment on output
oputput: nope

dashboard:
  # tiles I added
  add:
    - id: sys.mem   # memory
    # the cpu one
    - id: sys.disk
  columns: 3

plugins:
  # http knobs
  http:
    timeout: 10   # seconds, because prod is slow

theme:
  primary: "#7aa2f7"  # tokyo night

# what the on-call agent gets during an incident
roles:
  oncall:
    ttl: 2h
    grants:
      - net.hosts.add
      # the agent keeps its own notes
      - note
  dev: { grants: [note, sys] }   # flow style

# trailing comment at the end
`

func seed(t *testing.T, body string) string {
	t.Helper()
	p := setPath(t)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func mutate(t *testing.T, f func(*Config)) string {
	t.Helper()
	if err := Mutate(func(c Config) (Config, bool) { f(&c); return c, true }); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func mustContain(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("the file lost %q:\n%s", want, got)
		}
	}
}

// **The scenario that made `rta config edit` a trap.** A role with the reason
// it exists above it, and then an unrelated command: the next `rta dashboard
// add`, `rta profile set` or tile move rewrote the whole file and the reason
// went with it. Only the key that changed is written again now, so a block
// nothing touched is the same bytes it was — comments, order, blank lines, flow
// style and all.
func TestAnUnrelatedWriteLeavesTheRestOfTheFileByteForByte(t *testing.T) {
	seed(t, handWritten)
	got := mutate(t, func(c *Config) { c.Dashboard.Columns = 4 })

	for _, block := range []string{
		"# my own header\n\n# Default output\noutput: json   # line comment on output\noputput: nope\n",
		"plugins:\n  # http knobs\n  http:\n    timeout: 10   # seconds, because prod is slow\n",
		"theme:\n  primary: \"#7aa2f7\"  # tokyo night\n",
		"# what the on-call agent gets during an incident\nroles:\n  oncall:\n    ttl: 2h\n    grants:\n" +
			"      - net.hosts.add\n      # the agent keeps its own notes\n      - note\n" +
			"  dev: { grants: [note, sys] }   # flow style\n\n# trailing comment at the end\n",
	} {
		if !strings.Contains(got, block) {
			t.Errorf("a block nothing touched was changed; lost:\n%s\nin:\n%s", block, got)
		}
	}
	mustContain(t, got, "columns: 4")
}

// A command that writes what is already there writes nothing, so opening the
// file's owner's editor on it does not age it either.
func TestWritingWhatIsAlreadyThereChangesNothing(t *testing.T) {
	p := seed(t, handWritten)
	before, _ := os.Stat(p)
	got := mutate(t, func(*Config) {})
	if got != handWritten {
		t.Errorf("a no-op write changed the file:\n%s", got)
	}
	after, _ := os.Stat(p)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("a no-op write replaced the file")
	}
}

// The block that does change is written again, and takes its comments with
// it: the ones above the key, on its lines and between its entries.
func TestTheBlockThatChangedKeepsItsComments(t *testing.T) {
	seed(t, handWritten)
	got := mutate(t, func(c *Config) { c.Dashboard.Add = append(c.Dashboard.Add, Tile{ID: "sys.cpu"}) })
	mustContain(t, got, "# tiles I added", "- id: sys.mem # memory", "# the cpu one", "- id: sys.cpu", "columns: 3")
	// And the rest of the file is still the same bytes it was.
	mustContain(t, got, "  dev: { grants: [note, sys] }   # flow style", "# trailing comment at the end")
}

// A comment found by an index in a list is only as good as the list: remove the
// first tile and the second moves up, and its note has to move with it rather
// than stay at index 1 beside whichever tile is there now.
func TestACommentOnAListEntryFollowsItsEntry(t *testing.T) {
	seed(t, handWritten)
	got := mutate(t, func(c *Config) { c.Dashboard.Add = c.Dashboard.Add[1:] })
	if strings.Contains(got, "memory") {
		t.Errorf("the note on a tile that was removed stayed in the file:\n%s", got)
	}
	_, after, _ := strings.Cut(got, "add:\n")
	if !strings.HasPrefix(strings.TrimSpace(after), "# the cpu one\n  - id: sys.disk") {
		t.Errorf("the note about sys.disk did not move up with it:\n%s", got)
	}

	// One removed and another added is not one edited: the removed tile's note
	// must not land on the new one.
	seed(t, handWritten)
	got = mutate(t, func(c *Config) { c.Dashboard.Add = []Tile{c.Dashboard.Add[1], {ID: "sys.cpu"}} })
	if strings.Contains(got, "memory") {
		t.Errorf("a removed tile's note was handed to another tile:\n%s", got)
	}
	mustContain(t, got, "# the cpu one")
}

// A tile that `rta dashboard add` replaces in place — same capability, now with
// a span — is the same tile, and keeps what was said about it.
func TestACommentOnATileSurvivesItBeingEditedInPlace(t *testing.T) {
	seed(t, handWritten)
	got := mutate(t, func(c *Config) { c.Dashboard.Add[0].Span = 2 })
	mustContain(t, got, "# memory", "span: 2")
	if strings.Index(got, "# memory") > strings.Index(got, "sys.disk") {
		t.Errorf("the note moved off its tile:\n%s", got)
	}
}

// A key rta does not know is somebody's typo or a newer rta's key, and neither
// is rta's to delete on the way past. `rta config check` names it.
func TestAKeyRtaDoesNotManageIsNeverTouched(t *testing.T) {
	seed(t, handWritten)
	got := mutate(t, func(c *Config) { c.Output = "yaml" })
	mustContain(t, got, "oputput: nope", "output: yaml # line comment on output", "# Default output")
}

// A new key lands where Config declares it, so a file rta writes keeps the
// order it always had: ahead of the first block the file has for a key that
// comes later.
func TestANewBlockIsPlacedInTheOrderConfigDeclaresIt(t *testing.T) {
	seed(t, "theme:\n  primary: \"#7aa2f7\"\n\nroles:\n  a:\n    grants: [x]\n")
	got := mutate(t, func(c *Config) { c.Output = "json"; c.Plugins = map[string]map[string]any{"http": {"timeout": 5}} })
	order := []string{"output: json", "plugins:", "theme:", "roles:"}
	last := -1
	for _, want := range order {
		at := strings.Index(got, want)
		if at < 0 || at < last {
			t.Fatalf("%q is not after the one before it (%d, %d):\n%s", want, at, last, got)
		}
		last = at
	}
}

// With nothing but comments in the file — what `rta config edit` leaves a person
// who wrote notes and no settings yet — a new block goes after them.
func TestAFileOfCommentsAloneKeepsThemAndGainsTheBlock(t *testing.T) {
	seed(t, "# notes to self\n# nothing set yet\n")
	got := mutate(t, func(c *Config) { c.Output = "json" })
	if got != "# notes to self\n# nothing set yet\noutput: json\n" {
		t.Errorf("got:\n%s", got)
	}
}

// Taking a key away takes its block and the comments attached to it, and leaves
// the header and every detached remark alone: a note about `output` has no
// business outliving it, and the file's own header does.
func TestRemovingAKeyTakesItsBlockAndItsAttachedComments(t *testing.T) {
	seed(t, handWritten)
	got := mutate(t, func(c *Config) { c.Output = "" })
	if strings.Contains(got, "output: json") || strings.Contains(got, "line comment on output") ||
		strings.Contains(got, "# Default output") {
		t.Errorf("the key or what was said about it is still there:\n%s", got)
	}
	mustContain(t, got, "# my own header", "oputput: nope", "dashboard:", "# trailing comment at the end")
}

// The last key can go, and what is left still loads as an empty configuration.
func TestAFileEmptiedOfEveryKeyStillLoads(t *testing.T) {
	seed(t, "# header\noutput: json\n")
	got := mutate(t, func(c *Config) { c.Output = "" })
	if strings.TrimSpace(got) != "# header" {
		t.Errorf("got:\n%s", got)
	}
	cfg, err := LoadFile()
	if err != nil || cfg.Output != "" {
		t.Errorf("an emptied file does not load as the empty config: %+v %v", cfg, err)
	}
}

// An earlier build's header promised that a hand-written comment would not
// survive. It is replaced the next time the file is written, so the file does
// not go on making a promise that stopped being true.
func TestTheEarlierHeaderIsReplacedOnTheNextWrite(t *testing.T) {
	seed(t, legacyHeader+"output: json\n")
	got := mutate(t, func(c *Config) { c.Output = "yaml" })
	if strings.Contains(got, "does not survive") || !strings.HasPrefix(got, configHeader) {
		t.Errorf("the earlier header is still there:\n%s", got)
	}
	// But not when nothing is written: the file is not rewritten for its header.
	seed(t, legacyHeader+"output: json\n")
	if got := mutate(t, func(*Config) {}); !strings.HasPrefix(got, legacyHeader) {
		t.Errorf("a no-op write rewrote the header:\n%s", got)
	}
}

// A list written at the same column as its key is the style rta itself writes
// and the docs show; its entries belong to the block above them.
func TestAListAtTheKeysOwnColumnBelongsToTheKey(t *testing.T) {
	seed(t, "dashboard:\n  hidden:\n  - git.overview\n  - net.overview\noutput: json\n")
	got := mutate(t, func(c *Config) { c.Output = "yaml" })
	mustContain(t, got, "  hidden:\n  - git.overview\n  - net.overview\noutput: yaml")
}

// A document that is not a block mapping at the top has no blocks to keep. It
// is written again whole, carrying the comments it can.
func TestAFlowStyleDocumentIsWrittenAgain(t *testing.T) {
	seed(t, "{output: json}\n")
	got := mutate(t, func(c *Config) { c.Output = "yaml" })
	if !strings.Contains(got, "output: yaml") {
		t.Errorf("the change was not written:\n%s", got)
	}
	if _, err := LoadFile(); err != nil {
		t.Errorf("what was written does not load: %v", err)
	}
}

// A key with a dot in it, a pinned plugin section and a profile instance are
// the names comment paths have to quote or carry whole.
func TestCommentsSurviveUnusualKeyNames(t *testing.T) {
	seed(t, `plugins:
  # the pinned one
  pg@1a2b3c4d5e6f:
    host: db.internal   # prod
profiles:
  staging:
    plugins:
      # analytics instance
      pg/analytics@1a2b3c4d5e6f:
        set:
          host: x   # the host
`)
	got := mutate(t, func(c *Config) {
		c.Plugins["pg@1a2b3c4d5e6f"]["host"] = "db2.internal"
		p := c.Profiles["staging"]
		conn := p.Plugins["pg/analytics@1a2b3c4d5e6f"]
		conn.Set["host"] = "db3.internal"
		p.Plugins["pg/analytics@1a2b3c4d5e6f"] = conn
		c.Profiles["staging"] = p
	})
	mustContain(t, got, "# the pinned one", "host: db2.internal # prod", "# analytics instance", "host: db3.internal # the host")
}

// What splitTop calls a block is what a reader sees as one: the comment lines
// directly above a key are its own, a blank line breaks them off, and the text
// between blocks is the file's.
func TestSplitTopCutsABlockWhereAReaderWould(t *testing.T) {
	doc := splitTop("# header\n\n# about a\na: 1\n\n# detached\n\n# about b\nb:\n  x: 1\n\n  y: 2\n# note\nc: 3\n")
	if !doc.ok || len(doc.blocks) != 3 {
		t.Fatalf("split = %+v", doc)
	}
	for i, want := range []topBlock{
		{key: "a", head: 2, line: 3, end: 4},
		{key: "b", head: 7, line: 8, end: 12},
		{key: "c", head: 12, line: 13, end: 14},
	} {
		if doc.blocks[i] != want {
			t.Errorf("block %d = %+v, want %+v", i, doc.blocks[i], want)
		}
	}
}

func TestSplitTopRefusesWhatIsNotABlockMapping(t *testing.T) {
	for _, text := range []string{"{a: 1}\n", "- a\n- b\n", "  indented: 1\n", "%YAML 1.2\na: 1\n"} {
		if doc := splitTop(text); doc.ok {
			t.Errorf("%q was cut into blocks: %+v", text, doc.blocks)
		}
	}
}

// A new file starts with the header, so the first thing a person who opens it
// reads is that the file is optional and that their notes are safe.
func TestANewFileCarriesTheHeader(t *testing.T) {
	setPath(t)
	got := mutate(t, func(c *Config) { c.Output = "json" })
	if got != configHeader+"output: json\n" {
		t.Errorf("got:\n%s", got)
	}
}

// Whatever the splice keeps or drops, what is written has to load back as the
// configuration that was asked for: a text-level edit that corrupted a block
// would be the one failure worse than losing a comment. Every managed key is
// added, changed and removed in turn, against a file in which each of them is
// already written by hand.
func TestWhatIsWrittenAlwaysLoadsBackAsWhatWasAskedFor(t *testing.T) {
	steps := []struct {
		name string
		do   func(*Config)
	}{
		{"change the output", func(c *Config) { c.Output = "yaml" }},
		{"add a tile", func(c *Config) { c.Dashboard.Add = append(c.Dashboard.Add, Tile{ID: "sys.cpu", Span: 2}) }},
		{"hide a tile", func(c *Config) { c.Dashboard.Hidden = []string{"git.overview"} }},
		{"order tiles", func(c *Config) { c.Dashboard.Order = []string{"sys.overview", "note.list"} }},
		{"add a plugin value", func(c *Config) { c.Plugins["http"]["limit"] = 7 }},
		{"add a plugin section", func(c *Config) { c.Plugins["net"] = map[string]any{"timeout": 3} }},
		{"add a profile", func(c *Config) {
			c.Profiles = map[string]Profile{"staging": {Plugins: map[string]Connection{"db": {Set: map[string]any{"host": "h"}}}}}
		}},
		{"change a colour", func(c *Config) { c.Theme["primary"] = "#112233" }},
		{"add a role", func(c *Config) { c.Roles["night"] = Role{Grants: []string{"note"}, TTL: "1h"} }},
		{"remove a role", func(c *Config) { delete(c.Roles, "oncall") }},
		{"remove the theme", func(c *Config) { c.Theme = nil }},
		{"remove the plugins", func(c *Config) { c.Plugins = nil }},
		{"remove the dashboard", func(c *Config) { c.Dashboard = Dashboard{} }},
		{"remove the roles", func(c *Config) { c.Roles = nil }},
		{"remove the output", func(c *Config) { c.Output = "" }},
		{"add everything back", func(c *Config) {
			c.Output = "json"
			c.Dashboard.Columns = 2
			c.Plugins = map[string]map[string]any{"http": {"timeout": 9}}
			c.Theme = map[string]string{"accent": "#abcdef"}
			c.Roles = map[string]Role{"dev": {Grants: []string{"sys"}}}
		}},
	}
	seed(t, handWritten)
	for _, s := range steps {
		cfg, err := LoadFile()
		if err != nil {
			t.Fatalf("before %q: %v", s.name, err)
		}
		want := cfg
		s.do(&want)
		if err := Mutate(func(c Config) (Config, bool) { s.do(&c); return c, true }); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		got, err := LoadFile()
		if err != nil {
			raw, _ := os.ReadFile(Path())
			t.Fatalf("%s: what was written does not load: %v\n%s", s.name, err, raw)
		}
		wantTree, _ := typedTree(want)
		gotTree, _ := typedTree(got)
		if !reflect.DeepEqual(wantTree, gotTree) {
			raw, _ := os.ReadFile(Path())
			t.Fatalf("%s: loaded back as %v, asked for %v\n%s", s.name, gotTree, wantTree, raw)
		}
		if raw, _ := os.ReadFile(Path()); !strings.Contains(string(raw), "oputput: nope") {
			t.Fatalf("%s: the key rta does not manage is gone:\n%s", s.name, raw)
		}
	}
}

// A block that goes in ahead of one with a note above it is set apart from the
// note, so the note still reads as being about the block below it.
func TestANewBlockIsSetApartFromTheNoteOfTheBlockItGoesAheadOf(t *testing.T) {
	seed(t, "output: json\n\n# what the on-call agent gets\nroles:\n  oncall:\n    grants: [note]\n")
	got := mutate(t, func(c *Config) { c.Profiles = map[string]Profile{"staging": {Note: "shared"}} })
	want := "output: json\n\nprofiles:\n  staging:\n    note: shared\n\n# what the on-call agent gets\nroles:\n  oncall:\n    grants: [note]\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
