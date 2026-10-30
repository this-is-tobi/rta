package sys

import (
	"bytes"
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

const (
	mib = 1 << 20
	gib = 1 << 30
)

// calm is a host with nothing wrong with it, every reading present: the fixture
// each test bends one reading of, so a test says what it changed and not what it
// had to set up.
func calm() overviewFacts {
	return overviewFacts{
		cores: 14,
		host:  hostFact{name: "build-1", uptime: 3*24*time.Hour + 4*time.Hour + 5*time.Minute},
		cpu:   cpuFact{percent: 20.1},
		mem:   memFact{used: 24 * gib, total: 36 * gib, percent: 66.7, pressure: pressureNormal},
		swap:  swapFact{used: 2 * gib, total: 4 * gib, percent: 50},
		load:  loadFact{load1: 4.41, load5: 4.48, load15: 4.73},
		disk:  diskFact{mount: "/", used: 200 * gib, total: 500 * gib, percent: 40},
	}
}

func rowsOf(t *testing.T, f overviewFacts) ([]string, map[string]string) {
	t.Helper()
	kv, ok := f.page()
	if !ok {
		t.Fatalf("a page was expected, got none")
	}
	keys := make([]string, 0, len(kv.Pairs))
	values := map[string]string{}
	for _, p := range kv.Pairs {
		keys = append(keys, p.Key)
		values[p.Key] = p.Value
	}
	return keys, values
}

// The tile is the first eight lines of this answer and an agent reads all of it,
// so the order is the argument: the verdict, then what fills up soonest, then
// memory, load, the plain facts, and the host's name — which says what machine
// this is and nothing about how it is — last.
func TestTheCompactOverviewLeadsWithTheVerdictAndEndsWithTheHost(t *testing.T) {
	keys, v := rowsOf(t, calm())
	want := []string{"status", "disk", "mem", "load", "cpu", "swap", "host"}
	if !slices.Equal(keys, want) {
		t.Fatalf("rows = %v, want %v", keys, want)
	}
	for key, value := range map[string]string{
		"status": "ok",
		"disk":   "ok — 40% of / (200.0 of 500.0 GiB)",
		"mem":    "ok — 24.0 of 36.0 GiB",
		"load":   "ok — 32%/core (5 min)",
		"cpu":    "20.1% of 14 cores",
		"swap":   "2.0 of 4.0 GiB (50%)",
		"host":   "build-1 · up 3d 4h",
	} {
		if v[key] != value {
			t.Errorf("%s = %q, want %q", key, v[key], value)
		}
	}
}

// Eight rows, each one line at 80 columns. A wrapped row is two lines of a tile
// that is cut at eight, so this renders instead of counting pairs.
func TestTheCompactOverviewIsOneLinePerRowAtEightyColumns(t *testing.T) {
	worst := calm()
	worst.disk = diskFact{mount: "/System/Volumes/Data", used: 470 * gib, total: 500 * gib, percent: 94}
	worst.mem = memFact{used: 35 * gib, total: 36 * gib, percent: 97, pressure: pressureCritical}
	worst.load = loadFact{load1: 21.7, load5: 18, load15: 12}
	worst.swap = swapFact{used: 3 * gib, total: 4 * gib, percent: 75}
	for name, f := range map[string]overviewFacts{"calm": calm(), "everything wrong": worst} {
		kv, ok := f.page()
		if !ok {
			t.Fatalf("%s: no page", name)
		}
		var out bytes.Buffer
		if err := cli.Render(&out, kv, cli.Options{Format: cli.Pretty, NoColor: true, Width: 80}); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
		if len(lines) != len(kv.Pairs) || len(lines) > 8 {
			t.Errorf("%s: %d pairs drew %d lines, want one each and at most 8:\n%s", name, len(kv.Pairs), len(lines), out.String())
		}
	}
}

// Every row that is a verdict leads with a word the theme grades, because that
// is the only thing that makes it a colour: an unrecognised word draws plain,
// and the worst states used to be the plainest.
func TestEveryVerdictLeadsWithAWordTheThemeGrades(t *testing.T) {
	bad := calm()
	bad.disk = diskFact{mount: "/", used: 470 * gib, total: 500 * gib, percent: 94}
	bad.mem.pressure = pressureCritical
	bad.load = loadFact{load1: 21.7, load5: 18, load15: 12}
	bad.swap = swapFact{used: 3.5 * gib, total: 4 * gib, percent: 87.5}

	warn := calm()
	warn.disk = diskFact{mount: "/", used: 440 * gib, total: 500 * gib, percent: 88}
	warn.mem.pressure = pressureWarn
	warn.load = loadFact{load1: 9, load5: 10, load15: 8}
	warn.swap = swapFact{used: 3.5 * gib, total: 4 * gib, percent: 87.5}

	for _, tc := range []struct {
		name  string
		facts overviewFacts
		want  map[string]theme.StatusKind
	}{
		{"calm", calm(), map[string]theme.StatusKind{
			"status": theme.StatusGood, "disk": theme.StatusGood, "mem": theme.StatusGood, "load": theme.StatusGood,
			"cpu": theme.StatusNeutral, "swap": theme.StatusNeutral, "host": theme.StatusNeutral,
		}},
		{"warn", warn, map[string]theme.StatusKind{
			"status": theme.StatusWarn, "disk": theme.StatusWarn, "mem": theme.StatusWarn, "load": theme.StatusWarn,
			"cpu": theme.StatusNeutral, "swap": theme.StatusWarn, "host": theme.StatusNeutral,
		}},
		{"bad", bad, map[string]theme.StatusKind{
			"status": theme.StatusBad, "disk": theme.StatusBad, "mem": theme.StatusBad, "load": theme.StatusBad,
			"cpu": theme.StatusNeutral, "swap": theme.StatusWarn, "host": theme.StatusNeutral,
		}},
	} {
		_, v := rowsOf(t, tc.facts)
		for key, want := range tc.want {
			if got := theme.ClassifyStatus(v[key]); got != want {
				t.Errorf("%s: %s = %q draws as %v, want %v", tc.name, key, v[key], got, want)
			}
		}
	}
}

// The first row names the worst fact in a few words, and the grade it leads
// with is that fact's: a screen that says "ok" over a disk at 94% is the
// failure this page exists to stop.
func TestTheStatusRowNamesTheWorstFact(t *testing.T) {
	with := func(change func(*overviewFacts)) overviewFacts {
		f := calm()
		change(&f)
		return f
	}
	for _, tc := range []struct {
		name  string
		facts overviewFacts
		want  string
	}{
		{"a disk past the warning line", with(func(f *overviewFacts) {
			f.disk = diskFact{mount: "/", used: 440 * gib, total: 500 * gib, percent: 88}
		}), "warn — 88% of /"},
		{"a disk past the failing line", with(func(f *overviewFacts) {
			f.disk = diskFact{mount: "/data", used: 470 * gib, total: 500 * gib, percent: 94}
		}), "fail — 94% of /data"},
		{"memory pressure warning", with(func(f *overviewFacts) { f.mem.pressure = pressureWarn }),
			"warn — memory pressure"},
		{"memory pressure critical", with(func(f *overviewFacts) { f.mem.pressure = pressureCritical }),
			"fail — memory pressure critical"},
		{"load past a core each", with(func(f *overviewFacts) {
			f.load = loadFact{load1: 9, load5: 15.4, load15: 5}
		}), "fail — load 110%/core (5 min)"},
		{"load past seventy percent", with(func(f *overviewFacts) {
			f.load = loadFact{load1: 9, load5: 10.5, load15: 5}
		}), "warn — load 75%/core (5 min)"},
		{"a minute past every core over five calm ones", with(func(f *overviewFacts) {
			f.load = loadFact{load1: 30, load5: 4, load15: 3}
		}), "ok"},
		{"a warning and a failure: the failure is named", with(func(f *overviewFacts) {
			f.disk = diskFact{mount: "/", used: 440 * gib, total: 500 * gib, percent: 88}
			f.mem.pressure = pressureCritical
		}), "fail — memory pressure critical"},
		{"two warnings: the first row's", with(func(f *overviewFacts) {
			f.disk = diskFact{mount: "/", used: 440 * gib, total: 500 * gib, percent: 88}
			f.mem.pressure = pressureWarn
		}), "warn — 88% of /"},
	} {
		_, v := rowsOf(t, tc.facts)
		if v["status"] != tc.want {
			t.Errorf("%s: status = %q, want %q", tc.name, v["status"], tc.want)
		}
	}
}

// The thresholds are the ones the neighbouring code states: a filesystem is
// graded by the whole percent that is printed (diskUsage says why), warn from
// view.UsageWarn and fail from view.UsageBad, and a load from the same
// per-core lines sys.load uses.
func TestTheRowsAreGradedFromTheFigureThatIsPrinted(t *testing.T) {
	for _, tc := range []struct {
		percent float64
		lead    string
	}{
		{79.4, "ok"}, {79.6, "warn"}, {89.4, "warn"}, {89.6, "fail"}, {100, "fail"},
	} {
		f := calm()
		f.disk = diskFact{mount: "/", used: 1, total: 2, percent: tc.percent}
		_, v := rowsOf(t, f)
		if !strings.HasPrefix(v["disk"], tc.lead+" — ") {
			t.Errorf("a disk at %.1f%% reads %q, want it to lead with %q", tc.percent, v["disk"], tc.lead)
		}
	}
	for _, tc := range []struct {
		perCore float64
		lead    string
	}{
		{0.694, "ok"}, {0.696, "warn"}, {0.994, "warn"}, {0.996, "overloaded"}, {1.5, "overloaded"},
	} {
		f := calm()
		f.load = loadFact{load5: tc.perCore * float64(f.cores)}
		_, v := rowsOf(t, f)
		if !strings.HasPrefix(v["load"], tc.lead+" — ") {
			t.Errorf("a load of %.3f a core reads %q, want it to lead with %q", tc.perCore, v["load"], tc.lead)
		}
	}
}

// Where the kernel cannot say how much pressure memory is under (a kernel built
// without PSI, a container that hides it), the row falls back to the old
// reading, says so, and grades nothing: a used percentage has no line that means
// trouble, and a green "ok" over it would be a guess.
func TestMemoryWithoutPressureFallsBackToUsedAndSaysSo(t *testing.T) {
	f := calm()
	f.mem.pressureErr = errors.New("no pressure here")
	f.swap = swapFact{used: 3900 * mib, total: 4 * gib, percent: 97.5}
	_, v := rowsOf(t, f)
	if want := "24.0 GiB / 36.0 GiB (66.7%) · pressure not available"; v["mem"] != want {
		t.Errorf("mem = %q, want %q", v["mem"], want)
	}
	if got := theme.ClassifyStatus(v["mem"]); got != theme.StatusNeutral {
		t.Errorf("a memory row with no pressure draws as %v, want neutral", got)
	}
	if v["status"] != "ok" {
		t.Errorf("status = %q: nothing was found wrong, and a swap that is nearly full on its own is a fact", v["status"])
	}
	if strings.HasPrefix(v["swap"], "warn") {
		t.Errorf("swap = %q: it leads a warning only beside a pressure that is not ok", v["swap"])
	}
}

// Swap in use is what every Mac has and what a threshold on it would paint red:
// 31 of 32 GiB read as a failure on a machine whose kernel said memory was
// normal. It is a fact, and a warning only when it is high and the kernel
// agrees that memory is short.
func TestSwapIsAWarningOnlyWhenItIsHighAndMemoryIsUnderPressure(t *testing.T) {
	high := swapFact{used: 3.5 * gib, total: 4 * gib, percent: 87.5}
	for _, tc := range []struct {
		name     string
		swap     swapFact
		pressure pressureLevel
		warn     bool
	}{
		{"high, pressure normal", high, pressureNormal, false},
		{"high, pressure warn", high, pressureWarn, true},
		{"high, pressure critical", high, pressureCritical, true},
		{"low, pressure critical", swapFact{used: gib, total: 4 * gib, percent: 25}, pressureCritical, false},
	} {
		f := calm()
		f.swap, f.mem.pressure = tc.swap, tc.pressure
		_, v := rowsOf(t, f)
		if got := strings.HasPrefix(v["swap"], "warn — "); got != tc.warn {
			t.Errorf("%s: swap = %q, warning = %v, want %v", tc.name, v["swap"], got, tc.warn)
		}
	}
}

func TestAHostWithNoSwapHasNoSwapRow(t *testing.T) {
	f := calm()
	f.swap = swapFact{}
	keys, _ := rowsOf(t, f)
	if slices.Contains(keys, "swap") {
		t.Errorf("rows %v include swap for a host that has none", keys)
	}
}

// A row the account cannot read says so, in the row and in the verdict, and
// leaves the others as they were. It used to be dropped, which read as health:
// a page with no disk row is the page of a host whose disk is fine.
func TestARowThatCannotBeReadSaysSoAndTheVerdictAgrees(t *testing.T) {
	for _, tc := range []struct {
		key    string
		break_ func(*overviewFacts)
	}{
		{"disk", func(f *overviewFacts) { f.disk = diskFact{err: errors.New("listing partitions: denied")} }},
		{"mem", func(f *overviewFacts) { f.mem = memFact{err: errors.New("reading memory: denied")} }},
		{"load", func(f *overviewFacts) { f.load = loadFact{err: errors.New("reading load: denied")} }},
		{"cpu", func(f *overviewFacts) { f.cpu = cpuFact{err: errors.New("the CPU counters did not advance")} }},
		{"swap", func(f *overviewFacts) { f.swap = swapFact{err: errors.New("reading swap: denied")} }},
		{"host", func(f *overviewFacts) { f.host = hostFact{err: errors.New("nothing readable")} }},
	} {
		f := calm()
		tc.break_(&f)
		keys, v := rowsOf(t, f)
		if len(keys) != 7 {
			t.Errorf("%s unreadable: %d rows %v, want all seven", tc.key, len(keys), keys)
		}
		if !strings.HasPrefix(v[tc.key], "could not be read — ") {
			t.Errorf("%s = %q, want it to say it could not be read and why", tc.key, v[tc.key])
		}
		if want := "warn — " + tc.key + " could not be read"; v["status"] != want {
			t.Errorf("%s unreadable: status = %q, want %q", tc.key, v["status"], want)
		}
		if got := theme.ClassifyStatus(v[tc.key]); got != theme.StatusWarn {
			t.Errorf("%s = %q draws as %v, want amber", tc.key, v[tc.key], got)
		}
	}
}

// The failure that is worse than an unreadable row is still the verdict.
func TestAnUnreadableRowDoesNotHideAFailure(t *testing.T) {
	f := calm()
	f.swap = swapFact{err: errors.New("denied")}
	f.disk = diskFact{mount: "/", used: 470 * gib, total: 500 * gib, percent: 94}
	_, v := rowsOf(t, f)
	if v["status"] != "fail — 94% of /" {
		t.Errorf("status = %q, want the failing disk named", v["status"])
	}
}

// Only a host with nothing readable at all has no page. Anything less is a page
// with the unreadable rows marked.
func TestAHostWithNothingReadableHasNoPage(t *testing.T) {
	denied := errors.New("denied")
	f := overviewFacts{
		host: hostFact{err: denied}, cpu: cpuFact{err: denied}, mem: memFact{err: denied},
		swap: swapFact{err: denied}, load: loadFact{err: denied}, disk: diskFact{err: denied},
	}
	if _, ok := f.page(); ok {
		t.Error("a page was made of nothing")
	}
}

// The numbers are a second way to read the page, for a program: the figures the
// sentences were made from, and never a figure the page does not show.
func TestTheOverviewCarriesItsFiguresAsNumbers(t *testing.T) {
	f := calm()
	f.mem.pressure = pressureWarn
	kv, _ := f.page()
	for name, want := range map[string]float64{
		"disk_percent":     40,
		"disk_used_bytes":  200 * gib,
		"disk_total_bytes": 500 * gib,
		"mem_pressure":     1,
		"mem_percent":      66.7,
		"mem_used_bytes":   24 * gib,
		"mem_total_bytes":  36 * gib,
		"load_per_core":    4.41 / 14,
		"load1":            4.41,
		"load5":            4.48,
		"load15":           4.73,
		"cpu_percent":      20.1,
		"cores":            14,
		"swap_percent":     50,
		"swap_used_bytes":  2 * gib,
		"swap_total_bytes": 4 * gib,
		"uptime_seconds":   (3*24*time.Hour + 4*time.Hour + 5*time.Minute).Seconds(),
	} {
		got, there := kv.Numbers[name]
		if !there || math.Abs(got-want) > 1e-9 {
			t.Errorf("numbers[%s] = %v (present: %v), want %v", name, got, there, want)
		}
	}

	f = calm()
	f.mem.pressureErr = errors.New("no pressure here")
	f.swap = swapFact{}
	f.disk = diskFact{err: errors.New("denied")}
	kv, _ = f.page()
	for _, name := range []string{"mem_pressure", "swap_percent", "disk_percent", "disk_used_bytes"} {
		if _, there := kv.Numbers[name]; there {
			t.Errorf("numbers[%s] is present for a reading the page does not show", name)
		}
	}
}

func TestMemoryPressureFromTheLinuxFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want pressureLevel
		bad  bool
	}{
		{"idle", "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n", pressureNormal, false},
		{"just under the warning line", "some avg10=9.99 avg60=0.00 avg300=0.00 total=1\n", pressureNormal, false},
		{"on the warning line", "some avg10=10.00 avg60=0.00 avg300=0.00 total=1\n", pressureWarn, false},
		{"just under the critical line", "some avg10=39.99 avg60=0.00 avg300=0.00 total=1\n", pressureWarn, false},
		{"on the critical line", "some avg10=40.00 avg60=1.00 avg300=1.00 total=1\n", pressureCritical, false},
		{"full is not read", "some avg10=0.50 avg60=0.00 avg300=0.00 total=1\nfull avg10=60.00 avg60=0.00 avg300=0.00 total=1\n", pressureNormal, false},
		{"empty", "", 0, true},
		{"no some line", "full avg10=1.00 avg60=0.00 avg300=0.00 total=1\n", 0, true},
		{"no avg10", "some avg60=1.00 avg300=0.00 total=1\n", 0, true},
		{"not a number", "some avg10=lots avg60=0.00 avg300=0.00 total=1\n", 0, true},
		{"not a number we can compare", "some avg10=NaN avg60=0.00 avg300=0.00 total=1\n", 0, true},
	} {
		got, err := parsePSI(tc.text)
		if (err != nil) != tc.bad {
			t.Errorf("%s: error = %v, want error: %v", tc.name, err, tc.bad)
			continue
		}
		if err == nil && got != tc.want {
			t.Errorf("%s: level = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMemoryPressureFromTheMacKernelLevel(t *testing.T) {
	for level, want := range map[uint32]pressureLevel{1: pressureNormal, 2: pressureWarn, 4: pressureCritical} {
		got, err := pressureOfKernelLevel(level)
		if err != nil || got != want {
			t.Errorf("kernel level %d = %v, %v; want %v", level, got, err, want)
		}
	}
	for _, level := range []uint32{0, 3, 5, 8} {
		if got, err := pressureOfKernelLevel(level); err == nil {
			t.Errorf("kernel level %d was read as %v; it is not one the kernel documents", level, got)
		}
	}
}

// The live read: whatever this host is, the overview comes back with the verdict
// first and a reason for every row it could not fill, never a missing one.
func TestTheLiveOverviewHasTheVerdictFirstAndAllItsRows(t *testing.T) {
	v, err := runOverview(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	kv := v.(view.KeyValue)
	if len(kv.Pairs) < 6 || kv.Pairs[0].Key != "status" {
		t.Fatalf("pairs = %v, want the status row first and at least six rows", kv.Pairs)
	}
	if len(kv.Pairs) > 8 {
		t.Errorf("%d rows, want at most eight", len(kv.Pairs))
	}
	if _, there := kv.Numbers["load_per_core"]; !there {
		t.Errorf("no load_per_core in %v", kv.Numbers)
	}
}
