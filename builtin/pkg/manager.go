package pkg

import (
	"context"
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// outdated is one row of the answer: a package a manager knows is behind.
type outdated struct {
	Manager string
	Name    string
	Current string
	Latest  string
}

// manager is one package manager this built-in can read and drive.
//
// A value rather than an interface, so that adding one is filling in five
// fields in a new file — the extendability the design is judged on — and so
// the list in managers() is the whole inventory, readable in one place.
type manager struct {
	name string
	// bin is the executable whose presence on $PATH means the manager is
	// installed. Detection is the only place this package looks up $PATH.
	bin string
	// root says the upgrade must run as root. rta never escalates; it
	// prints the command and refuses when it is not root itself.
	root bool
	// list asks the manager what is behind. A manager that cannot answer
	// alone asks a fixed public registry with names taken from its own
	// installed list — never from a caller.
	list func(ctx context.Context, c *registryClient) ([]outdated, *view.Error)
	// upgrade is the argv that brings one package (or, with "", everything
	// the manager has behind) up to date. nil for "" means the manager has
	// no whole-set upgrade and a package must be named.
	upgrade func(pkg string) []string
	// version is the argv that prints the manager's version, when it is
	// not `<bin> --version`. Only go spells it differently.
	version []string
	// note is the one line the outdated table says under the manager's
	// name when something about it needs saying — that it needs root, or
	// that it cannot upgrade everything at once.
	note string
}

// managers is the inventory, in the order the table shows them: the OS's own
// first, then the general ones, then the language-level globals.
func managers() []manager {
	return []manager{
		brewManager(), aptManager(), dnfManager(), apkManager(), pacmanManager(),
		miseManager(),
		pipxManager(), uvManager(), npmManager(), bunManager(), cargoManager(), gemManager(), goManager(),
	}
}

// detected is every manager whose binary is on $PATH, in inventory order.
func detected() []manager {
	var out []manager
	for _, m := range managers() {
		if _, err := lookPath(m.bin); err == nil {
			out = append(out, m)
		}
	}
	return out
}

func managerByName(name string) (manager, bool) {
	for _, m := range managers() {
		if m.name == name {
			return m, true
		}
	}
	return manager{}, false
}

func managerNames() []string {
	var out []string
	for _, m := range detected() {
		out = append(out, m.name)
	}
	return out
}

// upgradeCommand renders the argv a person would type, for the table and
// for the refusal that prints it instead of running it.
func upgradeCommand(m manager, pkg string) string {
	argv := m.upgrade(pkg)
	if argv == nil {
		return "-"
	}
	cmd := strings.Join(argv, " ")
	if m.root {
		cmd = "sudo " + cmd
	}
	return cmd
}

// sortOutdated keeps the table stable across runs: by manager in inventory
// order, then by name.
func sortOutdated(rows []outdated) {
	order := map[string]int{}
	for i, m := range managers() {
		order[m.name] = i
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if order[rows[i].Manager] != order[rows[j].Manager] {
			return order[rows[i].Manager] < order[rows[j].Manager]
		}
		return rows[i].Name < rows[j].Name
	})
}

// semverLess is the one comparison the registries need: is a behind b. It
// reads the numeric prefix of every dot- or dash-separated segment, so
// 6.8.0-45 sorts after 6.8.0-40 and 1.0-r1 after 1.0-r0 — the kernel and
// apk spellings, which are the ones compared here. A pre-release suffix
// therefore sorts *after* its release, which is wrong in strict semver and
// unreachable in practice: a manager that reports "outdated" itself never
// comes through this, and registries answer stable versions.
func semverLess(a, b string) bool {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

func versionParts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' }) {
		n := 0
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				break
			}
			n = n*10 + int(ch-'0')
		}
		out = append(out, n)
	}
	return out
}
