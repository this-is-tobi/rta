package kv

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/mcp"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/pkg/view"
)

const (
	rightToLeft = "\u202e"
	escape      = "\x1b"
)

// folderNames are the top-level folders of a store somebody wrote odd names
// into: a newline that would end a table row, an escape sequence that a
// terminal's cleaner would drop and so draw as the ordinary folder beside it,
// a right-to-left override, and a folder with a space in it, which is a name's
// own and stays as it is.
var folderNames = map[string]string{
	"fnl\nforged-folder/inner": `"fnl\nforged-folder/"`,
	"prod" + escape + "[0m/x":  `"prod\x1b[0m/"`,
	rightToLeft + "evil/x":     `"\u202eevil/"`,
	"Spaced Folder/key":        "Spaced Folder/",
	"prod/db/password":         "prod/",
}

func oddFolders(t *testing.T) {
	t.Helper()
	setup(t)
	for key := range folderNames {
		text(t, runSet, map[string]any{"key": key, "value": "v"}, false)
	}
}

// The overview's folder table is the one place the feature draws a name a
// writer chose, and the cleaner a renderer applies to a cell is wrong of a name:
// a newline split a row in two, so a folder that does not exist had a row of its
// own, and an escape sequence or an override was dropped, so two folders drew
// alike. A name that does not read as itself is shown quoted with the character
// written out, as `fs` shows a file, and one that does is left as it is.
func TestAFolderNameWithAControlCharacterIsShownEscapedNotCleaned(t *testing.T) {
	oddFolders(t)

	var screen bytes.Buffer
	if err := cli.Render(&screen, overviewPage(t, req(nil, false)), cli.Options{Format: cli.Pretty, NoColor: true, Width: 100}); err != nil {
		t.Fatal(err)
	}
	drawn := screen.String()

	for _, want := range folderNames {
		if !strings.Contains(drawn, want) {
			t.Errorf("the folders table does not show %s:\n%s", want, drawn)
		}
	}
	// One row to a folder: the newline did not start another, and the escape
	// sequence did not make a second `prod/`.
	if n := strings.Count(drawn, "forged-folder/"); n != 1 {
		t.Errorf("forged-folder/ is drawn %d times, once being the folder it is part of:\n%s", n, drawn)
	}
	if n := strings.Count(drawn, "prod/"); n != 1 {
		t.Errorf("prod/ is drawn %d times; the folder with an escape in its name drew as a second one:\n%s", n, drawn)
	}
	for _, never := range []string{escape, rightToLeft} {
		if strings.Contains(drawn, never) {
			t.Errorf("the screen holds the raw character %q:\n%s", never, drawn)
		}
	}
}

// What an agent is sent is the same table, and over MCP the cell is cleaned a
// second time on the way to the model, which dropped the override and left
// `evil/` — the name of a folder that is not there.
func TestAnAgentIsToldAFolderNameAsItIsNotCleaned(t *testing.T) {
	oddFolders(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")

	res := callTool(t, mcpSession(t, mcp.Options{}), "kv_overview", map[string]any{"detail": true})
	if res.IsError {
		t.Fatalf("kv_overview was refused: %+v", res.Content)
	}
	answer := res.Content[0].(*sdk.TextContent).Text
	var folders []string
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if rows, ok := v["rows"].([]any); ok {
				for _, row := range rows {
					if cells, ok := row.([]any); ok && len(cells) > 0 {
						folders = append(folders, cells[0].(string))
					}
				}
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	var decoded any
	if err := json.Unmarshal([]byte(answer), &decoded); err != nil {
		t.Fatalf("the answer is not JSON: %v\n%s", err, answer)
	}
	walk(decoded)

	have := map[string]bool{}
	for _, f := range folders {
		have[f] = true
	}
	for _, want := range folderNames {
		if !have[want] {
			t.Errorf("the agent is not told the folder %s; it is told %q", want, folders)
		}
	}
	if len(folders) != len(folderNames) {
		t.Errorf("the agent is told %d folders, want %d: %q", len(folders), len(folderNames), folders)
	}
}

// A name that reads as itself is not changed by showing names this way, so a
// store with ordinary folders sees no difference — and the row for the keys in
// no folder, which no folder can be called, is not quoted for its space.
func TestOrdinaryFolderNamesAreShownAsTheyAre(t *testing.T) {
	seeded(t)
	tbl := sectionByID(t, overviewPage(t, req(nil, false)), "folders").(view.Table)
	got := []string{}
	for _, row := range tbl.Rows {
		got = append(got, row[0])
	}
	if want := []string{"prod/", "staging/", noFolder}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("folders = %q, want %q", got, want)
	}
}
