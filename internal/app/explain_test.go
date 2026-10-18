package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func TestExplainCatalog(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, reg, "explain")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sys.cpu", "cert.inspect", "net.ping", "read"} {
		if !strings.Contains(out, want) {
			t.Errorf("catalog missing %q", want)
		}
	}
}

// A plugin's name, or the start of one of its nested IDs, is asked about as
// often as a capability is: `rta explain sys` was "unknown capability" over
// three of the plugin's eight, picked in no order — `sys.cpu, sys.temp, sys.ps`
// for `sys.`, a different three for `sys`. What is under it is the answer.
func TestExplainListsWhatIsUnderAPluginOrAPrefix(t *testing.T) {
	reg, _ := NewRegistry()
	for arg, c := range map[string]struct{ want, notWant []string }{
		"sys":       {[]string{"sys.cpu", "sys.disk", "sys.host", "sys.load", "sys.mem", "sys.overview", "sys.ps", "sys.temp"}, []string{"net.dns", "note.add"}},
		"sys.":      {[]string{"sys.cpu", "sys.temp"}, []string{"net.dns"}},
		"net.hosts": {[]string{"net.hosts.add", "net.hosts.list", "net.hosts.rm", "net.hosts.toggle"}, []string{"net.dns", "net.overview"}},
	} {
		out, _, err := run(t, reg, "explain", arg)
		if err != nil {
			t.Fatalf("rta explain %s: %v", arg, err)
		}
		for _, want := range c.want {
			if !strings.Contains(out, want) {
				t.Errorf("rta explain %s: %q is missing:\n%s", arg, want, out)
			}
		}
		for _, notWant := range c.notWant {
			if strings.Contains(out, notWant) {
				t.Errorf("rta explain %s: %q is not under it:\n%s", arg, notWant, out)
			}
		}
	}
	// A word that is no prefix of anything is still an unknown capability.
	if _, _, err := run(t, reg, "explain", "syss"); err == nil {
		t.Error("`rta explain syss` listed something")
	}
	// And suggestions that score alike come in the registry's order, not in
	// whatever order an unstable sort left them: eight capabilities tied for a
	// word that matched only the plugin's name were offered as `sys.cpu,
	// sys.temp, sys.ps`.
	_, errOut, err := run(t, reg, "explain", "sys.zz")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Hint != "did you mean: sys.cpu, sys.disk, sys.host" {
		t.Errorf("tied suggestions for sys.zz: %v (stderr %q)", err, errOut)
	}
}

func TestExplainCard(t *testing.T) {
	reg, _ := NewRegistry()
	out, _, err := run(t, reg, "explain", "net.port")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"net.port", "rta net port <host>", "net_port", "input:ports"} {
		if !strings.Contains(out, want) {
			t.Errorf("card missing %q:\n%s", want, out)
		}
	}
}

