package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

func primaryOut(t *testing.T, v view.View, name string, opts Options) (stdout, notes string, did bool) {
	t.Helper()
	var out, note bytes.Buffer
	opts.Notes = &note
	did = PrintPrimary(&out, v, name, opts)
	return out.String(), note.String(), did
}

// A generator's answer, written to a pipe, is the value and nothing around it:
// `gen password | pbcopy` copied the box drawn around the password.
func TestAPipeIsHandedThePrimaryValueOfATableOneToARow(t *testing.T) {
	tbl := view.Table{
		Columns: []view.Column{{Name: "Password"}, {Name: "Entropy (bits)", Kind: view.KindNumber}},
		Rows:    [][]string{{"s1", "119.1"}, {"s2", "119.1"}},
		Total:   2,
	}
	out, notes, did := primaryOut(t, tbl, "Password", Options{Format: Pretty})
	if !did || out != "s1\ns2\n" {
		t.Fatalf("a pipe got %q (handled %v), want the two passwords one to a line", out, did)
	}
	if notes != "" {
		t.Errorf("a several-row answer put %q on the notes stream", notes)
	}

	one := view.Table{Columns: tbl.Columns, Rows: tbl.Rows[:1], Total: 1}
	out, notes, _ = primaryOut(t, one, "Password", Options{Format: Pretty})
	if out != "s1\n" || notes != "# Entropy (bits): 119.1\n" {
		t.Errorf("one row: stdout %q, notes %q — the value alone out, what else it says on the notes stream", out, notes)
	}
}

// What a script is about to parse comes with its status, on the stream that is
// not the data, and its headers are left for the structured formats.
func TestAResponseBodyGoesOutAloneAndItsStatusGoesToTheNotes(t *testing.T) {
	kv := view.KeyValue{Pairs: []view.Pair{
		{Key: "status", Value: "404 Not Found"},
		{Key: "header:Server", Value: "x"},
		{Key: "body", Value: "{\"error\":\"no\"}"},
	}}
	out, notes, did := primaryOut(t, kv, "body", Options{Format: Pretty})
	if !did || out != "{\"error\":\"no\"}\n" {
		t.Fatalf("stdout %q (handled %v)", out, did)
	}
	if notes != "# status: 404 Not Found\n" {
		t.Errorf("notes %q, want the status and not the header", notes)
	}
}

// Every case where the answer is not the one value, or is not going to a pipe,
// is drawn as it always was.
func TestThePrimaryValueIsLeftAloneWhereItIsNotWhatWasAskedFor(t *testing.T) {
	tbl := view.Table{Columns: []view.Column{{Name: "Password"}}, Rows: [][]string{{"s1"}}, Total: 1}
	kv := view.KeyValue{Pairs: []view.Pair{{Key: "status", Value: "200 OK"}}}
	for name, c := range map[string]struct {
		v    view.View
		name string
		opts Options
	}{
		"a terminal":                  {tbl, "Password", Options{Format: Pretty, Screen: true}},
		"json":                        {tbl, "Password", Options{Format: JSON}},
		"csv":                         {tbl, "Password", Options{Format: CSV}},
		"no primary declared":         {tbl, "", Options{Format: Pretty}},
		"a column the view lacks":     {tbl, "Token", Options{Format: Pretty}},
		"a key the view lacks (HEAD)": {kv, "body", Options{Format: Pretty}},
		"a text view":                 {view.Text{Body: "x"}, "body", Options{Format: Pretty}},
		"no rows":                     {view.Table{Columns: tbl.Columns}, "Password", Options{Format: Pretty}},
	} {
		if out, _, did := primaryOut(t, c.v, c.name, c.opts); did || out != "" {
			t.Errorf("%s: wrote %q (handled %v), want the view drawn as it was", name, out, did)
		}
	}
}

// A value a view masks prints as its mask, which is never what a pipe asked
// for: the renderer declines and the masked view is drawn whole.
func TestAMaskedPrimaryValueIsNotWrittenAsItsMask(t *testing.T) {
	tbl := view.Table{Columns: []view.Column{{Name: "Token"}}, Rows: [][]string{{"real"}}, Total: 1, Redacted: []string{"Token"}}
	if out, _, did := primaryOut(t, tbl, "Token", Options{Format: Pretty}); did || strings.Contains(out, "real") {
		t.Errorf("a masked primary was written: %q (handled %v)", out, did)
	}
}

// A page that is one of several says so on the notes stream: a pipe handed the
// first rows of more must be able to tell.
func TestAPartialPageSaysSoOnTheNotes(t *testing.T) {
	tbl := view.Table{Columns: []view.Column{{Name: "UUID"}}, Rows: [][]string{{"a"}, {"b"}}, Total: 40,
		Warnings: []view.Error{{Code: "demo.partial", Message: "2 unreadable"}}}
	_, notes, _ := primaryOut(t, tbl, "UUID", Options{Format: Pretty})
	if !strings.Contains(notes, "# 2 of 40 rows") || !strings.Contains(notes, "# demo.partial 2 unreadable") {
		t.Errorf("notes %q", notes)
	}
}

// What a server put in a body does not reach the pipe's reader as a terminal
// sequence: the cleaning the other pretty output gets.
func TestThePrimaryValueIsCleanedLikeEveryOtherPrettyOutput(t *testing.T) {
	kv := view.KeyValue{Pairs: []view.Pair{{Key: "body", Value: "ok\x1b]0;owned\x07"}}}
	out, _, _ := primaryOut(t, kv, "body", Options{Format: Pretty})
	if strings.ContainsRune(out, 0x1b) || strings.ContainsRune(out, 0x07) {
		t.Errorf("a control sequence reached the pipe: %q", out)
	}
}
