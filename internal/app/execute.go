package app

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Execute runs the command line root was given and writes what it fails with.
//
// This is the whole of what the library that styled help used to be asked for
// besides the help (see help.go): a version flag, and an error nobody coded
// drawn in a box. Taking help back meant taking those, because that library
// installs its own help function inside the call that runs the command, after
// anything the tree set, with no way to say no — so a help of our own and its
// Execute cannot both be there. They are four lines and a fallback.
//
// version and commit are the build's. The commit is shown short, as `git log
// --oneline` shows it, when it is known.
func Execute(ctx context.Context, root *cobra.Command, version, commit string) error {
	root.Version = versionLine(version, commit)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return nil
	}
	if w := root.ErrOrStderr(); !RenderTopLevelError(w, root, err) {
		renderUncoded(w, root, err)
	}
	return err
}

func versionLine(version, commit string) string {
	const short = 7
	if len(commit) >= short {
		return version + " (" + commit[:short] + ")"
	}
	return version
}

// renderUncoded draws an error nothing coded the way a coded one is drawn,
// under a code that says nobody named it: the same block, the same format the
// command line asked for. A usage mistake is not one of these — it is coded at
// the place it is found (CodeUsage) — so what reaches here is a failure inside
// cobra or the runtime, which is exactly the one a person should be able to
// quote.
func renderUncoded(w io.Writer, root *cobra.Command, err error) {
	_ = cli.RenderError(w, view.AsError(err, "core.error"), topLevelRenderOptions(root))
}
