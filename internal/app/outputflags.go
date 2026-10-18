package app

import (
	"slices"
	"strings"
)

// formatWords are the names a person gives the output format as a flag of its
// own, from the tools that have `--json`, `--yaml` or `--format`. Here the
// format is a value of the one flag every command takes, `-o`.
var formatWords = []string{"json", "yaml", "csv", "md"}

// outputFlagHint is what to say to `--json`, `--yaml`, `--csv`, `--md` or
// `--format` on a command that has no such flag: the `-o` that does it. "" for
// any other flag, which keeps the guess at the nearest spelling.
//
// A command's own flag of that name never gets here (the parser took it), so a
// capability that declares a `format` input keeps it.
func outputFlagHint(typed string) string {
	typed = strings.ToLower(typed)
	switch {
	case slices.Contains(formatWords, typed):
		return "the output format is chosen with -o, on every command: `-o " + typed + "`"
	case typed == "format":
		return "the output format is chosen with -o, on every command: `-o <format>`, one of " + formatNames()
	}
	return ""
}
