// Package seal is rta's state key: the 0600 secret that says a file in the
// data directory was written by rta rather than by whatever else could
// write there, and the MAC that proves it.
//
// It exists because two security features need the identical primitive and
// two implementations of "create a key, refuse to regenerate it on the read
// path" is one too many. internal/grant seals the authorization file;
// internal/consent seals the decisions that answer a parked call and
// internal/agentlog chains the record of what agents did.
//
// WHAT THIS DEFENDS, EXACTLY — the bound matters more than the mechanism,
// and it is stated once here for every caller.
//
// It stops a writer that cannot read the directory it is writing to. That
// is not a contrivance: it is precisely the shape a filesystem sandbox
// creates. A confined plugin under the deny set is refused reads of rta's
// data directory and was never refused writes, so it could blind-overwrite
// a state file — no read required, since the structs are public — and hand
// itself standing authority. Forging a seal needs the key, and reading the
// key needs the read the sandbox denies.
//
// It does NOT stop anything that can read this directory. Same-uid means no
// secret here is a secret from an attacker at that uid: they read the key
// and seal their own file. What sealing adds is that the two cases are now
// different, where before there was one case and it was lost.
//
// A MAC and not encryption, because the files it protects are meant to be
// readable — "what is this agent allowed to do" and "what did it do" are
// questions worth answering without unlocking anything. A plaintext file
// that cannot be forged keeps that.
package seal

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
	"github.com/this-is-tobi/rule-them-all/internal/paths"
)

// The two conditions a caller has to be able to tell apart, because they
// mean different things to the operator: a sealed file with no key beside
// it was not written by this rta, and a key too short to be one is a
// truncation that must not be papered over with a fresh key — that would
// reject every file sealed with the original as forged, which is a security
// alarm raised by the recovery rather than by the incident.
var (
	ErrMissing = errors.New("no seal key")
	ErrShort   = errors.New("seal key is too short")
)

// Path is where a named key lives: in the data directory, beside the file it
// authenticates, which is the whole of its threat model.
func Path(name string) string { return filepath.Join(paths.Data(), name) }

// Key loads the named key, creating it only when create is true.
//
// create is false on the read path, and that asymmetry is the point: a
// missing key there means the file being checked was written by something
// that did not have one, and generating a fresh key to check it against
// would turn "unforgeable" into "regenerate and accept".
func Key(name string, create bool) ([]byte, error) {
	raw, err := os.ReadFile(Path(name))
	if err == nil && len(raw) >= 32 {
		return raw, nil
	}
	if !create {
		return nil, ErrMissing
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating a seal key: %w", err)
	}
	if err := os.MkdirAll(paths.Data(), 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", paths.Data(), err)
	}
	// 0600, and written before whatever it authenticates, so there is never
	// a moment where a sealed file exists with no key to check it.
	//
	// Published rather than written, on behalf of a reader that takes no
	// lock: a plain os.WriteFile truncates first, and a reader that caught
	// the key mid-write read fewer than 32 bytes and concluded, out loud,
	// that the file "was not written by rta" — rta accusing itself of
	// forgery is the worst possible reading of a transient, and the same
	// truncation left permanently by a crash was worse still.
	//
	// Publish also settles which key wins when two writers race: a second
	// key silently replacing the first invalidates everything sealed with
	// the first, so the loser adopts the winner's key instead of
	// overwriting it.
	stored, err := atomicfile.Publish(Path(name), key, 0o600)
	if err != nil {
		return nil, fmt.Errorf("writing %s: %w", Path(name), err)
	}
	if len(stored) < 32 {
		return nil, ErrShort
	}
	return stored, nil
}

// MAC returns the hex HMAC-SHA256 of data under key.
func MAC(key, data []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// Equal compares two MACs without leaking where they differ.
//
// Constant-time even though a MAC is not a secret: the value being compared
// is derived from one, and a comparison whose duration depends on the
// prefix is exactly how a forgery gets built one byte at a time.
func Equal(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}
