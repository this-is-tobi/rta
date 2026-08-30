package app

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/profile"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// `rta profile set` and `rta profile rm` — an environment without a terminal.
//
// # Why this exists
//
// Every way to configure rta went through a TTY. The TUI has a form per
// profile and a form per plugin inside it, both generated from the plugin's
// own declarations, and both of them know which inputs are credentials. What
// there was no way to do was state one from a script, a Dockerfile, a
// provisioning run or a dotfiles repository — the places setup actually
// happens for a team.
//
// So a team that cannot script it writes the YAML by hand instead, and that
// is the path with the weaker guarantee: nothing checks a hand-written block
// until something tries to use it, `secrets:` holds a reference while `set:`
// holds a value, and `set:` is the obvious-looking place to put a password.
// One of those ends up in a 0644 file, inert, doing nothing, and every later
// connection fails authentication for no visible reason.
//
// The point of a command is that it can refuse. `--set` cannot be given a
// declared credential, `--secret` cannot be given a value instead of a
// reference, and every value is typed against the declaration before it is
// written — so the shapes that used to be silently wrong are now the shapes
// that do not get written.
//
// # `set`, not `add`
//
// Idempotent on purpose. The thing this is for runs more than once — a
// provisioning script, a dotfiles bootstrap, a CI job — and a command that
// fails the second time is one people wrap in `|| true`, which throws away
// every refusal above along with the one they were silencing.
//
// # What each flag replaces
//
// A block is replaced by what the flags state, and a block no flag mentions
// is left exactly as it was. So `--set` states the whole `set:` block for
// that plugin (omit a key to remove it) while leaving its `secrets:` alone,
// and a run with neither touches neither. That way a script is the source of
// truth for what it states and silent about the rest, which is the only
// reading under which running it twice and running it once are the same.

// profileSetCommand implements `rta profile set`.
func profileSetCommand(reg *registry.Registry, render renderFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <profile>",
		Short: "Create or update an environment without a terminal",
		Long: "States a profile from flags, so setting one up is a line in a script rather than\n" +
			"a form or a block of hand-written YAML.\n\n" +
			"Idempotent: running it twice is running it once. Each block is replaced by what\n" +
			"the flags state and a block no flag mentions is left alone, so `--set` restates\n" +
			"one plugin's configuration without disturbing its credentials.\n\n" +
			"`--set` refuses a declared credential outright: a value belongs in the store and\n" +
			"a profile carries the reference. `--secret` is that reference — never a value.",
		Example: "  rta kv set staging-db-password --file /run/secrets/db\n" +
			"  rta profile set staging --note 'shared staging' --ttl 8h\n" +
			"  rta profile set staging --plugin pg \\\n" +
			"      --set host=db.staging.internal --set port=5432 \\\n" +
			"      --secret password=kv:staging-db-password\n" +
			"  rta use staging",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeProfiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			v, verr := runProfileSet(cmd, args[0], reg)
			return render(cmd, v, verr)
		},
	}
	cmd.Flags().String("note", "", "why this environment exists")
	cmd.Flags().String("ttl", "", "how long a switch to it lasts (`30m`, `8h`, or `none`)")
	cmd.Flags().String("plugin", "", "which plugin the connection flags below are about")
	// StringArray, never StringSlice: StringSlice splits its argument on
	// commas, so `--set search-path=public,app` would silently become two
	// malformed pairs. A configuration value is not a list of flags.
	cmd.Flags().StringArray("set", nil, "a `key=value` this plugin reads; repeat for more")
	cmd.Flags().StringArray("secret", nil,
		"an `input=kv:entry` reference — never the value itself; repeat for more")
	cmd.Flags().String("kube", "", "reach it through a port-forward: `context/namespace/kind/name:port`")
	cmd.Flags().String("ssh", "", "or through an ssh host: `[user@]host[:port]/desthost:destport`")
	cmd.Flags().Bool("direct", false, "connect directly: remove any `kube:` or `ssh:` forward")
	_ = cmd.RegisterFlagCompletionFunc("plugin", completeInstalledPlugins)
	_ = cmd.RegisterFlagCompletionFunc("ttl", completeWindow)
	_ = cmd.RegisterFlagCompletionFunc("set", completeSetKeys)
	_ = cmd.RegisterFlagCompletionFunc("secret", completeSecretInputs)
	return cmd
}

