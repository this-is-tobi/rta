package pkg

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/this-is-tobi/rta/builtin/internal/verdict"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// listing is one pass over every detected manager: the rows, and the
// managers whose list failed, kept as rows too rather than dropped — a
// manager that could not answer is a fact about the machine, and a table
// that silently omitted it would read as "nothing is behind there".
type listing struct {
	rows    []outdated
	failed  map[string]*view.Error
	present []manager
}

// collect asks every detected manager what is behind, at the same time: each is
// its own process or its own registry, they share nothing but the client, and
// the sum of their waits is the time a machine with a dozen of them took.
func collect(ctx context.Context, c *registryClient, only string) listing {
	l := listing{failed: map[string]*view.Error{}}
	for _, m := range detected() {
		if only == "" || m.name == only {
			l.present = append(l.present, m)
		}
	}
	type answer struct {
		rows []outdated
		verr *view.Error
	}
	answers := make([]answer, len(l.present))
	var wg sync.WaitGroup
	for i, m := range l.present {
		wg.Go(func() { answers[i].rows, answers[i].verr = m.list(ctx, c) })
	}
	wg.Wait()
	for i, m := range l.present {
		if answers[i].verr != nil {
			l.failed[m.name] = answers[i].verr
			continue
		}
		l.rows = append(l.rows, answers[i].rows...)
	}
	sortOutdated(l.rows)
	return l
}

// managerStatus is the overview's line for one manager: that it could not be
// read, or how much is behind, and how many of its packages it could not read,
// counted apart as the tools line counts the ones it could not.
func managerStatus(l listing, name string) string {
	if verr, failed := l.failed[name]; failed {
		return "could not be read — " + verr.Message
	}
	n, broken := l.countFor(name), l.brokenFor(name)
	switch {
	case n > 0 && broken > 0:
		return fmt.Sprintf("outdated %d (%d could not be read)", n, broken)
	case n > 0:
		return fmt.Sprintf("outdated %d", n)
	case broken > 0:
		return "could not be read — " + format.CountOf(broken, "package")
	}
	return "ok"
}

// countFor is how many packages name has behind, a row saying one could not
// be read not among them.
func (l listing) countFor(name string) int {
	n := 0
	for _, r := range l.rows {
		if r.Manager == name && r.Broken == "" {
			n++
		}
	}
	return n
}

func (l listing) brokenFor(name string) int {
	n := 0
	for _, r := range l.rows {
		if r.Manager == name && r.Broken != "" {
			n++
		}
	}
	return n
}

func managerField() plugin.Field {
	return plugin.Field{Name: "manager", Type: plugin.String, Positional: true,
		Help:    "only this manager (brew, apt, mise, npm, go, …); every detected one when omitted",
		Suggest: func(context.Context, plugin.Request) []string { return managerNames() }}
}

func outdatedCapability() plugin.Capability {
	return host(plugin.Capability{
		ID:         "pkg.outdated",
		Summary:    "Every package behind its latest, across every manager on this machine, with the command that fixes each",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "One table across whichever of brew, apt, dnf, apk, pacman, mise, pipx, uv, npm, " +
			"bun, cargo, gem and go are installed here. Managers that can say what is " +
			"behind are asked; the ones that only list what is installed are compared " +
			"against their registry, with the names taken from the machine and never " +
			"from a caller. Every row carries the exact upgrade command, with sudo in " +
			"front where the manager needs root — rta prints it and never runs sudo " +
			"itself. A manager that failed to answer is a row too.",
		Inputs: []plugin.Field{managerField()},
		// The package table is where somebody decides to take an upgrade, so
		// taking it is one key from the row. Columns `target` and `package` are
		// named for pkg.upgrade's inputs, so both halves seed from the row and the
		// form only opens for the destructive confirmation.
		Actions: []plugin.Action{{Key: "u", Label: "upgrade", Target: "pkg.upgrade", Source: plugin.ActionRow}},
		Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
			only := req.String("manager")
			if only != "" {
				if _, ok := managerByName(only); !ok {
					return nil, unknownManager(req.Surface(), only)
				}
			}
			l := collect(ctx, newRegistryClient(), only)
			t := outdatedTable(l)
			// The table even when no manager is installed, and the sentence
			// beside it for a screen: see view.Table.Empty.
			if len(l.present) == 0 {
				t.Empty = "No package manager found on $PATH — none of " + strings.Join(allManagerNames(), ", ") + "."
			}
			return t, nil
		},
	})
}

func unknownManager(sf plugin.Surface, name string) *view.Error {
	return view.Errorf("pkg.manager.unknown", "%q is not a manager pkg knows", name).
		WithHint("one of " + strings.Join(allManagerNames(), ", ") + "; " + sf.CapabilityName("pkg.managers") +
			" shows which are on this machine").WithInput("manager")
}

func allManagerNames() []string {
	ms := managers()
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.name)
	}
	return out
}

