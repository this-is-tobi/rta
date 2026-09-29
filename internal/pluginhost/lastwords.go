package pluginhost

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"

	"github.com/this-is-tobi/rta/pkg/format"
)

// What a plugin says, and where it goes.
//
// go-plugin reads a plugin's stderr a line at a time and logs what it reads
// as an error through the hclog logger a launch hands it — one beginning
// [ERROR], a panic and the trace after it, an hclog entry at error — with its
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
//
// **go-plugin does not read those lines either: lastWords does.** It hands
// each line, as it came, to the writer it is given as Stderr, and parses it
// as a JSON log entry only for a logger that logs something — in a parser
// that asserts an entry's "@message", "@level" and "@timestamp" are strings.
// A plugin that wrote {"@message":1} panicked the goroutine reading it and
// took rta down with every plugin it ran, an MCP server's among them, the
// TUI's terminal left as it was. So the logger logs nothing, lastWords is
// the Stderr, and it reads each line by go-plugin's rule for what a plugin
// says at error, declining what it does not expect. How the process exited
// is the process's own state (exitStatus), no longer a line of go-plugin's
// in a stream a plugin could write the same line into.

// wordsKept is how many of a plugin's lines are kept, and wordLength how
// long one may be: enough for the reason a plugin gives and a panic's first
// lines, and a bound on what a plugin that writes without end costs.
const (
	wordsKept  = 4
	wordLength = 240
)

// lineLimit bounds the line being read: go-plugin's own buffer, the most it
// hands on as one line, so an entry it would have read whole is read whole.
const lineLimit = 64 * 1024

// lastWords is the writer a launch hands go-plugin as the plugin's stderr:
// it keeps what the plugin said at error and writes nothing.
type lastWords struct {
	mu sync.Mutex
	// line is the line being written, up to lineLimit bytes of it: go-plugin
	// writes a line and its end in two calls, and a long one in pieces.
	line      []byte
	said      []string
	panicking bool
}

// Write takes what the plugin wrote, a line or a piece of one.
func (w *lastWords) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	written := len(p)
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		piece := p
		if end >= 0 {
			piece = p[:end]
		}
		if room := lineLimit - len(w.line); len(piece) > room {
			piece = piece[:room]
		}
		w.line = append(w.line, piece...)
		if end < 0 {
			break
		}
		w.heard(string(w.line))
		w.line = w.line[:0]
		p = p[end+1:]
	}
	return written, nil
}

// heard reads one whole line, as go-plugin logs one at error: an hclog entry
// at that level, its message; a line opening [ERROR]; and a panic from its
// first line on. Anything else is a plugin talking to itself.
func (w *lastWords) heard(line string) {
	if message, entry := errorEntry(line); entry {
		w.keep(message)
		return
	}
	switch {
	case strings.HasPrefix(line, "panic: ") || strings.HasPrefix(line, "fatal error: "):
		// A panic's first line says what went wrong and the trace under it
		// where, so it is kept from its start rather than from its end.
		w.said, w.panicking = nil, true
		w.keep(line)
	case w.panicking || strings.HasPrefix(line, "[ERROR]"):
		w.keep(line)
	}
}

// errorEntry reads line as an hclog JSON entry, which any JSON object is to
// go-plugin: its message when it is at error, "" at any other level, and
// whether it was one. A level or a message that is not a string is not one
// at error, where go-plugin's own reading panicked.
func errorEntry(line string) (message string, entry bool) {
	var fields struct {
		Level   json.RawMessage `json:"@level"`
		Message json.RawMessage `json:"@message"`
	}
	if json.Unmarshal([]byte(line), &fields) != nil {
		return "", false
	}
	var level string
	if json.Unmarshal(fields.Level, &level) != nil || !strings.EqualFold(strings.TrimSpace(level), "error") {
		return "", true
	}
	_ = json.Unmarshal(fields.Message, &message)
	return message, true
}

// keep adds a line to what the plugin said: the last few, or a panic's
// first few, each cut to a bounded piece of itself.
func (w *lastWords) keep(line string) {
	line, _ = format.Head(strings.TrimRight(line, " \t\r\n"), wordLength)
	switch {
	case line == "":
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
}

// told is how the plugin exited, when it did not exit cleanly (exitStatus),
// and what it said, as the end of rta's own word on its failure — after
// everything of rta's, so a sequence a renderer reads as running to the end
// of the text takes nothing of rta's with it — or "" when there is neither.
func (w *lastWords) told(exited string) string {
	var b strings.Builder
	if exited != "" {
		b.WriteString("\nthe plugin exited: " + exited)
	}
	if w == nil {
		return b.String()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.said) > 0 {
		b.WriteString("\nthe plugin wrote: " + strings.Join(w.said, "\n  "))
	}
	return b.String()
}
