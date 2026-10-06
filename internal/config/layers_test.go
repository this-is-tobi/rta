package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// The configuration is one file and a directory beside it. These hold the four
// things that make that safe to rely on: what is read, what is refused, where a
// write lands, and that a file nobody named has no drop-ins at all.

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// layout is a config directory with the file and its drop-ins in it, with
// RTA_CONFIG pointing at the file.
func layout(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	writeFiles(t, dir, files)
	return dir
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func profile(name, host string) string {
	return "profiles:\n  " + name + ":\n    plugins:\n      db:\n        set:\n          host: " + host + "\n"
}

func TestTheFilesAreReadTogetherInTheOrderOfTheirNames(t *testing.T) {
	layout(t, map[string]string{
		"config.yaml":            "output: json\n" + profile("base", "b.example"),
		"config.d/20-prod.yaml":  profile("prod", "p.example"),
		"config.d/10-stage.yaml": profile("stage", "s.example"),
		"config.d/30-theme.yaml": "theme:\n  primary: \"#112233\"\n",
	})
	cfg, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.ProfileNames(), ","); got != "base,prod,stage" {
		t.Errorf("profiles = %s, want all three", got)
	}
	if cfg.Output != "json" || cfg.Theme["primary"] != "#112233" {
		t.Errorf("output = %q, theme = %v: the blocks of the other files are not read", cfg.Output, cfg.Theme)
	}
	if !cfg.Trusted() {
		t.Error("the merged configuration is not trusted, and its files were all named")
	}
	files, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f.Path))
	}
	if got := strings.Join(names, ","); got != "config.yaml,10-stage.yaml,20-prod.yaml,30-theme.yaml" {
		t.Errorf("files = %s, want the file and then each drop-in by name", got)
	}
}

// A unit stated twice is refused, naming both files, and not resolved by an
// order: the file that wins is the one thing a person could not see from either.
func TestAUnitStatedInTwoFilesIsRefusedNamingBoth(t *testing.T) {
	layout(t, map[string]string{
		"config.yaml":           profile("prod", "a.example"),
		"config.d/10-more.yaml": profile("prod", "b.example"),
	})
	_, err := LoadFile()
	if err == nil {
		t.Fatal("a profile stated twice was read")
	}
	for _, want := range []string{"profile prod", "config.yaml", "10-more.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestEachKindOfUnitIsRefusedTwice(t *testing.T) {
	for name, text := range map[string]string{
		"output":    "output: json\n",
		"dashboard": "dashboard:\n  columns: 3\n",
		"theme":     "theme:\n  primary: \"#112233\"\n",
		"role":      "roles:\n  day:\n    grants: [\"kv.get\"]\n",
		"plugin":    "plugins:\n  pg:\n    limit: 5\n",
	} {
		t.Run(name, func(t *testing.T) {
			layout(t, map[string]string{"config.yaml": text, "config.d/10-x.yaml": text})
			if _, err := LoadFile(); err == nil || !strings.Contains(err.Error(), "stated in both") {
				t.Errorf("error = %v, want the unit refused", err)
			}
		})
	}
}

// What names a file and what does not: an editor's backup, a hidden file, a
// README, a directory, and a link that resolves to nothing readable are not
// configuration, and one link that resolves to a file is.
func TestOnlyYAMLFilesAreReadAndALinkIsFollowedToAFile(t *testing.T) {
	dir := layout(t, map[string]string{
		"config.yaml":              "",
		"config.d/10-real.yaml":    profile("real", "r.example"),
		"config.d/20-backup.yaml~": profile("backup", "x"),
		"config.d/.30-hidden.yaml": profile("hidden", "x"),
		"config.d/40-notes.txt":    profile("notes", "x"),
		"config.d/README.md":       "# not configuration",
		"elsewhere/linked.yaml":    profile("linked", "l.example"),
	})
	drop := filepath.Join(dir, "config.d")
	if err := os.Mkdir(filepath.Join(drop, "50-dir.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "elsewhere", "linked.yaml"), filepath.Join(drop, "60-link.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "nowhere.yaml"), filepath.Join(drop, "70-dangling.yaml")); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(drop, "80-pipe.yaml")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.ProfileNames(), ","); got != "linked,real" {
		t.Errorf("profiles = %s, want only the regular files and the link to one", got)
	}
}

