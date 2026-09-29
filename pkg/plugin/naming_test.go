package plugin

import (
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// One capability and one input, spelled the way each surface's reader finds
// them — and a caller inside the process, or a completion keystroke, reads
// the CLI's spelling rather than none.
func TestACapabilityAndAnInputAreNamedTheWayTheirSurfaceNamesThem(t *testing.T) {
	for _, tc := range []struct {
		s          Surface
		capability string
		input      string
	}{
		{SurfaceCLI, "`rta codec jwk`", "--secret-file"},
		{SurfaceMCP, "the `codec_jwk` tool", `the "secret-file" argument`},
		{SurfaceTUI, "`codec.jwk`", "the secret-file box"},
		{SurfaceUnknown, "`rta codec jwk`", "--secret-file"},
		{SurfaceCompletion, "`rta codec jwk`", "--secret-file"},
	} {
		if got := tc.s.CapabilityName("codec.jwk"); got != tc.capability {
			t.Errorf("CapabilityName over %q = %q, want %q", tc.s, got, tc.capability)
		}
		if got := tc.s.InputName("secret-file"); got != tc.input {
			t.Errorf("InputName over %q = %q, want %q", tc.s, got, tc.input)
		}
	}
}

// A positional input is a slot on the CLI's command line, never a flag, and
// an input like any other everywhere else.
func TestAPositionalInputIsNamedByItsSlotOnTheCLI(t *testing.T) {
	for s, want := range map[Surface]string{
		SurfaceCLI:     "<hostname>",
		SurfaceUnknown: "<hostname>",
		SurfaceMCP:     `the "hostname" argument`,
		SurfaceTUI:     "the hostname box",
	} {
		if got := s.ArgumentName("hostname"); got != want {
			t.Errorf("ArgumentName over %q = %q, want %q", s, got, want)
		}
	}
}

// A call is one command line on the CLI, and a capability with its inputs
// named everywhere else.
func TestACallIsNamedTheWayItsSurfaceMakesIt(t *testing.T) {
	for s, want := range map[Surface]string{
		SurfaceCLI:     "`rta note edit --title --body`",
		SurfaceUnknown: "`rta note edit --title --body`",
		SurfaceMCP:     `the ` + "`note_edit`" + ` tool with the "title" argument and the "body" argument`,
		SurfaceTUI:     "`note.edit` with the title box and the body box",
	} {
		if got := s.CapabilityWith("note.edit", "title", "body"); got != want {
			t.Errorf("CapabilityWith over %q = %q, want %q", s, got, want)
		}
	}
	if got := SurfaceMCP.CapabilityWith("note.list"); got != SurfaceMCP.CapabilityName("note.list") {
		t.Errorf("with no inputs, CapabilityWith = %q, want the capability's name", got)
	}
}

// A whole call, values and all, is a command line at a terminal, the tool
// and its arguments to an agent, and the capability with its boxes filled in
// the TUI — with a value a shell would split quoted on the command line.
func TestAWholeCallIsSpelledTheWayItsSurfaceMakesIt(t *testing.T) {
	args := []Arg{
		{Name: "key", Value: "db password", Positional: true},
		{Name: "revision", Value: 2},
		{Name: "force", Value: true},
	}
	for s, want := range map[Surface]string{
		SurfaceCLI:     `rta kv restore 'db password' --revision 2 --force`,
		SurfaceUnknown: `rta kv restore 'db password' --revision 2 --force`,
		SurfaceMCP:     `kv_restore {"force":true,"key":"db password","revision":2}`,
		SurfaceTUI:     `kv.restore key="db password" revision=2 force`,
	} {
		if got := s.Call("kv.restore", args...); got != want {
			t.Errorf("Call over %q = %s, want %s", s, got, want)
		}
	}
	if got := SurfaceMCP.Call("kv.list"); got != "kv_list {}" {
		t.Errorf("a call with no arguments over MCP = %s", got)
	}
	// A value is whatever somebody stored — an agent's kv key among them —
	// so the command line a person pastes runs none of it, and the tool's
	// arguments carry it as it is.
	stored := Arg{Name: "key", Value: "a$(touch pwned)`id`<b>&c", Positional: true}
	if got, want := SurfaceCLI.Call("kv.get", stored), `rta kv get 'a$(touch pwned)`+"`id`"+`<b>&c'`; got != want {
		t.Errorf("a stored value on the CLI = %s, want %s", got, want)
	}
	if got, want := SurfaceMCP.Call("kv.get", stored), `kv_get {"key":"a$(touch pwned)`+"`id`"+`<b>&c"}`; got != want {
		t.Errorf("a stored value over MCP = %s, want %s", got, want)
	}
	// A TUI box is not a shell: a value goes in as it is typed.
	if got, want := SurfaceTUI.Call("kv.init", Arg{Name: "identity", Value: "~/.ssh/id_$USER"}),
		"kv.init identity=~/.ssh/id_$USER"; got != want {
		t.Errorf("a value in the TUI = %s, want %s", got, want)
	}
	if got, want := SurfaceCLI.Call("note.add", Arg{Name: "title", Value: "", Positional: true}), `rta note add ''`; got != want {
		t.Errorf("an empty value on the CLI = %s, want %s", got, want)
	}
	// A placeholder stands for what the reader types, bare as a usage line
	// spells it; anything else in angle brackets is a value like any other.
	got := SurfaceCLI.Call("kv.get", Arg{Name: "key", Value: "<key>", Positional: true},
		Arg{Name: "out", Value: "<file>"}, Arg{Name: "note", Value: "<a b>"})
	if want := `rta kv get <key> --out <file> --note '<a b>'`; got != want {
		t.Errorf("placeholders on the CLI = %s, want %s", got, want)
	}
	if got, want := SurfaceMCP.Call("kv.get", Arg{Name: "key", Value: "<key>"}), `kv_get {"key":"<key>"}`; got != want {
		t.Errorf("a placeholder over MCP = %s, want %s", got, want)
	}
}

// A value in a TUI box holding a character a reader does not see as itself
// is quoted with the character named by its code point, as textclean.Record
// shows the same record, by the rule the shell's spelling reads (glyph.Seen).
// Asked of unicode.IsPrint, a Hangul filler, a Braille blank and a
// variation selector each counted printable, and strconv.Quote left them
// raw: `kv.get key=prod/db` and a filler read as the call on the bare record.
func TestABoxValueSpellsACharacterAReaderDoesNotSeeByItsCodePoint(t *testing.T) {
	for _, r := range []rune{0x3164, 0x115f, 0xffa0, 0x2800, 0x1d159, 0x16fe4, 0xfe0f, 0xe0100, 0x034f, 0xad, 0xa0, 0x202e} {
		got := SurfaceTUI.Call("kv.get", Arg{Name: "key", Value: "prod/db" + string(r)})
		want := `kv.get key="prod/db` + strings.Trim(strconv.QuoteRuneToASCII(r), "'") + `"`
		if got != want {
			t.Errorf("a value ending in U+%04X in the TUI = %s, want %s", r, got, want)
		}
	}
	// A byte that is not UTF-8 is named as a byte, and a quote or a backslash
	// in a quoted value is escaped, so the spelling reads back as the value.
	bad := SurfaceTUI.Call("kv.get", Arg{Name: "key", Value: "a b\xff\"\\"})
	if want := `kv.get key="a b\xff\"\\"`; bad != want {
		t.Errorf("a value with a byte that is not UTF-8 in the TUI = %s, want %s", bad, want)
	}
	if v, err := strconv.Unquote(strings.TrimPrefix(bad, "kv.get key=")); err != nil || v != "a b\xff\"\\" {
		t.Errorf("the quoted value reads back as %q (%v)", v, err)
	}
	// A character a reader sees as itself stays as it is, however far from
	// ASCII: an accented letter, an ideograph, an emoji.
	for _, s := range []string{"café", "東京", string(rune(0x1f600))} {
		if got, want := SurfaceTUI.Call("kv.get", Arg{Name: "key", Value: s}), "kv.get key="+s; got != want {
			t.Errorf("a plain value in the TUI = %s, want %s", got, want)
		}
	}
}

// A switch turned off is joined to its flag on the command line. pflag gives
// a Bool flag its value only after an equals sign, and reads the next word as
// an argument of the command's own: `--tls false` turned the switch on and
// handed the command a stray "false" — the opposite call to the one spelled.
func TestASwitchTurnedOffIsSpelledAsTheCommandLineReadsIt(t *testing.T) {
	args := []Arg{{Name: "key", Value: "k", Positional: true}, {Name: "tls", Value: false}, {Name: "force", Value: true}}
	for s, want := range map[Surface]string{
		SurfaceCLI:     `rta kv restore k --tls=false --force`,
		SurfaceUnknown: `rta kv restore k --tls=false --force`,
		SurfaceMCP:     `kv_restore {"force":true,"key":"k","tls":false}`,
		SurfaceTUI:     `kv.restore key=k tls=false force`,
	} {
		if got := s.Call("kv.restore", args...); got != want {
			t.Errorf("Call over %q = %s, want %s", s, got, want)
		}
	}

	flags := pflag.NewFlagSet("restore", pflag.ContinueOnError)
	tls := flags.Bool("tls", true, "")
	force := flags.Bool("force", false, "")
	words := strings.Fields(SurfaceCLI.Call("kv.restore", args...))
	if err := flags.Parse(words[3:]); err != nil {
		t.Fatal(err)
	}
	if *tls || !*force || flags.NArg() != 1 || flags.Arg(0) != "k" {
		t.Errorf("the spelled call reads back as tls=%v force=%v arguments %q, want false, true and [k]",
			*tls, *force, flags.Args())
	}
}

// A value holding a byte that is not UTF-8 has no spelling in a tool's
// arguments, which are JSON: encoding/json wrote U+FFFD in the byte's place,
// so an agent handed `kv_get {"key":"db` and the replacement character made
// the call on another key. It is spelled where its JSON would be as what it
// is, with the byte named as the TUI and textclean.Record name it, in a form
// no agent can send as it stands; every other value keeps its JSON.
func TestAValueThatIsNotUTF8IsNotSpelledAsAnotherForAnAgent(t *testing.T) {
	for _, c := range []struct {
		value any
		want  string
	}{
		{"db\xff", `kv_get {"key":<not UTF-8: "db\xff">,"out":"<file>"}`},
		{[]string{"ok", "db\xfe"}, `kv_get {"key":["ok",<not UTF-8: "db\xfe">],"out":"<file>"}`},
		{[]string{"ok", "db"}, `kv_get {"key":["ok","db"],"out":"<file>"}`},
	} {
		got := SurfaceMCP.Call("kv.get", Arg{Name: "key", Value: c.value}, Arg{Name: "out", Value: "<file>"})
		if got != c.want {
			t.Errorf("Call over MCP with %q = %s, want %s", c.value, got, c.want)
		}
		if strings.ContainsRune(got, 0xfffd) {
			t.Errorf("%s names the replacement character, a key the call does not name", got)
		}
	}
}

// A list is given the way each surface takes one: the flag once per element
// on a command line, a word each by its place, a JSON array to an agent, and
// the box's comma-separated text in the TUI. Go's own spelling of a slice,
// `--tag '[ops a,b]'`, was a call on one element nobody stored.
func TestAListIsGivenTheWayItsSurfaceTakesOne(t *testing.T) {
	args := []Arg{{Name: "title", Value: "x", Positional: true}, {Name: "tag", Value: []string{"ops", "a,b"}}}
	for s, want := range map[Surface]string{
		SurfaceCLI:     `rta note add x --tag ops --tag '"a,b"'`,
		SurfaceUnknown: `rta note add x --tag ops --tag '"a,b"'`,
		SurfaceMCP:     `note_add {"tag":["ops","a,b"],"title":"x"}`,
		SurfaceTUI:     `note.add title=x tag=<a list no box text holds: "ops", "a,b">`,
	} {
		if got := s.Call("note.add", args...); got != want {
			t.Errorf("Call over %q = %s, want %s", s, got, want)
		}
	}
	for _, c := range []struct {
		s         Surface
		got, want string
	}{
		{SurfaceCLI, SurfaceCLI.InputTo("tag", []string{"ops", "db"}), "--tag ops --tag db"},
		{SurfaceMCP, SurfaceMCP.InputTo("tag", []string{"ops", "db"}), `the "tag" argument set to ["ops","db"]`},
		{SurfaceTUI, SurfaceTUI.InputTo("tag", []string{"ops", "db"}), "the tag box set to ops,db"},
		{SurfaceTUI, SurfaceTUI.InputTo("tag", []string{"ops", "night shift"}), `the tag box set to "ops,night shift"`},
		{SurfaceCLI, SurfaceCLI.InputTo("tag", []string{}), "--tag ''"},
		{SurfaceCLI, SurfaceCLI.InputTo("tag", []string{"<key>", "<a b>"}), "--tag <key> --tag '<a b>'"},
		{SurfaceMCP, SurfaceMCP.InputTo("tag", []string{}), `the "tag" argument set to []`},
		{SurfaceCLI, SurfaceCLI.SettingTo("recipient", []string{"age1a", "age1b"}), "--recipient age1a --recipient age1b"},
		{SurfaceMCP, SurfaceMCP.SettingTo("recipient", []string{"age1a", "age1b"}), "the operator's `recipient` set to [\"age1a\",\"age1b\"]"},
		{SurfaceMCP, SurfaceMCP.SettingTo("recipient", []string{"a,b", " c"}), "the operator's `recipient` set to [\"a,b\",\" c\"]"},
		{SurfaceTUI, SurfaceTUI.SettingTo("recipient", []string{"age1a", "age1b"}), "the recipient box set to age1a,age1b"},
		// A list given by its place is the rest of the command line, which
		// the CLI does not split at commas.
		{SurfaceCLI, SurfaceCLI.Call("net.hosts.rm", Arg{Name: "hostname", Value: []string{"a,b", "c d"}, Positional: true}),
			"rta net hosts rm a,b 'c d'"},
		// CSV's reader turns a CR LF into a LF, so no list flag carries one.
		{SurfaceCLI, SurfaceCLI.InputTo("tag", []string{"ok", "a\r\nb"}), `--tag ok --tag <no list flag keeps its CR LF: "a\r\nb">`},
		// A box trims the space around each element and leaves an empty box
		// unanswered, so neither is a list any box text gives.
		{SurfaceTUI, SurfaceTUI.InputTo("tag", []string{" ops"}), `the tag box set to <a list no box text holds: " ops">`},
		{SurfaceTUI, SurfaceTUI.InputTo("tag", []string{""}), `the tag box set to <a list no box text holds: "">`},
	} {
		if c.got != c.want {
			t.Errorf("over %q: got %s, want %s", c.s, c.got, c.want)
		}
	}
}

// Every list the CLI spells reads back through a shell and pflag's
// StringSlice as the elements it was given — one holding a comma, a quote, a
// line break or nothing at all among them — and every list the TUI spells as
// box text reads back through the form's splitting.
func TestASpelledListReadsBackAsItsElements(t *testing.T) {
	lists := [][]string{
		{"ops"}, {"ops", "a,b"}, {`say "hi"`, `"`}, {"", "x"}, {"line\nbreak", "a\rb"},
		{" lead", "trail "}, {"a$(touch pwned)`id`", "<a b>"}, {},
	}
	var script strings.Builder
	for _, list := range lists {
		script.WriteString("set -- " + SurfaceCLI.InputTo("tag", list) + `; printf '%s\0' "$@"; printf '\1'` + "\n")
	}
	shell, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash to read the command lines back")
	}
	// In a directory of its own: a spelling that let the shell run what an
	// element holds would run it there, not in this package's source.
	cmd := exec.Command(shell, "-c", script.String())
	cmd.Dir = t.TempDir()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	runs := strings.Split(strings.TrimSuffix(string(out), "\x01"), "\x01")
	for i, list := range lists {
		words := strings.Split(strings.TrimSuffix(runs[i], "\x00"), "\x00")
		flags := pflag.NewFlagSet("add", pflag.ContinueOnError)
		tags := flags.StringSlice("tag", []string{"default"}, "")
		if err := flags.Parse(words); err != nil {
			t.Errorf("%s: %v", SurfaceCLI.InputTo("tag", list), err)
			continue
		}
		if !slices.Equal(*tags, list) || flags.NArg() != 0 {
			t.Errorf("%s reads back as %q and arguments %q, want %q",
				SurfaceCLI.InputTo("tag", list), *tags, flags.Args(), list)
		}
	}

	// The form's reading of a box: split at commas, each element trimmed.
	for _, list := range [][]string{{"ops"}, {"ops", "db"}, {"night shift", "x"}, {"a", "", "b"}, {"café", "東京"}} {
		text := strings.TrimPrefix(SurfaceTUI.InputTo("tag", list), "the tag box set to ")
		if unquoted, err := strconv.Unquote(text); err == nil {
			text = unquoted
		}
		elements := strings.Split(text, ",")
		for i := range elements {
			elements[i] = strings.TrimSpace(elements[i])
		}
		if !slices.Equal(elements, list) {
			t.Errorf("the box text %q reads back as %q, want %q", text, elements, list)
		}
	}
}

