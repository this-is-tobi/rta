package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The starter teaches by example, so every example has to be a line rta would
// read. Uncommenting all of them is the strongest file the starter can become,
// and it must parse, set what it says and leave nothing for Check to name.
func TestEveryExampleInTheStarterIsAFileRtaReads(t *testing.T) {
	configAt(t)
	_, examples, ok := strings.Cut(Starter(), "# Uncomment what you want to change.\n")
	if !ok {
		t.Fatal("the starter no longer says where the examples begin")
	}
	var live []string
	for _, line := range strings.Split(examples, "\n") {
		switch {
		case line == "#" || line == "":
			live = append(live, "")
		case strings.HasPrefix(line, "# "):
			live = append(live, strings.TrimPrefix(line, "# "))
		default:
			t.Fatalf("example line %q is neither an example nor a gap", line)
		}
	}
	text := strings.Join(live, "\n") + "\n"
	cfg, err := Parse([]byte(text))
	if err != nil {
		t.Fatalf("the uncommented starter does not parse: %v\n%s", err, text)
	}
	if cfg.Output == "" || cfg.Dashboard.Columns == 0 || len(cfg.Dashboard.Hidden) == 0 ||
		len(cfg.Theme) == 0 || len(cfg.Plugins["http"]) == 0 {
		t.Errorf("an example sets nothing: %+v", cfg)
	}
	if got := CheckText([]byte(text)); len(got) != 0 {
		t.Errorf("an example is a key rta ignores: %v", got)
	}
}

// Left as it is, the starter is the zero config with a header and some
// comments: no key rta reads, so nothing is pinned.
func TestTheStarterAsItStandsStatesNothing(t *testing.T) {
	configAt(t)
	cfg, err := Parse([]byte(Starter()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Output != "" || cfg.Dashboard.Columns != 0 || len(cfg.Plugins) != 0 || len(cfg.Theme) != 0 {
		t.Errorf("the starter states %+v", cfg)
	}
}

// The header of a file rta writes names the schema the editor is to read, and
// it is the file `rta config edit` keeps beside the config.
func TestTheHeaderNamesTheSchemaFileBesideTheConfig(t *testing.T) {
	path := configAt(t)
	if err := Mutate(func(c Config) (Config, bool) { c.Output = "json"; return c, true }); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "# yaml-language-server: $schema="+SchemaFile+"\n") {
		t.Errorf("no modeline in:\n%s", b)
	}
	if filepath.Base(SchemaFile) != SchemaFile {
		t.Errorf("%q is not a bare file name", SchemaFile)
	}
}

func TestReplaceWritesWhenTheFileIsStillWhatTheEditorStartedFrom(t *testing.T) {
	path := configAt(t)
	if err := os.WriteFile(path, []byte("output: json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Replace([]byte("output: json\n"), []byte("# mine\noutput: yaml\n")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "# mine\noutput: yaml\n" {
		t.Errorf("file = %q", b)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("a rewrite changed the mode to %v", info.Mode().Perm())
	}
}

func TestReplaceRefusesWhenSomethingElseWroteWhileTheEditorWasOpen(t *testing.T) {
	path := configAt(t)
	if err := os.WriteFile(path, []byte("output: json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Replace([]byte("output: csv\n"), []byte("output: yaml\n"))
	if err == nil || !strings.Contains(err.Error(), "changed while the editor was open") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "output: json\n" {
		t.Errorf("the other writer's file was overwritten: %q", b)
	}
}

func TestReplaceCreatesTheFileAndItsDirectoryWhenThereIsNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "config.yaml")
	t.Setenv("RTA_CONFIG", path)
	if err := Replace(nil, []byte("output: json\n")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "output: json\n" {
		t.Errorf("file = %q", b)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("directory: %v, %v", info, err)
	}
}
