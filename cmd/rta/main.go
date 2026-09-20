// Command rta is the Rule Them All CLI.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/app"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/stdio"
)

// version and commit are set by the linker at release time
// (-X main.version=..., -X main.commit=...).
var (
	version = "dev"
	commit  = ""
)

// buildCommit is the revision this binary was built from.
//
// **An unstamped build is not an anonymous one.** The Go toolchain records
// `vcs.revision` in every binary built from a checkout, and rta was throwing
// it away: a `go install` build reported `rta version dev` while carrying the
// exact commit it came from. For a tool whose own argument is that
// authorization must attach to an artifact rather than to a name, being
// unable to say which artifact it is was the wrong way round.
func buildCommit() string {
	if commit != "" {
		return commit
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}

// buildVersion is the version this binary reports, and records as itself.
//
// The same gap as buildCommit's, one field along. `go install …@v0.23.0`
// stamps nothing, so the binary said `rta version dev` while the toolchain
// had written v0.23.0 into its build info as the module version it resolved
// — internal/app's releasedVersion already reads that field to pin a
// scaffolded plugin's SDK, and `--version` was the one reader still ignoring
// it. The cost was not cosmetic: session.SetSelf records this string so the
// doctor can compare an open MCP server's build against the process asking,
// and two go-installed builds of different releases compared as "dev" and
// "dev", which is to say not at all.
//
// A checkout build carries "(devel)" there, which is no version, and keeps
// saying dev; its commit is still reported beside it. A pseudo-version from
// `go install …@main` is kept as it is — long, but exactly which artifact
// this is, which is the whole point.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	return versionFrom(version, info.Main.Version)
}

// versionFrom prefers a linker stamp and falls back to the module version
// the toolchain recorded, unless that is the "(devel)" a checkout build gets.
//
// The module version loses its leading v. A release archive is stamped
// `0.24.0` by GoReleaser and a `go install` of the same tag records
// `v0.24.0`, and session.OtherBuilds compares the two strings as they are:
// left unequal, a doctor run from the archive would report a server started
// from the go-installed binary as a different build of the same release.
func versionFrom(stamp, module string) string {
	if stamp != "dev" {
		return stamp
	}
	if module == "" || module == "(devel)" {
		return stamp
	}
	return strings.TrimPrefix(module, "v")
}

func main() {
	// Before anything reads a secret into this address space. Confinement
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
	// The notice about artifacts found and not run is emitted by the root
	// command's PersistentPreRun rather than here, because it has to know
	// which output format was asked for: a sentence of English above
	// somebody's `-o json` is a line they have to strip out of what they
	// copied off the screen.

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

	root := app.NewRoot(reg, buildVersion())

	err = fang.Execute(ctx, root,
		fang.WithVersion(buildVersion()),
		fang.WithCommit(buildCommit()),
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
