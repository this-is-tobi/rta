package kv

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// overviewFolders is how many top-level folders the detailed page lists. A
// store that organises its names by folder has a handful of them, and the
// table is the answer to "where do my secrets live", which the biggest ones
// give; the rest are in the table's total, and `kv.tree` draws every one.
const overviewFolders = 8

// overviewRefresh is how often a dashboard tile of kv.overview is re-run. The
// inventory costs a decrypt, and for a passphrase store that is the key
// derivation the passphrase was chosen to make slow: some 350 ms of work on a
// laptop and, at age's own work factor, a quarter of a gigabyte of memory. At
// the dashboard's own five seconds that is a standing cost for as long as the
// terminal stays open, for anybody who has the passphrase in the environment. A
// store changes when somebody changes it, so nothing on the tile moves faster
// than this.
const overviewRefresh = 30 * time.Second

// noFolder labels the row of keys that are in no folder, which no folder can be
// called: a folder's name always ends in the slash.
const noFolder = "(no folder)"

func overviewCapability() plugin.Capability {
	return plugin.Capability{
		ID: "kv.overview", Summary: "What the secret store holds, by count and kind, and what can open it",
		// The words a person says it in rather than the ones it is written in:
		// "how many secrets are stored" has neither "secrets" nor "stored" in
		// the summary, whose words are the singular and "holds". What they keep in
		// it they call passwords and credentials, and what they ask of the keys
		// removed from it is whether anything was deleted.
		Keywords: []string{"secrets", "stored", "inventory", "contents", "counts", "locked", "passwords", "credentials", "deleted"},
		// HostSpecific as kv.status is, whose lines it carries: where this machine
		// keeps the store and whether the server's own environment opens it say
		// something about the server, never about whoever is calling it.
		Safety: plugin.Read, HostSpecific: true, Idempotent: true, Detailed: true,
		Refresh: overviewRefresh,
		Description: "What is in the store: how many keys and folders, how many of each kind, how many bytes " +
			"of values, how old the newest and the oldest are, and what is kept besides the live keys: earlier " +
			"values (`kv.history`) and removed keys (`kv.restore`). Then where the store is, how big, when it " +
			"last changed, how it is locked and whether this environment can open it. Names, counts and " +
			"sizes, never a value.\n\n" +
			"The inventory needs the store open, and this never asks for what opens it: with no key at hand " +
			"it says so, says what supplies one, and still answers the rest. `kv.status` is the cheaper " +
			"question and never decrypts. With `detail`: the biggest top-level folders, and who can open a " +
			"store locked to keys.",
		Inputs: unlockFields(),
		// The dashboard's kv tile, which is about the store rather than any
		// entry, so what it offers is the two things you want from there.
		Actions: storeActions(),
		Run:     runOverview,
	}
}

// runOverview answers what is in the store and how it is locked, in one page.
//
// **It never asks for a key.** A tile runs unasked, on a timer, and an agent has
// nobody to ask, so a store that will not open from what the call already holds
// answers with what can be said without opening it and one line saying what
// the inventory needs — the rule kv.status keeps by reading nothing but the
// file. On a terminal the open is made as a call nobody is there to answer
// (unattended), since asking is what a CLI request would otherwise do.
//
// The TUI's store session does not fill the tile either, and that is on
// purpose: every use of a session moves its idle deadline out, so a tile
// refreshing on a timer would keep a store unlocked for as long as the
// terminal stayed open, which is the one thing the session is built not to do.
// Opening the tile is a use, a person asking to see, and the session answers
// that. What the tile does is look: a session that is open is said to be, in the
// words of the header's badge, so that one screen does not say "store unlocked"
// and "no passphrase available" at once (SessionUntil, which is not a use).
func runOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	facts, verr := readStoreFacts(req)
	if verr != nil {
		return nil, verr
	}
	// What is in the store leads, then how it is locked, then where the file is:
	// a tile is cut from the bottom, and what a glance wants first is the count.
	// On a tile the lock comes straight after the count, since the lines between
	// them — the kinds, the ages, what is kept — are the ones a box a third of the
	// screen wide wraps over several lines and cuts, and a tile that never says
	// how the secret store is locked has not answered the question it is for.
	var pairs []view.Pair
	var counted *inventory
	var lock []view.Pair
	if facts.mode != "" {
		s, lerr := load(unattended(req))
		switch {
		case lerr == nil:
			inv := inventoryOf(s)
			counted = &inv
			pairs = append(pairs, inv.pairs(req.Surface(), isTile(req))...)
		case heldBySession(req, facts.mode, lerr):
			pairs = append(pairs, view.Pair{Key: "inventory", Value: "unlocked in this session — enter to see"})
		default:
			pairs = append(pairs, view.Pair{Key: "inventory", Value: cannotOpen(req, lerr)})
		}
		lock = facts.lock(unlocksHere(req, facts.mode, lerr))
	}
	at := len(pairs)
	if isTile(req) {
		at = min(1, at)
	}
	pairs = append(slices.Insert(pairs, at, lock...), facts.where...)
	summary := view.KeyValue{Pairs: pairs}
	if !req.Bool("detail") {
		return summary, nil
	}

	// The compact answer first, so the page opens with what the tile says. The
	// readers do not need the store open — they are public keys, kept in plain
	// text beside it — so a locked store still lists who can open it.
	p := plugin.NewPage(ctx, req)
	p.PutAs("summary", "summary", summary)
	if counted != nil && counted.folders > 0 {
		p.PutAs("folders", "top-level folders", counted.foldersTable(req.Surface()))
	}
	if facts.mode == modeKeys {
		p.AddAs("readers", "readers", runRecipients, plugin.Read, nil)
	}
	return p.View(), nil
}

