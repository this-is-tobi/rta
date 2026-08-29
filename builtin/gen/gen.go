// Package gen is the built-in generator plugin: cryptographically random
// passwords, tokens and UUIDs. Pure computation, no network, no state — the
// only entropy source anywhere in it is crypto/rand.
//
// Nothing here reveals a secret the caller did not already have, or uses one
// on their behalf — the kv.get precedent is about crossing
// the line between "protected at rest" and "revealed", and there is no "at
// rest" here: every value is synthesized fresh, with no prior owner, and
// handed straight back to the caller who asked for it. So everything below
// stays Read, needs no grant, and has nothing for --dry-run to preview.
package gen

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/google/uuid"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

const (
	lowerChars  = "abcdefghijklmnopqrstuvwxyz"
	upperChars  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digitChars  = "0123456789"
	symbolChars = "!@#$%^&*()-_=+[]{}<>?"
	// ambiguousChars are dropped by --exclude-ambiguous: characters that are
	// easy to mistype or misread from a printed or handwritten copy.
	ambiguousChars = "0O1lI"

	// Caps on numeric inputs. Table's contract already forbids streaming
	// unbounded rows into a slice; gen is the first plugin
	// whose own numeric fields can drive an unbounded loop, so it needs its
	// own bound rather than inheriting one.
	maxCount          = 1000
	maxPasswordLength = 1024
	maxTokenBytes     = 4096
)

// Plugin returns the gen plugin declaration.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "gen",
		Summary: "Generate passwords, tokens and UUIDs — offline, crypto/rand only",
		Capabilities: []plugin.Capability{
			{
				ID:      "gen.overview",
				Summary: "A sampler of the shapes worth generating, ready to use",
				// Being called `overview` is what makes this gen's dashboard
				// tile, so the decision lives here rather than in a map inside
				// the TUI: the tile refreshes on a timer with fresh, real,
				// usable secrets on it, and the shoulder-surf and screen-share
				// risk that carries was raised explicitly and accepted — on the
				// same "H hides it" basis every other tile's visibility rests
				// on, and the same basis kv.status and grant.list already show
				// state some viewers would rather not have on a projector. It
				// is the overview and not gen.password because the question at
				// a glance is "which shape do I need" — a password, an actual
				// 32-byte key, a UUID — and a column of passwords cannot answer
				// it.
				//
				// Wide enough for a 44-character base64 key and its label.
				// Everything here is a real value somebody is meant to read
				// and copy, and a value that wrapped or got an ellipsis is
				// not one — this is the one tile whose content has a natural
				// minimum width rather than a natural amount.
				MinWidth: 64,
				Description: "Generates one of each common shape side by side — passwords, encryption " +
					"key material, URL-safe and TOTP tokens, UUIDs — labelled with what it is for, the " +
					"entropy it actually carries and the command that reproduces it, so picking the " +
					"right shape does not require remembering the flags first. With --detail: every " +
					"preset, plus why a \"32-character key\" is not a 32-byte one. Every value is real " +
					"and freshly generated; nothing is an example.",
				Safety:     plugin.Read,
				Idempotent: false, // fresh randomness every call, by design
				Detailed:   true,
				Run:        runOverview,
			},
			{
				ID:      "gen.password",
				Summary: "Generate a cryptographically random password",
				Description: "crypto/rand only, with unbiased alphabet selection (rand.Int, never " +
					"rand.Intn or modulo, which biases low indices unless the alphabet size divides " +
					"the RNG range evenly). Reports entropy in bits: length x log2(alphabet size).",
				Safety: plugin.Read,
				Inputs: []plugin.Field{
					{Name: "length", Type: plugin.Int, Default: 20, Min: 1, Max: 4096, Help: "characters"},
					{Name: "no-lower", Type: plugin.Bool, Help: "exclude lowercase letters"},
					{Name: "no-upper", Type: plugin.Bool, Help: "exclude uppercase letters"},
					{Name: "no-digits", Type: plugin.Bool, Help: "exclude digits"},
					{Name: "symbols", Type: plugin.Bool, Help: "include symbols: " + symbolChars},
					{Name: "exclude-ambiguous", Type: plugin.Bool, Help: "drop look-alike characters: " + ambiguousChars},
					{Name: "count", Type: plugin.Int, Default: 1, Help: "how many to generate"},
				},
				Run: runPassword,
			},
			{
				ID:      "gen.token",
				Summary: "Generate random bytes, formatted as hex/base64/base32",
				Description: "crypto/rand bytes, formatted with --encoding. base32 at the right length " +
					"is already a TOTP secret (RFC 4648/6238) — there is no separate gen.totp.",
				Safety: plugin.Read,
				Inputs: []plugin.Field{
					{Name: "length", Type: plugin.Int, Default: 32, Min: 1, Max: 4096, Help: "bytes of entropy"},
					{Name: "encoding", Type: plugin.String, Default: "hex",
						Options: []string{"hex", "base64", "base64url", "base32"}, Help: "output encoding"},
				},
				Run: runToken,
			},
			{
				ID:      "gen.uuid",
				Summary: "Generate a UUID: v4 random (default) or v7 time-ordered",
				Description: "v1/v3/v5/v6 are deliberately not offered: v1 leaks the generating machine's " +
					"MAC address and a timestamp, v3/v5 hash a namespace+name instead of generating " +
					"anything (a different operation), and v6 is superseded by v7 for the same " +
					"time-ordering goal, per RFC 9562's own recommendation.",
				Safety: plugin.Read,
				Inputs: []plugin.Field{
					{Name: "version", Type: plugin.String, Default: "4", Options: []string{"4", "7"}, Help: "UUID version"},
					{Name: "count", Type: plugin.Int, Default: 1, Help: "how many to generate"},
				},
				Run: runUUID,
			},
		},
	}
}

