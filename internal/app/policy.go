package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
//
// There is none when the config file is the working-directory fallback, and
// that is said with the fix: a demand written into the directory being
// protected is the demand a deleted repository takes with it.
func operatorPolicyPath() (string, *view.Error) {
	if p := policy.OperatorPath(); p != "" {
		return p, nil
	}
	return "", view.Errorf("core.policy.path",
		"there is no config directory of your own to keep the policy file in").
		WithHint("set $RTA_CONFIG to the config file you mean, by its full path — the policy " +
			"file goes beside it, outside every repository")
}

func newPolicyCommand(opts *globalOpts) *cobra.Command {
	render := viewRender(opts, false)

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
	cmd.AddCommand(policyInitCommand(opts))
	cmd.AddCommand(policyRequireCommand(opts))
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
		Annotations:       readOnly(),
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

			pairs = append(pairs, axisPairs(ceiling)...)
			pairs = append(pairs, view.Pair{Key: "requireRepoPolicy", Value: yesNo(ceiling.RequireRepo)})
			if n := grant.Suppressed(); n > 0 {
				pairs = append(pairs, view.Pair{Key: "grants suppressed", Value: fmt.Sprintf(
					"%s if this ceiling did not forbid %s",
					format.Count(n, "stored grant would stand", "stored grants would stand"),
					format.Plural(n, "it", "them"))})
			}
			return render(cmd, view.KeyValue{Pairs: pairs}, nil)
		},
	}
}

// axisPairs is the axes of a ceiling that bound something, one pair each: what
// `policy show` reports as in force, and what `policy init --force` shows of
// the file it is asked to replace.
func axisPairs(c policy.Ceiling) []view.Pair {
	var pairs []view.Pair
	if c.MaxTTL > 0 {
		pairs = append(pairs, view.Pair{Key: "maxTTL", Value: format.Duration(c.MaxTTL)})
	}
	if len(c.Never) > 0 {
		pairs = append(pairs, view.Pair{Key: "never", Value: strings.Join(c.Never, ", ")})
	}
	if len(c.NeverProfile) > 0 {
		pairs = append(pairs, view.Pair{Key: "neverProfile", Value: strings.Join(c.NeverProfile, ", ")})
	}
	if len(c.RequireScope) > 0 {
		pairs = append(pairs, view.Pair{Key: "requireScope", Value: strings.Join(c.RequireScope, ", ")})
	}
	return pairs
}

func repoPolicyText(c policy.Ceiling) string {
	if len(c.Repo) > 0 {
		return strings.Join(c.Repo, ", ")
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
maxTTL: ` + starterTTL + `

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

func policyInitCommand(opts *globalOpts) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:               "init",
		Short:             "Write a starter " + policy.RepoFile + " in this directory",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		Long: "Writes a commented " + policy.RepoFile + " with every axis named and one of them" +
			" active, so tightening it later is an edit rather than a trip to the documentation.\n\n" +
			"Commit it. It needs no seal — it can only subtract.\n\n" +
			"--force replaces a file that is there, and never with a looser ceiling than the one it" +
			" holds: a file whose cap, never, neverProfile or requireScope the starter would loosen is" +
			" refused, and loosening one on purpose is an edit of the file. Otherwise it shows what the" +
			" file holds and asks; with no terminal to ask at, --yes answers for a script.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := policy.RepoFile
			_, statErr := os.Stat(path)
			exists := statErr == nil
			if exists && !force {
				// Never silently. This file is a security boundary somebody
				// else may have written, and overwriting one because a command
				// was run twice is the kind of help nobody asked for. The way
				// on is to edit it: the hint used to offer --force, which
				// replaced a team's ceiling with the starter and asked nothing.
				return view.Errorf("core.policy.exists", "%s already exists", path).
					WithHint("`rta policy show` says what it does; edit the file to change it, since " +
						"this command only writes a starter")
			}
			if exists {
				if err := confirmReplacement(cmd, opts, path); err != nil {
					return err
				}
			}
			if !opts.dryRun {
				if err := atomicfile.Write(path, []byte(starterPolicy), 0o644); err != nil {
					return view.Errorf("core.policy.write", "writing %s: %v", path, err)
				}
			}
			return renderView(cmd, opts, policyInitAnswer(path, exists, opts.dryRun))
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"replace an existing "+policy.RepoFile+" with the starter, unless that would loosen it, after asking")
	return cmd
}

