package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

func setPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("RTA_CONFIG", p)
	return p
}

func TestMissingFileIsZeroConfig(t *testing.T) {
	setPath(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Output != "" || len(cfg.Dashboard.Tiles) != 0 {
		t.Errorf("zero config not zero: %+v", cfg)
	}
}

func TestWriteLoadRoundTrip(t *testing.T) {
	setPath(t)
	var cfg Config
	cfg.Output = "json"
	cfg.Dashboard.Tiles = []Tile{
		{ID: "sys.cpu", With: map[string]any{"cores": true}},
		{ID: "sys.mem"},
	}
	if err := Write(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Output != "json" || len(got.Dashboard.Tiles) != 2 {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if got.Dashboard.Tiles[0].With["cores"] != true {
		t.Errorf("tile values lost: %+v", got.Dashboard.Tiles[0])
	}
	// File carries the explanatory header.
	raw, _ := os.ReadFile(Path())
	if !strings.HasPrefix(string(raw), "# rta configuration") {
		t.Errorf("header missing:\n%s", raw)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	setPath(t)
	var cfg Config
	cfg.Output = "yaml"
	if err := Write(cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_OUTPUT", "csv")
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Output != "csv" {
		t.Errorf("env override lost: %q", got.Output)
	}
}

func TestInvalidYAMLIsCodedWithHint(t *testing.T) {
	p := setPath(t)
	if err := os.WriteFile(p, []byte("output: [unclosed"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load()
	ve := view.AsError(err, "x")
	if ve == nil || ve.Code != "config.invalid" || ve.Hint == "" {
		t.Errorf("want config.invalid with hint, got %+v", err)
	}
}

// A regression test for a real bug review found: Config has
// no legitimate use for a YAML anchor or alias, so even a small, harmless
// one — nothing here fans out into anything — must be refused rather than
// silently decoded, the same way an unrelated syntax error already is.
func TestConfigRefusesYAMLAnchorsAndAliases(t *testing.T) {
	p := setPath(t)
	if err := os.WriteFile(p, []byte("output: &o json\ndashboard:\n  columns: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load()
	ve := view.AsError(err, "x")
	if ve == nil || ve.Code != "config.invalid" {
		t.Fatalf("want config.invalid for a file using an anchor, got %+v", err)
	}
	if !strings.Contains(ve.Message, "anchor") && !strings.Contains(ve.Message, "alias") {
		t.Errorf("error should name the reason: %v", ve.Message)
	}
}

// aliasBomb builds a "billion laughs"-style bomb sized to the exact
// levels/fan-out the audit finding measured against this project's own
// pinned goccy-yaml version: 6 levels of 10x fan-out, well under 1 KB on
// disk, decoding (pre-fix) into 1.1 million values in well under a second.
// Large enough to prove decoding really would explode; small enough that
// proving it — by reverting the fix and letting Unmarshal actually run —
// does not itself hang the machine running this test. A real attack would
// go further; this only has to demonstrate the mechanism.
func aliasBomb() string {
	var b strings.Builder
	b.WriteString("a0: &a0 [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i < 6; i++ {
		fmt.Fprintf(&b, "a%d: &a%d [", i, i)
		for j := 0; j < 10; j++ {
			if j > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "*a%d", i-1)
		}
		b.WriteString("]\n")
	}
	return b.String()
}

// The DoS this closes: config.Load runs at the start of essentially every
// rta command, so a config.yaml that takes seconds (or, at a real bomb's
// actual size, far longer, plus the memory) to decode denies service on
// every subsequent invocation, not just once. This asserts the refusal
// itself is fast — proportional to the file's bytes, not to what it would
// have expanded into — which is the property that makes it a fix rather
// than a slower way to eventually hit the same wall.
func TestConfigRefusesAnAliasExpansionBombQuickly(t *testing.T) {
	p := setPath(t)
	if err := os.WriteFile(p, []byte(aliasBomb()), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := Load()
		done <- err
	}()
	select {
	case err := <-done:
		ve := view.AsError(err, "x")
		if ve == nil || ve.Code != "config.invalid" {
			t.Fatalf("want config.invalid for an alias-expansion bomb, got %+v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Load did not return within 2s — it decoded the bomb instead of refusing it")
	}
}
