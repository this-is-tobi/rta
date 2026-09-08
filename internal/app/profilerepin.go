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
	// The two gates checkPin opens with, made here before anything is touched:
	// repinning to an artifact that is not installed, or to a namespace that
	// has no artifact at all, is not a smaller version of the operation, it is
	// a different and wrong one. Its remaining cases all ask whether an entry's
	// pin matches what is installed — which is the thing this command exists to
	// rewrite, so asking it here would refuse every entry worth repinning.
	var (
		o     registry.Origin
		known bool
	)
	if installed != nil {
		o, known = installed.Origin(ns)
	}
	if !known {
		// Installed-and-unapproved told apart from missing, the distinction
		// checkPin draws and for a reason that lands hardest right here: trust
		// is keyed on the digest, so a rebuild drops the approval, and repin is
		// the command somebody runs *after* a rebuild. Told "not a registered
		// plugin", they go looking for an install that is already on the disk.
		if w, ok := installed.(interface{ Untrusted(string) bool }); ok && w.Untrusted(ns) {
			return nil, view.Errorf("core.profile.untrustedplugin",
				"%q is installed and has not been run", ns).
				WithHint("`rta plugin trust " + ns + "` approves the artifact; rebuilding a " +
					"plugin changes it, so it needs approving again")
		}
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

	// The one place a key is measured against what was asked for, so the
	// collision scan and the rewrite below cannot drift on what "matching"
	// means — and the one place the target key is spelled, so they cannot
	// drift on that either.
	matches := func(key string) (string, string, bool) {
		keyNs, instance, pin := config.SplitKey(key)
		if keyNs != ns || (wantInstance != "" && instance != wantInstance) {
			return "", "", false
		}
		return instance, pin, true
	}
	named := func(instance string) string {
		if instance == "" {
			return ns
		}
		return ns + "/" + instance
	}

	var (
		rows []repinRow
		verr *view.Error
	)
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		rows, verr = nil, nil
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
			keys := p.PluginKeys()
			// Every matching entry rewrites to one key per instance, so two of
			// them under the same label land on top of each other: the second
			// assignment wins, the first entry's set:/secrets: block goes with
			// it, and both are reported as repinned. `profile set` keeps a
			// profile from reaching that state — applyConnectionFlags re-pins
			// in place rather than adding a second entry, and says so in the
			// receipt — but this file is meant to be hand-editable, and a
			// merge of two machines' configs can hold both.
			//
			// Refused before anything is written, and for the whole run rather
			// than the one profile: Mutate commits the file in one piece, so a
			// run that repaired nine profiles and quietly flattened the tenth
			// is not a smaller version of this. Which block survives is the
			// operator's answer to give, not a race between map keys.
			claimed := map[string]string{}
			for _, key := range keys {
				instance, _, ok := matches(key)
				if !ok {
					continue
				}
				if first, dup := claimed[instance]; dup {
					verr = view.Errorf("core.profile.repin.collision",
						"%s holds two entries for %q — %s and %s — and repinning would fold one onto the other",
						name, named(instance), first, key).
						WithHint("keep the one whose set:/secrets: block you want by removing the other from " +
							config.Path() + ", then repin — nothing was written")
					return cfg, false
				}
				claimed[instance] = key
			}
			for _, key := range keys {
				instance, pin, ok := matches(key)
				if !ok {
					continue
				}
				if pin == newPin {
					rows = append(rows, repinRow{name, key, "already pinned"})
					continue
				}
				newKey := named(instance) + "@" + newPin
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
	if verr != nil {
		return nil, verr
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
