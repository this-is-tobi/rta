package agent

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/builtin/internal/compact"
	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/pkg/view"
)

func logTable(t *testing.T, values map[string]any) view.Table {
	t.Helper()
	v, err := run(t, "agent.log", values)
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := v.(view.Sections); ok {
		return s.Items[0].View.(view.Table)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("want a Table, got %s", view.TypeOf(v))
	}
	return tbl
}

func columnNames(tbl view.Table) []string {
	names := make([]string, len(tbl.Columns))
	for i, c := range tbl.Columns {
		names[i] = c.Name
	}
	return names
}

func seed(t *testing.T) {
	t.Helper()
	isolate(t)
	for _, e := range []agentlog.Entry{
		{Cap: "sys.cpu", Agent: "claude", Client: "claude-code", Session: "aaaa1111", Outcome: agentlog.Ran, Auth: agentlog.Open},
		{Cap: "kv.get", Agent: "claude", Client: "claude-code", Session: "aaaa1111", Records: []string{"db-password"},
			Outcome: agentlog.Refused, Auth: agentlog.Blocked, Code: "core.grant.required",
			Reason: "no active grant for kv.get db-password"},
		{Cap: "note.add", Agent: "claude", Client: "claude-code", Session: "aaaa1111",
			Outcome: agentlog.Refused, Auth: agentlog.Blocked, Code: "core.mcp.badargs", Reason: `unknown argument: "text"`},
		{Cap: "note.add", Agent: "claude", Client: "claude-code", Session: "aaaa1111",
			Outcome: agentlog.Ran, Auth: agentlog.Live},
		{Cap: "net.dns", Agent: "claude", Client: "claude-code", Session: "aaaa1111", Records: []string{"example.org"},
			Outcome: agentlog.Ran, Auth: agentlog.Standing},
		{Cap: "sys.host", Agent: "claude", Client: "claude-code", Session: "aaaa1111",
			Outcome: agentlog.Failed, Auth: agentlog.Open, Code: "sys.host.read", Reason: "reading host info: no ioreg"},
	} {
		appendRow(t, e)
	}
}

// What a person at a terminal scans for is when, what, which record and what
// became of it. Twelve columns at eighty cells are drawn as a card of nine
// lines for every call, and thirty calls are a screen of scrolling.
func TestOnATerminalTheLogIsFourColumnsAndOneLinePerCall(t *testing.T) {
	defer compact.Pretend(true)()
	seed(t)
	tbl := logTable(t, nil)
	if got, want := columnNames(tbl), []string{"at", "capability", "record", "result"}; !slices.Equal(got, want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}
	var results []string
	for _, r := range tbl.Rows {
		results = append(results, r[3])
	}
	want := []string{"ran", "refused · no grant", "refused · bad argument", "ran · you approved", "ran · by grant", "failed · sys.host.read"}
	if !slices.Equal(results, want) {
		t.Errorf("results = %q\nwant      %q", results, want)
	}
	if tbl.Rows[1][2] != "db-password" || tbl.Rows[0][2] != "—" {
		t.Errorf("the record column is %q and %q, want the record named and a dash for none", tbl.Rows[1][2], tbl.Rows[0][2])
	}
	for i, r := range tbl.Rows {
		if w := len(strings.Join(r, "  ")); w > 70 {
			t.Errorf("row %d is %d cells wide: %q", i, w, r)
		}
	}
}

// Who joins the compact rows only when there is more than one to tell apart:
// a column that says "claude" thirty times says nothing, and the day a second
// agent calls its arrival is the news. The session id is a finer question: two
// agents with a server each are told apart by their names, so it joins only
// when one agent has more than one server open, which a name cannot say.
func TestWhoColumnsJoinTheCompactLogOnlyWhenThereIsMoreThanOne(t *testing.T) {
	defer compact.Pretend(true)()
	seed(t)
	appendRow(t, agentlog.Entry{Cap: "sys.cpu", Agent: "cursor", Session: "bbbb2222", Outcome: agentlog.Ran, Auth: agentlog.Open})
	cols := columnNames(logTable(t, nil))
	if !slices.Contains(cols, "agent") || slices.Contains(cols, "session") {
		t.Errorf("two agents with a server each: %v, want the agent and not the session", cols)
	}
	appendRow(t, agentlog.Entry{Cap: "sys.cpu", Agent: "claude", Session: "cccc3333", Outcome: agentlog.Ran, Auth: agentlog.Open})
	cols = columnNames(logTable(t, nil))
	for _, want := range []string{"agent", "session"} {
		if !slices.Contains(cols, want) {
			t.Errorf("claude from two servers beside cursor, and no %s column: %v", want, cols)
		}
	}
	if cols := columnNames(logTable(t, map[string]any{"agent": "cursor"})); slices.Contains(cols, "agent") || slices.Contains(cols, "session") {
		t.Errorf("one agent, one session, and still %v", cols)
	}
}

