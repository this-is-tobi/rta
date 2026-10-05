package app

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginconf"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

// configProblem is one thing in the config file that does not do what it
// says: a key rta ignores, a value no call would accept, a section that
// reaches no plugin.
type configProblem struct {
	// Line is where the key is written, or 0 for a problem found by a reader
	// that does not keep one (the palette, a plugin's declaration).
	Line int
	Key  string
	Text string
	// Fatal marks the two that stop every command rather than one setting: a
	// file that does not parse, and an output format nothing renders.
	Fatal bool
}

var parseSpot = regexp.MustCompile(`^\[(\d+):\d+\] `)

// configProblems holds config text to everything rta reads it for.
//
// Over the text rather than over the file on disk, so `rta config edit` asks
// the same questions of what the editor returned before any of it is written,
// and `rta config check` and the editor cannot disagree about one file. The
// questions are the ones `rta doctor` asks of the config, in the same words:
// that is deliberate, so a problem reads the same wherever it is found, and
// this is what doctor is to be shown by the keys rta ignores.
func configProblems(reg *registry.Registry, data []byte) []configProblem {
	cfg, err := config.Parse(data)
	if err != nil {
		verr := view.AsError(err, "config.invalid")
		msg, _, _ := strings.Cut(verr.Message, "\n")
		p := configProblem{Text: msg, Fatal: true}
		if at := strings.Index(msg, ": "); at >= 0 {
			if m := parseSpot.FindStringSubmatch(msg[at+2:]); m != nil {
				p.Line, _ = strconv.Atoi(m[1])
				p.Text = strings.TrimPrefix(msg[at+2:], m[0])
			}
		}
		return []configProblem{p}
	}

	var out []configProblem
	for _, f := range config.CheckText(data) {
		text := f.Reason
		if f.Hint != "" {
			text += " (" + f.Hint + ")"
		}
		out = append(out, configProblem{Line: f.Line, Key: f.Key, Text: text})
	}
	lines := config.KeyLines(data)
	if _, err := cli.ParseFormat(cfg.Output); err != nil {
		out = append(out, configProblem{Key: "output", Line: lines["output"], Fatal: true,
			Text: fmt.Sprintf("%q is not an output format (it takes %s)", cfg.Output, formatNames())})
	}
	// Apply resets the palette to what it is told, and this process has already
	// applied the file's own: put that back, so a check of one file does not
	// leave another's colours on the screen.
	was := theme.Current()
	for _, p := range theme.Apply(cfg.Theme) {
		text := p.Reason
		if p.Hint != "" {
			text += " (" + p.Hint + ")"
		}
		key := "theme." + p.Field
		out = append(out, configProblem{Key: key, Line: lines[key], Text: text})
	}
	theme.Apply(was)

	resolver, problems := pluginconf.Resolve(cfg, reg.Origin)
	problems = append(problems, resolver.Check(reg)...)
	for _, p := range problems {
		text := p.Reason
		if p.Hint != "" {
			text += " (" + p.Hint + ")"
		}
		key := "plugins." + p.Section
		if p.Key != "" {
			key += "." + p.Key
		}
		out = append(out, configProblem{Key: key, Line: lines[key], Text: text})
	}
	for _, line := range groupedProblems(profile.Check(cfg, withTrust{reg})) {
		out = append(out, configProblem{Key: "profiles", Text: line})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Line, out[j].Line
		return (a != 0 && b == 0) || (a != 0 && b != 0 && a < b)
	})
	return out
}

func problemsTable(problems []configProblem, empty string) view.Table {
	t := view.Table{Columns: []view.Column{{Name: "Line", Kind: view.KindNumber}, {Name: "Key"}, {Name: "Problem"}}}
	for _, p := range problems {
		line := ""
		if p.Line > 0 {
			line = strconv.Itoa(p.Line)
		}
		t.Rows = append(t.Rows, []string{line, p.Key, p.Text})
	}
	t.Total = len(t.Rows)
	t.Empty = empty
	return t
}

func configCheckCommand(reg *registry.Registry, render renderFn) *cobra.Command {
	return &cobra.Command{
		Use:         "check",
		Annotations: outputExempt(),
		Short:       "Name what in the config file rta ignores or cannot use",
		Long: "Holds the file to everything rta reads it for and lists what does not apply: a key\n" +
			"rta has no use for, with the line it is on and the one it was probably meant to be,\n" +
			"a value no call would take, a colour that is not one, a section that reaches no plugin.\n" +
			"Exits 1 when it finds anything, so it can gate a dotfiles repository or a CI step.\n" +
			"`rta doctor` says the same of the config among everything else about this machine.",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			data, err := config.ReadText()
			if err != nil {
				return render(cmd, nil, view.AsError(err, "core.config.read"))
			}
			if data == nil {
				return render(cmd, problemsTable(nil, "No config file at "+config.Path()+
					", so there is nothing to check: every key is at its default."), nil)
			}
			problems := configProblems(reg, data)
			if len(problems) == 0 {
				return render(cmd, problemsTable(nil, config.Path()+" has nothing rta ignores."), nil)
			}
			if err := render(cmd, problemsTable(problems, ""), nil); err != nil {
				return err
			}
			return render(cmd, nil, view.Errorf("core.config.check", "%s in %s",
				format.Count(len(problems), "problem", "problems"), config.Path()).
				WithHint("`rta config edit` opens the file and checks it again on save"))
		},
	}
}

// WarnIgnoredConfigKeys says once, at startup on a terminal, that the config
// file holds keys rta ignores.
//
// The decoder drops a key it has no field for without a word, so a typo in the
// file is a setting that quietly does nothing, and nothing in a command that
// succeeds would say so. One line, and only for a person: the same two
// conditions as the other startup notices, a terminal on the stream and prose
// asked for. Not for the commands that report the file in full, where it would
// say the same thing twice.
func WarnIgnoredConfigKeys(w io.Writer, cmd *cobra.Command, machineReadable bool) {
	if machineReadable || reportsConfigItself(cmd) {
		return
	}
	found, err := config.Check()
	if err != nil || len(found) == 0 {
		return
	}
	fmt.Fprintf(w, "rta: %s in %s — `rta config check` names %s\n",
		format.Count(len(found), "key rta does not read is", "keys rta does not read are"), config.Path(),
		format.Plural(len(found), "it", "them"))
}

// reportsConfigItself is the commands that already say what is in the file, or
// are how it is fixed.
func reportsConfigItself(cmd *cobra.Command) bool {
	for c := cmd; c != nil && c.HasParent(); c = c.Parent() {
		if c.Parent() == c.Root() {
			switch c.Name() {
			case "config", "doctor", "init", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
				return true
			}
		}
	}
	return false
}
