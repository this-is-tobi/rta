package pkg

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// The language-level global installers. Half of them can say what is behind
// on their own; the other half only list what is installed, and the latest
// version comes from their registry — with the name taken from the installed
// list, which is what keeps the read ungated.

func pipxManager() manager {
	return manager{
		name: "pipx", bin: "pipx",
		list: func(ctx context.Context, c *registryClient) ([]outdated, *view.Error) {
			out, _, verr := run(ctx, "pipx", "list", "--json")
			if verr != nil {
				return nil, verr
			}
			var doc struct {
				Venvs map[string]struct {
					Metadata struct {
						Main struct {
							Package string `json:"package"`
							Version string `json:"package_version"`
						} `json:"main_package"`
					} `json:"metadata"`
				} `json:"venvs"`
			}
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				return nil, view.Errorf("pkg.pipx.unreadable", "pipx list --json could not be read: %v", err)
			}
			var rows []outdated
			for _, v := range doc.Venvs {
				name, cur := v.Metadata.Main.Package, v.Metadata.Main.Version
				latest, verr := c.latestPyPI(ctx, name)
				if verr != nil {
					return nil, verr
				}
				if latest != "" && semverLess(cur, latest) {
					rows = append(rows, outdated{"pipx", name, cur, latest})
				}
			}
			return rows, nil
		},
		upgrade: func(pkg string) []string {
			if pkg == "" {
				return []string{"pipx", "upgrade-all"}
			}
			return []string{"pipx", "upgrade", pkg}
		},
	}
}

func uvManager() manager {
	return manager{
		name: "uv", bin: "uv",
		list: func(ctx context.Context, c *registryClient) ([]outdated, *view.Error) {
			// `uv tool list` prints `name v1.2.3` then `- binary` lines.
			out, _, verr := run(ctx, "uv", "tool", "list")
			if verr != nil {
				return nil, verr
			}
			var rows []outdated
			for _, line := range lines(out) {
				if strings.HasPrefix(line, "-") || strings.HasPrefix(line, " ") {
					continue
				}
				f := strings.Fields(line)
				if len(f) < 2 {
					continue
				}
				name, cur := f[0], strings.TrimPrefix(f[1], "v")
				latest, verr := c.latestPyPI(ctx, name)
				if verr != nil {
					return nil, verr
				}
				if latest != "" && semverLess(cur, latest) {
					rows = append(rows, outdated{"uv", name, cur, latest})
				}
			}
			return rows, nil
		},
		upgrade: func(pkg string) []string {
			if pkg == "" {
				return []string{"uv", "tool", "upgrade", "--all"}
			}
			return []string{"uv", "tool", "upgrade", pkg}
		},
	}
}

func npmManager() manager {
	return manager{
		name: "npm", bin: "npm",
		list: func(ctx context.Context, _ *registryClient) ([]outdated, *view.Error) {
			// Exit 1 means "something is outdated" and the JSON is still
			// the answer: {"name": {"current": "1.0.0", "wanted": …, "latest": "1.2.0"}}
			out, _, verr := run(ctx, "npm", "outdated", "-g", "--json")
			if verr != nil {
				return nil, verr
			}
			if strings.TrimSpace(out) == "" {
				return nil, nil
			}
			// npm can write more than one JSON document to stdout — an
			// update notice or an empty {} before the answer, depending on
			// the version — so the answer is the last document that has
			// any packages in it.
			type entry struct {
				Current string `json:"current"`
				Latest  string `json:"latest"`
			}
			var doc map[string]entry
			dec := json.NewDecoder(strings.NewReader(out))
			for {
				var one map[string]entry
				if err := dec.Decode(&one); err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					if doc != nil {
						break
					}
					return nil, view.Errorf("pkg.npm.unreadable", "npm outdated -g --json could not be read: %v", err)
				}
				if len(one) > 0 || doc == nil {
					doc = one
				}
			}
			var rows []outdated
			for name, v := range doc {
				rows = append(rows, outdated{"npm", name, v.Current, v.Latest})
			}
			return rows, nil
		},
		upgrade: func(pkg string) []string {
			if pkg == "" {
				return []string{"npm", "update", "-g"}
			}
			return []string{"npm", "install", "-g", pkg + "@latest"}
		},
	}
}

func bunManager() manager {
	return manager{
		name: "bun", bin: "bun",
		list: func(ctx context.Context, c *registryClient) ([]outdated, *view.Error) {
			// `bun pm ls -g` prints a tree: `├── name@1.2.3`.
			out, _, verr := run(ctx, "bun", "pm", "ls", "-g")
			if verr != nil {
				return nil, verr
			}
			var rows []outdated
			for _, line := range lines(out) {
				line = strings.TrimLeft(line, "│├└─ ")
				at := strings.LastIndex(line, "@")
				if at <= 0 {
					continue
				}
				name, cur := line[:at], line[at+1:]
				latest, verr := c.latestNPM(ctx, name)
				if verr != nil {
					return nil, verr
				}
				if latest != "" && semverLess(cur, latest) {
					rows = append(rows, outdated{"bun", name, cur, latest})
				}
			}
			return rows, nil
		},
		upgrade: func(pkg string) []string {
			if pkg == "" {
				return []string{"bun", "update", "-g"}
			}
			return []string{"bun", "install", "-g", pkg + "@latest"}
		},
	}
}