// completeSetKeys offers the config keys the plugin already on the command
// line declares, with their help text.
//
// The other half of not needing a form. A flag you have to look up is a flag
// people guess at, and a guessed key is the `hsot: typo.internal` this command
// spends a refusal on — better not to have been typed. The Options an input
// declares ride along in the description, since "one of disable|require|…" is
// the thing you would otherwise go and read.
//
// Silent whenever it cannot answer, like every completion here: one that
// refuses until the command line is valid is one nobody ever sees.
func completeSetKeys(cmd *cobra.Command, _ []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ns := config.PluginNamespace(strings.TrimSpace(mustString(cmd, "plugin")))
	if ns == "" || installed == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	// Past the `=` there is a value to suggest, not a key, and rta has no
	// business guessing at one — a host or a database name is not something
	// it knows. NoSpace so `host=` stays open for typing.
	if strings.Contains(prefix, "=") {
		return nil, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	}
	var out []cobra.Completion
	seen := map[string]bool{}
	for _, c := range installed.Capabilities() {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if f.Config == "" || !plugin.ProfileFillable(c, f) || seen[f.Config] {
				continue
			}
			seen[f.Config] = true
			desc := f.Help
			if len(f.Options) > 0 {
				desc = strings.Join(f.Options, "|")
			}
			out = append(out, cobra.CompletionWithDesc(f.Config+"=", desc))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}

// completeSecretInputs offers the inputs a `secrets:` mapping may target.
//
// Wider than the credentials, and that is deliberate: Fill gates on
// ProfileFillable, so mapping `user` onto a Secret's `username` key is an
// ordinary thing to want and it works. The credentials are marked, because
// they are the ones that have no other way in.
func completeSecretInputs(cmd *cobra.Command, _ []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ns := config.PluginNamespace(strings.TrimSpace(mustString(cmd, "plugin")))
	if ns == "" || installed == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	if strings.Contains(prefix, "=") {
		// A reference, and rta will not enumerate the store to complete one:
		// this runs on a keystroke and the entry names in an operator's store
		// are not something to unlock a store for. The scheme is the useful
		// part anyway — it is what makes the value a reference.
		return []cobra.Completion{cobra.CompletionWithDesc(prefix, "kv:<entry> or kube:<secret>/<key>")},
			cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	}
	var out []cobra.Completion
	seen := map[string]bool{}
	for _, c := range installed.Capabilities() {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if !plugin.ProfileFillable(c, f) || seen[f.Name] {
				continue
			}
			seen[f.Name] = true
			desc := f.Help
			if f.Type == plugin.Secret {
				// The same test profileSecrets applies, asked here because
				// this loop already has the declaration in hand.
				desc = "credential — " + desc
			}
			out = append(out, cobra.CompletionWithDesc(f.Name+"=", desc))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}

// profileRemoveCommand implements `rta profile rm`.
func profileRemoveCommand(reg *registry.Registry, render renderFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rm <profile>",
		Aliases: []string{"remove"},
		Short:   "Remove an environment, or one plugin from it",
		Long: "Without --plugin, removes the environment: it is switched off if it was on, and\n" +
			"every grant naming it is revoked.\n\n" +
			"That last part is not tidiness. A grant naming a profile nothing can look up\n" +
			"authorizes nothing, so leaving it behind is a row in `rta grant list` that reads\n" +
			"like access and is not — which is the one thing a record of consent must never\n" +
			"contain.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeProfiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			v, verr := runProfileRemove(cmd, args[0], reg)
			return render(cmd, v, verr)
		},
	}
	cmd.Flags().String("plugin", "", "remove only this plugin's entry, keeping the environment")
	_ = cmd.RegisterFlagCompletionFunc("plugin", completeInstalledPlugins)
	return cmd
}

