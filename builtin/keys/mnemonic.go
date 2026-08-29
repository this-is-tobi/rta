package keys

import (
	"errors"
	"fmt"

	"github.com/tyler-smith/go-bip39"
)

// toMnemonic renders a 32-byte ed25519 seed as 24 space-separated BIP39
// words: 256 bits of entropy plus an 8-bit checksum, at 11 bits per word.
func toMnemonic(seed []byte) (string, error) {
	words, err := bip39.NewMnemonic(seed)
	if err != nil {
		return "", fmt.Errorf("encoding the seed as words: %w", err)
	}
	return words, nil
}

// fromMnemonic recovers the entropy a BIP39 phrase encodes — the seed
// toMnemonic started from, for a phrase this package produced.
//
// go-bip39 validates the embedded checksum before returning anything, which
// is what catches a single mistyped, dropped or reordered word: the decoded
// seed either matches the phrase's own checksum or the call fails outright,
// rather than silently handing back 32 bytes that happen to be wrong.
//
// The error is deliberately rebuilt from scratch rather than wrapped: for a
// word that is not in the BIP39 list at all, go-bip39 (bip39.go, EntropyFromMnemonic)
// returns an ad-hoc `fmt.Errorf("word `%v` not found in reverse map", v)`
// that embeds the offending word verbatim. These words are private key
// material — runBackup's own warning says not to put them anywhere the key
// file itself would not go — so that text must never reach a renderer,
// even as one word out of twenty-four. Only the two exported sentinels,
// whose text is fixed and never carries input, are allowed through as-is;
// everything else, including that one and anything future versions of the
// dependency might add, gets a fixed, safe message instead. Found by review:
// the original version of this function passed the
// error straight through.
func fromMnemonic(words string) ([]byte, error) {
	seed, err := bip39.EntropyFromMnemonic(words)
	if err == nil {
		return seed, nil
	}
	switch {
	case errors.Is(err, bip39.ErrInvalidMnemonic):
		return nil, errors.New("invalid phrase: wrong number of words")
	case errors.Is(err, bip39.ErrChecksumIncorrect):
		return nil, errors.New("checksum mismatch: a word is wrong, out of order, or missing")
	default:
		return nil, errors.New("one or more words are not valid BIP39 words")
	}
}
