package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/builtin/kv"
	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/pluginhost"
	"github.com/this-is-tobi/rule-them-all/internal/plugintrust"
	"github.com/this-is-tobi/rule-them-all/internal/render/theme"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// writeStore creates a real kv store in the isolated data dir, locked with
// whatever RTA_KV_PASSPHRASE currently says.
func writeStore(t *testing.T) {
	t.Helper()
	for _, c := range kv.Plugin().Capabilities {
		if c.ID != "kv.set" {
			continue
		}
		_, err := c.Run(context.Background(), plugin.NewRequest(
			map[string]any{"key": "token", "value": "s3cr3t"}, false, true))
		if err != nil {
			t.Fatalf("writing the test store: %v", err)
		}
		return
	}
	t.Fatal("kv.set is not registered")
}

// allow issues a grant the way a person would, without going through the
// capability — this is about what doctor reports, not how it was issued.
func allow(t *testing.T, target, scope string) {
	t.Helper()
	now := time.Now()
	if verr := grant.Save([]grant.Grant{{
		Target: target, Scope: scope, Issued: now, Expires: now.Add(15 * time.Minute),
	}}); verr != nil {
		t.Fatalf("issuing a grant: %v", verr)
	}
}

// `rta doctor` is the one command whose job is to tell you the truth about
// this machine — including the line that says whether an AI agent started
// from this shell could read your secrets. It had no test at all, which is
// the wrong place to be relying on nobody making a mistake.
//
// Every test here isolates the data and config directories: doctor reads real
// user state by design, and a test that reads the developer's own store would
// pass or fail on what that laptop happens to own.

func isolate(t *testing.T) (dataDir, configDir string) {
	t.Helper()
	dataDir, configDir = t.TempDir(), t.TempDir()
	t.Setenv("RTA_DATA_DIR", dataDir)
	t.Setenv("RTA_CONFIG", filepath.Join(configDir, "config.yaml"))
	t.Setenv("RTA_KV_IDENTITY", "")
	t.Setenv("RTA_KV_PASSPHRASE", "")
	return dataDir, configDir
}

// report returns doctor's rows keyed by check name.
func report(t *testing.T) map[string][2]string {
	t.Helper()
	v := doctorReport(testRegistry(t))
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("doctor returned %s, want a table", view.TypeOf(v))
	}
	rows := map[string][2]string{}
	for _, r := range tbl.Rows {
		if len(r) != 3 {
			t.Fatalf("row %v: want check, status, detail", r)
		}
		rows[r[0]] = [2]string{r[1], r[2]}
	}
	if tbl.Total != len(tbl.Rows) {
		t.Errorf("Total = %d, rows = %d", tbl.Total, len(tbl.Rows))
	}
	return rows
}

func check(t *testing.T, rows map[string][2]string, name, wantStatus string, wantIn string) {
	t.Helper()
	got, ok := rows[name]
	if !ok {
		t.Fatalf("no %q row; got %v", name, rows)
	}
	if got[0] != wantStatus {
		t.Errorf("%s status = %q, want %q (detail %q)", name, got[0], wantStatus, got[1])
	}
	if wantIn != "" && !strings.Contains(got[1], wantIn) {
		t.Errorf("%s detail = %q, want it to mention %q", name, got[1], wantIn)
	}
}

func TestDoctorReportsAHealthyEmptyMachine(t *testing.T) {
	isolate(t)
	rows := report(t)
	check(t, rows, "capabilities", "ok", "capabilities")
	check(t, rows, "config", "info", "zero-config")
	check(t, rows, "agent grants", "ok", "none active")
	check(t, rows, "kv store", "ok", "none yet")
}

// The three kv answers are the point of the check, and they are genuinely
// different: no store, a store this shell can open, and a store whose key is
// here but locked. Saying "no key material" for the third would be a
// comforting lie.
func TestDoctorDistinguishesTheThreeKvPostures(t *testing.T) {
	t.Run("passphrase in the environment", func(t *testing.T) {
		isolate(t)
		t.Setenv("RTA_KV_PASSPHRASE", "correct horse")
		writeStore(t)
		check(t, report(t), "kv store", "info", "unlocks from this environment")
	})

	t.Run("no key material here", func(t *testing.T) {
		isolate(t)
		t.Setenv("RTA_KV_PASSPHRASE", "correct horse")
		writeStore(t)
		t.Setenv("RTA_KV_PASSPHRASE", "")
		check(t, report(t), "kv store", "ok", "could not open it")
	})
}

