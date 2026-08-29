package tui

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/render/theme"
)

// A style applied over text that already carries ANSI ends at the first reset
// inside it. profileCovers built its line by joining the plugin keys with a
// *styled* separator and then styling the whole result — so the separator's
// own `ESC[m` terminated the outer style, and every plugin after the first
// rendered in the terminal's default. On screen that was `pg@…` dim and
// `s3@…`/`vault@…` bright white: a status difference between plugins that
// does not exist, in the view whose entire job is saying what an environment
// reaches.
func TestEveryPluginInAProfileRowIsStyledTheSame(t *testing.T) {
	row := profileRow{
		name: "me",
		p: config.Profile{Plugins: map[string]config.Connection{
			"pg@685186a7c11a":    {},
			"s3@a586c1f10215":    {},
			"vault@877076d100df": {},
		}},
	}
	got := profileCovers(row)

	// The style's own escape sequence, taken from the theme rather than
	// written out here: a palette change must not turn this into a test of
	// what the colour used to be.
	open := strings.SplitN(theme.Faded.Render("x"), "x", 2)[0]
	if open == "" {
		t.Skip("colour is disabled in this environment, so there is nothing to assert")
	}
	for _, key := range []string{"pg@685186a7c11a", "s3@a586c1f10215", "vault@877076d100df"} {
		i := strings.Index(got, key)
		if i < 0 {
			t.Fatalf("%s is missing from the line: %q", key, got)
		}
		// Whatever immediately precedes the key has to be the style's opener.
		if !strings.HasSuffix(got[:i], open) {
			t.Errorf("%s is not styled like the others — it renders in the terminal default.\nline: %q",
				key, got)
		}
	}
}

func TestAProfileNoteStillReadsAsANote(t *testing.T) {
	// The note is deliberately a different style from the plugin keys, so
	// this is the control: fixing the nesting must not flatten the line into
	// one colour.
	row := profileRow{
		name: "me",
		p: config.Profile{
			Plugins: map[string]config.Connection{"pg@abc": {}},
			Note:    "personal profile",
		},
	}
	got := profileCovers(row)
	if !strings.Contains(got, "personal profile") {
		t.Fatalf("the note is missing: %q", got)
	}
	if !strings.Contains(got, "pg@abc") {
		t.Fatalf("the plugin key is missing: %q", got)
	}
}
