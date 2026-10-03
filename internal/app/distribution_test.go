package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The install report's last line told the person where the plugin's settings
// go as "plugins.pg@abc123: — `rta explain pg.query` lists its keys": a key
// with a dash after it, which reads as a line of config that stopped.
func TestTheInstallReportSaysWhereTheKeysGoInASentence(t *testing.T) {
	rep := plugindist.Report{
		Name: "hello", Version: "v0.1.0", Index: "local", URL: "file:///x",
		Digest: "6db7eaeebf84" + strings.Repeat("0", 52), Signature: "none stated", Path: "/store/hello",
		Declared: plugin.Plugin{Name: "hello", Capabilities: []plugin.Capability{{ID: "hello.greet", Safety: plugin.Read,
			Inputs: []plugin.Field{{Name: "greeting", Type: plugin.String, Config: "greeting"}}}}},
	}
	kv, ok := installView(rep, false).(view.KeyValue)
	if !ok {
		t.Fatal("the install report is not a key/value page")
	}
	var note string
	for _, p := range kv.Pairs {
		if p.Key == "to configure it" {
			note = p.Value
		}
	}
	if note == "" || strings.Contains(note, ":` —") || strings.Contains(note, ": —") ||
		!strings.Contains(note, "go under `plugins.hello@6db7eaeebf84:`") || !strings.Contains(note, "`rta explain hello.greet`") {
		t.Errorf("to configure it: %q", note)
	}
}

// A plugin that reads nothing from a config is not told to configure itself: the
// report said where "its keys go" for a plugin with none, a heading over
// nothing, where the next step for it is to see how it is called.
func TestTheInstallReportDoesNotSendAPluginWithNoKeysToTheConfig(t *testing.T) {
	rep := plugindist.Report{
		Name: "hello", Version: "v0.1.0", Index: "local", URL: "file:///x",
		Digest: "6db7eaeebf84" + strings.Repeat("0", 52), Signature: "none stated", Path: "/store/hello",
		Declared: plugin.Plugin{Name: "hello", Capabilities: []plugin.Capability{{ID: "hello.greet", Safety: plugin.Read,
			Inputs: []plugin.Field{{Name: "name", Type: plugin.String, Required: true}}}}},
	}
	kv, ok := installView(rep, false).(view.KeyValue)
	if !ok {
		t.Fatal("the install report is not a key/value page")
	}
	got := map[string]string{}
	for _, p := range kv.Pairs {
		got[p.Key] = p.Value
	}
	if _, configure := got["to configure it"]; configure {
		t.Errorf("a plugin with no config keys was sent to the config: %q", got["to configure it"])
	}
	if !strings.Contains(got["to use it"], "`rta explain hello.greet`") {
		t.Errorf("to use it: %q", got["to use it"])
	}
}

// "Up to date" is a verdict on the index as it was last updated, and an upgrade
// never fetches the index: it said so with no mention of the one command that
// would change the answer.
func TestUpToDateNamesTheIndexUpdateThatCouldChangeIt(t *testing.T) {
	got := upToDateValue(plugindist.Upgraded{
		Report:     plugindist.Report{Name: "hello", Version: "v0.1.0"},
		FromDigest: "6db7eaeebf84" + strings.Repeat("0", 52),
	})
	for _, want := range []string{"hello v0.1.0 (6db7eaeebf84)", "as last updated", "`rta plugin index update`"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not contain %q", got, want)
		}
	}
}

// An upgrade says what it did to the config that pinned the old build, and only
// that. It said "your pin plugins.pg@abc no longer applies" on every upgrade,
// whether or not the config named the old build — and never named `rta profile
// repin`, which moves every profile entry in one command and is the thing the
// person with forty of them needs.
func TestAnUpgradeNamesWhatTheConfigPinnedAndHowToMoveIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("RTA_CONFIG", path)
	old, fresh := "6db7eaeebf84"+strings.Repeat("0", 52), "099d72c38cfc"+strings.Repeat("1", 52)

	pairs := func(dryRun bool) map[string]string {
		out := map[string]string{}
		for _, p := range stalePinPairs("hello", old, fresh, dryRun) {
			out[p.Key] = p.Value
		}
		return out
	}

	// Nothing in the config names the build: nothing to say.
	if got := pairs(false); len(got) != 0 {
		t.Errorf("a config that pins nothing was told about pins: %v", got)
	}

	body := "profiles:\n  staging:\n    plugins:\n      hello@6db7eaeebf84:\n        set:\n          url: https://a.example\n" +
		"  other:\n    plugins:\n      hello@ffffffffffff:\n        set:\n          url: https://b.example\n" +
		"plugins:\n  hello@6db7eaeebf84:\n    url: https://c.example\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := pairs(false)
	if v := got["your profiles"]; !strings.Contains(v, "1 profile (staging)") || strings.Contains(v, "other") ||
		!strings.Contains(v, "`rta profile repin --all --plugin hello`") || !strings.Contains(v, "hello@099d72c38cfc") {
		t.Errorf("profiles row = %q", v)
	}
	if v := got["your pin"]; !strings.Contains(v, "plugins.hello@6db7eaeebf84 no longer applies") {
		t.Errorf("pin row = %q", v)
	}
	if v := pairs(true)["your profiles"]; !strings.Contains(v, "will refuse after the upgrade") {
		t.Errorf("a dry run speaks of what has already happened: %q", v)
	}
}