// confirmReplacement decides whether `policy init --force` may replace the
// policy file at path, and asks a person when only a person can say.
//
// **A ceiling is replaced by one no looser than it, or not at all.** The
// starter bounds one axis and leaves the other three empty, and the people
// who run `--force` are the ones with a ceiling worth keeping: a team that
// wrote maxTTL 5m and `never: [pg.dump]` and typed the flag because the
// refusal offered it lost both, with no question and no word. So the starter
// is compared with what stands first (policy.Loosens), and a file it would
// loosen is refused whoever answers, a person at a terminal and a script
// passing --yes alike. Loosening a ceiling is a decision, and it is made by
// editing the file.
//
// What is left, a file the starter keeps every bound of, is still somebody's
// file, and the comments and roles written in it go with it. A person at a
// terminal is shown what stands and asked; with nobody to ask, the flag alone
// refuses and --yes answers for a script, as it does for every other
// confirmation. A file that does not read is the exception: nothing says what
// it holds, and a key misspelt (`nevr:`) is how a bound goes missing while
// the file still looks like one, so only a person's yes replaces it. The
// starter itself is replaced without a word, as there is nothing in it to
// lose.
func confirmReplacement(cmd *cobra.Command, opts *globalOpts, path string) error {
	data, err := atomicfile.ReadCapped(path, 1<<20)
	if err != nil {
		return view.Errorf("core.policy.read", "reading %s: %v", path, err)
	}
	if string(data) == starterPolicy {
		return nil
	}
	starter, verr := policy.Parse(policy.RepoFile, []byte(starterPolicy))
	if verr != nil {
		return verr
	}
	standing, unreadable := policy.Parse(path, data)
	if unreadable == nil {
		if loosened := policy.Loosens(standing, starter); len(loosened) > 0 {
			return view.Errorf("core.policy.loosens", "the starter would loosen the ceiling in %s: %s",
				path, strings.Join(loosened, "; ")).
				WithHint("this command writes a starter and never a looser ceiling than the one that " +
					"stands — to loosen one on purpose, edit the file")
		}
		if opts.yes {
			return nil
		}
	}
	if !confirmTerminal() {
		if unreadable != nil {
			return view.Errorf(CodeConfirmRequired,
				"%s does not read, so there is no telling what it holds, and replacing it asks a person", path).
				WithHint("fix what it names, or remove the file and run this again — --yes does not " +
					"answer for a file nobody could read")
		}
		return view.Errorf(CodeConfirmRequired,
			"%s is not the starter, and replacing it asks first; there is no terminal to ask at", path).
			WithHint("`rta policy show` says what it holds, and `--force --yes` replaces it without " +
				"asking")
	}
	if opts.dryRun {
		return nil
	}
	w := cmd.ErrOrStderr()
	_ = cli.Render(w, view.KeyValue{Pairs: standingPairs(path, standing, unreadable)},
		renderOptions(cmd, cli.Pretty, opts.noColor))
	if !askYes(cmd.Context(), w, "Replace it with the starter (maxTTL "+starterTTL+", the other axes empty)? [y/N] ") {
		return declinedConfirmation(w)
	}
	return nil
}

// standingPairs is what the file --force is asked to replace holds, as the
// question shows it: the axes that bound something, and the roles that go
// with the file; or why it does not read.
func standingPairs(path string, standing policy.Ceiling, unreadable *view.Error) []view.Pair {
	pairs := []view.Pair{{Key: "file", Value: path}}
	if unreadable != nil {
		return append(pairs, view.Pair{Key: "does not read", Value: unreadable.Message})
	}
	axes := axisPairs(standing)
	if len(axes) == 0 {
		axes = []view.Pair{{Key: "bounds", Value: "nothing"}}
	}
	pairs = append(pairs, axes...)
	if len(standing.Roles) > 0 {
		names := make([]string, 0, len(standing.Roles))
		for _, r := range standing.Roles {
			names = append(names, r.Name)
		}
		slices.Sort(names)
		pairs = append(pairs, view.Pair{Key: "roles", Value: strings.Join(names, ", ") + " — replaced with the file"})
	}
	return pairs
}

// starterTTL is the one axis starterPolicy sets, named once so the file and
// the answer that states it cannot disagree about it. The roles comment in the
// file does the arithmetic for 1h and is rewritten with it.
const starterTTL = "1h"

// policyInitAnswer is what policy init answers: the file, by its full path,
// the one bound it puts in force, and what to do with it next.
//
// The full path because the file is written into whatever directory the
// command ran in, and a script setting a repository up reads where it landed
// from here; the bare name was the one thing the answer could not tell it. A
// file --force replaced says so, since what it replaced was somebody's
// ceiling.
func policyInitAnswer(path string, replaced, dryRun bool) view.KeyValue {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	label := "wrote"
	switch {
	case dryRun && replaced:
		label = "would replace"
	case dryRun:
		label = "would write"
	case replaced:
		label = "replaced"
	}
	next := "edit the other three axes and commit it — `rta policy require` then makes " +
		"its absence an error rather than silence"
	if dryRun {
		next = "run without --dry-run to write it"
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: label, Value: path},
		{Key: "ceiling", Value: "maxTTL " + starterTTL + " — no grant made under this directory " +
			"stands longer, whatever it asks for; never, neverProfile and requireScope are named and empty"},
		{Key: "next", Value: next},
	}}
}

