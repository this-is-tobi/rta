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

import (
	"slices"
	"strings"
)

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
	schemaDuration   = `^([0-9]+(\.[0-9]+)?(ns|us|µs|μs|ms|s|m|h|d|w))+$`
	schemaColor      = `^#[0-9a-fA-F]{6}$`
)

// themeKeys are the palette slots a theme: block may name, in the order the
// palette lists them. The package that owns them is the render tree's theme,
// which this leaf does not import, so a test holds the two lists to one answer.
var themeKeys = []string{"primary", "accent", "muted", "faint", "label", "good", "warn", "bad", "inverse", "ink"}

// statusThemeKeys are the slots only the operator's own named config file can
// state: the colours that mean ok, warn and failed, and the text of the two
// badges that say the last two. An environment's palette and a file nobody named
// are refused them (theme.Resolve), so a profile's theme: is not offered them.
// Held to the palette's own answer by a test, for the reason themeKeys is.
var statusThemeKeys = []string{"good", "warn", "bad", "inverse", "ink"}

// profileThemeKeys are the slots a profile's theme: may name.
func profileThemeKeys() []string {
	keys := make([]string, 0, len(themeKeys))
	for _, key := range themeKeys {
		if !slices.Contains(statusThemeKeys, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// schemaProfileRef is what a tile's profile: may say — a profile name, or
// name/instance — built from the loader's own name pattern rather than
// restated, so the two cannot drift, with the instance half spelled the way
// schemaProfileKey already spells it.
var schemaProfileRef = strings.TrimSuffix(profileName.String(), "$") + `(/[a-z][a-z0-9-]*)?$`

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
				"properties":           withViews(dashboardFields()),
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
			"theme": themeBlock("Overrides the built-in palette: "+strings.Join(themeKeys, ", ")+
				", each a #rrggbb string. A name or colour rta cannot use is left out and "+
				"reported by `rta doctor`; the rest apply. The status colours (good, warn, bad, "+
				"inverse, ink) count only in your own config file, and a file nobody named, such as a "+
				"./.rta.yaml in a repository, may state none of them, nor a text colour under 1.5:1 "+
				"against black or white.", themeKeys),
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
						"description": "How long the role's grants last unless --ttl says: 8h, 12h (the default), at most 1d.",
						"type":        "string",
						"pattern":     schemaDuration,
					},
					"agent": map[string]any{
						"description": "Which agent `rta grant issue` issues this role to when --agent is not given. " +
							"Your config only; a policy file naming an agent is refused.",
						"type": "string",
					},
				},
			},
			"view": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties":           dashboardFields(),
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
					"profile": map[string]any{
						"description": "The connection this tile is about — a profile name, or " +
							"name/instance — whatever environment is switched on. Omitted, " +
							"the tile follows the switched-on environment.",
						"type":    "string",
						"pattern": schemaProfileRef,
					},
					"width":   widthSchema("this tile is", "its section's, then the screen's, then the terminal's"),
					"height":  heightSchema("this tile's box is", "its section's, then the screen's, then what it has to say"),
					"refresh": refreshSchema("this tile is re-run", "its section's, then the screen's, then its capability's"),
					"section": map[string]any{
						"description": "The group this tile is drawn in, under a header that folds. " +
							"Tiles that name the same section are drawn together, at the place of the " +
							"first of them. Left out, the tile is in none — unless it expanded into " +
							"several panels, which are then in a section of their own named " +
							"`<plugin> · <profile>`.",
						"type":      "string",
						"minLength": 1,
						"maxLength": MaxSectionName,
					},
				},
			},
			"section": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"collapsed": map[string]any{
						"description": "Whether the section starts folded to its one header row. " +
							"z and Z in the TUI fold and unfold for the session and write nothing.",
						"type": "boolean",
					},
					"width":   widthSchema("this section's tiles are", "the screen's"),
					"height":  heightSchema("this section's tiles' boxes are", "the screen's"),
					"refresh": refreshSchema("this section's tiles are re-run", "the screen's"),
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
					"dashboard": map[string]any{
						"description": "The screens that belong to this environment: the name of one " +
							"of `dashboard: views:`, or a list of names — the first is drawn while this " +
							"profile is switched on and V walks all of them, and only them, while it is — " +
							"or the screen itself written here, with the keys a view has (tiles, add, hidden, " +
							"order, width, height, refresh, sections), addressed as a view named after this " +
							"profile. `default` is the `dashboard:` block, as a list entry. Left out, the " +
							"`dashboard:` block draws, and V walks every screen there is.",
						"oneOf": []any{
							map[string]any{"type": "string", "pattern": viewName.String()},
							map[string]any{
								"type":        "array",
								"minItems":    1,
								"uniqueItems": true,
								"items": map[string]any{
									"anyOf": []any{
										map[string]any{"type": "string", "pattern": viewName.String()},
										map[string]any{"const": DefaultView},
									},
								},
							},
							map[string]any{"$ref": "#/$defs/view"},
						},
					},
					"color": map[string]any{
						"description": "Marks this environment, so which one is switched on " +
							"is legible before the command runs: the colour of its badge, and " +
							"the primary colour (selection, titles, keys) of every screen drawn " +
							"while it is in force, unless its own theme states a primary. A " +
							"colour that is exactly one of the status colours, or under 1.5:1 " +
							"against black or white, is left out of the palette and the badge " +
							"keeps it, so a mark never reads as ok, warn or failed.",
						"type":    "string",
						"pattern": schemaColor,
					},
					"theme": themeBlock("Overrides the palette while this environment is in force: the "+
						"text colours of the top-level theme block, laid over it and over this "+
						"profile's color. The status colours (good, warn, bad, inverse, ink) are not an "+
						"environment's to state: they are set in your own config file. A name or "+
						"colour rta cannot use, a text colour under 1.5:1 against black or white, or "+
						"a primary that is a status colour, is left out and reported by `rta doctor`; "+
						"the rest apply.", profileThemeKeys()),
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
					"only": map[string]any{
						"description": "Fences inputs to an allowlist: per config key, a list " +
							"of the values this connection reaches. A call whose value " +
							"for the input falls outside it is refused before anything " +
							"is dialled — unlike set:, which only supplies a default the " +
							"caller can override. A set: value must be in its own list.",
						"type": "object",
						"additionalProperties": map[string]any{
							"type":     "array",
							"minItems": 1,
							"items": map[string]any{
								"type": []string{"string", "integer", "number", "boolean"},
							},
						},
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

// themeBlock is the schema of a block of palette colours, which the top-level
// `theme:` states and so does each profile: one grammar, described once, so
// the two cannot disagree about which colours an editor completes. The names are
// the caller's, since a profile is not offered the status colours.
func themeBlock(description string, keys []string) map[string]any {
	return map[string]any{
		"description":          description,
		"type":                 "object",
		"propertyNames":        map[string]any{"enum": keys},
		"additionalProperties": map[string]any{"type": "string", "pattern": schemaColor},
	}
}

// dashboardFields are the six keys of a dashboard arrangement, which the
// `dashboard:` block states and so does each of its views.
func dashboardFields() map[string]any {
	return map[string]any{
		"tiles": map[string]any{
			"description": "States the dashboard exactly, replacing the automatic " +
				"set; hidden and order are then not consulted. Only Read " +
				"capabilities run as tiles.",
			"type":  "array",
			"items": map[string]any{"$ref": "#/$defs/tile"},
		},
		"add": map[string]any{
			"description": "Tiles joined to the automatic set without freezing it: " +
				"a capability the automatic dashboard leaves out, or one " +
				"capability several times, each pinned to its own profile. " +
				"`rta dashboard add` writes an entry here.",
			"type":  "array",
			"items": map[string]any{"$ref": "#/$defs/tile"},
		},
		"hidden": map[string]any{
			"description": "What to leave off the screen: a capability ID hides an " +
				"automatic tile and every panel it expanded into; a tile key, " +
				"capability@profile or capability@profile/instance, hides that one " +
				"panel of an entry that expanded into several connections.",
			"type":  "array",
			"items": map[string]any{"type": "string"},
		},
		"order": map[string]any{
			"description": "Tile keys to place first, in this order — a capability " +
				"ID, or capability@profile for a pinned tile; anything not " +
				"named keeps its natural position after them.",
			"type":  "array",
			"items": map[string]any{"type": "string"},
		},
		"width":   widthSchema("every tile of this screen is", "each tile's own, from the terminal's width"),
		"height":  heightSchema("every tile's box on this screen is", "what each tile has to say"),
		"refresh": refreshSchema("every tile of this screen is re-run, unless its section or it says otherwise", "each capability's own"),
		"sections": map[string]any{
			"description": "How each section the tiles name starts out and how wide it draws, by " +
				"section name: `collapsed: true` opens it folded to its header row, `width`, `height` and `refresh` " +
				"are its tiles' own. An automatic group — the panels one entry expanded into — is " +
				"named `<plugin> · <profile>`. A name no tile is in is reported by `rta config check`.",
			"type":                 "object",
			"propertyNames":        map[string]any{"minLength": 1, "maxLength": MaxSectionName},
			"additionalProperties": map[string]any{"$ref": "#/$defs/section"},
		},
	}
}

// withViews is the dashboard block's keys with `views:`, the one key a view
// does not have: a view is a whole arrangement and holds no views.
func withViews(fields map[string]any) map[string]any {
	fields["views"] = map[string]any{
		"description": "Named dashboards, each a whole arrangement of its own with the same " +
			"keys, and a profile links them with `dashboard:` — the first drawn while that profile is " +
			"switched on, in place of this block, and V walking the rest. A view's tiles pin themselves to whichever " +
			"profiles they name, so one view can show every database of every environment.",
		"type":                 "object",
		"propertyNames":        map[string]any{"pattern": viewName.String(), "not": map[string]any{"const": DefaultView}},
		"additionalProperties": map[string]any{"$ref": "#/$defs/view"},
	}
	return fields
}

// refreshSchema is the one declaration of a `refresh:`, for a tile, a section and
// a screen alike: the same pattern, the same bound and the same sentence about
// what a value cannot do, since the key is meant to work the same wherever it is
// written.
func refreshSchema(what, inherits string) map[string]any {
	return map[string]any{
		"description": "How often " + what + " (30s, 5m, 1h; at most 24h). Left out, it is " + inherits +
			" — the nearest one wins. Never faster than the dashboard's own 5s tick, nor than a " +
			"pace the capability declared (eol.watch runs every 2h); a capability that reaches a " +
			"configured connection runs every 30s unless told.",
		"type":    "string",
		"pattern": schemaDuration,
	}
}

// widthSchema and heightSchema are the one declaration of `width:` and `height:`,
// for a tile, a section and a screen alike, as refreshSchema is of a pace: the same
// range and the same sentence about what a value is, since the keys are meant to
// work the same wherever they are written.
func widthSchema(what, inherits string) map[string]any {
	return map[string]any{
		"description": "How wide " + what + ", in twelfths of the screen: 12 is the whole row, 6 half, 4 a " +
			"third, 3 a quarter. Left out, it is " + inherits + " — the nearest one wins. A tile is never " +
			"drawn narrower than it can be read, so a width that does not fit a narrow terminal is read as the " +
			"least that does; one a section or the screen states yields to a tile that needs more room.",
		"type":    "integer",
		"minimum": 0,
		"maximum": GridTracks,
	}
}

func heightSchema(what, inherits string) map[string]any {
	return map[string]any{
		"description": "How tall " + what + ", in lines, borders included. Left out, it is " + inherits +
			" — the nearest one wins. A tile with more to show than eleven lines is lifted past that cap by " +
			"saying so; a row is as tall as the tallest of its tiles.",
		"type":    "integer",
		"minimum": 0,
		"maximum": MaxHeight,
	}
}
