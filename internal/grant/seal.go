package grant

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
	"github.com/this-is-tobi/rule-them-all/internal/paths"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The grant file's authority came from its location and nothing else: loadAll
// was os.ReadFile plus json.Unmarshal, so anything that could write to the
// data directory could author the answer to "what is this agent allowed to
// do". A sealed file is one that says who wrote it.
//
// WHAT THIS DEFENDS, EXACTLY — the bound matters more than the mechanism.
//
// It stops a writer that cannot read the directory it is writing to. That is
// not a contrivance: it is precisely the shape a filesystem sandbox creates.
// A confined plugin under the deny set is refused reads of rta's data
// directory and was never refused writes, so it could blind-overwrite
// grants.json — no read required, since the Grant struct is public — and
// hand itself a standing grant over every kv capability with a far-future
// expiry. That was reproduced end to end: a refused kv.get became the secret
// after an 82-byte write. Sealing closes it, because forging the seal needs
// the key and reading the key needs the read the sandbox denies.
//
// It does NOT stop anything that can read this directory. Same-uid means no
// secret here is a secret from an attacker at that uid: they read the key and
// seal their own file. §4.7.10 already says this about the age identity —
// "an agent with any file-reading tool can take the key and the ciphertext
// and decrypt the store itself, never touching kv.get or its grants" — and
// the same sentence is true of grants. What sealing adds is that the two
// cases are now different: an attacker who can only write is stopped, and an
// attacker who can read as well is not, where before there was one case and
// it was lost.
//
// A MAC and not encryption, because §4.7.11's promise that "what can the
// agent do right now?" is answerable without unlocking anything is worth
// keeping, and a plaintext file that cannot be forged keeps it.
//
// The precedent is one directory over: builtin/kv/crypt.go's writeKeys
// already refuses when kv.recipients disagrees with the recipient list
// embedded in the ciphertext, for exactly this reason — a file with no
// cryptographic tie to what it describes, writable by anyone who can write to
// the data directory.

const keyFile = "grants.key"

// keyPath is where the seal key lives. 0600, beside the file it authenticates
// — which is the whole of its threat model, see above.
func keyPath() string { return filepath.Join(paths.Data(), keyFile) }

// sealKey loads the key, creating it on first use.
//
// create is false on the read path: a missing key there means the grant file
// was written by something that did not have one, and generating a fresh key
// to check it against would turn "unforgeable" into "regenerate and accept".
func sealKey(create bool) ([]byte, *view.Error) {
	raw, err := os.ReadFile(keyPath())
	if err == nil && len(raw) >= 32 {
		return raw, nil
	}
	if !create {
		// Only reached with a grant file already in hand, so this is not the
		// "no grants yet" case — that one returns before any key is wanted.
		// A grant file with no key beside it was written by something that
		// did not have one, which is the same conclusion as a bad seal
		// reached by a shorter route.
		return nil, view.Errorf("core.grant.unsealed",
			"%s exists with no seal key beside it, so it was not written by rta", Path()).
			WithHint("no grant is honoured until this is resolved; `rm " + Path() +
				"` clears every grant, and any that were legitimate can be re-issued")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, view.Errorf("core.grant.write", "generating a seal key: %v", err)
	}
	if err := os.MkdirAll(paths.Data(), 0o755); err != nil {
		return nil, view.Errorf("core.grant.write", "creating %s: %v", paths.Data(), err)
	}
	// 0600, and written before the grants it authenticates, so there is never
	// a moment where a sealed file exists with no key to check it.
	//
	// Published rather than written, for the same reason Save is atomic and
	// on behalf of the same reader: Load is called from Check on every gated
	// MCP call and takes no lock, so it can land in the middle of a plain
	// os.WriteFile — which truncates first. A reader that caught the key file
	// mid-write read fewer than 32 bytes and concluded, out loud, that the
	// grant file "was not written by rta", advising the operator to delete
	// every grant they had. rta accusing itself of forgery is the worst
	// possible reading of a transient. The same truncation left permanently
	// by a crash or a full disk was worse still, because nothing recovered
	// from it.
	//
	// Publish also settles which key wins. Two writers are serialized by
	// acquireLock today, but that is a fact about the callers rather than
	// about this function, and a second key silently replacing the first
	// invalidates every grant sealed with the first — so the loser adopts the
	// winner's key instead of overwriting it.
	stored, err := atomicfile.Publish(keyPath(), key, 0o600)
	if err != nil {
		return nil, view.Errorf("core.grant.write", "writing %s: %v", keyPath(), err)
	}
	if len(stored) < 32 {
		// Something short was already there — a key truncated by the
		// os.WriteFile this replaced, most likely. Generating a fresh one
		// over the top would reject every grant sealed with the original as
		// forged, which is a security alarm raised by the recovery rather
		// than by the incident.
		return nil, view.Errorf("core.grant.unsealed",
			"%s is too short to be a seal key, so it was not written by this rta", keyPath()).
			WithHint("`rm " + keyPath() + " " + Path() + "` clears every grant and starts clean; " +
				"any that were legitimate can be re-issued")
	}
	return stored, nil
}

