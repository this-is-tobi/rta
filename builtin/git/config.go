package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	gitconfig "github.com/go-git/go-git/v5/config"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func configCapability() plugin.Capability {
	return plugin.Capability{
		ID:           "git.config",
		Summary:      "Effective config across system, global and local scope",
		Safety:       plugin.Read,
		HostSpecific: true,
		Idempotent:   true,
		Description: "Every key set in system, global or local config, one row per file it's " +
			"set in — the files `git config --list --show-origin` reads, both global ones " +
			"included, before any of them override each other: local wins over global, global " +
			"wins over system. A key missing from a scope simply has no row there rather than " +
			"one with an empty value. " +
			"`[include]`/`[includeIf]` directives are shown as written, not followed into the file " +
			"they point at. Over MCP only the repository's own config is returned: the machine-wide " +
			"scopes are the operator's, not the repository's. Values that carry a credential are " +
			"masked on every surface.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
		},
		Run: runConfig,
	}
}

func runConfig(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, verr := openRepoConfigOnly(ctx, req)
	if verr != nil {
		return nil, verr
	}

	t := view.Table{Columns: []view.Column{
		{Name: "Scope"},
		{Name: "Key"},
		{Name: "Value"},
	}}

	// **The machine-wide scopes are the operator's, not the repository's.**
	//
	// The package doc says every capability here is Read because "a
	// repository's history and diffs are not credentials", and that is true
	// of the other seven. It was never true of this one: system and global
	// config are not the repository at all, and they are where git keeps
	// credentials by convention — `url.https://oauth2:glpat-…@gitlab.com/
	// .insteadOf` is GitLab's own documented rewrite, `http.<url>.extraHeader`
	// carries an Authorization header for Azure DevOps, and `github.token` is
	// a plain PAT. So a Read capability with no grant handed an MCP caller the
	// operator's forge credentials, from outside the path root, on a default
	// server.
	//
	// Withheld from MCP rather than gated, because a grant is the wrong
	// instrument here: the agent has a real use for the repository's own
	// remotes and branch tracking, and no use at all for the operator's
	// machine-wide identity. Local stays; a person at a terminal, on their own
	// machine, still sees all three — the same rule Field.Local states for
	// inputs, applied to scopes.
	if req.Surface() != plugin.SurfaceMCP {
		// Every file git reads for these scopes, not go-git's LoadConfig,
		// which reads the first global file that exists and stops: with both
		// ~/.config/git/config and ~/.gitconfig present, git reads the two
		// and this showed one, so a key set only in the other was missing
		// from the answer to what git is configured with. A scope with no
		// file on this machine is missing rows, never a failure.
		machine, err := machineConfigs()
		if err != nil {
			return nil, view.Errorf("git.config.failed", "reading the machine-wide config: %v", err)
		}
		for _, m := range machine {
			addConfigRows(&t, m.scope, m.config)
		}
	}

	// repo.Config reads local scope for either kind of repository this
	// plugin opens: filesystem storage's own .git/config on disk, or the
	// config a remote clone synthesized in memory (its remote and branch
	// tracking, at minimum) — both implement the same ConfigStorer.
	local, err := repo.Config()
	if err != nil {
		return nil, view.Errorf("git.config.failed", "reading repository config: %v", err)
	}
	addConfigRows(&t, "local", local)

	t.Total = len(t.Rows)
	return t, nil
}

// systemGitConfig is the file git's system scope is read from here, the one
// go-git reads it from (config.Paths). A variable so a test can point it away
// from this machine's.
var systemGitConfig = "/etc/gitconfig"

// scopedConfig is one file of the operator's own git config, and the scope
// git reads it as.
type scopedConfig struct {
	scope  string
	config *gitconfig.Config
}

// machineConfigs is the operator's own git config, every file of it that
// exists, in the order git reads them: the system's, then the global scope's
// two, $XDG_CONFIG_HOME/git/config (~/.config/git/config when that is unset)
// and ~/.gitconfig. A later file's value wins over an earlier one's, as it
// does in git.
func machineConfigs() ([]scopedConfig, error) {
	type source struct{ scope, path string }
	sources := []source{{"system", systemGitConfig}}
	if home, err := os.UserHomeDir(); err == nil {
		xdg := os.Getenv("XDG_CONFIG_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		sources = append(sources,
			source{"global", filepath.Join(xdg, "git", "config")},
			source{"global", filepath.Join(home, ".gitconfig")})
	}
	var out []scopedConfig
	for _, s := range sources {
		// Opened without waiting, and read only when it is a file. git.hooks
		// reads these over MCP too, and a named pipe in place of one — which
		// unpacking an archive into a root that holds the home directory can
		// leave — blocked open(2) until a writer came, which no context can
		// interrupt, as the repository's own files did before openAt.
		f, err := os.OpenFile(s.path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if errors.Is(err, iofs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// And held to the bound a repository's own config is held to, since
		// go-git's reader takes the whole of it into memory first: a sparse
		// file of gigabytes costs its writer no disk.
		var cfg *gitconfig.Config
		info, err := f.Stat()
		switch {
		case err == nil && !info.Mode().IsRegular():
			err = errors.New("not a regular file")
		case err == nil && info.Size() > maxConfigBytes:
			err = tooLarge(maxConfigBytes)
		case err == nil:
			cfg, err = gitconfig.ReadConfig(io.LimitReader(f, info.Size()))
		}
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.path, err)
		}
		out = append(out, scopedConfig{scope: s.scope, config: cfg})
	}
	return out, nil
}

