package mcp

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
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

// padRegistry is a plugin whose three capabilities each answer with more than
// a result may hold: a table with an input to bound it, a tree with the three
// that bound and aim one (and a Local one a model is never offered), and a
// table with none.
func padRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	table := func(context.Context, plugin.Request) (view.View, error) { return bigTable(2000), nil }
	tree := func(context.Context, plugin.Request) (view.View, error) {
		out := view.Tree{}
		for i := range 5000 {
			out.Roots = append(out.Roots, view.Node{Label: strings.Repeat("n", 40) + strconv.Itoa(i)})
		}
		return out, nil
	}
	reg := registry.New()
	err := reg.Register(plugin.Plugin{Name: "pad", Summary: "pad", Capabilities: []plugin.Capability{
		{ID: "pad.list", Summary: "a long list", Safety: plugin.Read, Idempotent: true, Run: table,
			Inputs: []plugin.Field{{Name: "count", Type: plugin.Int, Help: "how many"}, {Name: "length", Type: plugin.Int, Help: "how long"}}},
		{ID: "pad.tree", Summary: "a large tree", Safety: plugin.Read, Idempotent: true, Run: tree,
			Inputs: []plugin.Field{{Name: "path", Type: plugin.String, Help: "where"}, {Name: "depth", Type: plugin.Int, Help: "how deep"},
				{Name: "limit", Type: plugin.Int, Help: "how many"}, {Name: "host", Type: plugin.String, Local: true, Help: "not offered"}}},
		{ID: "pad.plain", Summary: "a long list with nothing to turn down", Safety: plugin.Read, Idempotent: true, Run: table},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// A tool that is asked for too much has its own words for less, and the hint
// that sends a model to "a lower limit where the tool has one" leaves it to
// find them: gen_password has count and no limit, and fs_tree has depth, limit
// and path. The hint names the ones the tool being called has, and for a table
// that was cut how many rows fit.
func TestACutTableNamesTheToolsOwnInputAndHowManyRowsFit(t *testing.T) {
	s := connectWithData(t, padRegistry(t), Options{})
	res := callTool(t, s, "pad_list", map[string]any{})
	var got struct {
		Rows     [][]string
		Warnings []view.Error
	}
	if err := json.Unmarshal([]byte(res.Content[0].(*sdk.TextContent).Text), &got); err != nil || len(got.Warnings) != 1 {
		t.Fatalf("%v, %d warnings: want one cut warning", err, len(got.Warnings))
	}
	hint := got.Warnings[0].Hint
	if want := "set `count` to " + strconv.Itoa(len(got.Rows)) + " or less"; !strings.Contains(hint, want) {
		t.Errorf("the hint does not say %q: %s", want, hint)
	}
	if strings.Contains(hint, "limit") || strings.Contains(hint, "where the tool has one") {
		t.Errorf("the hint names a lever the tool does not have: %s", hint)
	}
}

func TestARefusedAnswerNamesTheToolsDepthLimitAndPath(t *testing.T) {
	s := connectWithData(t, padRegistry(t), Options{})
	res := callTool(t, s, "pad_tree", map[string]any{})
	var got view.Error
	if err := json.Unmarshal([]byte(res.Content[0].(*sdk.TextContent).Text), &got); err != nil || got.Code != "core.mcp.result.toolarge" {
		t.Fatalf("%v: %+v, want core.mcp.result.toolarge", err, got)
	}
	for _, want := range []string{"`limit`", "`depth`", "`path`"} {
		if !strings.Contains(got.Hint, want) {
			t.Errorf("the hint does not name %s: %s", want, got.Hint)
		}
	}
	if strings.Contains(got.Hint, "host") || strings.Contains(got.Hint, "where the tool has one") {
		t.Errorf("the hint names an input the tool does not offer, or is the generic one: %s", got.Hint)
	}
}

// A tool with no input that makes an answer smaller keeps the sentence that
// says what usually does.
func TestAToolWithNoLeverKeepsTheGenericHint(t *testing.T) {
	s := connectWithData(t, padRegistry(t), Options{})
	var got struct{ Warnings []view.Error }
	if err := json.Unmarshal([]byte(callTool(t, s, "pad_plain", map[string]any{}).Content[0].(*sdk.TextContent).Text), &got); err != nil ||
		len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0].Hint, "or a lower limit where the tool has one") {
		t.Errorf("%v: %+v, want the generic hint", err, got.Warnings)
	}
}
