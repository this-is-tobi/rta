package pkg

import (
	"cmp"
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
	// Target is what the manager's upgrade argv takes when that is not
	// Name. Only go sets it: the row names the binary, since that is what
	// somebody sees on $PATH and what pkg.upgrade takes, and `go install`
	// wants the package path the binary was built from.
	Target string
	// Broken is why the manager could not read this package, for a row that
	// says so in place of its versions, beside the ones it did read. Only
	// pipx sets it: it lists the venvs it can read and exits 1 over one it
	// cannot, and failing the whole manager for that hid what the others
	// have behind.
	Broken string
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
	ms := detected()
	out := make([]string, 0, len(ms))
	for _, m := range ms {
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
// 6.8.0-45 sorts after 6.8.0-40 — the kernel's spelling, which os.go
// compares — and then ranks a version with a pre-release marker below the
// same release without one.
//
// That ranking was once left out as unreachable, since registries answer
// stable versions. The installed side is where a pre-release sits, though —
// a tool installed at a release candidate, a pipx --pre, a go install @rc —
// and read as numbers alone 2.0.0-rc.1 was 2.0.0.0.1, newer than 2.0.0,
// and PEP 440's 2.0.0rc1 was 2.0.0 itself: the candidate read ok for good
// once the release it led up to had shipped.
func semverLess(a, b string) bool {
	ra, preA := splitPrerelease(a)
	rb, preB := splitPrerelease(b)
	pa, pb := versionParts(ra), versionParts(rb)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	if len(pa) != len(pb) {
		return len(pa) < len(pb)
	}
	switch {
	case preA == "":
		return false
	case preB == "":
		return true
	}
	return comparePrerelease(preA, preB) < 0
}

// splitPrerelease separates a version's release from a pre-release marker
// after it: semver's 2.0.0-rc.1 and 1.5.0-beta.3, PEP 440's 2.0.0rc1 and
// 1.0.dev0.
//
// A marker is one of the words below, because what may follow a release is
// more than pre-releases: the kernel's -45 and -generic, a git describe's
// -dirty, a Python post-release's .post1, a platform somebody's --version
// prints. Each of those is left on the release, to be read as before, and
// none ranks the version below the release it names.
func splitPrerelease(v string) (release, pre string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	end := 0
	for end < len(v) && (isDigit(v[end]) || v[end] == '.' && end+1 < len(v) && isDigit(v[end+1])) {
		end++
	}
	marker := v[end:]
	if marker != "" && (marker[0] == '-' || marker[0] == '.') {
		marker = marker[1:]
	}
	lower := strings.ToLower(marker)
	for _, w := range []string{"alpha", "beta", "preview", "pre", "rc", "dev", "snapshot", "canary", "nightly", "next", "a", "b", "c"} {
		if rest, ok := strings.CutPrefix(lower, w); ok && (rest == "" || !isLetter(rest[0])) {
			return v[:end], lower
		}
	}
	return v, ""
}

// comparePrerelease orders two markers the way semver orders identifiers:
// runs of digits by value, runs of letters by spelling, digits before
// letters, and the shorter first when it is the other's beginning — so
// rc.1 < rc.2 < rc.10 and alpha < beta < rc, as a1 < b1 < rc1.
func comparePrerelease(a, b string) int {
	ta, tb := markerRuns(a), markerRuns(b)
	for i := 0; i < len(ta) && i < len(tb); i++ {
		x, y := ta[i], tb[i]
		switch xd, yd := isDigit(x[0]), isDigit(y[0]); {
		case xd && yd:
			x, y = strings.TrimLeft(x, "0"), strings.TrimLeft(y, "0")
			if len(x) != len(y) {
				return cmp.Compare(len(x), len(y))
			}
		case xd != yd:
			if xd {
				return -1
			}
			return 1
		}
		if c := strings.Compare(x, y); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(ta), len(tb))
}

// markerRuns splits a marker into its runs of letters and of digits, every
// other character a separator: rc.10 is rc and 10, and rc10 is too.
func markerRuns(s string) []string {
	var runs []string
	separator := func(r rune) bool { return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') }
	for _, field := range strings.FieldsFunc(s, separator) {
		start := 0
		for i := 1; i <= len(field); i++ {
			if i == len(field) || isDigit(field[i]) != isDigit(field[start]) {
				runs = append(runs, field[start:i])
				start = i
			}
		}
	}
	return runs
}

func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func versionParts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' })
	out := make([]int, 0, len(parts))
	for _, p := range parts {
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