// A script, a pipe and a file are promised every field of every row, and so is
// a person who asks for the detail.
func TestAPipeAndDetailKeepEveryField(t *testing.T) {
	seed(t)
	full := []string{"seq", "at", "capability", "agent", "session", "record", "arguments", "profile", "outcome", "authorized", "code", "why"}
	func() {
		defer compact.Pretend(false)()
		if got := columnNames(logTable(t, nil)); !slices.Equal(got, full) {
			t.Errorf("piped: columns = %v, want %v", got, full)
		}
	}()
	defer compact.Pretend(true)()
	withClient := slices.Insert(slices.Clone(full), 4, "client")
	got := logTable(t, map[string]any{"detail": true})
	if !slices.Equal(columnNames(got), withClient) {
		t.Errorf("--detail: columns = %v, want %v", columnNames(got), withClient)
	}
	if got.Rows[0][4] != "claude-code" {
		t.Errorf("the client's own claim is %q, want what the client announced", got.Rows[0][4])
	}
}

func TestResultPhrasesAreReadFromTheCodesTheRecordAlreadyCarries(t *testing.T) {
	for _, tc := range []struct {
		e    agentlog.Entry
		want string
	}{
		{agentlog.Entry{Outcome: agentlog.Ran, Auth: agentlog.Open}, "ran"},
		{agentlog.Entry{Outcome: agentlog.Ran, Auth: agentlog.Standing, Role: "dev"}, "ran · by role dev"},
		{agentlog.Entry{Outcome: agentlog.Ran, Auth: agentlog.Operator, Credential: "operator:dash"}, "ran · operator:dash"},
		{agentlog.Entry{Outcome: agentlog.Failed, Code: "pg.query.timeout"}, "failed · pg.query.timeout"},
		{agentlog.Entry{Outcome: agentlog.Refused, Code: "core.profile.required"}, "refused · needs a profile"},
		{agentlog.Entry{Outcome: agentlog.Refused, Code: "core.lock.frozen"}, "refused · locked"},
		{agentlog.Entry{Outcome: agentlog.Refused, Code: "core.consent.declined"}, "refused · you declined"},
		{agentlog.Entry{Outcome: agentlog.Refused, Code: "core.mcp.path.outside"}, "refused · path outside the roots"},
		{agentlog.Entry{Outcome: agentlog.Refused, Code: "kv.passphrase.missing"}, "refused · kv.passphrase.missing"},
		// A row from before the code and the reason were stored apart.
		{agentlog.Entry{Outcome: agentlog.Refused, Reason: "core.grant.required: no active grant for kv.get"}, "refused · no grant"},
		{agentlog.Entry{Outcome: agentlog.Refused, Reason: "no active grant, a sentence with no code"}, "refused"},
	} {
		if got := resultPhrase(tc.e); got != tc.want {
			t.Errorf("%+v = %q, want %q", tc.e, got, tc.want)
		}
	}
}

// "I do not see it" reads as "it did not happen", so a listing that stops at
// its limit says how much it left out, and the JSON total is the real count.
func TestTheLogSaysHowManyCallsItLeftOut(t *testing.T) {
	isolate(t)
	for range 47 {
		appendRow(t, agentlog.Entry{Cap: "sys.cpu", Agent: "claude", Outcome: agentlog.Ran, Auth: agentlog.Open})
	}
	tbl := logTable(t, nil)
	if len(tbl.Rows) != 30 || tbl.Total != 47 {
		t.Fatalf("%d rows of a total of %d, want 30 of 47", len(tbl.Rows), tbl.Total)
	}
	if len(tbl.Warnings) != 1 || tbl.Warnings[0].Code != "agent.log.older" || !tbl.Warnings[0].Advisory ||
		!strings.Contains(tbl.Warnings[0].Message, "17 older calls are not shown") {
		t.Fatalf("warnings = %+v", tbl.Warnings)
	}
	if h := tbl.Warnings[0].Hint; !strings.Contains(h, "--limit 47 shows all of them") || !strings.Contains(h, "--since, --agent and --refused") {
		t.Errorf("the hint does not say how to see the rest: %q", h)
	}
	if all := logTable(t, map[string]any{"limit": 47}); len(all.Warnings) != 0 || all.Total != 47 {
		t.Errorf("the whole record asked for: total %d, warnings %+v", all.Total, all.Warnings)
	}
}

