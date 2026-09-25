package config

import (
	"bytes"
	"os/exec"
	"slices"
	"testing"
)

// AddArgs is the way back a receipt or the TUI's footer spells: the whole
// entry, so it can be pasted, with a list-shaped input as one --set per
// element and the inputs in a stable order.
func TestAddArgsSpellsTheWholeEntry(t *testing.T) {
	cases := []struct {
		tile Tile
		want string
	}{
		{Tile{ID: "sys.overview"}, "sys.overview"},
		{Tile{ID: "kube.overview", Profile: "prod/edge"}, "kube.overview --profile prod/edge"},
		{Tile{ID: "sys.overview", Span: 2, With: map[string]any{"cores": true}}, "sys.overview --span 2 --set cores=true"},
		{Tile{ID: "cert.expiry", With: map[string]any{"port": 8443, "host": "a.example"}},
			"cert.expiry --set host=a.example --set port=8443"},
		{Tile{ID: "eol.check", With: map[string]any{"product": []any{"nodejs", "go"}}},
			"eol.check --set product=nodejs --set product=go"},
	}
	for _, c := range cases {
		if got := c.tile.AddArgs(); got != c.want {
			t.Errorf("%+v: AddArgs() = %q, want %q", c.tile, got, c.want)
		}
	}
}

// The way back is pasted into a shell, so each argument is one word of it.
// Joined as they came, a value with a space, a ';' or a '$(' made a line that
// passed a stray argument or ran a second command, and a value holding an
// escape sequence printed as nothing.
func TestAddArgsIsOneShellWordPerArgument(t *testing.T) {
	tile := Tile{ID: "debug.ansi", Profile: "prod", With: map[string]any{
		"input": "a b; $(id)", "ports": "22,80", "raw": "\x1b[31mhi",
	}}
	want := "debug.ansi --profile prod --set 'input=a b; $(id)' --set ports=22,80 --set $'raw=\\033[31mhi'"
	if got := tile.AddArgs(); got != want {
		t.Errorf("AddArgs() = %s, want %s", got, want)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash to read the line back")
	}
	out, err := exec.Command(bash, "-c", "set -- "+tile.AddArgs()+`; printf '%s\0' "$@"`).Output()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0}) {
		got = append(got, string(w))
	}
	argv := []string{"debug.ansi", "--profile", "prod", "--set", "input=a b; $(id)",
		"--set", "ports=22,80", "--set", "raw=\x1b[31mhi"}
	if !slices.Equal(got, argv) {
		t.Errorf("read back as %q, want %q", got, argv)
	}
}