// `--platform` is where an author says the one thing a binary cannot say
// about itself, so its grammar is the one place a typo turns into a manifest
// nobody can install from. Every refusal here is one caught before the file
// exists.
func TestAPlatformSpecIsReadOrRefused(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "rta-plugin-lab.tar.gz")
	if err := os.WriteFile(artifact, []byte("bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	src, verr := parsePlatformSpec("linux/amd64=https://example.com/lab.tar.gz", "")
	if verr != nil {
		t.Fatalf("a published artifact was refused: %v", verr)
	}
	if src.OS != "linux" || src.Arch != "amd64" || src.URL != "https://example.com/lab.tar.gz" {
		t.Fatalf("src = %+v", src)
	}

	// A value with no scheme is a file here, and it becomes the absolute
	// file:// URL a manifest requires — a relative one is refused by the
	// manifest grammar, so leaving it alone would push the failure one step
	// further from the person who can fix it.
	src, verr = parsePlatformSpec("darwin/arm64="+artifact, "inner/rta-plugin-lab")
	if verr != nil {
		t.Fatalf("a local artifact was refused: %v", verr)
	}
	if !strings.HasPrefix(src.URL, "file:///") || !strings.HasSuffix(src.URL, "rta-plugin-lab.tar.gz") {
		t.Fatalf("URL = %q, want an absolute file URL", src.URL)
	}
	if src.Bin != "inner/rta-plugin-lab" {
		t.Fatalf("bin = %q", src.Bin)
	}

	for _, bad := range []struct{ spec, says string }{
		{"linux/amd64", "states no artifact"},
		{"linux/amd64=", "states no artifact"},
		{"=https://example.com/lab", "does not start with <os>/<arch>"},
		{"amd64=https://example.com/lab", "does not start with <os>/<arch>"},
		{"/amd64=https://example.com/lab", "does not start with <os>/<arch>"},
		{"linux/=https://example.com/lab", "does not start with <os>/<arch>"},
	} {
		if _, verr := parsePlatformSpec(bad.spec, ""); verr == nil {
			t.Errorf("%q was accepted", bad.spec)
		} else if !strings.Contains(verr.Message, bad.says) {
			t.Errorf("%q: %q, want it to say %q", bad.spec, verr.Message, bad.says)
		}
	}

	// A path that is not there is the likeliest mistake of all — a typo, or a
	// build that did not run — and it must not become a manifest claiming an
	// artifact nobody can fetch.
	_, verr = parsePlatformSpec("linux/amd64="+filepath.Join(dir, "never-built"), "")
	if verr == nil {
		t.Fatal("a platform pointing at nothing was accepted")
	}
	if !strings.Contains(verr.Hint, "https:// URL") {
		t.Fatalf("hint = %q, want it to name the published case", verr.Hint)
	}
}

// --checksums is read no further than a checksums file may be. It was read
// whole before its size was looked at, so a path naming something else, a
// release archive given in the wrong flag or a device, cost its whole size in
// memory to be refused, and one that never ends was read until memory ran
// out: /dev/zero took seven gigabytes in eight seconds and never finished.
func TestAChecksumsFileIsRefusedPastTheCapWithoutBeingRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checksums.txt")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(32 << 20); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, verr := readChecksumsFile(path)
	runtime.ReadMemStats(&after)
	if verr == nil || verr.Code != "plugin.manifest.checksums" {
		t.Fatalf("a checksums file of 32 MiB = %v, want plugin.manifest.checksums", verr)
	}
	if taken := after.TotalAlloc - before.TotalAlloc; taken > 8<<20 {
		t.Errorf("refusing a 32 MiB checksums file took %d bytes", taken)
	}
	if _, verr := readChecksumsFile(filepath.Join(t.TempDir(), "missing")); verr == nil ||
		verr.Code != "plugin.manifest.checksums" {
		t.Errorf("a missing checksums file = %v, want plugin.manifest.checksums", verr)
	}
}
