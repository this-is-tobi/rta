package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// configRegistry is two plugins with the keys the commands have to tell apart: a
// number held to two different ranges, a key nested one level, a closed set, a
// list, a bool, a credential, and an input that is not a config key at all.
func configRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	reg := registry.New()
	for _, p := range []plugin.Plugin{
		{Name: "net", Summary: "network", Capabilities: []plugin.Capability{
			{ID: "net.ping", Summary: "ping", Safety: plugin.Read, Run: run, Inputs: []plugin.Field{
				{Name: "timeout", Type: plugin.Int, Default: 10, Min: 1, Max: 300, Config: "timeout", Help: "seconds to wait"},
				{Name: "count", Type: plugin.Int, Default: 4, Min: 1, Max: 100, Config: "ping.count", Help: "probes"},
				{Name: "encoding", Type: plugin.String, Config: "encoding", Options: []string{"b64", "hex"}, Help: "how"},
				{Name: "resolve", Type: plugin.Bool, Config: "resolve", Help: "resolve names"},
				{Name: "tags", Type: plugin.StringSlice, Config: "tags", Help: "labels"},
				{Name: "token", Type: plugin.Secret, Local: true, EnvFallback: true, Help: "a credential"},
				{Name: "name", Type: plugin.String, Help: "not a config key"},
			}},
			{ID: "net.port", Summary: "port", Safety: plugin.Read, Run: run, Inputs: []plugin.Field{
				{Name: "timeout", Type: plugin.Int, Default: 2, Min: 1, Max: 60, Config: "timeout"},
			}},
		}},
		{Name: "gen", Summary: "generators", Capabilities: []plugin.Capability{
			{ID: "gen.password", Summary: "password", Safety: plugin.Read, Run: run, Inputs: []plugin.Field{
				{Name: "symbols", Type: plugin.Bool, Config: "password.symbols", Help: "include symbols"},
			}},
			{ID: "gen.overview", Summary: "overview", Safety: plugin.Read, Run: run},
		}},
	} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

// configRun runs one command against a config file with the given text, and
// returns what it printed and the file as it is afterwards.
func configRun(t *testing.T, reg *registry.Registry, text string, args ...string) (stdout, stderr string, file string, err error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if text != "" {
		if werr := os.WriteFile(path, []byte(text), 0o600); werr != nil {
			t.Fatal(werr)
		}
	}
	stdout, stderr, err = runConfigAt(t, reg, path, args...)
	b, _ := os.ReadFile(path)
	return stdout, stderr, string(b), err
}

func runConfigAt(t *testing.T, reg *registry.Registry, path string, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("RTA_CONFIG", path)
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_OUTPUT", "")
	SetInstalled(reg)
	t.Cleanup(func() { SetInstalled(nil) })
	root := NewRoot(reg, "test")
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

func codeOf(t *testing.T, stderr string) string {
	t.Helper()
	for _, field := range strings.Fields(ansi.Strip(stderr)) {
		if strings.HasPrefix(field, "core.") || strings.HasPrefix(field, "config.") {
			return field
		}
	}
	return ""
}

func TestConfigSetWritesAKeyHeldToWhatItTakes(t *testing.T) {
	reg := configRegistry(t)
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"an output format", []string{"output", "json"}, "output: json"},
		{"the long name of a format", []string{"output", "markdown"}, "output: md"},
		{"a colour", []string{"theme.primary", "#7aa2f7"}, "primary: \"#7aa2f7\""},
		{"a grid width", []string{"dashboard.columns", "3"}, "columns: 3"},
		{"a number a plugin declares", []string{"plugins.net.timeout", "20"}, "timeout: 20"},
		{"a nested key", []string{"plugins.net.ping.count", "7"}, "ping:\n      count: 7"},
		{"the capability spelling of a key", []string{"plugins.net.port.timeout", "5"}, "timeout: 5"},
		{"a bool", []string{"plugins.gen.password.symbols", "true"}, "password:\n      symbols: true"},
		{"a closed set, in the case it is declared", []string{"plugins.net.encoding", "HEX"}, "encoding: hex"},
		{"a list", []string{"plugins.net.tags", "a", "b c"}, "tags:\n    - a\n    - b c"},
		{"tiles to hide", []string{"dashboard.hidden", "gen.overview", "net.ping@prod"}, "hidden:\n  - gen.overview\n  - net.ping@prod"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, errOut, file, err := configRun(t, reg, "", append([]string{"config", "set"}, c.args...)...)
			if err != nil {
				t.Fatalf("%v\n%s", err, errOut)
			}
			if !strings.Contains(file, c.want) {
				t.Errorf("the file lacks %q:\n%s", c.want, file)
			}
			if !strings.Contains(out, "wrote") {
				t.Errorf("no receipt:\n%s", out)
			}
			if _, perr := config.Parse([]byte(file)); perr != nil {
				t.Errorf("the file it wrote does not load: %v", perr)
			}
		})
	}
}