// unattended is req as a call nobody is there to answer, which is what a read
// on a timer is. Only a CLI request can stop to ask for a passphrase
// (canPrompt), so that one is stripped of its surface — the same thing Reveal
// does for the host — and every other surface is left alone: an MCP request has
// nobody to ask already, and the TUI's keeps the surface its session is
// started under.
func unattended(req plugin.Request) plugin.Request {
	if req.Surface() == plugin.SurfaceCLI {
		return req.WithSurface(plugin.SurfaceUnknown)
	}
	return req
}

// unlocksHere is the `unlocks here` line of a page that has just tried to open
// the store, and lerr is why it could not, nil when it did. kv.status answers the
// cheaper question — is a key at hand — and never finds out whether it fits;
// this page has found out, and does not say "yes" over the line that says the
// store would not decrypt, or "you will be asked" under the one that says this
// page does not ask.
func unlocksHere(req plugin.Request, mode keyMode, lerr *view.Error) string {
	switch {
	case lerr == nil:
		return unlockAvailability(req, mode)
	case heldBySession(req, mode, lerr):
		return "yes — unlocked in this session"
	case keyRefused(req, lerr):
		return refusedKey(req, lerr)
	}
	return unlockLine(req, mode, req.Surface().CapabilityName("kv.list")+" asks, this page does not")
}

// isTile reports whether this is the dashboard's run of the overview: the
// compact answer on the TUI, which refreshes on a timer in a box a third of the
// screen wide. The page enter opens is the detailed one.
func isTile(req plugin.Request) bool {
	return req.Surface() == plugin.SurfaceTUI && !req.Bool("detail")
}

// heldBySession reports whether a tile's open failed only because the store is
// unlocked in the TUI's session and the tile, which never uses it, was not given
// the passphrase. Only a passphrase store has a session.
func heldBySession(req plugin.Request, mode keyMode, lerr *view.Error) bool {
	if lerr == nil || lerr.Code != "kv.passphrase.missing" || mode != modePassphrase || !isTile(req) {
		return false
	}
	_, open := SessionUntil()
	return open
}

// supplyPassphrase is how the caller is told to give the passphrase of a store
// this page cannot open without it. On the TUI the box is not on the page, so it
// says the keys that reach it: `e` from the page, and from a tile `enter` first.
func supplyPassphrase(req plugin.Request) string {
	if req.Surface() != plugin.SurfaceTUI {
		return passphraseHint(req.Surface())
	}
	keys := "e"
	if isTile(req) {
		keys = "enter, then e"
	}
	return "set " + passphraseEnv + ", or type the passphrase: " + keys
}

// keyRefused reports whether the open failed with a key at hand that was not
// accepted, as against one that was not there: the passphrase that did not
// decrypt the store, the identity the store is not locked to, and the
// passphrase given for a key that is not that key's own.
func keyRefused(req plugin.Request, verr *view.Error) bool {
	switch verr.Code {
	case "kv.wrongpass", "kv.wrongkey":
		return true
	case "kv.identity.locked":
		return lookupPassphrase(req) != ""
	}
	return false
}

// refusedKey is the line for a key that was at hand and was not accepted, which
// names it by where it came from and not by any part of it.
func refusedKey(req plugin.Request, verr *view.Error) string {
	if verr.Code == "kv.wrongkey" {
		return "no — identity " + identityPath(req) + " was not accepted by the store"
	}
	line := "no — the passphrase " + passphraseSource(req) + " was not accepted"
	if verr.Code == "kv.identity.locked" {
		line += " by the key"
	}
	return line
}

