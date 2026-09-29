package pluginhost

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/this-is-tobi/rta/pkg/format"
)

// What a plugin says, and where it goes.
//
// go-plugin logs through the hclog logger a launch hands it: each line the
// plugin writes to its stderr that it reads as an error — one beginning
// [ERROR], a panic and the trace after it, an hclog entry at error — and its
// own word on the process, "plugin process exited" and the status. That
// logger wrote to rta's standard error, so every trusted plugin that failed
// to start put a raw JSON line on the operator's terminal, before every
// command, beside rta's own line about the same failure; a panic put its
// whole trace there, a line of JSON at a time, in the middle of whatever the
// command drew, the TUI's screen among them.
//
// Nothing of it is written anywhere now. What it says is kept, a little of
// it, where rta's own word on the failure goes: the error a launch that
// failed returns, which a load problem, an install's verification and plugin
// dev each report, and the plugin.gone a call that lost its process ends in.
// Those are drawn by the renderers, which clean what a terminal acts on out
// of the plugin's words as they do out of everything else a plugin says.

// wordsKept is how many of a plugin's lines are kept, and wordLength how
// long one may be: enough for the reason a plugin gives and a panic's first
// lines, and a bound on what a plugin that writes without end costs.
const (
	wordsKept  = 4
	wordLength = 240
)

// lastWords is the writer a launch's logger writes to: it keeps what the
// plugin said and writes nothing. module is the logger's own name, which
// go-plugin's entries carry and the plugin's lines carry a name below.
type lastWords struct {
	module string

	mu        sync.Mutex
	said      []string
	exited    string
	panicking bool
}

// Write reads one entry, which hclog writes whole in one call.
func (w *lastWords) Write(p []byte) (int, error) {
	var entry struct {
		Module  string `json:"@module"`
		Message string `json:"@message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(p, &entry) != nil {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if entry.Module == w.module {
		// go-plugin's own. The status the process exited with is the one of
		// its words worth keeping; the rest are about its own plumbing.
		if entry.Message == "plugin process exited" && entry.Error != "" {
			w.exited, _ = format.Head(entry.Error, wordLength)
		}
		return len(p), nil
	}
	line, _ := format.Head(strings.TrimRight(entry.Message, " \t\r\n"), wordLength)
	switch {
	case line == "":
	case strings.HasPrefix(line, "panic: ") || strings.HasPrefix(line, "fatal error: "):
		// A panic's first line says what went wrong and the trace under it
		// where, so it is kept from its start rather than from its end.
		w.said, w.panicking = []string{line}, true
	case w.panicking:
		if len(w.said) < wordsKept {
			w.said = append(w.said, line)
		}
	default:
		w.said = append(w.said, line)
		if len(w.said) > wordsKept {
			w.said = w.said[len(w.said)-wordsKept:]
		}
	}
	return len(p), nil
}

// told is what the plugin said, as the end of rta's own word on its failure
// — after everything of rta's, so a sequence a renderer reads as running to
// the end of the text takes nothing of rta's with it — or "" when it said
// nothing.
func (w *lastWords) told() string {
	if w == nil {
		return ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var b strings.Builder
	if w.exited != "" {
		b.WriteString("\nthe plugin exited: " + w.exited)
	}
	if len(w.said) > 0 {
		b.WriteString("\nthe plugin wrote: " + strings.Join(w.said, "\n  "))
	}
	return b.String()
}