// seal returns the MAC for a grant file's bytes.
func seal(key, data []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// sealed is the on-disk shape: the grants, and a MAC over them.
//
// The MAC covers the grants and not the whole document, because a document
// containing its own MAC needs a rule for what that field held while the MAC
// was computed, and every such rule is a place for the writer and the reader
// to disagree.
type sealed struct {
	Seal   string  `json:"seal"`
	Grants []Grant `json:"grants"`
}

// canonical is the byte form the MAC is taken over.
//
// Re-encoded compactly on both sides rather than MACing the bytes as they sit
// in the file, so the seal describes the grants and not their formatting.
// Holding the inner JSON as a RawMessage and MACing that was the obvious
// first version and is wrong: json.MarshalIndent re-indents everything it
// emits, embedded RawMessage included, so the bytes sealed and the bytes
// written differed and every file failed its own check. Two representations
// of the same value is exactly the trap the comment above is about, one level
// down.
//
// **It therefore covers the fields the WRITING build declared, and is checked
// against the fields the READING build declares** — which are the same set
// only while both builds agree, and the two directions of disagreement behave
// differently:
//
//   - Upgrading is safe, and `omitempty` is what makes it so. A field a newer
//     build added is absent from every grant an older one wrote, so both sides
//     encode identical bytes and every existing seal still verifies. That is
//     the guarantee the Profile and ProfilePin field comments claim, and it
//     holds.
//   - Downgrading is not. Once a grant actually *populates* a new field, the
//     newer build sealed over bytes that include it; an older build drops it
//     on unmarshal, re-encodes without it, and the MAC fails. See unknown()
//     for what that must not be reported as.
//
// The second half was got wrong here once, in the direction that matters: a
// test that injected an unknown field into a file this build had already
// sealed proved only that a field added *after* sealing is not covered, which
// is true and is not the question. The question is what happens to a file
// sealed *with* one.
func canonical(grants []Grant) ([]byte, error) { return json.Marshal(grants) }

// unknown reports the grant field names in data that this build does not
// declare, sorted and deduplicated.
//
// **So that a downgrade is not reported as an attack.** Every seal mismatch
// answered `core.grant.forged` — "it was written by something other than rta"
// — with a hint to delete the file. Run an older rta against a grant file a
// newer one wrote, once any grant populates a field the older build lacks,
// and rta accuses itself and then tells the operator to destroy every
// permission they hold. seal.go already names that shape the worst possible
// reading, for the truncated-key case; ProfilePin is the first field likely to
// be populated on an ordinary machine, and every field after it widens the
// same door.
//
// It changes the diagnosis and never the decision. An unknown field still
// fails the seal and still honours nothing, which is the only safe answer when
// the bytes and the MAC disagree — an attacker who appends junk earns a
// differently-worded refusal and no access. What it buys is that the sentence
// is true: this build genuinely cannot tell a newer writer from a modified
// file, and it now says so rather than picking the accusation.
//
// Derived from the struct rather than a list kept here, because a list goes
// stale on the day a field is added, which is the day it matters.
func unknown(data []byte) []string {
	var doc struct {
		Grants []map[string]json.RawMessage `json:"grants"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
	}
	known := declaredFields()
	seen := map[string]bool{}
	var out []string
	for _, g := range doc.Grants {
		for name := range g {
			if known[name] || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// declaredFields is the JSON names Grant declares.
func declaredFields() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(Grant{})
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}

// legacy reports whether data is a grant file written before the seal
// existed: the bare JSON array Save produced until v0.5.
//
// Such a file is treated as NO grants — not as the grants it contains, and
// not as an error. Both alternatives are wrong in a way worth stating.
//
// Honouring it would delete the seal. An attacker who can write this
// directory would simply write the old shape, and "unforgeable" would mean
// "unforgeable unless you ask nicely". A migration that re-seals what it
// finds is the same hole with more steps, because the thing being migrated
// is exactly the thing whose authorship is in question.
//
// Refusing it punishes an upgrade rather than an attack. loadAll is on the
// read path of `grant list`, `rta doctor`, the dashboard tile and every
// gated MCP call, so one file rta itself wrote last week took all of them
// down at once with a JSON parse error about an internal type name.
//
// Dropping fails closed — the result is fewer permissions, never more, so
// the security outcome is identical to refusing — and it costs the operator
// only the grants, which are minutes-to-hours by construction and are
// re-issued with one command. It is not silent: Legacy lets the surfaces a
// person actually looks at say what happened.
func legacy(data []byte) bool {
	for _, b := range data {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		}
		return b == '['
	}
	return false
}

// Legacy reports whether a pre-seal grant file is sitting on disk, so the
// human-facing surfaces can explain the grants that vanished instead of
// leaving somebody to wonder.
//
// It re-reads the file rather than threading a flag out of loadAll: the two
// callers are `grant list` and `rta doctor`, both of which a person is
// waiting on, and neither is worth a second return value on the path every
// MCP call takes.
func Legacy() bool {
	data, err := os.ReadFile(Path())
	return err == nil && legacy(data)
}
