package pkg

import (
	"context"
	"os/exec"
	"runtime"
	"slices"
	"strings"
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

// **macOS's own Ruby is not a package manager of the person's.** On one Mac,
// 42 of the 60 packages the overview counted behind were the gems Apple's
// Ruby 2.6 ships, each offered with a `gem update` that cannot write to the
// system volume without sudo. Linux keeps its rows: a distribution's Ruby
// can hold gems installed with --user-install.
func TestMacOSOwnRubyIsNotAManagerOfYours(t *testing.T) {
	for _, tc := range []struct {
		goos, path string
		owned      bool
	}{
		{"darwin", "/usr/bin/gem", true},
		{"darwin", "/opt/homebrew/opt/ruby/bin/gem", false},
		{"darwin", "/Users/someone/.local/share/mise/shims/gem", false},
		{"linux", "/usr/bin/gem", false},
	} {
		if got := systemRuby(tc.goos)(tc.path) != ""; got != tc.owned {
			t.Errorf("%s %s: owned by the OS = %v, want %v", tc.goos, tc.path, got, tc.owned)
		}
	}

	f := &fake{bins: map[string]bool{"gem": true}, answers: map[string]fakeAnswer{
		"gem --version": {out: "3.0.3.1\n"},
		"gem outdated":  {out: "csv (3.0.9 < 3.3.7)\n"},
	}}
	install(t, f)
	lookPath = func(name string) (string, error) {
		if name == "gem" {
			return "/usr/bin/gem", nil
		}
		return "", exec.ErrNotFound
	}
	if runtime.GOOS != "darwin" {
		if len(detected()) != 1 {
			t.Errorf("gem at /usr/bin on %s = %d managers, want it read", runtime.GOOS, len(detected()))
		}
		return
	}
	if len(detected()) != 0 {
		t.Errorf("macOS's own gem was detected as a manager")
	}
	for _, row := range managersTable(context.Background()).Rows {
		if row[0] == "gem" && !strings.HasPrefix(row[5], "skipped — macOS's own Ruby") {
			t.Errorf("pkg.managers says gem is %q, want it skipped with the reason", row[5])
		}
	}
	_, err := runUpgradeCapability(context.Background(), req(t, "pkg.upgrade", map[string]any{"target": "gem"}))
	if ve := view.AsError(err, "x"); ve.Code != "pkg.manager.system" {
		t.Errorf("upgrading macOS's own gem = %+v, want pkg.manager.system", ve)
	}
	for _, ran := range f.ran {
		if ran == "gem outdated" {
			t.Error("macOS's own gem was asked what is outdated")
		}
	}
}
