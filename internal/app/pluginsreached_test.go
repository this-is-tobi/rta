package app

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Two command lines can do with fewer plugins than every one installed, and
// every other loads them all. The word a completion is narrowed to is never one
// of rta's own: a file named rta-plugin-doctor must not make `rta doctor`, or a
// completion below it, read only itself.
func TestOnlyCompletionLoadsFewerPluginsThanAreInstalled(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	complete := cobra.ShellCompRequestCmd
	for line, want := range map[string]string{
		"":                  "all",
		"completion zsh":    "none",
		complete + " pg <>": "pg",
		cobra.ShellCompNoDescRequestCmd + " pg query <>": "pg",
		complete + " pg":         "all",
		complete + " doctor <>":  "all",
		complete + " plugin <>":  "all",
		complete + " kv <>":      "all",
		complete + " -o json <>": "all",
		complete + " <> <>":      "all",
		"pg query":               "all",
		"doctor":                 "all",
		"--version":              "all",
	} {
		var args []string
		for _, w := range strings.Fields(line) {
			args = append(args, strings.ReplaceAll(w, "<>", ""))
		}
		load, name := pluginsReached(reg, args)
		got := name
		switch {
		case !load:
			got = "none"
		case name == "":
			got = "all"
		}
		if got != want {
			t.Errorf("rta %s loads %s, want %s", line, got, want)
		}
	}
}
