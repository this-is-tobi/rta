package pkg

import (
	"context"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// The OS's own managers. Every one needs root to upgrade, and rta never
// escalates: the table prints the command with sudo in front, and
// pkg.upgrade refuses with that command in the hint unless rta is root.

func aptManager() manager {
	return manager{
		name: "apt", bin: "apt-get", root: true,
		note: "needs root to upgrade",
		list: func(ctx context.Context, _ *registryClient) ([]outdated, *view.Error) {
			// apt prints "WARNING: apt does not have a stable CLI interface"
			// on stderr; the stdout shape has not changed in a decade:
			//   name/suite 1.2.3 arch [upgradable from: 1.2.2]
			out, _, verr := run(ctx, "apt", "list", "--upgradable")
			if verr != nil {
				return nil, verr
			}
			var rows []outdated
			for _, line := range lines(out) {
				name, rest, ok := strings.Cut(line, "/")
				if !ok || strings.HasPrefix(line, "Listing") {
					continue
				}
				f := strings.Fields(rest)
				if len(f) < 2 {
					continue
				}
				current := "-"
				if i := strings.Index(line, "upgradable from: "); i >= 0 {
					current = strings.TrimSuffix(line[i+len("upgradable from: "):], "]")
				}
				rows = append(rows, outdated{"apt", name, current, f[1]})
			}
			return rows, nil
		},
		upgrade: func(pkg string) []string {
			if pkg == "" {
				return []string{"apt-get", "upgrade", "-y"}
			}
			return []string{"apt-get", "install", "--only-upgrade", "-y", pkg}
		},
	}
}

func dnfManager() manager {
	return manager{
		name: "dnf", bin: "dnf", root: true,
		note: "needs root to upgrade",
		list: func(ctx context.Context, _ *registryClient) ([]outdated, *view.Error) {
			// Exit 100 means "updates available" and is the answer, not a
			// failure; the lines are `name.arch  version  repo`.
			out, code, verr := run(ctx, "dnf", "-q", "check-update")
			if verr != nil {
				return nil, verr
			}
			if code != 0 && code != 100 {
				return nil, view.Errorf("pkg.dnf.failed", "dnf check-update exited %d", code)
			}
			var rows []outdated
			for _, line := range lines(out) {
				f := strings.Fields(line)
				if len(f) < 3 || strings.HasPrefix(line, "Obsoleting") {
					continue
				}
				name, _, _ := strings.Cut(f[0], ".")
				rows = append(rows, outdated{"dnf", name, "-", f[1]})
			}
			return rows, nil
		},
		upgrade: func(pkg string) []string {
			if pkg == "" {
				return []string{"dnf", "upgrade", "-y"}
			}
			return []string{"dnf", "upgrade", "-y", pkg}
		},
	}
}

func apkManager() manager {
	return manager{
		name: "apk", bin: "apk", root: true,
		note: "needs root to upgrade",
		list: func(ctx context.Context, _ *registryClient) ([]outdated, *view.Error) {
			// `apk version -l '<'` prints `name-1.0-r0 < 1.1-r0` for every
			// installed package behind its repository.
			out, _, verr := run(ctx, "apk", "version", "-l", "<")
			if verr != nil {
				return nil, verr
			}
			var rows []outdated
			for _, line := range lines(out) {
				left, right, ok := strings.Cut(line, " < ")
				if !ok || strings.HasPrefix(line, "Installed") {
					continue
				}
				name, current := splitApkName(strings.TrimSpace(left))
				rows = append(rows, outdated{"apk", name, current, strings.TrimSpace(right)})
			}
			return rows, nil
		},
		upgrade: func(pkg string) []string {
			if pkg == "" {
				return []string{"apk", "upgrade"}
			}
			return []string{"apk", "add", "--upgrade", pkg}
		},
	}
}

// splitApkName separates `name-1.2.3-r0` at the first "-<digit>": apk
// package names carry dashes, versions start with a digit.
func splitApkName(s string) (name, version string) {
	for i := 1; i < len(s)-1; i++ {
		if s[i] == '-' && s[i+1] >= '0' && s[i+1] <= '9' {
			return s[:i], s[i+1:]
		}
	}
	return s, "-"
}

func pacmanManager() manager {
	return manager{
		name: "pacman", bin: "pacman", root: true,
		note: "needs root to upgrade",
		list: func(ctx context.Context, _ *registryClient) ([]outdated, *view.Error) {
			// `pacman -Qu` prints `name 1.0-1 -> 1.1-1`; exit 1 with no
			// output means nothing is behind.
			out, _, verr := run(ctx, "pacman", "-Qu")
			if verr != nil {
				return nil, verr
			}
			var rows []outdated
			for _, line := range lines(out) {
				f := strings.Fields(line)
				if len(f) < 4 || f[2] != "->" {
					continue
				}
				rows = append(rows, outdated{"pacman", f[0], f[1], f[3]})
			}
			return rows, nil
		},
		upgrade: func(pkg string) []string {
			if pkg == "" {
				return []string{"pacman", "-Syu", "--noconfirm"}
			}
			return []string{"pacman", "-S", "--noconfirm", pkg}
		},
	}
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
