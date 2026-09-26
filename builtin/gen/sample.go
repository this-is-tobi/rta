package gen

import (
	"context"
	"fmt"
	"strings"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A generator is only half of what somebody reaching for one needs. The
// other half is knowing which shape to ask for — a login password and a
// server-side encryption key are both "a random string" and are not
// interchangeable, and the flags that tell them apart are exactly what you
// do not remember at the moment you need one. So the overview generates the
// common shapes side by side, each labelled with what it is for, how much
// entropy it actually carries, and the call that reproduces it. Pick the
// row, or copy the call and keep it.

// recipe is one offered shape: what it is for, the call that produces it,
// and the capability and inputs that reproduce it outside this view.
//
// Both text fields are kept short on purpose. A row has to carry a 64-cell
// value — a 32-byte key in hex — and every cell beside it is competing for
// the same line, so the difference between "for anything with a strength
// policy" and "strength policies" is the difference between a table that
// reads and one that reflows. The long-form reasoning lives in the prose
// sections, which is where prose belongs.
type recipe struct {
	use  string       // what it is for, in a few words
	id   string       // the capability that reproduces it
	with []plugin.Arg // what that capability is given, in the order a command line spells it
	make func() (value string, bits float64, err error)
}

func passwordRecipe(use string, spec passwordSpec, with ...plugin.Arg) recipe {
	return recipe{use: use, id: "gen.password", with: with, make: func() (string, float64, error) {
		v, err := spec.generate()
		return v, spec.bits(), err
	}}
}

func tokenRecipe(use string, n int, encoding string, with ...plugin.Arg) recipe {
	return recipe{use: use, id: "gen.token", with: with, make: func() (string, float64, error) {
		v, err := token(n, encoding)
		return v, float64(n) * 8, err
	}}
}

// call is how the caller on sf reproduces r, for the Command column, spelled
// by the SDK (plugin.Surface.Call) with the "rta" a terminal types left
// implied, as the column always has.
//
// A row an agent read over MCP used to hold `gen password --length 24
// --symbols`: a command line it has no terminal for, with flags its tool's
// schema does not have. The column is the same contract on every surface —
// how to get another value of this shape — so what fills it follows the
// surface.
func (r recipe) call(sf plugin.Surface) string {
	return strings.TrimPrefix(sf.Call(r.id, r.with...), "rta ")
}

// lengthArg, encodingArg and switchedOn are the inputs the recipes give,
// spelled once.
func lengthArg(n int) plugin.Arg         { return plugin.Arg{Name: "length", Value: n} }
func encodingArg(name string) plugin.Arg { return plugin.Arg{Name: "encoding", Value: name} }
func switchedOn(input string) plugin.Arg { return plugin.Arg{Name: input, Value: true} }

// The alphabets behind the offered passwords, named once so a recipe reads
// as a decision rather than five booleans.
var (
	alnum      = alphabet(true, true, true, false, false)
	alnumSyms  = alphabet(true, true, true, true, false)
	alnumClear = alphabet(true, true, true, false, true)
)

// thirtyTwoChars is the shape people mean by "a 32-character key". Named
// because keyNote has to be able to state what it actually carries.
var thirtyTwoChars = passwordSpec{length: 32, alphabet: alnumSyms}

// passwordRecipes are for a human to type, paste or read aloud.
var passwordRecipes = []recipe{
	passwordRecipe("logins", passwordSpec{length: 20, alphabet: alnum}),
	passwordRecipe("strength policies", passwordSpec{length: 24, alphabet: alnumSyms},
		lengthArg(24), switchedOn("symbols")),
	passwordRecipe("read aloud, write down", passwordSpec{length: 20, alphabet: alnumClear},
		switchedOn("exclude-ambiguous")),
	passwordRecipe("service accounts", passwordSpec{length: 40, alphabet: alnumSyms},
		lengthArg(40), switchedOn("symbols")),
}

// keyRecipes are for a program to consume: config values, env vars, key
// material. The two 32-shaped rows are the point of the section — see
// keyNote for why they are not the same thing.
var keyRecipes = []recipe{
	// The default length and encoding are already 32 bytes of hex, so the
	// commonest row is also the shortest call.
	tokenRecipe("AES-256 key", 32, "hex"),
	tokenRecipe("env vars", 32, "base64", encodingArg("base64")),
	passwordRecipe("32-char field", thirtyTwoChars, lengthArg(32), switchedOn("symbols")),
	tokenRecipe("URL-safe", 32, "base64url", encodingArg("base64url")),
	tokenRecipe("authenticator apps", 20, "base32", lengthArg(20), encodingArg("base32")),
}

// keyNote is the correction worth printing next to the values rather than
// leaving somebody to find out later. "The key must be 32 characters" is
// widespread shorthand for AES-256, and it is off by a quarter: AES-256
// takes 32 *bytes* — 256 bits of key material. A printable character spans
// only a fraction of a byte's 256 values, so thirty-two of them fall well
// short of 256 bits. Strong for a password; not the key the algorithm is
// named after.
//
// The figure is computed from the very alphabet the row above it used, so
// the note cannot drift away from the value it is explaining.
func keyNote() string {
	return fmt.Sprintf(
		"AES-256 wants 32 *bytes* — 256 bits of key material. Thirty-two printable characters "+
			"(from the %d-character alphabet above) carry %.0f bits, not 256: a printable character "+
			"only spans part of a byte's range. Strong for a password, short of the key AES-256 is "+
			"named after — prefer the hex or base64 rows unless a field genuinely refuses anything "+
			"but 32 characters.",
		len(thirtyTwoChars.alphabet), thirtyTwoChars.bits())
}

// uuidRecipes: v4 unless something needs to sort by creation time.
var uuidRecipes = []recipe{
	{use: "random ids", id: "gen.uuid", make: func() (string, float64, error) {
		v, err := newUUID("4")
		return v, 122, err // 128 bits minus the 6 fixed version/variant bits
	}},
	{use: "sorts by creation", id: "gen.uuid", with: []plugin.Arg{{Name: "version", Value: "7"}},
		make: func() (string, float64, error) {
			v, err := newUUID("7")
			return v, 74, err // the rest is a millisecond timestamp
		}},
}

// runOverview is gen's sampler. It generates rather than describes: the
// value in front of you is a real one you can use, which is also why this
// backs a dashboard tile that refreshes — every refresh is a fresh set, and
// nothing here was ever anyone else's secret (package doc).
func runOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	if req.Bool("detail") {
		return detailedOverview(ctx, req)
	}
	// The tile has room for a handful of lines, so it offers the shapes
	// reached for most often; --detail (or opening the tile) has the rest.
	// It drops the command column the detail page carries: a tile is for
	// recognising the shape you want, and the call is what you want next.
	t := view.Table{Columns: []view.Column{
		{Name: "For"},
		{Name: "Value"},
		{Name: "Bits", Kind: view.KindNumber},
	}}
	for _, r := range []recipe{
		passwordRecipes[0], passwordRecipes[1],
		keyRecipes[1], keyRecipes[2],
		uuidRecipes[0],
	} {
		v, bits, err := r.make()
		if err != nil {
			return nil, view.Errorf("gen.rand.failed", "reading randomness: %v", err)
		}
		t.Rows = append(t.Rows, []string{r.use, v, fmt.Sprintf("%.0f", bits)})
	}
	t.Total = len(t.Rows)
	return t, nil
}

func detailedOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	sf := req.Surface()
	passwords, err := recipeTable(sf, passwordRecipes)
	if err != nil {
		return nil, err
	}
	keys, err := recipeTable(sf, keyRecipes)
	if err != nil {
		return nil, err
	}
	uuids, err := recipeTable(sf, uuidRecipes)
	if err != nil {
		return nil, err
	}
	p := plugin.NewPage(ctx, req)
	p.PutAs("passwords", "passwords", passwords)
	p.PutAs("keys", "keys & tokens", keys)
	p.PutAs("key-length", "about key length", view.Text{Body: keyNote()})
	p.PutAs("uuids", "uuids", uuids)
	return p.View(), nil
}

// recipeTable lays a group of recipes out as one row each: what it is for,
// the value, the entropy it carries, and the call that reproduces it on sf,
// the surface asking.
//
// A table rather than a stanza per recipe, because a table is what a
// catalogue is — four rows of the same four facts read as a comparison,
// which is the whole job here ("which of these do I want"), and the same
// facts stacked into paragraphs read as four separate things that happen to
// be adjacent.
//
// What a table costs is width, and the answer to that is shorter cells
// rather than a different shape: the labels and commands above are written
// to fit beside a 64-cell value, the "rta " prefix is implied, and the long
// reasoning that used to sit in a fifth column is prose in its own section.
// Where the terminal is still too narrow, the renderer shrinks columns and
// wraps inside the cell, which keeps every character of a value present and
// selectable — it is only ever the line that breaks, never the value.
func recipeTable(sf plugin.Surface, recipes []recipe) (view.Table, error) {
	t := view.Table{Columns: []view.Column{
		{Name: "For"},
		{Name: "Value"},
		{Name: "Bits", Kind: view.KindNumber},
		{Name: "Command"},
	}}
	for _, r := range recipes {
		v, bits, err := r.make()
		if err != nil {
			return t, view.Errorf("gen.rand.failed", "reading randomness: %v", err)
		}
		t.Rows = append(t.Rows, []string{r.use, v, fmt.Sprintf("%.0f", bits), r.call(sf)})
	}
	t.Total = len(t.Rows)
	return t, nil
}
