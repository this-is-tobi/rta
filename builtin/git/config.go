package git

import (
	"context"
	"regexp"
	"strings"

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
		Description: "Every key set in system, global or local config, one row per scope it's " +
			"set in — the same three files `git config --list --show-origin` reads, before any of " +
			"them override each other: local wins over global, global wins over system. A key " +
			"missing from a scope simply has no row there rather than one with an empty value. " +
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
	repo, verr := openRepo(ctx, req)
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
		// LoadConfig never errors for a scope with no file on this machine —
		// it hands back an empty Config instead (config.LoadConfig, go-git
		// v5.19.2) — so system/global are only ever missing rows, never a
		// failure this capability has to special-case.
		system, err := gitconfig.LoadConfig(gitconfig.SystemScope)
		if err != nil {
			return nil, view.Errorf("git.config.failed", "reading system config: %v", err)
		}
		addConfigRows(&t, "system", system)

		global, err := gitconfig.LoadConfig(gitconfig.GlobalScope)
		if err != nil {
			return nil, view.Errorf("git.config.failed", "reading global config: %v", err)
		}
		addConfigRows(&t, "global", global)
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
	return out
}

// urlUserinfo matches scheme://user:password@ — the password group is what
// gets replaced. Deliberately requires the colon: `ssh://git@host` names a
// user and no secret, and masking it would hide something that is not one.
var urlUserinfo = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)([^/:@\s]+):[^@/\s]+@`)
