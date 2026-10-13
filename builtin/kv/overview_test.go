package kv

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// overviewPairs runs kv.overview compact and returns its one KeyValue.
func overviewPairs(t *testing.T, r plugin.Request) view.KeyValue {
	t.Helper()
	v, err := runOverview(t.Context(), r)
	if err != nil {
		t.Fatalf("kv.overview: %v", err)
	}
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("kv.overview = %s, want a KeyValue", view.TypeOf(v))
	}
	return kv
}

// overviewPage runs kv.overview with detail on and returns the page.
func overviewPage(t *testing.T, r plugin.Request) view.Sections {
	t.Helper()
	v, err := runOverview(t.Context(), r.With(map[string]any{"detail": true}))
	if err != nil {
		t.Fatalf("kv.overview --detail: %v", err)
	}
	s, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("detailed kv.overview = %s, want Sections", view.TypeOf(v))
	}
	return s
}

func sectionIDs(s view.Sections) []string {
	out := make([]string, 0, len(s.Items))
	for _, item := range s.Items {
		out = append(out, item.ID)
	}
	return out
}

func sectionByID(t *testing.T, s view.Sections, id string) view.View {
	t.Helper()
	for _, item := range s.Items {
		if item.ID == id {
			return item.View
		}
	}
	t.Fatalf("no %q section: %v", id, sectionIDs(s))
	return nil
}

const aCertificate = "-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIU\n-----END CERTIFICATE-----"

// seeded is a store with every shape the overview counts: nested folders, a key
// in none, two kinds, a value that was replaced and a key that was removed.
func seeded(t *testing.T) (valueBytes int) {
	t.Helper()
	setup(t)
	for key, value := range map[string]string{
		"prod/db/password":   "p4ssw0rd",
		"prod/db/user":       "admin",
		"prod/api-token":     "tok-first",
		"staging/api-token":  "tok-staging",
		"tls-cert":           aCertificate,
		"scratch/throwaway":  "to be removed",
		"prod/api-token-old": "unused",
	} {
		text(t, runSet, map[string]any{"key": key, "value": value}, false)
	}
	text(t, runSet, map[string]any{"key": "prod/api-token", "value": "tok-second"}, false)
	text(t, runRemove, map[string]any{"key": "scratch/throwaway"}, false)
	text(t, runRemove, map[string]any{"key": "prod/api-token-old"}, false)
	for _, v := range []string{"p4ssw0rd", "admin", "tok-second", "tok-staging", aCertificate} {
		valueBytes += len(v)
	}
	return valueBytes
}

