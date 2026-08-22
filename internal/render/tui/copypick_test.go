package tui

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// copyChoices itself: unit-level coverage, no Model or clipboard involved.
// Its refusal conditions mirror copyValue's own (copyvalue_test.go) — same
// spec, same view shapes — so only what actually differs is covered here:
// row count no longer matters, and order is preserved.

func TestCopyChoicesListsEveryRowsValueInOrder(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{{Name: "Password"}, {Name: "Entropy (bits)"}},
		Rows:    [][]string{{"first", "1"}, {"second", "2"}, {"third", "3"}},
	}
	values, ok := copyChoices(copySpec{column: "Password"}, tbl)
	want := []string{"first", "second", "third"}
	if !ok || len(values) != len(want) {
		t.Fatalf("copyChoices = %v, %v, want %v, true", values, ok, want)
	}
	for i, w := range want {
		if values[i] != w {
			t.Errorf("values[%d] = %q, want %q", i, values[i], w)
		}
	}
}

func TestCopyChoicesRefusesTheSameShapesCopyValueDoes(t *testing.T) {
	if _, ok := copyChoices(copySpec{column: "Password"}, view.Text{Body: "x"}); ok {
		t.Error("copyChoices found values in a Text view")
	}
	if _, ok := copyChoices(copySpec{column: "Nope"}, view.Table{Rows: [][]string{{"x"}, {"y"}}}); ok {
		t.Error("copyChoices found a column that is not there")
	}
	redacted := view.Table{
		Columns:  []view.Column{{Name: "Password"}},
		Rows:     [][]string{{"a"}, {"b"}},
		Redacted: []string{"Password"},
	}
	if _, ok := copyChoices(copySpec{column: "Password"}, redacted); ok {
		t.Error("copyChoices read a column the view marked Redacted")
	}
	if _, ok := copyChoices(copySpec{column: "Password"}, view.Table{Columns: []view.Column{{Name: "Password"}}}); ok {
		t.Error("copyChoices found values in a table with no rows")
	}
}

// A ragged row shorter than the column list offers nothing for that column
// rather than aborting every other row's chance — the same leniency
// view.Redact's own masking loop already extends for the same reason.
func TestCopyChoicesSkipsARowTooShortToNameTheColumn(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{{Name: "Password"}, {Name: "Extra"}},
		Rows:    [][]string{{"full", "x"}, {"short"}},
	}
	values, ok := copyChoices(copySpec{column: "Password"}, tbl)
	if !ok || len(values) != 2 || values[0] != "full" || values[1] != "short" {
		t.Errorf("values = %v, ok = %v, want [full short], true", values, ok)
	}
}

// Model-level: the picker actually opens, actually copies, actually cancels.

func openPickerOnThreePasswords(t *testing.T) Model {
	t.Helper()
	m := New(registry.New(), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	withResult, _ := sized.(Model).Update(resultMsg{
		cap: genPasswordCap(), view: view.Table{
			Columns: []view.Column{{Name: "Password"}, {Name: "Entropy (bits)"}},
			Rows:    [][]string{{"first-pw", "94.2"}, {"second-pw", "94.2"}, {"third-pw", "94.2"}},
		},
	})
	opened, _ := withResult.(Model).Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	om, ok := opened.(Model)
	if !ok || om.mode != modeCopyPick {
		t.Fatalf("did not open the picker: mode = %v", om.mode)
	}
	return om
}

// The form seeds its bound value to the first option immediately on
// construction (huh's own doing, before any key is pressed) — confirming
// that copies the first row's raw value, not its "1: " display label.
func TestOpeningThePickerSeedsTheFirstRowsRawValue(t *testing.T) {
	om := openPickerOnThreePasswords(t)
	if got := om.copyPick.value; got != "first-pw" {
		t.Errorf("seeded value = %q, want the first row's raw value, no \"1: \" label", got)
	}
}

// confirmCopyPick copies whichever value the form landed on — exercised
// directly, the same way saveTheme/saveConfigForm's own tests set a
// binding and call the save method rather than simulate huh's internal
// keys for reaching that state.
func TestConfirmingThePickerCopiesTheChosenRawValue(t *testing.T) {
	stdin := fakeClipboard(t)
	om := openPickerOnThreePasswords(t)
	om.copyPick.value = "second-pw" // as if the operator had moved the highlight here

	confirmed, _ := om.confirmCopyPick()
	cm, ok := confirmed.(Model)
	if !ok {
		t.Fatal("confirmCopyPick did not return a Model")
	}

	if cm.mode != modeResult {
		t.Errorf("mode = %v, want modeResult once a value is chosen", cm.mode)
	}
	if cm.copyPick != nil {
		t.Error("the picker form is still attached after confirming")
	}
	got, err := os.ReadFile(stdin)
	if err != nil {
		t.Fatalf("nothing reached the clipboard: %v", err)
	}
	if string(got) != "second-pw" {
		t.Errorf("clipboard got %q, want the chosen value, not the first row's", got)
	}
	if cm.flash == "" {
		t.Error("no flash confirming the copy")
	}
}

// The same clipboard failure kv.copy and the single-value "c" path both
// surface (no clipboard program on the machine) must not be swallowed here
// either.
func TestConfirmingThePickerWithNoClipboardProgramFlashesTheFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH semantics differ")
	}
	om := openPickerOnThreePasswords(t)
	t.Setenv("PATH", t.TempDir())

	confirmed, _ := om.confirmCopyPick()
	cm, ok := confirmed.(Model)
	if !ok {
		t.Fatal("confirmCopyPick did not return a Model")
	}
	if cm.flash == "" || cm.mode != modeResult {
		t.Errorf("flash = %q, mode = %v, want a failure notice and no crash", cm.flash, cm.mode)
	}
}

