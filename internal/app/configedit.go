package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/editor"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/shutdown"
	"github.com/this-is-tobi/rta/internal/stdio"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

// noteMark opens each line of the note an editor is reopened with. Not `# rta:`
// or anything a person would write at the top of a file themselves: what starts
// with this is taken out again before the text is read.
const noteMark = "#!rta "

func configEditCommand(reg *registry.Registry, render renderFn, opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:         "edit",
		Annotations: outputExempt(),
		Short:       "Open the config file in $EDITOR, and check it on save",
		Long: "Opens the config file in $VISUAL, then $EDITOR, then vi, and holds what you save to\n" +
			"the same checks as `rta config check` before it replaces the file. A file that does\n" +
			"not parse reopens the editor with the reason and its line at the top, and leaving\n" +
			"the file as it is cancels; a key rta ignores is saved and named, with its line.\n\n" +
			"With no file yet it opens a starter with the common keys commented out, so nothing\n" +
			"is stated until you uncomment it. It keeps config.schema.json beside the file, which\n" +
			"the file's first lines point an editor at: completion and a warning on a wrong key\n" +
			"as you type. An editor that returns before you have saved loses the edit, so a\n" +
			"windowed one needs its wait flag: `EDITOR='code --wait'`.",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			v, verr := runConfigEdit(reg, opts.dryRun)
			return render(cmd, v, verr)
		},
	}
}

// editorTerminal is whether a person is here to edit a file, a var so a test
// can be one.
var editorTerminal = func() bool {
	return term.IsTerminal(int(stdio.Real().Fd())) && isTTY()
}

func runConfigEdit(reg *registry.Registry, dryRun bool) (view.View, *view.Error) {
	if !editorTerminal() {
		return nil, view.Errorf("core.config.edit.noterminal", "an editor needs a terminal, and there is none here").
			WithHint("`rta config set <key> <value>` changes one key without one, and `rta config path` " +
				"names the file for whatever does the editing")
	}
	argv := editor.Command()
	if dryRun {
		return view.Text{Body: "would open " + config.Path() + " in " + argv[0]}, nil
	}
	original, err := config.ReadText()
	if err != nil {
		return nil, view.AsError(err, "core.config.read")
	}
	body := original
	if body == nil {
		body = []byte(config.Starter())
	}

	// Held until the copy is gone, so a process asked to stop lets the edit end
	// before it exits (internal/shutdown). The copy is in a directory only this
	// account can enter, since the editor's own swap and backup files land
	// beside it: removing the directory removes all of them without knowing what
	// any particular editor calls them.
	defer shutdown.Hold()()
	dir, err := os.MkdirTemp("", "rta-config-")
	if err != nil {
		return nil, view.Errorf("core.config.edit.notemp", "making a private directory to edit in: %v", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.yaml")
	writeSchemaFile(reg, dir, body)

	note := ""
	for {
		if err := atomicfile.Write(path, []byte(note+string(body)), 0o600); err != nil {
			return nil, view.Errorf("core.config.edit.notemp", "writing the file to edit: %v", err)
		}
		if err := editor.Run(argv, path); err != nil {
			return nil, view.Errorf("core.config.edit.failed", "%s: %v", argv[0], err).
				WithHint("nothing was changed — set $EDITOR to something on this machine")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, view.Errorf("core.config.edit.gone", "the editor left no file behind: %v", err).
				WithHint("nothing was changed")
		}
		edited := stripNote(raw)
		if bytes.Equal(edited, body) {
			if note != "" {
				return view.KeyValue{Pairs: []view.Pair{{Key: "cancelled",
					Value: "the file was left as it was, so nothing was written to " + config.Path()}}}, nil
			}
			return view.KeyValue{Pairs: []view.Pair{{Key: "unchanged",
				Value: "nothing was changed in " + config.Path()}}}, nil
		}
		problems := configProblems(reg, edited)
		if fatal := fatalOnly(problems); len(fatal) > 0 {
			body, note = edited, editNote(fatal)
			continue
		}
		if err := config.Replace(original, edited); err != nil {
			return nil, view.AsError(err, "core.config.write")
		}
		writeSchemaFile(reg, filepath.Dir(config.Path()), edited)
		saved := view.Pair{Key: "wrote", Value: "saved " + config.Path()}
		if len(problems) == 0 {
			return view.KeyValue{Pairs: []view.Pair{saved}}, nil
		}
		check := view.Pair{Key: "check", Value: format.Count(len(problems), "problem", "problems") +
			" — saved as written, so what does not apply is named below, and `rta config edit` opens the file again"}
		return view.Sections{Items: []view.Section{
			{ID: "receipt", Title: "Saved", View: view.KeyValue{Pairs: []view.Pair{saved, check}}},
			{ID: "problems", Title: "Does not apply", View: problemsTable(problems, "")},
		}}, nil
	}
}

func fatalOnly(problems []configProblem) []configProblem {
	var out []configProblem
	for _, p := range problems {
		if p.Fatal {
			out = append(out, p)
		}
	}
	return out
}

// editNote is what the editor is reopened with: the reason the file was not
// saved, as comment lines at the top. A line number in it counts from the first
// line of the file after the note, which is where the person is looking.
func editNote(fatal []configProblem) string {
	shift := len(fatal) + 2
	lines := make([]string, 1, shift)
	lines[0] = "The file was not saved:"
	for _, p := range fatal {
		line := "  " + p.Text
		if p.Line > 0 {
			line = "  line " + strconv.Itoa(p.Line+shift) + ": " + p.Text
		}
		lines = append(lines, line)
	}
	lines = append(lines, "Fix it and save, or leave the file as it is to cancel. These lines go when you save.")
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(noteMark + l + "\n")
	}
	return b.String()
}

// stripNote takes the note off the top of what the editor returned.
func stripNote(raw []byte) []byte {
	for bytes.HasPrefix(raw, []byte(noteMark)) {
		_, rest, ok := bytes.Cut(raw, []byte("\n"))
		if !ok {
			return nil
		}
		raw = rest
	}
	return raw
}

// writeSchemaFile keeps the JSON Schema beside a file whose first lines point an
// editor at it, so completion and a warning on a wrong key work while it is
// edited and after. Best effort: a schema is a convenience, and failing to
// write one is no reason to refuse the edit it was for.
func writeSchemaFile(reg *registry.Registry, dir string, text []byte) {
	if !bytes.Contains(text, []byte("$schema="+config.SchemaFile)) {
		return
	}
	out, err := json.MarshalIndent(configSchemaFor(reg), "", "  ")
	if err != nil {
		return
	}
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	_ = atomicfile.Write(filepath.Join(dir, config.SchemaFile), append(out, '\n'), 0o644)
}