// A number is checked against the widest range any capability reading the key
// takes, which is what profile set does too: 200 is net.ping's, so it is a
// value some call accepts, and 400 is nobody's.
func TestConfigSetHoldsAPluginKeyToItsWidestRange(t *testing.T) {
	reg := configRegistry(t)
	if _, errOut, _, err := configRun(t, reg, "", "config", "set", "plugins.net.timeout", "200"); err != nil {
		t.Fatalf("200 was refused: %v\n%s", err, errOut)
	}
	_, errOut, file, err := configRun(t, reg, "", "config", "set", "plugins.net.timeout", "400")
	if err == nil || codeOf(t, errOut) != "core.config.set.range" {
		t.Fatalf("400 was accepted, or refused as %q: %v", codeOf(t, errOut), err)
	}
	if file != "" {
		t.Errorf("a refused value was written:\n%s", file)
	}
}

func TestConfigSetRefusesWhatTheKeyDoesNotTake(t *testing.T) {
	reg := configRegistry(t)
	for _, c := range []struct {
		name, code string
		args       []string
		hint       string
	}{
		{"an output nothing renders", "core.config.set.option", []string{"output", "jsno"}, "rta config set output json"},
		{"two values for one key", "core.config.set.arity", []string{"output", "json", "yaml"}, ""},
		{"a colour that is not one", "core.config.set.color", []string{"theme.primary", "red"}, "'#D97757'"},
		{"a width that is not a number", "core.config.set.type", []string{"dashboard.columns", "wide"}, ""},
		{"a negative width", "core.config.set.type", []string{"dashboard.columns", "--", "-1"}, ""},
		{"a word for an int", "core.config.set.type", []string{"plugins.net.timeout", "soon"}, "rta config set plugins.net.timeout"},
		{"yes for a bool", "core.config.set.type", []string{"plugins.gen.password.symbols", "yes"}, "rta config set plugins.gen.password.symbols true"},
		{"a value outside a closed set", "core.config.set.option", []string{"plugins.net.encoding", "rot13"}, ""},
		{"a tile that is no capability", "core.config.set.capability", []string{"dashboard.hidden", "gen.overvew"}, "gen.overview"},
		{"a block as a value", "core.config.set.block", []string{"theme", "x"}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, errOut, file, err := configRun(t, reg, "", append([]string{"config", "set"}, c.args...)...)
			if err == nil {
				t.Fatal("accepted")
			}
			if got := codeOf(t, errOut); got != c.code {
				t.Errorf("code = %q, want %q\n%s", got, c.code, errOut)
			}
			if !strings.Contains(errOut, c.hint) {
				t.Errorf("the refusal does not say %q:\n%s", c.hint, errOut)
			}
			if file != "" {
				t.Errorf("a refused value was written:\n%s", file)
			}
		})
	}
}

// The refusals worded for a profile's --set name the command that set it, not
// the flag that was never typed.
func TestConfigSetRefusalsAreNotWordedForProfileSet(t *testing.T) {
	_, errOut, _, _ := configRun(t, configRegistry(t), "", "config", "set", "plugins.net.timeout", "soon")
	if strings.Contains(errOut, "--set") || strings.Contains(errOut, "core.profile") {
		t.Errorf("worded for another command:\n%s", errOut)
	}
}

