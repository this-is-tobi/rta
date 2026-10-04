package pluginhost

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Failed is a plugin somebody approved and rta could not start: the artifact
// is where it was, its digest is trusted, and the launch ended before the
// plugin said what it declares.
//
// Not Untrusted, and kept apart from it: nothing here waits on a decision.
// An untrusted plugin is a decision nobody has made, said once at startup and
// carried to every surface that lists plugins. This one is a decision that
// was made and has stopped working, and its failure mode was the opposite of
// that gate's — loud at startup, on every command, with nothing in the words
// about how to make it stop, and then absent from `rta plugin list` and `rta
// doctor`, which listed only what had loaded. A plugin that stopped running
// looked, in the one place an operator goes to see what is installed, exactly
// like one that had never been.
type Failed struct {
	Name   string
	Path   string
	Digest string
	// Reason is what the launch said, without the path it opens with, on one
	// line: how the process ended and the last thing it wrote.
	Reason string
	// Remedy is the command that takes the plugin out of the way, as a
	// sentence a hint can carry, and "" when none can: a plugin the system
	// root provides is not the operator's to remove, and rta's words about it
	// must not offer a command that cannot work.
	Remedy string
}

// Short is the digest as a person quotes it.
func (f Failed) Short() string { return Untrusted{Digest: f.Digest}.Short() }

// Failed lists the plugins discovery found, trusted, and could not start.
func (h *Host) Failed() []Failed {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Failed(nil), h.failed...)
}

// failedToStart records a plugin that did not start and returns the problem
// LoadInto reports for it.
//
// **The way out is in the message.** A plugin that fails to start is
// reported before every command, and the report named the file and what the
// process wrote and stopped there, so an operator who wanted only for it to
// stop had to know that approving an artifact is the thing that makes rta
// launch it, and that `rta plugin untrust` is the inverse. The hint says so,
// with the one command for where the plugin lives: `untrust` for one on
// $PATH, `remove` for one rta installed, nothing for one the system root
// provides.
//
// A refusal that is already coded is left as it is. Those are the machine's —
// a TMPDIR too long for a socket, one that cannot hold it — and they stop
// every plugin in the same words, which ReportLoadProblems says once for all
// of them; a hint naming this plugin would make each cause its own and bring
// back the dozen copies of one sentence. The plugin is recorded all the same,
// so the inventory still shows it did not start.
func (h *Host) failedToStart(f Found, id Identity, err error) error {
	remedy := remedyFor(f)
	h.mu.Lock()
	h.failed = append(h.failed, Failed{
		Name: f.Name, Path: id.Path, Digest: id.Digest,
		Reason: reasonOf(id.Path, err), Remedy: remedy,
	})
	h.mu.Unlock()

	var coded *view.Error
	if errors.As(err, &coded) {
		return fmt.Errorf("plugin %s: %w", f.Name, err)
	}
	cause := view.Errorf("plugin.start", "%s", err.Error())
	if remedy != "" {
		cause = cause.WithHint(remedy)
	}
	return fmt.Errorf("plugin %s: %w", f.Name, cause)
}

// remedyFor is the sentence that says how to stop rta launching a plugin,
// by where the plugin was found.
func remedyFor(f Found) string {
	dir := filepath.Dir(f.Path)
	switch {
	case dir == filepath.Clean(ManagedBin()):
		return "`rta plugin remove " + f.Name + "` uninstalls it"
	case SystemBin() != "" && dir == filepath.Clean(SystemBin()):
		return ""
	}
	return "`rta plugin untrust " + f.Name + "` stops rta launching it"
}

// reasonOf is err's words on one line, led by what happened rather than where:
// the path the launch message opens with is the artifact's, which the row it
// goes in already carries.
func reasonOf(path string, err error) string {
	text := strings.TrimPrefix(err.Error(), path+" ")
	text = strings.Join(strings.Fields(strings.ReplaceAll(text, "\n", " — ")), " ")
	text, rest := format.Head(text, reasonLength)
	if rest > 0 {
		text += "…"
	}
	return text
}

// reasonLength bounds Failed.Reason: it goes into a table cell and a row of
// `rta doctor`, and the full words were said at startup.
const reasonLength = 240
