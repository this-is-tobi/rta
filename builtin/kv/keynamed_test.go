package kv

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A key padded with a character that draws as nothing is named in what kv
// says of it as the prompt names it (textclean.Record), with the character
// spelled out. %q leaves a Hangul filler as it is, so a paste refused at the
// prompt for "prod/db" and a filler — the prompt having asked "Value for" the
// key with the filler named — was refused, in the next line, for what read
// as "prod/db", and so was every other answer naming that key.
func TestWhatKVSaysOfAPaddedKeyNamesItAsThePromptDoes(t *testing.T) {
	setup(t)
	ctx := context.Background()
	padded := "prod/db" + string(rune(0x3164))
	named := textclean.Record(padded)
	if spelled := strings.Trim(strconv.QuoteRuneToASCII(0x3164), "'"); named == padded || !strings.Contains(named, spelled) {
		t.Fatalf("textclean.Record(%q) = %s, which does not spell the filler out", padded, named)
	}
	says := func(what string, v view.View, err error) {
		t.Helper()
		text := ""
		var ve *view.Error
		switch {
		case errors.As(err, &ve):
			text = ve.Message
		case err != nil:
			t.Fatalf("%s: %v", what, err)
		default:
			text = v.(view.Text).Body
		}
		if !strings.Contains(text, named) || strings.Contains(text, padded) {
			t.Errorf("%s says %q, which does not name the key as %s", what, text, named)
		}
	}
	const pass = "correct horse battery staple"

	stubPaste(t, "apiVersion: v1", true, nil)
	v, err := runSet(ctx, cliReq(map[string]any{"key": padded, "passphrase": pass}))
	says("a paste that spanned lines", v, err)
	stubValuePrompt(t, "-----BEGIN CERTIFICATE-----", nil)
	v, err = runSet(ctx, cliReq(map[string]any{"key": padded, "passphrase": pass}))
	says("a first line that opens a block", v, err)
	stubValuePrompt(t, "", nil)
	v, err = runSet(ctx, cliReq(map[string]any{"key": padded, "description": "x", "passphrase": pass}))
	says("a label for a key not stored", v, err)
	v, err = runGet(ctx, req(map[string]any{"key": padded}, false))
	says("a get of a key not stored", v, err)

	v, err = runSet(ctx, req(map[string]any{"key": padded, "value": "v", "passphrase": pass}, false))
	says("a set", v, err)
	v, err = runRename(ctx, req(map[string]any{"key": padded, "new-name": padded, "passphrase": pass}, false))
	says("a rename onto itself", v, err)
	v, err = runRemove(ctx, req(map[string]any{"key": padded, "passphrase": pass}, false))
	says("a remove", v, err)

	// A name ending in a slash is refused with the grant that already covers
	// it named in the hint, by the key, which drew as the bare folder there
	// while the message beside it spelled the filler out.
	folder := padded + "/"
	verr := checkKeyName(folder)
	if verr == nil {
		t.Fatalf("%s was not refused as a folder's name", textclean.Record(folder))
	}
	for _, text := range []string{verr.Message, verr.Hint} {
		if !strings.Contains(text, textclean.Record(folder)) || strings.Contains(text, folder) {
			t.Errorf("a folder's name is refused with %q, which does not name the key as %s",
				text, textclean.Record(folder))
		}
	}
}
