// Command rta is the Rule Them All CLI.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rule-them-all/internal/app"
	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/pluginconf"
	"github.com/this-is-tobi/rule-them-all/internal/pluginhost"
	"github.com/this-is-tobi/rule-them-all/internal/render/theme"
	"github.com/this-is-tobi/rule-them-all/internal/stdio"
)

// version is set by the linker at release time (-X main.version=...).
var version = "dev"

func main() {
	// Before anything reads a secret into this address space. ADR 0012
	// documented this in two places and it was implemented in none, which is
	// the worst shape a security claim can have.
	pluginhost.HardenSelf()

	// First, before anything at all: LoadPlugins below spawns every installed
	// plugin to read its declarations, and go-plugin gives each child
	// os.Stdin unconditionally (client.go:659, no opt-out). Whatever is on
	// fd 0 — an agent's JSON-RPC stream, a passphrase being typed — belongs
	// to rta, and a plugin that receives it is reading the user's input.
	// Surfaces that need it ask stdio.Real().
	if err := stdio.Claim(); err != nil {
		fmt.Fprintln(os.Stderr, "rta: cannot take standard input:", err)
		os.Exit(1)
	}

	reg, err := app.NewRegistry()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rta: broken built-in registration:", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// External plugins, before the command tree is built: cobra materializes
	// one command per capability, so a capability that arrives afterwards has
	// nothing to attach to.
	//
	// A plugin that fails to load is reported and skipped. Exiting instead
	// would mean any third-party binary on $PATH can stop rta from starting,
	// which is a thing a user experiences once and then stops installing
	// plugins over.
	host, problems := app.LoadPlugins(ctx, reg, os.Stderr)
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "rta:", p)
	}
	// Everything else worth knowing about a loaded plugin goes to `rta
	// doctor` instead of here: this runs before every command, and a fact
	// about the installation printed on every command is noise.
	app.SetLoadedPlugins(host.Loaded())
	// And what it found and refused to launch, so a plugin that is installed
	// and silent is visible everywhere plugins are listed rather than only in
	// the line that scrolled past at startup.
	untrusted := host.Untrusted()
	app.SetUntrustedPlugins(untrusted)
	// One line, and only when somebody is there to read it.
	//
	// A trust gate's failure mode is silence: a plugin installed, present and
	// doing nothing looks exactly like one that was never installed, and the
	// operator has no reason to suspect a decision is outstanding. So it is
	// said — once per run, however many are waiting, naming the command that
	// resolves it.
	//
	// Gated on stderr being a terminal, which is the question actually being
	// asked: is a person watching this stream. A script's stderr is somewhere
	// nobody is reading, and repeating a pending decision into it on every
	// invocation is how the message stops being read anywhere. `rta plugin
	// list` and `rta doctor` carry it in full for the times somebody looks.
	// Split by whether trusting would actually help. An artifact whose name
	// something already answers to is not a pending decision — approving it
	// earns a namespace collision on the next start — so it must not be
	// counted among the plugins waiting to be loaded, or offered that remedy.
	var waiting, colliding []string
	for _, u := range untrusted {
		if u.Taken {
			colliding = append(colliding, u.Name)
			continue
		}
		waiting = append(waiting, u.Name)
	}
	if term.IsTerminal(int(os.Stderr.Fd())) {
		if len(waiting) > 0 {
			fmt.Fprintf(os.Stderr,
				"rta: %d plugin(s) installed and not run: %s — `rta plugin trust <name>` to load, "+
					"`rta plugin trust` to see them\n",
				len(waiting), strings.Join(waiting, ", "))
		}
		if len(colliding) > 0 {
			fmt.Fprintf(os.Stderr,
				"rta: %d artifact(s) on $PATH name something already registered and were not run: "+
					"%s — trusting one would collide; remove or rename the file\n",
				len(colliding), strings.Join(colliding, ", "))
		}
	}

	// After the plugins, because a section is matched to the artifact that
	// registered the namespace and there is nothing to match against until
	// they are loaded. Before the command tree, because every surface reads
	// the same answer and a capability must not discover its own
	// configuration halfway through a run.
	//
	// Problems are not fatal and are not printed here. A config file naming a
	// plugin that is not installed today is an ordinary state — uninstalled,
	// not installed yet, one file shared across machines — and refusing to
	// start over it would make config a liability rather than a convenience.
	// `rta doctor` reports them.
	cfg, cfgErr := config.LoadFile()
	if cfgErr != nil {
		fmt.Fprintln(os.Stderr, "rta:", cfgErr)
	}
	app.SetPluginConfig(pluginconf.Resolve(cfg, reg.Origin))

	// Before the command tree, so every renderer — CLI and TUI alike — reads
	// the same palette from its first line of output; fang's own help and
	// error styling is separate and untouched. Not fatal for the same reason
	// SetPluginConfig's problems are not: a bad hex costs its own field a
	// color, never the run.
	app.SetThemeProblems(theme.Apply(cfg.Theme))

	root := app.NewRoot(reg, version)

	err = fang.Execute(ctx, root,
		fang.WithVersion(version),
		fang.WithErrorHandler(errorHandler(root)),
	)

	// Explicitly, not deferred: os.Exit does not run deferred functions, so a
	// defer here would leave a plugin subprocess behind on every single
	// invocation of rta — including the ones that only printed help.
	host.CloseAll()
	os.Exit(app.ExitCode(err))
}

// errorHandler lets rta format the errors it owns and fang style the rest.
//
// The split is app.RenderTopLevelError's: an error already printed by the
// command that produced it is swallowed, a *view.Error is rendered in the
// format the caller asked for, and anything else — a usage mistake, a bad
// flag — is fang's, because fang is what makes those look like the rest of
// the help.
// It closes over the root because the render options are read back off its
// parsed flags — which is where the config file's default lives too, so what
// an error is formatted with is exactly what the command would have used.
func errorHandler(root *cobra.Command) fang.ErrorHandler {
	return func(w io.Writer, styles fang.Styles, err error) {
		if app.RenderTopLevelError(w, root, err) {
			return
		}
		fang.DefaultErrorHandler(w, styles, err)
	}
}
