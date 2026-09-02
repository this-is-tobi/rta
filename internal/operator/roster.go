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
}

// Len reports how many keys are enrolled.
func (r Roster) Len() int {
	n := 0
	for _, es := range r.keys {
		n += len(es)
	}
	return n
}

// Entries hands the roster to the guard's remote mode: the same label/key
// pairs, in guard.OperatorKey's shape, so `rta grant guard remote` enrolls
// exactly what a roster file says and nothing re-parses it a second way.
func (r Roster) Entries() []guard.OperatorKey {
	var out []guard.OperatorKey
	for _, es := range r.keys {
		for _, e := range es {
			out = append(out, guard.OperatorKey{
				Label:     e.label,
				PublicKey: base64.StdEncoding.EncodeToString(e.key),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// Labels names every enrolled operator, sorted, for the startup banner.
func (r Roster) Labels() []string {
	var out []string
	for _, es := range r.keys {
		for _, e := range es {
			out = append(out, e.label)
		}
	}
	sort.Strings(out)
	return out
}

// LoadRoster reads a roster file: one "label base64-pubkey" pair per
// non-blank, non-comment line — the line RosterLine prints. The same
// permission discipline as mcp.LoadTokenFile, for the same reason spelled
// there: this file is the entire trust anchor for the operator channel, rta
// never writes it, and a permission check at load time is the only available
// guarantee that a wider write did not happen along the way. Group write or
// execute, or any world bit, refuses; group read only warns — unlike a token
// file the roster holds no secret at all, but a group-writable one is
// enrollment for whoever shares the group.
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
	seen := map[string]string{} // base64 pubkey -> label, to name a duplicate's other half
	labels := map[string]bool{} // one label per person: a shared label makes the audit line name a role
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return Roster{}, groupReadable, fmt.Errorf("%s:%d: expected \"label base64-pubkey\", got %q", path, i+1, line)
		}
		label, encoded := fields[0], fields[1]
		if verr := grant.CheckAgent(label); verr != nil || label == "" {
			return Roster{}, groupReadable, fmt.Errorf("%s:%d: label %q is not a valid operator label", path, i+1, label)
		}
		pub, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return Roster{}, groupReadable, fmt.Errorf("%s:%d: %q is not an ed25519 public key — "+
				"`rta operator status` on the operator's own machine prints the line to paste here", path, i+1, encoded)
		}
		if other, dup := seen[encoded]; dup {
			return Roster{}, groupReadable, fmt.Errorf("%s:%d: this key is already enrolled as %q — "+
				"one label per key, so the audit trail names one person", path, i+1, other)
		}
		if labels[label] {
			return Roster{}, groupReadable, fmt.Errorf("%s:%d: %q is already enrolled — "+
				"one key per label, or the audit trail names a role instead of a person", path, i+1, label)
		}
		seen[encoded] = label
		labels[label] = true
		fp := passkey.Fingerprint(pub)
		r.keys[fp] = append(r.keys[fp], rosterEntry{label: label, key: ed25519.PublicKey(pub)})
	}
	if r.Len() == 0 {
		return Roster{}, groupReadable, fmt.Errorf("%s enrolls no operators", path)
	}
	return r, groupReadable, nil
}
