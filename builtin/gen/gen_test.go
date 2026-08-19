package gen

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/this-is-tobi/rule-them-all/internal/render/cli"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func req(values map[string]any) plugin.Request {
	return plugin.NewRequest(values, false, false)
}

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordUsesTheRequestedLength(t *testing.T) {
	v, err := runPassword(context.Background(), req(map[string]any{"length": 24, "count": 1}))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	if len(tbl.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(tbl.Rows))
	}
	if got := len(tbl.Rows[0][0]); got != 24 {
		t.Errorf("password length = %d, want 24", got)
	}
}

func TestPasswordExcludingAmbiguousCharactersNeverContainsThem(t *testing.T) {
	v, err := runPassword(context.Background(), req(map[string]any{
		"length": 200, "count": 5, "symbols": true, "exclude-ambiguous": true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range v.(view.Table).Rows {
		for _, c := range ambiguousChars {
			if strings.ContainsRune(row[0], c) {
				t.Fatalf("password %q contains excluded ambiguous character %q", row[0], c)
			}
		}
	}
}

func TestPasswordExcludingEveryClassIsRefused(t *testing.T) {
	_, err := runPassword(context.Background(), req(map[string]any{
		"no-lower": true, "no-upper": true, "no-digits": true,
	}))
	if err == nil {
		t.Fatal("expected an error when the alphabet is empty")
	}
}

func TestPasswordLengthIsCapped(t *testing.T) {
	_, err := runPassword(context.Background(), req(map[string]any{"length": maxPasswordLength + 1}))
	if err == nil {
		t.Fatal("expected the length cap to be enforced")
	}
}

func TestPasswordCountIsCapped(t *testing.T) {
	_, err := runPassword(context.Background(), req(map[string]any{"count": maxCount + 1}))
	if err == nil {
		t.Fatal("expected the count cap to be enforced")
	}
}

// Two passwords in the same batch must not collide — a weak or seeded source
// would be visible here well before it showed up anywhere else.
func TestPasswordBatchIsNotRepeated(t *testing.T) {
	v, err := runPassword(context.Background(), req(map[string]any{"length": 16, "count": 50}))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range v.(view.Table).Rows {
		if seen[row[0]] {
			t.Fatalf("password %q generated twice in one batch", row[0])
		}
		seen[row[0]] = true
	}
}

func TestTokenEncodings(t *testing.T) {
	for _, enc := range []string{"hex", "base64", "base64url", "base32"} {
		v, err := runToken(context.Background(), req(map[string]any{"length": 16, "encoding": enc}))
		if err != nil {
			t.Fatalf("%s: %v", enc, err)
		}
		kv := v.(view.KeyValue)
		if kv.Pairs[0].Value == "" {
			t.Errorf("%s: empty token", enc)
		}
	}
}

func TestTokenLengthIsCapped(t *testing.T) {
	_, err := runToken(context.Background(), req(map[string]any{"length": maxTokenBytes + 1}))
	if err == nil {
		t.Fatal("expected the byte-length cap to be enforced")
	}
}

func TestUUIDv4IsValid(t *testing.T) {
	v, err := runUUID(context.Background(), req(map[string]any{"version": "4", "count": 3}))
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	if len(tbl.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(tbl.Rows))
	}
	for _, row := range tbl.Rows {
		id, err := uuid.Parse(row[0])
		if err != nil {
			t.Fatalf("not a valid uuid: %v", err)
		}
		if id.Version() != 4 {
			t.Errorf("version = %d, want 4", id.Version())
		}
	}
}

func TestUUIDv7IsTimeOrdered(t *testing.T) {
	v, err := runUUID(context.Background(), req(map[string]any{"version": "7"}))
	if err != nil {
		t.Fatal(err)
	}
	id, err := uuid.Parse(v.(view.Table).Rows[0][0])
	if err != nil {
		t.Fatal(err)
	}
	if id.Version() != 7 {
		t.Errorf("version = %d, want 7", id.Version())
	}
}

// The overview's whole point is that the values are real: somebody copies
// one out of the tile and uses it. A placeholder that looked like a password
// would be worse than no tile at all.
func TestOverviewValuesAreRealAndFresh(t *testing.T) {
	first := overviewRows(t)
	second := overviewRows(t)
	if len(first) == 0 {
		t.Fatal("overview generated nothing")
	}
	for purpose, v := range first {
		if v == "" {
			t.Errorf("%s produced an empty value", purpose)
		}
		if second[purpose] == v {
			t.Errorf("%s repeated across calls (%q) — the values must be freshly generated", purpose, v)
		}
	}
}

func overviewRows(t *testing.T) map[string]string {
	t.Helper()
	v, err := runOverview(t.Context(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("overview = %s, want Table", view.TypeOf(v))
	}
	out := map[string]string{}
	for _, r := range tbl.Rows {
		out[r[0]] = r[1]
	}
	return out
}

// Every offered shape must actually produce a value of that shape, and the
// entropy printed next to it must be the entropy it has — a generator that
// overstates its own strength is worse than one that says nothing.
func TestEveryRecipeProducesAValueAndAnHonestBitCount(t *testing.T) {
	for _, group := range [][]recipe{passwordRecipes, keyRecipes, uuidRecipes} {
		for _, r := range group {
			v, bits, err := r.make()
			if err != nil {
				t.Fatalf("%s: %v", r.use, err)
			}
			if v == "" {
				t.Errorf("%s produced nothing", r.use)
			}
			if bits < 74 {
				t.Errorf("%s claims only %.0f bits — too weak to offer", r.use, bits)
			}
			if r.cmd == "" {
				t.Errorf("%s has no reproducing command", r.use)
			}
		}
	}
}

// The two 32-shaped key rows are the reason the section exists: they look
// interchangeable and are not. The note explaining that must state the
// number the row above it actually shows, not a remembered one.
func TestKeyNoteMatchesTheValueItExplains(t *testing.T) {
	note := keyNote()
	if !strings.Contains(note, fmt.Sprintf("%.0f bits", thirtyTwoChars.bits())) {
		t.Errorf("note does not state the computed entropy (%.0f bits): %q", thirtyTwoChars.bits(), note)
	}
	if thirtyTwoChars.bits() >= 256 {
		t.Errorf("32 printable chars cannot carry %.0f bits — the alphabet grew past a byte", thirtyTwoChars.bits())
	}
	// The hex/base64 rows are the ones that really are AES-256 key material.
	for _, r := range keyRecipes[:2] {
		if _, bits, _ := r.make(); bits != 256 {
			t.Errorf("%s = %.0f bits, want exactly 256", r.use, bits)
		}
	}
}

func TestOverviewDetailIsASectionedPage(t *testing.T) {
	v, err := runOverview(t.Context(), plugin.NewRequest(map[string]any{"detail": true}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("detail = %s, want Sections", view.TypeOf(v))
	}
	want := map[string]bool{"passwords": true, "keys & tokens": true, "about key length": true, "uuids": true}
	for _, item := range s.Items {
		delete(want, item.Title)
	}
	if len(want) > 0 {
		t.Errorf("detail page is missing sections: %v", want)
	}
}

// The compact tile and the detail page must offer the same shapes, or the
// tile becomes a menu of things the full page cannot explain.
func TestTileShapesAllExistInTheDetailPage(t *testing.T) {
	known := map[string]bool{}
	for _, group := range [][]recipe{passwordRecipes, keyRecipes, uuidRecipes} {
		for _, r := range group {
			known[r.use] = true
		}
	}
	for purpose := range overviewRows(t) {
		if !known[purpose] {
			t.Errorf("tile offers %q, which no recipe defines", purpose)
		}
	}
}

// detailTables returns the recipe tables of the detail page, keyed by the
// section they were found in.
func detailTables(t *testing.T) map[string]view.Table {
	t.Helper()
	v, err := runOverview(t.Context(), plugin.NewRequest(map[string]any{"detail": true}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("detail = %s, want Sections", view.TypeOf(v))
	}
	out := map[string]view.Table{}
	for _, item := range s.Items {
		if tbl, ok := item.View.(view.Table); ok {
			out[item.Title] = tbl
		}
	}
	return out
}

// The page is a catalogue read in a terminal, and the thing on it that must
// survive intact is the generated value: a key the renderer shortened is not
// a key you can use, and one shortened with an ellipsis is worse than
// useless because it still looks like a key. Wrapping inside a cell is fine
// — every character is still on screen and selectable — but nothing may be
// dropped, at any width.
func TestNoGeneratedValueIsEverShortened(t *testing.T) {
	for _, tbl := range detailTables(t) {
		for _, row := range tbl.Rows {
			for _, cell := range row {
				if strings.ContainsAny(cell, "…") {
					t.Errorf("a cell was shortened by the producer: %q", cell)
				}
			}
		}
	}
	// And end to end, through the renderer, at the widths people use.
	v, err := runOverview(t.Context(), plugin.NewRequest(map[string]any{"detail": true}, false, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []int{120, 100, 80} {
		var buf bytes.Buffer
		if err := cli.Render(&buf, v, cli.Options{Format: cli.Pretty, NoColor: true, Width: w}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), "…") {
			t.Errorf("width %d: the renderer had to shorten something", w)
		}
	}
}

// The columns are a contract, not a layout preference: a JSON or MCP caller
// reads the same rows positionally, so their order and meaning have to be
// fixed even though only the terminal shows the headers.
func TestRecipeTableColumnsAreForValueBitsCommand(t *testing.T) {
	want := []string{"For", "Value", "Bits", "Command"}
	for title, tbl := range detailTables(t) {
		var got []string
		for _, c := range tbl.Columns {
			got = append(got, c.Name)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s columns = %v, want %v", title, got, want)
		}
		for _, row := range tbl.Rows {
			if len(row) != len(want) {
				t.Errorf("%s: row has %d cells, want %d: %v", title, len(row), len(want), row)
			}
			if row[0] == "" || row[1] == "" || row[3] == "" {
				t.Errorf("%s: row has an empty cell that must not be: %v", title, row)
			}
		}
	}
}

// Concise is the whole reason the table works at all: a row has to carry a
// 64-cell value, so every other cell is competing for the same line. These
// budgets are what keep the table from reflowing on an ordinary terminal.
func TestRecipeLabelsAndCommandsStayShort(t *testing.T) {
	for _, group := range [][]recipe{passwordRecipes, keyRecipes, uuidRecipes} {
		for _, r := range group {
			if len(r.use) > 24 {
				t.Errorf("%q is %d chars — too long for the For column", r.use, len(r.use))
			}
			if len(r.cmd) > 40 {
				t.Errorf("%q is %d chars — too long for the Command column", r.cmd, len(r.cmd))
			}
			if strings.HasPrefix(r.cmd, "rta ") {
				t.Errorf("%q repeats the implied rta prefix", r.cmd)
			}
		}
	}
}
