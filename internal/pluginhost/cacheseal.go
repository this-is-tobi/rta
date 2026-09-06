package pluginhost

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"os"
	"path/filepath"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/paths"
)

// The cache is sealed because an unsealed one defeats the digest.
//
// Everything else in this package is built on binding an authorisation to an
// artifact rather than to a name: the binary is re-hashed on every
// Open, so a plugin that changed on disk is a different plugin and its
// declaration is re-read. The cache is the one thing that can assert a
// declaration for a digest *without* the bytes that produced it — so an
// unauthenticated entry is a way to change what rta believes about a binary
// while the binary, and therefore its digest, stays exactly as it was.
//
// Reproduced before this existed: writing one file into the data directory
// flipped hello.greet from Read to Destructive and replaced its summary with
// attacker-chosen text, and rta served both. The dangerous direction is the
// other one — Destructive to Read is the same write, and it puts a capability
// that needs a human-issued grant in front of an agent that has none. The declaration also carries Summary, Description and
// Options, which go to models verbatim, so the same write is a
// prompt-injection channel that survives restarts.
//
// No read of the data directory is needed for any of that: the proto shape is
// public. That is the same writer-without-reader shape the grant seal was
// added for, and this is the third instance of the pattern the codebase has
// named — builtin/kv/crypt.go's recipient check was the first, internal/grant
// the second. The bound is the same as theirs and worth restating: it stops a
// writer that cannot read, which is precisely what a filesystem sandbox
// creates. It does not stop an attacker who can read this directory, because
// they take the key and seal their own entry. Same-uid is not a boundary.
//
// The key is NOT kept in the cache directory. pruneCache deletes the oldest
// files there to bound its size, and a key it could delete is a key that
// silently invalidates every entry the first time somebody installs 128
// plugin builds.

const cacheKeyFile = "plugin-cache.key"

func cacheKeyPath() string { return filepath.Join(paths.Data(), cacheKeyFile) }

// sealKey loads the cache key, creating it on first use.
//
// It returns nil rather than an error, and every caller treats nil as "no
// cache". That is the whole failure policy of this file and it is much
// simpler than the grant seal's, deliberately: a grant that cannot be
// authenticated is a security decision that has to be reported, whereas a
// cache entry that cannot be authenticated costs one process launch and
// produces the identical answer. There is nothing here a person needs to act
// on, so there is no error surface to get wrong.
func sealKey(create bool) []byte {
	if raw, err := os.ReadFile(cacheKeyPath()); err == nil && len(raw) >= 32 {
		return raw
	}
	if !create {
		return nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil
	}
	if err := os.MkdirAll(paths.Data(), 0o755); err != nil {
		return nil
	}
	// Published rather than written, so the path either does not exist or
	// holds all 32 bytes, and a second process racing this one adopts the
	// winner's key instead of overwriting it — see atomicfile.Publish, which
	// carries the reasoning and which the grant seal key now shares. That was
	// two copies of this function, and only this one had been got right.
	stored, err := atomicfile.Publish(cacheKeyPath(), key, 0o600)
	if err != nil || len(stored) < 32 {
		return nil
	}
	return stored
}

// sealFor is the MAC an entry must carry.
//
// It covers the digest as well as the declaration, and that is not
// decoration. A MAC over the declaration alone would let an attacker move a
// legitimately sealed entry from one plugin's digest to another's filename —
// every byte authentic, every signature valid, and rta serving plugin A's
// capabilities as plugin B's. The name of the file an entry lives under is
// part of what the entry claims, so it is part of what gets signed.
//
// Both inputs are length-prefixed. The digest is fixed-width hex today and
// the concatenation would be unambiguous without it; writing it down anyway
// costs nothing and means the next field added here cannot introduce a
// collision by being variable-length.
func sealFor(key []byte, digest string, body []byte) []byte {
	mac := hmac.New(sha256.New, key)
	var n [8]byte
	putLen(&n, len(digest))
	mac.Write(n[:])
	mac.Write([]byte(digest))
	putLen(&n, len(body))
	mac.Write(n[:])
	mac.Write(body)
	return mac.Sum(nil)
}

func putLen(b *[8]byte, n int) {
	for i := 7; i >= 0; i-- {
		b[i] = byte(n)
		n >>= 8
	}
}
