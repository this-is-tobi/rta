package config

// SchemaFile is the name `rta config edit` keeps the JSON Schema under, beside
// the config file, and the one the header's modeline points at.
//
// Named for what it is rather than schema.json: RTA_CONFIG can put the config
// file in any directory, and a file called schema.json that rta rewrites on
// every edit is a collision waiting for the directory that already has one.
const SchemaFile = "config.schema.json"

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