// Leaving inputs out is said the way the caller does it: a TUI form keeps its
// boxes, so there they are left empty rather than left off.
func TestInputsLeftOutAreSaidTheWayTheSurfaceLeavesThemOut(t *testing.T) {
	for s, want := range map[Surface]string{
		SurfaceCLI: "without --key and --secret-file",
		SurfaceMCP: `without the "key" argument and the "secret-file" argument`,
		SurfaceTUI: "with the key box and the secret-file box left empty",
	} {
		if got := s.WithoutInputs("key", "secret-file"); got != want {
			t.Errorf("WithoutInputs over %q = %q, want %q", s, got, want)
		}
	}
}

// The tool name is the one rule the bridge registers tools by, so a message
// naming a tool and the tool list cannot spell it two ways.
func TestToolNameReplacesEveryDot(t *testing.T) {
	if got := ToolName("pg.table.list"); got != "pg_table_list" {
		t.Errorf("ToolName = %q", got)
	}
	if got := SurfaceMCP.CapabilityName("pg.table.list"); !strings.Contains(got, ToolName("pg.table.list")) {
		t.Errorf("the MCP spelling %q does not carry the tool name", got)
	}
}

// The one phrase in which an agent may read a command line, spelled for the
// terminal of the person who types it.
func TestAskOperatorSpellsTheCommandForTheOperatorsTerminal(t *testing.T) {
	if got := AskOperator("grant allow kv.get"); got != "ask the operator to run `rta grant allow kv.get`" {
		t.Errorf("AskOperator = %q", got)
	}
}