// passwordSpec is a password's whole definition: how long, and drawn from
// what. Keeping it a value rather than a set of request lookups is what lets
// the overview offer named presets (sample.go) through the very code path
// `gen password` runs — one implementation of "what is a strong password
// here", not one per caller.
type passwordSpec struct {
	length   int
	alphabet string
}

// bits is the entropy a password of this shape carries: length x log2 of the
// alphabet size. It is the number that makes two presets comparable.
func (s passwordSpec) bits() float64 {
	if s.alphabet == "" {
		return 0
	}
	return float64(s.length) * math.Log2(float64(len(s.alphabet)))
}

func (s passwordSpec) generate() (string, error) { return randomString(s.alphabet, s.length) }

// alphabet assembles the character set from the classes asked for.
func alphabet(lower, upper, digits, symbols, excludeAmbiguous bool) string {
	out := ""
	if lower {
		out += lowerChars
	}
	if upper {
		out += upperChars
	}
	if digits {
		out += digitChars
	}
	if symbols {
		out += symbolChars
	}
	if excludeAmbiguous {
		out = dropChars(out, ambiguousChars)
	}
	return out
}

// specFrom reads a password shape out of a request.
func specFrom(req plugin.Request) (passwordSpec, *view.Error) {
	length := req.Int("length")
	if length <= 0 {
		length = 20
	}
	if length > maxPasswordLength {
		return passwordSpec{}, view.Errorf("gen.password.toolong", "length %d exceeds the %d-character limit", length, maxPasswordLength)
	}
	a := alphabet(!req.Bool("no-lower"), !req.Bool("no-upper"), !req.Bool("no-digits"),
		req.Bool("symbols"), req.Bool("exclude-ambiguous"))
	if a == "" {
		return passwordSpec{}, view.Errorf("gen.password.noalphabet", "every character class was excluded — nothing left to generate from").
			WithHint("drop --no-upper/--no-digits, or add --symbols")
	}
	return passwordSpec{length: length, alphabet: a}, nil
}

func runPassword(_ context.Context, req plugin.Request) (view.View, error) {
	spec, verr := specFrom(req)
	if verr != nil {
		return nil, verr
	}
	count, verr := boundedCount(req)
	if verr != nil {
		return nil, verr
	}

	t := view.Table{Columns: []view.Column{
		{Name: "Password"},
		{Name: "Entropy (bits)", Kind: view.KindNumber},
	}}
	for range count {
		pw, err := spec.generate()
		if err != nil {
			return nil, view.Errorf("gen.rand.failed", "reading randomness: %v", err)
		}
		t.Rows = append(t.Rows, []string{pw, fmt.Sprintf("%.1f", spec.bits())})
	}
	t.Total = len(t.Rows)
	return t, nil
}

// token returns n bytes of randomness in the named encoding. The byte count
// is the security parameter and the encoding is only how it is written down,
// which is why callers ask for bytes and never for an output length.
func token(n int, encoding string) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	switch encoding {
	case "base64":
		return base64.StdEncoding.EncodeToString(buf), nil
	case "base64url":
		return base64.URLEncoding.EncodeToString(buf), nil
	case "base32":
		return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
	default:
		return hex.EncodeToString(buf), nil
	}
}

func runToken(_ context.Context, req plugin.Request) (view.View, error) {
	length := req.Int("length")
	if length <= 0 {
		length = 32
	}
	if length > maxTokenBytes {
		return nil, view.Errorf("gen.token.toolong", "length %d exceeds the %d-byte limit", length, maxTokenBytes)
	}
	encoded, err := token(length, req.String("encoding"))
	if err != nil {
		return nil, view.Errorf("gen.rand.failed", "reading randomness: %v", err)
	}
	return view.KeyValue{Pairs: []view.Pair{
		{Key: "token", Value: encoded},
		{Key: "bytes", Value: fmt.Sprintf("%d", length)},
		{Key: "entropy", Value: fmt.Sprintf("%d bits", length*8)},
	}}, nil
}

func runUUID(_ context.Context, req plugin.Request) (view.View, error) {
	count, verr := boundedCount(req)
	if verr != nil {
		return nil, verr
	}
	version := req.String("version")
	t := view.Table{Columns: []view.Column{{Name: "UUID"}}}
	for range count {
		id, err := newUUID(version)
		if err != nil {
			return nil, view.Errorf("gen.rand.failed", "reading randomness: %v", err)
		}
		t.Rows = append(t.Rows, []string{id})
	}
	t.Total = len(t.Rows)
	return t, nil
}

func boundedCount(req plugin.Request) (int, *view.Error) {
	count := req.Int("count")
	if count <= 0 {
		count = 1
	}
	if count > maxCount {
		return 0, view.Errorf("gen.count.toomany", "count %d exceeds the %d limit", count, maxCount)
	}
	return count, nil
}

// dropChars removes every rune of drop from s.
func dropChars(s, drop string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(drop, r) {
			return -1
		}
		return r
	}, s)
}

// randomString draws length characters from alphabet using an unbiased
// selection: rand.Int against the exact alphabet size, never rand.Intn or a
// modulo reduction, both of which favor low indices unless the alphabet size
// happens to divide the RNG's range evenly.
func randomString(alphabet string, length int) (string, error) {
	buf := make([]byte, length)
	max := big.NewInt(int64(len(alphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = alphabet[n.Int64()]
	}
	return string(buf), nil
}

// newUUID generates one UUID of the named version. Anything but "7" is v4,
// matching the field's default rather than erroring on a value the schema
// already constrains.
func newUUID(version string) (string, error) {
	gen := uuid.NewRandom
	if version == "7" {
		gen = uuid.NewV7
	}
	id, err := gen()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
