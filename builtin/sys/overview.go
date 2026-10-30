package sys

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

// grade is how a reading stands against the line this package draws for it.
type grade int

const (
	// ungraded is a fact with no line that means trouble: a cpu share, a swap
	// that nothing says is too full.
	ungraded grade = iota
	gradeOK
	gradeWarn
	gradeFail
)

// overviewFacts is what the compact overview says, read once and kept apart from
// the words that say it. The words are decided here from these values and
// nothing else, so a test can put a disk at 94% or a kernel that publishes no
// pressure on the table without having either.
type overviewFacts struct {
	cores int
	host  hostFact
	cpu   cpuFact
	mem   memFact
	swap  swapFact
	load  loadFact
	disk  diskFact
}

// Each reading carries its own error: a page whose disk could not be read still
// has a memory row, and says in the disk's own row why it has no figure.
type hostFact struct {
	name   string
	uptime time.Duration
	err    error
}

type cpuFact struct {
	percent float64
	err     error
}

// memFact holds the used share as well as the pressure: the share is the whole
// row on a kernel that does not publish pressure, and a number for a program on
// every kernel.
type memFact struct {
	used, total uint64
	percent     float64
	pressure    pressureLevel
	pressureErr error
	err         error
}

// swapFact with a zero total and no error is a host without swap, which has no
// row; it is not a failure to read.
type swapFact struct {
	used, total uint64
	percent     float64
	err         error
}

type loadFact struct {
	load1, load5, load15 float64
	err                  error
}

type diskFact struct {
	mount       string
	used, total uint64
	percent     float64
	err         error
}

// row is one line of the page and what it adds to the verdict.
type row struct {
	key, value string
	grade      grade
	// headline is the fact in a few words, for the status row when this is the
	// worst row: "88% of /", not the sentence the row itself is.
	headline string
}

// The per-core load lines, shared with sys.load so the two cannot disagree about
// the same host.
const (
	loadBusy       = 0.7
	loadOverloaded = 1.0
)

