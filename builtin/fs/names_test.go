package fs

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/view"
)

// oddNames is a directory holding what a listing used to clean away: a name
// with an escape sequence in it, one with a newline, one with a tab, and an
// ordinary one with a space in it, which stays as it is.
func oddNames(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"esc\x1b[31mred", "line\nbreak", "tab\there", "Annual Report.pdf"} {
		if err := os.WriteFile(filepath.Join(root, name), make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func drawn(t *testing.T, v view.View) string {
	t.Helper()
	var out bytes.Buffer
	if err := cli.Render(&out, v, cli.Options{Format: cli.Pretty, NoColor: true, Width: 100}); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// The screen's cleaner drops what a terminal would act on, which is right of a
// body and wrong of a name: `esc` ESC `[31mred` was drawn as `escred`, which
// is not the file, a newline in a name split its tree line or table row in
// two, and a tab drew as a gap. A name that does not read as itself is shown
// quoted with the character written out, in the tree and in the table, and
// one that does is left as it is.
func TestAFileNameWithAControlCharacterIsShownEscapedNotCleaned(t *testing.T) {
	root := oddNames(t)
	want := []string{`"esc\x1b[31mred"`, `"line\nbreak"`, `"tab\there"`, "Annual Report.pdf"}

	for name, v := range map[string]view.View{
		"tree":  run(t, runTree, map[string]any{"path": root}),
		"usage": run(t, runUsage, map[string]any{"path": root, "limit": 20, "apparent": true}),
	} {
		screen := drawn(t, v)
		for _, w := range want {
			if !strings.Contains(screen, w) {
				t.Errorf("%s does not show %s:\n%s", name, w, screen)
			}
		}
		for _, bad := range []string{"escred", "linebreak", "tabhere"} {
			if strings.Contains(screen, bad) {
				t.Errorf("%s shows the cleaned name %q:\n%s", name, bad, screen)
			}
		}
		// One line to a name: the escape and the newline did not split any.
		if lines := strings.Count(strings.TrimSpace(screen), "\n") + 1; lines > 12 {
			t.Errorf("%s drew %d lines for four names:\n%s", name, lines, screen)
		}
	}
}
