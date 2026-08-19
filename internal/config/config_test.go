package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
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