func TestAFilterCountsWhatMatchesNotWhatWasRead(t *testing.T) {
	seed(t)
	tbl := logTable(t, map[string]any{"refused": true, "limit": 1})
	if len(tbl.Rows) != 1 || tbl.Total != 2 {
		t.Fatalf("%d rows of %d, want 1 of the 2 refusals", len(tbl.Rows), tbl.Total)
	}
	if !strings.Contains(tbl.Warnings[0].Message, "1 older matching call is not shown") {
		t.Errorf("message = %q", tbl.Warnings[0].Message)
	}
}

func TestAFilterOverAFullWindowSaysItOnlySearchedThat(t *testing.T) {
	isolate(t)
	appendRow(t, agentlog.Entry{Cap: "kv.get", Outcome: agentlog.Refused, Auth: agentlog.Blocked, Code: "core.grant.required"})
	for range maxRows {
		appendRow(t, agentlog.Entry{Cap: "sys.cpu", Outcome: agentlog.Ran, Auth: agentlog.Open})
	}
	tbl := logTable(t, map[string]any{"refused": true})
	if len(tbl.Rows) != 0 {
		t.Fatalf("the refusal is %d calls back and was found: %v", maxRows, tbl.Rows)
	}
	if len(tbl.Warnings) != 1 || tbl.Warnings[0].Code != "agent.log.window" || !tbl.Warnings[0].Advisory {
		t.Fatalf("a refusal that is not here because it was not looked for said nothing: %+v", tbl.Warnings)
	}
	if got := logTable(t, map[string]any{"refused": true, "since": "1d"}); len(got.Rows) != 1 {
		t.Errorf("--since reads as far back as it names, and found %d rows", len(got.Rows))
	}
}

func TestAfterNamesWhereThePageGoesOn(t *testing.T) {
	isolate(t)
	for range 12 {
		appendRow(t, agentlog.Entry{Cap: "sys.cpu", Outcome: agentlog.Ran, Auth: agentlog.Open})
	}
	tbl := logTable(t, map[string]any{"after": 2, "limit": 5})
	if tbl.Page == nil || tbl.Page.Next != "7" {
		t.Fatalf("page = %+v, want the cursor to go on from 7", tbl.Page)
	}
	if last := logTable(t, map[string]any{"after": 7, "limit": 5}); last.Page != nil {
		t.Errorf("the last page offers %+v", last.Page)
	}
}

func TestTheLogFiltersByAgent(t *testing.T) {
	seed(t)
	appendRow(t, agentlog.Entry{Cap: "sys.cpu", Agent: "cursor", Outcome: agentlog.Ran, Auth: agentlog.Open})
	if rows := logRows(t, map[string]any{"agent": "cursor"}); len(rows) != 1 || rows[0][2] != "sys.cpu" {
		t.Errorf("--agent cursor kept %v", rows)
	}
	tbl := logTable(t, map[string]any{"agent": "claudee"})
	if len(tbl.Rows) != 0 || !strings.Contains(tbl.Empty, `no call from an agent named "claudee"`) ||
		!strings.Contains(tbl.Empty, "claude, cursor") {
		t.Errorf("a mistyped agent is told apart from one that did nothing: %q", tbl.Empty)
	}
}

func TestSinceReadsTodayYesterdayAndDays(t *testing.T) {
	isolate(t)
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	for _, e := range []agentlog.Entry{
		{Cap: "a.three-days", At: midnight.AddDate(0, 0, -3).Add(time.Hour)},
		{Cap: "a.yesterday", At: midnight.AddDate(0, 0, -1).Add(time.Hour)},
		{Cap: "a.today", At: midnight.Add(time.Second)},
	} {
		e.Outcome, e.Auth = agentlog.Ran, agentlog.Open
		appendRow(t, e)
	}
	caps := func(since string) []string {
		var out []string
		for _, r := range logRows(t, map[string]any{"since": since}) {
			out = append(out, r[2])
		}
		return out
	}
	if got := caps("today"); !slices.Equal(got, []string{"a.today"}) {
		t.Errorf("today = %v", got)
	}
	if got := caps("Yesterday"); !slices.Equal(got, []string{"a.yesterday", "a.today"}) {
		t.Errorf("yesterday = %v", got)
	}
	if got := caps("4d"); len(got) != 3 {
		t.Errorf("4d = %v, want every call", got)
	}
	if got := caps("2d"); !slices.Equal(got, []string{"a.yesterday", "a.today"}) {
		t.Errorf("2d = %v", got)
	}
}

