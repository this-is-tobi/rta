package plugin

import (
	"context"
	"slices"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// Options are a closed set, and this is where the set is held to.
//
// Field.Options says it "enumerates every value this input accepts", and three
// of four surfaces kept that: the TUI offers a picker and nothing else, and
// MCP publishes the set as an enum. The CLI and the operator's own config
// passed whatever was written, so each handler decided alone what an unlisted
// value meant, and they disagreed: net.dns and sys.ps refused one by name,
// while `gen token --encoding b64` printed hex, `gen uuid --version 9` a v4, and
// `net listen --proto tpc` "nothing is listening" — a wrong answer, three
// different ways, with nothing to say so.
//
// Held to at run time, by the host, for every surface — the way Min and Max
// already are — so a handler can take its own Options as given. A value that
// names an option in another case has already been rewritten to the declared
// spelling by Resolve; what reaches this is a value naming none of them.

// CheckOptions reports the first input whose value is not among the Options
// its field declares, or nil. An empty value is not a choice and is left to
// the handler, which is where a field without a Default decides what nothing
// means.
func CheckOptions(c Capability, req Request) *view.Error {
	for _, f := range c.Inputs {
		if len(f.Options) == 0 {
			continue
		}
		var values []string
		switch f.Type {
		case String:
			values = []string{req.String(f.Name)}
		case StringSlice:
			values = req.StringSlice(f.Name)
		default:
			continue
		}
		for _, v := range values {
			if v == "" || slices.Contains(f.Options, v) {
				continue
			}
			return view.Errorf("core.input.option", "%s takes one of %s for %s, not %q",
				c.ID, strings.Join(f.Options, ", "), f.Name, v).
				WithHint("the set is closed: `rta explain " + c.ID + "` lists it beside the input")
		}
	}
	return nil
}

// GuardOptions is c's handler with CheckOptions in front of it, or the handler
// itself when no input declares Options. The registry installs it on every
// capability it holds, which is what makes the check the host's rather than
// something each surface has to remember.
func GuardOptions(c Capability) Handler {
	run := c.Run
	if run == nil || !slices.ContainsFunc(c.Inputs, func(f Field) bool { return len(f.Options) > 0 }) {
		return run
	}
	return func(ctx context.Context, req Request) (view.View, error) {
		if verr := CheckOptions(c, req); verr != nil {
			return nil, verr
		}
		return run(ctx, req)
	}
}
