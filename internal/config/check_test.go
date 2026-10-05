package config

import (
	"os"
	"strings"
	"testing"
)

func findings(t *testing.T, text string) []string {
	t.Helper()
	var out []string
	for _, f := range CheckText([]byte(text)) {
		out = append(out, f.String())
	}
	return out
}

// The decoder drops a key it has no field for, so every one of these files is
// valid and sets nothing, and `rta doctor` called each of them ok.
func TestCheckNamesTheKeysOfTheFileRtaIgnores(t *testing.T) {
	for _, c := range []struct {
		name, file string
		want       []string
	}{
		{"a typo at the top", "oputput: json\n",
			[]string{`oputput (line 1): is not a key rta reads (did you mean "output"?)`}},
		{"a typo inside the dashboard", "dashboard:\n  colums: 3\n  hiden: [note.list]\n",
			[]string{
				`dashboard.colums (line 2): is not a key rta reads (did you mean "columns"?)`,
				`dashboard.hiden (line 3): is not a key rta reads (did you mean "hidden"?)`,
			}},
		{"a typo in a flow mapping", "dashboard: {colums: 3}\n",
			[]string{`dashboard.colums (line 1): is not a key rta reads (did you mean "columns"?)`}},
		{"a typo in a tile, counted from the tile's index", "dashboard:\n  add:\n    - id: note.list\n    - id: sys.cpu\n      spna: 2\n",
			[]string{`dashboard.add[1].spna (line 5): is not a key rta reads (did you mean "span"?)`}},
		{"a typo in a role", "roles:\n  morning:\n    grnts: [a]\n    ttl: 8h\n",
			[]string{`roles.morning.grnts (line 3): is not a key rta reads (did you mean "grants"?)`}},
		{"a key that lives in another block", "columns: 3\n",
			[]string{"columns (line 1): is not a key rta reads (columns belongs under `dashboard:`)"}},
		{"a top-level key written inside the dashboard", "dashboard:\n  output: json\n",
			[]string{"dashboard.output (line 2): is not a key rta reads (output belongs at the top level of the file)"}},
		{"a key that resembles nothing", "zzzzzz: 1\n",
			[]string{"zzzzzz (line 1): is not a key rta reads (keys here: output, dashboard, plugins, profiles, theme, roles)"}},
		{"a dotted path written as one key", "dashboard.columns: 3\n",
			[]string{"dashboard.columns (line 1): is a dotted path written as one key, and the file nests it " +
				"(write it nested, `dashboard: {columns: <value>}`, or let `rta config set dashboard.columns <value>` do it)"}},
		{"a long dotted path", "plugins.gen.password.symbols: true\n",
			[]string{"plugins.gen.password.symbols (line 1): is a dotted path written as one key, and the file nests it " +
				"(write it nested, `plugins: {gen: {password: {symbols: <value>}}}`, " +
				"or let `rta config set plugins.gen.password.symbols <value>` do it)"}},
		{"a misplaced credential is not echoed", "plugins.pg.password: hunter2\n",
			[]string{"plugins.pg.password (line 1): is a dotted path written as one key, and the file nests it " +
				"(write it nested, `plugins: {pg: {password: <value>}}`, " +
				"or let `rta config set plugins.pg.password <value>` do it)"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := findings(t, c.file)
			if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(c.want, "\n  "))
			}
		})
	}
}

func TestCheckLeavesAFileThatIsRightAlone(t *testing.T) {
	file := "output: json\n" +
		"dashboard:\n  columns: 2\n  hidden: [gen.overview]\n  add:\n    - id: note.list\n      span: 2\n" +
		"plugins:\n  http:\n    timeout: 10\n" +
		"roles:\n  morning:\n    grants: [fs.tree]\n    ttl: 8h\n    agent: claude\n"
	if got := findings(t, file); len(got) != 0 {
		t.Errorf("a correct file was reported: %v", got)
	}
}

// Profiles and the theme have loaders that already name what they ignore, and a
// second line for the same typo would make doctor say it twice.
func TestCheckLeavesProfilesAndTheThemeToTheirOwnReaders(t *testing.T) {
	file := "profiles:\n  prod:\n    plguin: pg\ntheme:\n  primry: '#ffffff'\n"
	if got := findings(t, file); len(got) != 0 {
		t.Errorf("profiles and theme were reported here as well: %v", got)
	}
}

func TestCheckKnowsEveryKeyConfigDeclares(t *testing.T) {
	for _, key := range managedKeys {
		if got := findings(t, key+": null\n"); len(got) != 0 {
			t.Errorf("%q is a key Config declares and Check reported it: %v", key, got)
		}
	}
}

func TestCheckSaysNothingOfAFileItCannotParseOrDoesNotHave(t *testing.T) {
	if got := findings(t, "a: [unclosed\n"); len(got) != 0 {
		t.Errorf("a file that does not parse was reported key by key: %v", got)
	}
	if got := findings(t, ""); len(got) != 0 {
		t.Errorf("an empty file was reported: %v", got)
	}
	path := configAt(t)
	if got, err := Check(); err != nil || len(got) != 0 {
		t.Errorf("a missing file: %v, %v", got, err)
	}
	if err := os.WriteFile(path, []byte("oputput: json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Check()
	if err != nil || len(got) != 1 || got[0].Key != "oputput" || got[0].Line != 1 {
		t.Errorf("Check read %v, %v", got, err)
	}
}
