package tui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// fakeClipboard puts a stand-in for every clipboard program internal/clipboard
// knows about on PATH, and nothing else — the same technique
// internal/clipboard's own tests use, duplicated here rather than exported,
// since exporting a test helper across a package boundary for twenty lines
// buys less than it costs.
func fakeClipboard(t *testing.T) (stdin string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in is a shell script")
	}
	dir := t.TempDir()
	stdin = filepath.Join(dir, "stdin")
	script := "#!/bin/sh\ncat > " + stdin + "\n"
	for _, name := range []string{"pbcopy", "xclip", "xsel", "wl-copy", "clip", "clip.exe", "termux-clipboard-set"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return stdin
}

func genPasswordCap(rows ...[]string) plugin.Capability {
	if len(rows) == 0 {
		rows = [][]string{{"qT8!vN2vX9!fL3jRb@Yz", "94.2"}}
	}
	return plugin.Capability{
		ID: "gen.password", Summary: "generate a password", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Table{
				Columns: []view.Column{{Name: "Password"}, {Name: "Entropy (bits)", Kind: view.KindNumber}},
				Rows:    rows,
				Total:   len(rows),
			}, nil
		},
	}
}

func genTokenCap() plugin.Capability {
	return plugin.Capability{
		ID: "gen.token", Summary: "generate a token", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.KeyValue{Pairs: []view.Pair{
				{Key: "token", Value: "9f86d081884c7d659a2feaa0c55ad015"},
				{Key: "bytes", Value: "16"},
			}}, nil
		},
	}
}

// gen.overview always has more than one row — five recipes, always
// (builtin/gen/sample.go) — so its copySpecs entry can never resolve
// through copyValue, only copyChoices: every "c" against it reaches the
// picker. Column name is "Value", shared by every recipe regardless of
// what it generates, which is what lets one copySpec cover a table mixing
// passwords, keys and a UUID.
func TestGenOverviewsCopySpecMatchesItsActualCompactTableShape(t *testing.T) {
	compact := view.Table{
		Columns: []view.Column{{Name: "For"}, {Name: "Value"}, {Name: "Bits"}},
		Rows: [][]string{
			{"logins", "qT8!vN2vX9!fL3jRb@Yz", "94"},
			{"strength policies", "aB3!xY7#mK1$pQ9&wZ5*fH2@", "156"},
			{"env vars", "9f86d081884c7d659a2feaa0c55ad015", "256"},
			{"32-char field", "n4Kp!7wXq2Rt$5vLm8Zc#3Nb", "156"},
			{"random ids", "550e8400-e29b-41d4-a716-446655440000", "122"},
		},
	}
	spec, ok := copySpecs["gen.overview"]
	if !ok {
		t.Fatal("no copySpecs entry for gen.overview")
	}
	if _, ok := copyValue(spec, compact); ok {
		t.Error("copyValue resolved gen.overview's compact table directly — it should always be ambiguous")
	}
	values, ok := copyChoices(spec, compact)
	if !ok || len(values) != 5 {
		t.Fatalf("copyChoices = %v, %v, want all 5 recipe values", values, ok)
	}
	if values[0] != "qT8!vN2vX9!fL3jRb@Yz" {
		t.Errorf("values[0] = %q, want the first recipe's Value cell", values[0])
	}
}

// copyValue itself: unit-level coverage of the extraction rules, no Model
// or clipboard involved.

func TestCopyValueReadsTheNamedColumnFromASingleRow(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{{Name: "Password"}, {Name: "Entropy (bits)"}},
		Rows:    [][]string{{"s3cr3t!", "42.0"}},
	}
	val, ok := copyValue(copySpec{column: "Password"}, tbl)
	if !ok || val != "s3cr3t!" {
		t.Errorf("copyValue = %q, %v, want \"s3cr3t!\", true", val, ok)
	}
}

func TestCopyValueReadsTheNamedPairFromAKeyValue(t *testing.T) {
	kv := view.KeyValue{Pairs: []view.Pair{{Key: "token", Value: "abc123"}, {Key: "bytes", Value: "16"}}}
	val, ok := copyValue(copySpec{pair: "token"}, kv)
	if !ok || val != "abc123" {
		t.Errorf("copyValue = %q, %v, want \"abc123\", true", val, ok)
	}
}

// The scoping decision documented on copyValue: more than one row has no
// selection UI to pick among, so nothing is copied rather than silently
// always the first.
func TestCopyValueRefusesATableWithMoreThanOneRow(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{{Name: "Password"}},
		Rows:    [][]string{{"first"}, {"second"}},
	}
	if _, ok := copyValue(copySpec{column: "Password"}, tbl); ok {
		t.Error("copyValue picked a row out of an ambiguous multi-row table")
	}
}

