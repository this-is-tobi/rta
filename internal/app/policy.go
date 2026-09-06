package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/policy"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

// `rta policy` — the ceiling, as commands rather than as a file somebody
// copies out of the documentation.
//
// Deliberately an app command rather than a capability, so none of it is
// reachable over MCP. Reading a ceiling would be harmless enough — it is
// subtract-only information — but writing one must never be something an
// agent can reach, and keeping the whole group off that surface is a simpler
// guarantee than a per-subcommand one.
//
// The reason this exists at all is the gap `requireRepoPolicy` closes. A
// ceiling nobody can write without hand-editing YAML is a ceiling teams put
// off adopting, and the file they put off writing is the one thing standing
// between a hurried grant and production.

// operatorPolicyPath is the operator's own policy file, beside their config.
//
// This is the file that matters for requireRepoPolicy, and the reason is its
// location rather than its contents: it lives outside every repository, so it
// survives a branch that never carried .rta-policy.yaml, a bad merge, a
// `git clean`, and a client that launched rta from somewhere else.
func operatorPolicyPath() (string, error) {
	if p := policy.OperatorPath(); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("cannot locate your config directory")
}

func newPolicyCommand(opts *globalOpts) *cobra.Command {
	render := func(cmd *cobra.Command, v view.View, verr *view.Error) error {
		format, err := cli.ParseFormat(opts.output)
		if err != nil {
			return err
		}
		renderOpts := cli.Options{Format: format, NoColor: opts.noColor || !isTTY(), Width: termWidth()}
		if verr != nil {
			_ = cli.RenderError(cmd.ErrOrStderr(), verr, renderOpts)
			return Rendered(verr)
		}
		return cli.Render(cmd.OutOrStdout(), v, renderOpts)
	}

	cmd := &cobra.Command{
		Use:   "policy",
		Short: "The ceiling no grant on this machine may exceed",
		Long: "A grant is one person's decision. A policy is the boundary that decision has to" +
			" fit inside — and it can only ever subtract, which is what makes it safe to commit" +
			" to a repository with no seal and no key distribution.\n\n" +
			"The one thing a file cannot defend itself against is not being there. `rta policy" +
			" require` is the answer to that, and it is stored outside the repository on purpose.",
		RunE: groupRunE,
	}

	cmd.AddCommand(policyShowCommand(render))
	cmd.AddCommand(policyInitCommand())
	cmd.AddCommand(policyRequireCommand())
	return cmd
}

// policyShowCommand answers "what is in force, and where did rta look".
//
// The second half is the part worth having. Every previous way of asking this
// reported a ceiling when there was one and said nothing when there was not,
// which is exactly backwards: the case that needs explaining is the empty one.
func policyShowCommand(render func(*cobra.Command, view.View, *view.Error) error) *cobra.Command {
	return &cobra.Command{
		Use:               "show",
		Short:             "What ceiling is in force, and where rta looked for it",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ceiling, verr := grant.Ceiling()
			if verr != nil {
				return render(cmd, nil, verr)
			}
			pairs := []view.Pair{
				{Key: "searched from", Value: ceiling.SearchedFrom},
				{Key: "repository policy", Value: repoPolicyText(ceiling)},
			}
			if p, err := operatorPolicyPath(); err == nil {
				pairs = append(pairs, view.Pair{Key: "your own policy", Value: presentText(p)})
			}
			if explicit := strings.TrimSpace(os.Getenv("RTA_POLICY")); explicit != "" {
				pairs = append(pairs, view.Pair{Key: "RTA_POLICY", Value: explicit})
			}

			if ceiling.Empty() {
				pairs = append(pairs,
					view.Pair{Key: "in force", Value: "nothing — every grant is bounded only by what you type"},
					view.Pair{Key: "note", Value: "a machine whose policy was deleted looks exactly like " +
						"this one. `rta policy require` makes the difference visible"})
				return render(cmd, view.KeyValue{Pairs: pairs}, nil)
			}

			if ceiling.MaxTTL > 0 {
				pairs = append(pairs, view.Pair{Key: "maxTTL", Value: format.Duration(ceiling.MaxTTL)})
			}
			if len(ceiling.Never) > 0 {
				pairs = append(pairs, view.Pair{Key: "never", Value: strings.Join(ceiling.Never, ", ")})
			}
			if len(ceiling.NeverProfile) > 0 {
				pairs = append(pairs, view.Pair{Key: "neverProfile", Value: strings.Join(ceiling.NeverProfile, ", ")})
			}
			if len(ceiling.RequireScope) > 0 {
				pairs = append(pairs, view.Pair{Key: "requireScope", Value: strings.Join(ceiling.RequireScope, ", ")})
			}
			pairs = append(pairs, view.Pair{Key: "requireRepoPolicy", Value: yesNo(ceiling.RequireRepo)})
			if n := grant.Suppressed(); n > 0 {
				pairs = append(pairs, view.Pair{Key: "grants suppressed", Value: fmt.Sprintf(
					"%d stored grant(s) would stand if this ceiling did not forbid them", n)})
			}
			return render(cmd, view.KeyValue{Pairs: pairs}, nil)
		},
	}
}

func repoPolicyText(c policy.Ceiling) string {
	if c.RepoFound {
		return strings.Join(c.From, ", ")
	}
	return "none found walking up from " + c.SearchedFrom
}