func runProfileSet(cmd *cobra.Command, name string, reg *registry.Registry) (view.View, *view.Error) {
	if !config.ValidName(name) {
		return nil, view.Errorf("core.profile.name", "%q is not a valid profile name", name).
			WithHint("lowercase letters, digits and dashes, starting with a letter or digit — " +
				"the name reaches an environment variable and a grant record")
	}
	if verr := refuseUnhonouredConfig(); verr != nil {
		return nil, verr
	}
	// A profile and a plugin namespace share a command line, so a profile
	// called `pg` is one nothing can name. Check refuses it in the file; this
	// refuses it at the keystroke, which is where somebody can still choose a
	// different word.
	if installed != nil {
		if _, taken := installed.Origin(name); taken {
			return nil, view.Errorf("core.profile.namespace",
				"%q is the name of a registered plugin", name).
				WithHint("pick another — a profile name and a namespace share a command line")
		}
	}

	// Under the lock, and the whole load-mutate-write inside it. This
	// command is built to be scripted, and a script that states four
	// environments states them in parallel as readily as in sequence — eight
	// at once lost up to three of them, with all eight reporting success.
	//
	// Every refusal below stops the write by returning false rather than by
	// returning early, so a run that decides not to write still releases the
	// lock on the ordinary path.
	var (
		key      string
		receipts []view.Pair
		verr     *view.Error
		existed  bool
	)
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		if cfg.Profiles == nil {
			cfg.Profiles = map[string]config.Profile{}
		}
		_, existed = cfg.Profiles[name]
		p := cfg.Profiles[name]

		if cmd.Flags().Changed("note") {
			note, _ := cmd.Flags().GetString("note")
			p.Note = strings.TrimSpace(note)
		}
		if cmd.Flags().Changed("ttl") {
			ttl := strings.TrimSpace(mustString(cmd, "ttl"))
			if ttl == "none" {
				ttl = ""
			}
			if ttl != "" && (config.Profile{TTL: ttl}).BadTTL() {
				verr = view.Errorf("core.profile.ttl", "%q is not a duration", ttl).
					WithHint("write it as `30m`, `2h`, `12h` — or `none` for no deadline")
				return cfg, false
			}
			p.TTL = ttl
		}

		key, receipts, verr = applyConnectionFlags(cmd, name, &p, reg)
		if verr != nil {
			return cfg, false
		}
		if !existed && len(p.Plugins) == 0 {
			// A profile that configures nothing is one `rta use` refuses, so
			// creating one is creating something unusable. Updating an
			// existing empty one is left alone: the operator may be halfway
			// through.
			verr = view.Errorf("core.profile.empty",
				"a new profile has to configure at least one plugin").
				WithHint("add `--plugin <name>` and what it should connect to")
			return cfg, false
		}
		cfg.Profiles[name] = p
		return cfg, true
	}); err != nil {
		return nil, view.AsError(err, "core.profile.write")
	}
	if verr != nil {
		return nil, verr
	}

	// Read back what a later run will read, rather than reporting the value
	// this function built. The profile card is `rta profile show`'s renderer,
	// so what this prints on success and what that prints afterwards are the
	// same page — including its redaction and its problem rows.
	after, err := config.LoadFile()
	if err != nil {
		return nil, view.AsError(err, "core.profile.config")
	}
	written := after.Profiles[name]
	card := profileCard(name, written, reg)
	verb := "updated"
	if !existed {
		verb = "created"
	}
	what := verb + " " + name
	if key != "" {
		what += " — " + key
	}
	head := append([]view.Pair{{Key: "wrote", Value: what + " in " + config.Path()}}, receipts...)
	card.Pairs = append(head, card.Pairs...)
	return card, nil
}

