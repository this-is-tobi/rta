// Command plugin-hello is a complete rta plugin in one file.
//
// Build it and put it on your $PATH as `rta-plugin-hello`:
//
//	go build -o ~/.local/bin/rta-plugin-hello ./examples/plugin-hello
//
// Then `rta hello greet you` works — `name` is declared positional, so it is
// an argument rather than a flag — `rta hello greet` opens a form in the TUI
// with the name field completing from what already exists, and an agent
// connected over MCP sees `hello_greet` with its inputs typed. None of those
// three surfaces is mentioned anywhere below: you declare capabilities, rta
// renders them.
//
// The shape to copy is the one thing worth noticing — a plugin is a *value*,
// not a framework. Plugin() returns data. main hands it to sdk.Serve. There
// is nothing to register, no interface to satisfy, no lifecycle to hook, and
// no way to ask for special treatment from the host.
package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func main() { sdk.Serve(Plugin()) }

// languages is stand-in state. A real plugin would read a file, query a
// database or call an API here; what matters for the example is that Suggest
// completes from something that exists at call time rather than from a list
// baked into the declaration. Options is the right field for a fixed set.
var languages = map[string]string{
	"en": "Hello",
	"fr": "Bonjour",
	"es": "Hola",
	"ja": "こんにちは",
}

// Plugin returns the declaration. This is the entire contract with rta.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "hello",
		Summary: "A worked example of an rta plugin",
		Version: "0.1.0",
		Capabilities: []plugin.Capability{
			{
				ID:      "hello.greet",
				Summary: "Greet somebody, in a language of your choosing",
				// Read is the claim that this changes nothing. It is what
				// decides whether an agent may call this without an
				// operator's --allow-write, so it is a promise about blast
				// radius rather than a label.
				Safety:     plugin.Read,
				Idempotent: true,
				Description: "Prints a greeting. It exists to show the smallest complete " +
					"capability: typed inputs, a returned view, and no knowledge of which " +
					"surface is asking.",
				Inputs: []plugin.Field{
					{
						Name: "name", Type: plugin.String, Positional: true, Required: true,
						Help: "who to greet",
					},
					{
						Name: "lang", Type: plugin.String, Default: "en",
						Help: "language code",
						// Suggest runs on human surfaces only, on demand. The
						// values are not shipped with the declaration: what
						// exists right now is information in its own right,
						// and an agent listing tools has not asked for it.
						Suggest: func(context.Context, plugin.Request) []string {
							out := make([]string, 0, len(languages))
							for code, word := range languages {
								// A tab-separated description: shell
								// completion shows it, other surfaces strip it.
								out = append(out, code+"\t"+word)
							}
							sort.Strings(out)
							return out
						},
					},
					{Name: "shout", Type: plugin.Bool, Help: "upper-case the result"},
				},
				Run: greet,
			},
			{
				ID:         "hello.languages",
				Summary:    "List the languages this plugin knows",
				Safety:     plugin.Read,
				Idempotent: true,
				Description: "Returns a table, so that the example covers more than one view " +
					"type. rta renders it as a bordered table in a terminal, as rows in the " +
					"TUI, as CSV with -o csv, and as structured JSON to an agent — from this " +
					"one value.",
				Run: func(context.Context, plugin.Request) (view.View, error) {
					t := view.Table{Columns: []view.Column{
						{Name: "Code"},
						{Name: "Greeting"},
					}}
					codes := make([]string, 0, len(languages))
					for code := range languages {
						codes = append(codes, code)
					}
					sort.Strings(codes)
					for _, code := range codes {
						t.Rows = append(t.Rows, []string{code, languages[code]})
					}
					t.Total = len(t.Rows)
					return t, nil
				},
			},
		},
	}
}

func greet(_ context.Context, req plugin.Request) (view.View, error) {
	lang := req.String("lang")
	word, ok := languages[lang]
	if !ok {
		// A view.Error, not a bare error. The code is stable enough for a
		// script to branch on and the hint is what the person does next —
		// both of which a plain error string loses, on every surface.
		known := make([]string, 0, len(languages))
		for code := range languages {
			known = append(known, code)
		}
		sort.Strings(known)
		return nil, view.Errorf("hello.unknownlang", "no greeting for language %q", lang).
			WithHint("known languages: " + strings.Join(known, ", "))
	}

	msg := fmt.Sprintf("%s, %s!", word, req.String("name"))
	if req.Bool("shout") {
		msg = strings.ToUpper(msg)
	}
	return view.Text{Body: msg}, nil
}
