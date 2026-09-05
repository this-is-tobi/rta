package fs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// tree builds a fixture on disk. Sizes are stated in the map, so a test can
// say what it expects rather than measuring what it happened to write.
func fixture(t *testing.T, files map[string]int) string {
	t.Helper()
	root := t.TempDir()
	for name, size := range files {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func run(t *testing.T, h plugin.Handler, values map[string]any) view.View {
	t.Helper()
	v, err := h(context.Background(), plugin.NewRequest(values, false, false))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return v
}

func rowFor(t *testing.T, tbl view.Table, name string) []string {
	t.Helper()
	for _, r := range tbl.Rows {
		if len(r) > 0 && r[0] == name {
			return r
		}
	}
	t.Fatalf("no row for %q in %v", name, tbl.Rows)
	return nil
}

func TestUsageRanksBiggestFirstAndSumsDirectories(t *testing.T) {
	root := fixture(t, map[string]int{
		"small.txt":       10,
		"big/one.bin":     3000,
		"big/two.bin":     2000,
		"medium/only.bin": 1000,
	})
	tbl, ok := run(t, runUsage, map[string]any{"path": root, "limit": 20}).(view.Table)
	if !ok {
		t.Fatal("usage did not return a table")
	}
	var names []string
	for _, r := range tbl.Rows {
		names = append(names, r[0])
	}
	want := []string{"big/", "medium/", "small.txt"}
	if len(names) != len(want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("entry %d = %q, want %q (order: %v)", i, names[i], want[i], names)
		}
	}
	// A directory reports everything beneath it, not its own inode size.
	if got := rowFor(t, tbl, "big/"); !strings.Contains(got[1], "4.9 KiB") {
		t.Errorf("big/ size = %q, want the sum of its files", got[1])
	}
	if got := rowFor(t, tbl, "big/")[3]; got != "2" {
		t.Errorf("big/ file count = %q, want 2", got)
	}
}

// The share column is of what was scanned, which is what the reader is
// looking at — not of the disk, which they did not ask about.
func TestUsageSharesAreOfTheScannedTotal(t *testing.T) {
	root := fixture(t, map[string]int{"a.bin": 750, "b.bin": 250})
	tbl := run(t, runUsage, map[string]any{"path": root, "limit": 20}).(view.Table)
	if got := rowFor(t, tbl, "a.bin")[2]; got != "75.0%" {
		t.Errorf("a.bin share = %q, want 75.0%%", got)
	}
	if got := rowFor(t, tbl, "b.bin")[2]; got != "25.0%" {
		t.Errorf("b.bin share = %q, want 25.0%%", got)
	}
}

// An empty directory has a zero total, and a percentage of zero is a division
// by zero — the arithmetic that turns a listing into a crash.
func TestUsageOfAnEmptyDirectory(t *testing.T) {
	tbl := run(t, runUsage, map[string]any{"path": t.TempDir(), "limit": 20}).(view.Table)
	if len(tbl.Rows) != 0 {
		t.Errorf("an empty directory listed %v", tbl.Rows)
	}
	// And one holding only empty files, where the total is zero but there is
	// something to show.
	root := fixture(t, map[string]int{"empty.txt": 0})
	tbl = run(t, runUsage, map[string]any{"path": root, "limit": 20}).(view.Table)
	if got := rowFor(t, tbl, "empty.txt")[2]; got == "" || strings.Contains(got, "NaN") {
		t.Errorf("share of a zero total rendered as %q", got)
	}
}

// limit bounds what is shown, and Total has to keep reporting what exists —
// otherwise the reader takes the top 3 for the whole directory.
func TestUsageLimitDoesNotHideThatItLimited(t *testing.T) {
	root := fixture(t, map[string]int{"a": 5, "b": 4, "c": 3, "d": 2, "e": 1})
	tbl := run(t, runUsage, map[string]any{"path": root, "limit": 2}).(view.Table)
	if len(tbl.Rows) != 2 {
		t.Errorf("limit ignored: %d rows", len(tbl.Rows))
	}
	if tbl.Total != 5 {
		t.Errorf("Total = %d, want 5 — the reader must see that it was truncated", tbl.Total)
	}
}

// A symlink pointing at its own ancestor makes a naive walk run forever. This
// is the test that would hang rather than fail, which is why the walk uses
// Lstat and never follows one.
func TestUsageDoesNotFollowSymlinksIntoALoop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ")
	}
	root := fixture(t, map[string]int{"real/file.bin": 100})
	if err := os.Symlink(root, filepath.Join(root, "real", "loop")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	tbl := run(t, runUsage, map[string]any{"path": root, "limit": 20}).(view.Table)
	if len(tbl.Rows) == 0 {
		t.Fatal("no entries")
	}
	// The link counts as its own small entry, never as a second copy of the
	// tree it points at.
	if got := rowFor(t, tbl, "real/")[1]; strings.Contains(got, "MiB") || strings.Contains(got, "GiB") {
		t.Errorf("the symlink was followed: real/ measured %q", got)
	}
}

