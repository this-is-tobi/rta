package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/this-is-tobi/rta/internal/atomicfile"
)

// SchemaFile is the name `rta config edit` keeps the JSON Schema under, beside
// the config file, and the one the header's modeline points at.
//
// Named for what it is rather than schema.json: RTA_CONFIG can put the config
// file in any directory, and a file called schema.json that rta rewrites on
// every edit is a collision waiting for the directory that already has one.
const SchemaFile = "config.schema.json"

// ensureSchemaFile puts the base schema beside a config file rta has just
// written its header into, unless one is there already, so the modeline in that
// header never points at nothing: an editor that cannot load the schema it is
// told to use says so in its problems list, on line 1 of a file nothing is
// wrong with. `rta config edit` replaces it with one that also knows the
// installed plugins' keys, which this package cannot: it does not know them.
//
// Best effort, for the reason the schema exists at all: it is a convenience,
// and a directory that cannot hold a second file is no reason to fail the write
// of the first.
func ensureSchemaFile(dir string) {
	path := filepath.Join(dir, SchemaFile)
	if _, err := os.Stat(path); err == nil {
		return
	}
	data, err := json.MarshalIndent(Schema(), "", "  ")
	if err != nil {
		return
	}
	_ = atomicfile.Write(path, append(data, '\n'), 0o644)
}

// Starter is the text `rta config edit` opens when there is no config file
// yet: the header every written file starts with, and the keys most people
// reach for, each commented out.
//
// Commented out, and never pinned as real keys. A starter that stated the
// defaults would turn each of them into a choice the operator appears to have
// made, and pin it against the day the default improves; one that only shows
// the key and a sample value teaches the file's shape and changes nothing
// until a line is uncommented. A test uncomments every example and holds the
// result to Parse and Check, so a key renamed here without the starter
// following fails the next test run rather than teaching a typo.
func Starter() string {
	return configHeader + starterExamples
}

// starterExamples: every line after the one that says to uncomment is a line of
// the file with its leading `# ` put on.
const starterExamples = `#
# Uncomment what you want to change.
# output: json                  # the default --output: pretty, json, yaml, csv or md
#
# dashboard:
#   columns: 2                  # the grid's width; left out, it follows the terminal
#   hidden: [gen.overview]      # tiles to leave off the screen
#
# theme:
#   primary: '#D97757'          # the ten palette slots; ` + "`rta config schema`" + ` names them
#
# plugins:
#   http:
#     timeout: 10               # ` + "`rta explain http.get`" + ` names each key a plugin reads
`