// What an input is, as data. The card writes it in a sentence — "int, default
// 20, a value from 1 to 500" — and `-o json` carried it only inside that string,
// so a script had to take the sentence apart to learn a type, a default or a
// range. The page is the card and, under it, a table with a cell for each.
func TestExplainAnswersWithTheCardAndItsInputsAsATable(t *testing.T) {
	reg, _ := NewRegistry()
	out, _, err := run(t, reg, "explain", "git.log", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Type  string `json:"type"`
		Items []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			View  struct {
				Type    string `json:"type"`
				Columns []struct {
					Name string `json:"name"`
				} `json:"columns"`
				Rows [][]string `json:"rows"`
			} `json:"view"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &page); err != nil {
		t.Fatalf("explain -o json is not JSON: %v\n%s", err, out)
	}
	if page.Type != "sections" || len(page.Items) != 2 || page.Items[0].ID != "card" || page.Items[1].ID != "inputs" ||
		page.Items[1].View.Type != "table" {
		t.Fatalf("the page is %q with %d items, want the card and an inputs table:\n%s", page.Type, len(page.Items), out)
	}
	var names []string
	for _, c := range page.Items[1].View.Columns {
		names = append(names, c.Name)
	}
	want := "Name Type Required Default Min Max Options Config Positional Local Secret Help"
	if strings.Join(names, " ") != want {
		t.Errorf("columns = %q, want %q", strings.Join(names, " "), want)
	}
	limit := map[string]string{}
	for _, row := range page.Items[1].View.Rows {
		if row[0] == "limit" {
			for i, name := range names {
				limit[name] = row[i]
			}
		}
	}
	for column, wanted := range map[string]string{
		"Type": "int", "Default": "20", "Min": "1", "Max": "500", "Config": "log.limit", "Required": "", "Secret": "",
	} {
		if limit[column] != wanted {
			t.Errorf("git.log's limit %s = %q, want %q (row %v)", column, limit[column], wanted, limit)
		}
	}
}

// The table is the card's inputs and nothing the card leaves out: the options of
// a choice, the bounds of a duration as it was written, a secret that is never
// offered to an agent, and the --detail switch the host adds to a capability that
// has a detailed view.
func TestTheInputsTableCarriesWhatTheCardSays(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.thing.get", Safety: plugin.Read, Detailed: true,
		Inputs: []plugin.Field{
			{Name: "name", Type: plugin.String, Positional: true, Required: true, Help: "which one"},
			{Name: "mode", Type: plugin.String, Options: []string{"fast", "slow"}, Default: "fast"},
			{Name: "wait", Type: plugin.Duration, Min: "1s", Max: "5m", Default: "10s"},
			{Name: "token", Type: plugin.Secret, Local: true},
			{Name: "empty", Type: plugin.String, Default: ""},
		},
	}
	byName := map[string][]string{}
	for _, row := range inputsTable(c).Rows {
		byName[row[0]] = row
	}
	// Name Type Required Default Min Max Options Config Positional Local Secret Help
	for name, want := range map[string][]string{
		"name":   {"name", "string", "true", "", "", "", "", "", "true", "", "", "which one"},
		"mode":   {"mode", "string", "", "fast", "", "", "fast|slow", "", "", "", "", ""},
		"wait":   {"wait", "duration", "", "10s", "1s", "5m", "", "", "", "", "", ""},
		"token":  {"token", "secret", "", "", "", "", "", "", "", "true", "true", ""},
		"empty":  {"empty", "string", "", "", "", "", "", "", "", "", "", ""},
		"detail": {"detail", "bool", "", "false", "", "", "", "", "", "", "", "return the full detailed view instead of the compact summary"},
	} {
		if got := byName[name]; strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("input %s = %q, want %q", name, got, want)
		}
	}
	if none := inputsTable(plugin.Capability{ID: "demo.none"}); len(none.Rows) != 0 || none.Empty == "" {
		t.Errorf("a capability with no inputs has rows %v and no sentence for a person: %q", none.Rows, none.Empty)
	}
}

// A grant narrows to one record, and for a call that lists several hosts the
// record is one of them: the card read "optionally naming one targets".
func TestExplainCardSaysWhatAGrantNarrowsAListInputTo(t *testing.T) {
	reg, _ := NewRegistry()
	for id, want := range map[string]string{
		"cert.expiry": "optionally naming one of the targets",
		"kv.get":      "optionally naming one key",
	} {
		out, _, err := run(t, reg, "explain", id)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, want) || strings.Contains(out, "one targets") {
			t.Errorf("%s: card does not say %q:\n%s", id, want, out)
		}
	}
}

// A range refusal sends the reader here — "`rta explain net.listen` names it
// beside the input" — and the card printed every other declared fact about
// the input but not its bounds.
func TestExplainCardNamesAnInputsRangeBesideIt(t *testing.T) {
	reg, _ := NewRegistry()
	for id, want := range map[string]string{
		"net.listen": "a value from 1 to 65535",
		"sys.ps":     "a value from 1 to",
	} {
		out, _, err := run(t, reg, "explain", id)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, want) {
			t.Errorf("%s: card does not name the range %q:\n%s", id, want, out)
		}
	}
}

// The card is the authoritative reference, so what the TUI can do with a
// result is on it: a plugin author reads their own declaration back, and an
// operator learns which key does what without opening the TUI to find out.
func TestExplainCardListsWhatTheTUICanDoWithTheResult(t *testing.T) {
	reg, _ := NewRegistry()
	out, _, err := run(t, reg, "explain", "note.list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"action:x", "remove → note.rm", "from the row", "toggle:A", "show done", "--all"} {
		if !strings.Contains(out, want) {
			t.Errorf("note.list's card is missing %q:\n%s", want, out)
		}
	}
	out, _, err = run(t, reg, "explain", "gen.password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "copy") || !strings.Contains(out, "Password") {
		t.Errorf("gen.password's card does not say what c copies:\n%s", out)
	}
	out, _, err = run(t, reg, "explain", "agent.pending")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "live") {
		t.Errorf("agent.pending's card does not say it is live:\n%s", out)
	}
}

// The card is where a person or an agent is sent to find out how to invoke a
// capability, and it was the one surface that never mentioned --detail: the
// tool description advertises it in CLI syntax and the MCP schema publishes
// it as a boolean, so `rta explain sys.overview` described a capability whose
// richest view appeared not to exist. Asserted over the whole catalogue
// rather than one ID, because the disagreement was catalogue-wide.
func TestExplainCardShowsTheDetailFlag(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	detailed := 0
	for _, c := range reg.Capabilities() {
		if !c.Detailed {
			continue
		}
		detailed++
		out, _, err := run(t, reg, "explain", c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "--detail") {
			t.Errorf("%s: invocation omits --detail:\n%s", c.ID, out)
		}
		if !strings.Contains(out, "input:detail") {
			t.Errorf("%s: card does not say what --detail does:\n%s", c.ID, out)
		}
	}
	if detailed == 0 {
		t.Fatal("no Detailed capability in the catalogue: this test proves nothing")
	}
}

// …and it is the flag of a capability that has a detail view, not decoration
// on every card.
func TestExplainCardOmitsDetailWhereThereIsNone(t *testing.T) {
	reg, _ := NewRegistry()
	out, _, err := run(t, reg, "explain", "net.port")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "detail") {
		t.Errorf("net.port has no detail view but its card offers one:\n%s", out)
	}
}

func TestExplainSuggestsClosestMatch(t *testing.T) {
	reg, _ := NewRegistry()
	_, errOut, err := run(t, reg, "explain", "sys.cpuu")
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "core.capability.unknown" {
		t.Fatalf("want core.capability.unknown, got %v", err)
	}
	if !strings.Contains(errOut, "sys.cpu") {
		t.Errorf("suggestion missing from stderr:\n%s", errOut)
	}
	if ExitCode(err) != 1 {
		t.Errorf("exit = %d, want 1", ExitCode(err))
	}
}

func TestDoctorReport(t *testing.T) {
	reg, _ := NewRegistry()
	out, _, err := run(t, reg, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"capabilities", "config", "kv store"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor missing %q:\n%s", want, out)
		}
	}
}

// The card answers "why did that host appear?" and "what do I write?" — and
// until it named the file, not "into which file". That third answer lived in
// `rta doctor`, which is a different command for the last third of one
// question.
func TestExplainNamesTheFileTheConfigBlockGoesIn(t *testing.T) {
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "demo", Summary: "demo",
		Capabilities: []plugin.Capability{
			{ID: "demo.configured", Summary: "reads config", Safety: plugin.Read,
				Inputs: []plugin.Field{{Name: "host", Type: plugin.String, Config: "host", Help: "h"}},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "ok"}, nil
				}},
			{ID: "demo.plain", Summary: "reads nothing", Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "ok"}, nil
				}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, reg, "explain", "demo.configured")
	if err != nil {
		t.Fatal(err)
	}
	// run() points RTA_CONFIG at its own temp file and t.Setenv restores it
	// at the end of the test, so config.Path() here is the path the command
	// just used. Asserting on that rather than on the substring "config.yaml"
	// keeps this about the real location and not about a filename.
	if want := config.Path(); !strings.Contains(out, want) {
		t.Errorf("explain does not name the config file %s:\n%s", want, out)
	}

	// And says nothing when nothing here reads one — a path printed under a
	// capability that cannot be configured is an invitation to edit a file
	// that will not change anything.
	out, _, err = run(t, reg, "explain", "demo.plain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "config file") {
		t.Errorf("explain named a config file for a capability that reads none:\n%s", out)
	}
}

// The card names an environment variable for the inputs that read one, and
// for no others.
//
// Local and EnvFallback were the same set when this line was written, so
// "Local" was a correct test for "filled from $RTA_<NS>_<INPUT>". Splitting
// EnvFallback out of it separated
// them — EnvFallback is for the fields that genuinely are credentials — and
// Closing the redirect hole then marked every connection input on pg, s3
// and vault Local *without*
// EnvFallback, deliberately, because a field that merely chooses a
// destination must not be fillable from an ambient variable. The card did not
// follow: `rta explain pg.status` told operators that --host comes from
// $RTA_PG_HOST, and eleven inputs across the three plugins documented a
// channel that reads nothing.
//
// Worth a test rather than a fix alone, because the failure is silent in
// exactly the wrong place: the page is what somebody consults when a
// connection is already failing, so a fictitious variable sends them to
// export something that cannot help and to stop looking at the config key
// that would have.
//
// Written against a synthetic capability rather than pg's declaration: what
// is under test is which *rule* the card applies, and that has to hold for
// plugins nobody has written yet.
func TestExplainNamesAnEnvironmentVariableOnlyWhereOneIsRead(t *testing.T) {
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "demo", Summary: "demo",
		Capabilities: []plugin.Capability{
			{ID: "demo.connect", Summary: "connects somewhere", Safety: plugin.Read,
				Inputs: []plugin.Field{
					// Chooses a destination. Local so no MCP caller may aim it,
					// and no EnvFallback, so nothing fills it from the ambient
					// environment. Exactly plugins/pg's `host`.
					{Name: "host", Type: plugin.String, Config: "host", Local: true, Help: "where"},
					// A credential. The one shape that does read a variable.
					{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true, Help: "secret"},
					// Not Local at all: an ordinary payload input.
					{Name: "table", Type: plugin.String, Help: "what"},
				},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "ok"}, nil
				}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	out, _, err := run(t, reg, "explain", "demo.connect")
	if err != nil {
		t.Fatal(err)
	}

	// The credential's variable is named: "how do I give this thing its
	// password" must stay answerable from this page.
	if want := plugin.LocalEnvVar("demo.connect", "password"); !strings.Contains(out, "$"+want) {
		t.Errorf("card does not name $%s, so nothing says where the credential comes from:\n%s", want, out)
	}
	// The destination's is not, because nothing reads it.
	if bogus := plugin.LocalEnvVar("demo.connect", "host"); strings.Contains(out, "$"+bogus) {
		t.Errorf("card claims --host is filled from $%s, which plugin.Resolve never reads:\n%s", bogus, out)
	}
	// Both are still marked local: the fix must not cost the asymmetry
	// between the CLI and MCP its one mention.
	if n := strings.Count(out, "local (never offered to MCP callers)"); n != 2 {
		t.Errorf("expected both Local inputs marked local, got %d:\n%s", n, out)
	}
	// And the config key survives, which is the answer the operator actually
	// needs once the fictitious variable is gone.
	if !strings.Contains(out, "host") || !strings.Contains(out, "from config") {
		t.Errorf("card no longer says where --host does come from:\n%s", out)
	}
}

// A fenced input refuses the call that sets its switch, and the refusal names
// the switch, so the card an operator is sent to says what it widens.
func TestExplainCardSaysWhichInputASwitchWidens(t *testing.T) {
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "svc", Summary: "svc plugin",
		Capabilities: []plugin.Capability{{
			ID: "svc.list", Summary: "list", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "namespace", Type: plugin.String, Config: "namespace", Help: "namespace"},
				{Name: "all-namespaces", Type: plugin.Bool, Widens: "namespace", Help: "every namespace"},
			},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		}},
	}); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, reg, "explain", "svc.list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "widens --namespace") {
		t.Errorf("the card does not say what --all-namespaces widens:\n%s", out)
	}
}

// The card is where an operator reads where an input comes from, and "from
// config plugins.kube.namespace" is true of the plugin's own section and not of a
// profile's `set:`: the record a capability works on is named by the call. It
// says so on the input, and only there.
func TestExplainCardSaysAScopeIsNamedByTheCallAndNotByAProfile(t *testing.T) {
	reg := clusterishRegistry(t)
	out, _, err := run(t, reg, "explain", "cluster.pod.list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "the scope — a call names it, and a profile's `set:` never fills it") {
		t.Errorf("the card does not say a profile never fills the namespace of a read:\n%s", out)
	}
	// An input a profile does fill says nothing of the kind.
	out, _, err = run(t, reg, "explain", "cluster.sa.provision")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "never fills it") {
		t.Errorf("the card says a profile never fills an input it does fill:\n%s", out)
	}
}

func groupedPlugin() plugin.Plugin {
	return plugin.Plugin{
		Name: "svc", Summary: "svc plugin",
		Capabilities: []plugin.Capability{{
			ID: "svc.get", Summary: "get", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "bucket", Type: plugin.String, Required: true, Help: "bucket", Group: "storage"},
				{Name: "limit", Type: plugin.Int, Config: "limit", Help: "limit"},
				{Name: "host", Type: plugin.String, Local: true, Config: "host", Help: "host"},
				{Name: "user", Type: plugin.String, Local: true, Config: "user", Help: "user", Group: "login"},
			},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		}},
	}
}

func inputLine(out, name string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "input:"+name+" ") {
			return line
		}
	}
	return ""
}

// The card lists each input with the block a form shows it in, in the words it
// uses for every other fact about the input: the declared name where there is
// one, the name a connection input falls back to where there is not, and nothing
// for a call's own input that is in no block.
func TestExplainCardSaysWhichBlockEachInputIsShownIn(t *testing.T) {
	reg := registry.New()
	if err := reg.Register(groupedPlugin()); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, reg, "explain", "svc.get")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"bucket": "string, required, group storage",
		"host":   "string, group network",
		"user":   "string, group login",
	} {
		if line := inputLine(out, name); !strings.Contains(line, want) {
			t.Errorf("input %s: %q, want %q", name, line, want)
		}
	}
	if line := inputLine(out, "limit"); strings.Contains(line, "group") {
		t.Errorf("a call's own input with no group declared is placed in one: %q", line)
	}
}
