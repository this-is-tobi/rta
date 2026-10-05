package sys

import (
	"context"
	"testing"

	"github.com/shirou/gopsutil/v4/host"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A host whose PATH has none of the system tools still has a name, a kernel and
// an uptime. On macOS the host id is read by running ioreg, found through PATH,
// and a failure to find it used to fail the whole of `sys host` — under cron,
// launchd or a bare environment — over a field nothing shows.
func TestHostAnswersWhenTheSystemToolsAreNotOnPATH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	v, err := runHost(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatalf("sys host with no tools on PATH: %v", err)
	}
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("host returned %s, want a KeyValue", view.TypeOf(v))
	}
	have := map[string]string{}
	for _, p := range kv.Pairs {
		have[p.Key] = p.Value
	}
	for _, key := range []string{"hostname", "kernel", "uptime"} {
		if have[key] == "" {
			t.Errorf("no %s row in %v", key, kv.Pairs)
		}
	}
}

// And the overview, whose host line was dropped without a word when the same
// call failed.
func TestOverviewKeepsItsHostLineWhenTheSystemToolsAreNotOnPATH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	v, err := runOverview(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range v.(view.KeyValue).Pairs {
		if p.Key == "host" {
			return
		}
	}
	t.Errorf("no host line in %v", v.(view.KeyValue).Pairs)
}

// A piece that could not be read leaves its words out of the line, not a
// dangling "()" or a double space in it.
func TestTheHostLinesNameOnlyWhatWasRead(t *testing.T) {
	full := &host.InfoStat{Hostname: "build-1", Platform: "darwin", PlatformVersion: "26.0", KernelArch: "arm64", Uptime: 3600}
	if got := hostLine(full); got != "build-1 · darwin 26.0 (arm64) · up 1h 0m" {
		t.Errorf("full host line = %q", got)
	}
	noVersion := &host.InfoStat{Hostname: "build-1", Platform: "darwin", KernelArch: "arm64"}
	if got := hostLine(noVersion); got != "build-1 · darwin (arm64)" {
		t.Errorf("host line without a version = %q", got)
	}
	if got := osLine(&host.InfoStat{}); got != "" {
		t.Errorf("os line of nothing = %q", got)
	}
}
