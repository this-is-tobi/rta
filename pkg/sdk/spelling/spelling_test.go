package spelling

import (
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

func demo() plugin.Plugin {
	return plugin.Plugin{Name: "demo", Summary: "demo plugin", Capabilities: []plugin.Capability{
		{ID: "demo.key.list", Summary: "list keys",
			Inputs:  []plugin.Field{{Name: "limit", Help: "at most this many"}, {Name: "jobs"}, {Name: "host"}},
			Actions: []plugin.Action{{Key: "enter", Label: "show"}},
			Toggles: []plugin.Toggle{{Key: "A", Label: "all of them"}}},
		{ID: "demo.key.get", Summary: "get one", Inputs: []plugin.Field{{Name: "key", Positional: true}},
			HumanOnly: true},
	}}
}

// The speller itself, held to the spellings it exists to catch and the ones
// it must let through: a test that passes because its scanner sees nothing
// guards nothing. Over one plugin, the way a plugin's tests and sdktest.Check
// build it; rta's own tests hold the same rules over the host's registry.
func TestTheSpellerTellsATerminalsSpellingFromEveryoneElses(t *testing.T) {
	sp := ForPlugin(demo())
	for text, want := range map[string]bool{
		"run `rta demo key list --limit 5` to see more":                      true,
		"raise --limit to see more":                                          true,
		"reach it with: rta demo key get user:1":                             true,
		"There is no `rta demo restore`":                                     true,
		"see rta demo restore for that":                                      true,
		"`rta net dns example.org` shows what DNS returns":                   true,
		"`demo key list --limit 5` finds it":                                 true,
		"`--jobs 4` runs four at once":                                       true,
		"With --detail, every key":                                           true,
		"use demo key list to find it":                                       true,
		"ask the operator to run `rta grant allow demo.key.get user:1`":      false,
		"the `demo_key_list` tool with the \"limit\" argument lists more":    false,
		"`demo.key.list` with the limit box filled lists more":               false,
		"raise `limit` to see more":                                          false,
		"the server is running with --read-only":                             true,
		"the server is running with `read_only` on":                          false,
		"restored through `pg_restore --jobs`":                               false,
		"`createdb --host=db app` makes it":                                  false,
		"the structured equivalent of `demo key list`":                       false,
		"-----BEGIN PUBLIC KEY-----":                                         false,
		"rta does not take the backup; the demo key list is what it read":    false,
		"the files under demo/key and reads demo key.go":                     false,
		"a grant (`grant.allow`, for `demo.key.get` and that key) allows it": false,
		// The host's own switches, which no capability declares, follow a
		// capability's words as its own inputs do; a flag nothing gives the
		// command is some other program's.
		"`demo key list --dry-run` says what it would do": true,
		"`demo key list --output json` is the same list":  true,
		"`demo key list --profile prod` reads prod's":     true,
		"`demo key get --server edge` reads edge's":       false,
		// A flag a sentence read out of source splices together.
		"raise --" + Operand + " to see more": true,
		"raise --%s to see more":              true,
		// The host's short switches, read as the ones they are short for:
		// in prose, opening a span, and after a capability's words.
		"as CSV with -o csv":                     true,
		"pass -y to skip the question":           true,
		"see -h for the rest":                    true,
		"`-o json` is the same list":             true,
		"`demo key list -o=json` is the same":    true,
		"`demo key list -y` skips the prompt":    true,
		"the same as `kubectl get pods -o wide`": false,
		"what `du -sh * | sort -h` answers":      false,
		// Any other letter is another program's option, named as a thing,
		// and a dash inside a word or a number is no flag.
		"every `-e` and every compose-file value": false,
		"request header: -H 'Key: Value'":         false,
		"a 24-h window, an e-mail, x-y":           false,
		"-----BEGIN PUBLIC KEY----- -o":           true,
		"":                                        false,
		"-":                                       false,
		"`-`":                                     false,
		"``":                                      false,
	} {
		if got := len(sp.Find(text, false)) > 0; got != want {
			t.Errorf("Find(%q) found a terminal's spelling: %v, want %v", text, got, want)
		}
	}
	if hits := sp.Find("run rta demo key list now", false); len(hits) != 1 {
		t.Errorf("a command line was found %d times: %q", len(hits), hits)
	}
	// A namespace with a digit in it is a namespace all the same.
	s3 := ForPlugin(plugin.Plugin{Name: "s3", Capabilities: []plugin.Capability{{ID: "s3.bucket.list"}}})
	for _, text := range []string{"`rta s3 bucket list` shows what is there", "see rta s3 bucket list"} {
		if len(s3.Find(text, true)) == 0 {
			t.Errorf("Find(%q) passed a command line naming a namespace with a digit", text)
		}
	}
	// Text only a terminal reads may name one of rta's own commands, `rta
	// explain` or `rta doctor`, or a namespace on its own, and never a command
	// in any other namespace: this plugin's, or a built-in's the speller was
	// never given.
	for text, want := range map[string]bool{
		"`rta explain " + Operand + "` lists every input":  false,
		"run `rta doctor` first":                           false,
		"the name `rta mcp serve --as` uses":               false,
		"the deny list covers `rta lock`":                  false,
		"`rta demo key get " + Operand + "` shows it":      true,
		"`rta demo restore` puts it back":                  true,
		"`rta net dns example.org` shows what DNS returns": true,
		"allow it with `rta grant allow demo.key.get`":     true,
	} {
		if got := len(sp.Find(text, true)) > 0; got != want {
			t.Errorf("Find(%q) for a terminal alone found a terminal's spelling: %v, want %v", text, got, want)
		}
	}
}

// The host's switches are the names it reserves, and --profile with the three
// that state a connection for one call, which it adds only where there is a
// connection to state. rta's own tests hold the list to the flags its command
// tree gives a capability.
func TestTheHostSwitchesAreTheNamesTheHostReserves(t *testing.T) {
	got := HostSwitches()
	want := append(plugin.ReservedInputs(), "profile", "kube", "secret", "secrets-from")
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("HostSwitches() = %q, want %q", got, want)
	}
	got[0] = "changed"
	if HostSwitches()[0] == "changed" {
		t.Error("HostSwitches hands out the list the speller reads")
	}
}

