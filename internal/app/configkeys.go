package app

import (
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/near"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A config key is one setting of the file that `rta config get`, `set` and
// `unset` address by the dotted name `rta explain` prints it under:
// `output`, `theme.primary`, `dashboard.columns`, `plugins.http.timeout`.
//
// Each carries its own parse, read and write, because what a key accepts is
// decided where the thing it configures is: an output format by the renderer, a
// colour by the palette, a plugin's key by that plugin's declaration. The file
// stays a typed Config and every change goes through config.Mutate, so a key
// written here is a key the loader reads back and a comment beside it survives.
type configKey struct {
	Name string
	Help string
	// Default is what the key does while the file states nothing, in words.
	Default string
	Example string
	Options []string
	// Honoured is true for a key that counts when it is read from the
	// working-directory ./.rta.yaml, which is only ever the output format and
	// the theme: everything else is ignored from a file nobody named, so a
	// write there would change nothing.
	Honoured bool

	parse func(values []string) (any, *view.Error)
	read  func(config.Config) (any, bool)
	write func(*config.Config, any)
	drop  func(*config.Config) bool
}

// settingKeys are the keys of the file that belong to rta itself, in the order
// the file declares them.
func settingKeys(reg *registry.Registry) []configKey {
	slots := theme.Fields()
	keys := make([]configKey, 0, 4+len(slots))
	keys = append(keys, outputKey(), columnsKey(),
		capabilityListKey(reg, "dashboard.hidden", "tiles to leave off the screen",
			"nothing is hidden",
			func(c *config.Config) *[]string { return &c.Dashboard.Hidden }),
		capabilityListKey(reg, "dashboard.order", "tiles to put first, in this order",
			"the automatic order",
			func(c *config.Config) *[]string { return &c.Dashboard.Order }))
	for _, slot := range slots {
		keys = append(keys, themeKey(slot))
	}
	return keys
}

func oneValue(name, example string, values []string) *view.Error {
	if len(values) == 1 {
		return nil
	}
	return view.Errorf("core.config.set.arity", "%s takes one value, and %d were given", name, len(values)).
		WithHint("quote a value that holds spaces: `rta config set " + name + " " + example + "`")
}

func outputKey() configKey {
	formats := cli.Formats()
	names := make([]string, 0, len(formats))
	for _, f := range formats {
		name, _, _ := strings.Cut(f, "\t")
		names = append(names, name)
	}
	return configKey{
		Name: "output", Help: "the default --output format", Default: "pretty", Example: "json",
		Options: names, Honoured: true,
		parse: func(values []string) (any, *view.Error) {
			if verr := oneValue("output", "json", values); verr != nil {
				return nil, verr
			}
			f, err := cli.ParseFormat(values[0])
			if err != nil || values[0] == "" {
				return nil, view.Errorf("core.config.set.option", "output takes one of %s", formatNames()).
					WithHint("write it as `rta config set output json`")
			}
			// The default is no key at all: writing it would pin a value that
			// is only ever the default, the same way `rta init` leaves it out.
			if f == cli.Pretty {
				return "", nil
			}
			return string(f), nil
		},
		read: func(c config.Config) (any, bool) { return c.Output, c.Output != "" },
		write: func(c *config.Config, v any) {
			c.Output = v.(string)
		},
		drop: func(c *config.Config) bool {
			had := c.Output != ""
			c.Output = ""
			return had
		},
	}
}

func columnsKey() configKey {
	return configKey{
		Name: "dashboard.columns", Help: "the dashboard's grid width", Example: "3",
		Default: "automatic, from the terminal's width",
		parse: func(values []string) (any, *view.Error) {
			if verr := oneValue("dashboard.columns", "3", values); verr != nil {
				return nil, verr
			}
			n, err := strconv.Atoi(values[0])
			if err != nil || n < 0 {
				return nil, view.Errorf("core.config.set.type",
					"dashboard.columns is declared int, and takes a whole number from 0").
					WithHint("0 is automatic: write it as `rta config set dashboard.columns 3`")
			}
			return n, nil
		},
		read:  func(c config.Config) (any, bool) { return c.Dashboard.Columns, c.Dashboard.Columns > 0 },
		write: func(c *config.Config, v any) { c.Dashboard.Columns = v.(int) },
		drop: func(c *config.Config) bool {
			had := c.Dashboard.Columns != 0
			c.Dashboard.Columns = 0
			return had
		},
	}
}

// capabilityListKey is a dashboard key that names tiles: each value is a
// capability, or capability@profile for one pinned tile, held to the catalogue
// so a typo is refused here rather than hiding or placing nothing.
func capabilityListKey(reg *registry.Registry, name, help, def string, field func(*config.Config) *[]string) configKey {
	return configKey{
		Name: name, Help: help, Default: def, Example: "gen.overview",
		parse: func(values []string) (any, *view.Error) {
			out := make([]string, 0, len(values))
			for _, v := range values {
				id, _, _ := strings.Cut(v, "@")
				if _, ok := reg.Capability(id); ok {
					out = append(out, v)
					continue
				}
				verr := view.Errorf("core.config.set.capability", "no capability named %q", id)
				all := reg.Capabilities()
				ids := make([]string, 0, len(all))
				for _, c := range all {
					ids = append(ids, c.ID)
				}
				if guess := near.Word(id, ids); guess != "" {
					return nil, verr.WithHint("did you mean `" + guess + "`?")
				}
				return nil, verr.WithHint("`rta explain` lists every one")
			}
			return out, nil
		},
		read: func(c config.Config) (any, bool) {
			list := *field(&c)
			return list, len(list) > 0
		},
		write: func(c *config.Config, v any) { *field(c) = v.([]string) },
		drop: func(c *config.Config) bool {
			list := field(c)
			had := len(*list) > 0
			*list = nil
			return had
		},
	}
}

func themeKey(slot string) configKey {
	return configKey{
		Name: "theme." + slot, Help: "the " + slot + " colour of the palette", Example: "#D97757",
		Default: "the built-in colour", Honoured: true,
		parse: func(values []string) (any, *view.Error) {
			if verr := oneValue("theme."+slot, "'#D97757'", values); verr != nil {
				return nil, verr
			}
			if !theme.HexColor.MatchString(values[0]) {
				return nil, view.Errorf("core.config.set.color", "theme.%s takes a colour as #rrggbb", slot).
					WithHint("quote it, since # starts a comment in a shell: `rta config set theme." + slot + " '#D97757'`")
			}
			return values[0], nil
		},
		read: func(c config.Config) (any, bool) {
			v, ok := c.Theme[slot]
			return v, ok
		},
		write: func(c *config.Config, v any) {
			if c.Theme == nil {
				c.Theme = map[string]string{}
			}
			c.Theme[slot] = v.(string)
		},
		drop: func(c *config.Config) bool {
			_, had := c.Theme[slot]
			delete(c.Theme, slot)
			if len(c.Theme) == 0 {
				c.Theme = nil
			}
			return had
		},
	}
}
