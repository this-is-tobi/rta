package pluginhost

import (
	"os"
	"testing"
)

// A data directory of this binary's own, for the whole package.
//
// Every launch records what the plugin declared in the describe cache under
// the data directory (cache.go), sealed with a key it creates there the first
// time, and prunes the directory to its bound. Most tests here open the
// example plugin through New and Open without naming a directory, so the
// package wrote its fixture's declarations into the developer's own
// ~/.local/share/rta, made a cache key there if they had none, and pruned
// their real plugins' entries to make room — and every run of the package on
// one machine, two branches tested side by side or a stress loop beside the
// editor's, read and pruned that same directory under the others. Set for
// every test rather than in the helpers, as internal/app does, because the
// rule is about the package and a helper is something a new test can forget
// to call; a test that wants a directory of its own still sets one.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rta-pluginhost-tests")
	if err != nil {
		panic(err)
	}
	os.Setenv("RTA_DATA_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
