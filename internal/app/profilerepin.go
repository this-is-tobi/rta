package app

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/view"
)

// profileRepinCommand closes the gap `rta plugin upgrade` opens: trust binds
// to the artifact's digest, so upgrading rewrites it, and every profile
// entry naming the plugin's old pin then refuses with core.profile.stalepin
// until it is rewritten to match. `rta profile set <name> --plugin <ns>`
// already resolves the new digest instead of asking anyone to type it — see
// pinKey — but doing that once per profile, once per instance, is the part
// this closes: one command re-keys every entry an upgrade left stale, or
// exactly the one profile named, leaving every set:/secrets:/kube:/ssh:
// block untouched.
func profileRepinCommand(reg *registry.Registry, render renderFn, opts *globalOpts) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repin [profile]",
		Short: "Point every stale profile entry at the plugin artifact actually installed",
		Long: "Trust binds to a digest, never to a name or a version, so `rta plugin upgrade`\n" +
			"leaves every profile entry naming the plugin's old artifact refusing with\n" +
			"\"this profile's pin does not match the installed\" until it is repinned.\n\n" +
			"This rewrites the plugins: key of every matching entry to the digest that is\n" +
			"actually installed right now, keeping its set:/secrets:/kube:/ssh: block exactly\n" +
			"as it was — the same re-pin `rta profile set --plugin` already makes for one\n" +
			"profile, done across every one at once.\n\n" +
			"Name a profile to repin only that one; --all repins every profile that has an\n" +
			"entry for the plugin. `--plugin pg/analytics` narrows to one instance; `--plugin\n" +
			"pg` repins the default entry and every instance together, since one binary is\n" +
			"what all of them pin.",
		Example: "  rta plugin upgrade pg\n" +
			"  rta profile repin --all --plugin pg\n" +
			"  rta profile repin staging --plugin pg/analytics",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeProfiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			only := ""
			if len(args) == 1 {
				only = args[0]
			}
			all, _ := cmd.Flags().GetBool("all")
			v, verr := runProfileRepin(cmd, only, all, reg, opts.dryRun)
			return render(cmd, v, verr)
		},
	}
	cmd.Flags().String("plugin", "", "the plugin to repin — `pg`, or `pg/analytics` for one instance of it")
	cmd.Flags().Bool("all", false, "repin every profile that has an entry for the plugin, not just one named on the command line")
	_ = cmd.MarkFlagRequired("plugin")
	_ = cmd.RegisterFlagCompletionFunc("plugin", completeInstalledPlugins)
	return cmd
}

// repinRow is one entry a repin considered — reported whether it changed or
// was already correct, so a run says what it checked and not only what it
// wrote.
type repinRow struct {
	profile, key, status string
}

func runProfileRepin(cmd *cobra.Command, only string, all bool, reg *registry.Registry, dryRun bool) (view.View, *view.Error) {
	if verr := refuseUnhonouredConfig(); verr != nil {
		return nil, verr
	}
	if only == "" && !all {
		return nil, view.Errorf("core.profile.repin.scope",
			"name a profile, or pass --all to repin every one of them").
			WithHint("`rta profile repin --all --plugin pg`, or `rta profile repin staging --plugin pg`")
	}
	if only != "" && all {
		return nil, view.Errorf("core.profile.repin.scope",
			"a profile name and --all say two different things about which profiles to touch").
			WithHint("drop one or the other")
	}
	want := strings.TrimSpace(mustString(cmd, "plugin"))
	if want == "" {
		return nil, view.Errorf("core.profile.repin.noplugin", "name the plugin to repin").
			WithHint("`--plugin pg`, or `--plugin pg/analytics` for one instance of it")
	}
	ns, wantInstance, _ := config.SplitKey(want)
	if wantInstance != "" && !config.ValidInstance(wantInstance) {
		return nil, view.Errorf("core.profile.instance.invalid",
			"%q is not a valid instance label", wantInstance).
			WithHint("lowercase letters, digits and dashes, starting with a letter")
	}
	// The same three checks checkPin makes, made here before anything is
	// touched: repinning to an artifact that is not installed, or not
	// external, is not a smaller version of the operation, it is a
	// different and wrong one.
	var (
		o     registry.Origin
		known bool
	)
	if installed != nil {
		o, known = installed.Origin(ns)
	}
	if !known {
		return nil, view.Errorf("core.profile.unknownplugin",
			"%q is not a registered plugin", ns).
			WithHint("`rta plugin list` shows what is installed, including anything found and not run")
	}
	if !o.External() {
		return nil, view.Errorf("core.profile.pinned",
			"%q is built in and has no artifact to pin", ns).
			WithHint("nothing to repin — a built-in namespace is never pinned")
	}
	newPin := o.Short()

	var rows []repinRow
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		rows = nil
		wrote := false
		names := cfg.ProfileNames()
		if only != "" {
			if _, ok := cfg.Profiles[only]; !ok {
				return cfg, false // reported as unknownProfile below, outside the lock
			}
			names = []string{only}
		}
		for _, name := range names {
			p := cfg.Profiles[name]
			for _, key := range p.PluginKeys() {
				keyNs, instance, pin := config.SplitKey(key)
				if keyNs != ns || (wantInstance != "" && instance != wantInstance) {
					continue
				}
				if pin == newPin {
					rows = append(rows, repinRow{name, key, "already pinned"})
					continue
				}
				newKey := ns
				if instance != "" {
					newKey += "/" + instance
				}
				newKey += "@" + newPin
				status := "repinned to " + newKey
				if dryRun {
					status = "would repin to " + newKey
				} else {
					conn := p.Plugins[key]
					delete(p.Plugins, key)
					p.Plugins[newKey] = conn
					cfg.Profiles[name] = p
					wrote = true
				}
				rows = append(rows, repinRow{name, key, status})
			}
		}
		return cfg, wrote
	}); err != nil {
		return nil, view.AsError(err, "core.profile.write")
	}
	if only != "" {
		if cfg, err := config.LoadFile(); err == nil {
			if _, ok := cfg.Profiles[only]; !ok {
				return nil, unknownProfile(cfg, only)
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].profile != rows[j].profile {
			return rows[i].profile < rows[j].profile
		}
		return rows[i].key < rows[j].key
	})
	if len(rows) == 0 {
		scope := "any profile"
		if only != "" {
			scope = "profile " + only
		}
		return view.KeyValue{Pairs: []view.Pair{
			{Key: "repin", Value: "nothing to do — " + want + " names no entry in " + scope},
		}}, nil
	}
	t := view.Table{Columns: []view.Column{{Name: "Profile"}, {Name: "Key"}, {Name: "Status"}}}
	for _, r := range rows {
		t.Rows = append(t.Rows, []string{r.profile, r.key, r.status})
	}
	t.Total = len(t.Rows)
	return t, nil
}
