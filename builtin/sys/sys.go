// Package sys is the built-in host telemetry plugin: CPU, memory, disk,
// load, host info and processes. It needs zero configuration, which makes it
// the first-contact experience and the M0 proof that one
// capability serves every renderer.
package sys

import (
	"context"
	"fmt"
	stdnet "net"
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/shirou/gopsutil/v4/sensors"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Plugin returns the sys plugin declaration.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "sys",
		Summary: "Host telemetry: CPU, memory, disk, load, processes",
		Capabilities: []plugin.Capability{
			{
				ID:           "sys.overview",
				Summary:      "One-screen host health: cpu, memory, load, disk, uptime",
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				Detailed:     true,
				Description: "A dense one-line-per-subsystem summary. With --detail (and on any " +
					"full-page surface) it expands into a full report: identity, per-core usage, " +
					"memory and swap breakdown, every filesystem, sensors and top processes. " +
					"Lines a platform cannot provide are omitted rather than failing the view.",
				Run: runOverview,
			},
			{
				ID:           "sys.cpu",
				Summary:      "Show CPU model, core count and current usage",
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				Inputs: []plugin.Field{
					{Name: "cores", Type: plugin.Bool, Help: "per-core usage as a bar chart"},
				},
				Run: runCPU,
			},
			{
				ID:           "sys.mem",
				Summary:      "Show memory and swap usage",
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				Run:          runMem,
			},
			{
				ID:           "sys.disk",
				Summary:      "Show disk usage per mounted filesystem",
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				Inputs: []plugin.Field{
					{Name: "all", Type: plugin.Bool, Help: "include pseudo, duplicate and zero-size filesystems"},
				},
				Run: runDisk,
			},
			{
				ID:           "sys.load",
				Summary:      "Show load averages, normalized per core",
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				Run:          runLoad,
			},
			{
				ID:           "sys.host",
				Summary:      "Show host info: OS, kernel, uptime, addresses",
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				Run:          runHost,
			},
			{
				ID:           "sys.ps",
				Summary:      "List top processes by CPU or memory",
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				Inputs: []plugin.Field{
					{Name: "limit", Type: plugin.Int, Help: "maximum processes to list", Default: 15, Min: 1, Max: 1000},
					{Name: "sort", Type: plugin.String, Help: "sort by", Default: "cpu",
						Options: []string{"cpu", "mem"}},
				},
				Run: runPS,
			},
			{
				ID:           "sys.temp",
				Summary:      "Show sensor temperatures",
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				Run:          runTemp,
			},
		},
	}
}

// runOverview composes one dense line per subsystem — the grouped system
// tile. Each line degrades independently: what a platform cannot read is
// simply absent. On a full page (req "detail") it expands into a report.
func runOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	if req.Bool("detail") {
		return detailedOverview(ctx, req)
	}
	kv := view.KeyValue{}
	add := func(key, value string) {
		if value != "" {
			kv.Pairs = append(kv.Pairs, view.Pair{Key: key, Value: value})
		}
	}

	if info, err := host.InfoWithContext(ctx); err == nil {
		add("host", fmt.Sprintf("%s · %s %s (%s) · up %s",
			info.Hostname, info.Platform, info.PlatformVersion, info.KernelArch,
			humanDuration(time.Duration(info.Uptime)*time.Second)))
	}
	cores, _ := cpu.CountsWithContext(ctx, true)
	if percs, err := cpu.PercentWithContext(ctx, 200*time.Millisecond, false); err == nil && len(percs) > 0 {
		add("cpu", fmt.Sprintf("%.1f%% of %d cores", percs[0], cores))
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		add("mem", fmt.Sprintf("%s / %s (%.1f%%)", format.Bytes(vm.Used), format.Bytes(vm.Total), vm.UsedPercent))
	}
	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil && sw.Total > 0 {
		add("swap", fmt.Sprintf("%s / %s (%.1f%%)", format.Bytes(sw.Used), format.Bytes(sw.Total), sw.UsedPercent))
	}
	if avg, err := load.AvgWithContext(ctx); err == nil {
		v := fmt.Sprintf("%.2f · %.2f · %.2f", avg.Load1, avg.Load5, avg.Load15)
		if cores > 0 {
			perCore := avg.Load1 / float64(cores)
			v += fmt.Sprintf(" (%.0f%%/core, %s)", perCore*100, loadVerdict(perCore))
		}
		add("load", v)
	}
	add("disk", fullestDisk(ctx))

	if len(kv.Pairs) == 0 {
		return nil, view.Errorf("sys.overview.unavailable", "no host telemetry readable on this platform")
	}
	return kv, nil
}

