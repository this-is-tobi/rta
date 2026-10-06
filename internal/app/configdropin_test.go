package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The commands that read and write the configuration know it is more than one
// file: what they show names the file, what they write goes where the thing
// lives, and what they refuse is refused while a person is still looking.

// split is a config directory with a file and drop-ins in it, and the path of
// the file.
func split(t *testing.T, files map[string]string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	for name, text := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, filepath.Join(dir, "config.yaml")
}

func fileText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestConfigShowListsTheDropInsAndTheFileEachSettingIsIn(t *testing.T) {
	_, path := split(t, map[string]string{
		"config.yaml":           "output: json\n",
		"config.d/10-look.yaml": "theme:\n  good: \"#3ED598\"\n",
	})
	out, errOut, err := runConfigAt(t, configRegistry(t), path, "config", "show", "-o", "pretty")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	for _, want := range []string{"drop-ins", "10-look.yaml", "config.d/10-look.yaml"} {
		if !strings.Contains(out, want) {
			t.Errorf("config show does not say %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "theme.good") && !strings.Contains(line, "10-look.yaml") {
			t.Errorf("theme.good is not named as coming from the drop-in: %s", line)
		}
		if strings.Contains(line, "output") && strings.Contains(line, "json") && strings.Contains(line, "10-look") {
			t.Errorf("output is named as coming from the drop-in: %s", line)
		}
	}
}

func TestConfigCheckNamesTheFileEachProblemIsIn(t *testing.T) {
	_, path := split(t, map[string]string{
		"config.yaml":           "output: json\n",
		"config.d/10-look.yaml": "theme:\n  good: \"#3ED598\"\ncolums: 3\n",
	})
	out, errOut, err := runConfigAt(t, configRegistry(t), path, "config", "check", "-o", "pretty")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("exit %d, %v\n%s", ExitCode(err), err, errOut)
	}
	if !strings.Contains(out, "config.d/10-look.yaml") || !strings.Contains(out, "colums") {
		t.Errorf("the problem is not named with its file:\n%s", out)
	}
	if !strings.Contains(errOut, "2 configuration files") && !strings.Contains(errOut, "configuration files") {
		t.Errorf("the verdict does not say it read more than one file:\n%s", errOut)
	}
}

func TestConfigCheckOfCleanFilesSaysSo(t *testing.T) {
	_, path := split(t, map[string]string{
		"config.yaml":           "output: json\n",
		"config.d/10-look.yaml": "theme:\n  good: \"#3ED598\"\n",
	})
	out, errOut, err := runConfigAt(t, configRegistry(t), path, "config", "check", "-o", "pretty")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if strings.Contains(out, "10-look") || strings.Contains(errOut, "problem") {
		t.Errorf("clean files were reported against:\n%s\n%s", out, errOut)
	}
}

func TestConfigSetWritesToTheFileThatStatesTheKey(t *testing.T) {
	dir, path := split(t, map[string]string{
		"config.yaml":           "output: json\n",
		"config.d/10-look.yaml": "# the colours\ntheme:\n  good: \"#3ED598\"\n",
	})
	drop := filepath.Join(dir, "config.d", "10-look.yaml")
	mainBefore := fileText(t, path)
	out, errOut, err := runConfigAt(t, configRegistry(t), path, "config", "set", "theme.bad", "#FF0000", "-o", "pretty")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if got := fileText(t, drop); !strings.Contains(got, "#FF0000") || !strings.Contains(got, "# the colours") {
		t.Errorf("the drop-in after the write:\n%s", got)
	}
	if got := fileText(t, path); got != mainBefore {
		t.Errorf("the config file changed though the theme lives in the drop-in:\n%s", got)
	}
	if !strings.Contains(out, "10-look.yaml") {
		t.Errorf("the receipt does not name the file it wrote:\n%s", out)
	}
}

