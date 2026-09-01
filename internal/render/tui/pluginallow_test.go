package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/plugintrust"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

const allowPath = "/usr/local/bin/rta-plugin-weather"

// needyRow is an external, approved plugin declaring two locations.
func needyRow(needs ...plugin.Need) pluginRow {
	return pluginRow{
		plugin: plugin.Plugin{Name: "weather", Summary: "weather summary", Needs: needs},
		origin: registry.Origin{Path: allowPath, Digest: untrustedDigest},
	}
}

func trustWeather(t *testing.T) {
	t.Helper()
	if verr := plugintrust.Add(untrustedDigest, "weather", allowPath); verr != nil {
		t.Fatal(verr)
	}
}

// The form opens showing the grant as it stands, not an empty one.
//
// Allow replaces rather than accumulates, so a form that opened blank would
// make every visit a rebuild-from-memory — and submitting it without noticing
// would silently withdraw everything already granted. The seed is what makes
// "replaces" safe to expose as a screen.
func TestTheAllowFormOpensSeededWithTheCurrentGrant(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	trustWeather(t)
	if verr := plugintrust.Allow(untrustedDigest, []string{"kubeconfig"}); verr != nil {
		t.Fatal(verr)
	}

	fields := allowFields(needyRow("kubeconfig", "ssh"),
		plugintrust.Load().Allowed(untrustedDigest))
	if len(fields) != 2 {
		t.Fatalf("fields = %d, want one per declared need", len(fields))
	}
	seen := map[string]any{}
	for _, f := range fields {
		if f.Type != plugin.Bool {
			t.Errorf("%s is %v, want a checkbox", f.Name, f.Type)
		}
		seen[f.Name] = f.Default
	}
	if seen["kubeconfig"] != true {
		t.Errorf("kubeconfig seeded %v, want it checked — it is already allowed", seen["kubeconfig"])
	}
	if seen["ssh"] != false {
		t.Errorf("ssh seeded %v, want it unchecked", seen["ssh"])
	}
}

// Submitting the form writes exactly the checked set, because Allow states the
// whole grant. That is also what makes withdrawal expressible without a second
// control: clearing a box is the disallow.
func TestSubmittingTheAllowFormWritesTheWholeGrant(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	trustWeather(t)
	if verr := plugintrust.Allow(untrustedDigest, []string{"kubeconfig", "ssh"}); verr != nil {
		t.Fatal(verr)
	}

	m := New(registry.New(), config.Dashboard{}, nil)
	next, _ := m.startAllowForm(needyRow("kubeconfig", "ssh"))
	nm := next.(Model)
	if nm.form == nil {
		t.Fatalf("no form opened: %q", nm.flash)
	}
	// Take ssh away, leave kubeconfig.
	*nm.form.bools["ssh"] = false

	next, _ = nm.saveAllowForm()
	done := next.(Model)

	got := plugintrust.Load().Allowed(untrustedDigest)
	if !slices.Equal(got, []string{"kubeconfig"}) {
		t.Errorf("allowed = %v, want only [kubeconfig] — the form states the whole grant", got)
	}
	// The flash names the resulting set, not a delta: Allow replaced, so
	// "removed ssh" would describe an operation that did not happen.
	if !strings.Contains(done.flash, "kubeconfig") || strings.Contains(done.flash, "ssh") {
		t.Errorf("flash = %q, want the resulting grant stated in full", done.flash)
	}
}

// Clearing every box is the withdrawal, and it has to say what it does not do
// — the plugin is already running in this process.
func TestClearingEveryBoxWithdrawsAndSaysWhatThatDoesNotDo(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	trustWeather(t)
	if verr := plugintrust.Allow(untrustedDigest, []string{"kubeconfig"}); verr != nil {
		t.Fatal(verr)
	}

	m := New(registry.New(), config.Dashboard{}, nil)
	next, _ := m.startAllowForm(needyRow("kubeconfig"))
	nm := next.(Model)
	*nm.form.bools["kubeconfig"] = false
	next, _ = nm.saveAllowForm()

	if got := plugintrust.Load().Allowed(untrustedDigest); len(got) != 0 {
		t.Errorf("allowed = %v, want nothing", got)
	}
	if flash := next.(Model).flash; !strings.Contains(flash, "until rta exits") {
		t.Errorf("flash = %q, want it to say the running process still has it", flash)
	}
}

// Three refusals, each naming the first thing actually in the way rather than
// opening a form that cannot succeed. The untrusted case matters most:
// plugintrust.Allow refuses it too, but its error would arrive only after
// somebody had filled the form in, and the fix is a different key on the same
// row.
func TestTheAllowFormRefusesWhereThereIsNothingToAllow(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	m := New(registry.New(), config.Dashboard{}, nil)

	cases := []struct {
		name string
		row  pluginRow
		want string
	}{
		{
			name: "built in",
			row:  pluginRow{plugin: plugin.Plugin{Name: "kv", Needs: []plugin.Need{"kubeconfig"}}},
			want: "built into rta",
		},
		{
			name: "not approved to run yet",
			row: pluginRow{
				plugin:  plugin.Plugin{Name: "weather", Needs: []plugin.Need{"kubeconfig"}},
				origin:  registry.Origin{Path: allowPath, Digest: untrustedDigest},
				waiting: true,
			},
			want: "press t first",
		},
		{
			name: "asks for nothing",
			row:  needyRow(),
			want: "does not ask to read",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			next, _ := m.startAllowForm(c.row)
			nm := next.(Model)
			if nm.form != nil {
				t.Fatal("a form opened where there was nothing to allow")
			}
			if !strings.Contains(nm.flash, c.want) {
				t.Errorf("flash = %q, want it to mention %q", nm.flash, c.want)
			}
		})
	}
}

// The pane reads the trust file once per build, so a permission change made
// from inside it leaves the rows stale — and a screen still showing the old
// answer immediately after a permission change is the worst possible moment to
// be out of date.
func TestThePaneRefreshesAfterAGrantChanges(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	trustWeather(t)

	m := paneWithNeeds(t, plugin.Need("kubeconfig"))
	if len(m.plugins[0].ungranted) != 1 {
		t.Fatalf("fixture should start ungranted: %+v", m.plugins[0])
	}

	next, _ := m.startAllowForm(m.plugins[0])
	nm := next.(Model)
	if nm.form == nil {
		t.Fatalf("no form opened: %q", nm.flash)
	}
	*nm.form.bools["kubeconfig"] = true
	next, _ = nm.saveAllowForm()
	done := next.(Model)

	if len(done.plugins) == 0 {
		t.Fatal("the pane lost its rows")
	}
	if len(done.plugins[0].granted) != 1 || len(done.plugins[0].ungranted) != 0 {
		t.Errorf("row still shows granted=%v ungranted=%v after the grant was written",
			done.plugins[0].granted, done.plugins[0].ungranted)
	}
}