// detailedOverview is the full-page report, composed rather than rebuilt:
// each section is the view another sys capability already returns. Adding a
// capability to the report is one line here, and the section's rendering,
// JSON shape and MCP payload come along for free (plugin.Page).
//
// Sections whose capability fails on this platform (sensors, typically) are
// simply absent — a partial report beats a failed one.
func detailedOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	p := plugin.NewPage(ctx, req)
	// The id is the capability the section came from, not a slug of its
	// heading: a reader of `sys overview --detail` in JSON can line the
	// "storage" section up with `rta sys disk` and get the same shape, and
	// the heading stays free to be reworded. See view.Section.
	p.AddAs("host", "host", runHost, plugin.Read, nil)
	p.AddAs("cpu", "cpu", runCPU, plugin.Read, map[string]any{"cores": true}) // per-core bar chart
	p.AddAs("mem", "memory", runMem, plugin.Read, nil)
	p.AddAs("load", "load", runLoad, plugin.Read, nil)
	p.AddAs("disk", "storage", runDisk, plugin.Read, nil)
	p.AddAs("temp", "sensors", runTemp, plugin.Read, nil)
	p.AddAs("ps", "top processes", runPS, plugin.Read, map[string]any{"limit": detailTopN, "sort": "cpu"})

	if p.Empty() {
		return nil, view.Errorf("sys.overview.unavailable", "no host telemetry readable on this platform")
	}
	return p.View(), nil
}

// detailTopN bounds the process list in the detailed report: enough to be
// useful, short enough to stay one screen.
const detailTopN = 5

// realPartitions lists the filesystems worth reporting: what the platform
// calls real storage, minus rta's own noise filter, plus the root filesystem
// whenever the first two lost it.
//
// **That last clause is why rta reports any disk at all inside a container.**
// gopsutil keeps a mount only when its fstype is a non-"nodev" entry of
// /proc/filesystems and the mount is not a bind. In a container the root is
// `overlay`, which is nodev, and every other mount is a bind or a pseudo
// filesystem — so the list comes back *completely empty*, and `rta sys disk`
// printed an empty table while `df /` in the same shell showed a filesystem at
// 96%. rta publishes a Docker image, so this is a supported way to run it and
// the silence was the whole answer.
//
// Rescuing "/" rather than un-filtering `overlay` is deliberate: it fixes the
// class instead of the instance. Any root the platform's heuristic declines to
// call storage — a squashfs live image, a tmpfs initramfs — is still the
// filesystem holding your files and still the one that fills up, and whatever
// heuristic runs, "/" always counts. On an ordinary host the root is already
// in the filtered list and the rescue never runs.
// The decision is split into pure pieces below because the interesting half
// of it — that a missing root is recovered — cannot be provoked on a developer
// machine or on either CI runner, all of which have an ordinary root. Testing
// it through the live call would mean testing it nowhere.
func realPartitions(ctx context.Context) ([]disk.PartitionStat, error) {
	parts, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return nil, view.Errorf("sys.disk.partitions", "listing partitions: %v", err)
	}
	kept := keepReal(parts)
	if hasRoot(kept) {
		return kept, nil
	}
	// Only reached when the root went missing, so the second, unfiltered read
	// is paid for exactly where it buys something.
	all, err := disk.PartitionsWithContext(ctx, true)
	if err != nil {
		return kept, nil
	}
	return withRoot(kept, all), nil
}