func TestConfigSetNamesTheKeyItWasNearlyGivenWhenItIsNone(t *testing.T) {
	reg := configRegistry(t)
	for _, c := range []struct{ typed, want string }{
		{"oputput", "`output`"},
		{"columns", "`dashboard.columns`"},
		{"primary", "`theme.primary`"},
		{"dashboard.colums", "`dashboard.columns`"},
		{"plugins.net.timout", "`plugins.net.timeout`"},
		{"plugin.net.timeout", "`plugins.net.timeout`"},
		{"plugins.nt.timeout", "`net`"},
	} {
		_, errOut, _, err := configRun(t, reg, "", "config", "set", c.typed, "1")
		if err == nil || !strings.Contains(errOut, "did you mean "+c.want) {
			t.Errorf("%s: %v\n%s", c.typed, err, errOut)
		}
	}
}

func TestConfigSetPointsAtTheCommandThatWritesTheRestOfTheFile(t *testing.T) {
	reg := configRegistry(t)
	for typed, want := range map[string]string{
		"profiles.prod.note": "rta profile set",
		"dashboard.add":      "rta dashboard add",
		"dashboard.tiles":    "rta dashboard add",
		"roles.morning":      "rta config edit",
	} {
		_, errOut, _, err := configRun(t, reg, "", "config", "set", typed, "x")
		if err == nil || codeOf(t, errOut) != "core.config.key.managed" || !strings.Contains(errOut, want) {
			t.Errorf("%s: %v\n%s", typed, err, errOut)
		}
	}
}