// applyConnectionFlags folds --plugin and everything under it into p, and
// returns the plugins: key it wrote, "" when the run said nothing about a
// plugin.
func applyConnectionFlags(cmd *cobra.Command, name string, p *config.Profile,
	reg *registry.Registry,
) (key string, receipts []view.Pair, verr *view.Error) {
	stated := make([]string, 0, 5)
	for _, f := range []string{"set", "secret", "kube", "ssh", "direct"} {
		if cmd.Flags().Changed(f) {
			stated = append(stated, "--"+f)
		}
	}
	want := strings.TrimSpace(mustString(cmd, "plugin"))
	if want == "" {
		if len(stated) > 0 {
			return "", nil, view.Errorf("core.profile.noplugin",
				"nothing says which plugin %s is about",
				strings.Join(stated, ", ")).
				WithHint("add `--plugin <name>` — a profile configures each plugin separately")
		}
		return "", nil, nil
	}
	if cmd.Flags().Changed("kube") && cmd.Flags().Changed("ssh") {
		return "", nil, view.Errorf("core.profile.tunnel.twice",
			"--kube and --ssh both name a forward, and a call opens one").
			WithHint("keep whichever names where this connection really goes")
	}
	if cmd.Flags().Changed("direct") && (cmd.Flags().Changed("kube") || cmd.Flags().Changed("ssh")) {
		return "", nil, view.Errorf("core.profile.tunnel.direct",
			"--direct removes the forward that --kube or --ssh states").
			WithHint("state one or the other")
	}

	key = pinKey(want, installed)
	if p.Plugins == nil {
		p.Plugins = map[string]config.Connection{}
	}
	// Whatever is already there, so a run that states one block leaves the
	// others exactly as they were.
	conn := p.Plugins[key]

	if direct, _ := cmd.Flags().GetBool("direct"); direct {
		conn.Kube, conn.SSH = "", ""
	}
	if cmd.Flags().Changed("kube") {
		conn.Kube, conn.SSH = strings.TrimSpace(mustString(cmd, "kube")), ""
	}
	if cmd.Flags().Changed("ssh") {
		conn.SSH, conn.Kube = strings.TrimSpace(mustString(cmd, "ssh")), ""
	}

	ns := config.PluginNamespace(key)
	// What a restated block leaves behind. Stating the whole block is what
	// makes the command idempotent, and it is also how somebody changing a
	// host loses the `sslmode: require` line sitting beside it — a security
	// setting, gone, because they restated one key of four.
	//
	// Not a reason to merge instead: a merge makes a key impossible to remove
	// and stops a script being the source of truth for what it states. It is
	// a reason to say so, the same way the forward repair below does. A loss
	// an operator can see is a different thing from one they cannot.
	var dropped []string
	if cmd.Flags().Changed("set") {
		pairs, _ := cmd.Flags().GetStringArray("set")
		set, verr := parseSetFlags(pairs, ns, reg)
		if verr != nil {
			return "", nil, verr
		}
		dropped = append(dropped, missingKeys(conn.Set, set, "set.")...)
		conn.Set = set
	}
	if cmd.Flags().Changed("secret") {
		pairs, _ := cmd.Flags().GetStringArray("secret")
		secrets, verr := parseSecretFlags(pairs)
		if verr != nil {
			return "", nil, verr
		}
		was := make(map[string]any, len(conn.Secrets))
		for k := range conn.Secrets {
			was[k] = nil
		}
		now := make(map[string]any, len(secrets))
		for k := range secrets {
			now[k] = nil
		}
		dropped = append(dropped, missingKeys(was, now, "secrets.")...)
		conn.Secrets = secrets
	}
	sort.Strings(dropped)

	// A forward fills the endpoint inputs itself, laying them over whatever
	// `set:` said, so a stated host beside a coordinate is a line no run will
	// ever read — and CheckConnection refuses the pair rather than tolerate
	// it. Which leaves the question of what `--kube` alone should do to a key
	// the file already held.
	//
	// Removed, and said out loud. The alternative is refusing to add a
	// forward because of a key set in some earlier command, which is a
	// refusal about a line the operator is not looking at and cannot remove
	// with the flag they are holding — `--set` states the whole block, so
	// clearing one key means restating the others.
	//
	// **Only what was carried over.** A key this same invocation stated is
	// refused, not repaired: `--kube X --set host=y` is two contradictory
	// statements in one breath, and quietly dropping one of them would be
	// discarding a flag somebody just typed. The two cases are exactly
	// "did this run restate the block", which is what Changed answers.
	freed := freeEndpointKeys(&conn, ns, reg,
		!cmd.Flags().Changed("set"), !cmd.Flags().Changed("secret"))

	// **The same rules the report uses, before the write rather than after
	// it.** internal/profile.CheckConnection is what `rta profile list`,
	// `rta doctor` and the TUI all call; a second implementation here would
	// eventually disagree with them, and the disagreement would be a
	// connection this command called fine and every run refused.
	if problems := profile.CheckConnection(name, key, conn, installed); len(problems) > 0 {
		return "", nil, view.Errorf("core.profile.unusable", "%s", problems[0].Reason).
			WithHint(problems[0].Hint)
	}
	p.Plugins[key] = conn
	// Two different losses, so two different sentences. A key the forward
	// makes dead and a key a restatement left out are both the operator's
	// line going away, and telling them apart is the difference between "that
	// is fine" and "put it back".
	if len(dropped) > 0 {
		receipts = append(receipts, view.Pair{Key: "dropped",
			Value: strings.Join(dropped, ", ") + " — the flags state the whole block, " +
				"and these were not among them"})
	}
	if len(freed) > 0 {
		receipts = append(receipts, view.Pair{Key: "removed",
			Value: strings.Join(freed, ", ") + " — the forward fills those, so nothing read them"})
	}
	return key, receipts, nil
}

