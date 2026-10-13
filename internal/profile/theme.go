package profile

import (
	"strings"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/render/theme"
)

// What the screen is painted from while an environment is in force.
//
// The layers, lowest to highest: the built-in palette, the operator's own
// `theme:`, the environment's `color:` as the primary, and the environment's own
// `theme:`. The environment is a profile and not a connection: `--profile
// staging/analytics` is staging's colours, since an instance is one of the
// places a profile reaches and not a second environment with a screen of its
// own — an environment that painted differently depending on which of its
// databases a call touched would be two palettes under one name.
//
// Here and not in config, which does not import the render tree, and not in
// the theme package, which does not import config: this is the one place that
// knows both what a profile states and what a palette is made of, and the CLI
// and the TUI ask it the same question.

// ThemeFloor is the layers beneath an environment's own `theme:` block: the
// operator's block, and the environment's colour as the primary.
//
// **Only an environment this configuration is entitled to name.** A profile
// from a file nobody named is not honoured at all (Profile.Trusted), and what it
// says about colour is not either: a repository's own config does not get to
// recolour the screen of an environment, any more than it gets to point one at
// somewhere. The block is honoured from such a file, as the output format is,
// and was before environments had one: it is the file the person is sitting in,
// and what it can do is repaint their screen. What it must not do is make one
// status read as another — nobody who cloned a repository wrote that — so from
// a file nobody named it is held to the rule an environment's colours are
// (theme.Resolve), while the operator's own block never is: stating good and
// bad alike in a file you edit is a choice about your own screen.
//
// A name that is no profile, an empty one, and a colour that is not a colour
// each leave the operator's block alone: nothing is switched on, or what is
// switched on says nothing here, and a mistyped colour marks nothing
// (Profile.BadColor) rather than falling back to one nobody chose.
func ThemeFloor(cfg config.Config, env string) []theme.Layer {
	layers := []theme.Layer{{Source: "theme", Colors: cfg.Theme, Guarded: !cfg.Trusted()}}
	name := config.RefName(env)
	p, ok := cfg.Profiles[name]
	if !ok || !p.Trusted() {
		return layers
	}
	if p.Color != "" && !p.BadColor() {
		layers = append(layers, theme.Layer{
			Source: "profiles." + name, Colors: map[string]string{"primary": p.Color},
			As: "color", Guarded: true,
		})
	}
	return layers
}

// OwnTheme is an environment's own `theme:` as the layer it is: the topmost,
// and guarded.
func OwnTheme(name string, colors map[string]string) theme.Layer {
	return theme.Layer{Source: "profiles." + name + ".theme", Colors: colors, Guarded: true}
}

// ThemeLayers is every layer the palette is made of while env is in force —
// the empty name being no environment, which is the operator's block alone.
func ThemeLayers(cfg config.Config, env string) []theme.Layer {
	layers := ThemeFloor(cfg, env)
	name := config.RefName(env)
	if p, ok := cfg.Profiles[name]; ok && p.Trusted() && len(p.Theme) > 0 {
		layers = append(layers, OwnTheme(name, p.Theme))
	}
	return layers
}

// ThemeStanding is the entries of a profile's own `theme:` that the palette
// keeps: what ThemeProblems does not report. What a summary says the profile's
// palette is — a count, a line of slots — is this and not the block as written,
// since a line that is left out paints nothing and a first glance that counts it
// says the opposite of the warning beneath.
//
// Empty for a profile from a file nobody named, which paints nothing, and for
// one that does not exist.
func ThemeStanding(cfg config.Config, name string) map[string]string {
	p, ok := cfg.Profiles[name]
	if !ok || !p.Trusted() {
		return nil
	}
	left := map[string]bool{}
	for _, problem := range ThemeProblems(cfg, name) {
		if problem.Source == OwnTheme(name, nil).Source {
			left[problem.Field] = true
		}
	}
	standing := make(map[string]string, len(p.Theme))
	for slot, hex := range p.Theme {
		if !left[slot] {
			standing[slot] = hex
		}
	}
	return standing
}

// ThemeProblems is what a profile states about colour that the palette cannot
// use: an entry that is no colour or no slot, and one that would make a status
// read as another. Judged as the palette is built while the profile is in
// force, so a collision with the operator's own block is found; what is wrong
// with that block itself is not this profile's, and is not listed.
//
// Empty for a profile from a file nobody named, which paints nothing, and for
// one that does not exist.
func ThemeProblems(cfg config.Config, name string) []theme.Problem {
	_, problems := theme.Resolve(ThemeLayers(cfg, name)...)
	var own []theme.Problem
	for _, p := range problems {
		if strings.HasPrefix(p.Source, "profiles.") {
			own = append(own, p)
		}
	}
	return own
}
