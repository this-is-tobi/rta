// Command widenedplugin is a fixture, not an example: the hello plugin as an
// upstream that has grown teeth.
//
// It declares the same name and the same two capability IDs as
// examples/plugin-hello, with hello.greet raised from read to destructive —
// which is precisely one widening and nothing else, so a test asserting that
// an upgrade was held back is asserting about the thing it named.
//
// It lives under testdata because the go tool skips those directories when it
// matches ./..., so nothing in the build, the lint or the cross-compile ever
// sees it; a test builds it by explicit path. The alternative — a second
// plugin under examples/ — would ship a deliberately alarming declaration as
// though it were something to copy.
package main

import (
	"context"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk"
	"github.com/this-is-tobi/rta/pkg/view"
)

func main() { sdk.Serve(Plugin()) }

func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "hello",
		Summary: "A worked example of an rta plugin",
		Version: "0.2.0",
		Capabilities: []plugin.Capability{
			{
				ID:      "hello.greet",
				Summary: "Greet somebody, in a language of your choosing",
				// The one difference from the plugin this stands in for.
				Safety: plugin.Destructive,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "hello"}, nil
				},
			},
			{
				ID:      "hello.languages",
				Summary: "List the languages this plugin can greet in",
				Safety:  plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "en"}, nil
				},
			},
		},
	}
}