func TestConfigSetCannotStateACredential(t *testing.T) {
	_, errOut, file, err := configRun(t, configRegistry(t), "", "config", "set", "plugins.net.token", "hunter2")
	if err == nil || codeOf(t, errOut) != "core.config.key.secret" {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if strings.Contains(errOut, "hunter2") || file != "" {
		t.Errorf("the credential was echoed or written:\n%s\n%s", errOut, file)
	}
	if !strings.Contains(errOut, "rta profile set") {
		t.Errorf("no way to give the plugin its credential:\n%s", errOut)
	}
}

// What a person wrote in the file stays: the comments, the order of the keys,
// the blocks this did not touch, and a key rta does not know.
func TestConfigSetKeepsWhatThePersonWroteInTheFile(t *testing.T) {
	text := "# my machine\n" +
		"output: json   # json for the scripts\n" +
		"\n" +
		"roles:\n" +
		"  morning:\n" +
		"    # why: the standup\n" +
		"    grants: [fs.tree]\n" +
		"from-the-future: 1\n"
	_, errOut, file, err := configRun(t, configRegistry(t), text, "config", "set", "plugins.net.timeout", "20")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	for _, kept := range []string{"# my machine", "output: json   # json for the scripts", "# why: the standup",
		"grants: [fs.tree]", "from-the-future: 1", "timeout: 20"} {
		if !strings.Contains(file, kept) {
			t.Errorf("%q did not survive:\n%s", kept, file)
		}
	}
}

func TestConfigSetTwiceWritesNothingTheSecondTime(t *testing.T) {
	reg := configRegistry(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if _, errOut, err := runConfigAt(t, reg, path, "config", "set", "output", "json"); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	first, _ := os.ReadFile(path)
	before, _ := os.Stat(path)
	out, _, err := runConfigAt(t, reg, path, "config", "set", "output", "json", "-o", "pretty")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	after, _ := os.Stat(path)
	if !bytes.Equal(first, second) || !after.ModTime().Equal(before.ModTime()) {
		t.Error("the second run rewrote the file")
	}
	if !strings.Contains(out, "unchanged") || !strings.Contains(out, "already json") {
		t.Errorf("no say-so that nothing changed:\n%s", out)
	}
}

func TestConfigSetOfTheDefaultLeavesTheKeyOut(t *testing.T) {
	_, errOut, file, err := configRun(t, configRegistry(t), "output: json\n", "config", "set", "output", "pretty")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if strings.Contains(file, "output:") {
		t.Errorf("the default was pinned as a key:\n%s", file)
	}
	_, _, file, _ = configRun(t, configRegistry(t), "", "config", "set", "dashboard.columns", "0")
	if strings.Contains(file, "columns") {
		t.Errorf("an automatic width was pinned:\n%s", file)
	}
}

func TestConfigSetDryRunWritesNothing(t *testing.T) {
	out, _, file, err := configRun(t, configRegistry(t), "", "config", "set", "output", "json", "--dry-run", "-o", "pretty")
	if err != nil {
		t.Fatal(err)
	}
	if file != "" {
		t.Errorf("a dry run wrote:\n%s", file)
	}
	if !strings.Contains(out, "would write") || !strings.Contains(out, "would set output to json") {
		t.Errorf("a dry run does not say what it would do:\n%s", out)
	}
}

// The receipt's last word is the command that undoes it.
func TestConfigSetNamesTheWayBack(t *testing.T) {
	reg := configRegistry(t)
	out, _, _, err := configRun(t, reg, "", "config", "set", "plugins.net.timeout", "20", "-o", "pretty")
	if err != nil || !strings.Contains(out, "rta config unset plugins.net.timeout") {
		t.Errorf("a first write is undone by an unset:\n%s", out)
	}
	out, _, _, err = configRun(t, reg, "plugins:\n  net:\n    timeout: 15\n", "config", "set", "plugins.net.timeout", "20", "-o", "pretty")
	if err != nil || !strings.Contains(out, "rta config set plugins.net.timeout 15") {
		t.Errorf("a change is undone by setting the old value:\n%s", out)
	}
	out, _, _, _ = configRun(t, reg, "theme:\n  good: \"#00ff00\"\n", "config", "set", "theme.good", "#ff0000", "-o", "pretty")
	if !strings.Contains(out, "rta config set theme.good '#00ff00'") {
		t.Errorf("a colour is quoted in the line that puts it back:\n%s", out)
	}
}

func TestConfigUnsetRemovesAKeyAndTheBlockItLeavesEmpty(t *testing.T) {
	reg := configRegistry(t)
	text := "plugins:\n  net:\n    ping:\n      count: 7\n    timeout: 20\ntheme:\n  good: \"#00ff00\"\n"
	_, errOut, file, err := configRun(t, reg, text, "config", "unset", "plugins.net.ping.count")
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if strings.Contains(file, "ping") || !strings.Contains(file, "timeout: 20") {
		t.Errorf("file:\n%s", file)
	}
	_, _, file, _ = configRun(t, reg, text, "config", "unset", "plugins.net")
	if strings.Contains(file, "plugins") || !strings.Contains(file, "good") {
		t.Errorf("a plugin's whole section was not taken out, or took the rest:\n%s", file)
	}
	_, _, file, _ = configRun(t, reg, text, "config", "unset", "theme")
	if strings.Contains(file, "theme") || !strings.Contains(file, "timeout: 20") {
		t.Errorf("the theme was not taken out whole:\n%s", file)
	}
}

func TestConfigUnsetOfAKeyThatIsNotThereIsNotAnError(t *testing.T) {
	out, _, file, err := configRun(t, configRegistry(t), "", "config", "unset", "output", "-o", "pretty")
	if err != nil || file != "" {
		t.Fatalf("%v, wrote %q", err, file)
	}
	if !strings.Contains(out, "unchanged") {
		t.Errorf("no say-so:\n%s", out)
	}
}

func TestConfigGetPrintsTheBareValueForAScript(t *testing.T) {
	reg := configRegistry(t)
	text := "output: yaml\ndashboard:\n  hidden: [gen.overview, net.ping]\n  columns: 3\ntheme:\n  good: \"#00ff00\"\n" +
		"plugins:\n  net:\n    timeout: 20\n    ping:\n      count: 7\nprofiles:\n  prod:\n    note: the real one\n"
	for key, want := range map[string]string{
		"output":                 "yaml\n",
		"dashboard.columns":      "3\n",
		"dashboard.hidden":       "gen.overview\nnet.ping\n",
		"theme.good":             "#00ff00\n",
		"plugins.net.timeout":    "20\n",
		"plugins.net.ping.count": "7\n",
		"profiles.prod.note":     "the real one\n",
		"theme":                  "good: \"#00ff00\"\n",
	} {
		out, errOut, _, err := configRun(t, reg, text, "config", "get", key, "-o", "pretty")
		if err != nil || out != want {
			t.Errorf("get %s = %q, want %q (%v)\n%s", key, out, want, err, errOut)
		}
	}
}

func TestConfigGetOfAKeyThatIsNotSetSaysWhatItIsInstead(t *testing.T) {
	reg := configRegistry(t)
	out, errOut, _, err := configRun(t, reg, "", "config", "get", "plugins.net.timeout", "-o", "pretty")
	if err == nil || out != "" || ExitCode(err) != 1 {
		t.Fatalf("an unset key answered %q, %v", out, err)
	}
	if codeOf(t, errOut) != "core.config.unset" || !strings.Contains(errOut, "10 until then") ||
		!strings.Contains(errOut, "rta config set plugins.net.timeout 10") {
		t.Errorf("it does not say what the key is instead:\n%s", errOut)
	}
	_, errOut, _, err = configRun(t, reg, "", "config", "get", "nonsense", "-o", "pretty")
	if err == nil || codeOf(t, errOut) != "core.config.key.unknown" {
		t.Errorf("an unknown key: %v\n%s", err, errOut)
	}
}

// RTA_OUTPUT outranks the file, so what get answers is what a command would
// use, and show says where it came from.
func TestConfigGetAndShowSeeTheEnvironmentOverTheFile(t *testing.T) {
	reg := configRegistry(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("output: yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", path)
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	SetInstalled(reg)
	t.Cleanup(func() { SetInstalled(nil) })
	t.Setenv("RTA_OUTPUT", "csv")
	root := NewRoot(reg, "test")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"config", "get", "output"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "csv") {
		t.Errorf("get answered %q", out.String())
	}
	out.Reset()
	root = NewRoot(reg, "test")
	root.SetOut(&out)
	root.SetArgs([]string{"config"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "RTA_OUTPUT (the file says yaml)") {
		t.Errorf("show does not say where output comes from:\n%s", out.String())
	}
}

func TestBareConfigAndConfigShowAreOneAnswer(t *testing.T) {
	reg := configRegistry(t)
	text := "output: json\nplugins:\n  net:\n    timeout: 20\n"
	bare, _, _, err := configRun(t, reg, text, "config", "-o", "pretty")
	if err != nil {
		t.Fatal(err)
	}
	show, _, _, err := configRun(t, reg, text, "config", "show", "-o", "pretty")
	if err != nil {
		t.Fatal(err)
	}
	// The path differs by temp directory, and is the only thing that does.
	norm := func(s string) string {
		var keep []string
		for _, line := range strings.Split(s, "\n") {
			if !strings.HasPrefix(line, "file ") {
				keep = append(keep, line)
			}
		}
		return strings.Join(keep, "\n")
	}
	if norm(bare) != norm(show) {
		t.Errorf("bare:\n%s\nshow:\n%s", bare, show)
	}
	for _, want := range []string{"output", "json", "plugins.net.timeout", "20", "file", "$RTA_CONFIG", "yes, 4 lines"} {
		if !strings.Contains(bare, want) {
			t.Errorf("bare `rta config` lacks %q:\n%s", want, bare)
		}
	}
}

func TestConfigShowWithNoFileSaysSoAndHowToWriteOne(t *testing.T) {
	out, _, _, err := configRun(t, configRegistry(t), "", "config", "-o", "pretty")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no — every key is at its default") || !strings.Contains(out, "rta config set") {
		t.Errorf("%s", out)
	}
}

// A key nothing reads is shown as the file has it, marked, and a credential
// stated where it does not belong is never printed back.
func TestConfigShowMarksWhatNothingReadsAndNeverPrintsACredential(t *testing.T) {
	text := "plugins:\n  net:\n    timout: 5\n    token: hunter2\n  nope:\n    x: 1\n  gen:\n    password.symbols: true\n"
	out, _, _, err := configRun(t, configRegistry(t), text, "config", "-o", "pretty")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "hunter2") {
		t.Errorf("a credential was printed:\n%s", out)
	}
	for _, want := range []string{"nothing in net reads it", "no plugin named nope is registered", "redacted"} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}
}

