package tui

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// What somebody acts on, saves or copies is the record's value, not the
// spelling the screen draws it in. Every character below is planted by code
// point: a source file may not hold one (internal/textclean's source guard).

// A key holding an override is drawn spelled out, and the action on its row
// still reaches the key that exists.
func TestARowActionActsOnTheKeyTheRecordHolds(t *testing.T) {
	key := "reports/invoice" + string(rune(0x202e)) + "fdp.exe"
	var got []string
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "kv", Summary: "store",
		Capabilities: []plugin.Capability{
			{ID: "kv.list", Summary: "list", Safety: plugin.Read, Idempotent: true,
				Actions: []plugin.Action{{Key: "g", Label: "get", Target: "kv.get", Source: plugin.ActionRow}},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Table{Columns: []view.Column{{Name: "key"}}, Rows: [][]string{{key}}, Total: 1}, nil
				}},
			{ID: "kv.get", Summary: "get", Safety: plugin.Read, Idempotent: true,
				Inputs: []plugin.Field{{Name: "key", Type: plugin.String, Positional: true, Required: true, Help: "k"}},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					got = append(got, req.String("key"))
					return view.Text{Body: "value"}, nil
				}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{Tiles: []config.Tile{{ID: "kv.list"}}}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	c, _ := reg.Capability("kv.list")
	v, _ := c.Run(context.Background(), plugin.NewRequest(nil, false, false))
	shown, _ := sized.(Model).Update(resultMsg{cap: c, view: v})
	rm := shown.(Model)
	if cell := rm.result.view.(view.Table).Rows[0][0]; strings.ContainsRune(cell, 0x202e) {
		t.Fatalf("the cell on screen holds the override itself: %q", cell)
	}
	if strings.ContainsRune(rm.resultView(), 0x202e) {
		t.Fatal("the pane draws the override itself")
	}

	_, cmd := rm.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	drain(t, cmd)
	if len(got) != 1 || got[0] != key {
		t.Fatalf("kv.get ran with %q, want the key the row holds, %q", got, key)
	}
}

// An edit form nobody changed saves what the record already held — the
// title with its isolates, the tags with their override — and a box
// somebody did change saves what they typed.
func TestAPrefilledValueNobodyEditedComesBackAsTheRecordHoldsIt(t *testing.T) {
	title := "hello " + string(rune(0x2068)) + "Alice" + string(rune(0x2069)) + " there"
	tags := []string{"a" + string(rune(0x202e)) + "b", "plain"}
	c := plugin.Capability{ID: "note.edit", Summary: "edit", Safety: plugin.Write, Inputs: []plugin.Field{
		{Name: "title", Type: plugin.String, Help: "t"},
		{Name: "tags", Type: plugin.StringSlice, Help: "t"},
	}}
	cf := newCapForm(c, c.Inputs, map[string]any{"title": title, "tags": tags}, true, nil)
	if box := *cf.bindings["title"]; strings.ContainsRune(box, 0x2068) {
		t.Fatalf("the title box holds the isolate itself: %q", box)
	}

	got := cf.values()
	if got["title"] != title {
		t.Errorf("an untouched title came back as %q, want %q", got["title"], title)
	}
	if back, _ := got["tags"].([]string); !slices.Equal(back, tags) {
		t.Errorf("untouched tags came back as %q, want %q", got["tags"], tags)
	}

	*cf.bindings["title"] = "renamed"
	if got := cf.values()["title"]; got != "renamed" {
		t.Errorf("an edited title came back as %q, want what was typed", got)
	}
}