func cargoManager() manager {
	return manager{
		name: "cargo", bin: "cargo",
		note: "upgrades one crate at a time — name it",
		list: func(ctx context.Context, c *registryClient) ([]outdated, *view.Error) {
			// `cargo install --list` prints `name v1.2.3:` then indented
			// binary names.
			out, _, verr := run(ctx, "cargo", "install", "--list")
			if verr != nil {
				return nil, verr
			}
			var rows []outdated
			for _, line := range lines(out) {
				if strings.HasPrefix(line, " ") || !strings.HasSuffix(line, ":") {
					continue
				}
				f := strings.Fields(strings.TrimSuffix(line, ":"))
				if len(f) < 2 {
					continue
				}
				name, cur := f[0], strings.TrimPrefix(f[1], "v")
				latest, verr := c.latestCrate(ctx, name)
				if verr != nil {
					return nil, verr
				}
				if latest != "" && semverLess(cur, latest) {
					rows = append(rows, outdated{"cargo", name, cur, latest})
				}
			}
			return rows, nil
		},
		upgrade: func(pkg string) []string {
			if pkg == "" {
				return nil
			}
			return []string{"cargo", "install", pkg}
		},
	}
}

func gemManager() manager {
	return manager{
		name: "gem", bin: "gem",
		list: func(ctx context.Context, _ *registryClient) ([]outdated, *view.Error) {
			// `gem outdated` prints `name (1.0.0 < 1.2.0)`.
			out, _, verr := run(ctx, "gem", "outdated")
			if verr != nil {
				return nil, verr
			}
			var rows []outdated
			for _, line := range lines(out) {
				name, rest, ok := strings.Cut(line, " (")
				if !ok {
					continue
				}
				cur, latest, ok := strings.Cut(strings.TrimSuffix(rest, ")"), " < ")
				if !ok {
					continue
				}
				rows = append(rows, outdated{"gem", name, cur, latest})
			}
			return rows, nil
		},
		upgrade: func(pkg string) []string {
			if pkg == "" {
				return []string{"gem", "update"}
			}
			return []string{"gem", "update", pkg}
		},
	}
}

// goManager reads the binaries `go install` placed in GOBIN (or GOPATH/bin):
// each one embeds its module path and version, which `go version -m` prints,
// and the module proxy answers @latest — no GitHub, no rate limit, and no
// config, because the binary already says where it came from.
func goManager() manager {
	return manager{
		name: "go", bin: "go",
		list: func(ctx context.Context, c *registryClient) ([]outdated, *view.Error) {
			dir, verr := goBinDir(ctx)
			if verr != nil {
				return nil, verr
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return nil, nil
			}
			var rows []outdated
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				out, _, verr := run(ctx, "go", "version", "-m", filepath.Join(dir, e.Name()))
				if verr != nil {
					return nil, verr
				}
				module, cur := goModuleOf(out)
				if module == "" || cur == "" || cur == "(devel)" {
					continue
				}
				latest, verr := c.latestGoModule(ctx, module)
				if verr != nil {
					return nil, verr
				}
				if latest != "" && semverLess(cur, latest) {
					rows = append(rows, outdated{"go", e.Name(), cur, latest})
				}
			}
			return rows, nil
		},
		upgrade: func(pkg string) []string {
			if pkg == "" {
				return nil
			}
			return []string{"go", "install", pkg + "@latest"}
		},
		note: "upgrades one binary at a time — name the binary; the module path is read from it",
	}
}

func goBinDir(ctx context.Context) (string, *view.Error) {
	out, _, verr := run(ctx, "go", "env", "GOBIN")
	if verr != nil {
		return "", verr
	}
	if dir := strings.TrimSpace(out); dir != "" {
		return dir, nil
	}
	out, _, verr = run(ctx, "go", "env", "GOPATH")
	if verr != nil {
		return "", verr
	}
	return filepath.Join(strings.TrimSpace(out), "bin"), nil
}

// goModuleOf reads `go version -m` output: the `path` line is the package
// the binary was built from, the `mod` line is its module and version.
func goModuleOf(out string) (pkgPath, version string) {
	for _, line := range lines(out) {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "path":
			pkgPath = f[1]
		case "mod":
			if len(f) >= 3 {
				version = f[2]
			}
		}
	}
	return pkgPath, version
}

// goInstallTarget is what `go install <x>@latest` needs for a binary in
// GOBIN: the package path the binary was built from.
func goInstallTarget(ctx context.Context, bin string) (string, *view.Error) {
	dir, verr := goBinDir(ctx)
	if verr != nil {
		return "", verr
	}
	out, _, verr := run(ctx, "go", "version", "-m", filepath.Join(dir, bin))
	if verr != nil {
		return "", verr
	}
	pkgPath, _ := goModuleOf(out)
	if pkgPath == "" {
		return "", view.Errorf("pkg.go.unknown", "%s in %s was not built by go install, or carries no module path", bin, dir)
	}
	return pkgPath, nil
}
