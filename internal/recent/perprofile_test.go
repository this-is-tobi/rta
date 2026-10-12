package recent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A value typed under one environment is a statement about that environment's
// storage, and offered under another it is a suggestion to read the wrong
// bucket one keystroke from accepting it. The shortlist is the profile's.
func TestAValueTypedUnderOneProfileIsNotOfferedUnderAnother(t *testing.T) {
	isolated(t)
	c := capWith("s3.object.list", str("bucket"))
	Record(plugin.SurfaceCLI, c, "staging", map[string]any{"bucket": "staging-assets"})
	Record(plugin.SurfaceCLI, c, "prod", map[string]any{"bucket": "prod-assets"})
	Record(plugin.SurfaceCLI, c, "", map[string]any{"bucket": "mine"})

	used := Load()
	for profile, want := range map[string]string{"staging": "staging-assets", "prod": "prod-assets", "": "mine"} {
		got := used.For(profile, "s3.object.list", "bucket")
		if len(got) != 1 || got[0] != want {
			t.Errorf("under %q the shortlist is %v, want only %q", profile, got, want)
		}
	}
	if got := used.For("elsewhere", "s3.object.list", "bucket"); len(got) != 0 {
		t.Errorf("a profile nothing was typed under is offered %v", got)
	}
}

// Two connections of one environment are two places a value can be a value of,
// and the plugin's capabilities still share the one shortlist each has.
func TestAnInstanceHasAShortlistOfItsOwn(t *testing.T) {
	isolated(t)
	Record(plugin.SurfaceCLI, capWith("pg.table.list", str("schema")), "staging", map[string]any{"schema": "app"})
	Record(plugin.SurfaceCLI, capWith("pg.table.list", str("schema")), "staging/analytics", map[string]any{"schema": "events"})

	used := Load()
	if got := used.For("staging", "pg.table.get", "schema"); len(got) != 1 || got[0] != "app" {
		t.Errorf("the default instance is offered %v", got)
	}
	if got := used.For("staging/analytics", "pg.table.get", "schema"); len(got) != 1 || got[0] != "events" {
		t.Errorf("the labeled instance is offered %v", got)
	}
}

// The command tree is built before there is a command line, so it asks whether
// an input has been answered under any profile.
func TestAnInputAnsweredUnderAnyProfileIsUsed(t *testing.T) {
	isolated(t)
	Record(plugin.SurfaceCLI, capWith("s3.object.list", str("bucket")), "staging", map[string]any{"bucket": "b"})
	used := Load()
	if !used.Used("s3.object.get", "bucket") {
		t.Error("an input answered under staging is not used")
	}
	if used.Used("s3.object.get", "prefix") || used.Used("pg.table.list", "bucket") {
		t.Error("an input nobody answered is used")
	}
	if (Values{"staging|s3.bucket": {}}).Used("s3.object.get", "bucket") {
		t.Error("an emptied shortlist is used")
	}
}

// The file this replaced held "<namespace>.<input>" and nothing says which
// environment those values were typed under, so they are not adopted — by the
// base configuration least of all, which would offer a value typed under
// staging to every command that names no profile — and the next write drops
// them from the file.
func TestAShortlistOfTheOldShapeIsNotAdoptedByAnyProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)
	old := `{"inputs":{"s3.bucket":["typed-under-staging"]},"order":["s3.bucket"]}`
	if err := os.WriteFile(filepath.Join(dir, "recent.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	used := Load()
	for _, profile := range []string{"", "staging", "prod"} {
		if got := used.For(profile, "s3.object.list", "bucket"); len(got) != 0 {
			t.Errorf("under %q the old file offers %v", profile, got)
		}
	}
	if used.Used("s3.object.list", "bucket") {
		t.Error("the old file's input counts as used")
	}

	Record(plugin.SurfaceCLI, capWith("s3.object.list", str("bucket")), "staging", map[string]any{"bucket": "fresh"})
	raw, err := os.ReadFile(filepath.Join(dir, "recent.json"))
	if err != nil {
		t.Fatal(err)
	}
	var written struct {
		Inputs map[string][]string `json:"inputs"`
		Order  []string            `json:"order"`
	}
	if err := json.Unmarshal(raw, &written); err != nil {
		t.Fatal(err)
	}
	if _, kept := written.Inputs["s3.bucket"]; kept || len(written.Inputs) != 1 || len(written.Order) != 1 {
		t.Errorf("the old key survived the next write: %s", raw)
	}
}