// outdatedTable is the answer. Column names `target` and `package` are the
// names of pkg.upgrade's inputs on purpose: the TUI seeds a row action from
// the columns named for the target's key inputs, so `u` on a row runs the
// upgrade of that one package under that one manager with nothing to type.
func outdatedTable(l listing) view.Table {
	t := view.Table{Columns: []view.Column{
		{Name: "Target"},
		{Name: "Package"},
		{Name: "Installed"},
		{Name: "Latest"},
		{Name: "Status", Kind: view.KindStatus},
		{Name: "Upgrade"},
	}}
	for _, r := range l.rows {
		if r.Broken != "" {
			t.Rows = append(t.Rows, []string{r.Manager, r.Name, "-", "-", "fail " + r.Manager + ": " + r.Broken, "-"})
			continue
		}
		m, _ := managerByName(r.Manager)
		arg := r.Name
		if r.Target != "" {
			arg = r.Target
		}
		t.Rows = append(t.Rows, []string{r.Manager, r.Name, r.Current, r.Latest, "outdated", upgradeCommand(m, arg)})
	}
	names := make([]string, 0, len(l.failed))
	for n := range l.failed {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		t.Rows = append(t.Rows, []string{n, "-", "-", "-", "fail " + l.failed[n].Message, "-"})
	}
	if len(t.Rows) == 0 {
		for _, m := range l.present {
			t.Rows = append(t.Rows, []string{m.name, "-", "-", "-", "ok", "-"})
		}
	}
	t.Total = len(t.Rows)
	return t
}

func overviewCapability() plugin.Capability {
	return host(plugin.Capability{
		ID:         "pkg.overview",
		Summary:    "Is this machine up to date: every manager's outdated count, your own binaries, the OS and the kernel",
		Safety:     plugin.Read,
		Idempotent: true,
		Detailed:   true,
		Description: "The glance: a verdict first (ok, or warn with the worst thing it found), the total " +
			"behind and whether a reboot is owed, then the package managers that have something behind or " +
			"could not be read, one row each, and the ones that are current as one row; how many of your " +
			"GitHub-release binaries have a newer release; and whether the OS offers updates and the " +
			"kernel gap. A read that failed says so in its row, and the verdict with it, instead of " +
			"claiming nothing is behind. The managers are asked at the same time, and on a dashboard tile " +
			"under one deadline shorter than the tile's own, so one that is slow is a row that could not " +
			"be read and not a tile that timed out. `detail` adds every manager, the current ones too, " +
			"and the tables themselves.\n\n" +
			"Not on the automatic dashboard: this runs a dozen tools and asks four " +
			"registries. Name it in `dashboard: tiles:` once you have decided that is fine " +
			"every few seconds — most people want it once a morning, which is pkg.overview run by hand.\n\n" +
			"Nothing in pkg is reachable over MCP, reads included: what is installed here and " +
			"what is behind is the map an attacker draws first, and it is not an agent's to read.",
		Inputs: []plugin.Field{toolsField()},
		Run:    runOverview,
	})
}

// tileDeadline is the one deadline a dashboard tile's run of the overview holds
// all of its asking to: the managers, the tools and the OS together. A tile gets
// thirty seconds (the TUI's refreshTimeout), and a run that used them all was
// cut off as a whole, whichever line it was in the middle of; held to a little
// less, a line that is still waiting is the one that says it could not be read,
// and the rest are answered. By hand there is no such deadline, only the minute
// a command is given. A var for the tests.
var tileDeadline = 25 * time.Second

// isTile reports whether this is the dashboard's run of the overview: the
// compact answer on the TUI, which refreshes on a timer. The page enter opens is
// the detailed one, and is not on the dashboard's clock.
func isTile(req plugin.Request) bool {
	return req.Surface() == plugin.SurfaceTUI && !req.Bool("detail")
}

func runOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	return overviewWith(ctx, req, newRegistryClient())
}

// shown says on which of the overview's answers a row appears. A row that says
// nothing is the matter — no tools listed, no update offered, a manager that is
// current — is on the page enter opens and not in the compact answer, where a
// dozen of them were the news that nothing was wrong; and the managers that are
// current are one row there instead.
type shown int

const (
	always shown = iota
	compactOnly
	detailOnly
)

type overviewRow struct {
	view.Pair
	shown shown
}