// A broken default output format refuses most commands; these are how it is
// fixed, so they run, and say what they say in pretty.
func TestConfigCommandsRunOverAnOutputFormatNothingRenders(t *testing.T) {
	reg := configRegistry(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("output: jsno\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"config"}, {"config", "show"}, {"config", "path"}} {
		if _, errOut, err := runConfigAt(t, reg, path, args...); err != nil {
			t.Errorf("%v was refused: %v\n%s", args, err, errOut)
		}
	}
	// check says what is wrong, and that is the exit status it is for.
	if _, _, err := runConfigAt(t, reg, path, "config", "check"); ExitCode(err) != 1 {
		t.Errorf("check over a broken output exits %d, want 1", ExitCode(err))
	}
	if _, errOut, err := runConfigAt(t, reg, path, "config", "set", "output", "json"); err != nil {
		t.Fatalf("the fix was refused: %v\n%s", err, errOut)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "output: json") {
		t.Errorf("file:\n%s", b)
	}
}

func TestConfigPathIsThePathAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	out, _, err := runConfigAt(t, configRegistry(t), path, "config", "path", "-o", "pretty")
	if err != nil || out != path+"\n" {
		t.Errorf("%q, %v", out, err)
	}
}

// Without a config directory the file is ./.rta.yaml, which is honoured for the
// output format and the palette and for nothing else.
func TestConfigSetRefusesWhatAWorkingDirectoryFileWouldNotHonour(t *testing.T) {
	reg := configRegistry(t)
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("RTA_CONFIG", "")
	t.Chdir(t.TempDir())
	if config.TrustedPath() {
		t.Skip("this machine has a config directory")
	}
	run := func(args ...string) (string, error) {
		SetInstalled(reg)
		t.Cleanup(func() { SetInstalled(nil) })
		t.Setenv("RTA_DATA_DIR", t.TempDir())
		root := NewRoot(reg, "test")
		var out, errOut bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs(args)
		err := root.Execute()
		return errOut.String(), err
	}
	errOut, err := run("config", "set", "plugins.net.timeout", "20")
	if err == nil || codeOf(t, errOut) != "core.config.untrusted" {
		t.Errorf("a plugin key was written to a file nothing honours: %v\n%s", err, errOut)
	}
	if errOut, err := run("config", "set", "output", "json"); err != nil {
		t.Errorf("the output format is honoured from there: %v\n%s", err, errOut)
	}
	if b, _ := os.ReadFile(".rta.yaml"); !strings.Contains(string(b), "output: json") {
		t.Errorf(".rta.yaml:\n%s", b)
	}
}

