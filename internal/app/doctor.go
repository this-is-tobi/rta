package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rule-them-all/builtin/kv"
	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/render/cli"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// newDoctorCommand implements `rta doctor`: one command that checks the
// environment and reports actionable state (PROJECT.md §5.1). Checks grow
// with the features that need them (config, plugins, keyring...).
func newDoctorCommand(reg *registry.Registry, opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check rta's environment and report actionable findings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := cli.ParseFormat(opts.output)
			if err != nil {
				return err
			}
			renderOpts := cli.Options{Format: format, NoColor: opts.noColor || !isTTY(), Width: termWidth()}
			return cli.Render(cmd.OutOrStdout(), doctorReport(reg), renderOpts)
		},
	}
}

func doctorReport(reg *registry.Registry) view.View {
	t := view.Table{Columns: []view.Column{
		{Name: "Check"},
		{Name: "Status", Kind: view.KindStatus},
		{Name: "Detail"},
	}}
	add := func(check, status, detail string) {
		t.Rows = append(t.Rows, []string{check, status, detail})
	}

	// Registry health.
	caps := reg.Capabilities()
	add("built-ins", "ok", fmt.Sprintf("%d plugins, %d capabilities", len(reg.Plugins()), len(caps)))

	// Terminal.
	if isTTY() {
		add("terminal", "ok", "stdout is a TTY — styled output enabled")
	} else {
		add("terminal", "info", "stdout is piped — plain output")
	}

	// Config: zero-config is healthy; a broken file is an actionable error.
	cfgPath := config.Path()
	if _, statErr := os.Stat(cfgPath); statErr != nil {
		add("config", "info", "no config file — zero-config mode (`rta init` creates "+cfgPath+")")
	} else if cfg, err := config.Load(); err != nil {
		add("config", "error", err.Error())
	} else {
		detail := cfgPath
		if cfg.Output != "" {
			detail += fmt.Sprintf(" (output=%s)", cfg.Output)
		}
		switch n := len(cfg.Dashboard.Tiles); {
		case n > 0:
			detail += fmt.Sprintf(", %d dashboard tiles", n)
		default:
			detail += ", automatic dashboard (one tile per plugin)"
			if h := len(cfg.Dashboard.Hidden); h > 0 {
				detail += fmt.Sprintf(", %d hidden", h)
			}
		}
		add("config", "ok", detail)
	}

	// Standing agent permissions. Worth a line of its own: a grant issued
	// yesterday and forgotten is exactly the thing a health check should
	// surface, and the answer is usually "none".
	if grants, verr := grant.Load(); verr != nil {
		add("agent grants", "error", verr.Message)
	} else if len(grants) == 0 {
		add("agent grants", "ok", "none active — agents cannot write or destroy anything")
	} else {
		named := make([]string, 0, len(grants))
		for _, g := range grants {
			named = append(named, strings.TrimSpace(g.Target+" "+g.Scope))
		}
		add("agent grants", "info", fmt.Sprintf("%d active: %s (`rta grant list`)",
			len(grants), strings.Join(named, ", ")))
	}

	// What an MCP server launched from this shell would inherit. The store is
	// encrypted, but encryption only helps while the key is somewhere the
	// reader is not — and a server started from here starts with this
	// environment.
	if unlockable, from := kv.Unlockable(); from == "no store" {
		add("kv store", "ok", "none yet — `rta kv init --generate` sets one up")
	} else if unlockable {
		add("kv store", "info", "unlocks from this environment ("+from+
			") — an MCP server started here can read secrets, bounded only by grants")
	} else if locked := kv.LockedIdentity(); locked != "" {
		add("kv store", "ok", "locked to "+locked+", which is itself passphrase-protected — "+
			"an MCP server started here would be asked for a passphrase it cannot answer")
	} else {
		add("kv store", "ok", "no key material here — an MCP server started from this shell could not open it")
	}

	// Exec-tier plugins on $PATH (rta-<name>), discovery convention only for
	// now — execution support lands with the plugin host (M3).
	if found := execPlugins(); len(found) > 0 {
		add("exec plugins", "info", strings.Join(found, ", ")+" (execution support lands in M3)")
	} else {
		add("exec plugins", "ok", "none found on $PATH")
	}

	// MCP clients we can install into.
	if _, err := exec.LookPath("claude"); err == nil {
		add("claude CLI", "ok", "found — `rta mcp install claude` available")
	} else {
		add("claude CLI", "info", "not found — `rta mcp serve` still works with any MCP client")
	}

	t.Total = len(t.Rows)
	return t
}

// execPlugins scans $PATH for rta-* binaries (kubectl/gh convention).
func execPlugins() []string {
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, "rta-") || e.IsDir() {
				continue
			}
			if info, err := e.Info(); err == nil && info.Mode()&0o111 != 0 {
				seen[name] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
