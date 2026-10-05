package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/render/theme"
)

const typoFile = `# my machine
output: json   # json for the scripts

dashboard:
  colums: 3
  hidden: [gen.overview]

plugins:
  net:
    timout: 5
  gen:
    password.symbols: true
theme:
  good: green
roles:
  morning:
    grnts: [fs.tree]
`

// Every kind of thing that does not apply is named, each with the line it is on
// where a line is known, and the exit status is the verdict.
func TestConfigCheckNamesWhatDoesNotApplyAndExitsNonZero(t *testing.T) {
	out, errOut, _, err := configRun(t, configRegistry(t), typoFile, "config", "check", "-o", "pretty")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("exit %d, %v", ExitCode(err), err)
	}
	for _, want := range []string{
		"dashboard.colums", `did you mean "columns"?`,
		"plugins.net.timout", `did you mean "timeout"?`,
		"plugins.gen.password.symbols", "written as one key", "password: {symbols: <value>}",
		"theme.good", "is not a color",
		"roles.morning.grnts", `did you mean "grants"?`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("check does not say %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "core.config.check") || !strings.Contains(errOut, "5 problems") {
		t.Errorf("no verdict on stderr:\n%s", errOut)
	}
}

func TestConfigCheckGivesEachKeyItsLine(t *testing.T) {
	out, _, _, _ := configRun(t, configRegistry(t), typoFile, "config", "check", "-o", "json")
	var table struct {
		Rows [][]string `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &table); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	line := map[string]string{}
	for _, r := range table.Rows {
		line[r[1]] = r[0]
	}
	for key, want := range map[string]string{
		"dashboard.colums": "5", "plugins.net.timout": "10", "plugins.gen.password.symbols": "12",
		"theme.good": "14", "roles.morning.grnts": "17",
	} {
		if line[key] != want {
			t.Errorf("%s is on line %s, check says %q", key, want, line[key])
		}
	}
}

func TestConfigCheckOnAFileThatIsRightSaysSoAndExitsZero(t *testing.T) {
	out, errOut, _, err := configRun(t, configRegistry(t),
		"output: json\ndashboard:\n  columns: 2\nplugins:\n  net:\n    timeout: 20\n", "config", "check", "-o", "json")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	var table struct {
		Rows [][]string `json:"rows"`
	}
	if jerr := json.Unmarshal([]byte(out), &table); jerr != nil || table.Rows == nil || len(table.Rows) != 0 {
		t.Errorf("a clean check is a table with no rows: %v\n%s", jerr, out)
	}
}

func TestConfigCheckWithNoFileHasNothingToCheck(t *testing.T) {
	_, errOut, _, err := configRun(t, configRegistry(t), "", "config", "check")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
}

func TestConfigCheckNamesAFileThatDoesNotParseWithItsLine(t *testing.T) {
	out, _, _, err := configRun(t, configRegistry(t), "output: json\ndashboard: [unclosed\n", "config", "check", "-o", "json")
	if ExitCode(err) != 1 {
		t.Fatalf("exit %d", ExitCode(err))
	}
	var table struct {
		Rows [][]string `json:"rows"`
	}
	if jerr := json.Unmarshal([]byte(out), &table); jerr != nil || len(table.Rows) != 1 || table.Rows[0][0] == "" {
		t.Errorf("%v: %s", jerr, out)
	}
}

// Checking one file must not leave its colours on the screen.
func TestConfigCheckLeavesThePaletteAsItFoundIt(t *testing.T) {
	theme.Apply(map[string]string{"good": "#112233"})
	t.Cleanup(func() { theme.Apply(nil) })
	_, _, _, _ = configRun(t, configRegistry(t), "theme:\n  good: \"#aabbcc\"\n  bad: red\n", "config", "check", "-o", "json")
	if got := theme.Current()["good"]; got != "#112233" {
		t.Errorf("good = %s after a check of another file", got)
	}
}

func TestStartupNoticeNamesTheKeysRtaIgnores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("RTA_CONFIG", path)
	root := NewRoot(configRegistry(t), "test")
	sys, _, _ := root.Find([]string{"gen"})

	var buf bytes.Buffer
	WarnConfigProblems(&buf, sys, false)
	if buf.Len() != 0 {
		t.Errorf("a machine with no file was told something: %q", buf.String())
	}
	if err := os.WriteFile(path, []byte("oputput: json\ncolumns: 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	WarnConfigProblems(&buf, sys, false)
	got := buf.String()
	if !strings.Contains(got, "2 keys rta does not read are in "+path) || !strings.Contains(got, "rta config check") {
		t.Errorf("notice = %q", got)
	}
	if strings.Count(got, "\n") != 1 {
		t.Errorf("the notice is not one line: %q", got)
	}
}

func TestStartupNoticeIsForAPersonAtATerminalOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("RTA_CONFIG", path)
	if err := os.WriteFile(path, []byte("oputput: json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := NewRoot(configRegistry(t), "test")
	cmd := func(words ...string) *cobra.Command {
		c, _, err := root.Find(words)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	var buf bytes.Buffer
	WarnConfigProblems(&buf, cmd("gen"), true)
	if buf.Len() != 0 {
		t.Errorf("machine-readable output got a notice: %q", buf.String())
	}
	// The commands that report the file in full do not say it twice.
	for _, words := range [][]string{{"config"}, {"config", "check"}, {"doctor"}, {"init"}} {
		WarnConfigProblems(&buf, cmd(words...), false)
	}
	if buf.Len() != 0 {
		t.Errorf("a command that reports the file itself was told too: %q", buf.String())
	}
}

// The notice reaches a person through the root's own startup hook.
func TestStartupNoticeIsPrintedThroughTheRootHook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("oputput: json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	was := stderrIsTerminal
	stderrIsTerminal = func() bool { return true }
	t.Cleanup(func() { stderrIsTerminal = was })
	_, errOut, err := runConfigAt(t, configRegistry(t), path, "gen", "overview", "-o", "pretty")
	_ = err
	if !strings.Contains(errOut, "1 key rta does not read is in") {
		t.Errorf("stderr:\n%s", errOut)
	}
	_, errOut, _ = runConfigAt(t, configRegistry(t), path, "gen", "overview", "-o", "json")
	if strings.Contains(errOut, "does not read") {
		t.Errorf("a script's stderr got the notice:\n%s", errOut)
	}
}
