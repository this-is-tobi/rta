package config

import (
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

func TestParseTakesAnEmptyFileAsZeroConfig(t *testing.T) {
	configAt(t)
	for _, text := range []string{"", "\n", "# only a comment\n"} {
		cfg, err := Parse([]byte(text))
		if err != nil {
			t.Errorf("%q: %v", text, err)
		}
		if cfg.Output != "" || len(cfg.Profiles) != 0 {
			t.Errorf("%q: %+v", text, cfg)
		}
	}
}

// What `rta config edit` checks the editor's result with has to be what the
// loader would say about the same bytes once they were the file, or a save is
// accepted that every later command then refuses.
func TestParseRefusesWhatTheLoaderRefuses(t *testing.T) {
	path := configAt(t)
	for _, text := range []string{"output: [unclosed\n", "a: &x 1\nb: *x\n"} {
		_, parsed := Parse([]byte(text))
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		_, loaded := LoadFile()
		if parsed == nil || loaded == nil {
			t.Fatalf("%q: Parse %v, LoadFile %v", text, parsed, loaded)
		}
		if parsed.Error() != loaded.Error() {
			t.Errorf("%q: Parse says %q, LoadFile says %q", text, parsed, loaded)
		}
		if code := view.AsError(parsed, "").Code; code != "config.invalid" || !strings.Contains(parsed.Error(), path) {
			t.Errorf("%q: %v", text, parsed)
		}
	}
}

func TestParseStampsWhereTheTextCameFrom(t *testing.T) {
	configAt(t)
	cfg, err := Parse([]byte("profiles:\n  prod:\n    note: x\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Trusted() || !cfg.Profiles["prod"].Trusted() {
		t.Error("text parsed for a path somebody named is not stamped as theirs")
	}
}