// The page is the store's inventory: how many keys, how many folders — a name
// is in every folder its prefixes name, so prod/db/password is in prod/ and in
// prod/db/, and the line says how many of them are at the top, which is the
// count the folders table is of — how many of each kind and how many bytes of
// values, and what the store keeps beside the live keys.
func TestTheOverviewCountsWhatTheStoreHolds(t *testing.T) {
	bytes := seeded(t)

	kv := overviewPairs(t, req(nil, false))
	want := map[string]string{
		"keys":    "5 keys, 3 folders (2 at the top), " + format.Bytes(bytes) + " of values",
		"kinds":   "string 4 · certificate 1",
		"history": "1 earlier value · 2 removed keys, restorable",
	}
	for key, value := range want {
		if got := pairValue(kv, key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if got := pairValue(kv, "last set"); got == "" {
		t.Error("the overview does not say how old the values are")
	}
}

// What kv.status says about the file is not spelled a second time: the page
// begins with exactly the lines kv.status answers with, whatever state the store
// is in, so the two cannot disagree about where it is or whether it opens.
func TestTheOverviewBeginsWithWhatStatusSays(t *testing.T) {
	seeded(t)
	status, err := runStatus(t.Context(), req(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	facts := status.(view.KeyValue).Pairs
	page := overviewPairs(t, req(nil, false)).Pairs
	if len(page) <= len(facts) || !slices.Equal(page[:len(facts)], facts) {
		t.Errorf("the overview does not begin with kv.status's lines:\n status %v\n page   %v", facts, page)
	}
}

// Names, counts and sizes only: a value, or a piece of one, is what kv.get
// is a write for. The page is asked at every depth, compact and detailed.
func TestTheOverviewNeverShowsAValue(t *testing.T) {
	setup(t)
	secrets := []string{"s3cr3t-value-one", "tok-abcdef0123456789"}
	text(t, runSet, map[string]any{"key": "prod/db-password", "value": secrets[0], "description": "staging"}, false)
	text(t, runSet, map[string]any{"key": "api-token", "value": secrets[1]}, false)

	compact := fmt.Sprintf("%+v", overviewPairs(t, req(nil, false)))
	detailed := fmt.Sprintf("%+v", overviewPage(t, req(nil, false)))
	for _, secret := range secrets {
		for _, page := range []string{compact, detailed} {
			if strings.Contains(page, secret) || strings.Contains(page, secret[:6]) {
				t.Fatalf("the overview showed %q or the start of it:\n%s", secret, page)
			}
		}
	}
}

// A tile runs on a timer and an agent has nobody to ask. With nothing that
// opens the store at hand the overview says what is known of the file, and one
// line saying what the inventory needs and what supplies it.
func TestTheOverviewOfAStoreItCannotOpenSaysWhatItNeeds(t *testing.T) {
	seeded(t)

	for surface, supplied := range map[plugin.Surface]string{
		plugin.SurfaceCLI: "set RTA_KV_PASSPHRASE or give --passphrase",
		plugin.SurfaceTUI: "give the passphrase box",
		plugin.SurfaceMCP: "ask the operator to set RTA_KV_PASSPHRASE",
	} {
		r := plugin.NewRequest(nil, false, false).WithSurface(surface)
		kv := overviewPairs(t, r)
		line := pairValue(kv, "inventory")
		if !strings.HasPrefix(line, "needs a key at hand — ") || !strings.Contains(line, supplied) {
			t.Errorf("%s: the inventory line = %q, want it to say a key is needed and %q", surface, line, supplied)
		}
		for _, counted := range []string{"keys", "kinds", "history"} {
			if got := pairValue(kv, counted); got != "" {
				t.Errorf("%s: a store nothing opened still says %s = %q", surface, counted, got)
			}
		}
		if pairValue(kv, "store") == "" || pairValue(kv, "locked with") != "a passphrase" {
			t.Errorf("%s: the facts about the file are missing: %v", surface, kv.Pairs)
		}
	}
}

// A passphrase that does not fit is not a missing one, and is not said as one:
// the key was at hand and failed, which is worth a different line.
func TestTheOverviewOfAStoreTheKeyDoesNotOpenSaysSo(t *testing.T) {
	seeded(t)
	kv := overviewPairs(t, req(map[string]any{"passphrase": "not the passphrase"}, false))
	if line := pairValue(kv, "inventory"); !strings.HasPrefix(line, "not opened — could not decrypt the store") {
		t.Errorf("inventory = %q", line)
	}
}

// A person at a terminal is asked for a passphrase by everything else here,
// and the overview is not that: it would be the call a tile and a status line
// are built from. It says so beside the line that otherwise reads "you will be
// asked", and names the command that does ask.
func TestTheOverviewNeverAsksForAPassphraseAtATerminal(t *testing.T) {
	seeded(t)
	origPrompt, origCan := promptPassphrase, canPrompt
	promptPassphrase = func() (string, error) {
		t.Error("the overview asked for the store's passphrase")
		return "correct horse battery staple", nil
	}
	canPrompt = func(r plugin.Request) bool { return r.Surface() == plugin.SurfaceCLI }
	t.Cleanup(func() { promptPassphrase, canPrompt, prompted = origPrompt, origCan, "" })

	kv := overviewPairs(t, cliReq(nil))
	line := pairValue(kv, "inventory")
	if !strings.HasPrefix(line, "needs a key at hand") || !strings.Contains(line, "this page never asks, `rta kv list` does") {
		t.Errorf("inventory = %q", line)
	}
	if got := pairValue(kv, "unlocks here"); !strings.HasPrefix(got, "on request") {
		t.Errorf("unlocks here = %q: the line the overview's own disclaimer sits beside", got)
	}
	if got := pairValue(kv, "keys"); got != "" {
		t.Errorf("a store nobody opened was counted: keys = %q", got)
	}
}

// A key of the store's that has a passphrase of its own is asked for the same
// way, by the key prompt and not the store's: neither is made.
func TestTheOverviewNeverAsksForAKeysPassphraseEither(t *testing.T) {
	setupWithConfig(t)
	path := writeLockedSSHKey(t, t.TempDir(), "id_locked", "hunter2")
	asked := stubKeyPrompt(t, "hunter2")
	if _, err := runSet(t.Context(), cliReq(map[string]any{"key": "token", "value": "v", "identity": path})); err != nil {
		t.Fatal(err)
	}
	before := *asked
	keyPassphrases = map[string]string{}

	kv := overviewPairs(t, cliReq(map[string]any{"identity": path}))
	if *asked != before {
		t.Errorf("the overview asked for the key's passphrase %d time(s)", *asked-before)
	}
	if line := pairValue(kv, "inventory"); !strings.HasPrefix(line, "needs a key at hand") {
		t.Errorf("inventory = %q", line)
	}
	if got := pairValue(kv, "locked to"); !strings.HasPrefix(got, "1 key") {
		t.Errorf("locked to = %q: the readers are known without opening the store", got)
	}
}

// The environment's passphrase, or a key held unlocked, is what a call "has at
// hand", and the overview opens the store with it without being given anything.
func TestTheOverviewOpensTheStoreFromTheEnvironment(t *testing.T) {
	seeded(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")
	kv := overviewPairs(t, plugin.NewRequest(nil, false, false))
	if got := pairValue(kv, "keys"); !strings.HasPrefix(got, "5 keys") {
		t.Errorf("keys = %q", got)
	}
}

// A store that has not been made is a state, not a failure, and has no
// inventory to count.
func TestTheOverviewBeforeThereIsAStore(t *testing.T) {
	setup(t)
	kv := overviewPairs(t, req(nil, false))
	if got := pairValue(kv, "state"); !strings.Contains(got, "no store yet") {
		t.Errorf("state = %q", got)
	}
	if len(kv.Pairs) != 2 {
		t.Errorf("a store that does not exist has more to say than where it would be: %v", kv.Pairs)
	}
	// Nor does the detailed page invent a section for it.
	if got := sectionIDs(overviewPage(t, req(nil, false))); !slices.Equal(got, []string{"summary"}) {
		t.Errorf("sections of an absent store = %v", got)
	}
}

// A store made and then emptied has nothing to count, and says how to add.
func TestTheOverviewOfAnEmptyStore(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false)
	text(t, runRemove, map[string]any{"key": "k", "purge": true}, false)

	kv := overviewPairs(t, req(nil, false))
	if got := pairValue(kv, "keys"); !strings.HasPrefix(got, "none — ") || !strings.Contains(got, "kv set") {
		t.Errorf("keys = %q", got)
	}
	for _, absent := range []string{"kinds", "last set", "history"} {
		if got := pairValue(kv, absent); got != "" {
			t.Errorf("an empty store says %s = %q", absent, got)
		}
	}
}

// A line that says nothing about this store is left out: a zero on a tile reads
// as something somebody is meant to watch.
func TestTheOverviewLeavesOutWhatIsNotThere(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "flat", "value": "v"}, false)
	kv := overviewPairs(t, req(nil, false))
	if got := pairValue(kv, "keys"); got != "1 key, 1 B of values" {
		t.Errorf("keys = %q, want no folder named when there is none", got)
	}
	if got := pairValue(kv, "history"); got != "" {
		t.Errorf("history = %q for a store that kept nothing", got)
	}
}

// One folder is counted once however many keys are in it, and a key is
// tallied under the outermost folder that holds it.
func TestFoldersAreCountedOncePerFolder(t *testing.T) {
	for key, want := range map[string][]string{
		"flat":      nil,
		"a/b":       {"a/"},
		"a/b/c":     {"a/", "a/b/"},
		"a//b":      {"a/", "a//"},
		"/leading":  {"/"},
		"deep/a/b/": {"deep/", "deep/a/", "deep/a/b/"},
	} {
		if got := foldersOf(key); !slices.Equal(got, want) {
			t.Errorf("foldersOf(%q) = %v, want %v", key, got, want)
		}
	}
	inv := inventoryOf(store{Entries: map[string]entry{
		"a/b/one": {Value: []byte("1"), Kind: "string"},
		"a/b/two": {Value: []byte("22"), Kind: "string"},
		"a/three": {Value: []byte("333"), Kind: "json"},
		"loose":   {Value: []byte("4444")},
	}})
	if inv.folders != 2 || inv.keys != 4 || inv.valueBytes != 10 {
		t.Errorf("inventory = %+v, want 4 keys in 2 folders, 10 bytes", inv)
	}
	if len(inv.top) != 1 || inv.top[0].name != "a/" || inv.top[0].keys != 3 || inv.top[0].bytes != 6 {
		t.Errorf("top-level folders = %+v", inv.top)
	}
	if inv.loose.keys != 1 || inv.loose.bytes != 4 {
		t.Errorf("keys in no folder = %+v", inv.loose)
	}
	if got := inv.kindsLine(); got != "string 2 · json 1 · unknown 1" {
		t.Errorf("kinds = %q: an entry with no recorded kind is counted as unknown, last on a tie", got)
	}
}

// The folders table is the answer to "where do my secrets live", so it is the
// biggest folders that make the cut, and the table says how many rows there are.
// The sentence under it says how many folders there are, which is not the same
// number: the keys in no folder are a row and not a folder, and the page says
// where the rest are.
func TestTheFoldersTableIsBoundedAndSaysSo(t *testing.T) {
	entries := map[string]entry{"loose": {Value: []byte("x")}}
	// Twelve folders: f01/ holds one key, f02/ two, … and f12/ twelve.
	for n := 1; n <= 12; n++ {
		for k := 0; k < n; k++ {
			entries[fmt.Sprintf("f%02d/key%02d", n, k)] = entry{Value: []byte("v")}
		}
	}
	tbl := inventoryOf(store{Entries: entries}).foldersTable(plugin.SurfaceCLI)

	if len(tbl.Rows) != overviewFolders+1 {
		t.Fatalf("%d rows, want the %d biggest folders and the keys in none", len(tbl.Rows), overviewFolders)
	}
	// Rows, as every table's total is: a reader that finds the total larger than
	// the rows it was sent knows it was cut, and a total that left the row of
	// keys in no folder out would call nine folders and that row complete.
	if tbl.Total != 13 {
		t.Errorf("total = %d, want the 12 folders and the keys in none, so a reader can see what was cut", tbl.Total)
	}
	if tbl.Rows[0][0] != "f12/" || tbl.Rows[0][1] != "12" || tbl.Rows[overviewFolders-1][0] != "f05/" {
		t.Errorf("not the biggest folders first: %v", tbl.Rows)
	}
	if last := tbl.Rows[len(tbl.Rows)-1]; last[0] != noFolder || last[1] != "1" {
		t.Errorf("the keys in no folder are not the last row: %v", last)
	}
	if len(tbl.Warnings) != 1 || !tbl.Warnings[0].Advisory {
		t.Fatalf("a table that was cut says so once, as a note beside rows that are all right: %+v", tbl.Warnings)
	}
	note := tbl.Warnings[0]
	if want := "the 8 biggest of 12 top-level folders are shown"; !strings.Contains(note.Message, want) {
		t.Errorf("the note says %q, want it to say %q, the folders and not the rows", note.Message, want)
	}
	if want := "`rta kv tree` draws every folder"; note.Hint != want {
		t.Errorf("the hint says %q, want %q", note.Hint, want)
	}
	if agent := inventoryOf(store{Entries: entries}).foldersTable(plugin.SurfaceMCP).Warnings[0].Hint; !strings.Contains(agent, "kv_tree") || strings.Contains(agent, "rta kv") {
		t.Errorf("an agent is told %q, which is not the tool that draws the tree", agent)
	}
}

// A table that was not cut has nothing to say about it.
func TestTheFoldersTableOfAFewFoldersSaysNothingMore(t *testing.T) {
	tbl := inventoryOf(store{Entries: map[string]entry{
		"a/one": {Value: []byte("1")}, "b/two": {Value: []byte("2")}, "loose": {Value: []byte("3")},
	}}).foldersTable(plugin.SurfaceCLI)
	if len(tbl.Warnings) != 0 {
		t.Errorf("a table of every folder carries %+v", tbl.Warnings)
	}
}

// What the folders section lists is in its title, so a reader of a store with
// nested folders is not left to work out why the summary counts more of them.
func TestTheFoldersSectionSaysItListsTheTopLevelOnes(t *testing.T) {
	seeded(t)
	for _, item := range overviewPage(t, req(nil, false)).Items {
		if item.ID == "folders" {
			if item.Title != "top-level folders" {
				t.Errorf("the section is titled %q", item.Title)
			}
			return
		}
	}
	t.Fatal("no folders section")
}

// A store whose folders are all at the top says no more than the count.
func TestTheSummaryNamesTheTopLevelFoldersOnlyWhenThereAreOthers(t *testing.T) {
	inventoryOfNames := func(names ...string) inventory {
		entries := map[string]entry{}
		for _, name := range names {
			entries[name] = entry{Value: []byte("v")}
		}
		return inventoryOf(store{Entries: entries})
	}
	for want, inv := range map[string]inventory{
		"2 keys, 2 folders, 2 B of values":                inventoryOfNames("a/x", "b/x"),
		"1 key, 2 folders (1 at the top), 1 B of values":  inventoryOfNames("a/b/x"),
		"2 keys, 3 folders (1 at the top), 2 B of values": inventoryOfNames("a/b/x", "a/c/y"),
		"2 keys, 1 folder, 2 B of values":                 inventoryOfNames("flat", "a/x"),
	} {
		if got := pairValue(view.KeyValue{Pairs: inv.pairs(plugin.SurfaceCLI)}, "keys"); got != want {
			t.Errorf("keys = %q, want %q", got, want)
		}
	}
}

// Ties are broken the same way every time: the table is cut, so which folder is
// cut cannot depend on the order a map happened to be read in.
func TestTheFoldersTableOrderDoesNotDependOnTheMap(t *testing.T) {
	entries := map[string]entry{}
	for _, name := range []string{"d", "b", "a", "c", "e", "f", "g", "h", "i", "j"} {
		entries[name+"/k"] = entry{Value: []byte("v")}
	}
	for range 20 {
		tbl := inventoryOf(store{Entries: entries}).foldersTable(plugin.SurfaceCLI)
		got := make([]string, len(tbl.Rows))
		for i, row := range tbl.Rows {
			got[i] = row[0]
		}
		if want := []string{"a/", "b/", "c/", "d/", "e/", "f/", "g/", "h/"}; !slices.Equal(got, want) {
			t.Fatalf("rows = %v, want %v", got, want)
		}
	}
}

// With no folder in the store a table of one row would only repeat the totals.
func TestTheDetailedPageHasNoFoldersWhenThereAreNone(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "flat", "value": "v"}, false)
	if got := sectionIDs(overviewPage(t, req(nil, false))); !slices.Equal(got, []string{"summary"}) {
		t.Errorf("sections = %v", got)
	}
}

