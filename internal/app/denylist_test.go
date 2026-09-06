package app

import (
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/builtin/audit"
)

// The deny list the audit prints is derived from the catalogue; the two
// places the docs enumerate the same plugins by hand are pinned to it, so a
// plugin added tomorrow shows up in the docs the day the test runs.
func TestTheDocsNameEveryHumanOnlyPlugin(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	derived := audit.HumanOnlyPlugins(reg.Capabilities)
	if len(derived) < 4 {
		t.Fatalf("derived only %v; the deny list would be near empty", derived)
	}
	for _, doc := range []struct{ path, form string }{
		{"../../docs/30-boundary/10-the-boundary.md", "`rta %s`"},
		{"../../docs/30-boundary/20-mcp.md", "`%s`"},
	} {
		body, err := os.ReadFile(doc.path)
		if err != nil {
			t.Fatal(err)
		}
		for _, ns := range derived {
			want := strings.ReplaceAll(doc.form, "%s", ns)
			if !strings.Contains(string(body), want) {
				t.Errorf("%s never names %s, which every capability of is for the person at the terminal", doc.path, want)
			}
		}
	}
	// The verbs of the mixed plugins are on the block for the same reason and
	// are named the same way, in the chapter that explains the block.
	body, err := os.ReadFile("../../docs/30-boundary/10-the-boundary.md")
	if err != nil {
		t.Fatal(err)
	}
	verbs := audit.HumanOnlyVerbs(reg.Capabilities)
	if len(verbs) < 2 {
		t.Fatalf("derived only %v verbs; the block would miss the mixed plugins", verbs)
	}
	for _, v := range verbs {
		if want := "`rta " + v + "`"; !strings.Contains(string(body), want) {
			t.Errorf("the boundary chapter never names %s, which is for the person at the terminal", want)
		}
	}
}
