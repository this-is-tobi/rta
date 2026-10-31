package app

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/view"
)

// configRepinCommand is `rta profile repin` for the `plugins:` section. An
// upgrade rewrites the plugin's digest, the section pinned to the old one stops
// applying, and every call of the plugin is refused until it names the new one
// (pluginconf.Resolver.Withheld). `rta config set plugins.<ns>.<key>` writes
// under the installed pin already, one key at a time, and left the old heading
// and the rest of its keys behind it; this moves the whole section in one
// command, every key under it as it was.
func configRepinCommand(render renderFn, opts *globalOpts) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "repin",
		Annotations: outputExempt(),
		Short:       "Point a plugin's section at the build that is installed",
		Long: "Trust binds to a digest, never to a name or a version, so after `rta plugin upgrade`\n" +
			"the plugins: section pinned to the old build no longer applies, and rta refuses the\n" +
			"plugin's calls until it names the installed one rather than run them without it.\n\n" +
			"This rewrites the section's heading to the digest that is installed right now and\n" +
			"keeps every key under it, in the file it is in. A heading with no pin, or one too\n" +
			"short to trust, is pinned the same way.",
		Example: "  rta plugin upgrade pg\n" +
			"  rta config repin --plugin pg",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, verr := runConfigRepin(strings.TrimSpace(mustString(cmd, "plugin")), opts.dryRun)
			return render(cmd, v, verr)
		},
	}
	cmd.Flags().String("plugin", "", "the plugin whose section to repin — `pg`")
	_ = cmd.MarkFlagRequired("plugin")
	completeFlag(cmd, "plugin", completeInstalledPlugins)
	return cmd
}

// runConfigRepin is `rta config repin`.
func runConfigRepin(ns string, dryRun bool) (view.View, *view.Error) {
	if !config.TrustedPath() {
		return nil, view.Errorf("core.config.untrusted",
			"the plugins: sections of %s are not honoured, so repinning one there would do nothing", config.Path()).
			WithHint("set $RTA_CONFIG to the file you mean — a config file found in the working " +
				"directory could belong to a repository you cloned, so its sections are ignored")
	}
	if ns == "" {
		return nil, view.Errorf("core.config.repin.noplugin", "name the plugin whose section to repin").
			WithHint("`--plugin pg`")
	}
	// One section a plugin, for every instance of it: an instance is a profile's
	// idea, and a pin is the heading's, which `--plugin` does not take.
	if cut := strings.IndexAny(ns, "/@"); cut >= 0 {
		name := ns[:cut]
		if name == "" {
			name = "pg"
		}
		return nil, view.Errorf("core.config.repin.plugin",
			"%q is not a plugin's name: a plugins: section is one for each plugin, under its name", ns).
			WithHint("`--plugin " + name + "`")
	}
	o, refused := repinOrigin(ns, "config", "rta config repin --plugin "+ns)
	if refused != nil {
		return nil, refused
	}
	heading := ns + "@" + o.Short()

	var (
		from string
		verr *view.Error
	)
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		from, verr = "", nil
		var named []string
		for section := range cfg.Plugins {
			if sectionNS, _, _ := strings.Cut(section, "@"); sectionNS == ns {
				named = append(named, section)
			}
		}
		sort.Strings(named)
		switch {
		case len(named) == 0:
			return cfg, false
		case len(named) > 1:
			// Which section's keys survive is the operator's answer to give, as
			// profile repin's collision says of two entries under one label: the
			// rename would put one heading on top of the other, and the old pin an
			// upgrade left beside a new one is the usual way to have both.
			verr = view.Errorf("core.config.repin.collision",
				"plugins: holds %d sections for %q — %s — and repinning would fold one onto another",
				len(named), ns, strings.Join(named, ", ")).
				WithHint("keep the one whose keys you want by removing the others from " +
					config.Where("plugins", named[0]) + ", then repin — nothing was written")
			return cfg, false
		}
		from = named[0]
		if from == heading || dryRun {
			return cfg, false
		}
		cfg.Plugins[heading] = cfg.Plugins[from]
		delete(cfg.Plugins, from)
		return cfg, true
	}); err != nil {
		return nil, view.AsError(err, "core.config.write")
	}
	if verr != nil {
		return nil, verr
	}
	file := config.Where("plugins", heading)
	switch {
	case from == "":
		return view.KeyValue{Pairs: []view.Pair{{Key: "unchanged",
			Value: ns + " has no plugins: section — nothing written"}}}, nil
	case from == heading:
		return view.KeyValue{Pairs: []view.Pair{{Key: "unchanged",
			Value: "plugins." + heading + " names the installed build — nothing written to " + file}}}, nil
	case dryRun:
		return view.KeyValue{Pairs: []view.Pair{{Key: "would write",
			Value: "plugins." + from + " repinned to " + heading + " in " + config.Where("plugins", from)}}}, nil
	}
	return view.KeyValue{Pairs: []view.Pair{{Key: "wrote",
		Value: "plugins." + from + " repinned to " + heading + " in " + file}}}, nil
}
