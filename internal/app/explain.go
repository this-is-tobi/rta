package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/mcp"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/render/cli"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// newExplainCommand implements `rta explain [capability]`: the capability
// card (PROJECT.md §6.1). Works for humans, works pasted into a prompt, and
// is what the MCP resources will serve.
func newExplainCommand(reg *registry.Registry, opts *globalOpts) *cobra.Command {
	return &cobra.Command{
		Use:   "explain [capability]",
		Short: "Describe capabilities: inputs, safety class, invocation forms",
		Long: "Without arguments, lists every registered capability.\n" +
			"With a capability ID (e.g. sys.cpu), prints its full card:\n" +
			"summary, safety class, inputs, and CLI/MCP invocation forms.",
		Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
			var ids []cobra.Completion
			for _, c := range reg.Capabilities() {
				ids = append(ids, cobra.CompletionWithDesc(c.ID, c.Summary))
			}
			return ids, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := cli.ParseFormat(opts.output)
			if err != nil {
				return err
			}
			renderOpts := cli.Options{Format: format, NoColor: opts.noColor || !isTTY(), Width: termWidth()}
			if len(args) == 0 {
				return cli.Render(cmd.OutOrStdout(), catalogView(reg), renderOpts)
			}
			c, ok := reg.Capability(args[0])
			if !ok {
				ve := capabilityNotFound(reg, args[0])
				_ = cli.RenderError(cmd.ErrOrStderr(), ve, renderOpts)
				return ve
			}
			return cli.Render(cmd.OutOrStdout(), cardView(c), renderOpts)
		},
	}
}

func catalogView(reg *registry.Registry) view.View {
	t := view.Table{Columns: []view.Column{
		{Name: "Capability"},
		{Name: "Safety", Kind: view.KindStatus},
		{Name: "Summary"},
	}}
	for _, c := range reg.Capabilities() {
		t.Rows = append(t.Rows, []string{c.ID, string(c.Safety), c.Summary})
	}
	t.Total = len(t.Rows)
	return t
}

func cardView(c plugin.Capability) view.View {
	pairs := []view.Pair{
		{Key: "id", Value: c.ID},
		{Key: "summary", Value: c.Summary},
		{Key: "safety", Value: string(c.Safety)},
		{Key: "idempotent", Value: fmt.Sprintf("%t", c.Idempotent)},
		{Key: "cli", Value: cliForm(c)},
		{Key: "mcp-tool", Value: mcp.ToolName(c.ID)},
	}
	if grant.Required(c) {
		// The safety class alone no longer says what an agent may do, so the
		// card has to say the rest of it.
		need := "yes — a person must run `rta grant allow " + c.ID + "`"
		if c.Scope != "" {
			need += ", optionally naming one " + c.Scope
		}
		pairs = append(pairs, view.Pair{Key: "grant required (mcp)", Value: need})
	}
	if c.Description != "" {
		pairs = append(pairs, view.Pair{Key: "description", Value: c.Description})
	}
	for _, f := range c.Inputs {
		detail := string(f.Type)
		if f.Required {
			detail += ", required"
		}
		if f.Default != nil {
			detail += fmt.Sprintf(", default %v", f.Default)
		}
		if len(f.Options) > 0 {
			detail += ", one of: " + strings.Join(f.Options, "|")
		} else if f.Suggest != nil {
			detail += ", completes"
		}
		if f.Local {
			// Worth stating plainly: this input exists here and not over MCP,
			// which is otherwise a surprising asymmetry to discover by hand.
			detail += ", local (never offered to MCP callers)"
		}
		if f.Help != "" {
			detail += " — " + f.Help
		}
		pairs = append(pairs, view.Pair{Key: "input:" + f.Name, Value: detail})
	}
	if c.Detailed {
		// The card is the third surface that has to agree about --detail. The
		// tool description advertises it in CLI syntax and the MCP schema
		// publishes it as a boolean, while the one place a person or an agent
		// is sent to find out how to invoke a capability said nothing at all
		// — so `rta explain sys.overview` described a capability whose
		// richest view did not appear to exist. Worded exactly like the
		// schema's description, since the disagreement is the bug.
		pairs = append(pairs, view.Pair{
			Key:   "input:detail",
			Value: string(plugin.Bool) + ", default false — return the full detailed view instead of the compact summary",
		})
	}
	return view.KeyValue{Pairs: pairs}
}

// cliForm renders the canonical shell invocation for a capability.
func cliForm(c plugin.Capability) string {
	parts := append([]string{"rta"}, c.Words()...)
	for _, f := range c.Inputs {
		switch {
		case f.Positional && f.Required:
			parts = append(parts, "<"+f.Name+">")
		case f.Positional:
			parts = append(parts, "["+f.Name+"]")
		default:
			parts = append(parts, fmt.Sprintf("[--%s <%s>]", f.Name, f.Type))
		}
	}
	// --detail is a host flag rather than a declared input, which is why the
	// loop above cannot see it and why the invocation this card prints was
	// missing the flag its own description advertises. No value placeholder:
	// pflag registers it as a valueless bool, so `--detail true` is a parse
	// error where `--detail` is the whole thing.
	if c.Detailed {
		parts = append(parts, "[--detail]")
	}
	return strings.Join(parts, " ")
}

// capabilityNotFound returns a coded error with closest-match suggestions.
func capabilityNotFound(reg *registry.Registry, id string) *view.Error {
	type scored struct {
		id    string
		score int
	}
	var candidates []scored
	for _, c := range reg.Capabilities() {
		if s := similarity(id, c.ID); s > 0 {
			candidates = append(candidates, scored{c.ID, s})
		}
	}
	e := view.Errorf("core.capability.unknown", "unknown capability %q", id)
	if len(candidates) > 0 {
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
		n := min(3, len(candidates))
		ids := make([]string, n)
		for i := range n {
			ids[i] = candidates[i].id
		}
		return e.WithHint("did you mean: " + strings.Join(ids, ", "))
	}
	return e.WithHint("run `rta explain` to list all capabilities")
}

// similarity is a cheap shared-segment score, good enough for suggestions.
func similarity(a, b string) int {
	score := 0
	for _, sa := range strings.Split(a, ".") {
		for _, sb := range strings.Split(b, ".") {
			if sa == sb {
				score += 2
			} else if strings.HasPrefix(sb, sa) || strings.HasPrefix(sa, sb) {
				score++
			}
		}
	}
	return score
}
