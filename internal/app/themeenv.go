package app

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/render/theme"
)

// The CLI draws in the colours of the environment it is about.
//
// A profile has a colour and a theme, and switching the environment switches
// the screen the way it switches the dashboard: the one place a person looks to
// learn where they are is the output itself, and a result drawn in the same
// colours whichever environment produced it is one more thing that looks alike.
// The palette is package state read when a result is drawn (internal/render/
// theme), so what has to be decided is only when it is set: once at startup for
// the environment that is switched on — help and everything that never reaches
// a command's own run draw in that — and again by the command that is about to
// run, which is the first point where its own flags are known.

// ApplyTheme sets the palette for the environment env, "" being none, and
// records what in the configuration could not be used for `rta doctor`.
//
// The problems recorded are the operator's own block's. An environment's are
// every profile's to report whether or not it is the one in force, and are
// profile.Notes' — listed beside the profile by `rta profile list`, `show` and
// `doctor` — so reporting them here too would say each twice, and only for the
// environment that happened to be on.
func ApplyTheme(cfg config.Config, env string) {
	var own []theme.Problem
	for _, p := range theme.ApplyLayers(profile.ThemeLayers(cfg, env)...) {
		if !strings.HasPrefix(p.Source, "profiles.") {
			own = append(own, p)
		}
	}
	SetThemeProblems(own)
}

// environmentOf is the environment one invocation is drawn for, "" for none:
// --profile on the call when it has one, else the one switched on, and none for
// a call that leaves it.
//
// **The same answer the call's own resolution gives** (resolveProfile), which is
// the point: a command that names an environment, or leaves the one that is on,
// is about that and not the one whose colours were painted at startup. An
// instance reference is its environment (profile.ThemeFloor says why). A
// connection typed on the line is where the call goes, over whatever is on, and
// is no environment of anybody's — the badge says `ad hoc` for it — so it is
// drawn in the operator's own colours.
//
// A command with no `--profile` of its own takes the one that is switched on:
// it resolves nothing, so nothing on its line says otherwise. The `--profile` of
// `rta dashboard add` is a tile's pin and not the environment the command runs
// in, which is why the flag is read only where `--no-profile` sits beside it —
// on exactly the capabilities a profile can fill.
func environmentOf(cmd *cobra.Command) string {
	if cmd != nil && cmd.Flags().Lookup("no-profile") != nil {
		if noProfile(cmd) {
			return ""
		}
		if _, stated := adHocWords(cmd); stated {
			return ""
		}
		if f := cmd.Flags().Lookup("profile"); f != nil {
			if ref := strings.TrimSpace(f.Value.String()); ref != "" {
				return config.RefName(ref)
			}
		}
	}
	return profile.Active()
}
