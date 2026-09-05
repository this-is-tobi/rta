package plugindist

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// An index is a git repository holding index/<name>.yaml manifests — Krew's
// shape, adopted with its reasons: git buys partial updates,
// signed commits, blame and rollback, and every author already knows how to
// open a pull request against one. rta shells out to git the way tunnels
// shell out to kubectl and ssh: the operator's transports, proxies and
// credentials keep working, and rta adopts none of their maintenance — with
// three named exceptions it does own, and gitHardening says which and why.
//
// There is no default index, and one known one. rta attaches nothing on its
// own; `rta plugin index add official` attaches the first-party index by a
// name rta reserves for it (known.go), and `rta plugin index add <name>
// <repository>` attaches any other. The first index attached is still a
// decision the operator makes by name — it is just one line long now.

// indexName bounds what an index may be called: it becomes a directory under
// rta's data dir and a prefix in `index/plugin` specs, so the grammar is the
// profile-name one — nothing a filesystem or a shell could read differently.
var indexName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// git is overridable in tests that need a git that misbehaves; the ordinary
// tests use the real one against local repositories.
var gitBin = "git"

// gitHardening is prepended to every git rta runs.
//
// protocol.ext.allow=never is the one that matters: the ext:: transport takes
// a command line as its argument, so a repository URL is an execution when it
// is allowed. git's own default is already never — `git clone -- "ext::…"`
// answers `fatal: transport 'ext' not allowed` — so this closes no hole git
// leaves open. What it refuses to do is *depend* on a default:
// `protocol.allow = always` is a line CI images and container bases grow so
// that submodules over file:// work, and a per-command -c beats it.
//
// It belongs on the pull as much as the clone, and that is not symmetry. The
// clone's URL went through classifyGitURL; the pull's comes back out of
// .git/config, where an index attached before that grammar existed — or a
// `git remote set-url` by hand — put it, and where nothing re-reads it.
//
// http and git are the two transports classifyGitURL refuses, stated again
// where git can enforce them: an https URL that redirects to http is a
// downgrade the grammar never sees.
//
// Not protocol.allow=never, which is the default for every protocol without an
// explicit policy and would therefore refuse file — every local index, and
// every test in this package — along with https and ssh. Re-enabling them one
// by one would make rta the owner of git's transport table, and the first
// operator whose forge needs something rta did not list would be blocked for
// no security gain. Naming the three rta has decided are not repositories
// leaves the rest to the operator's git, which is the division "rta shells out
// to git" makes everywhere else.
var gitHardening = []string{
	"-c", "protocol.ext.allow=never",
	"-c", "protocol.http.allow=never",
	"-c", "protocol.git.allow=never",
}

// gitCommand is the only way this package runs git, so a fourth call site
// cannot be added without the hardening.
//
// The arguments are copied onto a fresh slice rather than appended to
// gitHardening: append would write into gitHardening's own backing array the
// moment it had spare capacity, and every later call would inherit whatever
// the last one passed.
func gitCommand(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, gitBin,
		append(append([]string{}, gitHardening...), args...)...)
}

// gitHint renders the command rta actually ran, hardening included. A hint
// that hid it would send somebody to debug a clone that works by hand.
func gitHint(args ...string) string {
	return "`" + strings.Join(append([]string{"git"},
		append(append([]string{}, gitHardening...), args...)...), " ") + "`"
}

func requireGit(what string) *view.Error {
	if _, err := exec.LookPath(gitBin); err != nil {
		return view.Errorf("plugin.index.git", "%s needs git and it is not on $PATH", what).
			WithHint("an index is a git repository, so your remotes, proxies and " +
				"credentials keep working — install git")
	}
	return nil
}

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

