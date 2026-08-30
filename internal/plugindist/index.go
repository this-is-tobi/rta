package plugindist

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/internal/paths"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// An index is a git repository holding plugins/<name>.yaml manifests — Krew's
// shape, adopted with its reasons: git buys partial updates,
// signed commits, blame and rollback, and every author already knows how to
// open a pull request against one. rta shells out to git the way tunnels
// shell out to kubectl and ssh: the operator's transports, proxies and
// credentials keep working, and rta adopts none of their maintenance.
//
// There is no default index. The official one is a repository that does not
// exist until the module is published, and hardcoding its future URL
// would make rta reach for a name nobody controls yet on the day somebody
// registers it. `rta plugin index add` is the whole story, and the first
// index attached is a decision the operator makes by name.

// indexName bounds what an index may be called: it becomes a directory under
// rta's data dir and a prefix in `index/plugin` specs, so the grammar is the
// profile-name one — nothing a filesystem or a shell could read differently.
var indexName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// git is overridable in tests that need a git that misbehaves; the ordinary
// tests use the real one against local repositories.
var gitBin = "git"

// Index is one attached index.
type Index struct {
	Name string
	Dir  string
}

func indexesDir() string { return filepath.Join(paths.Data(), "indexes") }

// Indexes lists what is attached, sorted by name.
func Indexes() []Index {
	entries, err := os.ReadDir(indexesDir())
	if err != nil {
		return nil
	}
	var out []Index
	for _, e := range entries {
		if !e.IsDir() || !indexName.MatchString(e.Name()) {
			continue
		}
		out = append(out, Index{Name: e.Name(), Dir: filepath.Join(indexesDir(), e.Name())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// IndexByName finds one attached index.
func IndexByName(name string) (Index, bool) {
	for _, ix := range Indexes() {
		if ix.Name == name {
			return ix, true
		}
	}
	return Index{}, false
}

// AddIndex clones url as the index called name. The url may be anything git
// clone accepts — https, ssh, or a local path, which is the only kind that
// can exist before the module is published and the kind every test uses.
func AddIndex(ctx context.Context, name, url string) *view.Error {
	if !indexName.MatchString(name) {
		return view.Errorf("plugin.index.name", "%q is not an index name", name).
			WithHint("lowercase letters, digits and dashes, up to 32")
	}
	if strings.HasPrefix(url, "-") {
		// The same argv rule every shell-out in this codebase holds: a value
		// that lands in argv may not begin like an option.
		return view.Errorf("plugin.index.url", "%q is not a repository", url)
	}
	dir := filepath.Join(indexesDir(), name)
	if _, err := os.Stat(dir); err == nil {
		return view.Errorf("plugin.index.exists", "an index called %q is already attached", name).
			WithHint("`rta plugin index update " + name + "` refreshes it; " +
				"`rta plugin index remove " + name + "` detaches it")
	}
	if _, err := exec.LookPath(gitBin); err != nil {
		return view.Errorf("plugin.index.git",
			"attaching an index needs git and it is not on $PATH").
			WithHint("an index is a git repository, so your remotes, proxies and " +
				"credentials keep working — install git")
	}
	if err := os.MkdirAll(indexesDir(), 0o755); err != nil {
		return view.Errorf("plugin.index.add", "%v", err)
	}
	cmd := exec.CommandContext(ctx, gitBin, "clone", "--quiet", "--", url, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		// A failed clone leaves no half-attached index behind: absence is the
		// one state every other command interprets correctly.
		_ = os.RemoveAll(dir)
		return view.Errorf("plugin.index.add", "cloning %s: %s", url,
			firstLine(string(out), err.Error())).
			WithHint("`git clone " + url + "` by hand shows the whole exchange")
	}
	return nil
}

// UpdateIndex fast-forwards one attached index, or every one when name is
// empty. Fast-forward only: an index whose history was rewritten is a fact
// the operator should see, not one a pull should paper over.
func UpdateIndex(ctx context.Context, name string) *view.Error {
	targets := Indexes()
	if name != "" {
		ix, ok := IndexByName(name)
		if !ok {
			return noSuchIndex(name)
		}
		targets = []Index{ix}
	}
	if len(targets) == 0 {
		return view.Errorf("plugin.index.none", "no index is attached").
			WithHint("`rta plugin index add <name> <repository>` attaches one")
	}
	for _, ix := range targets {
		cmd := exec.CommandContext(ctx, gitBin, "-C", ix.Dir, "pull", "--quiet", "--ff-only")
		if out, err := cmd.CombinedOutput(); err != nil {
			return view.Errorf("plugin.index.update", "updating %s: %s", ix.Name,
				firstLine(string(out), err.Error())).
				WithHint("`git -C " + ix.Dir + " pull --ff-only` by hand shows why — " +
					"a rewritten index history needs a deliberate re-add")
		}
	}
	return nil
}

// RemoveIndex detaches one index. Refused while an installed plugin records
// it as provenance: the lockfile would then point at an index that is not
// there, and "where did this binary come from" is the question the lockfile
// exists to answer.
func RemoveIndex(name string) *view.Error {
	ix, ok := IndexByName(name)
	if !ok {
		return noSuchIndex(name)
	}
	var held []string
	for _, e := range ReadLock() {
		if e.Index == name {
			held = append(held, e.Name)
		}
	}
	if len(held) > 0 {
		sort.Strings(held)
		return view.Errorf("plugin.index.held",
			"%s installed %s from this index", strings.Join(held, ", "), name).
			WithHint("`rta plugin remove <name>` first, or leave the index attached")
	}
	if err := os.RemoveAll(ix.Dir); err != nil {
		return view.Errorf("plugin.index.remove", "%v", err)
	}
	return nil
}

func noSuchIndex(name string) *view.Error {
	verr := view.Errorf("plugin.index.unknown", "no index called %q is attached", name)
	if all := Indexes(); len(all) > 0 {
		names := make([]string, 0, len(all))
		for _, ix := range all {
			names = append(names, ix.Name)
		}
		return verr.WithHint("attached: " + strings.Join(names, ", "))
	}
	return verr.WithHint("`rta plugin index add <name> <repository>` attaches one")
}

// Listed is one manifest found in one index.
type Listed struct {
	Manifest Manifest
	Index    string
}

// Manifests reads every valid manifest in one index, sorted by name, and
// reports the invalid ones apart. One malformed file must not cost the
// operator the rest of the catalogue — the LoadInto rule, applied to claims.
func Manifests(ix Index) ([]Listed, []*view.Error) {
	dir := filepath.Join(ix.Dir, "plugins")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, []*view.Error{view.Errorf("plugin.index.empty",
			"%s has no plugins/ directory — it is not an index", ix.Name).
			WithHint("an index holds one plugins/<name>.yaml per plugin")}
	}
	var (
		out  []Listed
		bad  []*view.Error
		seen = map[string]bool{}
	)
	for _, e := range entries {
		base, isManifest := strings.CutSuffix(e.Name(), ".yaml")
		if e.IsDir() || !isManifest {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			bad = append(bad, view.Errorf("plugin.index.manifest", "%s/%s: %v", ix.Name, e.Name(), err))
			continue
		}
		m, verr := ParseManifest(raw)
		if verr != nil {
			bad = append(bad, view.Errorf(verr.Code, "%s/%s: %s", ix.Name, e.Name(), verr.Message).
				WithHint(verr.Hint))
			continue
		}
		// The filename is the name the index gave the entry by placing it,
		// and the name inside is what the entry says about itself — where
		// they disagree, the layout wins and the manifest is refused (the
		// rule, third application).
		if m.Name != base {
			bad = append(bad, view.Errorf("plugin.index.manifest",
				"%s/%s declares name %q; the file's name is the claim", ix.Name, e.Name(), m.Name))
			continue
		}
		if seen[m.Name] {
			continue
		}
		seen[m.Name] = true
		out = append(out, Listed{Manifest: m, Index: ix.Name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.Name < out[j].Manifest.Name })
	return out, bad
}

// Resolve finds the manifest a spec names. A bare name searches every
// attached index and is refused when two carry it — which one wins must be
// the operator's statement, not attachment order — and `index/name` says
// exactly.
func Resolve(spec string) (Listed, *view.Error) {
	indexPart, name, qualified := strings.Cut(spec, "/")
	if !qualified {
		name, indexPart = spec, ""
	}
	if !plugin.ValidName(name) {
		return Listed{}, view.Errorf("plugin.install.spec", "%q is not a plugin name", name)
	}

	search := Indexes()
	if indexPart != "" {
		ix, ok := IndexByName(indexPart)
		if !ok {
			return Listed{}, noSuchIndex(indexPart)
		}
		search = []Index{ix}
	}
	if len(search) == 0 {
		return Listed{}, view.Errorf("plugin.index.none", "no index is attached").
			WithHint("`rta plugin index add <name> <repository>` attaches one")
	}

	var found []Listed
	for _, ix := range search {
		raw, err := os.ReadFile(filepath.Join(ix.Dir, "plugins", name+".yaml"))
		if err != nil {
			continue
		}
		m, verr := ParseManifest(raw)
		if verr != nil {
			return Listed{}, view.Errorf(verr.Code, "%s/%s.yaml: %s", ix.Name, name, verr.Message).
				WithHint(verr.Hint)
		}
		if m.Name != name {
			return Listed{}, view.Errorf("plugin.index.manifest",
				"%s/%s.yaml declares name %q; the file's name is the claim", ix.Name, name, m.Name)
		}
		found = append(found, Listed{Manifest: m, Index: ix.Name})
	}
	switch len(found) {
	case 0:
		return Listed{}, view.Errorf("plugin.install.unknown", "no attached index carries %q", name).
			WithHint("`rta plugin search` lists what they do carry; " +
				"`rta plugin index update` refreshes them")
	case 1:
		return found[0], nil
	default:
		names := make([]string, 0, len(found))
		for _, f := range found {
			names = append(names, f.Index+"/"+name)
		}
		return Listed{}, view.Errorf("plugin.install.ambiguous",
			"%d indexes carry %q", len(found), name).
			WithHint("say which: " + strings.Join(names, " or "))
	}
}

func firstLine(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
