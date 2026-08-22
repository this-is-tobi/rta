package kv

import (
	"encoding/json"
	"os"
	"strconv"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// storeVersion is stamped on every store this build writes. Its only job is
// to make the question below answerable without guessing, from here on.
const storeVersion = 2

// An entry's Value became []byte so that binary values survive a round trip
// (see the field's own comment). encoding/json writes a []byte as base64 and
// a string as itself, so that change also silently changed the on-disk
// format — and every store written before it stopped opening at all, with
// "illegal base64 data at input byte 0" and no way forward. The secrets were
// still there and still decryptable; only the last step, reading the
// plaintext back, refused.
//
// decodeStore reads either format.
//
// A store that carries a version stamp needs no guessing at all: the stamp is
// in the same JSON document as the values, so it is read first and believed.
// An earlier version of this function ignored it and inferred the format from
// whether the values parsed as base64 — while its own comment claimed
// "nothing on disk can settle it", which was untrue of every store this build
// had already written.
//
// Only an unstamped store still has to be guessed at, and there the fallback
// order is right: encoding/json rejects the whole document on the first value
// that is not base64, so one ordinary short secret is enough to identify the
// legacy format, and the decision is made once for the whole store — which is
// correct, because the format is a property of the writer.
//
// The residual ambiguity applies to unstamped stores alone: a legacy store in
// which *every* value happens to be valid base64 — short, correctly padded,
// drawn only from the base64 alphabet — reads as a current one and comes back
// as bytes nobody stored. Since the next write would then persist that, the
// original file is kept aside before any unstamped store is overwritten (see
// backupUnstamped), so the failure is recoverable rather than final.
func decodeStore(plaintext []byte) (store, *view.Error) {
	// The stamp cannot fail to parse on any value shape, so probing it is
	// safe on a document that will turn out to be either format.
	var stamp struct {
		Version int `json:"version"`
	}
	_ = json.Unmarshal(plaintext, &stamp)

	if stamp.Version >= storeVersion {
		var s store
		if err := json.Unmarshal(plaintext, &s); err != nil {
			return store{}, view.Errorf("kv.store.corrupt",
				"parsing decrypted store (format version %d): %v", stamp.Version, err)
		}
		return s, nil
	}

	var s store
	if err := json.Unmarshal(plaintext, &s); err == nil {
		return s, nil
	}
	l, err := decodeLegacyStore(plaintext)
	if err != nil {
		// Report the failure of the format it actually claims to be, not of
		// the fallback: "this is not a store" beats "this is not a v1 store".
		return store{}, view.Errorf("kv.store.corrupt", "parsing decrypted store: %v", err)
	}
	return l, nil
}

// legacyEntry is what an entry looked like when Value was a string.
type legacyEntry struct {
	Value       string    `json:"value"`
	Description string    `json:"description,omitempty"`
	Kind        string    `json:"kind,omitempty"`
	Filename    string    `json:"filename,omitempty"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
}

type legacyStore struct {
	Recipients []string               `json:"recipients,omitempty"`
	Entries    map[string]legacyEntry `json:"entries"`
}

func decodeLegacyStore(plaintext []byte) (store, error) {
	var l legacyStore
	if err := json.Unmarshal(plaintext, &l); err != nil {
		return store{}, err
	}
	s := store{Recipients: l.Recipients, Entries: make(map[string]entry, len(l.Entries))}
	for name, e := range l.Entries {
		s.Entries[name] = entry{
			Value:       []byte(e.Value),
			Description: e.Description,
			Kind:        e.Kind,
			Filename:    e.Filename,
			Created:     e.Created,
			Updated:     e.Updated,
		}
	}
	return s, nil
}

// backupUnstamped preserves a store written before the format was versioned,
// once, before the first write that would replace it.
//
// The upgrade path reads an unstamped store by inference (see decodeStore) and
// the next write persists whatever that inference produced. If the inference
// was wrong the original plaintext is gone from disk with nothing to compare
// against — a secrets store that quietly destroys a secret, which is the one
// failure this package must not have.
//
// The copy is of the encrypted file, so it is exactly as protected as the
// original and readable by the same keys. It is written once: a store that has
// already been through an upgrade write carries the stamp, so this does
// nothing on every subsequent save.
//
// A failure to back up is not a failure to save. The store is already open and
// the write is what the caller asked for; refusing it because a convenience
// copy could not be made would be the worse outcome.
func backupUnstamped() {
	path := storePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return // no store yet, or unreadable — either way there is nothing to keep
	}
	// Published, not written: this is the only copy of a store about to be
	// rewritten in a format the old code cannot read, so "kept once, whole"
	// is the entire requirement. Publish gives both — it refuses to overwrite
	// an existing backup, which is what the Stat guard here used to ask for
	// and could not deliver (two writes racing the same check both passed
	// it), and a failure leaves no file rather than a short one. A short one
	// was the worse outcome by far: it looks like a backup to the guard, so
	// it permanently prevented a real one from ever being taken.
	backup := path + ".pre-v" + strconv.Itoa(storeVersion) + ".bak"
	_, _ = atomicfile.Publish(backup, data, 0o600)
}

// stamped reports whether the store on disk already declares its format, so
// that the backup above is taken only on the one write that upgrades it.
// It reads the ciphertext only — the age header is enough to tell "there is a
// store here" from "there is not", and the decision itself is made by the
// caller, which has the decrypted document.
func needsBackup(loadedVersion int) bool { return loadedVersion < storeVersion }