func TestUsageRefusesAFileAndSaysWhatToDoInstead(t *testing.T) {
	root := fixture(t, map[string]int{"a.txt": 1})
	_, err := runUsage(context.Background(),
		plugin.NewRequest(map[string]any{"path": filepath.Join(root, "a.txt")}, false, false))
	if err == nil {
		t.Fatal("a file was measured as a directory")
	}
	var verr *view.Error
	if !asViewError(err, &verr) {
		t.Fatalf("error is not a view.Error: %T", err)
	}
	if verr.Hint == "" {
		t.Error("the error should say what to do instead")
	}
}

func TestUsageOnAMissingPath(t *testing.T) {
	_, err := runUsage(context.Background(),
		plugin.NewRequest(map[string]any{"path": filepath.Join(t.TempDir(), "nope")}, false, false))
	if err == nil {
		t.Fatal("a missing path was scanned")
	}
	if !strings.Contains(err.Error(), "no such path") {
		t.Errorf("error should name the problem plainly: %v", err)
	}
}

func TestUsageDetailIsASectionedPage(t *testing.T) {
	root := fixture(t, map[string]int{"big/one.bin": 3000, "small.txt": 10})
	page, ok := run(t, runUsage, map[string]any{
		"path": root, "limit": 20, "detail": true,
	}).(view.Sections)
	if !ok {
		t.Fatal("detail did not return a page")
	}
	var titles []string
	for _, item := range page.Items {
		titles = append(titles, item.Title)
	}
	for _, want := range []string{"summary", "biggest entries", "largest files anywhere beneath"} {
		found := false
		for _, got := range titles {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("no %q section; got %v", want, titles)
		}
	}
}

func TestTreeShowsDirectoriesFirstThenNames(t *testing.T) {
	root := fixture(t, map[string]int{
		"zeta.txt": 1, "alpha.txt": 1, "zdir/x": 1, "adir/y": 1,
	})
	tr, ok := run(t, runTree, map[string]any{"path": root, "depth": 1, "limit": 12}).(view.Tree)
	if !ok {
		t.Fatal("tree did not return a Tree")
	}
	if len(tr.Roots) != 1 {
		t.Fatalf("want one root, got %d", len(tr.Roots))
	}
	var labels []string
	for _, n := range tr.Roots[0].Children {
		labels = append(labels, n.Label)
	}
	want := []string{"adir/", "zdir/", "alpha.txt", "zeta.txt"}
	if len(labels) != len(want) {
		t.Fatalf("children = %v, want %v", labels, want)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Errorf("child %d = %q, want %q (%v)", i, labels[i], want[i], labels)
		}
	}
}

