package pkg

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// upgradeTimeout bounds one upgrade. A distribution upgrade downloads and
// installs for minutes; past this something is stuck on a prompt rta cannot
// answer, and the person needs the terminal back.
const upgradeTimeout = 20 * time.Minute

// runUpgrade is the seam for the one mutating exec: tests record what would
// have run, and the real one streams the manager's own output to the
// terminal, because an upgrade's progress is something to watch.
var runUpgrade = func(ctx context.Context, argv []string) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

var isRoot = func() bool { return os.Geteuid() == 0 }

func upgradeCapability() plugin.Capability {
	return host(plugin.Capability{
		ID:      "pkg.upgrade",
		Summary: "Bring one manager's packages, one package, or one of your own binaries up to date",
		// Destructive, and off the MCP surface with the rest of the
		// namespace (host's wrapper). It mutates the host — replaces binaries
		// on $PATH, and under a system manager touches everything the machine
		// runs — and an agent holding that is the authority-expanding shape
		// the harness deny lists exist for. Not "with a grant": not here.
		Safety:     plugin.Destructive,
		Idempotent: false,
		Scope:      "target",
		Description: "One target per call, never everything: a manager name upgrades what that " +
			"manager has behind (or one --package of it), and a name from `plugins: pkg: " +
			"tools:` installs that binary's latest GitHub release in place — fetched, hashed, " +
			"checked against the digest the release publishes, extracted, and swapped in " +
			"atomically, the path plugin install walks.\n\n" +
			"rta never runs sudo. apt, dnf, apk and pacman need root: as root this runs " +
			"them; otherwise it prints the exact command and refuses. A release that " +
			"publishes no digest is refused unless --unverified says you accept that.\n\n" +
			"--dry-run prints what would run.",
		Inputs: []plugin.Field{
			{Name: "target", Type: plugin.String, Positional: true, Required: true,
				Help: "a manager (brew, apt, mise, npm, go, …) or a tool from `plugins: pkg: tools:`",
				Suggest: func(ctx context.Context, req plugin.Request) []string {
					names := managerNames()
					if tools, verr := parseTools(req.StringSlice("tools")); verr == nil {
						for _, t := range tools {
							names = append(names, t.Bin)
						}
					}
					return names
				}},
			{Name: "package", Type: plugin.String,
				Help: "one package under that manager; everything it has behind when omitted (cargo and go need one)"},
			{Name: "unverified", Type: plugin.Bool, Local: true,
				Help: "install a release that publishes no digest — on your word alone"},
			toolsField(),
		},
		Run: runUpgradeCapability,
	})
}

func runUpgradeCapability(ctx context.Context, req plugin.Request) (view.View, error) {
	if verr := supported(); verr != nil {
		return nil, verr
	}
	target := strings.TrimSpace(req.String("target"))
	pkg := strings.TrimSpace(req.String("package"))

	if m, ok := managerByName(target); ok {
		return upgradeManager(ctx, req, m, pkg)
	}
	tools, verr := parseTools(req.StringSlice("tools"))
	if verr != nil {
		return nil, verr
	}
	for _, t := range tools {
		if t.Bin == target {
			if pkg != "" {
				return nil, view.Errorf("pkg.upgrade.package", "--package does not apply to a tool; %s is one binary", target)
			}
			v, verr := installTool(ctx, newRegistryClient(), t, req.Bool("unverified"), req.DryRun)
			if verr != nil {
				return nil, verr
			}
			return v, nil
		}
	}
	return nil, view.Errorf("pkg.upgrade.unknown", "%q is neither a manager pkg knows nor a tool in `plugins: pkg: tools:`", target).
		WithHint("managers: " + strings.Join(allManagerNames(), ", ") + "; tools are the bin names in your config")
}

func upgradeManager(ctx context.Context, req plugin.Request, m manager, pkg string) (view.View, error) {
	if _, err := lookPath(m.bin); err != nil {
		return nil, view.Errorf("pkg.manager.missing", "%s is not on this machine's PATH", m.bin)
	}
	argv := m.upgrade(pkg)
	if argv == nil {
		return nil, view.Errorf("pkg.upgrade.package", "%s upgrades one package at a time", m.name).
			WithHint("`rta pkg outdated " + m.name + "` lists them; pass one with --package")
	}
	if m.name == "go" && pkg != "" {
		// `go install` wants the package path, and the binary carries it.
		target, verr := goInstallTarget(ctx, pkg)
		if verr != nil {
			return nil, verr
		}
		argv = []string{"go", "install", target + "@latest"}
	}
	cmd := strings.Join(argv, " ")
	if m.root && !isRoot() {
		return nil, view.Errorf("pkg.upgrade.root", "%s needs root, and rta is not root", m.name).
			WithHint("rta never runs sudo; run it yourself: sudo " + cmd)
	}
	if req.DryRun {
		return view.Text{Body: "would run: " + cmd}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, upgradeTimeout)
	defer cancel()
	if err := runUpgrade(ctx, argv); err != nil {
		return nil, view.Errorf("pkg.upgrade.failed", "%s: %v", cmd, err).
			WithHint("the manager's own output above says why")
	}
	return view.Text{Body: "ran: " + cmd}, nil
}