func TestCopyValueRefusesAMissingColumnOrPair(t *testing.T) {
	tbl := view.Table{Columns: []view.Column{{Name: "Password"}}, Rows: [][]string{{"x"}}}
	if _, ok := copyValue(copySpec{column: "Nope"}, tbl); ok {
		t.Error("copyValue found a column that is not there")
	}
	kv := view.KeyValue{Pairs: []view.Pair{{Key: "token", Value: "x"}}}
	if _, ok := copyValue(copySpec{pair: "nope"}, kv); ok {
		t.Error("copyValue found a pair that is not there")
	}
}

// The property Redacted exists to give a field: copyValue must fail exactly
// where the on-screen renderer would already have masked the cell, or the
// clipboard sees what the screen deliberately did not show.
func TestCopyValueRefusesARedactedColumnOrPair(t *testing.T) {
	tbl := view.Table{
		Columns:  []view.Column{{Name: "Password"}},
		Rows:     [][]string{{"s3cr3t"}},
		Redacted: []string{"Password"},
	}
	if _, ok := copyValue(copySpec{column: "Password"}, tbl); ok {
		t.Error("copyValue read a column the view marked Redacted")
	}
	kv := view.KeyValue{
		Pairs:    []view.Pair{{Key: "token", Value: "s3cr3t"}},
		Redacted: []string{"token"},
	}
	if _, ok := copyValue(copySpec{pair: "token"}, kv); ok {
		t.Error("copyValue read a pair the view marked Redacted")
	}
}

func TestCopyValueRefusesAViewShapeItHasNoSpecFor(t *testing.T) {
	if _, ok := copyValue(copySpec{column: "Password"}, view.Text{Body: "x"}); ok {
		t.Error("copyValue found a value in a Text view")
	}
	if _, ok := copyValue(copySpec{column: "Password"}, nil); ok {
		t.Error("copyValue found a value in a nil view")
	}
}

// Every capActionSpecs entry keyed "c" and every copySpecs entry name a
// capability that must not appear in the other — capActionSpecs is checked
// first in the Update loop, so a capability declared in both would have its
// copySpecs hint shown (this file) and then silently never fire (dashboard.go).
func TestNoCapabilityDeclaresBothARowCopyActionAndACopySpec(t *testing.T) {
	for id, spec := range capActionSpecs {
		for _, a := range spec {
			if a.key != "c" {
				continue
			}
			if _, clash := copySpecs[id]; clash {
				t.Errorf("%q has both a capActionSpecs %q row action and a copySpecs entry", id, a.key)
			}
		}
	}
}

// Model-level: "c" actually copies through internal/clipboard when pressed.

