package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func fixture(t *testing.T) string {
	t.Helper()
	// Short on purpose, and not t.TempDir(): the end-to-end test types this
	// path into an 100-column form, and a temp path long enough to wrap makes
	// the assertion about terminal line breaks rather than about completion.
	dir, err := os.MkdirTemp("/tmp", "p")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	for _, name := range []string{"cert.pem", "certs", "notes.md", ".hidden"} {
		path := filepath.Join(dir, name)
		if name == "certs" {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Every suggestion has to extend what is being typed: suggestions are matched
// by prefix, so a rewritten path silently stops matching.
func TestPathSuggestionsExtendWhatIsTyped(t *testing.T) {
	dir := fixture(t)
	typed := filepath.Join(dir, "cert")
	got := pathSuggestions(typed, nil)
	if len(got) == 0 {
		t.Fatal("no suggestions")
	}
	for _, s := range got {
		if !strings.HasPrefix(s, typed) {
			t.Errorf("%q does not continue %q", s, typed)
		}
	}
}

// A directory keeps its separator, so the next tab walks into it instead of
// stopping on the folder.
func TestPathSuggestionsMarkDirectories(t *testing.T) {
	dir := fixture(t)
	got := pathSuggestions(filepath.Join(dir, "certs"), nil)
	want := filepath.Join(dir, "certs") + string(filepath.Separator)
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Listing a directory means its contents, not its name again.
func TestPathSuggestionsListInsideADirectory(t *testing.T) {
	dir := fixture(t)
	got := pathSuggestions(dir+string(filepath.Separator), nil)
	if len(got) != 3 { // cert.pem, certs/, notes.md — never .hidden
		t.Errorf("got %v, want the three visible entries", got)
	}
}

// Dotfiles stay out of the way until they are asked for by name, exactly as a
// shell does it — otherwise every listing of a home directory is dotfiles.
func TestPathSuggestionsHideDotfilesUntilAsked(t *testing.T) {
	dir := fixture(t)
	if got := pathSuggestions(dir+string(filepath.Separator)+".", nil); len(got) != 1 ||
		!strings.HasSuffix(got[0], ".hidden") {
		t.Errorf("got %v, want the hidden file once named", got)
	}
}

// What the field declared comes first: for an identity those are the keys you
// already have, which beat any amount of walking the disk.
func TestDeclaredCandidatesComeFirst(t *testing.T) {
	dir := fixture(t)
	got := pathSuggestions(dir+string(filepath.Separator), []string{"~/.ssh/id_ed25519"})
	if len(got) == 0 || got[0] != "~/.ssh/id_ed25519" {
		t.Errorf("got %v, want the declared key first", got)
	}
}

// A path that does not exist yet is an output file, not a mistake.
func TestUnknownDirectoryIsNotAnError(t *testing.T) {
	if got := pathSuggestions("/definitely/not/here/x", nil); len(got) != 0 {
		t.Errorf("got %v, want nothing", got)
	}
}

// End to end: the completion is live. A form field asking for a path is a
// blank line, and remembering one exactly with nothing to look at is the
// thing a shell would never ask of you.
func TestPathFieldCompletesWhileTyping(t *testing.T) {
	dir := fixture(t)
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "demo", Summary: "demo",
		Capabilities: []plugin.Capability{{
			ID: "demo.save", Summary: "save it somewhere", Safety: plugin.Write,
			Inputs: []plugin.Field{
				{Name: "out", Type: plugin.Path, Positional: true, Required: true, Help: "where to write it"},
			},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return view.Text{Body: "wrote " + req.String("out")}, nil
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	tm := teatest.NewTestModel(t, New(reg, config.Dashboard{}), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "demo.save")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "tab completes paths")

	for _, r := range dir + string(filepath.Separator) + "cert" {
		tm.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	// The rest of the only matching file, offered as you type it.
	waitFor(t, tm, ".pem")
	quit(t, tm)
}