// keyMissing reports whether the open failed because nothing that could open the
// store was at hand.
func keyMissing(req plugin.Request, verr *view.Error) bool {
	switch verr.Code {
	case "kv.passphrase.missing", "kv.identity.required":
		return true
	case "kv.identity.locked":
		return lookupPassphrase(req) == ""
	}
	return false
}

// cannotOpen is the line standing where the inventory would be, which says why
// there is none and what would supply it: the refusal's own message and hint,
// worded for the surface asking. A passphrase that was not given is not said
// twice, since the line begins by saying that no key is at hand, and a tile
// gives the line a quarter of its width.
//
// A locked key is said the same short way: the file is named by the line beside
// this one (unlocksHere), and that an agent signs and does not decrypt is the
// answer to a different question, of why `kv.list` will ask a person for a key
// the ssh-agent has.
func cannotOpen(req plugin.Request, verr *view.Error) string {
	lead, why := "not opened", verr.Message
	missing := keyMissing(req, verr)
	if missing {
		lead = "needs a key at hand"
	}
	if verr.Code == "kv.passphrase.missing" {
		why = ""
	}
	line := lead
	switch {
	case missing && verr.Code == "kv.identity.locked":
		line += " — the key's own passphrase: " + supplyPassphrase(req)
	case verr.Code == "kv.passphrase.missing":
		line += " — " + supplyPassphrase(req)
	case why != "" && verr.Hint != "":
		line += " — " + why + "; " + verr.Hint
	case why != "" || verr.Hint != "":
		line += " — " + why + verr.Hint
	}
	return line
}

// inventory is what a store holds, as counts and sizes. It is built from a
// decrypted store and carries no value, which is what makes it safe to show
// wherever the names are.
type inventory struct {
	keys       int
	folders    int
	valueBytes int
	kinds      map[string]int
	// oldest and newest are when a live key's value was last set: the zero time
	// until one is found, and left alone by an entry that has none.
	oldest, newest time.Time
	// earlier is the values live keys keep from before a write replaced them,
	// and removed the keys `kv.rm` set aside.
	earlier, removed int
	// top is every top-level folder, and loose the keys that are in none.
	top   []folderTally
	loose folderTally
}

// folderTally is what one folder holds.
type folderTally struct {
	name  string
	keys  int
	bytes int
}

func inventoryOf(s store) inventory {
	inv := inventory{kinds: map[string]int{}, removed: len(s.Removed), loose: folderTally{name: noFolder}}
	seen := map[string]bool{}
	byTop := map[string]*folderTally{}
	for name, e := range s.Entries {
		inv.keys++
		inv.valueBytes += len(e.Value)
		inv.kinds[kindName(e.Kind)]++
		inv.earlier += len(e.Previous)
		if !e.Updated.IsZero() {
			if inv.oldest.IsZero() || e.Updated.Before(inv.oldest) {
				inv.oldest = e.Updated
			}
			if e.Updated.After(inv.newest) {
				inv.newest = e.Updated
			}
		}

		// A name is in every folder its prefixes name, so a folder is counted
		// once however many keys share it, and a key is tallied under the
		// outermost one — the row a person looks for it under.
		in := foldersOf(name)
		for _, f := range in {
			seen[f] = true
		}
		tally := &inv.loose
		if len(in) > 0 {
			if byTop[in[0]] == nil {
				byTop[in[0]] = &folderTally{name: in[0]}
			}
			tally = byTop[in[0]]
		}
		tally.keys++
		tally.bytes += len(e.Value)
	}
	inv.folders = len(seen)
	for _, t := range byTop {
		inv.top = append(inv.top, *t)
	}
	// The most keys first, then the most bytes, then by name: which are cut is
	// what a bounded table has to get right, and an order that depended on the
	// map it was counted in would cut a different folder each time.
	slices.SortFunc(inv.top, func(a, b folderTally) int {
		return cmp.Or(cmp.Compare(b.keys, a.keys), cmp.Compare(b.bytes, a.bytes), strings.Compare(a.name, b.name))
	})
	return inv
}

// kindName is how a kind is counted: an entry written before kinds were
// recorded has none, and is counted as that rather than under a blank.
func kindName(kind string) string {
	if kind == "" {
		return "unknown"
	}
	return kind
}