func TestPressingCCopiesTheGeneratedValue(t *testing.T) {
	stdin := fakeClipboard(t)
	m := New(registry.New(), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	withResult, _ := sized.(Model).Update(resultMsg{
		cap: genPasswordCap(), view: view.Table{
			Columns: []view.Column{{Name: "Password"}, {Name: "Entropy (bits)"}},
			Rows:    [][]string{{"qT8!vN2vX9!fL3jRb@Yz", "94.2"}},
		},
	})

	after, _ := withResult.(Model).Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	nm := after.(Model)

	got, err := os.ReadFile(stdin)
	if err != nil {
		t.Fatalf("nothing reached the clipboard: %v", err)
	}
	if string(got) != "qT8!vN2vX9!fL3jRb@Yz" {
		t.Errorf("clipboard got %q, want the password byte for byte, no JSON wrapping", got)
	}
	if nm.flash == "" {
		t.Error("no flash confirming the copy")
	}
}

func TestPressingCCopiesTheNamedPairFromAKeyValueResult(t *testing.T) {
	stdin := fakeClipboard(t)
	m := New(registry.New(), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	withResult, _ := sized.(Model).Update(resultMsg{
		cap: genTokenCap(), view: view.KeyValue{Pairs: []view.Pair{
			{Key: "token", Value: "9f86d081884c7d659a2feaa0c55ad015"},
			{Key: "bytes", Value: "16"},
		}},
	})

	after, _ := withResult.(Model).Update(tea.KeyPressMsg{Code: 'c', Text: "c"})

	got, err := os.ReadFile(stdin)
	if err != nil {
		t.Fatalf("nothing reached the clipboard: %v", err)
	}
	if string(got) != "9f86d081884c7d659a2feaa0c55ad015" {
		t.Errorf("clipboard got %q, want the token alone, not \"bytes\" too", got)
	}
	if after.(Model).flash == "" {
		t.Error("no flash confirming the copy")
	}
}

// count > 1: no row is selectable, so "c" must not silently grab the first
// generated value and leave the rest unreachable — it opens the picker
// instead (copypick_test.go covers that flow end to end).
func TestPressingCOpensThePickerWhenMultipleValuesWereGenerated(t *testing.T) {
	stdin := fakeClipboard(t)
	m := New(registry.New(), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	withResult, _ := sized.(Model).Update(resultMsg{
		cap: genPasswordCap(), view: view.Table{
			Columns: []view.Column{{Name: "Password"}, {Name: "Entropy (bits)"}},
			Rows:    [][]string{{"first-pw", "94.2"}, {"second-pw", "94.2"}},
		},
	})

	after, _ := withResult.(Model).Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	nm := after.(Model)

	if nm.mode != modeCopyPick {
		t.Errorf("mode = %v, want modeCopyPick", nm.mode)
	}
	if _, err := os.Stat(stdin); err == nil {
		t.Error("opening the picker already reached the clipboard, before anything was chosen")
	}
	if nm.flash != "" {
		t.Errorf("flash = %q, want none yet — nothing has been chosen", nm.flash)
	}
}

// A capability with no copySpecs entry: "c" is inert, not a crash.
func TestPressingCOnACapabilityWithNoCopySpecDoesNothing(t *testing.T) {
	fakeClipboard(t)
	m := New(registry.New(), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	withResult, _ := sized.(Model).Update(resultMsg{
		cap: plugin.Capability{ID: "demo.hello", Safety: plugin.Read},
		view: view.Table{
			Columns: []view.Column{{Name: "Password"}},
			Rows:    [][]string{{"x"}},
		},
	})

	after, cmd := withResult.(Model).Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if cmd != nil {
		t.Error("an unspecced capability issued a clipboard command")
	}
	if after.(Model).flash != "" {
		t.Errorf("flash = %q, want none", after.(Model).flash)
	}
}

// No clipboard program on the machine: the failure is surfaced, not
// swallowed — matching kv.copy's own precedent for this exact condition.
func TestPressingCWithNoClipboardProgramFlashesTheFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH semantics differ")
	}
	t.Setenv("PATH", t.TempDir())
	m := New(registry.New(), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	withResult, _ := sized.(Model).Update(resultMsg{
		cap: genPasswordCap(), view: view.Table{
			Columns: []view.Column{{Name: "Password"}, {Name: "Entropy (bits)"}},
			Rows:    [][]string{{"qT8!vN2vX9!fL3jRb@Yz", "94.2"}},
		},
	})

	after, _ := withResult.(Model).Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	nm := after.(Model)

	if nm.flash == "" || nm.mode != modeResult {
		t.Errorf("flash = %q, mode = %v, want a failure notice and no crash", nm.flash, nm.mode)
	}
}

// The footer hint only appears when there is something to copy — declared
// for gen.password, hidden for count > 1, present again once a real
// registry's gen.token result offers its "token" pair.
func TestResultFooterOffersCopyOnlyWhenThereIsSomethingToCopy(t *testing.T) {
	m := New(registry.New(), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	single, _ := sized.(Model).Update(resultMsg{
		cap: genTokenCap(), view: view.KeyValue{Pairs: []view.Pair{{Key: "token", Value: "abc"}}},
	})
	if got := single.(Model).resultView(); !strings.Contains(got, "copy value") {
		t.Error("footer does not offer copy value for a single addressable value")
	}

	multi, _ := sized.(Model).Update(resultMsg{
		cap: genPasswordCap(), view: view.Table{
			Columns: []view.Column{{Name: "Password"}, {Name: "Entropy (bits)"}},
			Rows:    [][]string{{"first-pw", "94.2"}, {"second-pw", "94.2"}},
		},
	})
	if got := multi.(Model).resultView(); !strings.Contains(got, "copy which value?") {
		t.Error("footer does not offer the picker for a capability with several values")
	}

	withoutSpec, _ := sized.(Model).Update(resultMsg{
		cap: plugin.Capability{ID: "demo.hello", Safety: plugin.Read}, view: view.Text{Body: "x"},
	})
	got := withoutSpec.(Model).resultView()
	if strings.Contains(got, "copy value") || strings.Contains(got, "copy which value?") {
		t.Error("footer offers a copy hint for a capability with no copySpecs entry")
	}
}
