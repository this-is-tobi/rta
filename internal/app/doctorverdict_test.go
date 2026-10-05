package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// isDoctorFailure says whether err is the exit status of a doctor that found
// something to fail on.
func isDoctorFailure(err error) bool {
	var ve *view.Error
	return errors.As(err, &ve) && ve.Code == "core.doctor.failed"
}

func rowsOf(statuses ...string) [][]string {
	rows := make([][]string, 0, len(statuses))
	for _, s := range statuses {
		name, status, _ := strings.Cut(s, ":")
		rows = append(rows, []string{name, status, ""})
	}
	return rows
}

func checks(rows [][]string) string {
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r[0]
	}
	return strings.Join(names, " ")
}

// The report opens on what failed, then what is questionable, then the notes —
// the two that decide what an agent can reach ahead of the rest — and ends on
// what is fine. Rows of one kind keep the order they were written in.
func TestDoctorOrdersTheRowsByWhatNeedsReading(t *testing.T) {
	rows := rowsOf("data:ok", "config:warn", "team policy:info", "kv store:info", "locks:ok",
		"output:error", "grant guard:info", "roles:info", "claude code:warn")
	orderRows(rows)
	want := "output config claude code kv store grant guard team policy roles data locks"
	if got := checks(rows); got != want {
		t.Errorf("order = %s\nwant    %s", got, want)
	}
}

// A guard that is on and a store nothing can unlock are fine, and an agent-facing
// check is only a note to read first when it is a note.
func TestAnAgentFacingCheckThatIsOkIsNotAHeadline(t *testing.T) {
	rows := rowsOf("data:ok", "kv store:ok", "grant guard:ok", "config:info")
	orderRows(rows)
	if got, want := checks(rows), "config data kv store grant guard"; got != want {
		t.Errorf("order = %s, want %s", got, want)
	}
}

func TestTheVerdictCountsWhatTheReportFoundAndNamesWhatBearsOnAnAgent(t *testing.T) {
	cases := []struct {
		name string
		rows []string
		line string
		fail bool
	}{
		{"nothing", []string{"data:ok"}, "all ok", false},
		{"notes", []string{"data:ok", "team policy:info", "roles:info"},
			"all ok — 2 notes worth reading", false},
		{"one note", []string{"roles:info"}, "all ok — 1 note worth reading", false},
		{"agent notes", []string{"kv store:info", "grant guard:info", "roles:info"},
			"all ok — 3 notes worth reading; kv store and grant guard bear most on what an agent can reach", false},
		{"one agent note", []string{"grant guard:info"},
			"all ok — 1 note worth reading; grant guard bears most on what an agent can reach", false},
		{"warnings", []string{"data:warn", "roles:info"}, "1 warning, 1 note worth reading", false},
		{"errors", []string{"output:error", "config:warn", "data:warn"}, "1 error, 2 warnings", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := judge(view.Table{Rows: rowsOf(tc.rows...)})
			if got := v.line(); got != tc.line {
				t.Errorf("line = %q, want %q", got, tc.line)
			}
			if got := v.failure(false) != nil; got != tc.fail {
				t.Errorf("failure() != nil is %v, want %v", got, tc.fail)
			}
		})
	}
}

// --strict makes a warning fail too, and says which checks it failed on.
func TestStrictFailsOnAWarningAndNamesTheChecks(t *testing.T) {
	v := judge(view.Table{Rows: rowsOf("data:warn", "data:warn", "config:warn", "roles:info")})
	if err := v.failure(false); err != nil {
		t.Errorf("a warning failed without --strict: %v", err)
	}
	err := v.failure(true)
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "core.doctor.failed" {
		t.Fatalf("--strict over warnings = %v, want core.doctor.failed", err)
	}
	if !strings.Contains(ve.Message, "3 warnings: data, config") {
		t.Errorf("message = %q, want the count and each check once", ve.Message)
	}
}

// What a script gates on: the report is printed whole, in the format asked
// for, and the exit status says whether it found an error.
func TestDoctorExitsOneOnAnErrorRowAndZeroOtherwise(t *testing.T) {
	t.Setenv("RTA_OUTPUT", "")
	out, _, err := runWith(t, testRegistry(t), "output: bogus\n", "doctor", "-o", "json")
	if !isDoctorFailure(err) {
		t.Fatalf("a broken output default: err = %v, want core.doctor.failed", err)
	}
	if code := ExitCode(err); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !json.Valid([]byte(out)) || !strings.Contains(out, `"output"`) {
		t.Errorf("the report is not on stdout whole: %q", out)
	}

	if _, errOut, err := runWith(t, testRegistry(t), "", "doctor"); err != nil {
		t.Errorf("a healthy empty machine exited non-zero: %v %q", err, errOut)
	}
}

func TestDoctorStrictExitsOneOnAWarnRow(t *testing.T) {
	dataDir, _ := isolate(t)
	if err := os.Chmod(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exec := func(args ...string) error {
		root := NewRoot(testRegistry(t), "test")
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs(args)
		return root.ExecuteContext(context.Background())
	}
	if err := exec("doctor"); err != nil {
		t.Errorf("a warning failed doctor without --strict: %v", err)
	}
	if err := exec("doctor", "--strict"); !isDoctorFailure(err) {
		t.Errorf("--strict over a world-listable data directory: err = %v, want core.doctor.failed", err)
	}
}

// The verdict is the reading a person gets: the last line of pretty output, and
// nothing a program parsing another format would have to skip.
func TestOnlyPrettyDoctorEndsWithTheVerdict(t *testing.T) {
	t.Setenv("RTA_OUTPUT", "")
	pretty, _, err := runWith(t, testRegistry(t), "", "doctor")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(pretty, "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "all ok") && !strings.Contains(last, "worth reading") {
		t.Errorf("pretty doctor ends with %q, want the verdict", last)
	}
	asJSON, _, err := runWith(t, testRegistry(t), "", "doctor", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(asJSON)) {
		t.Errorf("-o json is not one document: %q", asJSON)
	}
}

// RTA_OUTPUT and `output:` are one shell apart, and the row that reports the
// default says which of them it is reading.
func TestTheConfigRowNamesWhereOutputCameFrom(t *testing.T) {
	cases := []struct {
		name, env, yaml, want string
	}{
		{"the file", "", "output: yaml\n", "(output=yaml)"},
		{"the environment", "json", "{}\n", "(output=json from RTA_OUTPUT)"},
		{"the environment over the file", "json", "output: yaml\n",
			"(output=json from RTA_OUTPUT, over the file's output=yaml)"},
		{"the environment, same as the file", "yaml", "output: yaml\n", "(output=yaml from RTA_OUTPUT)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			path := os.Getenv("RTA_CONFIG")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("RTA_OUTPUT", tc.env)
			check(t, report(t), "config", "ok", tc.want)
		})
	}
}

func TestTheConfigRowNamesRTAOutputWhenThereIsNoFile(t *testing.T) {
	isolate(t)
	t.Setenv("RTA_OUTPUT", "json")
	check(t, report(t), "config", "ok", "no config file")
	check(t, report(t), "config", "ok", "(output=json from RTA_OUTPUT)")
}
