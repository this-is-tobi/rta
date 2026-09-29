package git

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func remotesCapability() plugin.Capability {
	return plugin.Capability{
		ID:           "git.remotes",
		Summary:      "Where this repository fetches from and pushes to",
		Safety:       plugin.Read,
		HostSpecific: true,
		Idempotent:   true,
		Description: "The other half of \"which branch am I on\": which server that branch reaches, " +
			"and whether this machine has ever heard from it. Three remotes with confusingly " +
			"similar URLs is how somebody pushes a fix to their fork and waits for a review " +
			"nobody can see.\n\n" +
			"Branches counts what this repository knows about that remote — the refs a fetch " +
			"left behind — so a remote that has never been fetched reads as 0 rather than as " +
			"missing.\n\n" +
			"A remote is read from every file of config git reads, as git reads it — one set in " +
			"~/.gitconfig or in a file an include names, a pushurl, a url.<base>.insteadOf " +
			"rewriting the repository's own URL — and each row names the file its URL is set in. " +
			"Over MCP only the repository's own config is read, as git.config shows it there: a " +
			"remote the operator's config sets is theirs, not the repository's.\n\n" +
			"A credential embedded in a remote URL is masked, the same rule `git config` " +
			"follows: `https://user:token@host/repo.git` is a password in a file people paste " +
			"into issues, and it is not what anybody is asking this for.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
		},
		Run: runRemotes,
	}
}

func runRemotes(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, done, verr := openRepoRefs(ctx, req)
	if verr != nil {
		return nil, verr
	}
	defer done()
	pieces, err := shownConfig(ctx, req, repo)
	if verr := refusedByTheGate(err); verr != nil {
		return nil, verr
	}
	if err == nil {
		err = remoteConfigRefusal(pieces)
	}
	if err != nil {
		return nil, view.Errorf("git.remotes.failed", "reading remotes: %v", err)
	}
	known := knownBranches(repo)

	t := view.Table{Columns: []view.Column{
		{Name: "Remote"},
		{Name: "URL"},
		{Name: "Branches", Kind: view.KindNumber},
		{Name: "Origin"},
	}}
	for _, r := range configuredRemotes(pieces, configBase(repo)) {
		// One row per URL. A remote with a separate pushurl is two different
		// places under one name, and collapsing them is how "I pushed it" and
		// "it is not there" both stay true.
		urls := slices.Concat(r.urls, r.push)
		if len(urls) == 0 {
			urls = []setURL{{origin: r.origin}}
		}
		for _, u := range urls {
			t.Rows = append(t.Rows, []string{
				maskURLCredentials(r.name), maskURLCredentials(u.url), strconv.Itoa(known[r.name]), u.origin,
			})
		}
	}
	t.Total = len(t.Rows)
	// A remote an include sets that was not followed is missing from the rows,
	// and said to be, as git.config says a key is (includesUndecided): an
	// empty table then says nothing of whether the repository is local only.
	t.Warnings = unfollowedIncludes("git.remotes", "a remote set there is missing from this table", pieces)
	// The table even when nothing is listed, and the sentence beside it for a
	// screen: see view.Table.Empty.
	switch {
	case len(t.Rows) > 0:
	case len(t.Warnings) > 0:
		t.Empty = "No remotes in the config read here."
	default:
		t.Empty = "No remotes — this repository is local only."
	}
	return t, nil
}

// configuredRemote is a remote as git reads it: each URL it fetches from, then
// each one it pushes to where a pushurl names one, with where each is set, and
// where the remote is first named, for one naming no URL.
type configuredRemote struct {
	name       string
	urls, push []setURL
	origin     string
}

// setURL is a URL a remote names, rewritten as git rewrites it, and the file
// it is set in, as a row names it (scopedConfig.origin).
type setURL struct{ url, origin string }

