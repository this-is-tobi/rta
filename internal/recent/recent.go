// Package recent remembers what an operator supplied, so a completion can
// offer it back.
//
// It exists because the inputs people most want completed are the ones nothing
// can enumerate cheaply or safely: a bucket name, a database, a schema, a
// vault path. Asking the service is the obvious answer and it is the wrong one
// here, for reasons the codebase already established rather than reasons
// invented for this package:
//
//   - **The credential is deliberately not there.** Completion resolves a
//     profile through `profile.Bind`, never `Fill`, because fetching a
//     `secrets:` reference means unlocking an age store — about a second of
//     scrypt, and possibly a passphrase prompt in the middle of a half-typed
//     command line. So a live listing would authenticate with nothing for
//     exactly the operators who use profiles, which is who profiles are for.
//   - **A keystroke is a stricter budget than a dashboard tick, not a looser
//     one.** `plugins/pg` sets NoPreview on every capability so rta never
//     polls a database it was not told it may poll; opening one on tab is the
//     same guess with a shorter fuse. What that costs was measured
//     through a `kubectl port-forward`: PostgreSQL's default `sslmode:
//     prefer` kills the forward on a *clean disconnect*, so a tab press would
//     take the operator's tunnel down.
//   - **No cache could help where it matters.** Every `rta __complete` is a
//     fresh process tree, so a shell — where people actually press tab — pays
//     the full cost every time however clever the caching is.
//
// What is left is what the machine already knows: the values this person
// actually used. That works for every input of every plugin, including ones
// nobody has written yet, with no network, no credential and no plugin change.
//
// **Never written by an agent.** Only the CLI and the TUI record, and the rule
// is enforced on the surface rather than on the caller. An MCP call whose
// values became the operator's suggestions would be an agent choosing what a
// person is offered next, which is a quiet way to be steered.
package recent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
	"github.com/this-is-tobi/rule-them-all/internal/paths"
	"github.com/this-is-tobi/rule-them-all/internal/textclean"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

const (
	// perInput is how many values are kept for one input. Enough for the
	// handful of buckets or databases somebody moves between, short enough
	// that the list is still a shortlist rather than a history to read.
	perInput = 8
	// maxInputs bounds the whole file, so a long-lived installation cannot
	// grow one without anybody deciding to. Oldest-touched keys go first.
	maxInputs = 200
	// maxValue is the longest value worth offering. A completion list is one
	// line per entry; a SQL statement or a note body is neither completable
	// nor something to write to disk behind somebody's back.
	maxValue = 128
)

// Values is what has been used, keyed by "<namespace>.<input>", most recent
// first.
//
// Keyed by namespace rather than by capability, because `s3 object list
// --bucket` and `s3 object get --bucket` name the same bucket — the same
// assumption `Field.Config` already makes when one plugin's capabilities share
// a config key.
type Values map[string][]string

// file is the on-disk shape. A separate type so the format can gain a field
// without every reader having to care.
type file struct {
	// Inputs is the shortlist per key.
	Inputs Values `json:"inputs,omitempty"`
	// Order is the keys by last touch, oldest first — what pruning needs and
	// what a map cannot hold.
	Order []string `json:"order,omitempty"`
}

// Path is where the shortlists are kept.
func Path() string { return filepath.Join(paths.Data(), "recent.json") }

// Load reads the shortlists, or an empty set.
//
// Every failure answers empty. This is a convenience; a data directory that
// cannot be read should cost somebody a suggestion, not a command.
func Load() Values {
	var f file
	data, err := os.ReadFile(Path())
	if err != nil {
		return Values{}
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return Values{}
	}
	if f.Inputs == nil {
		return Values{}
	}
	return f.Inputs
}

// For is what has been used for one input of one capability, cleaned of
// anything that has since become unusable.
func (v Values) For(capID, input string) []string {
	return v[Key(capID, input)]
}

// Key is the shortlist a capability's input reads and writes.
func Key(capID, input string) string {
	return plugin.Namespace(capID) + "." + input
}

// Record files away what a run was given, for the inputs worth offering back.
//
// values is what the caller actually supplied, never the resolved request: a
// declared default is not a choice anybody made, and a value the environment
// filled in is already offered by the environment. Only called after a run
// succeeded — a bucket that does not exist is not a suggestion.
//
// Silent on every failure, and a no-op when nothing changed, which is what
// keeps a repeated command from rewriting the file on every invocation.
func Record(surface plugin.Surface, c plugin.Capability, values map[string]any) {
	if !records(surface) {
		return
	}
	fresh := offerable(c, values)
	if len(fresh) == 0 {
		return
	}
	mu.Lock()
	defer mu.Unlock()

	var f file
	if data, err := os.ReadFile(Path()); err == nil {
		_ = json.Unmarshal(data, &f)
	}
	if f.Inputs == nil {
		f.Inputs = Values{}
	}
	changed := false
	for _, key := range sortedKeys(fresh) {
		for _, value := range fresh[key] {
			if promote(f.Inputs, key, value) {
				changed = true
				f.Order = touch(f.Order, key)
			}
		}
	}
	if !changed {
		return
	}
	prune(&f)
	data, err := json.Marshal(f)
	if err != nil {
		return
	}
	if err := os.MkdirAll(paths.Data(), 0o755); err != nil {
		return
	}
	// 0600, like the selection and the grant file. It names the buckets,
	// databases and hosts somebody works with, which is nobody else's
	// business on a shared machine — the same class of thing a shell history
	// holds, and kept the same way.
	_ = atomicfile.Write(Path(), data, 0o600)
}

