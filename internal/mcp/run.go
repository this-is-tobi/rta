package mcp

import (
	"context"
	"fmt"
	"io"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/shutdown"
)

// Run serves one session on t until the client hangs up or ctx ends, and once
// ctx has ended waits no longer than grace for the calls in flight (zero means
// defaultShutdownGrace, the HTTP listener's): then it returns, and the process
// goes on to end without them.
//
// **The SDK's Run waits for as long as a call takes.** On ctx it closes the
// session, and closing a session waits for every request being handled, so a
// stdio server told to stop by a signal stayed up for the length of the
// longest call it had, and for good when one never returned: a plugin that
// hangs held `rta mcp serve` through every SIGTERM until a second one. The
// HTTP listener bounded this from the start (Serve); stdio did not.
//
// Calls are cancelled when ctx ends (Options.Shutdown), so what honours its
// context is already gone by the time the grace starts, and the grace is for
// what does not. A store write between its temporary file and its rename is
// let finish before the return (shutdown.Settle), as it is for any other exit
// taken from outside a command: the process is about to end, so nothing that
// starts after that is let begin.
func Run(ctx context.Context, server *sdk.Server, t sdk.Transport, grace time.Duration, stderr io.Writer) error {
	if grace <= 0 {
		grace = defaultShutdownGrace
	}
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx, t) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		if stderr != nil {
			fmt.Fprintf(stderr, "rta: mcp: calls still running %s after the stop was asked for, "+
				"ending without them\n", grace)
		}
		settle()
		return nil
	}
}

// settle is shutdown.Settle, a variable so that a test, which goes on running
// after Run returns, does not leave every later write in its process waiting.
var settle = shutdown.Settle