// A refusal of a declared bound says where the declaration can be read, and
// over MCP that is the schema the tool came with, never a command an agent
// has no terminal to run.
func TestARefusalOfADeclarationPointsWhereItsReaderCanReadIt(t *testing.T) {
	c := Capability{ID: "sys.ps", Summary: "s", Safety: Read,
		Inputs: []Field{{Name: "limit", Type: Int, Min: 1, Max: 1000}}}
	for s, want := range map[Surface]string{
		SurfaceCLI: "`rta explain sys.ps` names it beside the input",
		SurfaceTUI: "`rta explain sys.ps` names it beside the input",
		SurfaceMCP: "the tool's input schema names it",
	} {
		verr := CheckInputs(c, NewRequest(map[string]any{"limit": 0}, false, false).WithSurface(s))
		if verr == nil || !strings.HasSuffix(verr.Hint, want) {
			t.Errorf("over %q: %v, want a hint ending %q", s, verr, want)
		}
	}
}

// A value from the config is overridden for one run by giving the input on
// the call — here a number written in quotes, which Resolve cannot hold
// inside its range the way it holds one that is merely too large — and the
// hint names it as the call's own surface gives it: a flag, a positional
// argument's usage slot, or a box in a form.
func TestTheOverrideForAConfiguredValueIsNamedForItsSurface(t *testing.T) {
	c := Capability{ID: "pg.status", Summary: "s", Safety: Read, Inputs: []Field{
		{Name: "limit", Type: Int, Min: 1, Max: 100, Config: "limit"},
		{Name: "table", Type: String, Options: []string{"a", "b"}, Positional: true, Config: "table"},
	}}
	for _, tc := range []struct {
		key  string
		v    any
		s    Surface
		want string
	}{
		{"limit", "3", SurfaceCLI, "or give --limit on the call to override it for one run"},
		{"limit", "3", SurfaceTUI, "or fill in the limit box to override it for one run"},
		{"table", "c", SurfaceCLI, "or give <table> on the call to override it for one run"},
	} {
		req := ResolveRequest(c, Inputs{Config: map[string]any{tc.key: tc.v}}, false, false).WithSurface(tc.s)
		verr := CheckInputs(c, req)
		if verr == nil || !strings.HasSuffix(verr.Hint, tc.want) {
			t.Errorf("%s over %q: %v, want a hint ending %q", tc.key, tc.s, verr, tc.want)
		}
	}
}