// configuredRemotes is every remote pieces name, by name, as git's remote.c
// reads them from every file of config it reads.
//
// **Every file, not the repository's own.** go-git's Remotes reads
// .git/config alone, so a remote set in ~/.gitconfig or in a file an include
// names, one git fetches from and `git remote -v` lists, was missing, as was a
// pushurl set there, which is where git pushes; and a url.<base>.insteadOf set
// there, which git rewrites the repository's own URL by, was not applied. The
// pieces are the ones the caller may be shown (shownConfig), which over MCP
// are the repository's own files, so the operator's remotes are listed at a
// terminal and not to an agent, as git.config lists the rest of their config.
//
// Read in git's order: a remote's URLs from each file in turn, a later file's
// after an earlier one's, and an empty url or pushurl clearing the ones read
// before it ("setting this key to the empty string clears the list of urls",
// in git's words), so that a file read later can set them anew. Each is rewritten by the longest
// insteadOf it starts with, the first of equal length among the ones read,
// as git's alias_url rewrites it; a pushInsteadOf, which rewrites only what
// git pushes to, is not applied.
func configuredRemotes(pieces []scopedConfig, base string) []configuredRemote {
	var remotes []*configuredRemote
	named := map[string]*configuredRemote{}
	for _, p := range pieces {
		if !p.config.Raw.HasSection("remote") {
			continue
		}
		origin := p.origin(base)
		for _, sub := range p.config.Raw.Section("remote").Subsections {
			r := named[sub.Name]
			if r == nil {
				r = &configuredRemote{name: sub.Name, origin: origin}
				named[sub.Name] = r
				remotes = append(remotes, r)
			}
			for _, o := range sub.Options {
				switch {
				case o.IsKey("url"):
					r.urls = addURL(r.urls, o.Value, origin)
				case o.IsKey("pushurl"):
					r.push = addURL(r.push, o.Value, origin)
				}
			}
		}
	}
	rewrites := urlRewrites(pieces)
	out := make([]configuredRemote, 0, len(remotes))
	for _, r := range remotes {
		for _, list := range [][]setURL{r.urls, r.push} {
			for i := range list {
				list[i].url = rewrites.apply(list[i].url)
			}
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// addURL is list with value, set in origin, after it, or nothing where value
// is empty, which clears a remote's URLs as git reads it.
func addURL(list []setURL, value, origin string) []setURL {
	if value == "" {
		return nil
	}
	return append(list, setURL{url: value, origin: origin})
}

// urlRewrite is a url.<base>.insteadOf section: base, and each prefix a URL
// that starts with it is rewritten from, in the order they are read.
type urlRewrite struct {
	base      string
	insteadOf []string
}

// urlRules is the rewrites of URLs a config sets (urlRewrites).
type urlRules []urlRewrite

// apply is url rewritten by rs, as git's alias_url rewrites it.
func (rs urlRules) apply(url string) string {
	base, longest := "", -1
	for _, r := range rs {
		for _, prefix := range r.insteadOf {
			if len(prefix) > longest && strings.HasPrefix(url, prefix) {
				base, longest = r.base, len(prefix)
			}
		}
	}
	if longest < 0 {
		return url
	}
	return base + url[longest:]
}

// urlRewrites is every url.<base>.insteadOf pieces set, grouped by base in the
// order each base is first read, as git's rewrites keep them.
func urlRewrites(pieces []scopedConfig) urlRules {
	var out urlRules
	at := map[string]int{}
	for _, p := range pieces {
		if !p.config.Raw.HasSection("url") {
			continue
		}
		for _, sub := range p.config.Raw.Section("url").Subsections {
			for _, o := range sub.Options {
				if !o.IsKey("insteadOf") {
					continue
				}
				i, seen := at[sub.Name]
				if !seen {
					i = len(out)
					at[sub.Name] = i
					out = append(out, urlRewrite{base: sub.Name})
				}
				out[i].insteadOf = append(out[i].insteadOf, o.Value)
			}
		}
	}
	return out
}

// remoteStrings is each key of the remotes and the branches git reads as a
// string, by section, and by name within it, lower case: set with no value at
// all, a name alone on its line, each is one git refuses to run with where it
// reads the remotes ("missing value for 'remote.x.url'"). "" is the section
// with no subsection. Asked of git 2.50 key by key: its remote.c reads these,
// and passes a remote's prune, mirror or skipDefaultUpdate with no value as
// true, as it does a boolean.
var remoteStrings = map[string]map[string][]string{
	"remote": {
		"*": {"url", "pushurl", "push", "fetch", "tagopt", "receivepack", "uploadpack", "proxy", "vcs",
			"serveroption", "followremotehead", "proxyauthmethod"},
		"": {"pushdefault"},
	},
	"branch": {"*": {"remote", "pushremote", "merge"}},
	"url":    {"*": {"insteadof", "pushinsteadof"}},
}

// remoteConfigRefusal is why git refuses to run where it reads the remotes and
// what each branch tracks from pieces, nil where it does not: a key of theirs
// git reads as a string set with no value at all (remoteStrings), a fetch or
// push refspec git cannot parse (readRefspec), or a remote's boolean git does
// not take (remoteBooleans), the first of them in the order git reads them. git.remotes, git.branches and the overview answer from
// that config, and refuse where git refuses: `git remote -v`, `git branch
// -vv` and `git status` say nothing but that.
//
// **Read as set to nothing, it answered otherwise.** A url with no value is
// the empty url, which clears a remote's URLs, so git.remotes listed a remote
// with none; a branch's remote with no value is none, which sent the branch
// to the remote-tracking ref of its own name, an upstream git never reads.
func remoteConfigRefusal(pieces []scopedConfig) error {
	for _, p := range pieces {
		keys := make([]string, 0, len(p.blank))
		for key, v := range p.blank {
			if v.any && gitReadsAsAString(key) {
				keys = append(keys, key)
			}
		}
		if len(keys) > 0 {
			slices.Sort(keys)
			return fmt.Errorf("%s sets %s with no value, which git refuses to run with (missing value for '%s')",
				p.place(), keys[0], keys[0])
		}
		if !p.config.Raw.HasSection("remote") {
			continue
		}
		for _, sub := range p.config.Raw.Section("remote").Subsections {
			for _, o := range sub.Options {
				key := "remote." + sub.Name + "." + strings.ToLower(o.Key)
				if _, valid := readRefspec(o.Value, o.IsKey("fetch")); (o.IsKey("fetch") || o.IsKey("push")) &&
					!valid {
					return fmt.Errorf("%s sets %s to %q, which git refuses to run with (invalid refspec)",
						p.place(), key, o.Value)
				}
				if slices.Contains(remoteBooleans, strings.ToLower(o.Key)) && !isGitBool(o.Value) {
					return fmt.Errorf("%s sets %s to %q, which git refuses to run with (bad boolean config value)",
						p.place(), key, o.Value)
				}
			}
		}
	}
	return nil
}

// remoteBooleans is each key of a remote's git reads as a boolean where it
// reads the remotes, lower case: a value git's git_config_bool does not take
// (isGitBool) stops it ("bad boolean config value"), as a string key set with
// no value does, and one set with no value is true.
var remoteBooleans = []string{"mirror", "skipdefaultupdate", "skipfetchall", "prune", "prunetags"}

// gitReadsAsAString reports whether key, as configKey spells it, is one of
// remoteStrings.
func gitReadsAsAString(key string) bool {
	section, rest, _ := strings.Cut(key, ".")
	subsection, name := "", rest
	if i := strings.LastIndex(rest, "."); i >= 0 {
		subsection, name = rest[:i], rest[i+1:]
	}
	names := remoteStrings[section]
	if subsection != "" {
		return slices.Contains(names["*"], name)
	}
	return slices.Contains(names[""], name)
}

// knownBranches counts the remote-tracking refs each remote has left behind.
//
// From the refs rather than from a network call, like everything else in this
// plugin: the question a person is asking is what *this* repository knows, and
// a capability that dialled a server to draw a table would be a different and
// much more expensive thing wearing the same name.
func knownBranches(repo *git.Repository) map[string]int {
	out := map[string]int{}
	refs, err := repo.References()
	if err != nil {
		return out
	}
	_ = refs.ForEach(func(r *plumbing.Reference) error {
		name := r.Name()
		if !name.IsRemote() {
			return nil
		}
		if remote, _, ok := strings.Cut(name.Short(), "/"); ok {
			out[remote]++
		}
		return nil
	})
	return out
}
