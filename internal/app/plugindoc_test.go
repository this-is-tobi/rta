package app

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func noopHandler(context.Context, plugin.Request) (view.View, error) {
	return view.Text{}, nil
}

func docFixture() plugin.Plugin {
	return plugin.Plugin{
		Name:    "shelf",
		Summary: "Books on a shelf",
		Needs:   []plugin.Need{plugin.NeedNetrc},
		Capabilities: []plugin.Capability{
			{
				ID: "shelf.list", Summary: "Every book", Safety: plugin.Read, Idempotent: true,
				Description: "Alphabetically, by author.",
				Inputs: []plugin.Field{
					{Name: "room", Type: plugin.String, Config: "room", Help: "which room's shelf"},
				},
				Run: noopHandler,
			},
			{
				ID: "shelf.burn", Summary: "Burn a book", Safety: plugin.Destructive,
				Inputs: []plugin.Field{
					{Name: "title", Type: plugin.String, Required: true, Positional: true},
					{Name: "room", Type: plugin.String, Config: "room", Help: "which room's shelf"},
				},
				Run: noopHandler,
			},
		},
	}
}

func docPage(t *testing.T, p plugin.Plugin) view.Sections {
	t.Helper()
	page, verr := pluginDocView(p)
	if verr != nil {
		t.Fatal(verr)
	}
	return page.(view.Sections)
}

func docSection(t *testing.T, page view.Sections, title string) view.View {
	t.Helper()
	for _, s := range page.Items {
		if s.Title == title {
			return s.View
		}
	}
	t.Fatalf("no %q section; have %v", title, sectionTitles(page))
	return nil
}

func sectionTitles(page view.Sections) []string {
	out := make([]string, 0, len(page.Items))
	for _, s := range page.Items {
		out = append(out, s.Title)
	}
	return out
}

func renderDoc(t *testing.T, p plugin.Plugin) string {
	t.Helper()
	var b strings.Builder
	if err := cli.Render(&b, docPage(t, p), cli.Options{Format: cli.Markdown}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestPluginDocIsOnePageFromTheDeclaration(t *testing.T) {
	page := docPage(t, docFixture())

	caps := docSection(t, page, "Capabilities").(view.Table)
	if got := rowFor(t, caps, "Capability", "shelf.burn"); got[1] != "destructive" || got[2] != "Burn a book" {
		t.Errorf("capability row = %v", got)
	}

	cfg := docSection(t, page, "Configuration").(view.Sections)
	keys := cfg.Items[1].View.(view.Table)
	if got := rowFor(t, keys, "Key", "room"); got[1] != "shelf.burn, shelf.list" || got[2] != "which room's shelf" {
		t.Errorf("config row = %v", got)
	}

	described := docSection(t, page, "shelf.list").(view.Sections)
	if prose := described.Items[0].View.(view.Text).Body; prose != "Alphabetically, by author." {
		t.Errorf("description prose = %q", prose)
	}
	if got := pairValue(described.Items[1].View.(view.KeyValue), "description"); got != "" {
		t.Errorf("description still in the card as %q", got)
	}

	card := docSection(t, page, "shelf.burn").(view.KeyValue)
	if got := pairValue(card, "cli"); got != "rta shelf burn <title> [--room <string>]" {
		t.Errorf("cli form = %q", got)
	}
	if got := pairValue(card, "mcp exposure"); !strings.Contains(got, "--allow-destructive shelf.burn`") {
		t.Errorf("mcp exposure = %q", got)
	}

	doc := renderDoc(t, docFixture())
	for _, want := range []string{"# shelf\n", "Books on a shelf", "`netrc`", "## Capabilities", "## Configuration", "## shelf.list", "## shelf.burn"} {
		if !strings.Contains(doc, want) {
			t.Errorf("rendered page lacks %q\n%s", want, doc)
		}
	}
}

// The card names the reader's config file, and a page read by strangers must
// not carry one machine's path; nor may a build's digest end up pinned into a
// flag every installer would need to type differently.
func TestPluginDocCarriesNothingMachineSpecific(t *testing.T) {
	doc := renderDoc(t, docFixture())
	if strings.Contains(doc, "config file") {
		t.Errorf("page names a config file path:\n%s", doc)
	}
	if strings.Contains(doc, "shelf.burn@") {
		t.Errorf("page pins the allow flag to a digest:\n%s", doc)
	}
}

func TestPluginDocOmitsConfigurationWhenNothingIsConfigurable(t *testing.T) {
	p := docFixture()
	for i := range p.Capabilities {
		p.Capabilities[i].Inputs = nil
	}
	for _, title := range sectionTitles(docPage(t, p)) {
		if title == "Configuration" {
			t.Error("page has a Configuration section with no keys to list")
		}
	}
}

func pairValue(kv view.KeyValue, key string) string {
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

func rowFor(t *testing.T, tbl view.Table, col, want string) []string {
	t.Helper()
	idx := -1
	for i, c := range tbl.Columns {
		if c.Name == col {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("no %q column in %v", col, tbl.Columns)
	}
	for _, row := range tbl.Rows {
		if row[idx] == want {
			return row
		}
	}
	t.Fatalf("no row with %s = %q", col, want)
	return nil
}
