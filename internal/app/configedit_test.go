package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/editor"
)

// editing stands in for a person at a terminal with an editor: each time the
// editor is opened it is shown the file, records what it was shown, and
// replaces the text with what the step returns. The step sees the text the
// editor was shown, so a test can act on the note a reopened editor carries.
func editing(t *testing.T, steps ...func(shown string) string) (opened *[]string) {
	t.Helper()
	was, wasRun, wasTTY := editorTerminal, editor.Run, isTTY
	editorTerminal = func() bool { return true }
	isTTY = func() bool { return true }
	var shown []string
	editor.Run = func(_ []string, path string) error {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		shown = append(shown, string(raw))
		if len(shown) > len(steps) {
			t.Fatalf("the editor was opened %d times, the test planned %d", len(shown), len(steps))
		}
		return os.WriteFile(path, []byte(steps[len(shown)-1](string(raw))), 0o600)
	}
	t.Cleanup(func() { editorTerminal, editor.Run, isTTY = was, wasRun, wasTTY })
	return &shown
}

func leave(shown string) string { return shown }

func TestConfigEditOpensAStarterWhenThereIsNoFileAndKeepsNothingUntilALineIsUncommented(t *testing.T) {
	opened := editing(t, leave)
	out, errOut, file, err := configRun(t, configRegistry(t), "", "config", "edit", "-o", "pretty")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if file != "" {
		t.Errorf("leaving the starter alone created a file:\n%s", file)
	}
	if !strings.Contains(out, "nothing was changed") {
		t.Errorf("%s", out)
	}
	if shown := (*opened)[0]; shown != config.Starter() {
		t.Errorf("the editor was shown:\n%s", shown)
	}
}

func TestConfigEditOpensAFileWithNothingInItOnTheStarterToo(t *testing.T) {
	opened := editing(t, leave)
	_, errOut, file, err := configRun(t, configRegistry(t), "\n", "config", "edit", "-o", "pretty")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if (*opened)[0] != config.Starter() {
		t.Errorf("the editor was shown:\n%s", (*opened)[0])
	}
	if file != "\n" {
		t.Errorf("leaving the starter alone changed the file: %q", file)
	}
}

func TestConfigEditSavesWhatWasUncommentedAndKeepsTheSchemaBesideTheFile(t *testing.T) {
	editing(t, func(shown string) string {
		return strings.Replace(shown, "# output: json ", "output: json ", 1)
	})
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "config.yaml")
	out, errOut, err := runConfigAt(t, configRegistry(t), path, "config", "edit", "-o", "pretty")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "\noutput: json ") || !strings.Contains(string(b), "# dashboard:") {
		t.Errorf("file:\n%s", b)
	}
	if !strings.Contains(out, "saved "+path) {
		t.Errorf("no receipt:\n%s", out)
	}
	schema, err := os.ReadFile(filepath.Join(dir, "sub", config.SchemaFile))
	if err != nil || !strings.Contains(string(schema), `^net(@[0-9a-f]+)?$`) {
		t.Errorf("the schema beside the file lacks the plugin's keys (read: %v, %d bytes)", err, len(schema))
	}
}

// A file that does not parse is not saved. The editor comes back with the
// reason and its line at the top, where a person is looking, and the line
// counts from the first line of the file after the note.
func TestConfigEditReopensOnAFileThatDoesNotParseAndSavesTheFix(t *testing.T) {
	opened := editing(t,
		func(shown string) string { return shown + "dashboard: [unclosed\n" },
		func(shown string) string {
			return strings.Replace(shown, "dashboard: [unclosed", "dashboard:\n  columns: 2", 1)
		},
	)
	_, errOut, file, err := configRun(t, configRegistry(t), "output: json\n", "config", "edit", "-o", "pretty")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if len(*opened) != 2 {
		t.Fatalf("the editor was opened %d times", len(*opened))
	}
	second := (*opened)[1]
	if !strings.HasPrefix(second, noteMark+"The file was not saved:") || !strings.Contains(second, "to cancel") {
		t.Errorf("the reopened editor carries no reason:\n%s", second)
	}
	if !strings.Contains(second, "output: json\ndashboard: [unclosed") {
		t.Errorf("the reopened editor lost the edit:\n%s", second)
	}
	if strings.Contains(file, noteMark) {
		t.Errorf("the note was saved:\n%s", file)
	}
	if !strings.Contains(file, "columns: 2") || !strings.Contains(file, "output: json") {
		t.Errorf("the fix was not saved:\n%s", file)
	}
}

func TestConfigEditNoteCountsLinesFromTheFirstLineAfterIt(t *testing.T) {
	opened := editing(t,
		func(shown string) string { return shown + "\n\ndashboard: [unclosed\n" },
		leave,
	)
	_, _, _, _ = configRun(t, configRegistry(t), "output: json\n", "config", "edit", "-o", "pretty")
	second := (*opened)[1]
	// The edit has `dashboard: [unclosed` on line 4 of its own text; the note is
	// three lines long, so it is line 7 of what the editor shows.
	if !strings.Contains(second, noteMark+"  line 7: ") {
		t.Errorf("the line in the note does not match the file the editor shows:\n%s", second)
	}
	lines := strings.Split(second, "\n")
	if !strings.HasPrefix(lines[6], "dashboard: [unclosed") {
		t.Errorf("line 7 is %q", lines[6])
	}
}

