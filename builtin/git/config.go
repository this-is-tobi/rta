package git

import (
	"context"

	gitconfig "github.com/go-git/go-git/v5/config"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func configCapability() plugin.Capability {
	return plugin.Capability{
		ID:         "git.config",
		Summary:    "Effective config across system, global and local scope",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "Every key set in system, global or local config, one row per scope it's " +
			"set in — the same three files `git config --list --show-origin` reads, before any of " +
			"them override each other: local wins over global, global wins over system. A key " +
			"missing from a scope simply has no row there rather than one with an empty value. " +
			"`[include]`/`[includeIf]` directives are shown as written, not followed into the file " +
			"they point at.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
		},
		Run: runConfig,
	}
}

func runConfig(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, verr := openRepo(ctx, req.String("path"))
	if verr != nil {
		return nil, verr
	}

	t := view.Table{Columns: []view.Column{
		{Name: "Scope"},
		{Name: "Key"},
		{Name: "Value"},
	}}

	// LoadConfig never errors for a scope with no file on this machine — it
	// hands back an empty Config instead (config.LoadConfig, go-git v5.19.2)
	// — so system/global are only ever missing rows, never a failure this
	// capability has to special-case.
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
			t.Rows = append(t.Rows, []string{scope, s.Name + "." + o.Key, o.Value})
		}
		for _, sub := range s.Subsections {
			for _, o := range sub.Options {
				t.Rows = append(t.Rows, []string{scope, s.Name + "." + sub.Name + "." + o.Key, o.Value})
			}
		}
	}
}
