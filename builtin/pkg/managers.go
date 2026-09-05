package pkg

import (
	"context"
	"regexp"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func managersCapability() plugin.Capability {
	return host(plugin.Capability{
		ID:         "pkg.managers",
		Summary:    "Which package managers this machine has, with version and path — and which of the ones pkg knows are not here",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "One row per manager pkg knows, present or not. Detection is a $PATH lookup " +
			"and the version is the manager's own `--version`, so this answers in a moment " +
			"where pkg overview asks every manager what is behind first. An absent row is " +
			"the diagnostic: a manager installed in a shell whose $PATH this process does " +
			"not share shows up here as absent, and that is why pkg overview finds nothing.",
		Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
			if verr := supported(); verr != nil {
				return nil, verr
			}
			return managersTable(ctx), nil
		},
	})
}

// managersTable is the answer. The first column is named `manager` for
// pkg.outdated's input, so `o` on a row in the TUI lists that manager's
// packages with nothing to type.
func managersTable(ctx context.Context) view.Table {
	t := view.Table{Columns: []view.Column{
		{Name: "manager"},
		{Name: "Binary"},
		{Name: "Path"},
		{Name: "Version"},
		{Name: "Root"},
		{Name: "Status", Kind: view.KindStatus},
	}}
	for _, m := range managers() {
		root := "-"
		if m.root {
			root = "yes"
		}
		path, err := lookPath(m.bin)
		if err != nil {
			t.Rows = append(t.Rows, []string{m.name, m.bin, "-", "-", root, "absent"})
			continue
		}
		t.Rows = append(t.Rows, []string{m.name, m.bin, path, managerVersion(ctx, m), root, "ok"})
	}
	t.Total = len(t.Rows)
	return t
}

// managerVersion asks the manager for its version and reads the number out of
// whatever it prints. Every manager here answers `--version` except go, whose
// spelling is `go version`; the outputs range from a bare "10.8.2" to
// "Homebrew 4.3.10" to pacman's multi-line banner, and the version is the
// first thing in the first line that looks like one.
func managerVersion(ctx context.Context, m manager) string {
	argv := m.version
	if argv == nil {
		argv = []string{m.bin, "--version"}
	}
	out, _, verr := run(ctx, argv[0], argv[1:]...)
	if verr != nil {
		return "-"
	}
	return parseVersion(out)
}

var versionRE = regexp.MustCompile(`\d+\.\d+[0-9A-Za-z.\-+]*`)

func parseVersion(out string) string {
	line := firstLine(out, "")
	v := versionRE.FindString(line)
	if v == "" {
		return "-"
	}
	return v
}