// IndexOrigin is where an attached index was cloned from, read out of the
// clone rather than out of anything rta wrote down.
//
// rta keeps no record of the URL: Indexes() is a directory scan and Index is
// {Name, Dir}, so nothing in rta's own state remembers an attach. git already
// does — `git clone` writes remote.origin.url, absolutising a plain path and
// preserving a file:// URL — which makes the fact available with no schema, no
// migration, and no second copy that can drift from the repository it
// describes. A cached copy would have exactly one interesting failure mode: an
// index re-pointed with `git remote set-url` would go on counting as local,
// which is the case the caller exists to catch.
//
// **--local, and that is load-bearing rather than tidy.** `git config --get`
// reads system, then global, then repository config, so a `~/.gitconfig`
// holding a `[remote "origin"] url = …` line answers for a directory that is
// not a repository at all — measured, not assumed: a plain temp directory
// returned that URL and exited 0. Without --local, dropping two lines in a
// gitconfig would make any directory claim whatever origin it liked, which is
// precisely the decision this feeds.
//
// Every failure is an error rather than an empty string, because the caller
// uses this to decide whether an index may hand rta a local path to open, and
// "I could not tell" belongs on the refusing side of that question.
func IndexOrigin(ctx context.Context, ix Index) (string, *view.Error) {
	if verr := requireGit("reading where an index came from"); verr != nil {
		return "", verr
	}
	unreadable := func(why string) *view.Error {
		return view.Errorf("plugin.index.origin",
			"%s states no single origin, so rta cannot tell where it was attached from: %s",
			ix.Name, why).
			WithHint(gitHint("-C", ix.Dir, "remote", "-v") + " shows what git has. An index " +
				"attached with `rta plugin index add` always has one; a directory placed " +
				"here by hand has none, and only an index attached from this machine may " +
				"name file:// artifacts")
	}
	cmd := gitCommand(ctx, "-C", ix.Dir, "config", "--local", "--get-all", "remote.origin.url")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", unreadable(firstLine(string(out), err.Error()))
	}
	// --get-all rather than --get, and then exactly one. git fetches from the
	// first of several URLs while --get hands back the last, so a multi-valued
	// remote.origin.url is a question rta cannot answer rather than one it
	// would answer about the wrong repository.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 1 || strings.TrimSpace(lines[0]) == "" {
		return "", unreadable("it names " + strconv.Itoa(len(lines)) + " origins")
	}
	return strings.TrimSpace(lines[0]), nil
}

// OriginForDisplay is an origin on its way to a person: no credentials, and
// nothing that renders as something other than what it is.
//
// `git remote -v` shows a token in a clone URL, and an operator who put one
// there knows it is there — but rta printing it into a table also prints it
// into `--output json`, into terminal scrollback, and into whatever is reading
// that, which is three copies of a credential nobody asked rta to make. And an
// origin can hold an escape sequence: classifyGitURL refuses one on the way
// in, but a .git/config written before that grammar existed was never checked.
//
// The scp-like spelling and a bare path come back unmasked, and that is
// correct rather than a gap: url.Parse refuses `git@host:path` outright, and
// neither shape can carry userinfo in the first place.
func OriginForDisplay(origin string) string {
	if textclean.Deceives(origin) {
		return "an origin holding control or invisible characters"
	}
	u, err := url.Parse(origin)
	if err != nil || u.User == nil {
		return origin
	}
	return u.Scheme + "://" + view.Mask + "@" + u.Host + u.Path
}