func TestConfigEditOpensADropInAndCreatesItWhenSaved(t *testing.T) {
	dir, path := split(t, map[string]string{"config.yaml": "output: json\n"})
	opened := editing(t, func(shown string) string {
		return shown + "roles:\n  day:\n    grants: [\"fs.tree\"]\n"
	})
	_, errOut, err := runConfigAt(t, configRegistry(t), path, "config", "edit", "10-roles", "-o", "pretty")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if shown := (*opened)[0]; !strings.Contains(shown, "$schema=../config.schema.json") {
		t.Errorf("a new drop-in is opened on:\n%s", shown)
	}
	got := fileText(t, filepath.Join(dir, "config.d", "10-roles.yaml"))
	if !strings.Contains(got, "day:") {
		t.Errorf("the drop-in was not created with what was saved:\n%s", got)
	}
	if main := fileText(t, path); main != "output: json\n" {
		t.Errorf("the config file changed:\n%s", main)
	}
}

func TestConfigEditRefusesWhatAnotherFileAlreadyStates(t *testing.T) {
	dir, path := split(t, map[string]string{
		"config.yaml":           "roles:\n  day:\n    grants: [\"fs.tree\"]\n",
		"config.d/10-more.yaml": "output: json\n",
	})
	opened := editing(t,
		func(shown string) string { return shown + "roles:\n  day:\n    grants: [\"fs.hash\"]\n" },
		leave)
	out, errOut, err := runConfigAt(t, configRegistry(t), path, "config", "edit", "10-more", "-o", "pretty")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if len(*opened) != 2 || !strings.Contains((*opened)[1], "stated in both") {
		t.Fatalf("the editor was reopened with %v, want a note saying a role is stated twice", *opened)
	}
	if !strings.Contains(out, "cancelled") {
		t.Errorf("%s", out)
	}
	if got := fileText(t, filepath.Join(dir, "config.d", "10-more.yaml")); got != "output: json\n" {
		t.Errorf("what another file states was saved into the drop-in:\n%s", got)
	}
}

func TestConfigEditRefusesAPathForADropIn(t *testing.T) {
	_, path := split(t, map[string]string{"config.yaml": ""})
	editing(t)
	_, errOut, err := runConfigAt(t, configRegistry(t), path, "config", "edit", "../escape", "-o", "pretty")
	if err == nil || !strings.Contains(errOut, "not a file name") {
		t.Errorf("a path was taken as a drop-in's name: %v\n%s", err, errOut)
	}
}

func TestDoctorSaysHowManyDropInsTheConfigIsReadFrom(t *testing.T) {
	_, path := split(t, map[string]string{
		"config.yaml":           "output: json\n",
		"config.d/10-look.yaml": "theme:\n  good: \"#3ED598\"\n",
	})
	t.Setenv("RTA_CONFIG", path)
	var detail string
	doctorConfig(func(check, status, d string) {
		if check == "config" {
			detail = status + ": " + d
		}
	})
	if !strings.HasPrefix(detail, "ok: ") || !strings.Contains(detail, "1 drop-in") {
		t.Errorf("the config row = %q", detail)
	}
}

// A unit two files state is a fault of the files together: it is reported once,
// as the files', and not once under each of them.
func TestConfigCheckReportsAUnitStatedTwiceOnce(t *testing.T) {
	_, path := split(t, map[string]string{
		"config.yaml":          "roles:\n  day:\n    grants: [\"fs.tree\"]\n",
		"config.d/10-a.yaml":   "roles:\n  night:\n    grants: [\"fs.tree\"]\n",
		"config.d/20-dup.yaml": "roles:\n  day:\n    grants: [\"fs.hash\"]\n",
	})
	out, _, err := runConfigAt(t, configRegistry(t), path, "config", "check", "-o", "json")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("exit %d, %v", ExitCode(err), err)
	}
	if n := strings.Count(out, "stated in both"); n != 1 {
		t.Errorf("the clash is reported %d times:\n%s", n, out)
	}
}
