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
// `output`, `theme.primary`, `dashboard.width`, `plugins.http.timeout`.
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
	// the text colours of the theme: everything else, the status colours
	// included (theme.Resolve), is ignored from a file nobody named, so a write
	// there would change nothing.
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
	keys := make([]configKey, 0, 5+len(slots))
	keys = append(keys, outputKey(), widthKey(), heightKey(), refreshKey(),
		capabilityListKey(reg, "dashboard.hidden", "tiles to leave off the screen",
			"nothing is hidden",
			func(c *config.Config) *[]string { return &c.Dashboard.Hidden }),
		capabilityListKey(reg, "dashboard.order", "tiles to put first, in this order",
			"the automatic order",
			func(c *config.Config) *[]string { return &c.Dashboard.Order }))
	for _, slot := range slots {
		keys = append(keys, themeKey(slot))
	}
	return append(keys, themeStatusKey())
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

// gridKey is dashboard.width and dashboard.height: one declaration of a whole
// number in a range, since the two are the same kind of key and read the same.
func gridKey(name, help, example, def string, lo, hi int, field func(*config.Config) *int) configKey {
	return configKey{
		Name: name, Help: help, Example: example, Default: def,
		parse: func(values []string) (any, *view.Error) {
			if verr := oneValue(name, example, values); verr != nil {
				return nil, verr
			}
			n, err := strconv.Atoi(values[0])
			if err != nil || (n != 0 && (n < lo || n > hi)) {
				return nil, view.Errorf("core.config.set.type",
					"%s takes a whole number from %d to %d, or 0", name, lo, hi).
					WithHint("0 leaves it to each tile: write it as `rta config set " + name + " " + example + "`")
			}
			return n, nil
		},
		read:  func(c config.Config) (any, bool) { n := *field(&c); return n, n > 0 },
		write: func(c *config.Config, v any) { *field(c) = v.(int) },
		drop: func(c *config.Config) bool {
			p := field(c)
			had := *p != 0
			*p = 0
			return had
		},
	}
}

func widthKey() configKey {
	return gridKey("dashboard.width", "how wide a tile is, in twelfths of the screen (6 is half, 4 a third)", "4",
		"each tile's own, from the terminal's width", 1, config.GridTracks,
		func(c *config.Config) *int { return &c.Dashboard.Width })
}

func heightKey() configKey {
	return gridKey("dashboard.height", "how tall a tile is, in lines of its box", "14",
		"what each tile has to say, up to eleven lines", config.MinHeight, config.MaxHeight,
		func(c *config.Config) *int { return &c.Dashboard.Height })
}

func refreshKey() configKey {
	return configKey{
		Name: "dashboard.refresh", Help: "how often a tile is re-run, unless its section or it says otherwise", Example: "30s",
		Default: "each capability's own: 30s for one that reaches a connection, 5s for the rest",
		parse: func(values []string) (any, *view.Error) {
			if verr := oneValue("dashboard.refresh", "30s", values); verr != nil {
				return nil, verr
			}
			d, err := config.ParseRefresh(values[0])
			if err != nil {
				return nil, view.Errorf("core.config.set.type", "dashboard.refresh %s", err.Error()).
					WithHint("write it as `rta config set dashboard.refresh 30s`")
			}
			return config.FormatRefresh(d), nil
		},
		read:  func(c config.Config) (any, bool) { return c.Dashboard.Refresh, c.Dashboard.Refresh != "" },
		write: func(c *config.Config, v any) { c.Dashboard.Refresh = v.(string) },
		drop: func(c *config.Config) bool {
			had := c.Dashboard.Refresh != ""
			c.Dashboard.Refresh = ""
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
	read, write, drop := themeEntry(slot)
	return configKey{
		Name: "theme." + slot, Help: "the " + slot + " colour of the palette", Example: "#D97757",
		Default: "the built-in colour", Honoured: !theme.IsStatus(slot),
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
		read: read, write: write, drop: drop,
	}
}

// themeStatusKey is theme.status, the entry of the palette that is a choice among
// three words and not a colour. Not honoured from a file nobody named, for the
// reason the status colours are not (theme.Resolve): a repository's own config does
// not get to make a failure read as plain text.
func themeStatusKey() configKey {
	const name = "theme." + theme.GradesKey
	read, write, drop := themeEntry(theme.GradesKey)
	return configKey{
		Name: name, Help: "how much of the colour that grades is drawn: all, only the alerts, or none",
		Default: theme.GradesAll.String(), Example: "alerts", Options: theme.GradeModes(),
		parse: func(values []string) (any, *view.Error) {
			if verr := oneValue(name, "alerts", values); verr != nil {
				return nil, verr
			}
			if _, ok := theme.ParseGrades(values[0]); !ok {
				return nil, view.Errorf("core.config.set.option", "%s takes one of %s",
					name, strings.Join(theme.GradeModes(), ", ")).
					WithHint("all draws every status colour, alerts only warn and bad, off none: " +
						"`rta config set " + name + " alerts`")
			}
			return values[0], nil
		},
		read: read, write: write, drop: drop,
	}
}

// themeEntry is how a key of the `theme:` block is read, written and dropped,
// which is the same for a colour and for the one entry that is not: the block is a
// map of strings, and what a value must be is each key's parse.
func themeEntry(slot string) (
	read func(config.Config) (any, bool),
	write func(*config.Config, any),
	drop func(*config.Config) bool,
) {
	read = func(c config.Config) (any, bool) {
		v, ok := c.Theme[slot]
		return v, ok
	}
	write = func(c *config.Config, v any) {
		if c.Theme == nil {
			c.Theme = map[string]string{}
		}
		c.Theme[slot] = v.(string)
	}
	drop = func(c *config.Config) bool {
		_, had := c.Theme[slot]
		delete(c.Theme, slot)
		if len(c.Theme) == 0 {
			c.Theme = nil
		}
		return had
	}
	return read, write, drop
}