// pairs are the inventory's lines of the summary, which lead it: what is held,
// what it is, how old, and what is kept beside it. Each line that says nothing
// about this store is left out, because a zero on a tile reads as a thing
// somebody is meant to watch.
//
// A tile (glance) says what is kept in fewer words, since a third of a screen
// wraps "1 earlier value · 2 removed keys, restorable" over three lines and a
// tile cut after the second ends a phrase on a comma. The words are the page's
// own, shortened, and the page that enter opens has them whole.
func (inv inventory) pairs(sf plugin.Surface, glance bool) []view.Pair {
	var pairs []view.Pair
	if inv.keys == 0 {
		pairs = append(pairs, view.Pair{Key: "keys", Value: "none — " + sf.CapabilityName("kv.set") + " adds one"})
	} else {
		held := format.CountOf(inv.keys, "key")
		if inv.folders > 0 {
			held += ", " + format.CountOf(inv.folders, "folder")
			// Every folder at every depth, which is not what the folders table
			// lists: that is the top level, and a reader who is shown two numbers
			// is told which is which.
			if inv.folders > len(inv.top) {
				held += " (" + strconv.Itoa(len(inv.top)) + " at the top)"
			}
		}
		held += ", " + format.Bytes(inv.valueBytes) + " of values"
		pairs = append(pairs, view.Pair{Key: "keys", Value: held},
			view.Pair{Key: "kinds", Value: inv.kindsLine()})
		if line := inv.setLine(); line != "" {
			pairs = append(pairs, view.Pair{Key: "last set", Value: line})
		}
	}
	var kept []string
	if inv.earlier > 0 {
		kept = append(kept, format.CountOf(inv.earlier, "earlier value"))
	}
	if inv.removed > 0 && glance {
		kept = append(kept, strconv.Itoa(inv.removed)+" removed")
	} else if inv.removed > 0 {
		kept = append(kept, format.CountOf(inv.removed, "removed key")+", restorable")
	}
	if len(kept) > 0 {
		sep := " · "
		if glance {
			sep = ", "
		}
		pairs = append(pairs, view.Pair{Key: "history", Value: strings.Join(kept, sep)})
	}
	return pairs
}

// kindsLine counts each kind held, the most common first — at most the seven
// kinds there are.
func (inv inventory) kindsLine() string {
	names := make([]string, 0, len(inv.kinds))
	for k := range inv.kinds {
		names = append(names, k)
	}
	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(cmp.Compare(inv.kinds[b], inv.kinds[a]), strings.Compare(a, b))
	})
	parts := make([]string, len(names))
	for i, k := range names {
		parts[i] = k + " " + strconv.Itoa(inv.kinds[k])
	}
	return strings.Join(parts, " · ")
}

// setLine is how old the values are: the newest and the oldest, which is the
// answer to "has anything been sitting there for a year". One age when the two
// read the same, and nothing when no entry says when it was set.
func (inv inventory) setLine() string {
	if inv.newest.IsZero() {
		return ""
	}
	newest, oldest := format.Ago(inv.newest), format.Ago(inv.oldest)
	if newest == oldest {
		return newest
	}
	return "newest " + newest + ", oldest " + oldest
}

// foldersTable is the top-level folders, the biggest first and bounded, with
// the keys that are in none as the last row. A table that was cut says how many
// rows there are (Table.Total), which is how a program is told what is missing,
// and a person is told in a sentence under it, in folders: the row of keys in no
// folder is a row, and counted as one it would be a folder that is not there.
//
// **A folder is named by whoever wrote a key in it**, so its name is shown
// (plugin.ListedName) and not cleaned: the cleaner a renderer applies to a cell
// is right of a body and wrong of a name. A newline in one started a row of its
// own, a folder the store does not have; an escape sequence or an override was
// dropped, so `prod<ESC>[0m/` drew as a second `prod/`, and over MCP the model
// was told so. A plain name reads as itself, spaces and accents included.
func (inv inventory) foldersTable(sf plugin.Surface) view.Table {
	t := view.Table{Columns: []view.Column{
		{Name: "Folder"},
		{Name: "Keys", Kind: view.KindNumber},
		{Name: "Size", Kind: view.KindBytes},
	}}
	row := func(label string, f folderTally) []string {
		return []string{label, strconv.Itoa(f.keys), format.Bytes(f.bytes)}
	}
	shown := min(len(inv.top), overviewFolders)
	for _, f := range inv.top[:shown] {
		t.Rows = append(t.Rows, row(plugin.ListedName(f.name), f))
	}
	t.Total = len(inv.top)
	if inv.loose.keys > 0 {
		t.Rows = append(t.Rows, row(noFolder, inv.loose))
		t.Total++
	}
	// Advisory: every row there is is right, and the rest is one call away.
	if shown < len(inv.top) {
		t.Warnings = append(t.Warnings, view.Error{Code: "kv.overview.folders", Advisory: true,
			Message: fmt.Sprintf("the %d biggest of %s are shown", shown, format.CountOf(len(inv.top), "top-level folder")),
			Hint:    sf.CapabilityName("kv.tree") + " draws every folder"})
	}
	return t
}