// keepReal drops the mounts rta considers noise.
func keepReal(parts []disk.PartitionStat) []disk.PartitionStat {
	out := make([]disk.PartitionStat, 0, len(parts))
	for _, p := range parts {
		if pseudoFS[p.Fstype] || systemVolume(p.Mountpoint) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func hasRoot(parts []disk.PartitionStat) bool {
	for _, p := range parts {
		if p.Mountpoint == "/" {
			return true
		}
	}
	return false
}

// withRoot puts the root filesystem back at the front of a list that lost it,
// taking it from the unfiltered mount table. First, because it is the one an
// operator scanning the table wants to see, and because fullestDisk otherwise
// reports whichever mount happens to be fullest while the root fills up
// unmentioned.
//
// It re-checks kept rather than trusting the caller to have done so. The
// hasRoot call in realPartitions is about not paying for a second read of the
// mount table, not about correctness here — and a version of this that relied
// on the caller listed the root twice the moment a test called it directly.
func withRoot(kept, all []disk.PartitionStat) []disk.PartitionStat {
	if hasRoot(kept) {
		return kept
	}
	for _, p := range all {
		if p.Mountpoint == "/" {
			return append([]disk.PartitionStat{p}, kept...)
		}
	}
	return kept
}

// fullestDisk summarizes the most-used real filesystem — the one that pages
// you first.
func fullestDisk(ctx context.Context) string {
	parts, err := realPartitions(ctx)
	if err != nil {
		return ""
	}
	best, bestPct := "", -1.0
	for _, p := range parts {
		u, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		if u.UsedPercent > bestPct {
			bestPct = u.UsedPercent
			best = fmt.Sprintf("%s %s / %s (%.0f%%, %s)", p.Mountpoint,
				format.Bytes(u.Used), format.Bytes(u.Total), u.UsedPercent, usageStatus(u.UsedPercent))
		}
	}
	return best
}

func runCPU(ctx context.Context, req plugin.Request) (view.View, error) {
	if req.Bool("cores") {
		// Per-core view: one bar per core, fixed 0-100 scale.
		percs, err := cpu.PercentWithContext(ctx, 200*time.Millisecond, true)
		if err != nil {
			return nil, view.Errorf("sys.cpu.usage", "reading per-core usage: %v", err)
		}
		chart := view.Chart{Kind: view.ChartBar, Unit: "%", Max: 100}
		for i, p := range percs {
			chart.Series = append(chart.Series, view.Series{
				Name: fmt.Sprintf("core%d", i), Points: []float64{p},
			})
		}
		return chart, nil
	}
	infos, err := cpu.InfoWithContext(ctx)
	if err != nil {
		return nil, view.Errorf("sys.cpu.info", "reading CPU info: %v", err)
	}
	// Sampling interval: long enough to be meaningful, short enough for a CLI.
	percs, err := cpu.PercentWithContext(ctx, 200*time.Millisecond, false)
	if err != nil {
		return nil, view.Errorf("sys.cpu.usage", "reading CPU usage: %v", err)
	}
	physical, _ := cpu.CountsWithContext(ctx, false)
	logical, _ := cpu.CountsWithContext(ctx, true)

	kv := view.KeyValue{}
	if len(infos) > 0 {
		if model := cpuModel(infos[0]); model != "" {
			kv.Pairs = append(kv.Pairs, view.Pair{Key: "model", Value: model})
		}
	}
	kv.Pairs = append(kv.Pairs,
		view.Pair{Key: "cores", Value: fmt.Sprintf("%d physical, %d logical", physical, logical)},
	)
	if len(percs) > 0 {
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "usage", Value: fmt.Sprintf("%.1f%%", percs[0])})
	}
	return kv, nil
}

// cpuModel names the processor as well as the platform allows.
//
// ModelName is empty on every arm64 Linux machine, and rta ships linux/arm64:
// /proc/cpuinfo there has no "model name" line at all, only "CPU implementer"
// and "CPU part", which gopsutil folds into VendorID instead. Reading
// ModelName alone therefore printed a bare "model:" with nothing after it on
// every Pi, Graviton and Ampere box, and the CI matrix cannot see it — ubuntu
// is amd64, where the line exists, and macOS is arm64 but reads sysctl rather
// than procfs.
//
// The vendor alone ("ARM", "Broadcom", "Apple", "Qualcomm"…) is thinner than
// a model name and it is what the hardware actually reports; an empty string
// falls through to no pair at all, because a key with no value tells a reader
// less than its absence does.
func cpuModel(i cpu.InfoStat) string {
	if i.ModelName != "" {
		return i.ModelName
	}
	return i.VendorID
}

func runMem(ctx context.Context, _ plugin.Request) (view.View, error) {
	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return nil, view.Errorf("sys.mem.read", "reading memory: %v", err)
	}
	pairs := []view.Pair{
		{Key: "total", Value: format.Bytes(vm.Total)},
		{Key: "used", Value: fmt.Sprintf("%s (%.1f%%)", format.Bytes(vm.Used), vm.UsedPercent)},
		{Key: "available", Value: format.Bytes(vm.Available)},
	}
	// Swap pressure is the classic "why is this box slow" signal; shown only
	// where swap exists.
	if sw, err := mem.SwapMemoryWithContext(ctx); err == nil && sw.Total > 0 {
		pairs = append(pairs, view.Pair{
			Key:   "swap",
			Value: fmt.Sprintf("%s / %s (%.1f%%)", format.Bytes(sw.Used), format.Bytes(sw.Total), sw.UsedPercent),
		})
	}
	return view.KeyValue{Pairs: pairs}, nil
}