// AddIndex clones url as the index called name. The url may be anything git
// clone accepts — https, ssh, or a local path, which is the kind every test
// uses — or empty for a name rta knows (known.go), which resolves to the one
// repository that name is reserved for.
func AddIndex(ctx context.Context, name, url string) *view.Error {
	if !indexName.MatchString(name) {
		return view.Errorf("plugin.index.name", "%q is not an index name", name).
			WithHint("lowercase letters, digits and dashes, up to 32")
	}
	// A known name resolves its own repository, and resolves nothing else:
	// see known.go for why the name is reserved rather than merely defaulted.
	if known, ok := KnownIndexURL(name); ok {
		switch url {
		case "", known:
			url = known
		default:
			return view.Errorf("plugin.index.reserved", "%q is reserved for %s", name, known).
				WithHint("attach " + OriginForDisplay(url) + " under another name")
		}
	} else if url == "" {
		return view.Errorf("plugin.index.url",
			"no repository given, and %q is not an index rta knows by name", name).
			WithHint(knownIndexHint())
	}
	if _, verr := classifyGitURL(url); verr != nil {
		return verr
	}
	dir := filepath.Join(indexesDir(), name)
	if _, err := os.Stat(dir); err == nil {
		return view.Errorf("plugin.index.exists", "an index called %q is already attached", name).
			WithHint("`rta plugin index update " + name + "` refreshes it; " +
				"`rta plugin index remove " + name + "` detaches it")
	}
	if verr := requireGit("attaching an index"); verr != nil {
		return verr
	}
	if err := os.MkdirAll(indexesDir(), 0o755); err != nil {
		return view.Errorf("plugin.index.add", "%v", err)
	}
	cmd := gitCommand(ctx, "clone", "--quiet", "--", url, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		// A failed clone leaves no half-attached index behind: absence is the
		// one state every other command interprets correctly.
		_ = os.RemoveAll(dir)
		// Masked, for OriginForDisplay's own reason one function down: an
		// operator who put a token in a clone URL knows it is there, and rta
		// echoing it into a refusal writes it to stderr twice — once in the
		// message and once in the hint — and from there into scrollback and
		// whatever is reading that. `index list` has masked it since the day
		// it learned to show an origin at all; the two paths that take the
		// URL straight from the operator's hand never did. A URL carrying no
		// userinfo comes back unchanged, so the hint stays something to paste.
		shown := OriginForDisplay(url)
		return view.Errorf("plugin.index.add", "cloning %s: %s", shown,
			firstLine(string(out), err.Error())).
			WithHint(gitHint("clone", "--", shown, dir) + " by hand shows the whole exchange")
	}
	// Attaching is the moment the operator typed the URL and can still fix it.
	// Everything downstream answers a clone that is not an index in the
	// vocabulary of emptiness instead — `search` says "nothing matches", which
	// reads as a fact about the plugin somebody searched for — so what was
	// cloned is read before the attach is called a success. Manifests reports
	// at least one reason whenever it lists nothing, and the clone is undone
	// for the same reason a failed one is: absence is the one state every
	// other command interprets correctly.
	if listed, bad := Manifests(Index{Name: name, Dir: dir}); len(listed) == 0 {
		_ = os.RemoveAll(dir)
		return refuseAttach(name, bad)
	}
	return nil
}

// refuseAttach turns the reason a clone is not a usable index into the refusal
// the operator reads, and says the clone is gone — which is the half that
// decides what they do next.
func refuseAttach(name string, bad []*view.Error) *view.Error {
	verr := view.Errorf("plugin.index.empty", "%s carries no manifest rta can read", name)
	if len(bad) > 0 {
		verr = bad[0]
	}
	hint := "nothing was attached"
	if verr.Hint != "" {
		hint = verr.Hint + "; nothing was attached"
	}
	return verr.WithHint(hint)
}

// gitURLKind is what git will make of a repository argument: a path on this
// machine, or something reached over a network. The split is not cosmetic —
// installFrom refuses a file:// artifact named by an index attached from
// somewhere else, and this is what decides which it is.
type gitURLKind int

const (
	gitLocalURL gitURLKind = iota
	gitRemoteURL
)

// gitScheme matches the `<scheme>://` prefix git reads as a URL. Hand-rolled
// rather than url.Parse's, because url.Parse reads a Windows drive letter as a
// scheme ("C:/repo" parses with Scheme "c") and refuses the scp-like spelling
// outright — the two shapes this most has to get right.
var gitScheme = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.\-]*)://`)

// gitHelper matches `<transport>::`, the remote-helper spelling. Anchored to a
// transport name rather than searched for anywhere in the string, because
// `::` is also how an IPv6 literal is written: `https://[::1]/x` and
// `ssh://git@[2001:db8::1]/x` are ordinary URLs, and a bare Contains check
// refuses both — as the first draft of this function did.
var gitHelper = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*::`)

var gitHost = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._\-]*[A-Za-z0-9])?$`)