// Everything a plugin declares that every surface shows, in the order it is
// declared, each named by whose it is and where it is said, and a HumanOnly
// capability's marked as read at a terminal alone.
func TestDeclaredIsEverythingAPluginSaysAboutItself(t *testing.T) {
	var got []string
	for _, d := range Declared(demo()) {
		line := d.ID + " " + d.Where + ": " + d.Text
		if d.TerminalOnly {
			line += " (terminal)"
		}
		got = append(got, line)
	}
	want := []string{
		"demo summary: demo plugin",
		"demo.key.list summary: list keys",
		"demo.key.list description: ",
		"demo.key.list agent text: ",
		"demo.key.list help of limit: at most this many",
		"demo.key.list help of jobs: ",
		"demo.key.list help of host: ",
		"demo.key.list action enter: show",
		"demo.key.list toggle A: all of them",
		"demo.key.get summary: get one (terminal)",
		"demo.key.get description:  (terminal)",
		"demo.key.get agent text:  (terminal)",
		"demo.key.get help of key:  (terminal)",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Declared =\n%q\nwant\n%q", got, want)
	}
}

// What the SDK's connection helpers hand an agent spells nothing only a
// terminal can act on, so a hint built from them passes the rule the
// plugin's own text is held to; the one command line in them is the one
// AskOperator hands the operator.
func TestTheSDKsConnectionHelpersSpellNothingForATerminalOverMCP(t *testing.T) {
	sp := ForPlugin(demo())
	mcp := plugin.SurfaceMCP
	for _, text := range []string{
		mcp.SettingName("host"), mcp.SettingName("host", "jobs", "limit"),
		mcp.SettingTo("limit", 5), mcp.SettingTo("host", false),
		mcp.SettingsHint("demo.key.list"), mcp.DNSHint("db.internal"),
	} {
		if hits := sp.Find(text, false); len(hits) > 0 {
			t.Errorf("%q spells a terminal's: %q", text, hits)
		}
	}
}

// An example's title is read on every surface the example is shown on, so it
// is declared text like a summary is and is held to the same rule.
func TestDeclaredIncludesTheTitlesOfExamples(t *testing.T) {
	p := demo()
	p.Capabilities[0].Examples = []plugin.Example{
		{Title: "the first twenty", Inputs: map[string]any{"limit": 20}},
		{Title: "raise --limit for more", Inputs: map[string]any{"limit": 100}},
	}
	var titles, spelled []string
	for _, d := range Declared(p) {
		if strings.HasPrefix(d.Where, "title of example") {
			titles = append(titles, d.Where+": "+d.Text)
			if len(ForPlugin(p).Find(d.Text, d.TerminalOnly)) > 0 {
				spelled = append(spelled, d.Text)
			}
		}
	}
	want := []string{"title of example 1: the first twenty", "title of example 2: raise --limit for more"}
	if !slices.Equal(titles, want) {
		t.Errorf("example titles in Declared = %q, want %q", titles, want)
	}
	if !slices.Equal(spelled, []string{"raise --limit for more"}) {
		t.Errorf("the speller held these titles to a terminal's spelling: %q", spelled)
	}
}
