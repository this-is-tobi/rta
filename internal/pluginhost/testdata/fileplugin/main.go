// Command fileplugin is a plugin whose one capability says what it was handed
// for a setting declared to hold certificates: the path, whether that path is
// a file written for the call alone, and what the file holds — for the test of
// a CA the host read for a call reaching a plugin's own process as a file.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk"
	"github.com/this-is-tobi/rta/pkg/view"
)

func main() {
	sdk.Serve(plugin.Plugin{
		Name:    "file",
		Summary: "says what it was handed for its CA setting",
		Capabilities: []plugin.Capability{{
			ID: "file.show", Summary: "say what ca-file is", Safety: plugin.Read, Idempotent: true,
			Inputs: []plugin.Field{{Name: "ca-file", Type: plugin.String, Local: true, Config: "ca-file",
				File: plugin.FileCertificates, Help: "CA"}},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				path := req.String("ca-file")
				content, err := os.ReadFile(path)
				if err != nil {
					return nil, err
				}
				return view.Text{Body: fmt.Sprintf("%s\n%t\n%s", path, req.Temporary("ca-file"), content)}, nil
			},
		}},
	})
}
