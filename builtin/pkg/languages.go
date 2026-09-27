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
			out, verr := run(ctx, "pipx", "list", "--json")
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
					rows = append(rows, outdated{Manager: "pipx", Name: name, Current: cur, Latest: latest})
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
			out, verr := run(ctx, "uv", "tool", "list")
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
					rows = append(rows, outdated{Manager: "uv", Name: name, Current: cur, Latest: latest})
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
			// the answer: {"name": {"current": "1.0.0", "wanted": …, "latest": "1.2.0"}}.
			// It is also the status of every npm failure, so it is read as
			// that answer only once the answer has something in it.
			st, verr := runStatus(ctx, "npm", "outdated", "-g", "--json")
			if verr != nil {
				return nil, verr
			}
			if st.code != 0 && st.code != 1 {
				return nil, st.failed("npm")
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
			dec := json.NewDecoder(strings.NewReader(st.out))
			for {
				var raw json.RawMessage
				err := dec.Decode(&raw)
				if errors.Is(err, io.EOF) {
					break
				}
				var one map[string]entry
				if err == nil {
					if verr := npmFailure(raw); verr != nil {
						return nil, verr
					}
					err = json.Unmarshal(raw, &one)
				}
				if err != nil {
					// A later document that will not parse is only safe to
					// ignore once an earlier one actually answered. `doc !=
					// nil` was also true for the empty {} npm sometimes
					// writes first, so a malformed answer after it was
					// reported as "nothing is outdated" — a clean bill of
					// health for output nobody could read.
					if len(doc) > 0 {
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
				if versionless(v.Current, v.Latest) {
					continue
				}
				rows = append(rows, outdated{Manager: "npm", Name: name, Current: v.Current, Latest: v.Latest})
			}
			if st.code != 0 && len(rows) == 0 {
				return nil, st.failed("npm")
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

// npmFailure is npm's own account of why it could not answer, when a
// document on its stdout is one.
//
// With --json npm writes a failure to stdout as {"error": {"code": …,
// "summary": …}} and exits 1, the status it also means "something is
// outdated" by. Decoded as the answer, that was a package named error with
// no version on either side, and its row's upgrade was `npm install -g
// error@latest` — one key in the TUI from a global install of whatever the
// registry holds under that name. A package that really is called error
// carries its versions, and is left to be one.
func npmFailure(raw json.RawMessage) *view.Error {
	var doc struct {
		Error *struct {
			Code    string `json:"code"`
			Summary string `json:"summary"`
			Current string `json:"current"`
			Latest  string `json:"latest"`
		} `json:"error"`
	}
	// A document of any other shape leaves Error nil, and reading it as the
	// answer is the caller's next step, with its own refusal if it cannot.
	_ = json.Unmarshal(raw, &doc)
	if doc.Error == nil {
		return nil
	}
	e := doc.Error
	if !versionless(e.Current, e.Latest) || (e.Code == "" && e.Summary == "") {
		return nil
	}
	return view.Errorf("pkg.npm.failed", "npm outdated: %s", firstLine(e.Summary, e.Code))
}

// versionless reports whether an entry in a manager's JSON answer carries
// no version on either side. Such an entry says nothing is behind, and the
// managers that key their answer by name — npm and mise — would otherwise
// make a row of it whose upgrade installs whatever goes by that key, which
// was never a package on this machine.
func versionless(current, latest string) bool { return current == "" && latest == "" }

func bunManager() manager {
	return manager{
		name: "bun", bin: "bun",
		list: func(ctx context.Context, c *registryClient) ([]outdated, *view.Error) {
			// `bun pm ls -g` prints a tree: `├── name@1.2.3`. Before the
			// first global install there is no global package.json for it
			// to read, and it says so and exits 1: nothing installed, so
			// nothing behind.
			st, verr := runStatus(ctx, "bun", "pm", "ls", "-g")
			if verr != nil {
				return nil, verr
			}
			if st.code != 0 {
				if strings.Contains(st.reason, "No package.json was found") {
					return nil, nil
				}
				return nil, st.failed("bun")
			}
			var rows []outdated
			for _, line := range lines(st.out) {
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
					rows = append(rows, outdated{Manager: "bun", Name: name, Current: cur, Latest: latest})
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
			out, verr := run(ctx, "cargo", "install", "--list")
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
					rows = append(rows, outdated{Manager: "cargo", Name: name, Current: cur, Latest: latest})
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
			out, verr := run(ctx, "gem", "outdated")
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
				rows = append(rows, outdated{Manager: "gem", Name: name, Current: cur, Latest: latest})
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
		name: "go", bin: "go", version: []string{"go", "version"},
		list: func(ctx context.Context, c *registryClient) ([]outdated, *view.Error) {
			dir, verr := goBinDir(ctx)
			if verr != nil {
				return nil, verr
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return nil, nil //nolint:nilerr // no go bin directory means no go-installed tools, which is an answer and not a failure
			}
			var rows []outdated
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				// Exit 1 is go saying the file holds no Go build info — a
				// script somebody keeps in GOBIN. It is not a tool go install
				// placed, and not a reason to fail the ones that are.
				st, verr := runStatus(ctx, "go", "version", "-m", filepath.Join(dir, e.Name()))
				if verr != nil {
					return nil, verr
				}
				if st.code != 0 {
					continue
				}
				pkgPath, module, cur := goModuleOf(st.out)
				if pkgPath == "" || module == "" || cur == "" || cur == "(devel)" {
					continue
				}
				// The proxy answers @latest for a *module*, and the package
				// a binary was built from is usually not one: govulncheck is
				// golang.org/x/vuln/cmd/govulncheck inside golang.org/x/vuln.
				// Asked with the package path it answered 404, which read as
				// "not found" and never as outdated — every tool laid out
				// under cmd/ was reported current forever.
				latest, verr := c.latestGoModule(ctx, module)
				if verr != nil {
					return nil, verr
				}
				if latest != "" && semverLess(cur, latest) {
					rows = append(rows, outdated{Manager: "go", Name: e.Name(), Current: cur, Latest: latest, Target: pkgPath})
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
	out, verr := run(ctx, "go", "env", "GOBIN")
	if verr != nil {
		return "", verr
	}
	if dir := strings.TrimSpace(out); dir != "" {
		return dir, nil
	}
	out, verr = run(ctx, "go", "env", "GOPATH")
	if verr != nil {
		return "", verr
	}
	return filepath.Join(strings.TrimSpace(out), "bin"), nil
}

// goModuleOf reads `go version -m` output: the `path` line is the package
// the binary was built from, the `mod` line is its module and version. All
// three are kept apart because they are asked about differently — the
// module is what the proxy knows, the package is what `go install` takes.
func goModuleOf(out string) (pkgPath, module, version string) {
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
				module, version = f[1], f[2]
			}
		}
	}
	return pkgPath, module, version
}

// goInstallTarget is what `go install <x>@latest` needs for a binary in
// GOBIN: the package path the binary was built from.
func goInstallTarget(ctx context.Context, bin string) (string, *view.Error) {
	dir, verr := goBinDir(ctx)
	if verr != nil {
		return "", verr
	}
	// A status of 1 is a file go cannot read build info from, which is
	// the refusal below and not a failure of go's.
	st, verr := runStatus(ctx, "go", "version", "-m", filepath.Join(dir, bin))
	if verr != nil {
		return "", verr
	}
	pkgPath, _, _ := goModuleOf(st.out)
	if pkgPath == "" {
		return "", view.Errorf("pkg.go.unknown", "%s in %s was not built by go install, or carries no module path", bin, dir)
	}
	return pkgPath, nil
}
