package spelling

import "testing"

func TestTerminalWordingFindsWhatAddressesAPersonAtACommandLine(t *testing.T) {
	for text, want := range map[string]string{
		"Run it from a terminal.":                "from a terminal",
		"Read on the terminal, not in a file.":   "on the terminal",
		"Piping it keeps only the names.":        "Piping",
		"Taken from a pipe":                      "",
		"Shown as a dashboard tile.":             "dashboard tile",
		"Pinned on the dashboard.":               "on the dashboard",
		"Press enter in the TUI.":                "the TUI",
		"Not kept in shell history.":             "shell history",
		"Opens the full-page surface.":           "full-page surface",
		"Lists the Grafana dashboards.":          "",
		"One tile of the map is returned.":       "",
		"Returns the stored items, newest first": "",
	} {
		if got := TerminalWording(text); got != want {
			t.Errorf("TerminalWording(%q) = %q, want %q", text, got, want)
		}
	}
}