// A cloned repository does not get to arrange what rta connects to: the
// working-directory fallback is not a file anybody named, so it has no drop-ins
// either, however it is spelled.
func TestTheWorkingDirectoryFallbackHasNoDropIns(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("RTA_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	writeFiles(t, dir, map[string]string{
		".rta.yaml":           "",
		".rta.d/10-evil.yaml": profile("prod", "attacker.example"),
		"rta.d/10-evil.yaml":  profile("prod", "attacker.example"),
	})
	if got := DropInDir(); got != "" {
		t.Fatalf("the fallback has the drop-in directory %s", got)
	}
	cfg, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Profiles) != 0 || cfg.Trusted() {
		t.Errorf("profiles = %v, trusted = %v: a repository's drop-in was read", cfg.ProfileNames(), cfg.Trusted())
	}
}

func TestTheDropInDirectoryIsTheFilesOwnNameWithDotD(t *testing.T) {
	dir := t.TempDir()
	for file, want := range map[string]string{
		"config.yaml": "config.d",
		"rta.yml":     "rta.d",
		"rta":         "rta.d",
		"my.conf.yml": "my.conf.d",
	} {
		t.Setenv("RTA_CONFIG", filepath.Join(dir, file))
		if got := filepath.Base(DropInDir()); got != want {
			t.Errorf("%s has the drop-in directory %s, want %s", file, got, want)
		}
	}
}

func TestAFileThatDoesNotParseIsNamedByItsOwnPath(t *testing.T) {
	dir := layout(t, map[string]string{
		"config.yaml":          "",
		"config.d/10-bad.yaml": "profiles: [this is: not valid\n",
	})
	_, err := LoadFile()
	if err == nil {
		t.Fatal("a file that does not parse was read")
	}
	if !strings.Contains(err.Error(), filepath.Join(dir, "config.d", "10-bad.yaml")) {
		t.Errorf("error %q does not name the drop-in", err)
	}
}

func TestADirectoryOfTooManyFilesIsRefused(t *testing.T) {
	dir := layout(t, map[string]string{"config.yaml": ""})
	for i := range maxDropIns + 1 {
		writeFiles(t, dir, map[string]string{"config.d/" + strconv.Itoa(1000+i) + ".yaml": ""})
	}
	if _, err := LoadFile(); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("error = %v, want the count refused", err)
	}
}

// **A write goes where the unit lives.** Edited, a profile in a drop-in changes
// that drop-in and nothing else — the config file is the same bytes, not a copy
// of the profile and not rewritten — and a unit nothing states yet is written
// to the config file.
func TestAWriteGoesToTheFileThatStatesTheUnit(t *testing.T) {
	dir := layout(t, map[string]string{
		"config.yaml":           "# mine\n" + profile("base", "b.example"),
		"config.d/10-prod.yaml": "# the production profile\n" + profile("prod", "p.example"),
	})
	err := Mutate(func(cfg Config) (Config, bool) {
		p := cfg.Profiles["prod"]
		p.Note = "edited"
		cfg.Profiles["prod"] = p
		cfg.Profiles["new"] = Profile{Plugins: map[string]Connection{"db": {Set: map[string]any{"host": "n.example"}}}}
		return cfg, true
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "config.yaml")); !strings.Contains(got, "new:") || strings.Contains(got, "edited") || strings.Contains(got, "p.example") {
		t.Errorf("the config file after the write:\n%s\nwant the new profile and nothing of prod", got)
	}
	drop := read(t, filepath.Join(dir, "config.d", "10-prod.yaml"))
	if !strings.Contains(drop, "edited") || !strings.Contains(drop, "# the production profile") || strings.Contains(drop, "new:") || strings.Contains(drop, "b.example") {
		t.Errorf("the drop-in after the write:\n%s\nwant prod edited, its comment kept and nothing else", drop)
	}
	cfg, err := LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.ProfileNames(), ","); got != "base,new,prod" {
		t.Errorf("profiles = %s after the write", got)
	}
}