// Whether a call needs the operator's grant or was a typo the agent will fix is
// the first thing an operator scanning the hour wants to know, and "refused 6"
// said neither.
func TestTheOverviewTellsWhatNeedsYourGrantFromWhatWasMalformed(t *testing.T) {
	seed(t)
	appendRow(t, agentlog.Entry{Cap: "net.dns", Agent: "claude", Outcome: agentlog.Refused, Auth: agentlog.Blocked, Code: "core.mcp.badargs"})
	appendRow(t, agentlog.Entry{Cap: "no_such_tool", Agent: "claude", Outcome: agentlog.Refused, Auth: agentlog.Blocked, Code: "core.mcp.unknown"})
	appendRow(t, agentlog.Entry{Cap: "sys.cpu", Agent: "claude", Outcome: agentlog.Refused, Auth: agentlog.Blocked, Code: "core.lock.frozen"})
	v, err := run(t, "agent.overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := overviewPair(t, v, "needs your grant"), "1 — kv.get db-password; `rta agent log --refused` lists them"; got != want {
		t.Errorf("needs your grant = %q, want %q", got, want)
	}
	if got, want := overviewPair(t, v, "malformed or unknown calls"), "3 — note.add, net.dns, no_such_tool"; got != want {
		t.Errorf("malformed or unknown calls = %q, want %q", got, want)
	}
	if got, want := overviewPair(t, v, "refused otherwise"), "1 — sys.cpu"; got != want {
		t.Errorf("refused otherwise = %q, want %q", got, want)
	}
	for _, p := range v.(view.KeyValue).Pairs {
		if p.Key == "refused" {
			t.Errorf("the lumped counter is back: %+v", p)
		}
	}
}

func TestTheOverviewFiltersByAgent(t *testing.T) {
	seed(t)
	appendRow(t, agentlog.Entry{Cap: "sys.cpu", Agent: "cursor", Outcome: agentlog.Ran, Auth: agentlog.Open})
	v, err := run(t, "agent.overview", map[string]any{"agent": "cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if got := overviewPair(t, v, "calls in the last hour"); got != "1" {
		t.Errorf("cursor made %s calls, want 1", got)
	}
	if got := overviewPair(t, v, "needs your grant"); got != "0" {
		t.Errorf("claude's refusal is in cursor's overview: %q", got)
	}
	if got := overviewPair(t, v, "last call"); !strings.HasPrefix(got, "sys.cpu ran,") {
		t.Errorf("cursor's last call = %q, want its own sys.cpu rather than the newest call of anybody", got)
	}
	appendRow(t, agentlog.Entry{Cap: "kv.get", Agent: "claude", Outcome: agentlog.Refused, Auth: agentlog.Blocked})
	v, err = run(t, "agent.overview", map[string]any{"agent": "cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if got := overviewPair(t, v, "last call"); !strings.HasPrefix(got, "sys.cpu ran,") {
		t.Errorf("claude's newer call is cursor's last call: %q", got)
	}
	v, err = run(t, "agent.overview", map[string]any{"agent": "nobody"})
	if err != nil {
		t.Fatal(err)
	}
	if got := overviewPair(t, v, "last call"); got != "nothing recorded yet from nobody" {
		t.Errorf("an agent with no calls: last call = %q", got)
	}
}

func TestSeveralServersUnderOneNameAreOneEntry(t *testing.T) {
	isolate(t)
	for _, id := range []string{"aaaa1111", "bbbb2222"} {
		if err := session.Start(session.Record{ID: id, Agent: "claude", Client: "claude-code",
			Since: time.Now().Add(-time.Minute), PID: os.Getpid()}); err != nil {
			t.Fatal(err)
		}
	}
	appendRow(t, agentlog.Entry{Cap: "sys.cpu", Agent: "claude", Session: "aaaa1111", Outcome: agentlog.Ran, Auth: agentlog.Open})
	got, n, err := Connected()
	if err != nil || n != 2 || got != "2 — claude ×2 (1 call)" {
		t.Errorf("Connected() = %q, %d, %v", got, n, err)
	}
}

// A parked call that names no record is not a call on the record "".
func TestAParkedCallThatNamesNoRecordShowsADash(t *testing.T) {
	isolate(t)
	park(t, "note.add")
	v, err := run(t, "agent.pending", nil)
	if err != nil {
		t.Fatal(err)
	}
	row := v.(view.Table).Rows[0]
	if row[2] != "—" {
		t.Errorf("the record cell is %q, want a dash", row[2])
	}
}
