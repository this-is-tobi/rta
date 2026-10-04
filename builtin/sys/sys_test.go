package sys

import (
	"context"
	"errors"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/process"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatalf("sys plugin invalid: %v", err)
	}
}

func TestAllCapabilitiesAreReadIdempotent(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.Safety != plugin.Read || !c.Idempotent {
			t.Errorf("%s: sys capabilities must be read + idempotent", c.ID)
		}
	}
}

// TestCapabilitiesReturnWellFormedViews runs each capability against the real
// host and asserts view shape, not values (values are machine-dependent).
func TestCapabilitiesReturnWellFormedViews(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, c := range Plugin().Capabilities {
		t.Run(c.ID, func(t *testing.T) {
			req := plugin.NewRequest(map[string]any{"limit": 5}, false, false)
			v, err := c.Run(ctx, req)
			if err != nil {
				// Sensors are platform-dependent: a coded, hinted error is
				// the contract-correct outcome where none are readable.
				if c.ID == "sys.temp" {
					ve := view.AsError(err, "x")
					if ve.Hint == "" || ve.Code == "x" {
						t.Fatalf("sys.temp must fail coded+hinted, got %+v", ve)
					}
					t.Skipf("no sensors here: %v", ve.Message)
				}
				t.Fatalf("run: %v", err)
			}
			switch tv := v.(type) {
			case view.KeyValue:
				if len(tv.Pairs) == 0 {
					t.Error("empty KeyValue")
				}
				for _, p := range tv.Pairs {
					if p.Key == "" || p.Value == "" {
						t.Errorf("empty pair: %+v", p)
					}
				}
			case view.Table:
				if len(tv.Columns) == 0 {
					t.Error("table without columns")
				}
				for _, row := range tv.Rows {
					if len(row) != len(tv.Columns) {
						t.Errorf("row width %d != %d columns", len(row), len(tv.Columns))
					}
				}
			default:
				t.Errorf("unexpected view type %q", view.TypeOf(v))
			}
		})
	}
}

