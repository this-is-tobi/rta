// Command rta is the Rule Them All CLI.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"charm.land/fang/v2"

	"github.com/this-is-tobi/rule-them-all/internal/app"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// version is set by the linker at release time (-X main.version=...).
var version = "dev"

func main() {
	reg, err := app.NewRegistry()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rta: broken built-in registration:", err)
		os.Exit(1)
	}
	root := app.NewRoot(reg, version)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err = fang.Execute(ctx, root,
		fang.WithVersion(version),
		fang.WithErrorHandler(errorHandler),
	)
	os.Exit(app.ExitCode(err))
}

// errorHandler keeps capability errors quiet (they were already rendered to
// stderr with code + hint by the runner) and lets fang style usage errors.
func errorHandler(w io.Writer, styles fang.Styles, err error) {
	if _, ok := err.(*view.Error); ok {
		return // already rendered by runCapability via cli.RenderError
	}
	fang.DefaultErrorHandler(w, styles, err)
}