// A writer that leaves a unit as it was leaves its file as it was, byte for
// byte, and that is every drop-in a write did not mean.
func TestAFileWhoseUnitsDidNotChangeIsNotRewritten(t *testing.T) {
	dir := layout(t, map[string]string{
		"config.yaml":           profile("base", "b.example"),
		"config.d/10-prod.yaml": "# kept as typed\n\n" + profile("prod", "p.example") + "\n\n",
	})
	drop := filepath.Join(dir, "config.d", "10-prod.yaml")
	before := read(t, drop)
	info, _ := os.Stat(drop)
	err := Mutate(func(cfg Config) (Config, bool) {
		p := cfg.Profiles["base"]
		p.Note = "touched"
		cfg.Profiles["base"] = p
		return cfg, true
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, drop); got != before {
		t.Errorf("the drop-in changed though none of its units did:\n%s", got)
	}
	if after, _ := os.Stat(drop); !after.ModTime().Equal(info.ModTime()) {
		t.Error("the drop-in was written again though nothing in it changed")
	}
}

func TestAUnitRemovedIsRemovedFromTheFileThatStatedIt(t *testing.T) {
	dir := layout(t, map[string]string{
		"config.yaml":           profile("base", "b.example"),
		"config.d/10-prod.yaml": profile("prod", "p.example"),
	})
	err := Mutate(func(cfg Config) (Config, bool) {
		delete(cfg.Profiles, "prod")
		return cfg, true
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "config.d", "10-prod.yaml")); strings.Contains(got, "prod") || strings.Contains(got, "p.example") {
		t.Errorf("the drop-in still states the profile:\n%s", got)
	}
	if got := read(t, filepath.Join(dir, "config.yaml")); !strings.Contains(got, "base") {
		t.Errorf("the config file lost its own profile:\n%s", got)
	}
}

func TestWhereNamesTheFileThatStatesAUnit(t *testing.T) {
	dir := layout(t, map[string]string{
		"config.yaml":           profile("base", "b.example"),
		"config.d/10-prod.yaml": profile("prod", "p.example") + "dashboard:\n  columns: 2\n",
	})
	for _, tc := range []struct{ kind, name, file string }{
		{"profiles", "base", "config.yaml"},
		{"profiles", "prod", "config.d/10-prod.yaml"},
		{"dashboard", "", "config.d/10-prod.yaml"},
		{"profiles", "nobody", "config.yaml"},
		{"theme", "", "config.yaml"},
	} {
		if got, want := Where(tc.kind, tc.name), filepath.Join(dir, tc.file); got != want {
			t.Errorf("Where(%s, %s) = %s, want %s", tc.kind, tc.name, got, want)
		}
	}
}

// The text `rta config edit` is about to save is held to the same rule as the
// files it is saved beside — the config file's text and a drop-in's, an existing
// drop-in or a new one.
func TestAnEditThatStatesWhatAnotherFileStatesIsRefusedBeforeItIsSaved(t *testing.T) {
	dir := layout(t, map[string]string{
		"config.yaml":           profile("base", "b.example"),
		"config.d/10-prod.yaml": profile("prod", "p.example"),
	})
	drop := filepath.Join(dir, "config.d")
	for name, tc := range map[string]struct {
		path, text string
		refused    bool
	}{
		"the file states what a drop-in states": {Path(), profile("prod", "other.example"), true},
		"the file states something new":         {Path(), profile("fresh", "f.example"), false},
		"a drop-in states what the file states": {filepath.Join(drop, "10-prod.yaml"), profile("base", "x.example"), true},
		"a drop-in states its own units again":  {filepath.Join(drop, "10-prod.yaml"), profile("prod", "q.example"), false},
		"a new drop-in states what one states":  {filepath.Join(drop, "20-new.yaml"), profile("prod", "x.example"), true},
		"a new drop-in states something new":    {filepath.Join(drop, "20-new.yaml"), profile("fresh", "x.example"), false},
	} {
		_, err := Validate(tc.path, []byte(tc.text))
		if tc.refused != (err != nil) {
			t.Errorf("%s: error = %v, want refused = %v", name, err, tc.refused)
		}
		if err != nil && !strings.Contains(err.Error(), "stated in both") {
			t.Errorf("%s: error %q does not say what is doubled", name, err)
		}
	}
}

// Every file is checked for the keys rta ignores, and a finding names its file.
func TestCheckNamesTheFileAKeyIsIgnoredIn(t *testing.T) {
	dir := layout(t, map[string]string{
		"config.yaml":           "oputput: json\n",
		"config.d/10-prod.yaml": profile("prod", "p.example") + "colums: 3\n",
	})
	found, err := Check()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range found {
		got[f.Key] = f.File
	}
	if got["oputput"] != filepath.Join(dir, "config.yaml") || got["colums"] != filepath.Join(dir, "config.d", "10-prod.yaml") {
		t.Errorf("findings by file = %v, want each key named in the file that holds it", got)
	}
}
