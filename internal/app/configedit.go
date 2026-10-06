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
	cmd := &cobra.Command{
		Use:         "edit [drop-in]",
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
			"windowed one needs its wait flag: `EDITOR='code --wait'`.\n\n" +
			"Name a file of the config.d directory beside it to edit that one instead — an\n" +
			"existing one, or a new one, which is created when you save. It is held to the same\n" +
			"checks, and to one more: a profile, a role, a plugin's settings or a block that\n" +
			"another file already states is refused, since each lives in one file.",
		Example: "  rta config edit              # the config file\n" +
			"  rta config edit 10-prod      # config.d/10-prod.yaml",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeDropIns,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			v, verr := runConfigEdit(reg, opts.dryRun, name)
			return render(cmd, v, verr)
		},
	}
	documentArgs(cmd, argDoc{"drop-in", "a file of the config.d directory beside the config file, by name — " +
		"`10-prod` is config.d/10-prod.yaml; left out, the config file itself"})
	return cmd
}

// completeDropIns offers the files of the drop-in directory by name, without
// their extension, as `rta config edit` takes them.
func completeDropIns(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	files, err := config.Files()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, f := range files {
		if !f.Main {
			names = append(names, strings.TrimSuffix(filepath.Base(f.Path), filepath.Ext(f.Path)))
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// editorTerminal is whether a person is here to edit a file, a var so a test
// can be one.
var editorTerminal = func() bool {
	return term.IsTerminal(int(stdio.Real().Fd())) && isTTY()
}

func runConfigEdit(reg *registry.Registry, dryRun bool, name string) (view.View, *view.Error) {
	if !editorTerminal() {
		return nil, view.Errorf("core.config.edit.noterminal", "an editor needs a terminal, and there is none here").
			WithHint("`rta config set <key> <value>` changes one key without one, and `rta config path` " +
				"names the file for whatever does the editing")
	}
	target := config.Path()
	if name != "" {
		var err error
		if target, err = config.DropInFile(name); err != nil {
			return nil, view.AsError(err, "core.config.edit.dropin")
		}
	}
	argv := editor.Command()
	if dryRun {
		return view.Text{Body: "would open " + target + " in " + argv[0]}, nil
	}
	original, err := config.ReadFile(target)
	if err != nil {
		return nil, view.AsError(err, "core.config.read")
	}
	// A file with nothing in it is opened as no file is: on the starter. A
	// drop-in starts on the one line an editor needs to check it, since what it
	// holds is whole units and the starter's commented keys are the file's own.
	body := original
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte(config.Starter())
		if name != "" {
			body = []byte("# yaml-language-server: $schema=../" + config.SchemaFile + "\n")
		}
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
	if name != "" {
		// Beside a schema one directory up, which is where the header of a
		// drop-in says to look.
		path = filepath.Join(dir, "config.d", filepath.Base(target))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, view.Errorf("core.config.edit.notemp", "making a private directory to edit in: %v", err)
		}
	}
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
					Value: "the file was left as it was, so nothing was written to " + target}}}, nil
			}
			return view.KeyValue{Pairs: []view.Pair{{Key: "unchanged",
				Value: "nothing was changed in " + target}}}, nil
		}
		problems := configProblems(reg, target, edited)
		if fatal := fatalOnly(problems); len(fatal) > 0 {
			body, note = edited, editNote(fatal)
			continue
		}
		if err := config.ReplaceAt(target, original, edited); err != nil {
			return nil, view.AsError(err, "core.config.write")
		}
		writeSchemaFile(reg, filepath.Dir(config.Path()), edited)
		saved := view.Pair{Key: "wrote", Value: "saved " + target}
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
	if !bytes.Contains(text, []byte(config.SchemaFile)) {
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
