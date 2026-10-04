package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// `rta explain` suggested by shared dotted segments, which found `disk space`
// by an accident of how the segments were compared and found nothing for a
// misspelled word. It asks the matcher the TUI search and the unknown-command
// hint ask, so a word finds the same thing in all three.
func TestExplainSuggestsByTheWordsTypedAndByTheirSpelling(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for query, want := range map[string]string{
		"disk space": "sys.disk",
		"proceses":   "sys.ps",
		"ps":         "sys.ps",
		"uuid":       "gen.uuid",
		"ports":      "net.port",
		"sys.cpuu":   "sys.cpu",
		"kvv":        "kv.copy",
	} {
		_, _, err := run(t, reg, "explain", query)
		var ve *view.Error
		if !errors.As(err, &ve) {
			t.Errorf("`rta explain %s` answered %v, want an unknown capability", query, err)
			continue
		}
		hint, _ := strings.CutPrefix(ve.Hint, "did you mean: ")
		if !strings.Contains(", "+hint+",", ", "+want+",") {
			t.Errorf("`rta explain %s` hints %q, want %s among the suggestions", query, ve.Hint, want)
		}
	}
}
