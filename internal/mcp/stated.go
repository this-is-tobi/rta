package mcp

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// statedValues are the values the operator stated for the connection one call
// ran against, held to name them by what they are in an error the call hands
// an agent (errResultAs).
//
// **An error is written for the person at a terminal, and says where the
// connection went.** "no address for db.prod.internal", "nothing is listening
// on 10.1.2.3:5432", "Cannot connect to the Docker daemon at unix:///x.sock",
// each with a hint to ask DNS about the name or to probe the port. Over MCP that
// is the operator's network read back one refusal at a time, which is what a
// store entry's name and a cluster's are kept from an agent for (the
// connection that cannot be opened says only that it cannot), and what the
// data and config directories are named by their place instead of their path
// for (operatorPaths). The inputs a plugin declares Local are the connection:
// host, port, address, region, namespace, a path. A caller may not supply one,
// so any in an error was stated by the operator's config or a profile, and the
// agent is told which, by the profile when a profile is in play and by the
// input's name otherwise. The record keeps the error as it was written, and so
// does every surface a person is at.
//
// What is not masked, so that the error still reads: a default, which is
// nobody's statement, an input that takes one of a closed set of words, a
// switch, and a number but as the port of an address (a bare 5432 in a
// sentence is not told from any other number). A host in the URL a Local value
// is written as is masked too, since that is how an error repeats it.
type statedValues struct {
	// entries are the texts to name, the longest first so that an address is
	// named whole before the host inside it is.
	entries []statedText
	// again is how a sentence that already names the profile beside the address
	// (plugin.Request.Reached) reads once the address is named by it, and what
	// it is changed to: the profile once, not twice.
	again, once string
}

type statedText struct{ text, label string }

// statedFor is what the operator stated for the connection run reached, or nil
// for a call that stated nothing.
func statedFor(c plugin.Capability, run plugin.Request) *statedValues {
	stated := run.OperatorStated(c)
	if len(stated) == 0 {
		return nil
	}
	profile := run.Profile()
	label := func(input string) string {
		if profile != "" {
			return "profile " + strconv.Quote(profile)
		}
		return "<" + input + ">"
	}
	var out []statedText
	var hosts []statedText
	var ports []statedText
	add := func(into *[]statedText, text, input string) {
		// Not a value that is only punctuation: a dot or a slash is a word of
		// no sentence, and naming every one would rewrite the error around it.
		if text = textclean.Model(text); strings.ContainsFunc(text, func(r rune) bool {
			return unicode.IsLetter(r) || unicode.IsDigit(r)
		}) {
			*into = append(*into, statedText{text, label(input)})
		}
	}
	for _, f := range c.Inputs {
		v, ok := stated[f.Name]
		if !ok || f.Type == plugin.Bool || len(f.Options) > 0 {
			continue
		}
		switch t := v.(type) {
		case string:
			add(&out, t, f.Name)
			add(&hosts, t, f.Name)
			if u, err := url.Parse(t); err == nil && strings.Contains(t, "://") && u.Hostname() != "" {
				add(&out, u.Hostname(), f.Name)
				add(&hosts, u.Hostname(), f.Name)
				if u.Port() != "" {
					add(&out, u.Hostname()+":"+u.Port(), f.Name)
				}
			}
		case []string:
			for _, e := range t {
				add(&out, e, f.Name)
				add(&hosts, e, f.Name)
			}
		case int, int64, uint64:
			if f.Type == plugin.Int {
				add(&ports, fmt.Sprint(t), f.Name)
			}
		}
	}
	// A port is named with the host it belongs to, as an error spells an
	// address, and not on its own.
	for _, h := range hosts {
		for _, p := range ports {
			joined := h.label
			if p.label != h.label {
				joined += ":" + p.label
			}
			out = append(out, statedText{h.text + ":" + p.text, joined})
		}
	}
	// And a value in the quotation marks an error puts it in is named with
	// them, so that no pair of quotes is left around the name.
	for _, e := range out {
		out = append(out, statedText{`"` + e.text + `"`, e.label}, statedText{"'" + e.text + "'", e.label})
	}
	if len(out) == 0 {
		return nil
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].text) > len(out[j].text) })
	st := &statedValues{entries: out}
	if profile != "" {
		st.once = label("")
		st.again = st.once + " (profile " + profile + ")"
	}
	return st
}

// mask is s with each stated value named by what it is, where it stands as a
// word of its own (the rule that names a path, replacePath) and in one pass,
// so that a name written in place of a value is never read as a value itself.
func (s *statedValues) mask(text string) string {
	if s == nil {
		return text
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		named := false
		for _, e := range s.entries {
			if strings.HasPrefix(text[i:], e.text) && startsAPath(text[:i]) && endsAPath(text[i+len(e.text):]) {
				out.WriteString(e.label)
				i += len(e.text)
				named = true
				break
			}
		}
		if !named {
			out.WriteByte(text[i])
			i++
		}
	}
	if s.again == "" {
		return out.String()
	}
	return strings.ReplaceAll(out.String(), s.again, s.once)
}

// probes reports whether hint spells a call that names a stated value: the
// `net_dns {"name":"db.prod.internal"}` that shows what DNS returns for it, the
// port check that dials it. Such a hint is dropped from what an agent is told
// and not rewritten, since the call it suggests would be one on a name that is
// the profile's, and the agent has no use for the sentence around it.
func (s *statedValues) probes(hint string) bool {
	if s == nil {
		return false
	}
	spans := strings.Split(textclean.Model(hint), "`")
	for i := 1; i < len(spans); i += 2 {
		for _, e := range s.entries {
			if strings.Contains(spans[i], e.text) {
				return true
			}
		}
	}
	return false
}

// agentError is e as an agent is to be told it: with the hint dropped when it
// is a probe of what the operator stated. The values in what remains are named
// when the error is written out (errResultAs).
func (s *statedValues) agentError(e *view.Error) *view.Error {
	if e == nil || e.Hint == "" || !s.probes(e.Hint) {
		return e
	}
	out := *e
	out.Hint = ""
	return &out
}
