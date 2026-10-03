package mcp

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/pkg/view"
)

// sent is what a model is handed for v: the one text content of the result.
func sent(t *testing.T, v view.View) (string, bool) {
	t.Helper()
	res, err := viewResult(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("a result is one content, got %d", len(res.Content))
	}
	return res.Content[0].(*sdk.TextContent).Text, res.IsError
}

func bigTable(rows int) view.Table {
	t := view.Table{Columns: []view.Column{{Name: "Line"}, {Name: "Content"}}}
	for i := range rows {
		t.Rows = append(t.Rows, []string{strconv.Itoa(i + 1), strings.Repeat("x", 100)})
	}
	t.Total = rows
	return t
}

// An answer a model cannot hold is cut down and says so: it keeps the true
// total, warns that the rows are partial, and fits.
func TestATableTooLargeForAResultIsCutAndSaysSo(t *testing.T) {
	text, isErr := sent(t, bigTable(2000))
	if isErr || len(text) > maxResultBytes {
		t.Fatalf("a %d byte result, error %v, want one within %d", len(text), isErr, maxResultBytes)
	}
	var got struct {
		Rows     [][]string
		Total    int
		Warnings []struct{ Code, Message, Hint string }
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 2000 || len(got.Rows) == 0 || len(got.Rows) >= 2000 {
		t.Errorf("total %d with %d rows, want the true total over a cut list", got.Total, len(got.Rows))
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Code != "core.mcp.result.cut" ||
		!strings.Contains(got.Warnings[0].Message, "of 2000 rows") || got.Warnings[0].Hint == "" {
		t.Errorf("a cut table does not say what it left out: %+v", got.Warnings)
	}
}

// A list that carries the cursor for its next page, cut, must not hand the
// cursor on: it points past the rows the cut dropped, and a call that follows
// it would skip them without a word.
func TestACutTableDoesNotHandOnACursorPastTheRowsItDropped(t *testing.T) {
	tbl := bigTable(2000)
	tbl.Page = &view.Cursor{Next: "after-2000"}
	text, _ := sent(t, tbl)
	if strings.Contains(text, "after-2000") {
		t.Errorf("a cut table still carries the cursor past its dropped rows: %.200s", text)
	}
	whole := bigTable(10)
	whole.Page = &view.Cursor{Next: "after-10"}
	if text, _ := sent(t, whole); !strings.Contains(text, "after-10") {
		t.Errorf("a table sent whole lost its cursor: %s", text)
	}
}

// A log is read from its end: what is dropped is the oldest, and what is kept
// is what happened last.
func TestACutLogKeepsItsNewestRows(t *testing.T) {
	tbl := bigTable(2000)
	tbl.Tail = true
	text, _ := sent(t, tbl)
	var got struct{ Rows [][]string }
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	if last := got.Rows[len(got.Rows)-1][0]; last != "2000" || len(got.Rows) >= 2000 {
		t.Errorf("%d rows ending at line %s, want a cut log that ends on its newest, 2000", len(got.Rows), last)
	}
}

func TestAnAnswerWithinTheBudgetIsSentWhole(t *testing.T) {
	text, _ := sent(t, bigTable(100))
	var got struct {
		Rows     [][]string
		Warnings []view.Error
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil || len(got.Rows) != 100 || len(got.Warnings) != 0 {
		t.Errorf("%d rows, %d warnings, %v: want the whole table and nothing said", len(got.Rows), len(got.Warnings), err)
	}
}

// A patch ends on a whole line and on the line that says it was cut.
func TestATextTooLargeIsCutAtALineAndSaysSo(t *testing.T) {
	body := strings.Repeat("+a line of a patch\n", 20000)
	text, isErr := sent(t, view.Text{Body: body})
	if isErr || len(text) > maxResultBytes {
		t.Fatalf("a %d byte result, error %v", len(text), isErr)
	}
	var got view.Text
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(got.Body, "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "[cut: the first ") || !strings.Contains(last, " of "+strconv.Itoa(len(body))+" bytes") {
		t.Errorf("the text ends on %q, want the line that says how much was left out", last)
	}
	if prev := lines[len(lines)-2]; prev != "+a line of a patch" {
		t.Errorf("the cut fell inside a line: %q", prev)
	}
}

// A response is a status and headers beside a body: it is the body that is cut.
func TestAValueTooLargeIsCutAndTheRestOfThePageSurvives(t *testing.T) {
	kv := view.KeyValue{Pairs: []view.Pair{
		{Key: "status", Value: "200 OK"},
		{Key: "body", Value: strings.Repeat("y", 300<<10)},
	}}
	text, isErr := sent(t, kv)
	if isErr || len(text) > maxResultBytes {
		t.Fatalf("a %d byte result, error %v", len(text), isErr)
	}
	if !strings.Contains(text, `"200 OK"`) || !strings.Contains(text, "[cut: the first ") {
		t.Errorf("the status was lost or the body was not marked cut: %.200s", text)
	}
}

func TestASectionsPageCutsItsLargestSection(t *testing.T) {
	page := view.Sections{Items: []view.Section{
		{ID: "summary", Title: "summary", View: view.KeyValue{Pairs: []view.Pair{{Key: "a", Value: "b"}}}},
		{ID: "rows", Title: "rows", View: bigTable(2000)},
	}}
	text, isErr := sent(t, page)
	if isErr || len(text) > maxResultBytes {
		t.Fatalf("a %d byte result, error %v", len(text), isErr)
	}
	if !strings.Contains(text, `"key":"a"`) || !strings.Contains(text, "core.mcp.result.cut") {
		t.Errorf("the small section was lost or the large one not marked: %.300s", text)
	}
}

// A tree cut in half reads as a whole one, so it is refused, with what to do.
func TestAShapeThatCannotBeCutIsRefusedAndNamesTheRemedy(t *testing.T) {
	tree := view.Tree{}
	for i := range 5000 {
		tree.Roots = append(tree.Roots, view.Node{Label: strings.Repeat("n", 40) + strconv.Itoa(i)})
	}
	text, isErr := sent(t, tree)
	if !isErr {
		t.Fatalf("an oversize tree went through at %d bytes", len(text))
	}
	var got view.Error
	if err := json.Unmarshal([]byte(text), &got); err != nil || got.Code != "core.mcp.result.toolarge" || got.Hint == "" {
		t.Errorf("refusal = %+v (%v), want core.mcp.result.toolarge with a hint", got, err)
	}
}