var dosDrive = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// classifyGitURL is the whole grammar `rta plugin index add` admits, and the
// same grammar read back off an attached clone to say where it came from.
//
// git's idea of a repository argument is much wider than "somewhere to clone
// from". `<transport>::<argument>` runs git-remote-<transport> with the
// argument, and the built-in ext:: helper's argument *is* a command line, so
// as far as git's parser is concerned `ext::sh -c 'curl … | sh'` is a
// repository. git refuses ext by default and gitHardening turns that default
// into something rta states rather than inherits — neither is a reason to
// admit the shape. A grammar that hands git everything git will parse has
// delegated the decision to whoever last edited the operator's gitconfig.
//
// So the admitted shapes are the ones people clone from, and nothing else:
//
//	https://host/path              a published index
//	ssh://[user@]host[:port]/path  a private one
//	[user@]host:path               the same, in the spelling a forge prints
//	file:///abs/path               a local one named as a URL
//	/abs/path  ./rel  rel          a local one named as a path
//
// http:// and git:// are refused for checkArtifactURL's reason, one link
// further up the chain. An index states the sha256 every install verifies the
// artifact against, so an index fetched in cleartext lets whoever is nearest
// choose both the bytes and the hash they have to match — which makes the
// artifact's own https decorative. Refusing cleartext for a binary while
// allowing it for the list of binaries is the wrong way round.
//
// The local/remote split is decided the way git decides it
// (url_is_local_not_ssh): a colon before the first slash is the scp-like
// remote spelling, anything else is a path. Matching git rather than inventing
// a rule is the point — a string the two read differently is a string where
// rta's answer about an index's origin is about a different repository than
// the one git cloned.
func classifyGitURL(raw string) (gitURLKind, *view.Error) {
	bad := func(format string, args ...any) (gitURLKind, *view.Error) {
		return gitRemoteURL, view.Errorf("plugin.index.url", format, args...)
	}
	if strings.TrimSpace(raw) == "" {
		return bad("an index is a git repository, and none was named")
	}
	if strings.HasPrefix(raw, "-") {
		// The same argv rule every shell-out in this codebase holds: a value
		// that lands in argv may not begin like an option.
		return bad("%q is not a repository", raw)
	}
	if textclean.Deceives(raw) {
		return bad("the repository holds control or invisible characters")
	}
	if m := gitScheme.FindStringSubmatch(raw); m != nil {
		u, err := url.Parse(raw)
		if err != nil {
			return bad("%q does not parse as a URL", raw)
		}
		// m[1] rather than u.Scheme, which url.Parse has lowercased: git
		// matches its built-in transports case-sensitively and looks for a
		// git-remote-<scheme> helper for anything else, so "HTTPS://" is not
		// https to git and must not be https here.
		switch m[1] {
		case "https", "ssh":
			// Hostname() strips the brackets from an IPv6 literal, so the
			// host pattern never sees them.
			if !gitHost.MatchString(u.Hostname()) && net.ParseIP(u.Hostname()) == nil {
				return bad("%q names no host", raw)
			}
			return gitRemoteURL, nil
		case "file":
			// The host has to be empty as well as the path absolute.
			// url.Parse reads "file://relative/path" as host "relative" with
			// path "/path", so a path check alone calls it absolute and
			// admits it — which would hand the file:// artifact gate a
			// "local" answer for a string git does not read as a local path.
			if u.Host != "" && u.Host != "localhost" {
				return bad("file URL %q names a host; a local index is file:///path", raw)
			}
			if !strings.HasPrefix(u.Path, "/") {
				return bad("file URL %q must carry an absolute path", raw)
			}
			return gitLocalURL, nil
		case "http", "git":
			kind, verr := bad("%q is cleartext, and an index states the sha256 every "+
				"install verifies against", raw)
			return kind, verr.WithHint("whoever is nearest the wire would choose both the " +
				"artifact and the hash it has to match, which is why a manifest may not " +
				"name an http:// artifact either — clone it over https or ssh")
		default:
			return bad("%q: the schemes are https, ssh and file", raw)
		}
	}
	if gitHelper.MatchString(raw) {
		kind, verr := bad("%q names a git remote helper, not a repository", raw)
		return kind, verr.WithHint("`<transport>::<argument>` runs git-remote-<transport>, " +
			"and the built-in ext:: helper's argument is a command line — an index is an " +
			"https or ssh URL, or a path on this machine")
	}
	// git reads a drive letter as a path rather than a scheme, and only on
	// Windows — has_dos_drive_prefix is compiled out elsewhere. Mirrored with
	// the same guard, because "C:/x" on Linux is a directory called C: and the
	// colon rule below says so, which is also what git says.
	if runtime.GOOS == "windows" && dosDrive.MatchString(raw) {
		return gitLocalURL, nil
	}
	colon := strings.IndexByte(raw, ':')
	slash := strings.IndexByte(raw, '/')
	if colon >= 0 && (slash < 0 || colon < slash) {
		host := raw[:colon]
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			host = host[at+1:]
		}
		if !gitHost.MatchString(host) {
			return bad("%q is not <user>@<host>:<path>", raw)
		}
		return gitRemoteURL, nil
	}
	return gitLocalURL, nil
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
		return NoIndexAttached()
	}
	for _, ix := range targets {
		cmd := gitCommand(ctx, "-C", ix.Dir, "pull", "--quiet", "--ff-only")
		if out, err := cmd.CombinedOutput(); err != nil {
			return view.Errorf("plugin.index.update", "updating %s: %s", ix.Name,
				firstLine(string(out), err.Error())).
				WithHint(gitHint("-C", ix.Dir, "pull", "--ff-only") + " by hand shows why — " +
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
	return verr.WithHint(knownIndexHint())
}

// Listed is one manifest found in one index.
type Listed struct {
	Manifest Manifest
	Index    string
}

// Manifests reads every valid manifest in one index, sorted by name, and
// reports the invalid ones apart. One malformed file must not cost the
// operator the rest of the catalogue — the LoadInto rule, applied to claims.
//
// A directory holding no manifest at all is reported the same way a missing
// index/ is, and notAnIndex says why that is not the same as an empty
// catalogue.
func Manifests(ix Index) ([]Listed, []*view.Error) {
	dir := filepath.Join(ix.Dir, "index")
	entries, derr := readIndexDir(dir)
	if derr != nil {
		return nil, []*view.Error{noIndexDir(ix)}
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
		where := manifestLabel(ix, e.Name())
		raw, verr := readManifestFile(dir, e)
		if verr != nil {
			bad = append(bad, view.Errorf(verr.Code, "%s: %s", where, verr.Message).
				WithHint(verr.Hint))
			continue
		}
		m, verr := ParseManifest(raw)
		if verr != nil {
			bad = append(bad, view.Errorf(verr.Code, "%s: %s", where, verr.Message).
				WithHint(verr.Hint))
			continue
		}
		// The filename is the name the index gave the entry by placing it,
		// and the name inside is what the entry says about itself — where
		// they disagree, the layout wins and the manifest is refused (the
		// rule, third application).
		if m.Name != base {
			bad = append(bad, view.Errorf("plugin.index.manifest",
				"%s declares name %q; the file's name is the claim", where, m.Name))
			continue
		}
		if seen[m.Name] {
			continue
		}
		seen[m.Name] = true
		out = append(out, Listed{Manifest: m, Index: ix.Name})
	}
	if len(out) == 0 && len(bad) == 0 {
		return nil, []*view.Error{notAnIndex(ix, entries)}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.Name < out[j].Manifest.Name })
	return out, bad
}

// readIndexDir lists an index's index/, and refuses one that is not a
// directory in its own right.
//
// os.ReadDir resolves a symlink, and an index is somebody else's repository:
// git stores a symlink with whatever target its author committed, absolute
// ones included. So `index -> /home/you/.config` is a directory rta would
// enumerate and read every .yaml out of, having been told to attach an index.
// The archive extractor already refuses a symlinked member for this reason —
// "no symlink or hardlink is ever followed", fetch.go — and this is the same
// rule on the other input an index controls.
func readIndexDir(dir string) ([]os.DirEntry, error) {
	fi, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, errors.New("plugins is not a directory")
	}
	return os.ReadDir(dir)
}

