package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/near"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/shellquote"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// resolveConfigKey is the key a name from the command line stands for.
//
// The names are the ones `rta explain` prints a plugin's key under, so a line
// copied off a card works as typed: `plugins.http.timeout`, and for a plugin
// found on $PATH the pinned `plugins.pg@1a2b3c4d.host` the card spells. A bare
// namespace is accepted for those too, since the pin is something rta can look
// up and nobody should have to type a digest.
func resolveConfigKey(reg *registry.Registry, raw string) (configKey, *view.Error) {
	raw = strings.TrimSpace(raw)
	for _, k := range settingKeys(reg) {
		if k.Name == raw {
			return k, nil
		}
	}
	if raw == "theme" {
		return blockKey("theme", "the palette overrides", func(c *config.Config) bool {
			had := len(c.Theme) > 0
			c.Theme = nil
			return had
		}), nil
	}
	if rest, ok := strings.CutPrefix(raw, "plugins."); ok {
		return resolvePluginKey(reg, rest)
	}
	if raw == "plugins" {
		return blockKey("plugins", "every plugin's settings", func(c *config.Config) bool {
			had := len(c.Plugins) > 0
			c.Plugins = nil
			return had
		}), nil
	}
	return configKey{}, unknownConfigKey(reg, raw)
}

// blockKey is a whole block the file holds, which can be taken out and not
// stated in one value.
func blockKey(name, help string, drop func(*config.Config) bool) configKey {
	return configKey{
		Name: name, Help: help, Honoured: name == "theme",
		parse: func([]string) (any, *view.Error) {
			return nil, view.Errorf("core.config.set.block", "%s is a block of keys, not one value", name).
				WithHint("name one key in it, or `rta config edit` opens the file")
		},
		read:  func(config.Config) (any, bool) { return nil, false },
		write: func(*config.Config, any) {},
		drop:  drop,
	}
}

func resolvePluginKey(reg *registry.Registry, rest string) (configKey, *view.Error) {
	section, key, _ := strings.Cut(rest, ".")
	if section == "" {
		return configKey{}, view.Errorf("core.config.key.shape", "plugins. names no plugin").
			WithHint("write it as `plugins.<plugin>.<key>`, for example `plugins.http.timeout`")
	}
	ns, pin, pinned := strings.Cut(section, "@")
	origin, known := reg.Origin(ns)
	if !known {
		return configKey{}, unknownPlugin(reg, ns)
	}
	heading := ns
	switch {
	case origin.External():
		heading = ns + "@" + origin.Short()
		if pinned && (len(pin) < 8 || !strings.HasPrefix(origin.Digest, pin)) {
			return configKey{}, view.Errorf("core.config.key.pin",
				"this pin does not match the installed %q", ns).
				WithHint("the installed one is `" + heading + "` — or leave the pin off, and rta writes it")
		}
	case pinned:
		return configKey{}, view.Errorf("core.config.key.pin", "%q is built in and has no artifact to pin", ns).
			WithHint("write it as `plugins." + ns + "." + key + "`")
	}
	if key == "" {
		return blockKey("plugins."+heading, ns+"'s settings", func(c *config.Config) bool {
			_, had := c.Plugins[heading]
			delete(c.Plugins, heading)
			if len(c.Plugins) == 0 {
				c.Plugins = nil
			}
			return had
		}), nil
	}

	readers := pluginconf.Readers(reg, ns)
	cfgKey, ok := declaredConfigKey(reg, ns, key, readers)
	if !ok {
		for _, name := range profileSecrets(ns, reg) {
			if name == key || strings.HasSuffix(key, "."+name) {
				return configKey{}, view.Errorf("core.config.key.secret",
					"%s is a credential, and a config file is plaintext that is read on every call", name).
					WithHint("store it with `rta kv set " + ns + "-" + name + " --file <path>` and map it with " +
						"`rta profile set <name> --plugin " + ns + " --secret " + name + "=kv:" + ns + "-" + name + "`")
			}
		}
		return configKey{}, unknownPluginKey(reg, ns, key, readers)
	}
	return pluginKey(reg, ns, heading, cfgKey, pluginconf.SharedField(readers[cfgKey])), nil
}