// overviewWith is the overview asking through c. The managers, the tools and
// the OS are asked at the same time, since they share nothing, and the verdict
// is made of what came back: what could not be read first, since not knowing is
// the part of the page the rest depends on, then a reboot that is owed, then
// what is behind.
func overviewWith(ctx context.Context, req plugin.Request, c *registryClient) (view.View, error) {
	raw := req.StringSlice("tools")
	// A list that is not in the grammar is a mistake in the configuration, said
	// before anything is asked.
	if _, verr := parseTools(raw); verr != nil {
		return nil, verr
	}
	if isTile(req) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, tileDeadline)
		defer cancel()
	}
	var (
		l     listing
		tools []toolState
		st    osState
		wg    sync.WaitGroup
	)
	wg.Go(func() { l = collect(ctx, c, "") })
	wg.Go(func() { tools, _ = readTools(ctx, c, raw) }) //nolint:errcheck // parsed above, which is all it fails on
	wg.Go(func() { st = osReader(ctx) })
	wg.Wait()

	var found verdict.Verdict
	var rows, unreadable, behindRows, okRows []overviewRow
	var unreadableNames []string
	total := 0
	for _, m := range l.present {
		value := managerStatus(l, m.name)
		row := overviewRow{Pair: view.Pair{Key: m.name, Value: value}}
		_, failed := l.failed[m.name]
		if !failed {
			total += l.countFor(m.name)
		}
		switch {
		case failed || strings.HasPrefix(value, "could not be read"):
			unreadable = append(unreadable, row)
			unreadableNames = append(unreadableNames, m.name)
		case value == "ok":
			row.shown = detailOnly
			okRows = append(okRows, row)
		default:
			behindRows = append(behindRows, row)
		}
	}

	behind, unchecked := 0, 0
	for _, t := range tools {
		switch {
		case t.behind():
			behind++
		case !t.compared():
			// Counted apart from the current ones: "ok — 7 current" was
			// counting tools whose installed version nobody managed to
			// read, which is the one summary line somebody reads instead
			// of the table.
			unchecked++
		}
	}
	toolsRow := overviewRow{Pair: view.Pair{Key: "tools"}}
	switch {
	case len(tools) == 0:
		toolsRow.Value, toolsRow.shown = "none listed — `plugins: pkg: tools:`", detailOnly
	case behind == 0 && unchecked == 0:
		toolsRow.Value = fmt.Sprintf("ok — %d current", len(tools))
	case behind == 0:
		toolsRow.Value = fmt.Sprintf("could not be read — %d of %d", unchecked, len(tools))
	default:
		toolsRow.Value = fmt.Sprintf("outdated %d of %d", behind, len(tools))
		if unchecked > 0 {
			toolsRow.Value += fmt.Sprintf(" (%d could not be read)", unchecked)
		}
	}

	switch {
	case len(l.present) == 0:
		found.Warn("no package manager found")
	case len(unreadableNames) > 0:
		found.Warn(strings.Join(unreadableNames, ", ") + " could not be read")
	}
	if unchecked > 0 {
		found.Warn(format.CountOf(unchecked, "tool") + " could not be read")
	}
	if len(st.Notes) > 0 {
		found.Warn("os updates could not be read")
	}
	if st.RebootRequired {
		found.Warn("reboot owed")
	}
	if total > 0 || behind > 0 {
		var parts []string
		if total > 0 {
			parts = append(parts, format.CountOf(total, "package"))
		}
		if behind > 0 {
			parts = append(parts, format.CountOf(behind, "tool"))
		}
		found.Warn(strings.Join(parts, ", ") + " behind")
	}

	rows = append(rows, overviewRow{Pair: view.Pair{Key: "total behind",
		Value: format.CountOf(total, "package") + ", " + format.CountOf(behind, "tool")}})
	rows = append(rows, overviewRow{Pair: rebootPair(st)})
	for _, p := range osFacts(st) {
		row := overviewRow{Pair: p}
		if st.quiet() {
			row.shown = detailOnly
		}
		rows = append(rows, row)
	}
	rows = append(rows, unreadable...)
	rows = append(rows, behindRows...)
	rows = append(rows, okRows...)
	switch {
	case len(l.present) == 0:
		rows = append(rows, overviewRow{Pair: view.Pair{Key: "managers", Value: "none found on $PATH"}})
	case len(okRows) > 0:
		rows = append(rows, overviewRow{Pair: view.Pair{Key: "managers", Value: fmt.Sprintf("%d current", len(okRows))}, shown: compactOnly})
	}
	rows = append(rows, toolsRow)

	detail := req.Bool("detail")
	kv := view.KeyValue{Pairs: []view.Pair{{Key: "status", Value: found.String()}}}
	for _, r := range rows {
		if (r.shown == compactOnly && detail) || (r.shown == detailOnly && !detail) {
			continue
		}
		kv.Pairs = append(kv.Pairs, r.Pair)
	}
	if !detail {
		return kv, nil
	}
	p := plugin.NewPage(ctx, req)
	p.PutAs("summary", "summary", kv)
	if len(l.present) > 0 {
		p.PutAs("outdated", "outdated", outdatedTable(l))
	}
	if len(tools) > 0 {
		p.PutAs("tools", "tools", toolsTable(tools))
	}
	if len(st.Updates) > 0 {
		t := view.Table{Columns: []view.Column{{Name: "Update"}, {Name: "Version"}, {Name: "Restarts", Kind: view.KindStatus}}}
		for _, u := range st.Updates {
			restart := "no"
			if u.Restart {
				restart = "pending reboot"
			}
			t.Rows = append(t.Rows, []string{u.Title, u.Version, restart})
		}
		t.Total = len(t.Rows)
		p.PutAs("os-updates", "os updates", t)
	}
	return p.View(), nil
}