// What `rta config show` withholds, `rta config get` does not print either: a
// credential stated in a `plugins:` section, whether one a profile could fill
// (net's token) or one nothing in the file reads (web's bearer, which is
// declared a secret and no config key), by the whole section, the whole block or
// the key itself.
//
// Fails without redactCredentials and credentialInputs: get printed the value
// and show printed the bearer.
func TestConfigGetNeverPrintsACredentialThatShowWithholds(t *testing.T) {
	reg := configRegistry(t)
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	if err := reg.Register(plugin.Plugin{Name: "web", Summary: "web", Capabilities: []plugin.Capability{
		{ID: "web.get", Summary: "get", Safety: plugin.Read, Run: run, Inputs: []plugin.Field{
			{Name: "bearer", Type: plugin.Secret, Help: "bearer token"},
			{Name: "timeout", Type: plugin.Int, Default: 5, Min: 1, Max: 60, Config: "timeout"},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	text := "plugins:\n  net:\n    token: hunter2\n  web:\n    bearer: hunter3\n    timeout: 7\n"
	for _, args := range [][]string{
		{"config", "get", "plugins"},
		{"config", "get", "plugins.net"},
		{"config", "get", "plugins.net.token"},
		{"config", "get", "plugins.web"},
		{"config", "get", "plugins.web.bearer"},
		{"config", "show", "-o", "pretty"},
	} {
		out, errOut, _, _ := configRun(t, reg, text, args...)
		if strings.Contains(out+errOut, "hunter") {
			t.Errorf("%v printed a credential:\n%s\n%s", args, out, errOut)
		}
	}
	out, _, _, err := configRun(t, reg, text, "config", "get", "plugins.web")
	if err != nil || !strings.Contains(out, "timeout: 7") || !strings.Contains(out, "bearer: (redacted") {
		t.Errorf("the rest of the section was not printed beside the redaction: %q, %v", out, err)
	}
	_, errOut, _, err := configRun(t, reg, text, "config", "get", "plugins.web.bearer")
	if err == nil || !strings.Contains(errOut, "core.config.key.secret") {
		t.Errorf("asking for a credential by name is not refused as one: %q, %v", errOut, err)
	}
}
