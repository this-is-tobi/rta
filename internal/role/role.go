// Package role is what `rta grant issue` issues: a named list of grant lines, in
// the grammar `rta grant allow` already parses, with a default duration.
//
// A role adds no authority. Nothing is granted by a role existing in a
// file; a person at a terminal issues it with `rta grant issue`, under
// the guard's passphrase where there is one, and every grant it produces
// passes the team ceiling exactly as a typed one does. What a role saves is
// the typing and the prompts: a day's grants under one word, and one
// passphrase instead of one per line.
//
// Two files may hold roles. The team's, in the policy file beside the
// ceiling it is bounded by, and the operator's own, in their config. The
// same name in both is refused at use rather than resolved by precedence:
// an issue that silently took the other file's version of "dev" is the
// kind of surprise a permission system must not spring.
package role

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/pflag"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/policy"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Line is one grant a role issues.
type Line struct {
	Raw     string
	Target  string
	Scope   string
	Profile string
	TTL     string
	Note    string
	Rate    string
	MaxUses int
}

// Parse reads one role line: a target, an optional record, and the flags
// `grant allow` takes for the grant itself. What it does not take is who
// the grant is for — `--agent` is given once, at issue — and where it
// is issued — `--server` is the operator channel's, and a role is local.
func Parse(raw string) (Line, error) {
	words := strings.Fields(raw)
	if len(words) == 0 {
		return Line{}, fmt.Errorf("an empty line names nothing to grant")
	}
	l := Line{Raw: strings.TrimSpace(raw)}
	fs := pflag.NewFlagSet("role", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&l.Profile, "profile", "", "")
	fs.StringVar(&l.TTL, "ttl", "", "")
	fs.StringVar(&l.Note, "note", "", "")
	fs.StringVar(&l.Rate, "rate", "", "")
	fs.IntVar(&l.MaxUses, "max-uses", 0, "")
	if err := fs.Parse(words); err != nil {
		return Line{}, fmt.Errorf("%q: %v — a role line is a target, an optional record, and "+
			"--profile, --ttl, --max-uses, --rate or --note; the agent is given at issue", l.Raw, err)
	}
	args := fs.Args()
	switch {
	case len(args) == 0:
		return Line{}, fmt.Errorf("%q names no target", l.Raw)
	case len(args) > 2:
		return Line{}, fmt.Errorf("%q: more than a target and a record — %v", l.Raw, args)
	}
	l.Target = args[0]
	if len(args) == 2 {
		l.Scope = args[1]
	}
	return l, nil
}

// Source is one role and where it was read from.
type Source struct {
	Name string
	Role config.Role
	// From is the file. Team says it is a repository's — found by the walk
	// up from the working directory, written by somebody who may not be
	// the operator — rather than the operator's own config, policy file,
	// or a file they named with RTA_POLICY.
	From string
	Team bool
}

// Lines parses every line of the role, naming the first that does not.
func (s Source) Lines() ([]Line, error) {
	if len(s.Role.Grants) == 0 {
		return nil, fmt.Errorf("role %q in %s grants nothing", s.Name, s.From)
	}
	out := make([]Line, 0, len(s.Role.Grants))
	for _, raw := range s.Role.Grants {
		l, err := Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("role %q in %s: %v", s.Name, s.From, err)
		}
		out = append(out, l)
	}
	return out, nil
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Available lists every role this machine can issue: the team's from the
// policy files, then the operator's own from their config, by name. A name
// that appears twice is listed twice; Find is what refuses it.
func Available() ([]Source, *view.Error) {
	ceiling, verr := policy.Load()
	if verr != nil {
		return nil, verr
	}
	var out []Source
	for _, r := range ceiling.Roles {
		out = append(out, Source{Name: r.Name, Role: r.Role, From: r.From, Team: r.Team})
	}
	cfg, err := config.Load()
	if err != nil {
		return nil, view.AsError(err, "role.config")
	}
	// Only from a config somebody named. The ./.rta.yaml fallback is a
	// file a cloned repository could carry, and a role from it would be
	// treated as the operator's own — the one place the team-role rule
	// does not apply. Profiles, plugins and the dashboard already refuse
	// that file; roles are the fourth block, not the exception.
	if cfg.Trusted() {
		for name, r := range cfg.Roles {
			out = append(out, Source{Name: name, Role: r, From: config.Path()})
		}
	}
	for _, s := range out {
		if !nameRe.MatchString(s.Name) {
			return nil, view.Errorf("role.badname", "%s names a role %q; a role name is lowercase letters, digits and dashes", s.From, s.Name)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].From < out[j].From
	})
	return out, nil
}

// Find is the one role `grant issue` issues under this name.
func Find(name string) (Source, *view.Error) {
	all, verr := Available()
	if verr != nil {
		return Source{}, verr
	}
	var hits []Source
	for _, s := range all {
		if s.Name == name {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return Source{}, view.Errorf("role.unknown", "no role named %q", name).
			WithHint("`rta grant roles` lists what this machine can issue; a role is a `roles:` entry " +
				"in " + policy.RepoFile + ", in your policy file, or in your config")
	default:
		files := make([]string, 0, len(hits))
		for _, h := range hits {
			files = append(files, h.From)
		}
		return Source{}, view.Errorf("role.ambiguous", "%q is defined in more than one file: %s",
			name, strings.Join(files, ", ")).
			WithHint("`grant issue` would have to pick one — rename one of them")
	}
}
