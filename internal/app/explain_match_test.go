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

// A list of suggestions ends where the likeness does: `ports` is `net.port`,
// and kv.env is only there because its summary says "exports".
func TestExplainOffersNothingThatOnlyContainsTheWord(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = run(t, reg, "explain", "ports")
	var ve *view.Error
	if !errors.As(err, &ve) {
		t.Fatalf("`rta explain ports` answered %v", err)
	}
	if strings.Contains(ve.Hint, "kv.env") {
		t.Errorf("`rta explain ports` hints %q, which offers a capability for a word inside another", ve.Hint)
	}
}

// `backup` is not `audit.why` for a summary that says "back": a word a person
// types that only begins like a word of a summary is another word.
func TestExplainDoesNotOfferWhatOnlyBeginsLikeTheWordTyped(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = run(t, reg, "explain", "backup")
	var ve *view.Error
	if !errors.As(err, &ve) {
		t.Fatalf("`rta explain backup` answered %v", err)
	}
	if strings.Contains(ve.Hint, "audit.why") || strings.Contains(ve.Hint, "grant.revoke") {
		t.Errorf("`rta explain backup` hints %q, which offers a capability for a word it only begins like", ve.Hint)
	}
}
