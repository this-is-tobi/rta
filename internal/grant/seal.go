package grant

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/seal"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The grant file's authority came from its location and nothing else: loadAll
// was os.ReadFile plus json.Unmarshal, so anything that could write to the
// data directory could author the answer to "what is this agent allowed to
// do". A sealed file is one that says who wrote it.
//
// The mechanism and its exact bound now live in internal/seal, which grants
// share with the consent decisions and the agent ledger — the
// bound is unchanged and worth restating in one line: it stops a writer that
// cannot read (a confined plugin blind-overwriting grants.json, reproduced
// end to end: a refused kv.get became the secret after an 82-byte write),
// and it stops nothing that can read this directory, because that attacker
// reads the key and seals their own file.
//
// The precedent is one directory over: builtin/kv/crypt.go's writeKeys
// already refuses when kv.recipients disagrees with the recipient list
// embedded in the ciphertext, for exactly this reason — a file with no
// cryptographic tie to what it describes, writable by anyone who can write to
// the data directory.

const keyFile = "grants.key"

// keyPath is where the seal key lives. 0600, beside the file it authenticates
// — which is the whole of its threat model, see above.
func keyPath() string { return seal.Path(keyFile) }

// sealKey loads the key, creating it on first use.
//
// create is false on the read path: a missing key there means the grant file
// was written by something that did not have one, and generating a fresh key
// to check it against would turn "unforgeable" into "regenerate and accept".
func sealKey(create bool) ([]byte, *view.Error) {
	key, err := seal.Key(keyFile, create)
	switch {
	case err == nil:
		return key, nil
	case errors.Is(err, seal.ErrMissing):
		// Only reached with a grant file already in hand, so this is not the
		// "no grants yet" case — that one returns before any key is wanted.
		// A grant file with no key beside it was written by something that
		// did not have one, which is the same conclusion as a bad seal
		// reached by a shorter route.
		return nil, view.Errorf("core.grant.unsealed",
			"%s exists with no seal key beside it, so it was not written by rta", Path()).
			WithHint("no grant is honoured until this is resolved; `rm " + Path() +
				"` clears every grant, and any that were legitimate can be re-issued")
	case errors.Is(err, seal.ErrShort):
		// Something short was already there — a key truncated by a
		// non-atomic write, most likely. Generating a fresh one over the top
		// would reject every grant sealed with the original as forged, which
		// is a security alarm raised by the recovery rather than by the
		// incident.
		return nil, view.Errorf("core.grant.unsealed",
			"%s is too short to be a seal key, so it was not written by this rta", keyPath()).
			WithHint("`rm " + keyPath() + " " + Path() + "` clears every grant and starts clean; " +
				"any that were legitimate can be re-issued")
	default:
		return nil, view.Errorf("core.grant.write", "%v", err)
	}
}

// sealOf returns the MAC for a grant file's bytes.
func sealOf(key, data []byte) string { return seal.MAC(key, data) }

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
// maxGrantFile bounds every read of grants.json.
//
// The file is written by rta and read by rta, and in between it sits in a
// directory a lower-trust process can write to — the same reasoning
// internal/consent states for its own queue, and grants.json sits on a
// strictly hotter path than that queue: Reserve reads it before every gated
// MCP call. The read happens before the seal is checked (it has to — the seal
// is inside the bytes), so a forged file does not need to be *valid* to cost
// something, only large.
//
// A grant is a few hundred bytes and Issue collapses duplicates, so 256 KiB
// is several hundred live grants — past any real operator, and matched to the
// number the consent queue already chose for a file in the same directory.
const maxGrantFile = 256 << 10

func Legacy() bool {
	data, err := atomicfile.ReadCapped(Path(), maxGrantFile)
	return err == nil && legacy(data)
}