// A branch that stopped listing looks exactly like an empty directory, and
// that is a lie about the filesystem.
func TestTreeSaysWhatItIsNotShowing(t *testing.T) {
	files := map[string]int{}
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		files["many/"+n] = 1
	}
	files[".hidden"] = 1
	root := fixture(t, files)

	tr := run(t, runTree, map[string]any{"path": root, "depth": 1, "limit": 12}).(view.Tree)
	details := map[string]string{}
	for _, n := range tr.Roots[0].Children {
		details[n.Label] = n.Detail
	}
	// Not descending is not the same as being empty.
	if !strings.Contains(details["many/"], "5 entries") {
		t.Errorf("a directory below the depth limit did not say how much it holds: %v", details)
	}
	if !strings.Contains(details["…"], "hidden") {
		t.Errorf("hidden entries were dropped without saying so: %v", details)
	}

	// And per-directory truncation.
	tr = run(t, runTree, map[string]any{"path": root, "depth": 2, "limit": 2}).(view.Tree)
	var found bool
	for _, n := range tr.Roots[0].Children {
		if n.Label == "many/" {
			for _, c := range n.Children {
				if c.Label == "…" && strings.Contains(c.Detail, "more") {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("a truncated directory did not say how many entries it hid")
	}
}

func TestTreeShowsSymlinkTargetsWithoutFollowingThem(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ")
	}
	root := fixture(t, map[string]int{"real/deep/file.bin": 10})
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	tr := run(t, runTree, map[string]any{"path": root, "depth": 3, "limit": 12}).(view.Tree)
	for _, n := range tr.Roots[0].Children {
		if n.Label != "link" {
			continue
		}
		if !strings.HasPrefix(n.Detail, "→ ") {
			t.Errorf("symlink detail = %q, want its target", n.Detail)
		}
		if len(n.Children) != 0 {
			t.Errorf("the symlink was descended into: %+v", n.Children)
		}
		return
	}
	t.Error("no link entry in the tree")
}

// depth 0 or negative would otherwise mean "show nothing", which reads as an
// empty directory.
func TestTreeDepthIsAtLeastOne(t *testing.T) {
	root := fixture(t, map[string]int{"a.txt": 1})
	for _, depth := range []int{0, -1} {
		tr := run(t, runTree, map[string]any{"path": root, "depth": depth, "limit": 12}).(view.Tree)
		if len(tr.Roots[0].Children) == 0 {
			t.Errorf("depth %d showed nothing at all", depth)
		}
	}
}

func TestHashMatchesAndSaysSo(t *testing.T) {
	root := fixture(t, map[string]int{"f.bin": 0})
	path := filepath.Join(root, "f.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("hello"))
	want := hex.EncodeToString(sum[:])

	kv := run(t, runHash, map[string]any{"path": path, "algo": "sha256"}).(view.KeyValue)
	if got := pairValue(kv, "sha256"); got != want {
		t.Errorf("sha256 = %q, want %q", got, want)
	}

	// The comparison is the point: two hex strings are what people are bad at.
	for _, spelling := range []string{
		want, strings.ToUpper(want), "sha256:" + want, "  " + want + "  ",
		want + "  f.bin", "*" + want,
	} {
		kv = run(t, runHash, map[string]any{"path": path, "algo": "sha256", "expect": spelling}).(view.KeyValue)
		if got := pairValue(kv, "match"); !strings.HasPrefix(got, "yes") {
			t.Errorf("expect %q did not match: %q", spelling, got)
		}
	}
	kv = run(t, runHash, map[string]any{"path": path, "algo": "sha256", "expect": "deadbeef"}).(view.KeyValue)
	if got := pairValue(kv, "match"); !strings.HasPrefix(got, "NO") {
		t.Errorf("a wrong checksum reported %q", got)
	}
	if pairValue(kv, "expected") != "deadbeef" {
		t.Error("a mismatch should show what was expected")
	}
}

// sha1 and md5 are offered because projects still publish them, and the
// output has to say what they are worth.
func TestWeakHashesCarryTheirCaveat(t *testing.T) {
	root := fixture(t, map[string]int{"f.bin": 4})
	path := filepath.Join(root, "f.bin")
	for algo, weak := range map[string]bool{"md5": true, "sha1": true, "sha256": false, "sha512": false} {
		kv := run(t, runHash, map[string]any{"path": path, "algo": algo}).(view.KeyValue)
		note := pairValue(kv, "note")
		if weak && note == "" {
			t.Errorf("%s carries no caveat", algo)
		}
		if !weak && note != "" {
			t.Errorf("%s should need no caveat, got %q", algo, note)
		}
	}
}

func TestHashRejectsUnknownAlgorithmsAndDirectories(t *testing.T) {
	root := fixture(t, map[string]int{"f.bin": 1})
	_, err := runHash(context.Background(), plugin.NewRequest(
		map[string]any{"path": filepath.Join(root, "f.bin"), "algo": "sha3"}, false, false))
	if err == nil {
		t.Error("an unknown algorithm was accepted")
	} else if !strings.Contains(err.Error(), "sha3") {
		t.Errorf("the error should name what was rejected: %v", err)
	}

	if _, err := runHash(context.Background(),
		plugin.NewRequest(map[string]any{"path": root, "algo": "sha256"}, false, false)); err == nil {
		t.Error("a directory was hashed")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0: "0 B", 1: "1 B", 1023: "1023 B", 1024: "1.0 KiB",
		1536: "1.5 KiB", 1048576: "1.0 MiB", 1073741824: "1.0 GiB",
	}
	for n, want := range cases {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
	// A very large size must not run off the end of the unit table.
	if got := humanBytes(1 << 62); strings.Contains(got, "%!") || got == "" {
		t.Errorf("humanBytes(1<<62) = %q", got)
	}
}

func pairValue(kv view.KeyValue, key string) string {
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

func asViewError(err error, target **view.Error) bool {
	if v, ok := err.(*view.Error); ok {
		*target = v
		return true
	}
	return false
}

// fs.tree is the fs dashboard tile (fs.usage carries NoPreview), so this is
// the page opening that tile lands on. Before it existed, fs was the only
// plugin whose tile expanded into exactly what the tile already showed.
func TestTreeDetailIsAComposedPage(t *testing.T) {
	files := map[string]int{}
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		files["many/"+n] = 1
	}
	files[".hidden"] = 1
	files["deep/down/far.txt"] = 1
	// Four visible entries at the root against --limit 2, so the trim is real
	// rather than assumed: the first version of this test counted two and
	// truncated nothing.
	files["loose1.txt"] = 1
	files["loose2.txt"] = 1
	root := fixture(t, files)

	v := run(t, runTree, map[string]any{"path": root, "depth": 1, "limit": 2, "detail": true})
	page, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("detail returned %T, want view.Sections", v)
	}
	keys := map[string]view.View{}
	for _, s := range page.Items {
		keys[s.Key()] = s.View
	}
	// Addressed by section id, which is what Section.Key() returns when one
	// is set — the stable handle, kebab-case like every other identifier in
	// rta, as opposed to the title, which is prose and free to change.
	for _, want := range []string{"summary", "tree", "not-shown"} {
		if _, ok := keys[want]; !ok {
			t.Errorf("no %q section: %v", want, page.Items)
		}
	}
	if _, ok := keys["tree"].(view.Tree); !ok {
		t.Errorf("the tree section holds %T, not the tree", keys["tree"])
	}

	// The point of the section: every bound in one place. Scattered through
	// the branches they happened in, these are what a reader has to find and
	// recognise before concluding the tree is complete — and the markers for
	// depth, --limit and dotfiles look nothing alike.
	missing, ok := keys["not-shown"].(view.KeyValue)
	if !ok {
		t.Fatalf("not-shown holds %T, want KeyValue", keys["not-shown"])
	}
	got := map[string]string{}
	for _, p := range missing.Pairs {
		got[p.Key] = p.Value
	}
	for _, want := range []string{"below --depth", "past --limit", "hidden"} {
		if got[want] == "" {
			t.Errorf("no %q entry: %v", want, got)
		}
	}
	// depth 1 stops at both directories; limit 2 trims the third entry.
	if !strings.Contains(got["below --depth"], "2 directories") {
		t.Errorf("below --depth = %q, want both stopped directories counted", got["below --depth"])
	}
	if !strings.Contains(got["hidden"], "1 dotfile") {
		t.Errorf("hidden = %q, want the one dotfile counted", got["hidden"])
	}
}

// "complete" and "this build forgot to count" must not render identically.
func TestTreeDetailSaysSoWhenNothingIsHidden(t *testing.T) {
	root := fixture(t, map[string]int{"a.txt": 1, "sub/b.txt": 1})
	page := run(t, runTree, map[string]any{
		"path": root, "depth": 5, "limit": 100, "detail": true,
	}).(view.Sections)
	for _, s := range page.Items {
		if s.Key() != "not-shown" {
			continue
		}
		txt, ok := s.View.(view.Text)
		if !ok {
			t.Fatalf("not shown holds %T, want a Text saying the tree is complete", s.View)
		}
		if !strings.Contains(txt.Body, "Nothing") {
			t.Errorf("not shown = %q", txt.Body)
		}
		return
	}
	t.Error("a complete tree omitted the section entirely, which reads as an unfinished page")
}

// The tree's detail page answers the other half of the question.
//
// Every other detail page in the catalogue is composed from what sibling
// capabilities already return (plugin.Page), and this one was not: it showed
// the walk it had just done and stopped. The reason recorded in the code was
// that fs.usage is a recursive scan carrying NoPreview — which conflated two
// different things. NoPreview means do not run me *unprompted*, because the
// dashboard refreshes on a timer. A detail page is the opposite case:
// somebody pressed enter.
//
// The objection underneath it was real and is answered by passing the bound
// rather than dropping the section, which is what this test pins: the
// composed scan gets this request's own --depth, so it walks the same shape
// the tree just walked instead of starting an unbounded scan from a keypress.
func TestTreeDetailComposesUsageWithinTheSameBound(t *testing.T) {
	root := t.TempDir()
	// deep/ sits below --depth 1, so an unbounded scan would count it and a
	// correctly bounded one would not.
	deep := filepath.Join(root, "shallow", "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "top.bin"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "buried.bin"), make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	v := run(t, runTree, map[string]any{"path": root, "depth": 1, "limit": 12, "detail": true})
	secs, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("detail view is %T, not sections", v)
	}
	var largest view.View
	ids := make([]string, 0, len(secs.Items))
	for _, s := range secs.Items {
		ids = append(ids, s.ID)
		if s.ID == "largest" {
			largest = s.View
		}
	}
	if largest == nil {
		t.Fatalf("no `largest` section: the page is %v", ids)
	}
	tbl, ok := largest.(view.Table)
	if !ok {
		t.Fatalf("largest section is %T, want a table", largest)
	}
	if len(tbl.Rows) == 0 {
		t.Fatal("the largest-entries section is empty")
	}

	// Bounded: at --depth 1 the scan must not have descended into shallow/
	// to find the 1 MiB file, so shallow/ is reported at the size of what
	// the bound let it see rather than at 1 MiB.
	for _, row := range tbl.Rows {
		if strings.HasPrefix(row[0], "shallow") && strings.Contains(row[1], "MiB") {
			t.Errorf("the composed scan ignored --depth: %v", row)
		}
	}
}
