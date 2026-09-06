package config

// The config file's JSON Schema, served by `rta config schema` so an editor
// can complete and explain the file instead of the operator round-tripping
// through `rta doctor` for every typo.
//
// Hand-built beside the structs rather than generated from them, because the
// generation that matters — descriptions a person reads in a hover — does not
// live in struct tags, and a go:generate step extracting doc comments is a
// build dependency this repo does not otherwise carry. What keeps it honest
// is the same arrangement that keeps profileKeys honest: schema_test.go walks
// the structs' yaml tags and the claimed-keys maps against these properties
// in both directions, so a field added without a schema entry fails the next
// test run rather than silently completing to nothing.
//
// The schema states the *envelope*, deliberately: `plugins:` sections and
// `set:` overlays hold keys each plugin declares about itself, and `theme:`
// names internal/render/theme's fields — none of which this leaf package may
// know. Those stay open objects with a description saying who does know, and
// `rta doctor` remains the deep validator.

// schemaPatterns, named once so a grammar stated here cannot drift from the
// code that enforces it.
var (
	// schemaPluginKey mirrors what registration and pluginconf accept: a
	// namespace, with an artifact pin for anything on $PATH.
	schemaPluginKey = `^[a-z][a-z0-9-]*(@[0-9a-f]+)?$`
	// schemaProfileKey adds the instance label a profile entry may carry —
	// `pg/analytics@pin` — which a top-level plugins: section may not: plugin
	// config is about the artifact, instances are about places, and places
	// live in profiles.
	schemaProfileKey = `^[a-z][a-z0-9-]*(/[a-z][a-z0-9-]*)?(@[0-9a-f]+)?$`
	schemaDuration   = `^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`
	schemaColor      = `^#[0-9a-fA-F]{6}$`
)

