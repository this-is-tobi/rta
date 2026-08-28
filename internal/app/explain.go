package app

import (
	"fmt"
	"github.com/this-is-tobi/rule-them-all/internal/config"
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
				// Marked, because it has just been printed. Returning it bare
				// made `rta explain <typo>` print the same error twice, in two
				// slightly different layouts — this one, then main's.
				return Rendered(ve)
			}
			return cli.Render(cmd.OutOrStdout(), cardView(reg, c), renderOpts)
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

func cardView(reg *registry.Registry, c plugin.Capability) view.View {
	pairs := []view.Pair{
		{Key: "id", Value: c.ID},
		{Key: "summary", Value: c.Summary},
		{Key: "safety", Value: string(c.Safety)},
		{Key: "idempotent", Value: fmt.Sprintf("%t", c.Idempotent)},
		{Key: "cli", Value: cliForm(c)},
		{Key: "mcp-tool", Value: mcp.ToolName(c.ID)},
	}
	// What it takes for an agent to reach this at all, before any grant: the
	// operator's flag, spelled out. An external plugin's destructive
	// capability is pinned to that binary's digest, and a control whose
	// correct invocation has to be computed from a hash is a control people
	// turn off — so it is printed rather than described.
	if flag := mcpOptionsForExplain(reg).AllowFlag(c); flag != "" {
		pairs = append(pairs, view.Pair{
			Key:   "mcp exposure",
			Value: "off by default — `rta mcp serve " + flag + "`",
		})
	}
	if grant.Required(c, "") {
		// The safety class alone no longer says what an agent may do, so the
		// card has to say the rest of it.
		need := "yes — a person must run `rta grant allow " + c.ID + "`"
		if c.Scope != "" {
			need += ", optionally naming one " + c.Scope
		}
		pairs = append(pairs, view.Pair{Key: "grant required (mcp)", Value: need})
	}
	// The other axis, and it is a property of the call rather than of the
	// capability: reaching any connection but the operator's base one always
	// needs a grant, whatever the safety class. Printed where somebody looking
	// up "how do I invoke this" will see it, since that is the same page they
	// consult to find out why an agent was refused.
	if plugin.Profilable(c) {
		pairs = append(pairs, view.Pair{
			Key: "profiles",
			Value: "--profile <name> runs this against a configured connection; over MCP that " +
				"always needs `rta grant allow " + plugin.Namespace(c.ID) + " --profile <name>`",
		})
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
			// The variable name too — but only for the inputs that actually
			// read one. A Local input is the way a plugin gets a credential and
			// "how do I give this thing its password" must be answerable from
			// the page describing it rather than from somebody's README. The
			// name, never the value.
			//
			// Gated on EnvFallback because Resolve is (resolve.go's env loop,
			// D74): Local alone means "no remote caller may supply this", and
			// says nothing about where it comes from. Printing the variable for
			// every Local input was true while the two were the same set, and
			// D94 made them different — marking eleven connection inputs across
			// pg, s3 and vault Local without making them EnvFallback, on
			// purpose, because a field that merely chooses a destination must
			// not be fillable from an ambient variable. So `rta explain
			// pg.status` began telling operators that --host comes from
			// $RTA_PG_HOST, which nothing reads. A page that documents a
			// credential channel that does not exist is worse than one that
			// omits it: it is the page somebody consults when the connection is
			// already failing.
			if f.EnvFallback {
				detail += ", from $" + plugin.LocalEnvVar(c.ID, f.Name)
			}
		}
		if f.Config != "" {
			// The key, and where to write it — never the value. What an
			// operator has configured is theirs; this answers "why did that
			// host appear?" and "where do I change it?", which are the two
			// questions, and neither of them needs the value echoed onto a
			// terminal that may be shared.
			detail += ", from config " + configSection(reg, c) + "." + f.Config
		}
		if f.Endpoint != plugin.EndpointNone {
			// Named on the same page and for the same reason the config key
			// is: this answers "why did that host appear?" for the one source
			// an operator did not type anywhere. A profile stating a `kube:`
			// coordinate overwrites this input with an address rta computed,
			// and an input silently overwritten by the host is the kind of
			// thing somebody discovers by wondering why their `set: host` did
			// nothing.
			detail += ", filled by a profile's tunnel (the forward's " + string(f.Endpoint) + ")"
		}
		if f.Help != "" {
			detail += " — " + f.Help
		}
		pairs = append(pairs, view.Pair{Key: "input:" + f.Name, Value: detail})
	}
	// Where that block goes, once, rather than on every line that names it.
	// This card already answers "why did that host appear?" and "what do I
	// write?"; without the path it does not answer "into which file", and an
	// operator had to run `rta doctor` for the third of the three questions.
	// Only when something here can actually read a file.
	for _, f := range c.Inputs {
		if f.Config != "" {
			pairs = append(pairs, view.Pair{Key: "config file", Value: config.Path()})
			break
		}
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

// configSection names the config block this capability's plugin reads,
// spelled the way an operator has to write it: bare for a built-in, and
// pinned to the artifact for anything found on $PATH — the same grammar
// --allow-destructive uses, and the same reason (ADR 0015).
func configSection(reg *registry.Registry, c plugin.Capability) string {
	words := c.Words()
	if len(words) == 0 {
		return "plugins"
	}
	ns := words[0]
	if o, ok := reg.Origin(ns); ok && o.External() {
		return "plugins." + ns + "@" + o.Short()
	}
	return "plugins." + ns
}

// mcpOptionsForExplain is the gate as it would be configured right now, used
// only to ask it what flag a capability needs. One helper rather than two
// literals, so `rta explain` and `rta plugin dev` cannot disagree about what
// it takes to reach the same capability.
//
// It takes the registry because the gate reads provenance from it. That is
// also why this stopped being callable from anywhere: what flag a capability
// needs depends on where the capability came from, and a helper that could
// answer without being told which catalogue it was talking about was
// answering from a package-level variable.
func mcpOptionsForExplain(reg *registry.Registry) mcp.Options {
	return mcp.Options{Origin: reg.Origin}
}