// Leaving the reopened file as it is cancels, and nothing is written.
func TestConfigEditCancelsWhenTheReopenedFileIsLeftAlone(t *testing.T) {
	editing(t,
		func(shown string) string { return shown + "dashboard: [unclosed\n" },
		leave,
	)
	out, _, file, err := configRun(t, configRegistry(t), "output: json\n", "config", "edit", "-o", "pretty")
	if err != nil {
		t.Fatal(err)
	}
	if file != "output: json\n" {
		t.Errorf("the file was changed:\n%s", file)
	}
	if !strings.Contains(out, "cancelled") {
		t.Errorf("%s", out)
	}
}

// A key rta ignores does not stop a save: the file is valid, and it may be a
// key from a newer rta. It is saved and named, with its line.
func TestConfigEditSavesAKeyRtaIgnoresAndNamesIt(t *testing.T) {
	editing(t, func(shown string) string { return shown + "dashboard:\n  colums: 3\n" })
	out, errOut, file, err := configRun(t, configRegistry(t), "output: json\n", "config", "edit", "-o", "pretty")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(file, "colums: 3") {
		t.Errorf("the file was not saved:\n%s", file)
	}
	for _, want := range []string{"dashboard.colums", `did you mean "columns"?`, "1 problem"} {
		if !strings.Contains(out, want) {
			t.Errorf("the receipt lacks %q:\n%s", want, out)
		}
	}
}

// An output format nothing renders breaks every command, so it is a reason not
// to save, as a file that does not parse is.
func TestConfigEditDoesNotSaveAnOutputNothingRenders(t *testing.T) {
	opened := editing(t,
		func(shown string) string { return strings.Replace(shown, "output: json", "output: jsno", 1) },
		leave,
	)
	_, _, file, _ := configRun(t, configRegistry(t), "output: json\n", "config", "edit", "-o", "pretty")
	if file != "output: json\n" {
		t.Errorf("a broken default was saved:\n%s", file)
	}
	if len(*opened) != 2 || !strings.Contains((*opened)[1], "is not an output format") {
		t.Errorf("the editor was not given the reason: %v", *opened)
	}
}

// Something else wrote the file while the editor was open: the edit is refused
// rather than laid over it, and the other writer's work is still there.
func TestConfigEditDoesNotOverwriteAWriteThatLandedWhileTheEditorWasOpen(t *testing.T) {
	editing(t, func(shown string) string {
		if err := config.Mutate(func(c config.Config) (config.Config, bool) {
			c.Dashboard.Columns = 4
			return c, true
		}); err != nil {
			t.Error(err)
		}
		return strings.Replace(shown, "output: json", "output: yaml", 1)
	})
	_, errOut, file, err := configRun(t, configRegistry(t), "output: json\n", "config", "edit", "-o", "pretty")
	if err == nil || codeOf(t, errOut) != "config.conflict" || !strings.Contains(errOut, "changed while the editor was open") {
		t.Fatalf("the edit was accepted over a concurrent write: %v\n%s", err, errOut)
	}
	if !strings.Contains(file, "columns: 4") || strings.Contains(file, "output: yaml") {
		t.Errorf("the other writer's file was overwritten:\n%s", file)
	}
}

func TestConfigEditNeedsATerminal(t *testing.T) {
	_, errOut, file, err := configRun(t, configRegistry(t), "", "config", "edit")
	if err == nil || codeOf(t, errOut) != "core.config.edit.noterminal" || !strings.Contains(errOut, "rta config set") {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if file != "" {
		t.Errorf("wrote %q", file)
	}
}

func TestConfigEditDryRunOpensNothing(t *testing.T) {
	opened := editing(t)
	out, _, _, err := configRun(t, configRegistry(t), "", "config", "edit", "--dry-run", "-o", "pretty")
	if err != nil || len(*opened) != 0 || !strings.Contains(out, "would open") {
		t.Errorf("%v, opened %d times:\n%s", err, len(*opened), out)
	}
}

// The copy the editor works on is in a directory only this account can enter,
// and is gone when the edit ends however it ends.
func TestConfigEditWorksOnAPrivateCopyThatIsRemoved(t *testing.T) {
	var where string
	editing(t, func(shown string) string { return shown })
	run := editor.Run
	editor.Run = func(argv []string, path string) error {
		where = path
		info, err := os.Stat(filepath.Dir(path))
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("the editing directory: %v, %v", info, err)
		}
		return run(argv, path)
	}
	_, _, _, _ = configRun(t, configRegistry(t), "output: json\n", "config", "edit", "-o", "pretty")
	if _, err := os.Stat(filepath.Dir(where)); !os.IsNotExist(err) {
		t.Errorf("the copy was left behind at %s", where)
	}
}