// The copy key hands the clipboard the value, and the picker labels it
// cleaned.
func TestCopyTakesTheValueAndThePickerDrawsItCleaned(t *testing.T) {
	secret := "pw" + string(rune(0x202e)) + "321"
	stdin := fakeClipboard(t)
	m := New(registry.New(), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	one, _ := sized.(Model).Update(resultMsg{cap: genPasswordCap(), view: view.Table{
		Columns: []view.Column{{Name: "Password"}},
		Rows:    [][]string{{secret}},
	}})
	press(t, one.(Model), "c")
	if copied, err := os.ReadFile(stdin); err != nil || string(copied) != secret {
		t.Fatalf("clipboard got %q (%v), want the value itself", copied, err)
	}

	several, _ := sized.(Model).Update(resultMsg{cap: genPasswordCap(), view: view.Table{
		Columns: []view.Column{{Name: "Password"}},
		Rows:    [][]string{{secret}, {"other"}},
	}})
	pick := press(t, several.(Model), "c")
	if pick.mode != modeCopyPick {
		t.Fatalf("c did not open the picker: mode = %v", pick.mode)
	}
	if pick.copyPick.value != secret {
		t.Errorf("the picker holds %q, want the value itself", pick.copyPick.value)
	}
	if strings.ContainsRune(pick.copyPickView(), 0x202e) {
		t.Error("the picker draws the override itself")
	}
}

// Two tiles of one capability against two hosts share a key, and each still
// draws its own answer: what a tile returned is kept by more than its key.
func TestTwoTilesOfOneKeyEachDrawTheirOwnAnswer(t *testing.T) {
	reg := registry.New()
	err := reg.Register(plugin.Plugin{Name: "obj", Summary: "obj", Capabilities: []plugin.Capability{
		{ID: "obj.get", Summary: "get", Safety: plugin.Read, Idempotent: true,
			Inputs: []plugin.Field{{Name: "host", Type: plugin.String, Help: "h"}},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return view.Text{Body: "answer from " + req.String("host")}, nil
			}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{Tiles: []config.Tile{
		{ID: "obj.get", With: map[string]any{"host": "alpha"}},
		{ID: "obj.get", With: map[string]any{"host": "beta"}},
	}}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = sized.(Model)
	var idx []int
	for i, tl := range m.tiles {
		if tl.cap.ID == "obj.get" {
			idx = append(idx, i)
		}
	}
	if len(idx) != 2 {
		t.Fatalf("obj.get tiles at %v, want two", idx)
	}
	for _, i := range idx {
		host, _ := m.tiles[i].values["host"].(string)
		updated, _ := m.Update(tileMsg{key: m.tiles[i].key(), idx: i, v: view.Text{Body: "answer from " + host}})
		m = updated.(Model)
	}
	// Rebuilt, too: rebuildTiles carries each panel over by its key alone,
	// which hands both tiles the answer of whichever came last, and what the
	// capability returned is what either of them draws.
	for _, rebuilt := range []bool{false, true} {
		if rebuilt {
			m.rebuildTiles()
		}
		screen := m.dashboardView()
		for _, host := range []string{"alpha", "beta"} {
			if strings.Count(screen, "answer from "+host) != 1 {
				t.Errorf("rebuilt %v: the dashboard draws %q %d times, want once:\n%s",
					rebuilt, "answer from "+host, strings.Count(screen, "answer from "+host), screen)
			}
		}
	}
}

// The same for a tile: its panel is drawn cleaned, once, and its copy key
// copies the value its capability returned.
func TestATileIsDrawnCleanedAndCopiesItsValue(t *testing.T) {
	secret := "pw" + string(rune(0x202e)) + "321"
	stdin := fakeClipboard(t)
	reg := registry.New()
	c := genPasswordCap([]string{secret, "94.2"})
	if err := reg.Register(plugin.Plugin{Name: "gen", Summary: "gen", Capabilities: []plugin.Capability{c}}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{Tiles: []config.Tile{{ID: "gen.password"}}}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = sized.(Model)
	i := tileIndex(t, m, "gen.password")
	v, _ := c.Run(context.Background(), plugin.NewRequest(nil, false, false))
	updated, _ := m.Update(tileMsg{key: m.tiles[i].key(), idx: i, v: v})
	m = updated.(Model)
	m.selected = i

	if strings.ContainsRune(m.dashboardView(), 0x202e) {
		t.Fatal("the dashboard draws the override itself")
	}
	press(t, m, "c")
	if copied, err := os.ReadFile(stdin); err != nil || string(copied) != secret {
		t.Fatalf("clipboard got %q (%v), want the value itself", copied, err)
	}
}

// A tile whose inputs changed under it — its entry edited in another
// terminal and adopted on the next tick — keeps the panel it had until it
// answers again, and holds that panel only cleaned: what its capability
// returned was kept under the inputs it no longer has. The copy key hands
// over no display spelling as a value, and the footer offers no copy.
func TestATileWhoseInputsChangedCopiesNoDisplaySpelling(t *testing.T) {
	secret := "pw" + string(rune(0x202e)) + "321"
	stdin := fakeClipboard(t)
	reg := registry.New()
	c := genPasswordCap([]string{secret, "94.2"})
	if err := reg.Register(plugin.Plugin{Name: "gen", Summary: "gen", Capabilities: []plugin.Capability{c}}); err != nil {
		t.Fatal(err)
	}
	entry := func(length int) config.Dashboard {
		return config.Dashboard{Tiles: []config.Tile{{ID: "gen.password", With: map[string]any{"length": length}}}}
	}
	m := New(reg, entry(20), nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = sized.(Model)
	i := tileIndex(t, m, "gen.password")
	v, _ := c.Run(context.Background(), plugin.NewRequest(nil, false, false))
	m = answerTile(t, m, i, v)

	m.dash = entry(30)
	m.rebuildTiles()
	m.selected = tileIndex(t, m, "gen.password")
	if footer := plain(m.dashFooter()); strings.Contains(footer, "copy value") {
		t.Errorf("the footer offers a copy the tile cannot make: %q", footer)
	}
	press(t, m, "c")
	if copied, err := os.ReadFile(stdin); err == nil && string(copied) != secret {
		t.Fatalf("clipboard got %q, the display spelling of the value", copied)
	}
}