// readOverview reads everything the compact overview says. It reads less than
// the detailed report does and more cheaply: the host's name and uptime, not its
// kernel, its platform and its process count, so a refresh no longer runs
// sw_vers or counts every process on the machine.
func readOverview(ctx context.Context) overviewFacts {
	var f overviewFacts
	f.cores, _ = cpu.CountsWithContext(ctx, true)
	f.host = readHostFact(ctx)
	if s, err := sampleCPU(ctx); err != nil {
		f.cpu.err = err
	} else {
		f.cpu.percent = s.total
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err != nil {
		f.mem.err = err
	} else {
		f.mem = memFact{used: vm.Used, total: vm.Total, percent: vm.UsedPercent}
		f.mem.pressure, f.mem.pressureErr = readMemoryPressure(ctx)
	}
	if sw, err := mem.SwapMemoryWithContext(ctx); err != nil {
		f.swap.err = err
	} else {
		f.swap = swapFact{used: sw.Used, total: sw.Total, percent: sw.UsedPercent}
	}
	if avg, err := load.AvgWithContext(ctx); err != nil {
		f.load.err = err
	} else {
		f.load = loadFact{load1: avg.Load1, load5: avg.Load5, load15: avg.Load15}
	}
	f.disk = fullestDisk(ctx)
	return f
}

func readHostFact(ctx context.Context) hostFact {
	name, nameErr := os.Hostname()
	secs, upErr := host.UptimeWithContext(ctx)
	if nameErr != nil && upErr != nil {
		return hostFact{err: nameErr}
	}
	return hostFact{name: name, uptime: uptimeOf(secs)}
}

// fullestDisk is the most-used real filesystem — the one that pages you first.
// "Soonest full" would need history; the fullest by share is the answer a
// single reading can give.
func fullestDisk(ctx context.Context) diskFact {
	parts, err := realPartitions(ctx)
	if err != nil {
		return diskFact{err: err}
	}
	best := diskFact{percent: -1}
	for _, p := range parts {
		u, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		if u.UsedPercent > best.percent {
			best = diskFact{mount: p.Mountpoint, used: u.Used, total: u.Total, percent: u.UsedPercent}
		}
	}
	if best.percent < 0 {
		return diskFact{err: errors.New("no filesystem reported a size")}
	}
	return best
}

// page is the compact overview: the verdict, then what fills up or runs out
// first, then the plain facts, then the host's name — which says which machine
// this is and nothing about how it is — last. It is cut by a tile from the
// bottom, so the order is the argument.
//
// ok is false when nothing at all could be read, which is a host with nothing
// to say; anything less is a page whose unreadable rows say so.
func (f overviewFacts) page() (view.KeyValue, bool) {
	if f.host.err != nil && f.cpu.err != nil && f.mem.err != nil && f.load.err != nil && f.disk.err != nil {
		return view.KeyValue{}, false
	}
	rows := []row{f.diskRow(), f.memRow(), f.loadRow(), f.cpuRow(), f.swapRow(), f.hostRow()}
	kv := view.KeyValue{Numbers: f.numbers()}
	kv.Pairs = append(kv.Pairs, statusRow(rows))
	for _, r := range rows {
		if r.key != "" {
			kv.Pairs = append(kv.Pairs, view.Pair{Key: r.key, Value: r.value})
		}
	}
	return kv, true
}

// statusRow is the page's first line: "ok", or the worst row's grade and what
// it found. A failure is named before a warning, and between two of a grade the
// one that comes first on the page is, because the page is already ordered by
// what pages you first.
func statusRow(rows []row) view.Pair {
	worst := row{}
	for _, r := range rows {
		if r.key != "" && r.grade > worst.grade {
			worst = r
		}
	}
	switch worst.grade {
	case gradeFail:
		return view.Pair{Key: "status", Value: "fail — " + worst.headline}
	case gradeWarn:
		return view.Pair{Key: "status", Value: "warn — " + worst.headline}
	}
	return view.Pair{Key: "status", Value: "ok"}
}

// led puts the word a row is graded by in front of its fact.
func led(word, fact string) string { return word + " — " + fact }

// unreadable is a row whose reading failed: said in the row, in the vocabulary
// the theme draws amber, and carried to the verdict. A missing row reads as a
// healthy one, which is what this replaced.
func unreadable(key string, err error) row {
	return row{
		key:      key,
		value:    led("could not be read", err.Error()),
		grade:    gradeWarn,
		headline: key + " could not be read",
	}
}

// sizes is two byte counts of one thing: "815.4 of 926.4 GiB", the unit said
// once when both are in it.
func sizes(used, total uint64) string {
	u, t := format.Bytes(used), format.Bytes(total)
	if i := strings.LastIndexByte(u, ' '); i > 0 && strings.HasSuffix(t, u[i:]) {
		u = u[:i]
	}
	return u + " of " + t
}

func (f overviewFacts) diskRow() row {
	d := f.disk
	if d.err != nil {
		return unreadable("disk", d.err)
	}
	fact := fmt.Sprintf("%.0f%% of %s", d.percent, d.mount)
	r := row{key: "disk", headline: fact}
	detail := fact + " (" + sizes(d.used, d.total) + ")"
	// Graded on the whole percent that is printed: diskUsage says why.
	switch shown := math.Round(d.percent); {
	case shown >= view.UsageBad:
		r.value, r.grade = led("fail", detail), gradeFail
	case shown >= view.UsageWarn:
		r.value, r.grade = led("warn", detail), gradeWarn
	default:
		r.value, r.grade = led("ok", detail), gradeOK
	}
	return r
}

func (f overviewFacts) memRow() row {
	m := f.mem
	if m.err != nil {
		return unreadable("mem", m.err)
	}
	if m.pressureErr != nil {
		return row{key: "mem", value: fmt.Sprintf("%s / %s (%.1f%%) · pressure not available",
			format.Bytes(m.used), format.Bytes(m.total), m.percent)}
	}
	held := sizes(m.used, m.total)
	switch m.pressure {
	case pressureCritical:
		return row{key: "mem", value: led("critical", "pressure, "+held), grade: gradeFail, headline: "memory pressure critical"}
	case pressureWarn:
		return row{key: "mem", value: led("warn", "pressure, "+held), grade: gradeWarn, headline: "memory pressure"}
	}
	return row{key: "mem", value: led("ok", held), grade: gradeOK}
}

func (f overviewFacts) loadRow() row {
	l := f.load
	if l.err != nil {
		return unreadable("load", l.err)
	}
	if f.cores <= 0 {
		return row{key: "load", value: fmt.Sprintf("%.2f · %.2f · %.2f", l.load1, l.load5, l.load15)}
	}
	// The five-minute average, not the one-minute one. The minute's is the
	// noisiest reading a host has: a test run or an IDE indexing puts it past
	// the core count for a minute at a time, and the first tile on the
	// screen went red and back again through ordinary work, which teaches a
	// person to read past red. Five minutes is a machine that stays busy, and
	// it is the row of sys.load that grades the same way.
	shown := math.Round(l.load5 / float64(f.cores) * 100)
	fact := fmt.Sprintf("%.0f%%/core (5 min)", shown)
	r := row{key: "load", headline: "load " + fact}
	switch {
	case shown >= loadOverloaded*100:
		r.value, r.grade = led("overloaded", fact), gradeFail
	case shown >= loadBusy*100:
		r.value, r.grade = led("warn", fact), gradeWarn
	default:
		r.value, r.grade = led("ok", fact), gradeOK
	}
	return r
}

func (f overviewFacts) cpuRow() row {
	if f.cpu.err != nil {
		return unreadable("cpu", f.cpu.err)
	}
	value := fmt.Sprintf("%.1f%%", f.cpu.percent)
	if f.cores > 0 {
		value += fmt.Sprintf(" of %d cores", f.cores)
	}
	return row{key: "cpu", value: value}
}

// swapRow is a plain fact unless it is high while the kernel says memory is
// short. Swap in use alone is what every Mac has — 31 of 32 GiB beside a normal
// pressure — and a line on it would paint a healthy machine red; high swap
// beside pressure is a machine paging.
func (f overviewFacts) swapRow() row {
	s := f.swap
	if s.err != nil {
		return unreadable("swap", s.err)
	}
	if s.total == 0 {
		return row{}
	}
	fact := fmt.Sprintf("%s (%.0f%%)", sizes(s.used, s.total), s.percent)
	underPressure := f.mem.err == nil && f.mem.pressureErr == nil && f.mem.pressure != pressureNormal
	if math.Round(s.percent) >= view.UsageWarn && underPressure {
		return row{key: "swap", value: led("warn", fact), grade: gradeWarn, headline: fmt.Sprintf("swap %.0f%% used", s.percent)}
	}
	return row{key: "swap", value: fact}
}

func (f overviewFacts) hostRow() row {
	h := f.host
	if h.err != nil {
		return unreadable("host", h.err)
	}
	var parts []string
	if h.name != "" {
		parts = append(parts, h.name)
	}
	if h.uptime > 0 {
		parts = append(parts, "up "+format.Uptime(h.uptime))
	}
	if len(parts) == 0 {
		return unreadable("host", errors.New("no hostname or uptime"))
	}
	return row{key: "host", value: strings.Join(parts, " · ")}
}

// numbers are the figures the rows were made from, for a program (see
// view.KeyValue.Numbers). A reading the page does not show has no number: a
// consumer must not mistake an absent figure for a zero.
func (f overviewFacts) numbers() map[string]float64 {
	n := map[string]float64{}
	if f.cores > 0 {
		n["cores"] = float64(f.cores)
	}
	if f.disk.err == nil {
		n["disk_percent"] = f.disk.percent
		n["disk_used_bytes"] = float64(f.disk.used)
		n["disk_total_bytes"] = float64(f.disk.total)
	}
	if f.mem.err == nil {
		n["mem_percent"] = f.mem.percent
		n["mem_used_bytes"] = float64(f.mem.used)
		n["mem_total_bytes"] = float64(f.mem.total)
		if f.mem.pressureErr == nil {
			n["mem_pressure"] = float64(f.mem.pressure)
		}
	}
	if f.load.err == nil {
		n["load1"], n["load5"], n["load15"] = f.load.load1, f.load.load5, f.load.load15
		if f.cores > 0 {
			n["load_per_core"] = f.load.load1 / float64(f.cores)
		}
	}
	if f.cpu.err == nil {
		n["cpu_percent"] = f.cpu.percent
	}
	if f.swap.err == nil && f.swap.total > 0 {
		n["swap_percent"] = f.swap.percent
		n["swap_used_bytes"] = float64(f.swap.used)
		n["swap_total_bytes"] = float64(f.swap.total)
	}
	if f.host.err == nil && f.host.uptime > 0 {
		n["uptime_seconds"] = f.host.uptime.Seconds()
	}
	return n
}
