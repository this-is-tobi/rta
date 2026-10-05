package app

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

// runConfigShow is bare `rta config`: the file, and what it states.
//
// Read from the file and from the environment separately, since the answer to
// "why is it json" is one or the other: RTA_OUTPUT outranks the file, and a
// reader who sees only the effective value goes looking in the wrong place.
func runConfigShow(reg *registry.Registry) (view.View, *view.Error) {
	file, err := config.LoadFile()
	if err != nil {
		return nil, view.AsError(err, "core.config.read")
	}
	text, err := config.ReadText()
	if err != nil {
		return nil, view.AsError(err, "core.config.read")
	}
	ignored, _ := config.Check()

	state := "no — every key is at its default; `rta config set <key> <value>` or `rta config edit` writes it"
	if text != nil {
		state = "yes, " + format.Count(strings.Count(string(text), "\n"), "line", "lines")
	}
	pairs := []view.Pair{
		{Key: "file", Value: config.Path()},
		{Key: "exists", Value: state},
		{Key: "from", Value: configSource()},
	}
	if len(ignored) > 0 {
		pairs = append(pairs, view.Pair{Key: "ignored",
			Value: format.Count(len(ignored), "key rta does not read", "keys rta does not read") +
				" — `rta config check` names them"})
	}
	return view.Sections{Items: []view.Section{
		{ID: "file", Title: "File", View: view.KeyValue{Pairs: pairs}},
		{ID: "settings", Title: "Settings", View: configTable(reg, file)},
	}}, nil
}

// configSource says where the path came from, so a file that is not where
// somebody expected is explained rather than only located.
func configSource() string {
	switch {
	case os.Getenv("RTA_CONFIG") != "":
		return "$RTA_CONFIG"
	case paths.OwnConfigDir() == "":
		return "the working directory, because there is no config directory — its plugins, " +
			"profiles and dashboard are not honoured"
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && paths.OwnConfigDir() == x+"/rta" {
		return "$XDG_CONFIG_HOME/rta"
	}
	return "the default, ~/.config/rta"
}

// configTable is every key the file states, each with where its value comes
// from: the file, or the environment where that outranks it.
func configTable(reg *registry.Registry, file config.Config) view.Table {
	t := view.Table{Columns: []view.Column{{Name: "Key"}, {Name: "Value"}, {Name: "Source"}}}
	add := func(key, value, source string) { t.Rows = append(t.Rows, []string{key, value, source}) }

	if env := os.Getenv("RTA_OUTPUT"); env != "" {
		source := "RTA_OUTPUT"
		if file.Output != "" && file.Output != env {
			source += " (the file says " + file.Output + ")"
		}
		add("output", env, source)
	}
	for _, k := range settingKeys(reg) {
		if k.Name == "output" && os.Getenv("RTA_OUTPUT") != "" {
			continue
		}
		if v, ok := k.read(file); ok {
			add(k.Name, strings.Join(configValueLines(v), ", "), "file")
		}
	}
	if n := len(file.Dashboard.Tiles); n > 0 {
		add("dashboard.tiles", format.Count(n, "tile", "tiles")+" — `rta dashboard list`", "file")
	}
	if n := len(file.Dashboard.Add); n > 0 {
		add("dashboard.add", format.Count(n, "tile", "tiles")+" — `rta dashboard list`", "file")
	}
	headings := make([]string, 0, len(file.Plugins))
	for h := range file.Plugins {
		headings = append(headings, h)
	}
	sort.Strings(headings)
	for _, h := range headings {
		ns, _, _ := strings.Cut(h, "@")
		_, registered := reg.Origin(ns)
		readers := pluginconf.Readers(reg, ns)
		credentials := map[string]bool{}
		for _, name := range profileSecrets(ns, reg) {
			credentials[name] = true
		}
		for _, leaf := range config.SectionLeaves(file.Plugins[h]) {
			value := strings.Join(configValueLines(leaf.Value), ", ")
			source := "file"
			switch {
			case credentials[leaf.Key] || credentials[leaf.Key[strings.LastIndex(leaf.Key, ".")+1:]]:
				value = "(redacted — a credential does not belong in this file)"
				source = "file (nothing reads it)"
			case !registered:
				source = "file (no plugin named " + ns + " is registered)"
			case len(readers[leaf.Key]) == 0:
				source = "file (nothing in " + ns + " reads it)"
			}
			add("plugins."+h+"."+leaf.Key, value, source)
		}
	}
	if names := file.ProfileNames(); len(names) > 0 {
		add("profiles", strings.Join(names, ", ")+" — `rta profile list`", "file")
	}
	if len(file.Roles) > 0 {
		roles := make([]string, 0, len(file.Roles))
		for name := range file.Roles {
			roles = append(roles, name)
		}
		sort.Strings(roles)
		add("roles", strings.Join(roles, ", ")+" — `rta grant roles`", "file")
	}
	t.Total = len(t.Rows)
	t.Empty = "Nothing is set, so every key is at its default. `rta config set <key> <value>` states one."
	return t
}

// configValueLines is a value as the lines it prints on: one for a scalar, one
// per element for a list.
func configValueLines(v any) []string {
	switch list := v.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, len(list))
		for i, e := range list {
			out[i] = fmt.Sprint(e)
		}
		return out
	}
	return []string{fmt.Sprint(v)}
}

// runConfigGet is `rta config get`: the value alone, so it can be captured.
// A key the file does not state is an error that says what it is instead,
// since an empty line is an answer a script would take for one.
func runConfigGet(reg *registry.Registry, raw string) (view.View, *view.Error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, view.AsError(err, "core.config.read")
	}
	key, verr := resolveConfigKey(reg, raw)
	name := raw
	if verr == nil {
		name = key.Name
		if v, ok := key.read(cfg); ok {
			return view.Text{Body: strings.Join(configValueLines(v), "\n")}, nil
		}
	}
	// What the file holds that no key stands for — a profile, a role, the
	// tiles, a whole block — is read off the file as it is, and printed as the
	// YAML that states it.
	if sub, ok := configBlock(cfg, name); ok {
		out, merr := yaml.Marshal(sub)
		if merr != nil {
			return nil, view.Errorf("core.config.read", "encoding %s: %v", name, merr)
		}
		return view.Text{Body: strings.TrimRight(string(out), "\n")}, nil
	}
	if verr != nil {
		return nil, verr
	}
	hint := "`rta config set " + key.Name + " " + exampleArg(key.Example) + "` states it"
	if key.Default != "" {
		hint = "it is " + key.Default + " until then — " + hint
	}
	return nil, view.Errorf("core.config.unset", "%s is not set", key.Name).WithHint(hint)
}

// configBlock is the part of the file at a dotted name, as the tree the file
// parses to. A name that runs through a list is not one: tiles are addressed
// by `rta dashboard`, not by position.
func configBlock(cfg config.Config, name string) (any, bool) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, false
	}
	var tree any
	if yaml.Unmarshal(data, &tree) != nil {
		return nil, false
	}
	for _, seg := range strings.Split(name, ".") {
		m, ok := tree.(map[string]any)
		if !ok {
			return nil, false
		}
		if tree, ok = m[seg]; !ok {
			return nil, false
		}
	}
	return tree, true
}
