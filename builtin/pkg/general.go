package pkg

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// The managers that run as the person: Homebrew and mise.

func brewManager() manager {
	return manager{
		name: "brew", bin: "brew",
		list: func(ctx context.Context, _ *registryClient) ([]outdated, *view.Error) {
			out, _, verr := run(ctx, "brew", "outdated", "--json=v2")
			if verr != nil {
				return nil, verr
			}
			var doc struct {
				Formulae []struct {
					Name      string   `json:"name"`
					Installed []string `json:"installed_versions"`
					Current   string   `json:"current_version"`
				} `json:"formulae"`
				Casks []struct {
					Name      string   `json:"name"`
					Installed []string `json:"installed_versions"`
					Current   string   `json:"current_version"`
				} `json:"casks"`
			}
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				return nil, view.Errorf("pkg.brew.unreadable", "brew outdated --json=v2 could not be read: %v", err)
			}
			var rows []outdated
			for _, f := range doc.Formulae {
				rows = append(rows, outdated{"brew", f.Name, last(f.Installed), f.Current})
			}
			for _, c := range doc.Casks {
				rows = append(rows, outdated{"brew", c.Name, last(c.Installed), c.Current})
			}
			return rows, nil
		},
		upgrade: func(pkg string) []string {
			if pkg == "" {
				return []string{"brew", "upgrade"}
			}
			return []string{"brew", "upgrade", pkg}
		},
	}
}

func last(v []string) string {
	if len(v) == 0 {
		return "-"
	}
	return v[len(v)-1]
}

func miseManager() manager {
	return manager{
		name: "mise", bin: "mise",
		list: func(ctx context.Context, _ *registryClient) ([]outdated, *view.Error) {
			out, _, verr := run(ctx, "mise", "outdated", "--json")
			if verr != nil {
				return nil, verr
			}
			// {"node": {"current": "20.1.0", "latest": "22.0.0", ...}, ...}
			var doc map[string]struct {
				Current string `json:"current"`
				Latest  string `json:"latest"`
			}
			if strings.TrimSpace(out) == "" {
				return nil, nil
			}
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				return nil, view.Errorf("pkg.mise.unreadable", "mise outdated --json could not be read: %v", err)
			}
			var rows []outdated
			for tool, v := range doc {
				rows = append(rows, outdated{"mise", tool, v.Current, v.Latest})
			}
			return rows, nil
		},
		upgrade: func(pkg string) []string {
			if pkg == "" {
				return []string{"mise", "upgrade"}
			}
			return []string{"mise", "upgrade", pkg}
		},
	}
}