// A connection setting is named the way its reader changes it: a flag at a
// terminal, a box in a form, and to an agent the operator's setting — never
// an argument, which a Local input's tool does not have and the bridge
// would drop. A caller inside the process, or a keystroke, reads the CLI's.
func TestAConnectionSettingIsNamedTheWayItsReaderChangesIt(t *testing.T) {
	for _, tc := range []struct {
		s                  Surface
		one, two, three    string
		to, on, off, where string
		dns                string
	}{
		{SurfaceCLI, "--ca-file", "--host and --port", "--user, --host and --port",
			"--sslmode disable", "--tls", "--tls=false",
			"`rta explain pg.status` lists every input and which of the command line, the rta config, a profile and the environment can set it",
			"`rta net dns db.internal` shows what DNS returns"},
		{SurfaceUnknown, "--ca-file", "--host and --port", "--user, --host and --port",
			"--sslmode disable", "--tls", "--tls=false",
			"`rta explain pg.status` lists every input and which of the command line, the rta config, a profile and the environment can set it",
			"`rta net dns db.internal` shows what DNS returns"},
		{SurfaceTUI, "the ca-file box", "the host and port boxes", "the user, host and port boxes",
			"the sslmode box set to disable", "the tls box set to true", "the tls box set to false",
			"`rta explain pg.status` lists every input and which of the command line, the rta config, a profile and the environment can set it",
			"`net.dns name=db.internal` shows what DNS returns"},
		{SurfaceMCP, "the operator's `ca-file` setting", "the operator's `host` and `port` settings",
			"the operator's `user`, `host` and `port` settings",
			"the operator's `sslmode` set to disable", "the operator's `tls` set to true", "the operator's `tls` set to false",
			"ask the operator to run `rta explain pg.status`, which lists every setting and which of the rta config, " +
				"a profile and the environment the operator can set it in",
			"`net_dns {\"name\":\"db.internal\"}` shows what DNS returns"},
	} {
		for _, c := range []struct{ got, want string }{
			{tc.s.SettingName("ca-file"), tc.one},
			{tc.s.SettingName("host", "port"), tc.two},
			{tc.s.SettingName("user", "host", "port"), tc.three},
			{tc.s.SettingTo("sslmode", "disable"), tc.to},
			{tc.s.SettingTo("tls", true), tc.on},
			{tc.s.SettingTo("tls", false), tc.off},
			{tc.s.SettingsHint("pg.status"), tc.where},
			{tc.s.DNSHint("db.internal"), tc.dns},
		} {
			if c.got != c.want {
				t.Errorf("over %q: got %q, want %q", tc.s, c.got, c.want)
			}
		}
	}
	// A value a shell would split or run is one word on the CLI, as Call
	// spells it.
	if got := SurfaceCLI.SettingTo("ca-file", "my certs/ca.pem"); got != "--ca-file 'my certs/ca.pem'" {
		t.Errorf("a value with a space is spelled %q", got)
	}
}

