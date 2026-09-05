package kv

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/this-is-tobi/rule-them-all/builtin/internal/itemstore"
	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// maxRevisions is how much of its past an entry keeps.
//
// Bounded because every revision is a full copy of a value inside the same
// encrypted document, and a certificate bundle rotated weekly for a year
// would otherwise be fifty copies of itself behind one key. Five is enough
// for the accident this exists for — a rotation that broke something, a
// paste over the wrong key, an edit saved half-done — and small enough that
// the store stays the size of its secrets rather than of their history.
const maxRevisions = 5

// revision is what an entry was before a write replaced it.
//
// A flat copy rather than the entry itself, so that a revision cannot carry
// revisions of its own: the chain lives on the live entry, in order, and
// restoring one of them pushes the current value onto that same chain —
// which is what makes a restore undoable by the same mechanism.
type revision struct {
	Value       []byte    `json:"value"`
	Description string    `json:"description,omitempty"`
	Kind        string    `json:"kind,omitempty"`
	Filename    string    `json:"filename,omitempty"`
	Origin      string    `json:"origin,omitempty"`
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
	Retired     time.Time `json:"retired"`
}

// removedEntry is an entry `kv rm` took out of the listing, kept whole —
// revisions included — until somebody restores it or purges it.
type removedEntry struct {
	entry
	RemovedAt time.Time `json:"removed_at"`
}

// retired returns the revisions a replacement of e should carry: e itself,
// on top of whatever e already kept, capped.
func (e entry) retired(now time.Time) []revision {
	r := revision{
		Value: e.Value, Description: e.Description, Kind: e.Kind, Filename: e.Filename,
		Origin: e.origin(), Created: e.Created, Updated: e.Updated, Retired: now,
	}
	out := append([]revision{r}, e.Previous...)
	if len(out) > maxRevisions {
		out = out[:maxRevisions]
	}
	return out
}

func historyCapability() plugin.Capability {
	return plugin.Capability{
		ID: "kv.history", Summary: "What a key held before, and when each value was replaced",
		Safety: plugin.Read, Idempotent: true,
		Description: "Every write over an existing key keeps what it replaced — the last " +
			strconv.Itoa(maxRevisions) + " values, inside the same encrypted store — and `kv rm` " +
			"keeps the whole entry aside rather than destroying it. This lists that past: kind, " +
			"size, description, when each value was set and when it was replaced. Never the values " +
			"themselves, which is what keeps it a Read; `kv restore` brings one back.",
		Inputs: unlockFields([]plugin.Field{
			{Name: "key", Type: plugin.String, Positional: true, Required: true, Help: "key to look back on",
				Suggest: suggestKeys},
		}...),
		Run: runHistory,
	}
}

func runHistory(_ context.Context, req plugin.Request) (view.View, error) {
	key := req.String("key")
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	e, live := s.Entries[key]
	head := "current"
	if !live {
		r, removed := s.Removed[key]
		if !removed {
			return nil, notFound(key)
		}
		e, head = r.entry, "removed "+itemstore.Age(r.RemovedAt)
	}
	t := view.Table{Columns: []view.Column{
		{Name: "Revision"},
		{Name: "Kind"},
		{Name: "Size", Kind: view.KindBytes},
		{Name: "Description"},
		{Name: "Set", Kind: view.KindDuration},
		{Name: "Replaced", Kind: view.KindDuration},
		{Name: "Source"},
	}}
	t.Rows = append(t.Rows, []string{head, e.Kind, format.Bytes(uint64(len(e.Value))),
		e.Description, itemstore.Age(e.Updated), "", e.origin()})
	for i, r := range e.Previous {
		t.Rows = append(t.Rows, []string{strconv.Itoa(i + 1), r.Kind, format.Bytes(uint64(len(r.Value))),
			r.Description, itemstore.Age(r.Updated), itemstore.Age(r.Retired), r.Origin})
	}
	t.Total = len(t.Rows)
	return t, nil
}

