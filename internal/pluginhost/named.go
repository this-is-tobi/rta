package pluginhost

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// CheckPath is a path an operator names for a plugin to read under strict
// confinement (`rta plugin access add <name> --path`), made absolute, or why
// it may not be one.
//
// **The worst a named path can do is what the macOS profile already lets every
// plugin read**, and these refusals are what holds it there: that profile
// denies rta's own state and the credential locations, and lets everything
// else be read. So a path is refused:
//
//   - inside rta's own state, which no plugin reads by any grant;
//   - inside a credential location, which a plugin reaches only by declaring
//     the need for it, so that its own request is the one way there — a path
//     that could name ~/.ssh would be a need nobody declared;
//   - the home directory, or one above it, which would hand over every file of
//     it at once and is no answer to "where is the CA";
//   - one that is not there, since a grant for a path that does not exist is
//     a rule about whatever is put there later.
//
// A path that holds rta's own state or a credential location is not refused:
// the launch carves them out of it (strictPolicy), as it does out of /etc.
// Each rule is asked of the path as given and of where it leads, since the
// kernel follows a link before it looks.
func CheckPath(raw string) (string, *view.Error) {
	bad := func(format string, args ...any) *view.Error {
		return view.Errorf("plugin.access.path", format, args...).WithInput("path")
	}
	p := filepath.Clean(plugin.ExpandHome(raw))
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if _, err := os.Stat(p); err != nil {
		return "", bad("%s is not there to read: %v", p, err)
	}
	home, _ := os.UserHomeDir()
	for _, form := range withTarget(p) {
		if home != "" && slices.ContainsFunc(withTarget(home), func(h string) bool { return inside(h, form) }) {
			return "", bad("%s holds the home directory, and a plugin is given a path to read, never all of it", form).
				WithHint("name the directory the file is in")
		}
		for _, own := range append(tier1(), paths.OwnFiles()...) {
			if slices.ContainsFunc(withTarget(own), func(o string) bool { return inside(form, o) }) {
				return "", bad("%s is rta's own state, which no plugin reads", form)
			}
		}
		for need, location := range tier2() {
			if slices.ContainsFunc(withTarget(location), func(l string) bool { return inside(form, l) }) {
				return "", bad("%s is in %s, the %s credential location, which a plugin reads only when it declares that it needs it",
					form, location, need).
					WithHint("`rta plugin access add <name> " + string(need) + "` gives it to a plugin that declares it")
			}
		}
	}
	return p, nil
}
