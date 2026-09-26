package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/view"
)

// An output format nobody typed and nothing renders is refused as
// core.output.invalid, naming where it came from and what it takes, in
// pretty prose, exit 2 — on every command that renders, the app's own as well
// as a capability. It used to be the plain error ParseFormat returned, styled
// by fang with no code, which named neither the variable nor the file: the
// person reading it had typed no format at all.
func TestAnOutputFormatNobodyTypedIsRefusedNamingWhereItCameFrom(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   string
		yaml  string
		names string
	}{
		{"environment", "bogus", "", "RTA_OUTPUT"},
		{"config file", "", "output: bogus\n", "output:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RTA_OUTPUT", tc.env)
			for _, args := range [][]string{
				{"demo", "item", "list"},
				{"plugin", "list"},
				{"explain"},
				{"profile", "list"},
			} {
				_, _, err := runWith(t, testRegistry(t), tc.yaml, args...)
				var ve *view.Error
				if !errors.As(err, &ve) || ve.Code != CodeOutputInvalid || ExitCode(err) != 2 {
					t.Errorf("%v: err = %#v (exit %d), want %s and exit 2", args, err, ExitCode(err), CodeOutputInvalid)
					continue
				}
				if !strings.Contains(ve.Message, tc.names) || !strings.Contains(ve.Message, `"bogus"`) {
					t.Errorf("%v: %q does not name %s and the value", args, ve.Message, tc.names)
				}
				if !strings.Contains(ve.Hint, "pretty, json, yaml, csv or md") {
					t.Errorf("%v: hint %q does not list the formats", args, ve.Hint)
				}
				var buf bytes.Buffer
				root := NewRoot(testRegistry(t), "test")
				if !RenderTopLevelError(&buf, root, err) {
					t.Errorf("%v: the refusal was left for fang", args)
				}
				if !strings.Contains(buf.String(), CodeOutputInvalid) || json.Valid(buf.Bytes()) {
					t.Errorf("%v: rendered %q, want pretty prose carrying the code", args, buf.String())
				}
			}
		})
	}
}

// Refused before the command runs, not where it renders: the app's own
// commands render last, so a write had landed by the time the format failed,
// and the command exited 2 over it.
func TestABrokenOutputDefaultStopsAWriteBeforeItLands(t *testing.T) {
	t.Setenv("RTA_OUTPUT", "bogus")
	run := session(t, setRegistry(t))
	_, _, err := run("profile", "set", "staging", "--plugin", "db", "--set", "host=db.internal")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != CodeOutputInvalid {
		t.Fatalf("err = %#v, want %s", err, CodeOutputInvalid)
	}
	if _, serr := os.Stat(config.Path()); !os.IsNotExist(serr) {
		t.Errorf("the profile was written before the refusal: %v", serr)
	}
}

// What a broken default does not stop: the commands that write no view in it,
// doctor, which reports it, and init, which is how the key gets rewritten.
func TestABrokenOutputDefaultLeavesTheViewlessCommandsAlone(t *testing.T) {
	root := NewRoot(testRegistry(t), "test")
	for _, c := range []struct {
		path  []string
		needs bool
	}{
		{[]string{"mcp", "serve"}, false},
		{[]string{"mcp", "install"}, false},
		{[]string{"doctor"}, false},
		{[]string{"init"}, false},
		{[]string{"config", "schema"}, false},
		{[]string{"plugin", "doc"}, false},
		{[]string{"plugin", "new"}, false},
		{[]string{"plugin", "manifest"}, false},
		{[]string{"plugin", "dev"}, false},
		{[]string{"help"}, false},
		{[]string{"completion", "zsh"}, false},
		{[]string{"profile"}, false},
		{[]string{"profile", "list"}, true},
		{[]string{"plugin", "install"}, true},
		{[]string{"demo", "item", "list"}, true},
	} {
		cmd, _, err := root.Find(c.path)
		if err != nil || cmd.Name() != c.path[len(c.path)-1] {
			t.Errorf("%v: found %v, %v", c.path, cmd, err)
			continue
		}
		if got := needsOutputFormat(cmd); got != c.needs {
			t.Errorf("%v: needsOutputFormat = %v, want %v", c.path, got, c.needs)
		}
	}
	t.Setenv("RTA_OUTPUT", "bogus")
	out, errOut, err := run(t, testRegistry(t), "config", "schema")
	if err != nil || !json.Valid([]byte(out)) {
		t.Errorf("config schema stopped: %v %q", err, errOut)
	}
}

