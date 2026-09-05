package pkg

import (
	"context"
	"fmt"
	"sort"
	"strings"

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

func collect(ctx context.Context, c *registryClient, only string) listing {
	l := listing{failed: map[string]*view.Error{}}
	for _, m := range detected() {
		if only != "" && m.name != only {
			continue
		}
		l.present = append(l.present, m)
		rows, verr := m.list(ctx, c)
		if verr != nil {
			l.failed[m.name] = verr
			continue
		}
		l.rows = append(l.rows, rows...)
	}
	sortOutdated(l.rows)
	return l
}

func (l listing) countFor(name string) int {
	n := 0
	for _, r := range l.rows {
		if r.Manager == name {
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
		Run: func(ctx context.Context, req plugin.Request) (view.View, error) {
			if verr := supported(); verr != nil {
				return nil, verr
			}
			only := req.String("manager")
			if only != "" {
				if _, ok := managerByName(only); !ok {
					return nil, unknownManager(only)
				}
			}
			l := collect(ctx, newRegistryClient(), only)
			if len(l.present) == 0 {
				return view.Text{Body: "No package manager found on $PATH — none of " + strings.Join(allManagerNames(), ", ") + "."}, nil
			}
			return outdatedTable(l), nil
		},
	})
}

func unknownManager(name string) *view.Error {
	return view.Errorf("pkg.manager.unknown", "%q is not a manager pkg knows", name).
		WithHint("one of " + strings.Join(allManagerNames(), ", ") + "; `rta pkg managers` shows which are on this machine")
}

func allManagerNames() []string {
	var out []string
	for _, m := range managers() {
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
		{Name: "target"},
		{Name: "package"},
		{Name: "Installed"},
		{Name: "Latest"},
		{Name: "Status", Kind: view.KindStatus},
		{Name: "Upgrade"},
	}}
	for _, r := range l.rows {
		m, _ := managerByName(r.Manager)
		t.Rows = append(t.Rows, []string{r.Manager, r.Name, r.Current, r.Latest, "outdated", upgradeCommand(m, r.Name)})
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
		Description: "The glance: which package managers are here and how many packages each has " +
			"behind, how many of your GitHub-release binaries have a newer release, " +
			"whether the OS offers updates, whether a reboot is owed, and the kernel gap. " +
			"--detail adds the tables themselves.\n\n" +
			"Not on the automatic dashboard: this runs a dozen tools and asks four " +
			"registries. Name it in `dashboard: tiles:` once you have decided that is fine " +
			"every few seconds — most people want it once a morning, which is `rta pkg overview`.\n\n" +
			"Nothing in pkg is reachable over MCP, reads included: what is installed here and " +
			"what is behind is the map an attacker draws first, and it is not an agent's to read.",
		Inputs: []plugin.Field{toolsField()},
		Run:    runOverview,
	})
}

func runOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	if verr := supported(); verr != nil {
		return nil, verr
	}
	c := newRegistryClient()
	l := collect(ctx, c, "")
	tools, verr := readTools(ctx, c, req.StringSlice("tools"))
	if verr != nil {
		return nil, verr
	}
	st := readOS(ctx)

	kv := view.KeyValue{}
	if len(l.present) == 0 {
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "managers", Value: "none found on $PATH"})
	}
	total := 0
	for _, m := range l.present {
		v := "ok"
		if verr, failed := l.failed[m.name]; failed {
			v = "fail " + verr.Message
		} else if n := l.countFor(m.name); n > 0 {
			v = fmt.Sprintf("outdated %d", n)
			total += n
		}
		kv.Pairs = append(kv.Pairs, view.Pair{Key: m.name, Value: v})
	}
	behind := 0
	for _, t := range tools {
		if t.behind() {
			behind++
		}
	}
	switch {
	case len(tools) == 0:
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "tools", Value: "none listed — `plugins: pkg: tools:`"})
	case behind == 0:
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "tools", Value: fmt.Sprintf("ok — %d current", len(tools))})
	default:
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "tools", Value: fmt.Sprintf("outdated %d of %d", behind, len(tools))})
	}
	for _, p := range osStatePairs(st).Pairs {
		kv.Pairs = append(kv.Pairs, p)
	}
	kv.Pairs = append(kv.Pairs, view.Pair{Key: "total behind", Value: fmt.Sprintf("%d packages, %d tools", total, behind)})

	if !req.Bool("detail") {
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
