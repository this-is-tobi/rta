// Command bigplugin is a plugin whose one capability answers with as many
// bytes as it is asked for, for the tests of what the host does with an answer
// larger than it is willing to take.
package main

import (
	"context"
	"strings"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk"
	"github.com/this-is-tobi/rta/pkg/view"
)

func main() {
	sdk.Serve(plugin.Plugin{
		Name:    "big",
		Summary: "answers with as many bytes as it is asked for",
		Capabilities: []plugin.Capability{{
			ID: "big.dump", Summary: "answer with that many bytes", Safety: plugin.Read, Idempotent: true,
			Inputs: []plugin.Field{{Name: "bytes", Type: plugin.Int, Required: true, Help: "how many"}},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return view.Text{Body: strings.Repeat("x", req.Int("bytes"))}, nil
			},
		}},
	})
}