// An input the caller gives is spelled with its value the way Call gives it:
// a flag and one shell word at a terminal, the argument and the JSON an agent
// sends, the box and what is typed into it. A value built from what a server
// holds runs nothing when the command line is pasted, and an agent reads a
// number as a number and a string as a string.
func TestAnInputIsGivenWithItsValueTheWayItsSurfaceGivesIt(t *testing.T) {
	for _, tc := range []struct {
		s                             Surface
		format, jobs, on, off, spaced string
	}{
		{SurfaceCLI, "--format directory", "--jobs 1", "--online", "--online=false", "--label 'nightly shop'"},
		{SurfaceUnknown, "--format directory", "--jobs 1", "--online", "--online=false", "--label 'nightly shop'"},
		{SurfaceMCP, `the "format" argument set to "directory"`, `the "jobs" argument set to 1`,
			`the "online" argument set to true`, `the "online" argument set to false`,
			`the "label" argument set to "nightly shop"`},
		{SurfaceTUI, "the format box set to directory", "the jobs box set to 1", "the online box set to true",
			"the online box set to false", `the label box set to "nightly shop"`},
	} {
		for _, c := range []struct{ got, want string }{
			{tc.s.InputTo("format", "directory"), tc.format},
			{tc.s.InputTo("jobs", 1), tc.jobs},
			{tc.s.InputTo("online", true), tc.on},
			{tc.s.InputTo("online", false), tc.off},
			{tc.s.InputTo("label", "nightly shop"), tc.spaced},
		} {
			if c.got != c.want {
				t.Errorf("over %q: got %s, want %s", tc.s, c.got, c.want)
			}
		}
	}

	if got, want := SurfaceCLI.InputTo("method", "$(touch pwned)"), "--method '$(touch pwned)'"; got != want {
		t.Errorf("a value holding a command substitution on the CLI = %s, want %s", got, want)
	}
	// The string "1" is not the number an int input takes, and an agent told
	// the one is not told the other.
	if got, want := SurfaceMCP.InputTo("jobs", "1"), `the "jobs" argument set to "1"`; got != want {
		t.Errorf("a string over MCP = %s, want %s", got, want)
	}
	// A placeholder stands for what the reader types: bare on a command line,
	// and a string in the JSON an agent sends.
	if got, want := SurfaceCLI.InputTo("database", "<name>"), "--database <name>"; got != want {
		t.Errorf("a placeholder on the CLI = %s, want %s", got, want)
	}
	if got, want := SurfaceMCP.InputTo("database", "<name>"), `the "database" argument set to "<name>"`; got != want {
		t.Errorf("a placeholder over MCP = %s, want %s", got, want)
	}

	// The command line reads back as the values it spells.
	flags := pflag.NewFlagSet("backup", pflag.ContinueOnError)
	online := flags.Bool("online", true, "")
	jobs := flags.Int("jobs", 4, "")
	words := strings.Fields(SurfaceCLI.InputTo("online", false) + " " + SurfaceCLI.InputTo("jobs", 1))
	if err := flags.Parse(words); err != nil {
		t.Fatal(err)
	}
	if *online || *jobs != 1 || flags.NArg() != 0 {
		t.Errorf("the spelled inputs read back as online=%v jobs=%d arguments %q, want false, 1 and none",
			*online, *jobs, flags.Args())
	}
}