func addConfigRows(t *view.Table, scope string, cfg *gitconfig.Config) {
	for _, s := range cfg.Raw.Sections {
		for _, o := range s.Options {
			key := s.Name + "." + o.Key
			t.Rows = append(t.Rows, []string{scope, key, maskConfigValue(key, o.Value)})
		}
		for _, sub := range s.Subsections {
			for _, o := range sub.Options {
				// The subsection is part of the key and is itself a place a
				// credential hides: `url.https://tok@host/.insteadOf` carries
				// it in the *name*, not the value.
				key := s.Name + "." + sub.Name + "." + o.Key
				t.Rows = append(t.Rows, []string{scope, maskURLCredentials(key), maskConfigValue(key, o.Value)})
			}
		}
	}
}

// secretKey matches config keys whose value is a credential by convention.
//
// A name test, and therefore a heuristic — which is why it is the *second*
// line here rather than the only one. maskURLCredentials below is the
// syntactically certain half, the kind net.info's masking relies on, and it
// catches the shape that actually appears in the wild.
var secretKey = regexp.MustCompile(`(?i)(^|\.)(token|password|passwd|secret|apikey|api-key|extraheader|bearer)$`)

// maskConfigValue hides a value that carries a credential.
//
// Applied on every surface, not only MCP, for net.info's reason: a person
// asking "what is my git config" did not ask for their PAT in a terminal
// transcript, a tmux scrollback, or `-o json` piped somewhere. The value is
// still on disk in a file they own, one `git config` away.
func maskConfigValue(key, value string) string {
	if value == "" {
		return value
	}
	if secretKey.MatchString(key) {
		return view.Mask
	}
	return maskURLCredentials(value)
}

// maskURLCredentials replaces the password in any URL carrying userinfo.
//
// **This is the syntactically certain half.** `scheme://user:secret@host` has
// exactly one reading, so masking it needs no guess about what a key means —
// and it is the shape git's own documented credential patterns take, in
// `url.<base>.insteadOf` (where it lives in the key) and in remote URLs
// (where it lives in the value).
//
// The username survives: it is identity rather than secret, it is what makes
// the row worth reading at all, and GitLab's own pattern puts the constant
// "oauth2" there.
func maskURLCredentials(s string) string {
	out := s
	for _, m := range urlUserinfo.FindAllStringSubmatch(s, -1) {
		out = strings.Replace(out, m[0], m[1]+m[2]+":"+view.Mask+"@", 1)
	}
	// A second, independent pass for the colon-free spelling: GitHub's own
	// documented form for a PAT in a remote URL is `https://<token>@host/...`
	// — no colon, so urlUserinfo's pattern never matches it, and the token
	// reached an MCP agent in the clear. There is no way to tell that spelling
	// apart from an ordinary bare username from the string alone, so — the
	// same trade maskProxy already takes for a proxy URL — the whole userinfo
	// is masked rather than guessed at: `https://alice@github.com/...` shows
	// a masked username too. Runs after urlUserinfo on purpose, over its
	// output: anything that pass already replaced now carries the colon this
	// pattern requires to be absent, so it is not masked a second time.
	for _, m := range urlBareUserinfo.FindAllStringSubmatch(out, -1) {
		out = strings.Replace(out, m[0], m[1]+view.Mask+"@", 1)
	}
	return out
}

// urlUserinfo matches scheme://user:password@ — the password group is what
// gets replaced. Deliberately requires the colon: `ssh://git@host` names a
// user and no secret, and masking it would hide something that is not one.
var urlUserinfo = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)([^/:@\s]+):[^@/\s]+@`)

// urlBareUserinfo matches http(s)://user@ with no colon at all — GitHub's
// PAT-as-username spelling, and an ordinary bare username the same shape.
// Restricted to http(s), unlike urlUserinfo above: ssh://git@host names the
// conventional SSH user and no secret, and the SCP-like git@host:path form
// has no "://" for this pattern to anchor on in the first place — both are
// left alone, same as urlUserinfo already leaves them.
var urlBareUserinfo = regexp.MustCompile(`(https?://)([^/:@\s]+)@`)
