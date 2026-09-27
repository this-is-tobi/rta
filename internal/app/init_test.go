package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/view"
)

// noTerminal says, for the rest of the test, that there is no terminal to
// draw the wizard's form on.
func noTerminal(t *testing.T) {
	t.Helper()
	saved := initTerminal
	t.Cleanup(func() { initTerminal = saved })
	initTerminal = func() (io.Reader, io.Writer, func(), error) {
		return nil, nil, nil, errors.New("open /dev/tty: device not configured")
	}
}

// Without a terminal the wizard refuses, coded and in the format asked for.
// It was a plain error, the one fang styled as a box under `-o json`.
func TestInitWithNoTerminalIsACodedRefusal(t *testing.T) {
	noTerminal(t)
	_, _, err := run(t, testRegistry(t), "init", "-o", "json")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "core.init.terminal" || ve.Hint == "" {
		t.Fatalf("err = %#v, want core.init.terminal with a hint", err)
	}
	var buf bytes.Buffer
	if !RenderTopLevelError(&buf, NewRoot(testRegistry(t), "test"), err) {
		t.Fatal("the refusal was left for fang")
	}
}

// answeredInit stands in for the person filling the wizard's form in, at a
// terminal of its own, for the rest of the test. What it returns is whether
// the form it stood in for was last opened as a dry run's.
func answeredInit(t *testing.T, a initAnswers) *bool {
	t.Helper()
	asDryRun, _ := answeredAt(t, a)
	return asDryRun
}

// answeredAt is answeredInit, also returning what the form was last drawn on.
func answeredAt(t *testing.T, a initAnswers) (asDryRun *bool, drawnOn *io.Writer) {
	t.Helper()
	savedAsk, savedTerminal := askInit, initTerminal
	t.Cleanup(func() { askInit, initTerminal = savedAsk, savedTerminal })
	var screen bytes.Buffer
	initTerminal = func() (io.Reader, io.Writer, func(), error) {
		return strings.NewReader(""), &screen, func() {}, nil
	}
	asDryRun, drawnOn = new(bool), new(io.Writer)
	askInit = func(_ context.Context, _ *registry.Registry, _ config.Config, dryRun bool,
		_ io.Reader, out io.Writer,
	) (initAnswers, error) {
		*asDryRun, *drawnOn = dryRun, out
		return a, nil
	}
	return asDryRun, drawnOn
}