// plugin manifest and plugin dev draw a view on one path only. A broken default
// refuses that path before it writes or builds anything, and leaves the other
// to run as it did: the manifest's bytes are no view, and a command run under
// dev is checked by the root it runs in. Every path here fails on its missing
// binary or directory; what matters is which failure comes first.
func TestABrokenOutputDefaultRefusesOnlyThePathThatRenders(t *testing.T) {
	t.Setenv("RTA_OUTPUT", "bogus")
	missing := filepath.Join(t.TempDir(), "no-such-plugin")
	index := t.TempDir()
	for _, tc := range []struct {
		args    []string
		refused bool
	}{
		{[]string{"plugin", "manifest", missing, "--index", index}, true},
		{[]string{"plugin", "manifest", missing}, false},
		{[]string{"plugin", "dev", missing}, true},
		{[]string{"plugin", "dev", missing, "--", "mcp", "serve", "--as", "dev"}, false},
	} {
		_, _, err := run(t, testRegistry(t), tc.args...)
		var ve *view.Error
		if refused := errors.As(err, &ve) && ve.Code == CodeOutputInvalid; err == nil || refused != tc.refused {
			t.Errorf("%v: err = %v, want refused as %s: %v", tc.args, err, CodeOutputInvalid, tc.refused)
		}
	}
}

// A typed --output is the format asked for, whatever the default says, so a
// broken default does not stand between a script and the format it names.
func TestATypedOutputOverridesABrokenDefault(t *testing.T) {
	t.Setenv("RTA_OUTPUT", "bogus")
	out, errOut, err := run(t, testRegistry(t), "demo", "item", "list", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !json.Valid([]byte(out)) {
		t.Errorf("-o json wrote %q", out)
	}
}

// doctor is where somebody goes to find what is wrong, so a broken default
// is a failing row there rather than the reason it will not run — and the
// report is drawn in pretty, the one format left to draw it in.
func TestDoctorRunsAndReportsABrokenOutputDefault(t *testing.T) {
	for _, tc := range []struct {
		env, yaml, names string
	}{
		{"bogus", "", "RTA_OUTPUT"},
		{"", "output: bogus\n", "output:"},
	} {
		t.Setenv("RTA_OUTPUT", tc.env)
		out, errOut, err := runWith(t, testRegistry(t), tc.yaml, "doctor")
		if err != nil || out == "" || json.Valid([]byte(out)) {
			t.Fatalf("doctor did not draw its report in pretty: %v %q\n%s", err, errOut, out)
		}
		// Read back as json, typed, which the broken default does not stand
		// in the way of.
		out, errOut, err = runWith(t, testRegistry(t), tc.yaml, "doctor", "-o", "json")
		if err != nil {
			t.Fatalf("%v %q", err, errOut)
		}
		var report struct{ Rows [][]string }
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatal(err)
		}
		var row []string
		for _, r := range report.Rows {
			if r[0] == "output" {
				row = r
			}
		}
		if len(row) != 3 || row[1] != "error" || !strings.Contains(row[2], tc.names) || !strings.Contains(row[2], "bogus") {
			t.Errorf("no failing output row naming %s: %q", tc.names, row)
		}
	}
}

// RTA_OUTPUT is not the config file, and holds when the file does not parse.
// Load returned before reading the variable, so `RTA_OUTPUT=json` answered a
// script in pretty prose whenever the file had a typo in it, and a variable
// naming no format was neither refused nor reported by doctor.
func TestRTAOutputHoldsOverAConfigFileThatDoesNotParse(t *testing.T) {
	broken := "profiles: [unclosed\n"
	t.Setenv("RTA_OUTPUT", "json")
	out, errOut, err := runWith(t, testRegistry(t), broken, "demo", "item", "list")
	if err != nil || !json.Valid([]byte(out)) {
		t.Errorf("RTA_OUTPUT=json over a broken file wrote %q: %v %q", out, err, errOut)
	}
	t.Setenv("RTA_OUTPUT", "bogus")
	_, _, err = runWith(t, testRegistry(t), broken, "demo", "item", "list")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != CodeOutputInvalid || !strings.Contains(ve.Message, "RTA_OUTPUT") {
		t.Errorf("err = %#v, want %s naming RTA_OUTPUT", err, CodeOutputInvalid)
	}
	out, errOut, err = runWith(t, testRegistry(t), broken, "doctor", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	var report struct{ Rows [][]string }
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range report.Rows {
		found = found || (len(r) == 3 && r[0] == "output" && r[1] == "error" && strings.Contains(r[2], "RTA_OUTPUT"))
	}
	if !found {
		t.Errorf("doctor has no failing output row over a broken file:\n%s", out)
	}
}

// A good default is no row at all: the config row already says what it is.
func TestDoctorSaysNothingAboutAWorkingOutputDefault(t *testing.T) {
	t.Setenv("RTA_OUTPUT", "")
	out, errOut, err := runWith(t, testRegistry(t), "output: yaml\n", "doctor", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if strings.Contains(out, `"output"`) {
		t.Errorf("a working default earned a row:\n%s", out)
	}
}