func runDisk(ctx context.Context, req plugin.Request) (view.View, error) {
	all := req.Bool("all")
	var parts []disk.PartitionStat
	var err error
	if all {
		parts, err = disk.PartitionsWithContext(ctx, true)
		if err != nil {
			return nil, view.Errorf("sys.disk.partitions", "listing partitions: %v", err)
		}
	} else if parts, err = realPartitions(ctx); err != nil {
		return nil, err
	}
	t := view.Table{Columns: []view.Column{
		{Name: "Mount"},
		{Name: "FS"},
		{Name: "Size", Kind: view.KindBytes},
		{Name: "Used", Kind: view.KindBytes},
		{Name: "Free", Kind: view.KindBytes},
		{Name: "Use%", Kind: view.KindPercent},
		{Name: "Status", Kind: view.KindStatus},
	}}
	// realPartitions has already dropped the noise; --all keeps the raw list.
	for _, p := range parts {
		u, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		t.Rows = append(t.Rows, []string{
			p.Mountpoint,
			p.Fstype,
			format.Bytes(u.Total),
			format.Bytes(u.Used),
			format.Bytes(u.Free),
			fmt.Sprintf("%.0f%%", u.UsedPercent),
			usageStatus(u.UsedPercent),
		})
	}
	t.Total = len(t.Rows)
	return t, nil
}

// pseudoFS lists filesystem types that are not storage.
//
// Most of these only ever fire on macOS and the BSDs. On Linux gopsutil has
// already dropped every "nodev" type — which is all of them but squashfs —
// before this map is consulted, so the entries are kept as a statement of what
// rta considers noise on any platform rather than as a filter that does work
// on every one.
//
// `overlay` is deliberately *not* here. It is a container's real root, backed
// by real disk, and the numbers statfs reports for it are the ones that decide
// whether the container runs out of space. See realPartitions.
var pseudoFS = map[string]bool{
	"devfs": true, "devtmpfs": true, "tmpfs": true, "proc": true,
	"sysfs": true, "cgroup": true, "cgroup2": true,
	"squashfs": true, "autofs": true, "nullfs": true, "debugfs": true,
	"securityfs": true, "fusectl": true, "ramfs": true,
}

// systemVolume hides the macOS APFS system mounts that mirror the same
// container ("/System/Volumes/…"); user data lives under Data, which shares
// its numbers with "/", so nothing is lost.
func systemVolume(mountpoint string) bool {
	return strings.HasPrefix(mountpoint, "/System/Volumes/")
}

// loadVerdict grades a per-core load average in the shared vocabulary.
func loadVerdict(perCore float64) string {
	switch {
	case perCore >= 1.0:
		return "overloaded"
	case perCore >= 0.7:
		return "busy"
	default:
		return "ok"
	}
}

// usageStatus maps a fill percentage to the shared status vocabulary
// (rendered green/yellow/red by every surface).
func usageStatus(pct float64) string {
	switch {
	case pct >= 90:
		return "ERROR >90%"
	case pct >= 80:
		return "WARN >80%"
	default:
		return "ok"
	}
}

func runLoad(ctx context.Context, _ plugin.Request) (view.View, error) {
	avg, err := load.AvgWithContext(ctx)
	if err != nil {
		return nil, view.Errorf("sys.load.read", "reading load averages: %v", err)
	}
	// Raw load means nothing without core count: 16.0 is idle on 32 cores
	// and a meltdown on 2. Normalize and say so.
	cores, _ := cpu.CountsWithContext(ctx, true)
	format := func(v float64) string {
		if cores <= 0 {
			return fmt.Sprintf("%.2f", v)
		}
		perCore := v / float64(cores)
		return fmt.Sprintf("%.2f (%.0f%%/core, %s)", v, perCore*100, loadVerdict(perCore))
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "load1", Value: format(avg.Load1)},
		{Key: "load5", Value: format(avg.Load5)},
		{Key: "load15", Value: format(avg.Load15)},
	}}, nil
}

func runHost(ctx context.Context, _ plugin.Request) (view.View, error) {
	info, err := host.InfoWithContext(ctx)
	if err != nil {
		return nil, view.Errorf("sys.host.read", "reading host info: %v", err)
	}
	uptime := time.Duration(info.Uptime) * time.Second
	pairs := []view.Pair{
		{Key: "hostname", Value: info.Hostname},
		{Key: "os", Value: fmt.Sprintf("%s %s (%s)", info.Platform, info.PlatformVersion, info.KernelArch)},
		{Key: "kernel", Value: info.KernelVersion},
		{Key: "uptime", Value: humanDuration(uptime)},
		{Key: "procs", Value: fmt.Sprintf("%d", info.Procs)},
	}
	if ips := hostAddrs(); len(ips) > 0 {
		pairs = append(pairs, view.Pair{Key: "addresses", Value: strings.Join(ips, ", ")})
	}
	return view.KeyValue{Pairs: pairs}, nil
}

