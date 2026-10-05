package app

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// configSchemaCommand implements `rta config schema`.
func configSchemaCommand(reg *registry.Registry) *cobra.Command {
	return &cobra.Command{
		Use:         "schema",
		Annotations: outputExempt(),
		Short:       "Print the config file's JSON Schema, for editor completion",
		Long: "Prints a JSON Schema describing every key the config file may carry, with the\n" +
			"explanation an editor shows on hover, and each installed plugin's own keys under\n" +
			"`plugins:` with their types, bounds and defaults. `rta config edit` keeps a copy\n" +
			"beside the file, named " + config.SchemaFile + ", which the file's first lines point at.\n" +
			"To keep one yourself:\n\n" +
			"  rta config schema > " + config.SchemaFile + "\n\n" +
			"and put this modeline at the top of the config file:\n\n" +
			"  # yaml-language-server: $schema=" + config.SchemaFile + "\n\n" +
			"VS Code's YAML extension (redhat.vscode-yaml) and every other editor speaking\n" +
			"yaml-language-server read that line and complete, validate and explain each\n" +
			"field in place. A plugin that is not installed on this machine is not in the\n" +
			"schema, so its section is accepted as it stands; `rta config check` and\n" +
			"`rta doctor` remain the deep validators.",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Raw JSON straight to stdout, deliberately outside the render
			// pipeline: the output already is a machine format, and wrapping
			// it in an --output envelope would break the one thing the
			// command exists for — redirecting into a file an editor reads.
			out, err := json.MarshalIndent(configSchemaFor(reg), "", "  ")
			if err != nil {
				return err
			}
			out = append(out, '\n')
			_, err = cmd.OutOrStdout().Write(out)
			return err
		},
	}
}

// configSchemaFor is the file's schema with each registered plugin's keys in
// it, from the same declarations `rta explain`, `rta doctor` and `rta config
// set` read, so what an editor completes is what the host would accept.
//
// A plugin that is not registered is not described, and its section stays an
// open object: the schema states what this machine knows, and a file shared
// with one that has more should not be flagged for it.
func configSchemaFor(reg *registry.Registry) map[string]any {
	s := config.Schema()
	patterns := map[string]any{}
	for _, p := range reg.Plugins() {
		if section := pluginSchema(reg, p); section != nil {
			patterns["^"+regexp.QuoteMeta(p.Name)+"(@[0-9a-f]+)?$"] = section
		}
	}
	if len(patterns) > 0 {
		plugins := s["properties"].(map[string]any)["plugins"].(map[string]any)
		plugins["patternProperties"] = patterns
	}
	return s
}

func pluginSchema(reg *registry.Registry, p plugin.Plugin) map[string]any {
	readers := pluginconf.Readers(reg, p.Name)
	if len(readers) == 0 {
		return nil
	}
	keys := make([]string, 0, len(readers))
	for k := range readers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	root := schemaBlock(p.Summary)
	for _, key := range keys {
		segs := strings.Split(key, ".")
		cur := root
		for _, seg := range segs[:len(segs)-1] {
			props := cur["properties"].(map[string]any)
			next, ok := props[seg].(map[string]any)
			if !ok {
				next = schemaBlock("")
				props[seg] = next
			}
			cur = next
		}
		cur["properties"].(map[string]any)[segs[len(segs)-1]] = keySchema(reg, p.Name, key, readers[key])
	}
	return root
}

func schemaBlock(description string) map[string]any {
	block := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{},
	}
	if description != "" {
		block["description"] = description
	}
	return block
}

// keySchema is one config key as the value a section may state for it: the type
// the declaration gives it, the widest range any reader of it takes, and its
// default. Options are offered as examples and not as an enum, because a run
// reads an option in any case and a schema that flagged `BASE32` would warn on
// a value every call accepts.
func keySchema(reg *registry.Registry, ns, key string, readers []plugin.Field) map[string]any {
	f := pluginconf.SharedField(readers)
	out := map[string]any{}
	switch f.Type {
	case plugin.Int:
		out["type"] = "integer"
	case plugin.Float:
		out["type"] = "number"
	case plugin.Bool:
		out["type"] = "boolean"
	case plugin.StringSlice:
		out["type"] = "array"
		out["items"] = map[string]any{"type": "string"}
	default:
		out["type"] = "string"
	}
	if f.Min != nil {
		out["minimum"] = f.Min
	}
	if f.Max != nil {
		out["maximum"] = f.Max
	}
	if f.Default != nil {
		out["default"] = f.Default
	}
	description := f.Help
	if len(f.Options) > 0 {
		out["examples"] = f.Options
		description += " — one of " + strings.Join(f.Options, ", ")
	}
	var reads []string
	for _, c := range reg.Capabilities() {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, in := range c.Inputs {
			if in.Config == key {
				reads = append(reads, c.ID)
				break
			}
		}
	}
	if len(reads) > 0 {
		description += " (read by " + strings.Join(reads, ", ") + ")"
	}
	out["description"] = strings.TrimSpace(description)
	return out
}