// Schema is the JSON Schema (draft 2020-12) for the config file.
func Schema() map[string]any {
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"title":   "rta configuration",
		"description": "rta's config file. Zero config is a valid config: everything here is " +
			"optional, and rta works without the file existing at all.",
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"output": map[string]any{
				"description": "Default --output format when the flag is not given.",
				"type":        "string",
				"enum":        []any{"pretty", "json", "yaml", "csv", "md"},
			},
			"dashboard": map[string]any{
				"description": "The TUI's landing screen. With nothing set it builds itself, " +
					"one tile per registered plugin, and plugins installed later still appear.",
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"tiles": map[string]any{
						"description": "States the dashboard exactly, replacing the automatic " +
							"set; hidden and order are then not consulted. Only Read " +
							"capabilities run as tiles.",
						"type":  "array",
						"items": map[string]any{"$ref": "#/$defs/tile"},
					},
					"hidden": map[string]any{
						"description": "Capability IDs to leave out of the automatic set.",
						"type":        "array",
						"items":       map[string]any{"type": "string"},
					},
					"order": map[string]any{
						"description": "Capability IDs to place first, in this order; anything " +
							"not named keeps its natural position after them.",
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
					"columns": map[string]any{
						"description": "Fixes the grid width instead of deriving it from the " +
							"terminal; 0 means automatic.",
						"type":    "integer",
						"minimum": 0,
					},
				},
			},
			"plugins": map[string]any{
				"description": "Each plugin's own settings, stated once instead of retyped per " +
					"invocation. The key is a namespace for a built-in and " +
					"namespace@digest for anything on $PATH. What the keys inside a " +
					"section mean is each plugin's declaration — `rta explain <ns>` " +
					"lists them, and `rta doctor` reports the ones nothing reads.",
				"type":                 "object",
				"propertyNames":        map[string]any{"pattern": schemaPluginKey},
				"additionalProperties": map[string]any{"type": "object"},
			},
			"profiles": map[string]any{
				"description": "Named environments switched between with `rta use` or " +
					"--profile, and the unit an agent grant names.",
				"type":                 "object",
				"propertyNames":        map[string]any{"pattern": profileName.String()},
				"additionalProperties": map[string]any{"$ref": "#/$defs/profile"},
			},
			"theme": map[string]any{
				"description": "Overrides the built-in palette. Keys are the names `rta theme` " +
					"lists (primary, good, label, …), each a #rrggbb string.",
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string", "pattern": schemaColor},
			},
			"roles": map[string]any{
				"description": "Bundles `rta grant issue <role>` issues whole, under one passphrase: " +
					"grant lines in the grammar `rta grant allow` takes, and how long the grants last.",
				"type":                 "object",
				"additionalProperties": map[string]any{"$ref": "#/$defs/role"},
			},
		},
		"$defs": map[string]any{
			"role": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []any{"grants"},
				"properties": map[string]any{
					"grants": map[string]any{
						"description": "One grant per line: a target, an optional record, and --profile, " +
							"--ttl, --max-uses, --rate or --note. The agent is the session's.",
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
					"ttl": map[string]any{
						"description": "How long the role's grants last unless --ttl says: 8h, 12h (the default).",
						"type":        "string",
					},
				},
			},
			"tile": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []any{"id"},
				"properties": map[string]any{
					"id": map[string]any{
						"description": "The capability this tile runs, e.g. note.list.",
						"type":        "string",
					},
					"with": map[string]any{
						"description": "Inputs for the run, keyed the way the capability " +
							"declares them.",
						"type": "object",
					},
					"span": map[string]any{
						"description": "How many grid columns this tile occupies; 0 leaves the " +
							"decision to the capability.",
						"type":    "integer",
						"minimum": 0,
					},
				},
			},
			"profile": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"plugins": map[string]any{
						"description": "What this environment is: the connections, keyed the " +
							"way the top-level plugins: section is keyed — pinned " +
							"(pg@1a2b3c4d) for anything on $PATH, so a PATH impostor " +
							"cannot inherit the stated connection — plus an optional " +
							"instance label (pg/analytics@1a2b3c4d) when one " +
							"environment holds several connections to the same " +
							"plugin. A bare key is the default instance; a call " +
							"names a labeled one as --profile <name>/<instance>.",
						"type":                 "object",
						"propertyNames":        map[string]any{"pattern": schemaProfileKey},
						"additionalProperties": map[string]any{"$ref": "#/$defs/connection"},
					},
					"note": map[string]any{
						"description": "Why this environment exists — printed by `rta profile " +
							"list`, `rta use` and `rta doctor`.",
						"type": "string",
					},
					"ttl": map[string]any{
						"description": "How long an activation lasts by default (2h, 30m); " +
							"empty means no deadline. `rta use --for` overrides it.",
						"type":    "string",
						"pattern": schemaDuration,
					},
					"color": map[string]any{
						"description": "Marks this environment, so which one is switched on " +
							"is legible before the command runs. Paints the profile's own " +
							"name and nothing else — never the palette, which would put " +
							"this colour beside the ones that mean ok, warn and failed.",
						"type":    "string",
						"pattern": schemaColor,
					},
				},
			},
			"connection": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"set": map[string]any{
						"description": "Overlays the plugin's own configuration; keys are the " +
							"same Field.Config keys legal under plugins.<ns>.",
						"type": "object",
					},
					"secrets": map[string]any{
						"description": "Where each credential input's value comes from — a " +
							"reference, never a value: kv:<entry> for the local " +
							"store, kube:<secret>/<key> for a cluster Secret.",
						"type": "object",
						"additionalProperties": map[string]any{
							"type":    "string",
							"pattern": `^(kv|kube):.+$`,
						},
					},
					"kube": map[string]any{
						"description": "Port-forward coordinate, context/namespace/kind/name:port. " +
							"The call runs through a forward the host raises and " +
							"tears down; kube: Secrets are read from its namespace.",
						"type":    "string",
						"pattern": `^[^/]+/[^/]+/[^/]+/[^/:]+:[0-9]+$`,
					},
					"ssh": map[string]any{
						"description": "Jump-host target, [user@]host[:port]/desthost:destport — " +
							"the head is an ~/.ssh/config alias or hostname. States " +
							"at most one of kube: and ssh:.",
						"type": "string",
					},
					"secrets-from": map[string]any{
						"description": "Which cluster and namespace kube: secret references are " +
							"read from, as context/namespace, for a connection that " +
							"reaches its service directly and opens no forward. With " +
							"kube: present the Secrets already come from its " +
							"namespace, so state at most one of the two.",
						"type":    "string",
						"pattern": `^[^/]+/[^/]+$`,
					},
					"tunnelTLS": map[string]any{
						"description": "The far side of this connection's forward terminates TLS " +
							"itself, so URL-role inputs are filled with https. A " +
							"statement about the destination, never the hop; the " +
							"certificate is still verified.",
						"type": "boolean",
					},
				},
			},
		},
	}
}