// missingKeys names the keys of before that after does not have, prefixed.
func missingKeys(before, after map[string]any, prefix string) []string {
	var out []string
	for k := range before {
		if _, kept := after[k]; !kept {
			out = append(out, prefix+k)
		}
	}
	return out
}

// freeEndpointKeys drops the keys a forward makes dead, and reports them.
//
// Scoped by the two booleans to what this run did not state: see the comment
// at the call site. Nothing happens at all when the connection opens no
// forward, which is why `--direct` needs no counterpart — clearing the
// coordinate simply makes those keys live again.
func freeEndpointKeys(conn *config.Connection, ns string, reg *registry.Registry,
	setCarried, secretsCarried bool,
) []string {
	if !conn.Tunnelled() {
		return nil
	}
	var freed []string
	for _, c := range reg.Capabilities() {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if f.Endpoint == plugin.EndpointNone || !plugin.ProfileFillable(c, f) {
				continue
			}
			// `set:` is keyed by Field.Config and `secrets:` by Field.Name.
			// Two keyspaces for the same input, which is why this walks the
			// declarations rather than either map.
			if setCarried && f.Config != "" {
				if _, stated := conn.Set[f.Config]; stated {
					delete(conn.Set, f.Config)
					freed = append(freed, "set."+f.Config)
				}
			}
			if secretsCarried {
				if _, mapped := conn.Secrets[f.Name]; mapped {
					delete(conn.Secrets, f.Name)
					freed = append(freed, "secrets."+f.Name)
				}
			}
		}
	}
	if len(conn.Set) == 0 {
		conn.Set = nil
	}
	if len(conn.Secrets) == 0 {
		conn.Secrets = nil
	}
	sort.Strings(freed)
	return freed
}

