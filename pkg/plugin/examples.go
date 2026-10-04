package plugin

import (
	"fmt"
	"maps"
	"slices"
)

// Example is one call of a capability worth showing beside its declaration:
// what a person pastes first.
//
// **Declared as values, not as a command line.** The same example is shown at
// a terminal as `rta sys ps --sort mem --limit 5`, to an agent as the sys_ps
// tool and its arguments, and in the TUI as a filled-in form, and one string
// cannot be all three — the plugin would write the one it knows, and the
// other readers would be told to type a flag they do not have (the reason
// Summary and Description may not spell one). Declared as the inputs it
// gives, each surface spells it the way its reader makes the call
// (Capability.ExampleCall), and Validate holds it to the capability it
// belongs to: an example naming a flag that was renamed is a declaration that
// fails to load, not a line in a help page that nobody tries until a reader
// does.
type Example struct {
	// Title says what the call is for, in a few words: "the five biggest
	// processes". One line, shown beside the call it describes.
	Title string
	// Inputs are the values the call gives, by input name, in the shapes a
	// declared Default takes. An input the call leaves out is left to its
	// default, so an example gives the ones that make it the example.
	Inputs map[string]any
}

// maxExamples bounds a capability's examples. They are shown whole in
// `--help` and `rta explain`, where a screenful of calls teaches less than
// the two or three that cover the ways the capability is used.
const maxExamples = 4

// ExampleArgs is e as the inputs a call is spelled from (Surface.Call): the
// ones the command line takes by their place first, in the order it takes them
// (Arguments), then the rest in the order the capability declares them — one
// spelling for a given example, whatever order its map is read in.
func (c Capability) ExampleArgs(e Example) []Arg {
	var out []Arg
	for _, f := range c.Arguments() {
		if v, given := e.Inputs[f.Name]; given {
			out = append(out, Arg{Name: f.Name, Value: v, Positional: true})
		}
	}
	for _, f := range c.Inputs {
		if v, given := e.Inputs[f.Name]; given && !f.Positional {
			out = append(out, Arg{Name: f.Name, Value: v})
		}
	}
	return out
}

// ExampleCall spells e the way a caller on s makes it: the command line at a
// terminal, the tool and its arguments to an agent, the filled-in form in the
// TUI.
func (c Capability) ExampleCall(s Surface, e Example) string {
	return s.Call(c.ID, c.ExampleArgs(e)...)
}

// checkExamples holds a capability's examples to the capability.
//
// An example is the one piece of declared text that names inputs, so it is the
// piece that goes stale quietly: rename a flag and every help page still shows
// the old one. Each rule below is a way that happens.
//
// Refused: an input that does not exist; a Local one, which is the operator's
// to give and which an agent's tool does not have, so the example would be a
// call only some readers could make; a credential, which would be printed in
// every help page and every tool listing in the clear; a value the input would
// refuse (its type, its options, its range); and a call that leaves out an
// input the capability cannot run without.
func checkExamples(c Capability) error {
	if len(c.Examples) > maxExamples {
		return fmt.Errorf("capability %q declares %d examples, want at most %d; a help page teaches by the "+
			"few that cover how it is used", c.ID, len(c.Examples), maxExamples)
	}
	for i, e := range c.Examples {
		where := fmt.Sprintf("capability %q: example %d", c.ID, i+1)
		if e.Title == "" {
			return fmt.Errorf("%s has no title; say what the call is for, in a few words", where)
		}
		if err := checkLine(where+" title", e.Title, maxSummary); err != nil {
			return err
		}
		for _, name := range slices.Sorted(maps.Keys(e.Inputs)) {
			f, ok := inputOf(c, name)
			switch {
			case !ok:
				return fmt.Errorf("%s (%q) gives input %q, which the capability does not declare",
					where, e.Title, name)
			case f.Local:
				return fmt.Errorf("%s (%q) gives input %q, which is Local: it is the operator's to give and an "+
					"agent's tool does not have it, so the example would be a call only some readers could make",
					where, e.Title, name)
			case f.Type.Sensitive():
				return fmt.Errorf("%s (%q) gives input %q, a %s: an example is printed in every help page and "+
					"tool listing, in the clear", where, e.Title, name, f.Type)
			}
			v := e.Inputs[name]
			if problem, hint := StatedTypeProblem(f, v); problem != "" {
				return fmt.Errorf("%s (%q) gives input %q a value that %s; %s", where, e.Title, name, problem, hint)
			}
			if len(f.Options) > 0 {
				for _, o := range optionValues(f, v) {
					if o != "" && !slices.Contains(f.Options, o) {
						return fmt.Errorf("%s (%q) gives input %q the value %q, which is not one of its options %v",
							where, e.Title, name, o, f.Options)
					}
				}
			}
			if want, ok := f.Range(v); !ok {
				return fmt.Errorf("%s (%q) gives input %q the value %v, and its range is %s",
					where, e.Title, name, v, want)
			}
		}
		for _, f := range c.Inputs {
			if !f.Required {
				continue
			}
			if v, given := e.Inputs[f.Name]; !given || empty(v) {
				return fmt.Errorf("%s (%q) leaves out input %q, which the capability cannot run without; "+
					"give it a value%s", where, e.Title, f.Name, localNote(f))
			}
		}
	}
	return nil
}

// localNote is the sentence's tail for a required input an example cannot give
// because it is Local: there is no example to write, and saying so beats
// leaving the author to find out by trying.
func localNote(f Field) string {
	if f.Local {
		return fmt.Sprintf(" — or declare no example: %q is Local, so no example can give it", f.Name)
	}
	return ""
}