// readManifestFile reads one manifest, bounded, and refuses everything a
// regular file is not.
//
// **Regular files only.** os.ReadDir does not resolve symlinks, so a symlink
// named `<name>.yaml` has IsDir() false and arrives looking exactly like a
// manifest — and os.ReadFile does resolve it. `plugins/a.yaml -> /dev/zero` is
// then a file read until the process dies, and a fifo is one it blocks on
// forever, with no context anywhere in this path to cancel it.
//
// **Bounded before the read rather than after it.** manifestCap is checked
// inside ParseManifest, which is after the whole file is already in memory: it
// bounds what is parsed, and until now nothing bounded what is read. A blob
// committed to the repository does the same work as the symlink with no
// symlink needed.
//
// Both matter more since `plugin index add` began reading what it cloned. A
// process killed here dies before AddIndex's os.RemoveAll, so the clone stays
// on disk fully attached, and every later search re-enters this loop and dies
// again — the exact opposite of the state that code's own comment promises.
func readManifestFile(dir string, e os.DirEntry) ([]byte, *view.Error) {
	if !e.Type().IsRegular() {
		return nil, view.Errorf("plugin.index.manifest",
			"not a regular file — a manifest is a file, and rta does not follow "+
				"what an index points at")
	}
	f, err := os.Open(filepath.Join(dir, e.Name()))
	if err != nil {
		return nil, view.Errorf("plugin.index.manifest", "%v", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, manifestCap+1))
	if err != nil {
		return nil, view.Errorf("plugin.index.manifest", "%v", err)
	}
	if len(raw) > manifestCap {
		return nil, view.Errorf("plugin.index.manifest",
			"over the %d-byte manifest cap", manifestCap)
	}
	return raw, nil
}