// parseSetFlags turns `--set key=value` into the `set:` block, typed against
// what the namespace declares.
//
// Two things happen here that cannot happen in a file.
//
// A key naming a declared credential is refused outright. A `plugin.Secret`
// input can never be a config key — pkg/plugin.Validate refuses that
// declaration, because a config file is plaintext read on every invocation —
// so a value under one of those names is inert wherever it lands. The file
// path finds that out when the connection fails to authenticate; here it is
// the first thing checked, and the message names the flag that does work.
//
// And a value is written as the type the field declares. YAML types a bare
// word for you and every surface downstream reads with a type assertion, so
// `port: "5432"` reaches the handler as 0 and `tls: "true"` as false — the
// failure plugin.StatedTypeProblem now reports about a hand-written file.
// A flag argument is always text, so this is the one place that decides, and
// writing the string would produce that exact bug on every run.
func parseSetFlags(pairs []string, ns string, reg *registry.Registry) (map[string]any, *view.Error) {
	byConfig, secrets := declaredFor(ns, reg)

	// Collected first, because a StringSlice input is stated by repeating the
	// key and the whole list has to be in hand before any of it is typed.
	order := make([]string, 0, len(pairs))
	raw := map[string][]string{}
	for _, pair := range pairs {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			// The argument is deliberately not echoed. Somebody who typed a
			// value where a pair belongs has already put it on a command
			// line; rta will not put it anywhere else.
			return nil, view.Errorf("core.profile.set.shape",
				"one --set argument is not a `key=value` pair").
				WithHint("write it as `--set host=db.internal`")
		}
		k = strings.TrimSpace(k)
		if k == "" {
			return nil, view.Errorf("core.profile.set.shape", "one --set argument names no key").
				WithHint("write it as `--set host=db.internal`")
		}
		if secrets[k] {
			return nil, view.Errorf("core.profile.set.secret",
				"%q is a credential, and `--set` writes values into %s in plaintext",
				k, config.Path()).
				WithHint("store it and reference it instead: `rta kv set " + ns + "-" + k +
					" --file <path>` then `--secret " + k + "=kv:" + ns + "-" + k + "`")
		}
		if v == "" {
			// Never silently dropped. `--set` states the whole block, so an
			// empty value is either a key meant to be removed — which is done
			// by omitting it — or a variable that expanded to nothing in the
			// script that called this, which is worth finding out about.
			return nil, view.Errorf("core.profile.set.empty",
				"`--set %s=` states no value", k).
				WithHint("`--set` states the whole block, so leave a key out to remove it — " +
					"and check the variable this came from expanded")
		}
		if _, seen := raw[k]; !seen {
			order = append(order, k)
		}
		raw[k] = append(raw[k], v)
	}

	set := make(map[string]any, len(order))
	for _, k := range order {
		values := raw[k]
		f, declared := byConfig[k]
		if !declared {
			// Left as text for CheckConnection to refuse in its own words,
			// which name the capability and point at `rta explain`. Guessing
			// a type for a key nothing reads would be inventing a fact about
			// a field that does not exist.
			set[k] = values[len(values)-1]
			continue
		}
		if f.Type != plugin.StringSlice && len(values) > 1 {
			return nil, view.Errorf("core.profile.set.repeated",
				"`--set %s` is given more than once, and %s takes one value", k, k).
				WithHint("repeating a key states a list, and only a list-shaped input takes one")
		}
		v, verr := typedSetValue(f, values)
		if verr != nil {
			return nil, verr
		}
		set[k] = v
	}
	if len(set) == 0 {
		return nil, nil
	}
	return set, nil
}

// typedSetValue turns the text a flag carries into the type f declares.
func typedSetValue(f plugin.Field, values []string) (any, *view.Error) {
	raw := values[len(values)-1]
	switch f.Type {
	case plugin.StringSlice:
		return append([]string{}, values...), nil
	case plugin.Int:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return nil, badSetValue(f, "a whole number", "5432")
		}
		return n, nil
	case plugin.Float:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, badSetValue(f, "a number", "1.5")
		}
		return n, nil
	case plugin.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			// `yes` and `on` land here, and that is the point: they are what
			// somebody writes when they mean true, and YAML 1.2 reads both as
			// text — so the file spelling of this mistake silently disables
			// whatever the field turns on.
			return nil, badSetValue(f, "`true` or `false`", "true")
		}
		return b, nil
	case plugin.Secret:
		// Unreachable: a Secret cannot be a config key, so it cannot be found
		// by config key here. Kept because "unreachable" is a claim about
		// another package, and the cost of being wrong is a credential in a
		// plaintext file.
		return nil, view.Errorf("core.profile.set.secret",
			"%q is a credential and cannot be stated with --set", f.Name).
			WithHint("store it and reference it with `--secret`")
	}
	return raw, nil
}