func TestTheDetailedPageListsTheFolders(t *testing.T) {
	seeded(t)
	page := overviewPage(t, req(nil, false))
	if got := sectionIDs(page); !slices.Equal(got, []string{"summary", "folders"}) {
		t.Fatalf("sections = %v", got)
	}
	if _, ok := sectionByID(t, page, "summary").(view.KeyValue); !ok {
		t.Error("the page does not open on the compact answer")
	}
	tbl := sectionByID(t, page, "folders").(view.Table)
	got := map[string]string{}
	for _, row := range tbl.Rows {
		got[row[0]] = row[1]
	}
	if got["prod/"] != "3" || got["staging/"] != "1" || got[noFolder] != "1" || len(got) != 3 {
		t.Errorf("folders = %v", got)
	}
}

// Who can open the store is public — the keys sit in plain text beside it — so
// the page lists them even when this call cannot open the store itself.
func TestTheDetailedPageListsWhoCanOpenAStoreLockedToKeys(t *testing.T) {
	setupWithConfig(t)
	path := writeLockedSSHKey(t, t.TempDir(), "id_locked", "hunter2")
	stubKeyPrompt(t, "hunter2")
	if _, err := runSet(t.Context(), cliReq(map[string]any{"key": "a/b", "value": "v", "identity": path})); err != nil {
		t.Fatal(err)
	}

	page := overviewPage(t, plugin.NewRequest(nil, false, false))
	if got := sectionIDs(page); !slices.Equal(got, []string{"summary", "readers"}) {
		t.Fatalf("sections = %v", got)
	}
	readers, ok := sectionByID(t, page, "readers").(view.Table)
	if !ok || len(readers.Rows) != 1 {
		t.Errorf("readers = %+v", sectionByID(t, page, "readers"))
	}
	summary := sectionByID(t, page, "summary").(view.KeyValue)
	if line := pairValue(summary, "inventory"); !strings.HasPrefix(line, "needs a key at hand") {
		t.Errorf("a store the call could not open still reads as counted: %q", line)
	}
}