// A grant issued and forgotten is exactly what a health check is for.
func TestDoctorNamesActiveGrants(t *testing.T) {
	isolate(t)
	allow(t, "kv.get", "db-password")
	rows := report(t)
	check(t, rows, "agent grants", "info", "kv.get db-password")
	if !strings.Contains(rows["agent grants"][1], "1 active") {
		t.Errorf("detail = %q, want the count", rows["agent grants"][1])
	}
}

// Both sides of a credential grant, because a report that only names what was
// withheld is not a report of what this machine permits.
//
// The ungranted row is the one that saves a debugging session: a plugin whose
// every call needs a kubeconfig fails with its own tool's "operation not
// permitted", which reads as a broken install. The granted row is the one that
// makes the permission auditable — rta's whole argument is that an
// authorisation should be something you can point at, and one that shows up
// only in the subcommand that issued it is not.
func TestDoctorNamesACredentialLocationGrantedAndWithheld(t *testing.T) {
	asks := func(digest string) *pluginhost.Client {
		return &pluginhost.Client{
			Identity: pluginhost.Identity{Path: "/somewhere/rta-plugin-" + digest[:4], Digest: digest},
			Declared: plugin.Plugin{
				Name: "lab" + digest[:4], Summary: "a lab plugin",
				Capabilities: []plugin.Capability{{ID: "lab" + digest[:4] + ".get", Safety: plugin.Read}},
				Needs:        []plugin.Need{plugin.NeedKubeconfig},
			},
		}
	}
	withheld, granted := strings.Repeat("a", 64), strings.Repeat("b", 64)

	isolate(t)
	for _, d := range []string{withheld, granted} {
		if verr := plugintrust.Add(d, "lab"+d[:4], "/somewhere/rta-plugin-"+d[:4]); verr != nil {
			t.Fatal(verr)
		}
	}
	if verr := plugintrust.Allow(granted, []string{string(plugin.NeedKubeconfig)}); verr != nil {
		t.Fatal(verr)
	}
	SetLoadedPlugins([]*pluginhost.Client{asks(withheld), asks(granted)})
	t.Cleanup(func() { SetLoadedPlugins(nil) })

	rows := report(t)
	check(t, rows, "plugin lab"+withheld[:4], "warn", "has not been allowed to")
	check(t, rows, "plugin lab"+granted[:4], "ok", "allowed to read kubeconfig")
	if !strings.Contains(rows["plugin lab"+granted[:4]][1], "disallow") {
		t.Errorf("the granted row does not say how to take it back: %q",
			rows["plugin lab"+granted[:4]][1])
	}
}

// An unreadable config is the actionable case: zero-config is fine, a broken
// file is not, and doctor must not present the second as the first.
func TestDoctorFlagsABrokenConfig(t *testing.T) {
	_, configDir := isolate(t)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("output: [not a string\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check(t, report(t), "config", "error", "")
}

// Corrupt state must be reported, not panicked over: the grants file is
// plaintext JSON a person can edit, so a person can break it.
func TestDoctorSurvivesACorruptGrantsFile(t *testing.T) {
	dataDir, _ := isolate(t)
	if err := os.WriteFile(filepath.Join(dataDir, "grants.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	check(t, report(t), "agent grants", "error", "")
}

// The theme row is SetThemeProblems' own reason to exist: theme.Apply
// already fell back to the built-in for a bad field before this is ever
// reached, so the row is not "did the color apply" — it always does — it is
// "why is the color on screen not the one written down", which nothing else
// says.
func TestDoctorReportsAThemeProblem(t *testing.T) {
	isolate(t)
	t.Cleanup(func() { SetThemeProblems(nil) })
	SetThemeProblems([]theme.Problem{{
		Field: "primary", Reason: `"not-a-color" is not a color`, Hint: "the form is #rrggbb",
	}})
	check(t, report(t), "theme", "warn", "primary")
}

// And nothing configured earns no row at all — the same quiet default every
// other doctor check gives a healthy machine.
func TestDoctorSaysNothingAboutThemeWhenNothingWasOverridden(t *testing.T) {
	isolate(t)
	t.Cleanup(func() { SetThemeProblems(nil) })
	SetThemeProblems(nil)
	if rows := report(t); rows["theme"] != [2]string{} {
		t.Errorf("theme row = %v, want none", rows["theme"])
	}
}
