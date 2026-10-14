package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every form the TUI builds takes the palette's look. A form added without it
// would be the one place the environment's colour stops, and nothing on the
// screen would say why, so the rule is held where the forms are made.
func TestEveryFormTheTUIBuildsIsDrawnInThePalette(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forms, themed := 0, 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		forms += strings.Count(string(b), "huh.NewForm(")
		themed += strings.Count(string(b), "WithTheme(formTheme())")
	}
	if forms == 0 {
		t.Fatal("found no form; the scan reads the wrong files")
	}
	if forms != themed {
		t.Errorf("%d forms are built and %d take the palette's look: every huh.NewForm needs .WithTheme(formTheme())", forms, themed)
	}
}