// policyRequireCommand is the one that closes the gap this whole file exists
// for, and where it writes is the entire point.
//
// The demand goes in the operator's own policy file, never in the repository.
// A repository policy demanding a repository policy is removed along with its
// own demand, so it would be a check that passes exactly when it is not needed.
func policyRequireCommand(opts *globalOpts) *cobra.Command {
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
			path, verr := operatorPolicyPath()
			if verr != nil {
				return verr
			}
			// Opened without waiting and read under a cap, as the policy is
			// everywhere else it is read: a named pipe where the file goes
			// would otherwise hold this command for good.
			existing, err := atomicfile.ReadCapped(path, 1<<20)
			if err != nil && !os.IsNotExist(err) {
				return view.Errorf("core.policy.read", "reading %s: %v", path, err)
			}

			updated, changed := setRequireRepo(string(existing), !off)
			if changed && !opts.dryRun {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					return view.Errorf("core.policy.write", "creating %s: %v", filepath.Dir(path), err)
				}
				if err := atomicfile.Write(path, []byte(updated), 0o600); err != nil {
					return view.Errorf("core.policy.write", "writing %s: %v", path, err)
				}
			}
			return renderView(cmd, opts, policyRequireAnswer(path, !off, changed, opts.dryRun))
		},
	}
	cmd.Flags().BoolVar(&off, "off", false, "stop requiring a repository policy")
	return cmd
}

// policyRequireAnswer is what policy require answers: the file, the setting
// it now holds, whether the directory the command ran in meets the
// requirement once it stands, and what to do next.
//
// The file under the same key policy init puts its own under, so a script
// setting a machine up reads where each landed the same way; one that already
// said so is "unchanged", in the words profile set and dashboard hide answer a
// write that would change nothing with.
//
// The directory is a pair of the answer rather than a line beside it. Half of
// it went to stderr — the half saying the directory had no policy, which is
// the thing somebody turning the requirement on most needs to hear — and a
// script doing it across machines reads it from -o json or not at all. A dry
// run that would turn it on leaves it out: the requirement is not in force
// yet, so the ceiling it would be read from has not checked it.
func policyRequireAnswer(path string, on, changed, dryRun bool) view.KeyValue {
	var pairs []view.Pair
	switch {
	case !changed && on:
		pairs = append(pairs, view.Pair{Key: "unchanged",
			Value: "a repository policy is already required — nothing written to " + path})
	case !changed:
		pairs = append(pairs, view.Pair{Key: "unchanged",
			Value: "a repository policy is already not required — nothing written to " + path})
	case dryRun:
		pairs = append(pairs, view.Pair{Key: "would write", Value: path})
	default:
		pairs = append(pairs, view.Pair{Key: "wrote", Value: path})
	}
	// yes or no, as policy show's row of the same name reads.
	pairs = append(pairs, view.Pair{Key: "requireRepoPolicy", Value: yesNo(on)})
	next := "`rta policy show` says what ceiling a directory with no " + policy.RepoFile + " runs under"
	switch {
	case dryRun && changed:
		next = "run without --dry-run to write it"
	case on:
		var here string
		here, next = repoPolicyHere()
		pairs = append(pairs, view.Pair{Key: "this directory", Value: here})
	}
	return view.KeyValue{Pairs: append(pairs, view.Pair{Key: "next", Value: next})}
}

// repoPolicyHere is whether the working directory meets a requirement that
// stands, read from the ceiling as every later command will read it, and what
// to do about it. Refused, the refusal's own message is the answer: no file,
// a file that constrains nothing, or a policy file that does not parse.
func repoPolicyHere() (here, next string) {
	ceiling, verr := grant.Ceiling()
	switch {
	case verr != nil && verr.Code == "policy.repo.missing":
		return verr.Message, "`rta policy init` writes one here"
	case verr != nil:
		return verr.Message, verr.Hint
	case len(ceiling.Repo) > 0:
		return "has one: " + strings.Join(ceiling.Repo, ", "),
			"a directory without one is refused, naming the directory rta searched from"
	}
	return "no " + policy.RepoFile + " found from " + ceiling.SearchedFrom,
		"`rta policy init` writes one here"
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