// esc cancels the picker without copying anything — the same universal
// "esc goes back" property every other form-shaped screen has
// (keys_test.go's TestEscapeGoesBackFromEveryScreenThatHasABack covers this
// one too).
func TestEscapingThePickerCopiesNothing(t *testing.T) {
	stdin := fakeClipboard(t)
	om := openPickerOnThreePasswords(t)

	after, _ := om.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	am, ok := after.(Model)
	if !ok {
		t.Fatal("Update did not return a Model")
	}

	if am.mode != modeResult {
		t.Errorf("mode = %v, want modeResult after cancelling", am.mode)
	}
	if am.copyPick != nil {
		t.Error("the picker form is still attached after cancelling")
	}
	if _, err := os.Stat(stdin); err == nil {
		t.Error("cancelling the picker still reached the clipboard")
	}
	if am.flash != "" {
		t.Errorf("flash = %q, want none — nothing was chosen", am.flash)
	}
}

// Resizing while the picker is open must not panic — fitCopyPick is the
// same shape as fitForm/fitThemeForm, and both are covered for exactly
// this at their own call sites.
func TestResizingWhileThePickerIsOpenDoesNotPanic(t *testing.T) {
	om := openPickerOnThreePasswords(t)
	_, _ = om.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
}

// End to end, through the real bubbletea program loop rather than a bare
// Model.Update call: confirmCopyPick's own tests above prove the copy logic
// is correct once the form reaches huh.StateCompleted, but not that a
// single real Enter press actually gets it there. It does — huh seeds a
// Select's bound value to its first option before any key is pressed, and
// with one field in the group that same Enter both confirms and submits.
func TestPressingCThenEnterCopiesTheDefaultChoice(t *testing.T) {
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "gen", Summary: "gen",
		Capabilities: []plugin.Capability{
			{
				ID: "gen.password", Summary: "generate a password", Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Table{
						Columns: []view.Column{{Name: "Password"}},
						Rows:    [][]string{{"first-pw"}, {"second-pw"}, {"third-pw"}},
					}, nil
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	tm := teatest.NewTestModel(t, New(reg, config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "gen.password")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "first-pw")

	tm.Send(tea.KeyPressMsg{Code: 'c', Text: "c"})
	waitFor(t, tm, "copy which value?")

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "copied value") // back in modeResult, flash visible

	quit(t, tm)
}

// Dashboard tile copy: "c" against a tile's own preview, without opening
// it — gen.overview is the real capability this exists for.

func tileIndex(t *testing.T, m Model, capID string) int {
	t.Helper()
	for i, tl := range m.tiles {
		if tl.cap.ID == capID {
			return i
		}
	}
	t.Fatalf("no tile for %q", capID)
	return -1
}

func TestPressingCOnATileOpensThePickerAgainstItsOwnPreview(t *testing.T) {
	base, _ := realModel(t, 120, 40)
	i := tileIndex(t, base, "gen.overview")
	base.selected = i
	base.tiles[i].view = view.Table{
		Columns: []view.Column{{Name: "For"}, {Name: "Value"}, {Name: "Bits"}},
		Rows: [][]string{
			{"logins", "qT8!vN2vX9!fL3jRb@Yz", "94"},
			{"env vars", "9f86d081884c7d659a2feaa0c55ad015", "256"},
		},
	}

	pick := press(t, base, "c")
	if pick.mode != modeCopyPick {
		t.Fatalf("c on the tile did not open the picker: mode = %v", pick.mode)
	}
	if pick.copyPick.cap.ID != "gen.overview" {
		t.Errorf("picker cap = %q, want gen.overview", pick.copyPick.cap.ID)
	}
	if pick.copyPick.returnTo != modeDashboard {
		t.Errorf("returnTo = %v, want modeDashboard", pick.copyPick.returnTo)
	}
}

// Confirming a tile-opened picker goes back to the dashboard, not
// modeResult — and restarts tile refresh the same way every other screen
// returning to the dashboard does (closeToOrigin's own property, reused
// here through closeCopyPick).
func TestConfirmingATilePickerReturnsToTheDashboardAndRestartsRefresh(t *testing.T) {
	stdin := fakeClipboard(t)
	base, _ := realModel(t, 120, 40)
	i := tileIndex(t, base, "gen.overview")
	base.selected = i
	base.tiles[i].view = view.Table{
		Columns: []view.Column{{Name: "For"}, {Name: "Value"}, {Name: "Bits"}},
		Rows:    [][]string{{"logins", "first-value", "94"}, {"env vars", "second-value", "256"}},
	}
	beforeGen := base.tickGen

	pick := press(t, base, "c")
	if pick.mode != modeCopyPick {
		t.Fatalf("did not open the picker: mode = %v", pick.mode)
	}
	confirmed, _ := pick.confirmCopyPick()
	cm, ok := confirmed.(Model)
	if !ok {
		t.Fatal("confirmCopyPick did not return a Model")
	}

	if cm.mode != modeDashboard {
		t.Errorf("mode = %v, want modeDashboard", cm.mode)
	}
	if cm.tickGen == beforeGen {
		t.Error("tile refresh was not restarted on return to the dashboard")
	}
	got, err := os.ReadFile(stdin)
	if err != nil {
		t.Fatalf("nothing reached the clipboard: %v", err)
	}
	if string(got) != "first-value" {
		t.Errorf("clipboard got %q, want the first recipe's value", got)
	}
}

// A tile with no copySpecs entry: "c" is inert, not a crash — and does not
// fall through to selectedAction's own key matching for a capability that
// happens to declare something else under "c".
func TestPressingCOnATileWithNoCopySpecDoesNothing(t *testing.T) {
	base, _ := realModel(t, 120, 40)
	i := tileIndex(t, base, "sys.overview")
	base.selected = i

	after := press(t, base, "c")
	if after.mode != modeDashboard {
		t.Errorf("mode = %v, want modeDashboard unchanged", after.mode)
	}
	if after.copyPick != nil {
		t.Error("a picker opened for a tile with no copySpecs entry")
	}
}

// The dashboard footer only offers the copy hint when the selected tile
// actually has something to copy.
func TestDashFooterOffersCopyOnlyWhenTheSelectedTileHasSomethingToCopy(t *testing.T) {
	base, _ := realModel(t, 120, 40)
	i := tileIndex(t, base, "gen.overview")
	base.selected = i
	base.tiles[i].view = view.Table{
		Columns: []view.Column{{Name: "For"}, {Name: "Value"}, {Name: "Bits"}},
		Rows:    [][]string{{"logins", "a-value", "94"}, {"env vars", "b-value", "256"}},
	}
	if got := base.dashFooter(); !strings.Contains(plain(got), "copy which value?") {
		t.Error("footer does not offer the picker for the gen tile")
	}

	j := tileIndex(t, base, "sys.overview")
	base.selected = j
	if got := base.dashFooter(); strings.Contains(plain(got), "copy value") || strings.Contains(plain(got), "copy which value?") {
		t.Error("footer offers a copy hint for a tile with no copySpecs entry")
	}
}

// End to end, through the real bubbletea program loop: land on the gen
// tile from a fresh dashboard, copy straight off its preview without
// opening it, confirm the default choice, land back on the dashboard.
func TestPressingCOnTheRealGenTileEndToEnd(t *testing.T) {
	tm := teatest.NewTestModel(t, New(mustRegistry(t), config.Dashboard{}, nil), teatest.WithInitialTermSize(120, 40))
	waitFor(t, tm, "gen.overview")
	// Move onto the gen tile: right along the bottom-right area of the
	// grid a few times is more robust than counting exact columns, since
	// arrange.go's own layout decides span per tile. moveSelection clamps
	// rather than wraps, so this overshoots onto the last tile (git.overview,
	// alphabetically after gen among the unranked plugins) and one left
	// lands back on gen — still not a hardcoded column, just one fixed point
	// short of the same clamp.
	for range 12 {
		tm.Send(tea.KeyPressMsg{Code: tea.KeyRight})
	}
	tm.Send(tea.KeyPressMsg{Code: tea.KeyLeft})
	tm.Send(tea.KeyPressMsg{Code: 'c', Text: "c"})
	waitFor(t, tm, "copy which value?")

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "copied value")

	quit(t, tm)
}

func mustRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	_, reg := realModel(t, 120, 40)
	return reg
}
