package pkg

import (
	"context"
	"runtime"
	"slices"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// **An update macOS offers is not a reboot owed.** softwareupdate marks an
// offered update that restarts the machine once installed with Action:
// restart, and the reading took that for a reboot owed: the overview said
// "warn — reboot owed" ahead of every package behind, on a machine where a
// reboot would have installed nothing. macOS keeps no record of a reboot
// owed, so there is no reboot line there; the offered updates are behind,
// counted after the packages, and each says whether it restarts the machine.
func TestAnOfferedMacOSUpdateIsNotARebootOwed(t *testing.T) {
	install(t, machine(brewCurrent, aptCurrent))
	readsOS(t, osState{Updates: []osUpdate{
		{Title: "macOS Tahoe 26.7.3", Version: "26.7.3", Restart: true, Label: "macOS Tahoe 26.7.3-25G342"},
		{Title: "Safari", Version: "27.2", Label: "Safari27.2TahoeAuto-27.2"},
	}})

	pairs := compact(t, plugin.SurfaceCLI)
	if got := valueOf(t, pairs, "status"); got != "warn — 2 OS updates offered" {
		t.Errorf("status = %q, want the offered updates as what is behind", got)
	}
	if slices.Contains(keysOf(pairs), "reboot") {
		t.Errorf("rows = %v: an OS that keeps no record of a reboot owed has no reboot line", keysOf(pairs))
	}
	if runtime.GOOS == "darwin" {
		if got := valueOf(t, pairs, "os updates"); got != "2 offered, 1 restarts the machine" {
			t.Errorf("os updates = %q", got)
		}
	}

	v, err := osCapability().Run(context.Background(), req(t, "pkg.os", nil))
	if err != nil {
		t.Fatal(err)
	}
	sections, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("pkg.os answered %T, want sections", v)
	}
	for _, s := range sections.Items {
		switch s.ID {
		case "state":
			if keys := keysOf(s.View.(view.KeyValue).Pairs); slices.Contains(keys, "reboot") {
				t.Errorf("pkg.os state = %v, want no reboot line", keys)
			}
		case "updates":
			rows := s.View.(view.Table).Rows
			if rows[0][2] != "yes" || rows[1][2] != "no" {
				t.Errorf("restarts = %q, %q; want yes for the one that restarts, no for the other", rows[0][2], rows[1][2])
			}
		}
	}
}
