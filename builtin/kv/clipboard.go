package kv

import (
	"context"
	"fmt"
	"strings"

	"github.com/this-is-tobi/rta/internal/clipboard"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// copyToClipboard hands the value to the first clipboard program installed,
// through internal/clipboard — shared with the TUI's own per-value copy
// action (internal/render/tui), which needs the same "byte for byte,
// whatever the value contains" property for a value that has nowhere to be
// re-fetched from if a first attempt mangled it.
func copyToClipboard(value []byte) *view.Error {
	ok, failed, tried := clipboard.Copy(value)
	if ok {
		return nil
	}
	if len(failed) > 0 {
		return view.Errorf("kv.clipboard.failed",
			"no clipboard program would take the value: %s", strings.Join(failed, ", ")).
			WithHint("over SSH there is usually no clipboard to write to — take the value " +
				"another way: rta kv get <key> --out <file>")
	}
	return view.Errorf("kv.clipboard.missing", "no clipboard program on this machine").
		WithHint("install one of: " + strings.Join(tried, ", ") +
			" — or take the value another way: rta kv get <key> --out <file>")
}

// runCopy puts one value on the clipboard and says nothing about what it is.
//
// The clipboard belongs to whoever is at the machine, which is why kv.copy is
// HumanOnly rather than granted: nothing an agent copied there would come
// back to it, while what it replaced — the address somebody copied a second
// ago — would be gone. Never a tool means an agent's call never opens the
// store either, so the passphrase in a server's environment is not spent on
// a question that was always going to be no.
func runCopy(_ context.Context, req plugin.Request) (view.View, error) {
	key := req.String("key")
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	e, ok := s.Entries[key]
	if !ok {
		return nil, notFound(key)
	}
	size := format.Bytes(uint64(len(e.Value)))
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would copy %q (%s, %s) to the clipboard", key, e.Kind, size)}, nil
	}
	// Exactly what is stored, all of it. `pass -c` copies the first line
	// because its file format is "password, then notes"; a value here is the
	// whole secret, and a private key truncated at its first newline is not
	// a smaller secret but a broken one.
	if verr := copyToClipboard(e.Value); verr != nil {
		return nil, verr
	}
	return view.Text{Body: fmt.Sprintf(
		"copied %q to the clipboard — %s, %s, not printed anywhere\n\n"+
			"nothing clears it afterwards: it stays pastable until you copy something else.",
		key, e.Kind, size)}, nil
}
