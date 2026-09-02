package operator

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/guard"
	"github.com/this-is-tobi/rule-them-all/internal/passkey"
)

// Roster is the set of operator keys a server trusts, loaded once at
// startup and held in memory for the process's life. That lifetime is the
// pin: like the token file, the roster is never re-read, so a same-uid
// rewrite of the file changes nothing for the server that is actually
// listening — an attacker edits a file the process already stopped looking
// at, and the next restart is an operator action with the banner naming
// what loaded.
type Roster struct {
	// keys maps a fingerprint to every enrolled key wearing it. Normally one;
	// a genuine 32-bit collision costs one extra Verify attempt, not a wrong
	// answer, because the signature picks the key that made it.
	keys map[string][]rosterEntry
}

type rosterEntry struct {
	label string
	key   ed25519.PublicKey
	role  Role
}

// Role is what one enrolled key may do on this server. Enrollment itself
// stays the trust decision — a bare roster line means a full operator, as
// it has since the channel existed — and the annotation only subtracts,
// the same direction a team policy ceiling moves. The vocabulary is two
// words on purpose: "full" and "read" are the only distinction anything
// needs yet (a dashboard that watches versus a person who answers), and a
// finer grid would be permissions nobody asked for, invented ahead of use.
type Role string

const (
	// RoleFull is a bare roster line: every verb the server offers.
	RoleFull Role = "full"
	// RoleRead is `role=read`: the verbs that change nothing. The intended
	// occupant is a component, not a person — a status page or the Option-B
	// sidecar holds its own key, watches the queue and the grants, and a
	// compromise of that component's key can publish nothing but reads.
	RoleRead Role = "read"
)

// Allows reports whether a role covers a verb. The read allowlist is
// spelled verb by verb, so a verb this build has never heard of — or one
// added later without classifying it here — is refused for read-only
// keys rather than granted by omission; and the zero Role, which only a
// bug could produce (LoadRoster always sets one), allows nothing at all.
func (ro Role) Allows(verb string) bool {
	switch ro {
	case RoleFull:
		return true
	case RoleRead:
		switch verb {
		case VerbStatus, VerbGrantList, VerbConsentList, VerbLockList:
			return true
		}
	}
	return false
}

// Len reports how many keys are enrolled.
func (r Roster) Len() int {
	n := 0
	for _, es := range r.keys {
		n += len(es)
	}
	return n
}