// hostAddrs returns up to four non-loopback unicast addresses, IPv4 first —
// the first question anyone asks a host.
func hostAddrs() []string {
	ifaces, err := stdnet.Interfaces()
	if err != nil {
		return nil
	}
	var v4, v6 []string
	for _, iface := range ifaces {
		if iface.Flags&stdnet.FlagUp == 0 || iface.Flags&stdnet.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*stdnet.IPNet)
			if !ok || ipnet.IP.IsLinkLocalUnicast() {
				continue
			}
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				v4 = append(v4, ip4.String())
			} else {
				v6 = append(v6, ipnet.IP.String())
			}
		}
	}
	out := append(v4, v6...)
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

func runPS(ctx context.Context, req plugin.Request) (view.View, error) {
	limit := req.Int("limit")
	if limit <= 0 {
		limit = 15
	}
	sortBy := req.String("sort")
	if sortBy == "" {
		sortBy = "cpu"
	}
	if sortBy != "cpu" && sortBy != "mem" {
		return nil, view.Errorf("sys.ps.badsort", "unknown sort %q", sortBy).
			WithHint("use --sort cpu or --sort mem")
	}
	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, view.Errorf("sys.ps.list", "listing processes: %v", err)
	}
	type row struct {
		pid  int32
		name string
		cpu  float64
		mem  float32
		rss  uint64
	}
	rows := make([]row, 0, len(procs))
	for _, p := range procs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		name, err := p.NameWithContext(ctx)
		if err != nil {
			continue // process may have exited, or be inaccessible
		}
		cpuPct, _ := p.CPUPercentWithContext(ctx)
		memPct, _ := p.MemoryPercentWithContext(ctx)
		r := row{pid: p.Pid, name: name, cpu: cpuPct, mem: memPct}
		if mi, err := p.MemoryInfoWithContext(ctx); err == nil && mi != nil {
			r.rss = mi.RSS
		}
		rows = append(rows, r)
	}
	if sortBy == "mem" {
		sort.Slice(rows, func(i, j int) bool { return rows[i].rss > rows[j].rss })
	} else {
		sort.Slice(rows, func(i, j int) bool { return rows[i].cpu > rows[j].cpu })
	}
	total := len(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	t := view.Table{
		Columns: []view.Column{
			{Name: "PID", Kind: view.KindNumber},
			{Name: "Name"},
			{Name: "CPU%", Kind: view.KindPercent},
			{Name: "Mem%", Kind: view.KindPercent},
			{Name: "RSS", Kind: view.KindBytes},
		},
		Total: total,
	}
	for _, r := range rows {
		t.Rows = append(t.Rows, []string{
			fmt.Sprintf("%d", r.pid),
			r.name,
			fmt.Sprintf("%.1f", r.cpu),
			fmt.Sprintf("%.1f", r.mem),
			format.Bytes(r.rss),
		})
	}
	return t, nil
}

func runTemp(ctx context.Context, _ plugin.Request) (view.View, error) {
	temps, err := sensors.TemperaturesWithContext(ctx)
	if err != nil && len(temps) == 0 {
		return nil, view.Errorf("sys.temp.unavailable", "reading sensors: %v", err).
			WithHint("sensor access is platform-dependent; on macOS it may need elevated privileges")
	}
	t := view.Table{Columns: []view.Column{
		{Name: "Sensor"},
		{Name: "°C", Kind: view.KindNumber},
		{Name: "Status", Kind: view.KindStatus},
	}}
	for _, s := range temps {
		if s.Temperature <= 0 {
			continue // dead or unreadable probe
		}
		status := "ok"
		switch {
		case s.Critical > 0 && s.Temperature >= s.Critical:
			status = "ERROR critical"
		case s.High > 0 && s.Temperature >= s.High:
			status = "WARN high"
		case s.Temperature >= 85:
			status = "WARN high"
		}
		t.Rows = append(t.Rows, []string{s.SensorKey, fmt.Sprintf("%.0f", s.Temperature), status})
	}
	if len(t.Rows) == 0 {
		return nil, view.Errorf("sys.temp.none", "no readable temperature sensors").
			WithHint("sensor access is platform-dependent; on macOS it may need elevated privileges")
	}
	t.Total = len(t.Rows)
	return t, nil
}

func humanDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}
