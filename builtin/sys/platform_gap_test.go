package sys

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
)

// Both gaps pinned here were invisible to the test suite and to CI, because
// each only appears on a platform neither runner is: arm64 Linux for the CPU
// model, and any container for the root filesystem. The live-host tests in
// sys_test.go would catch them there and nowhere else, so the decisions are
// pinned as pure functions instead — those run everywhere.

// **A key with no value is worse than no key.**
//
// /proc/cpuinfo on arm64 carries no "model name" line at all, so gopsutil
// leaves ModelName empty and folds "CPU implementer" into VendorID. Reading
// ModelName alone printed a bare "model:" on every Pi, Graviton and Ampere
// machine — and rta ships linux/arm64. Neither CI runner can see it: ubuntu is
// amd64, where the line exists, and macOS is arm64 but reads sysctl.
func TestCPUModelFallsBackToTheVendorAndNeverRendersBlank(t *testing.T) {
	tests := []struct {
		name string
		info cpu.InfoStat
		want string
		why  string
	}{
		{
			name: "amd64 linux and macos: a real model name wins",
			info: cpu.InfoStat{ModelName: "Apple M3 Pro", VendorID: "Apple"},
			want: "Apple M3 Pro",
			why:  "the vendor must never displace a model name that exists",
		},
		{
			name: "arm64 linux: no model name, so the implementer stands in",
			info: cpu.InfoStat{ModelName: "", VendorID: "ARM"},
			want: "ARM",
			why:  "this is the Raspberry Pi and Graviton case the pair used to render blank",
		},
		{
			name: "nothing readable at all: no pair rather than an empty one",
			info: cpu.InfoStat{},
			want: "",
			why:  "runCPU appends the pair only for a non-empty result",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cpuModel(tc.info); got != tc.want {
				t.Errorf("cpuModel(%+v) = %q, want %q — %s", tc.info, got, tc.want, tc.why)
			}
		})
	}
}

// **The root filesystem is never noise.**
//
// gopsutil keeps a mount only when its fstype is a non-"nodev" entry of
// /proc/filesystems and the mount is not a bind. Inside a container the root is
// `overlay` (nodev) and everything else is a bind or a pseudo filesystem, so
// the filtered list comes back completely empty: `rta sys disk` printed nothing
// and `rta sys overview` had no disk line, while df in the same shell showed
// the filesystem at 96%. rta publishes a Docker image, so that is a supported
// way to run it.
func TestTheRootFilesystemSurvivesAFilterThatDropsEverything(t *testing.T) {
	// A real container's mount table, trimmed to the shapes that matter.
	full := []disk.PartitionStat{
		{Mountpoint: "/", Fstype: "overlay"},
		{Mountpoint: "/proc", Fstype: "proc"},
		{Mountpoint: "/sys/fs/cgroup", Fstype: "cgroup2"},
		{Mountpoint: "/dev/shm", Fstype: "tmpfs"},
	}

	// What gopsutil's own filter hands rta in a container: nothing at all.
	kept := keepReal(nil)
	if hasRoot(kept) {
		t.Fatal("the empty container case no longer reproduces; the rest of this test proves nothing")
	}

	got := withRoot(kept, full)
	if !hasRoot(got) {
		t.Fatal("the root filesystem was lost: rta reports no disk at all inside a container")
	}
	if got[0].Mountpoint != "/" {
		t.Errorf("root is at index %d, want first: an operator scanning the table reads the top",
			len(got))
	}
	if got[0].Fstype != "overlay" {
		t.Errorf("root fstype = %q, want overlay carried through unchanged", got[0].Fstype)
	}
}

// overlay left pseudoFS when the rescue landed: it is a container's real root,
// backed by real disk, and its numbers are the ones that decide whether the
// container runs out of space. Keeping it in the map would have had the filter
// drop the very mount the rescue exists to recover.
func TestOverlayIsNotTreatedAsNoise(t *testing.T) {
	if pseudoFS["overlay"] {
		t.Error("overlay is back in pseudoFS: a container's root is real storage, not noise")
	}
	kept := keepReal([]disk.PartitionStat{{Mountpoint: "/", Fstype: "overlay"}})
	if !hasRoot(kept) {
		t.Error("keepReal dropped an overlay root")
	}
}