// badSetValue reports a value the declared type cannot hold, without ever
// repeating the value: this runs over something somebody typed on a command
// line, and the reason it did not parse is not a reason to print it again.
func badSetValue(f plugin.Field, want, example string) *view.Error {
	return view.Errorf("core.profile.set.type",
		"%s is declared %s, and takes %s", f.Name, f.Type, want).
		WithHint("write it as `--set " + f.Config + "=" + example + "`")
}

// parseSecretFlags turns `--secret input=ref` into the `secrets:` block.
//
// The scheme is what makes this a reference rather than a value, so a missing
// one is refused here rather than left to the loader — and refused **without
// echoing the argument**, because the single most likely thing to be sitting
// where a scheme should be is the credential itself.
func parseSecretFlags(pairs []string) (map[string]string, *view.Error) {
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		input, ref, ok := strings.Cut(pair, "=")
		input = strings.TrimSpace(input)
		if !ok || input == "" {
			return nil, view.Errorf("core.profile.secret.shape",
				"one --secret argument is not an `input=reference` pair").
				WithHint("write it as `--secret password=kv:staging-db-password`")
		}
		out[input] = strings.TrimSpace(ref)
	}
	// Asked the way the loader asks it, so the two cannot disagree about what
	// counts as a source.
	if bad := (config.Connection{Secrets: out}).BadSecretRefs(); len(bad) > 0 {
		named := make([]string, 0, len(bad))
		for _, b := range bad {
			named = append(named, strings.TrimPrefix(b, "secrets."))
		}
		return nil, view.Errorf("core.profile.secret.value",
			"--secret takes a reference to a stored credential, not the credential: %s",
			strings.Join(named, ", ")).
			WithHint("store it first — `rta kv set <entry> --file <path>` — then " +
				"`--secret <input>=kv:<entry>`. If what you passed was the credential " +
				"itself, it is in this shell's history now")
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// declaredFor is what a namespace offers a profile: its config keys, and the
// names of the credentials that must never be one.
//
// The credential half is asked exactly as `rta profile show` asks it, which
// is the list that command redacts — so what this refuses to write and what
// that refuses to print are the same set. That is why this reads the registry
// the command was built over rather than the `installed` package variable
// used for the checks below: those two are the same object on every path
// (NewRoot sets one from the other), and where they could ever differ, the
// refusal should agree with the page that displays the result.
func declaredFor(ns string, reg *registry.Registry) (map[string]plugin.Field, map[string]bool) {
	byConfig := map[string]plugin.Field{}
	for _, c := range reg.Capabilities() {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if f.Config != "" && plugin.ProfileFillable(c, f) {
				byConfig[f.Config] = f
			}
		}
	}
	secrets := map[string]bool{}
	for _, f := range profileSecrets(ns, reg) {
		secrets[f] = true
	}
	return byConfig, secrets
}

// pinKey is the plugins: key a profile entry must carry for what the operator
// named: bare for a built-in, `<namespace>@<digest>` for anything on $PATH.
//
// Resolved from the registry rather than demanded, because a digest is not
// something anybody should be asked to type and typing one wrong is exactly
// the failure the pin exists to prevent. That is not a weakening: the pin
// stops a $PATH impostor from *later* claiming a namespace the operator
// configured, and rta reading the digest of the artifact registered right now
// is the same answer the operator would have copied out of `rta plugin list`.
//
// A key the operator pinned themselves is left alone, so a deliberate pin at
// an artifact that is not the installed one still reaches checkPin and is
// still refused by name.
func pinKey(want string, inst profile.Installed) string {
	if strings.Contains(want, "@") || inst == nil {
		return want
	}
	if o, known := inst.Origin(want); known && o.External() {
		return want + "@" + o.Short()
	}
	return want
}