func restoreCapability() plugin.Capability {
	return plugin.Capability{
		ID: "kv.restore", Summary: "Bring back a removed key, or an earlier value of one",
		Safety: plugin.Write, NeedsGrant: true, Scope: "key",
		Description: "With no --revision, a key `kv rm` removed comes back exactly as it was, " +
			"history included. With one, the key's current value is replaced by that earlier " +
			"one — the number `kv history` lists, 1 being the most recent — and the value being " +
			"replaced joins the history in turn, so a restore is itself undoable.\n\n" +
			"A removed key whose name has since been reused is refused rather than merged: " +
			"rename the live one first, or purge the removed one.",
		Inputs: unlockFields([]plugin.Field{
			{Name: "key", Type: plugin.String, Positional: true, Required: true, Help: "key to restore",
				Suggest: suggestKeys},
			{Name: "revision", Type: plugin.Int,
				Help: "an earlier value to restore, as `kv history` numbers them; omit to restore a removed key"},
		}...),
		Run: runRestore,
	}
}

func runRestore(_ context.Context, req plugin.Request) (view.View, error) {
	if verr := refuseSilentIdentity(req); verr != nil {
		return nil, verr
	}
	unlock, verr := lockStore()
	if verr != nil {
		return nil, verr
	}
	defer unlock()
	s, verr := load(req)
	if verr != nil {
		return nil, verr
	}
	key := req.String("key")
	n := req.Int("revision")
	now := time.Now()

	if n == 0 {
		r, removed := s.Removed[key]
		if !removed {
			if _, live := s.Entries[key]; live {
				return nil, view.Errorf("kv.restore.live", "%q is in the store, not removed", key).
					WithHint("`rta kv history " + key + "` lists its earlier values; --revision brings one back")
			}
			return nil, notFound(key)
		}
		if _, live := s.Entries[key]; live {
			return nil, view.Errorf("kv.restore.taken", "%q was removed and then set again", key).
				WithHint("`rta kv rename " + key + " <other>` frees the name, or `rta kv rm --purge " + key +
					"` drops the removed one")
		}
		if req.DryRun {
			return view.Text{Body: fmt.Sprintf("would restore %q (%s, removed %s)", key, r.Kind,
				itemstore.Age(r.RemovedAt))}, nil
		}
		s.Entries[key] = r.entry
		delete(s.Removed, key)
		if verr := save(req, s); verr != nil {
			return nil, verr
		}
		return view.Text{Body: fmt.Sprintf("restored %q (%s) — as it was when removed, history included", key, r.Kind)}, nil
	}

	e, live := s.Entries[key]
	if !live {
		if _, removed := s.Removed[key]; removed {
			return nil, view.Errorf("kv.restore.removed", "%q is removed", key).
				WithHint("`rta kv restore " + key + "` with no --revision brings it back first")
		}
		return nil, notFound(key)
	}
	if n < 0 || n > len(e.Previous) {
		return nil, view.Errorf("kv.restore.norevision", "%q has %s, not a revision %d", key,
			plural(len(e.Previous), "earlier value"), n).
			WithHint("`rta kv history " + key + "` numbers them")
	}
	r := e.Previous[n-1]
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would restore revision %d of %q (%s, set %s)", n, key, r.Kind,
			itemstore.Age(r.Updated))}, nil
	}
	s.Entries[key] = entry{
		Value: r.Value, Description: r.Description, Kind: r.Kind, Filename: r.Filename,
		Origin: "revision:" + strconv.Itoa(n), Created: e.Created, Updated: now,
		Previous: e.retired(now),
	}
	if verr := save(req, s); verr != nil {
		return nil, verr
	}
	return view.Text{Body: fmt.Sprintf("restored revision %d of %q (%s) — the value it replaced is revision 1 now",
		n, key, r.Kind)}, nil
}

// removedTable lists what `kv rm` set aside, for `kv list --removed`.
func removedTable(s store) view.View {
	if len(s.Removed) == 0 {
		return view.Text{Body: "Nothing removed — `rta kv rm` keeps what it removes here until `rta kv restore` or `--purge`."}
	}
	names := make([]string, 0, len(s.Removed))
	for k := range s.Removed {
		names = append(names, k)
	}
	sort.Strings(names)
	t := view.Table{Columns: []view.Column{
		{Name: "Key"},
		{Name: "Kind"},
		{Name: "Size", Kind: view.KindBytes},
		{Name: "Description"},
		{Name: "Removed", Kind: view.KindDuration},
	}}
	for _, k := range names {
		r := s.Removed[k]
		t.Rows = append(t.Rows, []string{k, r.Kind, format.Bytes(uint64(len(r.Value))), r.Description,
			itemstore.Age(r.RemovedAt)})
	}
	t.Total = len(t.Rows)
	return t
}