// How old the values are, said once when the oldest and the newest read the
// same, and not at all for entries that carry no date.
func TestTheOverviewSaysHowOldTheValuesAre(t *testing.T) {
	now := time.Now()
	inv := inventoryOf(store{Entries: map[string]entry{
		"new": {Value: []byte("v"), Updated: now.Add(-3 * 24 * time.Hour)},
		"old": {Value: []byte("v"), Updated: now.Add(-400 * 24 * time.Hour)},
	}})
	if got := inv.setLine(); got != "newest "+format.Ago(inv.newest)+", oldest "+format.Ago(inv.oldest) || !strings.Contains(got, "3 days ago") {
		t.Errorf("last set = %q", got)
	}
	same := inventoryOf(store{Entries: map[string]entry{"one": {Value: []byte("v"), Updated: now.Add(-3 * 24 * time.Hour)}}})
	if got := same.setLine(); got != "3 days ago" {
		t.Errorf("last set = %q for one key", got)
	}
	undated := inventoryOf(store{Entries: map[string]entry{"one": {Value: []byte("v")}}})
	if got := undated.setLine(); got != "" {
		t.Errorf("last set = %q for an entry with no date", got)
	}
}

// The overview is a glance that costs a decrypt, which is what the pace its
// capability declares is for: a tile of it must not be re-run at the
// dashboard's own few seconds, which the key derivation of a passphrase store
// would turn into a standing load.
func TestAnOverviewTileIsNotRunAtTheDashboardsPace(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.ID != "kv.overview" {
			continue
		}
		if c.Refresh < 15*time.Second {
			t.Errorf("refresh = %s, want a pace slower than the dashboard's own", c.Refresh)
		}
		if !c.Detailed || !c.HostSpecific || c.Safety != plugin.Read || !c.Idempotent {
			t.Errorf("kv.overview is declared %+v", c)
		}
		if len(c.Actions) != 2 || c.Actions[0].Target != "kv.list" || c.Actions[1].Target != "kv.set" {
			t.Errorf("the tile's actions are %v, want the list and an add", c.Actions)
		}
		return
	}
	t.Fatal("kv.overview is not declared")
}