func presentText(path string) string {
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return path + " (not present)"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// starterPolicy is what `rta policy init` writes.
//
// Commented rather than minimal, and every axis present but only one active.
// A file somebody has to look up the keys for is a file they write once and
// never tighten; a file that already names its four axes is one they edit.
const starterPolicy = `# The ceiling no grant in this repository may exceed.
#
# This file can only ever SUBTRACT authority. There is no allow key, which is
# why it needs no signature and can be committed like any other file: the
# worst a hostile edit achieves is that rta refuses more than you wanted.
# ` + "`roles:`" + ` below is not one either — a role grants nothing by being
# here; a person issues it, sees its lines first, and the ceiling above caps
# every one of them.
#
# A subdirectory may add its own and tighten this further. It cannot loosen it.

# Cap how long any grant may stand, however long somebody asks for.
maxTTL: 1h

# Targets no grant may name at all. A capability ID, or a plugin name to cover
# all of it.
never: []
#  - pg.dump
#  - vault.snapshot

# Connections no grant may name. The blunt instrument for "not production".
neverProfile: []
#  - production

# Targets that may only be granted against one named record, never wholesale.
# ` + "`rta grant allow kv.get`" + ` covers the entire store; listing it here makes
# that form an error and only ` + "`rta grant allow kv.get one-key`" + ` is accepted.
requireScope: []
#  - kv.get
#  - s3.object.get

# Roles ` + "`rta grant issue <role>`" + ` issues whole, under one passphrase:
# grant lines in the grammar ` + "`rta grant allow`" + ` takes, and how long the
# grants last (12h unless said). Every line is capped by the ceiling above —
# under the 1h maxTTL up there, an 8h role stands for one hour, and
# ` + "`rta grant roles`" + ` says so before you issue it.
# roles:
#   dev:
#     ttl: 8h
#     grants:
#       - kv.get db-password
#       - pg.query --profile staging
#       - note
`

func policyInitCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:               "init",
		Short:             "Write a starter " + policy.RepoFile + " in this directory",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: "Writes a commented " + policy.RepoFile + " with every axis named and one of them" +
			" active, so tightening it later is an edit rather than a trip to the documentation.\n\n" +
			"Commit it. It needs no seal — it can only subtract.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := policy.RepoFile
			if _, err := os.Stat(path); err == nil && !force {
				// Never silently. This file is a security boundary somebody
				// else may have written, and overwriting one because a command
				// was run twice is the kind of help nobody asked for.
				return fmt.Errorf("%s already exists — `rta policy show` says what it does, "+
					"or pass --force to replace it", path)
			}
			if err := atomicfile.Write(path, []byte(starterPolicy), 0o644); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "✓ wrote %s\n", path)
			fmt.Fprintln(out, "  It caps every grant here at 1h. Edit the other three axes and commit it.")
			fmt.Fprintln(out, "  `rta policy require` then makes its absence an error rather than silence.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing "+policy.RepoFile)
	return cmd
}

// policyRequireCommand is the one that closes the gap this whole file exists
// for, and where it writes is the entire point.
//
// The demand goes in the operator's own policy file, never in the repository.
// A repository policy demanding a repository policy is removed along with its
// own demand, so it would be a check that passes exactly when it is not needed.
func policyRequireCommand() *cobra.Command {
	var off bool
	cmd := &cobra.Command{
		Use:               "require",
		Short:             "Refuse to run in a directory with no " + policy.RepoFile,
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: "Records, in your own policy file rather than in any repository, that a" +
			" repository policy must be found. Without it a deleted " + policy.RepoFile + " is" +
			" indistinguishable from a machine that never had one: rta runs with no ceiling" +
			" and nothing anywhere says so.\n\n" +
			"With it, that becomes a refusal naming the directory rta searched from — which" +
			" also catches the case nobody expects, where an MCP client launched rta somewhere" +
			" other than the repository you thought you were protecting.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := operatorPolicyPath()
			if err != nil {
				return fmt.Errorf("locating your config directory: %w", err)
			}
			existing, err := os.ReadFile(path)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("reading %s: %w", path, err)
			}

			updated, changed := setRequireRepo(string(existing), !off)
			if !changed {
				fmt.Fprintf(cmd.OutOrStdout(), "already %s in %s\n", yesNo(!off), path)
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			if err := atomicfile.Write(path, []byte(updated), 0o600); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if off {
				fmt.Fprintf(out, "✓ %s no longer requires a repository policy\n", path)
				return nil
			}
			fmt.Fprintf(out, "✓ %s now requires a %s\n", path, policy.RepoFile)
			ceiling, verr := grant.Ceiling()
			switch {
			case verr != nil:
				fmt.Fprintf(cmd.ErrOrStderr(),
					"  and this directory does not have one yet: %s\n"+
						"  `rta policy init` writes one here.\n", verr.Message)
			case ceiling.RepoFound:
				fmt.Fprintf(out, "  This directory has one: %s\n", strings.Join(ceiling.From, ", "))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&off, "off", false, "stop requiring a repository policy")
	return cmd
}

// setRequireRepo edits the key in place, preserving whatever else the file
// holds — this is the operator's own policy and may carry real constraints.
// Rewriting it through a YAML round-trip would drop their comments, which is
// the same objection rta makes to writing another tool's config file.
func setRequireRepo(body string, want bool) (string, bool) {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "requireRepoPolicy:") {
			continue
		}
		desired := fmt.Sprintf("requireRepoPolicy: %t", want)
		if strings.TrimSpace(line) == desired {
			return body, false
		}
		lines[i] = desired
		return strings.Join(lines, "\n"), true
	}
	if !want {
		// Absent already means not required, so there is nothing to write and
		// nothing to report as a change.
		return body, false
	}
	trimmed := strings.TrimRight(body, "\n")
	if trimmed != "" {
		trimmed += "\n"
	} else {
		trimmed = "# rta's own policy file. Unlike a repository's, this one is yours alone.\n"
	}
	return trimmed + "requireRepoPolicy: true\n", true
}