func runProfileRemove(cmd *cobra.Command, name string, reg *registry.Registry) (view.View, *view.Error) {
	if verr := refuseUnhonouredConfig(); verr != nil {
		return nil, verr
	}
	want := strings.TrimSpace(mustString(cmd, "plugin"))
	var (
		verr *view.Error
		key  string
	)
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		p, known := cfg.Profiles[name]
		if !known {
			verr = unknownProfile(cfg, name)
			return cfg, false
		}
		if want == "" {
			delete(cfg.Profiles, name)
			return cfg, true
		}
		key = pinKey(want, installed)
		if _, has := p.Plugins[key]; !has {
			hint := name + " configures nothing"
			if keys := p.PluginKeys(); len(keys) > 0 {
				hint = name + " configures: " + strings.Join(keys, ", ")
			}
			verr = view.Errorf("core.profile.noentry",
				"%s has no entry for %q", name, want).WithHint(hint)
			return cfg, false
		}
		delete(p.Plugins, key)
		cfg.Profiles[name] = p
		return cfg, true
	}); err != nil {
		return nil, view.AsError(err, "core.profile.write")
	}
	if verr != nil {
		return nil, verr
	}

	if want != "" {
		after, err := config.LoadFile()
		if err != nil {
			return nil, view.AsError(err, "core.profile.config")
		}
		card := profileCard(name, after.Profiles[name], reg)
		card.Pairs = append([]view.Pair{
			{Key: "removed", Value: key + " from " + name}}, card.Pairs...)
		return card, nil
	}

	pairs := []view.Pair{{Key: "removed", Value: "profile " + name + " from " + config.Path()}}
	// The switch follows, or this machine stays switched on to a name nothing
	// can look up — and because the selection also bounds agents, every agent
	// call would then be refused against it.
	if sel := profile.LoadSelection(); sel.Active == name {
		if verr := profile.SaveSelection(profile.Selection{}); verr != nil {
			return nil, verr
		}
		pairs = append(pairs, view.Pair{Key: "switched off",
			Value: "it was on — commands run against the base configuration"})
	}
	if n := grant.RevokeProfile(name, time.Now()); n > 0 {
		pairs = append(pairs, view.Pair{Key: "revoked",
			Value: plural(n, "grant", "grants") + " naming it, which would have authorized nothing"})
	}
	return view.KeyValue{Pairs: pairs}, nil
}

// refuseUnhonouredConfig stops a write that would land in a file nothing
// reads back.
//
// config.Path falls back to ./.rta.yaml when the operator has no config
// directory — ordinary under `env -i`, inside a container and in CI, which is
// precisely where a script is running. A profile written there is loaded and
// then ignored, because a working-directory config file could otherwise ship
// a profile called "prod" in a cloned repository. Succeeding silently would
// make this command's whole purpose — being scriptable — the case it gets
// wrong.
func refuseUnhonouredConfig() *view.Error {
	if config.TrustedPath() {
		return nil
	}
	return view.Errorf("core.profile.untrusted",
		"profiles in %s are not honoured, so writing one there would do nothing", config.Path()).
		WithHint("set $RTA_CONFIG to the file you mean — a config file found in the working " +
			"directory could belong to a repository you cloned, so its profiles are ignored")
}

// completeInstalledPlugins offers what a profile entry may name, pinned.
func completeInstalledPlugins(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if installed == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	seen := map[string]bool{}
	var out []cobra.Completion
	for _, c := range installed.Capabilities() {
		ns := plugin.Namespace(c.ID)
		if seen[ns] {
			continue
		}
		seen[ns] = true
		// The bare namespace, because pinKey resolves it. Offering a digest
		// to type would be offering the mistake back.
		out = append(out, cobra.Completion(ns))
	}
	sort.Strings(out)
	return out, cobra.ShellCompDirectiveNoFileComp
}

func mustString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}
