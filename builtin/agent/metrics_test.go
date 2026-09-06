package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/pkg/view"
)

func metricsBody(t *testing.T) string {
	t.Helper()
	v, err := run(t, "agent.metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	txt, ok := v.(view.Text)
	if !ok {
		t.Fatalf("want Text, got %s", view.TypeOf(v))
	}
	return txt.Body
}

// **One unescaped quote makes the whole file unparseable, not one series
// wrong.**
//
// An agent name is a word the operator typed and a capability id comes from
// a plugin, so either can carry a quote, a backslash or a newline. A textfile
// collector that cannot parse the file drops all of it, and the dashboard
// goes blank in exactly the way a dashboard goes blank when nothing is
// happening — which is the failure this format is worst at telling you about.
func TestALabelValueCannotBreakTheFile(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	hostile := `claude" injected="yes`
	if err := agentlog.Append(agentlog.Entry{
		Cap: "sys.cpu", Agent: hostile, Outcome: agentlog.Ran, Auth: agentlog.Open,
	}); err != nil {
		t.Fatal(err)
	}
	body := metricsBody(t)
	if strings.Contains(body, `agent="claude" injected="yes"`) {
		t.Errorf("a quote in an agent name escaped its label:\n%s", body)
	}
	if !strings.Contains(body, `agent="claude\" injected=\"yes"`) {
		t.Errorf("the name is not escaped the way the exposition format requires:\n%s", body)
	}
	// Every line is either a comment or `name{labels} value`. A broken escape
	// shows up here as a line with no value on the end.
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if i := strings.LastIndex(line, " "); i < 0 || strings.TrimSpace(line[i+1:]) == "" {
			t.Errorf("line is not a sample: %q", line)
		}
	}
}

// **The total has to survive retention or it is not a counter.**
//
// Counting the retained record alone falls every time a segment is dropped,
// Prometheus reads a fall as a process restart, and the graph loses the gap.
// The ledger already tracks what it retired, so the true total is the sum —
// and the retired count is published beside it so the gap is visible rather
// than merely handled.
func TestTheTotalIncludesWhatRetentionDropped(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	for range 3 {
		if err := agentlog.Append(agentlog.Entry{
			Cap: "sys.cpu", Outcome: agentlog.Ran, Auth: agentlog.Open,
		}); err != nil {
			t.Fatal(err)
		}
	}
	body := metricsBody(t)
	if !strings.Contains(body, "rta_agent_calls_total 3") {
		t.Errorf("total is not the number of calls:\n%s", body)
	}
	if !strings.Contains(body, "# TYPE rta_agent_calls_retired_total counter") {
		t.Errorf("nothing publishes what retention dropped:\n%s", body)
	}

	// The arithmetic on its own, because the case that matters cannot be
	// reached from a small fixture: a record only retires anything after
	// eight megabytes, and until it does, "everything ever" and "everything
	// still here" are the same number and a wrong sum passes.
	if got := callsEver(agentlog.Report{Entries: 12, Retired: 400}); got != 412 {
		t.Errorf("callsEver with 400 retired = %v, want 412 — the counter falls at the next rotation", got)
	}
}

// A family with no series still declares itself. An empty result is a fact —
// "no grants are active" — and a panel that goes blank because a series
// vanished looks identical to one that broke.
func TestAFamilyWithNothingInItStillDeclaresItself(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	body := metricsBody(t)
	for _, want := range []string{
		"# TYPE rta_grants_active gauge",
		"# TYPE rta_agent_calls_recorded_total counter",
		"# TYPE rta_record_intact gauge",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q from an empty record:\n%s", want, body)
		}
	}
}

// The metric worth alerting on: a record that stops verifying is either a bug
// or somebody editing it, and both are things to hear about in minutes.
func TestABrokenChainIsReportedAsZero(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)
	if err := agentlog.Append(agentlog.Entry{
		Cap: "sys.cpu", Outcome: agentlog.Ran, Auth: agentlog.Open,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metricsBody(t), "rta_record_intact 1") {
		t.Fatal("an untouched record does not verify")
	}
	segs, err := agentlog.Segments()
	if err != nil || len(segs) == 0 {
		t.Fatalf("no segments: %v", err)
	}
	// One character changed in a recorded argument: the quiet single-line edit
	// the chain exists to make visible.
	raw, err := os.ReadFile(segs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(segs[0], []byte(strings.Replace(string(raw), "sys.cpu", "sys.mem", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metricsBody(t), "rta_record_intact 0") {
		t.Errorf("an edited record still reports as intact:\n%s", metricsBody(t))
	}
}

// The agent namespace is for the person at the terminal, and the numbers are
// about the caller's own behaviour — so the scrape target is never a tool.
func TestMetricsAreNotServedOverMCP(t *testing.T) {
	if !capability(t, "agent.metrics").HumanOnly {
		t.Fatal("agent.metrics is reachable over MCP")
	}
}
