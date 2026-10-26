package tui

import (
	"strings"
	"testing"
)

// A profile's name and a plugin key are a config file's, which a drop-in or a
// repository can bring, and every pane shows them cleaned; the bar that asks
// whether to delete one drew it raw, at the one moment the operator reads
// exactly what will go. A right-to-left override reorders what follows it and
// an escape sequence can hide it. The question names them as a listing does:
// as they are when they read as themselves, quoted with what is not visible
// written out when they do not.
func TestTheDeleteQuestionNamesWhatItDeletesAsItIs(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	odd := "prod\u202egnitset"
	for _, tc := range []struct {
		screen mode
		open   string
	}{
		{modeProfiles, ""},
		{modeProfilePlugins, "staging\x1b]8;;https://example.invalid\x07"},
	} {
		m.mode, m.armedDelete, m.profileOpen = tc.screen, odd, tc.open
		bar := m.footerFor(tc.screen)
		if strings.Contains(bar, "\u202e") || strings.Contains(bar, "\x1b]8;;") {
			t.Errorf("the question on screen %v draws the name raw: %q", tc.screen, bar)
		}
		if !strings.Contains(plain(bar), "prod") {
			t.Errorf("the question on screen %v does not name it: %q", tc.screen, plain(bar))
		}
	}
	m.mode, m.armedDelete, m.profileOpen = modeProfiles, "prod", ""
	if bar := plain(m.footerFor(modeProfiles)); !strings.Contains(bar, "delete profile prod and revoke") {
		t.Errorf("a name that reads as itself is not shown as it is: %q", bar)
	}
}