// Entries hands the roster to the guard's remote mode: label/key pairs in
// guard.OperatorKey's shape, so `rta grant guard remote` enrolls exactly
// what a roster file says and nothing re-parses it a second way — minus
// the read-only rows. A guard entry is grant-signing trust: loadAll
// honours any signature one of these keys makes, with no verb dispatch in
// between to ask what the key was enrolled *for*. So the role gate on the
// wire has this as its belt: a read-only key never crosses into the guard
// at all, and even a dispatch bug that let its issue call through would
// produce a grant the store refuses to load.
func (r Roster) Entries() []guard.OperatorKey {
	var out []guard.OperatorKey
	for _, es := range r.keys {
		for _, e := range es {
			if e.role != RoleFull {
				continue
			}
			out = append(out, guard.OperatorKey{
				Label:     e.label,
				PublicKey: base64.StdEncoding.EncodeToString(e.key),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// Operators names every enrolled key with its role, sorted by label — the
// startup banner's and the status verb's shared view of who this server
// answers to.
func (r Roster) Operators() []OperatorInfo {
	var out []OperatorInfo
	for _, es := range r.keys {
		for _, e := range es {
			out = append(out, OperatorInfo{Label: e.label, Role: e.role})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// LoadRoster reads a roster file: one "label base64-pubkey" pair per
// non-blank, non-comment line — the line RosterLine prints — optionally
// followed by key=value annotations, of which `role=read` is the only one
// yet. Anything unrecognized in the annotation position refuses the whole
// load rather than falling back to a full enrollment: `roel=read` silently
// meaning "full operator" is exactly the failure a restriction annotation
// must not have. The same permission discipline as mcp.LoadTokenFile, for
// the same reason spelled there: this file is the entire trust anchor for
// the operator channel, rta never writes it, and a permission check at
// load time is the only available guarantee that a wider write did not
// happen along the way. Group write or execute, or any world bit, refuses;
// group read only warns — unlike a token file the roster holds no secret
// at all, but a group-writable one is enrollment for whoever shares the
// group.
func LoadRoster(path string) (Roster, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return Roster{}, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Roster{}, false, err
	}
	groupReadable := false
	if runtime.GOOS != "windows" {
		mode := info.Mode().Perm()
		if mode&0o037 != 0 {
			return Roster{}, false, fmt.Errorf(
				"%s has weak permissions (mode %s) — someone besides its owner can write or execute "+
					"it, or any account on this machine can read it, and it decides who may operate "+
					"this server; chmod 600 it", path, mode)
		}
		groupReadable = mode&0o040 != 0
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return Roster{}, groupReadable, err
	}
	r := Roster{keys: map[string][]rosterEntry{}}
	seen := map[string]string{} // decoded key bytes -> label, to name a duplicate's other half
	labels := map[string]bool{} // one label per person: a shared label makes the audit line name a role
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return Roster{}, groupReadable, fmt.Errorf("%s:%d: expected \"label base64-pubkey [role=read]\", got %q", path, i+1, line)
		}
		label, encoded := fields[0], fields[1]
		role := RoleFull
		roleSet := false
		for _, annotation := range fields[2:] {
			k, v, cut := strings.Cut(annotation, "=")
			if !cut || k != "role" {
				return Roster{}, groupReadable, fmt.Errorf("%s:%d: %q is not an annotation this rta knows — "+
					"only role=read (or role=full, the default) — and guessing here could enroll more than "+
					"the line means", path, i+1, annotation)
			}
			if roleSet {
				return Roster{}, groupReadable, fmt.Errorf("%s:%d: role is set twice — one role per key", path, i+1)
			}
			roleSet = true
			switch Role(v) {
			case RoleFull:
				role = RoleFull
			case RoleRead:
				role = RoleRead
			default:
				return Roster{}, groupReadable, fmt.Errorf("%s:%d: %q is not a role — \"read\" restricts this "+
					"key to status, grant.list, consent.list and lock.list; \"full\" (or no annotation) is everything", path, i+1, v)
			}
		}
		if verr := grant.CheckAgent(label); verr != nil || label == "" {
			return Roster{}, groupReadable, fmt.Errorf("%s:%d: label %q is not a valid operator label", path, i+1, label)
		}
		// Strict, because base64 is not injective by default: a 32-byte key
		// ends its encoding with two padding bits the lenient decoder ignores,
		// so four spellings name one key. This file's one-label-per-key rule
		// is an anti-impersonation check, and with roles it became a security
		// boundary — the same key enrolled twice, once role=read and once
		// bare, would let a watching key answer as a full operator and cross
		// into the guard's signing set. Strict decoding leaves one spelling
		// per key (the one RosterLine prints), and the dedup below keys on
		// the decoded bytes anyway, so neither layer's removal reopens this.
		pub, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return Roster{}, groupReadable, fmt.Errorf("%s:%d: %q is not an ed25519 public key — "+
				"`rta operator status` on the operator's own machine prints the line to paste here", path, i+1, encoded)
		}
		if other, dup := seen[string(pub)]; dup {
			return Roster{}, groupReadable, fmt.Errorf("%s:%d: this key is already enrolled as %q — "+
				"one label per key, so the audit trail names one person", path, i+1, other)
		}
		if labels[label] {
			return Roster{}, groupReadable, fmt.Errorf("%s:%d: %q is already enrolled — "+
				"one key per label, or the audit trail names a role instead of a person", path, i+1, label)
		}
		seen[string(pub)] = label
		labels[label] = true
		fp := passkey.Fingerprint(pub)
		r.keys[fp] = append(r.keys[fp], rosterEntry{label: label, key: ed25519.PublicKey(pub), role: role})
	}
	if r.Len() == 0 {
		return Roster{}, groupReadable, fmt.Errorf("%s enrolls no operators", path)
	}
	return r, groupReadable, nil
}
