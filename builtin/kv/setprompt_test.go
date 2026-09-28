package kv

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// stubValuePrompt makes a person reachable at the terminal on the CLI alone,
// answering kv set's value prompt with answer, and records each key it was
// asked for.
func stubValuePrompt(t *testing.T, answer string, err error) *[]string {
	t.Helper()
	return stubPaste(t, answer, false, err)
}

// stubPaste is stubValuePrompt for an answer the prompt reports more arrived
// with, the rest of a paste that spanned lines.
func stubPaste(t *testing.T, answer string, more bool, err error) *[]string {
	t.Helper()
	var asked []string
	origPrompt, origCan := promptValue, canPrompt
	promptValue = func(key string) ([]byte, bool, error) {
		asked = append(asked, key)
		return []byte(answer), more, err
	}
	canPrompt = func(req plugin.Request) bool { return req.Surface() == plugin.SurfaceCLI }
	t.Cleanup(func() { promptValue, canPrompt = origPrompt, origCan })
	return &asked
}

// At a terminal, a secret is typed where nothing keeps it: kv set given no
// value asks for one, and what is typed is stored as it was typed, labelled by
// what it holds, from the person who typed it.
func TestSetAtATerminalAsksForTheValue(t *testing.T) {
	setup(t)
	asked := stubValuePrompt(t, " s3cret with spaces ", nil)
	v, err := runSet(context.Background(), cliReq(map[string]any{
		"key": "db-password", "passphrase": "correct horse battery staple",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 1 || (*asked)[0] != "db-password" {
		t.Fatalf("asked for %q, want db-password once", *asked)
	}
	if body := v.(view.Text).Body; body != `set "db-password" (string, 20 B)` {
		t.Errorf("answer = %q", body)
	}
	got, err := runGet(context.Background(), req(map[string]any{"key": "db-password"}, false))
	if err != nil {
		t.Fatal(err)
	}
	if body := got.(view.Text).Body; body != " s3cret with spaces " {
		t.Errorf("stored %q, want exactly what was typed", body)
	}
	tbl := table(t, runList, map[string]any{"detail": true})
	if src := tbl.Rows[0][col(t, tbl, "Source")]; src != "typed" {
		t.Errorf("source = %q, want typed", src)
	}
}

// Only a call that gives nothing at all is asked: a value or a file is the
// value, and a description or a kind alone relabels what is there.
func TestSetAsksOnlyWhenGivenNothing(t *testing.T) {
	setup(t)
	asked := stubValuePrompt(t, "from the prompt", nil)
	ctx := context.Background()
	if _, err := runSet(ctx, cliReq(map[string]any{
		"key": "k", "value": "given", "passphrase": "correct horse battery staple",
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := runSet(ctx, cliReq(map[string]any{
		"key": "k", "description": "relabelled", "passphrase": "correct horse battery staple",
	})); err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 0 {
		t.Errorf("asked for %q with a value or a label given", *asked)
	}
}

// An empty answer is no value, refused as an empty value always was, and so
// is a prompt that could not be read: nothing is stored either way.
func TestSetRefusesAnEmptyAnswer(t *testing.T) {
	for name, err := range map[string]error{"empty": nil, "unreadable": io.EOF} {
		t.Run(name, func(t *testing.T) {
			setup(t)
			stubValuePrompt(t, "", err)
			_, got := runSet(context.Background(), cliReq(map[string]any{
				"key": "k", "passphrase": "correct horse battery staple",
			}))
			if !refusedAsNoValue(got) {
				t.Fatalf("want kv.set.novalue, got %v", got)
			}
			tbl := table(t, runList, nil)
			if len(tbl.Rows) != 0 {
				t.Errorf("something was stored: %v", tbl.Rows)
			}
		})
	}
}

// A description or a kind for a key the store does not hold yet makes the
// entry, so at a terminal its value is asked for too: refused, the hint sent
// the person to type it after the key, into the shell's history. A dry run
// asks and says what it would set, and an empty answer is refused as a key
// with nothing to relabel always was.
func TestSetAsksForTheValueOfANewKeyGivenALabel(t *testing.T) {
	setup(t)
	asked := stubValuePrompt(t, "s3cret", nil)
	ctx := context.Background()
	v, err := runSet(ctx, cliReq(map[string]any{
		"key": "api-token", "description": "prod API", "passphrase": "correct horse battery staple",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 1 || (*asked)[0] != "api-token" {
		t.Fatalf("asked for %q, want api-token once", *asked)
	}
	if body := v.(view.Text).Body; body != `set "api-token" (string, 6 B)` {
		t.Errorf("answer = %q", body)
	}
	tbl := table(t, runList, map[string]any{"detail": true})
	row := tbl.Rows[0]
	if row[col(t, tbl, "Description")] != "prod API" || row[col(t, tbl, "Source")] != "typed" {
		t.Errorf("stored as %v, want described as given and typed", row)
	}

	dry := plugin.NewRequest(map[string]any{
		"key": "other", "kind": "string", "passphrase": "correct horse battery staple",
	}, true, false).WithSurface(plugin.SurfaceCLI)
	if v, err := runSet(ctx, dry); err != nil || v.(view.Text).Body != `would set "other" (string, 6 B)` {
		t.Errorf("a dry run answered %v, %v", v, err)
	}
	if rows := table(t, runList, nil).Rows; len(rows) != 1 {
		t.Errorf("a dry run stored something: %v", rows)
	}

	stubValuePrompt(t, "", nil)
	_, err = runSet(ctx, cliReq(map[string]any{
		"key": "other", "description": "x", "passphrase": "correct horse battery staple",
	}))
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "kv.set.unknown" {
		t.Errorf("an empty answer for a new key: want kv.set.unknown, got %v", err)
	}
}

// A pasted certificate, key or JSON credential the prompt could not read on
// after — on Windows, or pasted slower than the prompt waits — reaches it as
// its first line alone, which is never a value on its own: refused, whether
// the prompt came before the store was opened or after, and nothing is
// stored. A line that only starts like one is a value like any other.
func TestSetRefusesTheFirstLineAPasteWasCutTo(t *testing.T) {
	setup(t)
	ctx := context.Background()
	for _, first := range []string{"-----BEGIN OPENSSH PRIVATE KEY-----", "{", " [ "} {
		stubValuePrompt(t, first, nil)
		for _, values := range []map[string]any{{"key": "k"}, {"key": "k", "description": "deploy key"}} {
			values["passphrase"] = "correct horse battery staple"
			_, err := runSet(ctx, cliReq(values))
			var ve *view.Error
			if !errors.As(err, &ve) || ve.Code != "kv.set.multiline" {
				t.Errorf("%q for %v: want kv.set.multiline, got %v", first, values, err)
			}
		}
	}
	if rows := table(t, runList, nil).Rows; len(rows) != 0 {
		t.Errorf("something was stored: %v", rows)
	}
	stubValuePrompt(t, `{"token":"t"}`, nil)
	if _, err := runSet(ctx, cliReq(map[string]any{"key": "k", "passphrase": "correct horse battery staple"})); err != nil {
		t.Errorf("a JSON value on one line: %v", err)
	}
}

// A paste that spanned lines reaches the prompt as its first line, and the
// rest as what the prompt read on after it and dropped: refused whatever the
// first line holds, a kubeconfig's apiVersion line or nothing at all, since it
// is not the value pasted, and nothing is stored. The refusal says the rest
// that arrived with the line reached no shell, that a paste slower than the
// prompt waits may still have sent some on to it, and where a value that
// spans lines is given.
func TestSetRefusesAPasteThatSpannedLines(t *testing.T) {
	setup(t)
	ctx := context.Background()
	for _, first := range []string{"apiVersion: v1", "", "-----BEGIN CERTIFICATE-----"} {
		stubPaste(t, first, true, nil)
		for _, values := range []map[string]any{{"key": "k"}, {"key": "k", "description": "kubeconfig"}} {
			values["passphrase"] = "correct horse battery staple"
			_, err := runSet(ctx, cliReq(values))
			var ve *view.Error
			if !errors.As(err, &ve) || ve.Code != "kv.set.multiline" {
				t.Errorf("%q with more for %v: want kv.set.multiline, got %v", first, values, err)
				continue
			}
			if !strings.Contains(ve.Hint, "dropped") || !strings.Contains(ve.Hint, "slower") ||
				!strings.Contains(ve.Hint, "--file") {
				t.Errorf("the refusal's hint does not say what was dropped, what may not have been, "+
					"and where to give it: %q", ve.Hint)
			}
		}
	}
	if rows := table(t, runList, nil).Rows; len(rows) != 0 {
		t.Errorf("something was stored: %v", rows)
	}
	// A line with nothing after it is a value like any other.
	stubPaste(t, "apiVersion: v1", false, nil)
	if _, err := runSet(ctx, cliReq(map[string]any{"key": "k", "passphrase": "correct horse battery staple"})); err != nil {
		t.Errorf("a line with nothing after it: %v", err)
	}
}

// Nobody is asked who is not at a terminal: an agent has an argument to give
// the value in, the TUI a masked box, and a script on the CLI with no terminal
// on its standard input keeps the refusal it always had rather than a read
// nobody will answer.
func TestSetAsksNobodyAwayFromATerminal(t *testing.T) {
	setup(t)
	asked := stubValuePrompt(t, "from the prompt", nil)
	for _, surface := range []plugin.Surface{plugin.SurfaceMCP, plugin.SurfaceTUI, plugin.SurfaceUnknown} {
		r := plugin.NewRequest(map[string]any{"key": "k", "passphrase": "correct horse battery staple"}, false, false).
			WithSurface(surface)
		if _, err := runSet(context.Background(), r); !refusedAsNoValue(err) {
			t.Errorf("%s: want kv.set.novalue, got %v", surface, err)
		}
		labelled := plugin.NewRequest(map[string]any{
			"key": "k", "description": "x", "passphrase": "correct horse battery staple",
		}, false, false).WithSurface(surface)
		var ve *view.Error
		if _, err := runSet(context.Background(), labelled); !errors.As(err, &ve) || ve.Code != "kv.set.unknown" {
			t.Errorf("%s, a label for a new key: want kv.set.unknown, got %v", surface, err)
		}
	}
	canPrompt = func(plugin.Request) bool { return false }
	if _, err := runSet(context.Background(), cliReq(map[string]any{
		"key": "k", "passphrase": "correct horse battery staple",
	})); !refusedAsNoValue(err) {
		t.Errorf("the CLI with no terminal: want kv.set.novalue, got %v", err)
	}
	if len(*asked) != 0 {
		t.Errorf("asked for %q away from a terminal", *asked)
	}
}

func refusedAsNoValue(err error) bool {
	var ve *view.Error
	return errors.As(err, &ve) && ve.Code == "kv.set.novalue"
}