// The form stays interactive; what init answers once it is closed is a view,
// in the format asked for. It printed "✓ wrote <file>" on stdout whatever -o
// said, and "nothing written" for a form cancelled.
func TestInitAnswersWithAViewInTheFormatAskedFor(t *testing.T) {
	run := session(t, testRegistry(t))
	onATerminal(t)

	answeredInit(t, initAnswers{output: "pretty", confirmed: true})
	out, errOut, err := run("init", "-o", "pretty", "--no-color")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	readsOnATerminal(t, out, "wrote", "output", "dashboard", "next")

	answeredInit(t, initAnswers{output: "yaml", tiles: []string{"demo.item.list"}, confirmed: true})
	out, errOut, err = run("init", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	pairs := answerPairs(t, out)
	if pairs["wrote"] != config.Path() || pairs["output"] != "yaml" ||
		!strings.Contains(pairs["dashboard"], "demo.item.list") || !strings.Contains(pairs["next"], "rta") {
		t.Errorf("answered %v, want the file, the two answers it holds and what comes next", pairs)
	}
	if written, _ := config.LoadFile(); written.Output != "yaml" {
		t.Errorf("the file holds output %q, want the answer given", written.Output)
	}

	// Cancelled is an answer, exit 0, and the file as it was.
	answeredInit(t, initAnswers{output: "json", confirmed: false})
	out, errOut, err = run("init", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if pairs = answerPairs(t, out); !strings.HasSuffix(pairs["unchanged"], "nothing written to "+config.Path()) {
		t.Errorf("a cancelled wizard answered %v", pairs)
	}
	if written, _ := config.LoadFile(); written.Output != "yaml" {
		t.Errorf("a cancelled wizard wrote output %q", written.Output)
	}

	// init is how a default nothing renders gets rewritten, so it runs under
	// one and answers in pretty, the one format left.
	t.Setenv("RTA_OUTPUT", "bogus")
	out, errOut, err = run("init", "--no-color")
	if err != nil {
		t.Fatalf("a broken default stopped init: %v %q", err, errOut)
	}
	readsOnATerminal(t, out, "unchanged")
}

// Stdout carries the answer, and only the answer: `rta init -o json >
// answer.json` was refused because stdout was not a terminal, although the
// person answering the form was at one. The form is drawn on the terminal
// initTerminal finds, and stdout holds the json alone.
func TestInitAnswersIntoAFileWhileItAsksAtTheTerminal(t *testing.T) {
	run := session(t, testRegistry(t))
	saved := isTTY
	t.Cleanup(func() { isTTY = saved })
	isTTY = func() bool { return false }
	_, drawnOn := answeredAt(t, initAnswers{output: "yaml", confirmed: true})

	out, errOut, err := run("init", "-o", "json")
	if err != nil {
		t.Fatalf("init with stdout redirected: %v %q", err, errOut)
	}
	if pairs := answerPairs(t, out); pairs["wrote"] != config.Path() || pairs["output"] != "yaml" {
		t.Errorf("stdout held %q, want the answer alone", out)
	}
	if *drawnOn == nil || *drawnOn == io.Writer(os.Stdout) {
		t.Errorf("the form was drawn on %v, want the terminal initTerminal found", *drawnOn)
	}
}

// Where the form goes: the standard streams when both are the terminal, and
// the controlling terminal when either is pointed elsewhere — stdin a pipe,
// stderr a log — since the person is there whatever the streams say. No
// terminal at all is the one case with nobody to ask.
func TestTheFormIsDrawnOnTheTerminalWhereverTheStreamsPoint(t *testing.T) {
	dir := t.TempDir()
	file := func(name string) *os.File {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
		return f
	}
	stdin, stderr, tty := file("stdin"), file("stderr"), file("tty")
	opened := 0
	open := func() (*os.File, *os.File, error) { opened++; return tty, tty, nil }
	for _, tc := range []struct {
		name      string
		terminals []*os.File
		in, out   *os.File
	}{
		{"both streams at the terminal", []*os.File{stdin, stderr}, stdin, stderr},
		{"stdin a pipe", []*os.File{stderr}, tty, tty},
		{"stderr a log", []*os.File{stdin}, tty, tty},
	} {
		opened = 0
		isTerminal := func(f *os.File) bool { return slices.Contains(tc.terminals, f) }
		in, out, release, err := formTerminal(stdin, stderr, isTerminal, open)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		release()
		if in != io.Reader(tc.in) || out != io.Writer(tc.out) {
			t.Errorf("%s: drawn on %v reading %v, want %v reading %v", tc.name, out, in, tc.out, tc.in)
		}
		if want := tc.in == tty; (opened == 1) != want {
			t.Errorf("%s: the controlling terminal was opened %d times", tc.name, opened)
		}
	}
	none := func() (*os.File, *os.File, error) { return nil, nil, errors.New("no controlling terminal") }
	if _, _, _, err := formTerminal(stdin, stderr, func(*os.File) bool { return false }, none); err == nil {
		t.Error("with no terminal anywhere the form was drawn anyway")
	}
}

// --dry-run never reached init: `rta init --dry-run` asked its questions and
// wrote the answers to the file. The form is told, so its last button does not
// read "write".
func TestInitDryRunWritesNothing(t *testing.T) {
	run := session(t, testRegistry(t))
	onATerminal(t)
	asDryRun := answeredInit(t, initAnswers{output: "json", confirmed: true})

	out, errOut, err := run("init", "--dry-run", "-o", "json")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !*asDryRun {
		t.Error("the form was opened as though --dry-run had not been passed")
	}
	pairs := answerPairs(t, out)
	if pairs["would write"] != config.Path() || pairs["output"] != "json" ||
		!strings.Contains(pairs["next"], "--dry-run") {
		t.Errorf("a dry run answered %v", pairs)
	}
	if _, err := os.Stat(config.Path()); !os.IsNotExist(err) {
		t.Errorf("--dry-run wrote %s: %v", config.Path(), err)
	}
}

// Leaving the wizard is coded as that, and anything else that stops the form
// under its own code; neither is huh's bare sentence.
func TestInitFormErrorsAreCoded(t *testing.T) {
	for err, code := range map[error]string{
		huh.ErrUserAborted:                  "core.init.aborted",
		fmt.Errorf("open /dev/tty: denied"): "core.init.form",
	} {
		ve := view.AsError(initFormError(err), "")
		if ve.Code != code || ve.Hint == "" {
			t.Errorf("%v: %#v, want %s with a hint", err, ve, code)
		}
	}
}

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
	initCarries = []string{"Plugins", "Profiles", "Roles", "Theme"}
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
			Order:   []string{"note.list"},
			Columns: 3,
		},
		Plugins: map[string]map[string]any{
			"pg@919b9ed08761": {"host": "db.internal", "port": 15433},
		},
		Roles: map[string]config.Role{"dev": {Grants: []string{"kv.get db-password"}, TTL: "8h"}},
	}

	// The automatic-dashboard path, which is the one most people take.
	got := initConfig(current, "json", nil)
	if !reflect.DeepEqual(got.Roles, current.Roles) {
		t.Errorf("roles = %v, want %v — a role is written deliberately, and re-running the wizard must keep it", got.Roles, current.Roles)
	}
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
	got = initConfig(current, "pretty", []string{"note.list"})
	if !reflect.DeepEqual(got.Plugins, current.Plugins) {
		t.Errorf("plugins lost on the fixed-tiles path: %v", got.Plugins)
	}
	if got.Output != "" {
		t.Errorf("output = %q, want empty — pretty is what an absent key already means",
			got.Output)
	}
	if len(got.Dashboard.Tiles) != 1 || got.Dashboard.Tiles[0].ID != "note.list" {
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