func TestCPUCoresChart(t *testing.T) {
	v, err := runCPU(context.Background(), plugin.NewRequest(map[string]any{"cores": true}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	chart, ok := v.(view.Chart)
	if !ok {
		t.Fatalf("want Chart, got %s", view.TypeOf(v))
	}
	if chart.Kind != view.ChartBar || len(chart.Series) == 0 {
		t.Errorf("chart = %+v", chart)
	}
	// The 0-100 scale is a property of the chart, not of each core: the cores
	// share one axis, so a per-series scale could only ever be a promise the
	// renderer had to break.
	if chart.Max != 100 {
		t.Errorf("per-core usage must be drawn on a fixed 0-100 scale, got Max=%v", chart.Max)
	}
	for _, s := range chart.Series {
		if len(s.Points) != 1 {
			t.Errorf("a bar series is one value: %+v", s)
		}
	}
}

func TestPSRespectsLimit(t *testing.T) {
	ctx := context.Background()
	v, err := runPS(ctx, plugin.NewRequest(map[string]any{"limit": 3}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	if len(tbl.Rows) > 3 {
		t.Errorf("limit ignored: %d rows", len(tbl.Rows))
	}
	if tbl.Total < len(tbl.Rows) {
		t.Errorf("Total %d < rows %d", tbl.Total, len(tbl.Rows))
	}
}

// sys.ps ranked by gopsutil's CPUPercent, the CPU time spent since a process
// started over the seconds since then: a process idle for a minute and
// spinning a core since read 14% while ps said 100, below processes busy
// once and idle ever since. The use is what each process spent over the
// window, so a large total that did not move reads 0 and a small one that
// did reads what it spent.
func TestPSMeasuresCPUOverTheWindowNotSinceTheProcessStarted(t *testing.T) {
	const window = 100 * time.Millisecond
	busyOnce, busyNow, unreadable, exited := &process.Process{Pid: 1}, &process.Process{Pid: 2},
		&process.Process{Pid: 3}, &process.Process{Pid: 4}
	reads := map[int32]int{}
	// busyNow has had a core to itself since the test began, so what it has
	// spent is the time since then: what it spent between its two readings
	// is the time between them, however long a loaded machine made the
	// window.
	started := time.Now()
	spent := func(_ context.Context, p *process.Process) (float64, error) {
		reads[p.Pid]++
		switch {
		case p == busyOnce && reads[p.Pid] == 2:
			// The second pass reaching busyNow a window late, as one slowed
			// by a loaded machine does.
			time.Sleep(window)
			return 5000, nil
		case p == busyOnce:
			return 5000, nil
		case p == busyNow:
			return 1 + time.Since(started).Seconds(), nil
		case p == exited && reads[p.Pid] == 1:
			return 30, nil
		}
		return 0, errors.New("no such process")
	}
	use, gone, err := recentCPU(context.Background(),
		[]*process.Process{busyOnce, busyNow, unreadable, exited}, spent, window)
	if err != nil {
		t.Fatal(err)
	}
	if got := use[busyOnce.Pid]; got != 0 {
		t.Errorf("a process that spent nothing in the window reads %.1f%%, want 0", got)
	}
	// A whole core over the time between its own two readings, and not over
	// the window alone: divided by the window, the late second pass read as
	// two cores. The margin is for a pause between a reading and its
	// timestamp, not for the window, whose length this does not depend on.
	if got := use[busyNow.Pid]; got < 80 || got > 120 {
		t.Errorf("a process on a core the whole time reads %.1f%%, want close to 100", got)
	}
	if _, ok := use[unreadable.Pid]; ok || gone[unreadable.Pid] {
		t.Errorf("a process unreadable from the start has a use or is gone: %v, %v", use, gone)
	}
	if !gone[exited.Pid] {
		t.Errorf("a process that stopped answering inside the window is not reported gone: %v", gone)
	}
}

// On macOS another user's process read as 0.0% and 0 B — gopsutil discards
// the kernel's refusal and returns zeros — so a root process spinning a core
// ranked last, under a warning that implied the rest were accurate. Nothing
// that runs has nothing resident, so there a zero is the refusal; on Linux a
// kernel thread's zero is real.
func TestAZeroFromARefusedReadIsNotShownAsIdle(t *testing.T) {
	for _, tc := range []struct {
		goos string
		rss  uint64
		want bool
	}{
		{"darwin", 0, true},
		{"darwin", 4096, false},
		{"linux", 0, false},
	} {
		if got := taskUnreadable(tc.goos, tc.rss); got != tc.want {
			t.Errorf("taskUnreadable(%s, %d) = %v, want %v", tc.goos, tc.rss, got, tc.want)
		}
	}
	if runtime.GOOS != "darwin" {
		return
	}
	v, err := runPS(context.Background(), plugin.NewRequest(map[string]any{"limit": 1000}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range v.(view.Table).Rows {
		if row[4] == "0 B" {
			t.Errorf("a process read as nothing resident is listed as idle: %v", row)
		}
	}
}

func TestLoadIsNormalizedPerCore(t *testing.T) {
	v, err := runLoad(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range v.(view.KeyValue).Pairs {
		if !strings.Contains(p.Value, "/core") {
			t.Errorf("%s not normalized: %q", p.Key, p.Value)
		}
	}
}

func TestDiskFiltersNoiseAndHasStatus(t *testing.T) {
	v, err := runDisk(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	for _, row := range tbl.Rows {
		if pseudoFS[row[1]] || systemVolume(row[0]) {
			t.Errorf("noise row leaked through default filter: %v", row)
		}
	}
	if got := tbl.Columns[len(tbl.Columns)-1].Kind; got != view.KindStatus {
		t.Errorf("last column kind = %q, want status", got)
	}
	for _, row := range tbl.Rows {
		status := row[len(row)-1]
		if status != "ok" && !strings.HasPrefix(status, "WARN") && !strings.HasPrefix(status, "ERROR") {
			t.Errorf("unexpected status %q", status)
		}
	}
}

func TestUsageStatus(t *testing.T) {
	tests := []struct {
		pct  float64
		want string
	}{
		{10, "ok"}, {79.9, "ok"}, {80, "WARN >80%"}, {90, "ERROR >90%"},
	}
	for _, tt := range tests {
		if got := usageStatus(tt.pct); got != tt.want {
			t.Errorf("usageStatus(%v) = %q, want %q", tt.pct, got, tt.want)
		}
	}
}

// `sys disk` says the same thing twice about one mount: a Use% the renderer
// grades by colour, and a Status column carrying the band as a word — the
// half that survives a pipe, a --no-color terminal and --output json.
//
// Twice is right; disagreeing is not. A row reading "ok" in green beside a
// figure the renderer painted amber is the failure x509check exists to have
// stopped happening about certificates, so this walks the boundaries and both
// sides of each rather than trusting that two lists of numbers stay equal.
func TestTheStatusWordAndTheColourAgreeAboutTheSameDisk(t *testing.T) {
	// The fractions matter more than the round numbers: the cell is printed to
	// whole percent and the word used to be graded from the reading behind it,
	// so 79.6 is exactly where the two came apart.
	for _, pct := range []float64{0, 50, 79.4, 79.6, view.UsageWarn, 84.9, 89.4,
		89.6, view.UsageBad, 99.9, 100} {
		cell, status := diskUsage(pct)
		word := theme.ClassifyStatus(status)
		colour := theme.ClassifyUsage(cell)
		if word != colour {
			t.Errorf("at %.1f%%: %q classifies %v and the cell %q classifies %v",
				pct, status, word, cell, colour)
		}
	}
}

// The Use% column declares the kind that makes the colour happen, and the
// Status column beside it the one that makes the word happen. Swapping either
// silently removes half the signal.
func TestTheDiskColumnsDeclareTheKindsThatGradeThem(t *testing.T) {
	v, err := runDisk(context.Background(), plugin.NewRequest(map[string]any{}, false, false))
	if err != nil {
		t.Skipf("no readable filesystem here: %v", err)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("disk view = %T", v)
	}
	kinds := map[string]view.ColumnKind{}
	for _, c := range tbl.Columns {
		kinds[c.Name] = c.Kind
	}
	if kinds["Use%"] != view.KindUsage {
		t.Errorf("Use%% is %q, want usage — nothing else colours it", kinds["Use%"])
	}
	if kinds["Status"] != view.KindStatus {
		t.Errorf("Status is %q, want status", kinds["Status"])
	}
}

func TestPSSortValidation(t *testing.T) {
	_, err := runPS(context.Background(), plugin.NewRequest(map[string]any{"limit": 3, "sort": "disk"}, false, false))
	ve := view.AsError(err, "x")
	if ve.Code != "sys.ps.badsort" || ve.Hint == "" {
		t.Errorf("want sys.ps.badsort with hint, got %+v", ve)
	}
	// Empty sort defaults to cpu (handlers may be called without CLI defaults).
	if _, err := runPS(context.Background(), plugin.NewRequest(map[string]any{"limit": 1}, false, false)); err != nil {
		t.Errorf("empty sort should default to cpu: %v", err)
	}
}

func TestPSHasRSSColumn(t *testing.T) {
	v, err := runPS(context.Background(), plugin.NewRequest(map[string]any{"limit": 3, "sort": "mem"}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	last := tbl.Columns[len(tbl.Columns)-1]
	if last.Name != "RSS" || last.Kind != view.KindBytes {
		t.Errorf("RSS column wrong: %+v", last)
	}
}

func TestHumanDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Minute, "30m"},
		{90 * time.Minute, "1h 30m"},
		{25*time.Hour + 5*time.Minute, "1d 1h 5m"},
	}
	for _, tt := range tests {
		if got := humanDuration(tt.in); got != tt.want {
			t.Errorf("humanDuration(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestOverviewGroupsSubsystems: the grouped view carries one dense line per
// subsystem; the essentials must be present on any host running the tests.
func TestOverviewGroupsSubsystems(t *testing.T) {
	v, err := runOverview(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	kv := v.(view.KeyValue)
	keys := map[string]string{}
	for _, p := range kv.Pairs {
		if _, dup := keys[p.Key]; dup {
			t.Errorf("duplicate overview line %q", p.Key)
		}
		keys[p.Key] = p.Value
	}
	for _, want := range []string{"host", "cpu", "mem", "disk"} {
		if keys[want] == "" {
			t.Errorf("overview missing %q line (got %v)", want, keys)
		}
	}
	if !strings.Contains(keys["mem"], " / ") {
		t.Errorf("mem line not used/total: %q", keys["mem"])
	}
	if !strings.Contains(keys["cpu"], "cores") {
		t.Errorf("cpu line missing core count: %q", keys["cpu"])
	}
}

func TestLoadVerdict(t *testing.T) {
	tests := []struct {
		perCore float64
		want    string
	}{
		{0.2, "ok"}, {0.69, "ok"}, {0.7, "busy"}, {0.99, "busy"}, {1.0, "overloaded"},
	}
	for _, tt := range tests {
		if got := loadVerdict(tt.perCore); got != tt.want {
			t.Errorf("loadVerdict(%v) = %q, want %q", tt.perCore, got, tt.want)
		}
	}
}

// TestDetailedOverviewComposesCapabilities: the full-page report is built
// from other capabilities' views, so it carries several view types at once.
func TestDetailedOverviewComposesCapabilities(t *testing.T) {
	v, err := runOverview(context.Background(), plugin.NewRequest(map[string]any{"detail": true}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("want Sections, got %s", view.TypeOf(v))
	}
	titles := map[string]string{}
	for _, item := range s.Items {
		if item.View == nil {
			t.Errorf("section %q has no view", item.Title)
			continue
		}
		titles[item.Title] = view.TypeOf(item.View)
	}
	// Composition reuses the capabilities' own view types.
	if titles["host"] != "keyvalue" {
		t.Errorf("host section = %q, want keyvalue", titles["host"])
	}
	if titles["cpu"] != "chart" {
		t.Errorf("cpu section = %q, want chart (per-core bars)", titles["cpu"])
	}
	if titles["storage"] != "table" {
		t.Errorf("storage section = %q, want table", titles["storage"])
	}
	if titles["top processes"] != "table" {
		t.Errorf("top processes section = %q, want table", titles["top processes"])
	}
	// The compact view stays a flat KeyValue for the tile.
	compact, err := runOverview(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := compact.(view.KeyValue); !ok {
		t.Errorf("compact overview = %s, want keyvalue", view.TypeOf(compact))
	}
}

// **A shorter list is indistinguishable from the whole of a smaller thing.**
//
// This machine has processes this user may not read — 44 of 778 on the box
// this was written on — and every one of them was dropped from the table
// with the Total counted from what was left. Nothing said so, on the screen
// somebody opens to find what is running.
//
// Skipped where everything happens to be readable, which is the ordinary
// case in a root container: the fix is that an unreadable process is
// *reported*, not that one always exists.
func TestPSSaysHowManyProcessesItCouldNotRead(t *testing.T) {
	v, err := runPS(context.Background(), plugin.NewRequest(map[string]any{"limit": 5}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	table, ok := v.(view.Table)
	if !ok {
		t.Fatalf("ps returned %s, want a Table", view.TypeOf(v))
	}
	if len(table.Warnings) == 0 {
		t.Skip("every process on this machine is readable, so there is nothing to report")
	}
	w := table.Warnings[0]
	if w.Code != "sys.ps.partial" {
		t.Errorf("code = %q, want sys.ps.partial", w.Code)
	}
	if !strings.Contains(w.Message, "could not be read") {
		t.Errorf("message = %q, want it to say what happened", w.Message)
	}
	// The count has to be a count, not "some": it is the difference between
	// the table in front of the reader and the machine behind it.
	if !regexp.MustCompile(`\d+ process`).MatchString(w.Message) {
		t.Errorf("message = %q, want it to say how many", w.Message)
	}
}

// And a warning is never invented: a listing that read everything says
// nothing extra, or the caveat stops being worth reading.
func TestDiskWarnsOnlyAboutMountsItCouldNotStat(t *testing.T) {
	v, err := runDisk(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	table := v.(view.Table)
	for _, w := range table.Warnings {
		if w.Code != "sys.disk.partial" {
			t.Errorf("unexpected warning %+v", w)
		}
		// Every named mount must be absent from the rows, or the warning is
		// describing something it did in fact manage to read.
		for _, row := range table.Rows {
			if strings.Contains(w.Hint, row[0]) {
				t.Errorf("warning names %q, which is in the table: %s", row[0], w.Hint)
			}
		}
	}
}