// mu serialises this process's own writes. Two rta processes finishing at once
// still race for the file and the later one wins the whole of it, which is the
// right trade for a convenience: the alternative is a lock file in the way of
// every command, to protect a shortlist.
var mu sync.Mutex

// records reports whether a surface may write here.
//
// A closed set rather than "not MCP", so a surface added later has to be named
// before it can steer what a person is offered.
func records(s plugin.Surface) bool {
	return s == plugin.SurfaceCLI || s == plugin.SurfaceTUI
}

// offerable is the subset of a run's values worth keeping, keyed for storage.
func offerable(c plugin.Capability, values map[string]any) map[string][]string {
	out := map[string][]string{}
	for _, f := range c.Inputs {
		v, given := values[f.Name]
		if !given || !worthOffering(f) {
			continue
		}
		for _, raw := range entries(v) {
			// Refused rather than cleaned, and the difference matters here.
			// These strings come back out onto a completion list, where one
			// keystroke accepts one as an input — so a cleaned version would
			// hand somebody a value that is not the one that worked. Anything
			// that would display as something other than what it is simply
			// does not get remembered.
			s := strings.TrimSpace(raw)
			if s == "" || len(s) > maxValue || textclean.Deceives(s) || authorizationHeader(s) {
				continue
			}
			key := Key(c.ID, f.Name)
			out[key] = append(out[key], s)
		}
	}
	return out
}

// worthOffering decides by declared type, not by inspecting the value.
//
// Secret is the one that matters and it is refused outright: a credential must
// not reach a file rta writes for convenience, whatever else is true about it.
// Text is a body written in an editor and Bool is a question with two answers,
// neither of which anybody completes.
func worthOffering(f plugin.Field) bool {
	switch f.Type {
	case plugin.Secret, plugin.Text, plugin.Bool:
		return false
	}
	if credentialName(f.Name) || credentialName(f.Config) {
		return false
	}
	// A closed set is already offered in full, and offering three of its
	// members again above the list is noise.
	return len(f.Options) == 0
}

// credentialWords are the names a field carries when it holds a credential,
// whatever type its author declared it as.
//
// A backstop, not the rule. `Type: Secret` is the rule, and it is what keeps a
// value out of a form's plain box as well as out of this file — but a
// declaration can be wrong, and this one was: builtin/http declared `bearer`
// and `basic` as plain Strings, so a token went to disk and came back on a
// completion list rendered in the clear. That is fixed at the declaration;
// this is here because the next plugin to get it wrong will not be one this
// repo can edit.
//
// Being wrong in this direction costs a suggestion nobody sees, which is why a
// name heuristic is acceptable here and would not be anywhere a value is
// actually gated.
var credentialWords = []string{
	"secret", "token", "password", "passwd", "credential", "auth", "bearer", "apikey",
}

func credentialName(name string) bool {
	if name == "" {
		return false
	}
	lower := strings.ToLower(name)
	for _, w := range credentialWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// authorizationHeader reports whether a value is an Authorization header,
// whatever field it arrived on.
//
// The one shape that is a credential no matter what the input is called:
// `http -H 'Authorization: Bearer …'` goes on a header list whose name says
// nothing, beside header names that are worth remembering. Narrow on purpose —
// this is not an attempt to recognise a secret by looking at it, which is not
// a thing that works.
func authorizationHeader(v string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(v)), "authorization:")
}

// entries renders one supplied value as the strings to remember.
func entries(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []string:
		return t
	case nil:
		return nil
	default:
		return []string{fmt.Sprint(t)}
	}
}

// promote moves value to the front of its shortlist and reports whether that
// changed anything — which it does not when it was already there.
func promote(inputs Values, key, value string) bool {
	list := inputs[key]
	if len(list) > 0 && list[0] == value {
		return false
	}
	kept := make([]string, 0, perInput)
	kept = append(kept, value)
	for _, existing := range list {
		if existing == value || len(kept) == perInput {
			continue
		}
		kept = append(kept, existing)
	}
	inputs[key] = kept
	return true
}

// touch moves key to the end of the recency order.
func touch(order []string, key string) []string {
	out := make([]string, 0, len(order)+1)
	for _, k := range order {
		if k != key {
			out = append(out, k)
		}
	}
	return append(out, key)
}

// prune drops the least recently touched keys once the file is too wide.
func prune(f *file) {
	// Anything in the map but not in the order is from a file written before
	// the order existed, or by a writer that lost a race. Ordered first so
	// pruning has something to go on.
	known := map[string]bool{}
	for _, k := range f.Order {
		known[k] = true
	}
	for _, k := range sortedInputs(f.Inputs) {
		if !known[k] {
			f.Order = append([]string{k}, f.Order...)
		}
	}
	for len(f.Order) > maxInputs {
		delete(f.Inputs, f.Order[0])
		f.Order = f.Order[1:]
	}
	for k := range f.Inputs {
		if !contains(f.Order, k) {
			delete(f.Inputs, k)
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedInputs(m Values) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