// **A frozen counter is "unreadable", never "0% busy".**
//
// macOS refreshes the host-wide CPU counter about once a second, so two reads
// 200ms apart come back identical roughly half the time. gopsutil turns that
// pair into a usage of 0, and `rta sys cpu` printed 0.0% on a machine at load
// 6 — see sampleCPU. The macOS CI runner may or may not land in the frozen
// window on any given run, and the Linux one never does, so the decision is
// pinned here on synthetic samples rather than left to the live-host tests.
func TestAFrozenCPUSampleIsUnreadableNotIdle(t *testing.T) {
	same := []cpu.TimesStat{
		{CPU: "cpu0", User: 100, System: 50, Idle: 850},
		{CPU: "cpu1", User: 10, System: 5, Idle: 985},
	}
	if s, ok := busyShares(same, same); ok {
		t.Fatalf("identical reads produced a usage of %.1f%%; an idle core still accrues idle ticks, "+
			"so an unchanged counter is a stale read and must not become a number", s.total)
	}
	if _, ok := busyShares(same, same[:1]); ok {
		t.Error("reads describing different core sets were combined")
	}
	if _, ok := busyShares(nil, nil); ok {
		t.Error("no cores at all produced a usage figure")
	}
}

// The host-wide figure is the per-core ticks summed, not the cached total the
// kernel keeps for host_statistics — that is the whole fix. One core flat out
// and one core idle over the same window is a machine half busy.
func TestUsageIsSummedFromTheCoresNotTheCachedTotal(t *testing.T) {
	before := []cpu.TimesStat{
		{CPU: "cpu0", User: 100, Idle: 100},
		{CPU: "cpu1", User: 100, Idle: 100},
	}
	after := []cpu.TimesStat{
		{CPU: "cpu0", User: 120, Idle: 100}, // 20 ticks, all busy
		{CPU: "cpu1", User: 100, Idle: 120}, // 20 ticks, all idle
	}
	s, ok := busyShares(before, after)
	if !ok {
		t.Fatal("advancing counters were reported as frozen")
	}
	if s.total != 50 {
		t.Errorf("total = %.1f%%, want 50%%", s.total)
	}
	if len(s.perCore) != 2 || s.perCore[0] != 100 || s.perCore[1] != 0 {
		t.Errorf("per-core = %v, want [100 0]", s.perCore)
	}
}

// When even the per-core counters have not moved after the window, sampleCPU
// keeps reading rather than reporting the frozen pair, and gives up with an
// error — not a number — once its patience runs out.
func TestSampleCPUWaitsForTheCountersToMoveThenGivesUp(t *testing.T) {
	frozen := []cpu.TimesStat{{CPU: "cpu0", User: 100, Idle: 100}}
	moved := []cpu.TimesStat{{CPU: "cpu0", User: 110, Idle: 130}}

	reads := 0
	thawsOnThirdRead := func(context.Context) ([]cpu.TimesStat, error) {
		reads++
		if reads < 4 {
			return frozen, nil
		}
		return moved, nil
	}
	s, err := sampleCPUWith(context.Background(), thawsOnThirdRead, time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("counters that thawed within patience were reported unreadable: %v", err)
	}
	if reads != 4 {
		t.Errorf("read %d times, want 4: one base read, two frozen retries, one that moved", reads)
	}
	if s.total != 25 {
		t.Errorf("total = %.1f%%, want 25%% (10 busy ticks of 40)", s.total)
	}

	neverThaws := func(context.Context) ([]cpu.TimesStat, error) { return frozen, nil }
	_, err = sampleCPUWith(context.Background(), neverThaws, time.Millisecond, 20*time.Millisecond)
	if !errors.Is(err, errCPUFrozen) {
		t.Errorf("counters that never moved gave %v, want errCPUFrozen", err)
	}
}

// An ordinary host must not pay for the rescue: its root is already in the
// filtered list, so nothing is prepended and nothing is duplicated.
func TestAnOrdinaryRootIsNotRescuedTwice(t *testing.T) {
	parts := []disk.PartitionStat{
		{Mountpoint: "/", Fstype: "ext4"},
		{Mountpoint: "/home", Fstype: "xfs"},
		{Mountpoint: "/proc", Fstype: "proc"},
	}
	kept := keepReal(parts)
	if !hasRoot(kept) {
		t.Fatal("an ext4 root was filtered out")
	}
	roots := 0
	for _, p := range withRoot(kept, parts) {
		if p.Mountpoint == "/" {
			roots++
		}
	}
	if roots != 1 {
		t.Errorf("root appears %d times, want 1", roots)
	}
}