// declaredConfigKey is the config key a name stands for in a namespace. Two
// spellings both work: the key as the card prints it, and the capability and
// input it belongs to — `plugins.gen.password.symbols` is the input `symbols`
// of `gen.password`, which reads the key `password.symbols`. The capability
// reading comes first, because it names a declaration and the key is only what
// that declaration says it is called.
func declaredConfigKey(reg *registry.Registry, ns, key string, readers map[string][]plugin.Field) (string, bool) {
	if at := strings.LastIndex(key, "."); at > 0 {
		if c, ok := reg.Capability(ns + "." + key[:at]); ok {
			for _, f := range c.Inputs {
				if f.Name == key[at+1:] && f.Config != "" {
					return f.Config, true
				}
			}
		}
	}
	if _, ok := readers[key]; ok {
		return key, true
	}
	return "", false
}

func pluginKey(reg *registry.Registry, ns, heading, cfgKey string, f plugin.Field) configKey {
	name := "plugins." + heading + "." + cfgKey
	example := pluginKeyExample(f)
	def := ""
	if f.Default != nil {
		def = plugin.NumberText(f.Default)
		if _, isString := f.Default.(string); isString {
			def = fmt.Sprint(f.Default)
		}
	}
	options := f.Options
	if f.Type == plugin.Bool {
		options = []string{"true", "false"}
	}
	return configKey{
		Name: name, Help: f.Help, Default: def, Example: example, Options: options,
		parse: func(values []string) (any, *view.Error) {
			if !f.Type.Repeatable() {
				if verr := oneValue(name, example, values); verr != nil {
					return nil, verr
				}
			}
			v, verr := typedSetValue(f, name, values)
			if verr == nil {
				v, verr = heldToDeclaration(f, v)
			}
			if verr != nil {
				// Worded for a profile's `--set`, whose refusals these are: the
				// code and the line that shows how to write it again are this
				// command's.
				out := *verr
				out.Code = strings.Replace(out.Code, "core.profile.set.", "core.config.set.", 1)
				out.Hint = strings.NewReplacer("--set "+name+"=", "rta config set "+name+" ").Replace(out.Hint)
				return nil, &out
			}
			return v, nil
		},
		read: func(c config.Config) (any, bool) { return config.SectionValue(c.Plugins[heading], cfgKey) },
		write: func(c *config.Config, v any) {
			if c.Plugins == nil {
				c.Plugins = map[string]map[string]any{}
			}
			if c.Plugins[heading] == nil {
				c.Plugins[heading] = map[string]any{}
			}
			config.SetSectionValue(c.Plugins[heading], cfgKey, v)
		},
		drop: func(c *config.Config) bool {
			section := c.Plugins[heading]
			if !config.DeleteSectionValue(section, cfgKey) {
				return false
			}
			if len(section) == 0 {
				delete(c.Plugins, heading)
			}
			if len(c.Plugins) == 0 {
				c.Plugins = nil
			}
			return true
		},
	}
}

// pluginKeyExample is a value a hint can show for the key, one the key takes.
func pluginKeyExample(f plugin.Field) string {
	switch {
	case len(f.Options) > 0:
		return f.Options[0]
	case f.Type == plugin.Bool:
		return "true"
	case f.Type == plugin.Int:
		return setExample(f, "1")
	case f.Type == plugin.Float:
		return setExample(f, "1.5")
	case f.Default != nil:
		return fmt.Sprint(f.Default)
	}
	return "<value>"
}

func unknownPlugin(reg *registry.Registry, ns string) *view.Error {
	verr := view.Errorf("core.config.key.plugin", "no plugin named %q is registered", ns)
	plugins := reg.Plugins()
	names := make([]string, 0, len(plugins))
	for _, p := range plugins {
		names = append(names, p.Name)
	}
	if guess := near.Word(ns, names); guess != "" {
		return verr.WithHint("did you mean `" + guess + "`?")
	}
	return verr.WithHint("`rta plugin list` shows what is installed")
}