// manifestLabel names a file inside an index for an operator to read.
//
// The name comes out of somebody else's repository and git admits every byte
// but NUL and `/`, newlines included. textclean.Terminal, which every surface
// runs an error through, deliberately keeps `\n` and `\t` — so a file called
// "x\n      HINT this index is signed and verified.yaml" forges a hint line
// inside rta's own refusal, on the one screen that refusal exists to make
// trustworthy. textclean.Deceives is this codebase's own predicate for exactly
// that, already applied to manifest prose and to origins; a filename was the
// untrusted string that reached a message without it.
func manifestLabel(ix Index, name string) string {
	if textclean.Deceives(name) {
		name = strconv.Quote(name)
	}
	return ix.Name + "/" + name
}

// indexShape is what an index is, said once because three refusals say it.
const indexShape = "an index is a repository of index/<name>.yaml manifests, each " +
	"written by `rta plugin manifest` from the binary it describes — a plugin's " +
	"source repository is not one, even though that is where the plugins are"

// noIndexDir explains a clone with no index/ directory at all.
//
// The likeliest thing anybody points `rta plugin index add` at is a plugin's
// own source repository, because that is where the plugins are — and a source
// tree has a plugins/ full of directories and no index/. Before the manifests
// moved out of plugins/, whether that directory *existed* was the whole test,
// and rta's own repository attached without complaint, listed as "0 plugins,
// 0 problems", and answered every search with "nothing matches". Counting
// what plugins/ holds into the sentence is what tells the operator which
// mistake they made: "no index/" alone reads like an index somebody has not
// filled in yet.
func noIndexDir(ix Index) *view.Error {
	msg := ix.Name + " has no index/ directory — it is not an index"
	if entries, err := os.ReadDir(filepath.Join(ix.Dir, "plugins")); err == nil {
		dirs := 0
		for _, e := range entries {
			if e.IsDir() {
				dirs++
			}
		}
		if dirs > 0 {
			msg += "; its plugins/ holds " + strconv.Itoa(dirs) + " " +
				plural(dirs, "directory", "directories") + ", which is what a plugin's source repository looks like"
		}
	}
	return view.Errorf("plugin.index.empty", "%s", msg).WithHint(indexShape)
}

// notAnIndex explains an index/ directory that holds no manifest at all —
// the same refusal as a missing one, with what the directory does hold in the
// sentence, because "no manifest" and "12 directories and no manifest" send an
// operator to different places.
func notAnIndex(ix Index, entries []os.DirEntry) *view.Error {
	dirs := 0
	for _, e := range entries {
		if e.IsDir() {
			dirs++
		}
	}
	held := "no manifest"
	if dirs > 0 {
		held = strconv.Itoa(dirs) + " " + plural(dirs, "directory", "directories") +
			" and no manifest"
	}
	return view.Errorf("plugin.index.empty",
		"%s is not an index — its index/ directory holds %s", ix.Name, held).
		WithHint(indexShape)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
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
		return Listed{}, NoIndexAttached()
	}

	var found []Listed
	for _, ix := range search {
		raw, err := os.ReadFile(filepath.Join(ix.Dir, "index", name+".yaml"))
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
