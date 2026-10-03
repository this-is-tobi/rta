package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
)

// The tree is built from the configuration main already read, not from a
// second read of the file: the file says yaml, the handed-over value says
// json, and the default of --output is the handed-over one. Without the
// option NewRoot still reads the file itself, which is what every other
// caller of it relies on.
func TestNewRootTakesTheConfigItIsGivenInsteadOfReadingTheFileAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("output: yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", path)
	t.Setenv("RTA_OUTPUT", "")
	reg := registry.New()

	defaultOutput := func(options ...RootOption) string {
		t.Helper()
		flag := NewRoot(reg, "test", options...).PersistentFlags().Lookup("output")
		if flag == nil {
			t.Fatal("the root has no --output flag")
		}
		return flag.DefValue
	}

	if got := defaultOutput(); got != "yaml" {
		t.Errorf("default output without the option = %q, want the file's yaml", got)
	}
	if got := defaultOutput(WithConfig(config.Config{Output: "json"}, nil)); got != "json" {
		t.Errorf("default output with the option = %q, want the handed-over json", got)
	}
}