func unknownPluginKey(reg *registry.Registry, ns, key string, readers map[string][]plugin.Field) *view.Error {
	verr := view.Errorf("core.config.key.unknown", "nothing in %q reads %q", ns, key)
	keys := make([]string, 0, len(readers))
	for k := range readers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return verr.WithHint(ns + " reads no config keys at all")
	}
	if guess := near.Word(key, keys); guess != "" {
		return verr.WithHint("did you mean `plugins." + ns + "." + guess + "`?")
	}
	for _, c := range reg.Capabilities() {
		if plugin.Namespace(c.ID) == ns {
			return verr.WithHint("`rta explain " + c.ID + "` lists the keys " + ns + " reads — " +
				strings.Join(keys, ", "))
		}
	}
	return verr.WithHint("it reads " + strings.Join(keys, ", "))
}

// unknownConfigKey answers a name that is no key. Three blocks of the file are
// written by a command of their own, or by hand, and are named as that rather
// than guessed at; anything else gets the nearest key.
func unknownConfigKey(reg *registry.Registry, raw string) *view.Error {
	head, rest, _ := strings.Cut(raw, ".")
	switch {
	case head == "profiles":
		return view.Errorf("core.config.key.managed", "profiles are written by `rta profile set`, not by key").
			WithHint("`rta profile list` shows them and `rta profile show <name>` one")
	case head == "dashboard" && (rest == "tiles" || rest == "add" || strings.HasPrefix(rest, "tiles.") ||
		strings.HasPrefix(rest, "add.")):
		return view.Errorf("core.config.key.managed", "dashboard tiles are written by `rta dashboard add`, not by key").
			WithHint("`rta dashboard list` shows them and `rta dashboard rm` takes one down")
	case head == "roles":
		return view.Errorf("core.config.key.managed", "roles are written in the file, not by key").
			WithHint("`rta config edit` opens it, and `rta grant roles` lists them")
	}
	verr := view.Errorf("core.config.key.unknown", "no config key named %q", raw)
	names := configKeyNames(reg)
	if !strings.Contains(raw, ".") {
		for _, n := range names {
			if strings.HasSuffix(n, "."+raw) && !strings.HasPrefix(n, "plugins.") {
				return verr.WithHint("did you mean `" + n + "`?")
			}
		}
	}
	if guess := near.Word(raw, names); guess != "" {
		return verr.WithHint("did you mean `" + guess + "`?")
	}
	return verr.WithHint("`rta config set --help` lists the keys, and `rta explain <capability>` the ones a plugin reads")
}

// configKeyNames is every key `config set` takes, for suggestions and for
// completion: the file's own, then each plugin's, in the card's own spelling.
func configKeyNames(reg *registry.Registry) []string {
	var names []string
	for _, k := range settingKeys(reg) {
		names = append(names, k.Name)
	}
	for _, p := range reg.Plugins() {
		readers := pluginconf.Readers(reg, p.Name)
		keys := make([]string, 0, len(readers))
		for k := range readers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			names = append(names, "plugins."+p.Name+"."+k)
		}
	}
	return names
}

// configSetLine is the command that states a key, as a card ends its list of
// them: the key spelled as the card spells it, and a value it takes. "" for a
// name that resolves to nothing, so a card never ends in a line that fails.
func configSetLine(reg *registry.Registry, name string) string {
	key, verr := resolveConfigKey(reg, name)
	if verr != nil || key.parse == nil {
		return ""
	}
	return "rta config set " + key.Name + " " + exampleArg(key.Example)
}

// exampleArg is an example value as one shell word, or a placeholder as it is.
func exampleArg(example string) string {
	if strings.HasPrefix(example, "<") {
		return example
	}
	return shellquote.Arg(example)
}
