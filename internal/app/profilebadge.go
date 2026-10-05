package app

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/render/theme"
)

// BadgeStyle is how the active environment's line is drawn, and whether it is
// drawn at all.
type BadgeStyle struct {
	// MachineReadable is set when the output format is anything but pretty.
	MachineReadable bool
	// NoColor keeps the line and drops the paint.
	NoColor bool
	// ASCII draws the unmarked environment's bullet as an asterisk, for the
	// terminals cli.ASCIIOnly says cannot show it.
	ASCII bool
}

// WarnActiveProfile says which environment this command is about to reach, in
// the colour the operator gave it.
//
// **"Am I on prod?" had no answer on this surface.** `rta use prod` is a switch
// that outlives the command that made it — that is the entire point of it — so
// the twentieth command afterwards runs against production with nothing on
// screen saying so, and the operator finds out from the result. The consent
// model already covers the agent side of this: a grant names the profile, is
// visible in `rta grant list`, and expires. A person at a terminal had their
// memory. The TUI has carried this badge in its header since the switch
// existed; this is the same fact on the surface where most commands are typed.
//
// **Two rules, and the second is the default.** An environment with a `color:`
// announces itself before every command, because writing one is the operator
// saying *this* environment is worth interrupting them about. One without says
// so before every command it can change, in the plain bullet the TUI's header
// gives an unmarked environment. It used to say nothing, which left a bare
// `rta use staging` — no `--for`, so it stays on tomorrow — running every
// command of every plugin it covers against staging with no sign of it on any
// of them: kubectl's context hazard, and the one place an environment acts
// without being named.
//
// What keeps that from becoming a banner nobody reads is where it prints:
// where the environment acts, and nowhere else. "Acts" is the command's own
// word for it — a leaf command of a plugin the environment covers that takes
// `--profile`, which a capability does exactly when a profile has something to
// fill in — run without one, since a command that names its environment has
// said so itself. `rta use`, `rta doctor`, a group's help and the plugins the
// environment says nothing about stay as quiet as they were.
//
// Same two conditions as the notice above, for the same two reasons: somebody
// has to be watching the stream, and they have to have asked for prose rather
// than for something to paste into a parser.
func WarnActiveProfile(w io.Writer, cmd *cobra.Command, cfg config.Config, style BadgeStyle) {
	if style.MachineReadable {
		return
	}
	sel := profile.LoadSelection()
	now := time.Now()
	name := sel.Name(now)
	if name == "" {
		return
	}
	p, ok := cfg.Profiles[name]
	if !ok {
		return
	}
	// A colour that is not a colour marks nothing, rather than falling back to
	// one nobody chose: the default bullet below says "this environment is on",
	// which is true of it, and `rta doctor` is where the colour is reported.
	marked := p.Color != "" && !p.BadColor()
	if !marked && !actsOn(cmd, p) {
		return
	}

	label := badgeLabel(name, p.Color, marked, style)
	// The deadline rides along because it is the other half of the same
	// question — being on production and being on it for six more minutes are
	// different situations, and both are decided before the command runs.
	if left, deadline := sel.Left(now); deadline && left > 0 {
		label += " " + profile.ShortDuration(left) + " left"
	}
	fmt.Fprintln(w, label)
}

func badgeLabel(name, color string, marked bool, style BadgeStyle) string {
	switch {
	case style.NoColor:
		// The brackets are the badge when there is no colour to be a badge
		// with. Dropping the line entirely would be worse: --no-color is a
		// statement about ANSI, not about wanting to know less.
		return "[ " + name + " ]"
	case marked:
		return theme.Badge(name, color)
	case style.ASCII:
		return theme.GoodText.Render("* " + name)
	}
	return theme.GoodText.Render("● " + name)
}

// actsOn reports whether p can change what cmd does: a leaf command of a plugin
// p covers, that takes --profile and was not given one. The flag is registered
// on a capability only when a profile has an input of it to fill (see
// plugin.Profilable), so it answers "can this environment change anything
// here" without a second lookup of the capability.
func actsOn(cmd *cobra.Command, p config.Profile) bool {
	if cmd == nil || cmd.HasSubCommands() {
		return false
	}
	flag := cmd.Flags().Lookup("profile")
	if flag == nil || strings.TrimSpace(flag.Value.String()) != "" {
		return false
	}
	words := strings.Fields(cmd.CommandPath())
	return len(words) > 1 && p.Covers(words[1])
}
