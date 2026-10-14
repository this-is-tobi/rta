package gitignore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// fakeRepo stands in for both hosts: the listing GitHub's API gives and the raw
// files beside it. A test makes either answer wrongly by setting a field.
type fakeRepo struct {
	files map[string]string // path -> content, the repository as it is

	treeID     string
	treeStatus int         // 0 is 200
	treeHeader http.Header // set on the listing's response
	truncated  bool
	extra      []map[string]any // entries added to the listing as they are

	rawStatus map[string]int    // path -> status to answer with instead of the file
	rawBody   map[string]string // path -> body to serve instead of the listed file

	// treeHold, when set, keeps the listing from being answered until it is
	// closed or the request is given up.
	treeHold chan struct{}

	mu   sync.Mutex
	hits map[string]int
	srv  *httptest.Server
}

func newRepo(t *testing.T, files map[string]string) (*fakeRepo, *source) {
	t.Helper()
	f := &fakeRepo{files: files, treeID: "0123456789abcdef0123456789abcdef01234567", hits: map[string]int{},
		rawStatus: map[string]int{}, rawBody: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/github/gitignore/git/trees/main", func(w http.ResponseWriter, r *http.Request) {
		f.count("tree")
		if r.URL.Query().Get("recursive") != "1" {
			t.Errorf("the listing was asked for without recursive=1: %s", r.URL.RawQuery)
		}
		if f.treeHold != nil {
			select {
			case <-f.treeHold:
			case <-r.Context().Done():
				return
			}
		}
		for k, v := range f.treeHeader {
			w.Header()[k] = v
		}
		if f.treeStatus != 0 {
			w.WriteHeader(f.treeStatus)
			return
		}
		items := []map[string]any{{"path": "Global", "type": "tree", "sha": strings.Repeat("a", 40)}}
		for p, content := range f.files {
			items = append(items, map[string]any{"path": p, "type": "blob", "sha": blobID([]byte(content)), "size": len(content)})
		}
		items = append(items, f.extra...)
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": f.treeID, "truncated": f.truncated, "tree": items})
	})
	mux.HandleFunc("/github/gitignore/main/", func(w http.ResponseWriter, r *http.Request) {
		f.count("raw")
		p := strings.TrimPrefix(r.URL.Path, "/github/gitignore/main/")
		if st := f.rawStatus[p]; st != 0 {
			w.WriteHeader(st)
			return
		}
		body, ok := f.rawBody[p]
		if !ok {
			body, ok = f.files[p]
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f, &source{api: f.srv.URL, raw: f.srv.URL, client: f.srv.Client()}
}

func (f *fakeRepo) count(kind string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits[kind]++
}

func (f *fakeRepo) got(kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[kind]
}

// The repository the tests read: shaped like the real one, including the two
// kinds of name it shares between files (Racket is a main template and a
// community one; ColdBox is in two community folders).
func sample() map[string]string {
	return map[string]string{
		"Go.gitignore":                                "*.exe\n*.test\n",
		"Node.gitignore":                              "node_modules/\n",
		"Racket.gitignore":                            "compiled/\n",
		"Python.gitignore":                            "__pycache__/\n",
		"Global/macOS.gitignore":                      ".DS_Store\n",
		"Global/Vim.gitignore":                        "*.swp\n",
		"Global/VisualStudioCode.gitignore":           ".vscode/*\n",
		"Global/JetBrains.gitignore":                  ".idea/\n",
		"community/Alteryx.gitignore":                 "*.yxzp\n",
		"community/Racket.gitignore":                  "racket-community/\n",
		"community/BoxLang/ColdBox.gitignore":         "boxlang/\n",
		"community/CFML/ColdBox.gitignore":            "cfml/\n",
		"community/Python/JupyterNotebooks.gitignore": ".ipynb_checkpoints\n",
	}
}

func request(t *testing.T, c plugin.Capability, values map[string]any) plugin.Request {
	t.Helper()
	return plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), false, false)
}

func list(t *testing.T, src *source, values map[string]any) (view.Table, error) {
	t.Helper()
	v, err := runListAt(context.Background(), request(t, listCapability(), values), src)
	if err != nil {
		return view.Table{}, err
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("list answered a %T, want a table", v)
	}
	return tbl, nil
}

func show(t *testing.T, src *source, names ...string) (string, error) {
	t.Helper()
	v, err := runShowAt(context.Background(), request(t, showCapability(), map[string]any{"templates": names}), src)
	if err != nil {
		return "", err
	}
	txt, ok := v.(view.Text)
	if !ok {
		t.Fatalf("show answered a %T, want text", v)
	}
	return txt.Body, nil
}

func asViewError(t *testing.T, err error) *view.Error {
	t.Helper()
	var ve *view.Error
	if !errors.As(err, &ve) {
		t.Fatalf("error %v is not a *view.Error", err)
	}
	return ve
}

func column(tbl view.Table, i int) []string {
	out := make([]string, len(tbl.Rows))
	for r, row := range tbl.Rows {
		out[r] = row[i]
	}
	return out
}

// --- the listing ---

func TestTheListIsInKindOrderAndThenByName(t *testing.T) {
	_, src := newRepo(t, sample())
	tbl, err := list(t, src, map[string]any{"limit": 100})
	if err != nil {
		t.Fatal(err)
	}
	names := strings.Join(column(tbl, 0), ",")
	kinds := strings.Join(column(tbl, 1), ",")
	if !strings.HasPrefix(names, "Go,Node,Python,Racket,JetBrains,macOS,Vim,VisualStudioCode,Alteryx,") {
		t.Errorf("templates = %s", names)
	}
	if !strings.HasPrefix(kinds, "main,main,main,main,global,global,global,global,community,") {
		t.Errorf("kinds = %s", kinds)
	}
	if tbl.Total != len(sample()) {
		t.Errorf("total = %d, want %d", tbl.Total, len(sample()))
	}
}

func TestABareNameIsTheTemplatesNameUnlessAnotherFileHasItToo(t *testing.T) {
	_, src := newRepo(t, sample())
	tbl, err := list(t, src, map[string]any{"term": "racket"})
	if err != nil {
		t.Fatal(err)
	}
	// The main one keeps the bare name; the community one with the same name
	// is spelled by its path, which is what show takes for it.
	if got := strings.Join(column(tbl, 0), ","); got != "Racket,community/Racket" {
		t.Errorf("racket rows = %s", got)
	}
	tbl, _ = list(t, src, map[string]any{"term": "coldbox"})
	if got := strings.Join(column(tbl, 0), ","); got != "community/BoxLang/ColdBox,community/CFML/ColdBox" {
		t.Errorf("coldbox rows = %s", got)
	}
}

func TestEverythingTheListNamesIsATemplateShowTakesBack(t *testing.T) {
	_, src := newRepo(t, sample())
	tbl, err := list(t, src, map[string]any{"limit": 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range column(tbl, 0) {
		out, err := show(t, src, name)
		if err != nil {
			t.Errorf("the list names %q and show refuses it: %v", name, err)
			continue
		}
		if !strings.Contains(out, "### "+name+" ###\n") {
			t.Errorf("show %q printed another template: %q", name, out)
		}
	}
}

func TestTheListNarrowsByTermAndKind(t *testing.T) {
	_, src := newRepo(t, sample())
	tbl, _ := list(t, src, map[string]any{"term": "VIM"})
	if got := strings.Join(column(tbl, 0), ","); got != "Vim" {
		t.Errorf("term VIM = %s", got)
	}
	tbl, _ = list(t, src, map[string]any{"kind": "global", "limit": 100})
	if got := strings.Join(column(tbl, 0), ","); got != "JetBrains,macOS,Vim,VisualStudioCode" {
		t.Errorf("kind global = %s", got)
	}
	tbl, _ = list(t, src, map[string]any{"term": "python", "kind": "community"})
	// Its name is unique upstream, so the list gives the bare name, which show
	// takes back (and so does the path, which another test holds).
	if got := strings.Join(column(tbl, 0), ","); got != "JupyterNotebooks" {
		t.Errorf("python in community = %s", got)
	}
}

// "unity" is part of "community", so a term read against the whole path listed
// every community template for it, and "main" matched nothing since a main
// template has no folder to say so. A term is part of the name a row shows,
// and a kind's own word lists that kind.
func TestATermIsPartOfANameAndNotOfTheFolderThatSaysAKind(t *testing.T) {
	files := sample()
	files["Unity.gitignore"] = "Library/\n"
	files["community/Mainsail.gitignore"] = "sail/\n"
	_, src := newRepo(t, files)
	for term, want := range map[string]string{
		"unity":  "Unity",
		"UNITY":  "Unity",
		"python": "Python,JupyterNotebooks", // a community template's folder is part of its name
		// The kind's word lists the kind, and a name that holds the word stays
		// listed beside it.
		"main":      "Go,Node,Python,Racket,Unity,Mainsail",
		"global":    "JetBrains,macOS,Vim,VisualStudioCode",
		"Global":    "JetBrains,macOS,Vim,VisualStudioCode",
		"community": "Alteryx,community/BoxLang/ColdBox,community/CFML/ColdBox,Mainsail,JupyterNotebooks,community/Racket",
	} {
		tbl, err := list(t, src, map[string]any{"term": term, "limit": 100})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(column(tbl, 0), ","); got != want {
			t.Errorf("term %q = %s, want %s", term, got, want)
		}
	}
	// A kind and a term together are both held to.
	tbl, _ := list(t, src, map[string]any{"term": "main", "kind": "community"})
	if got := strings.Join(column(tbl, 0), ","); got != "Mainsail" {
		t.Errorf("main in community = %s", got)
	}
}

func TestAMissingFileAndAMissingListingAreWordedAsSentences(t *testing.T) {
	repo, src := newRepo(t, sample())
	repo.rawStatus["Go.gitignore"] = http.StatusNotFound
	_, err := show(t, src, "Go")
	if ve := asViewError(t, err); ve.Code != "gitignore.response.notfound" || ve.Message != "GitHub does not have the Go template" {
		t.Errorf("a missing file: %+v", ve)
	}
	repo.treeStatus = http.StatusNotFound
	_, err = list(t, src, nil)
	if ve := asViewError(t, err); ve.Message != "GitHub does not have the template listing" {
		t.Errorf("a missing listing: %+v", ve)
	}
}

func TestACutListSaysHowManyThereAre(t *testing.T) {
	_, src := newRepo(t, sample())
	tbl, _ := list(t, src, map[string]any{"limit": 3})
	if len(tbl.Rows) != 3 || tbl.Total != len(sample()) {
		t.Errorf("%d rows of %d, want 3 of %d", len(tbl.Rows), tbl.Total, len(sample()))
	}
}

func TestAListWithNothingInItSaysSoAndNamesTheWayOn(t *testing.T) {
	_, src := newRepo(t, sample())
	tbl, err := list(t, src, map[string]any{"term": "nonesuch"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tbl.Rows) != 0 || !strings.Contains(tbl.Empty, `"nonesuch"`) || !strings.Contains(tbl.Empty, "gitignore list") {
		t.Errorf("rows %d, empty %q", len(tbl.Rows), tbl.Empty)
	}
}

func TestTheListingIsAskedOnceForAsLongAsItIsTrusted(t *testing.T) {
	repo, src := newRepo(t, sample())
	for range 3 {
		if _, err := list(t, src, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := show(t, src, "Go"); err != nil {
		t.Fatal(err)
	}
	if n := repo.got("tree"); n != 1 {
		t.Errorf("the listing was asked for %d times, want once", n)
	}
	src.at = time.Now().Add(-2 * listingLife)
	if _, err := list(t, src, nil); err != nil {
		t.Fatal(err)
	}
	if n := repo.got("tree"); n != 2 {
		t.Errorf("after the listing went stale it was asked for %d times in all, want 2", n)
	}
}

func TestAnEntryTheListingGetsWrongIsLeftOut(t *testing.T) {
	repo, src := newRepo(t, sample())
	good := strings.Repeat("b", 40)
	repo.extra = []map[string]any{
		{"path": "../Escape.gitignore", "type": "blob", "sha": good},
		{"path": "Global/../../Up.gitignore", "type": "blob", "sha": good},
		{"path": "/Rooted.gitignore", "type": "blob", "sha": good},
		{"path": `Back\slash.gitignore`, "type": "blob", "sha": good},
		{"path": "Esc\x1b[31m.gitignore", "type": "blob", "sha": good},
		{"path": "BadId.gitignore", "type": "blob", "sha": "NOT-A-HASH"},
		{"path": "NotATemplate.txt", "type": "blob", "sha": good},
		{"path": ".gitignore", "type": "blob", "sha": good},
		{"path": "docs/Elsewhere.gitignore", "type": "blob", "sha": good},
		{"path": "Dir.gitignore", "type": "tree", "sha": good},
	}
	tbl, err := list(t, src, map[string]any{"limit": 100})
	if err != nil {
		t.Fatal(err)
	}
	if tbl.Total != len(sample()) {
		t.Errorf("%d entries listed, want only the %d real ones: %v", tbl.Total, len(sample()), column(tbl, 0))
	}
}

// --- show ---

func TestAnAnswerIsEachTemplateUnderItsNameAsItWasInTheOrderAsked(t *testing.T) {
	_, src := newRepo(t, sample())
	got, err := show(t, src, "macOS", "Go")
	if err != nil {
		t.Fatal(err)
	}
	want := "# github/gitignore, tree 0123456: macOS, Go\n" +
		"# Each section is the text of one file from that tree, with nothing merged or deduplicated.\n" +
		"\n### macOS ###\n.DS_Store\n" +
		"\n### Go ###\n*.exe\n*.test\n"
	if got != want {
		t.Errorf("answer =\n%q\nwant\n%q", got, want)
	}
}

func TestATemplateAskedForTwiceIsPrintedOnceWhateverTheCase(t *testing.T) {
	_, src := newRepo(t, sample())
	got, err := show(t, src, "go", "Global/Vim", "GO", "vim")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, "### Go ###"); n != 1 {
		t.Errorf("Go printed %d times", n)
	}
	if !strings.Contains(got, ": Go, Global/Vim\n") && !strings.Contains(got, ": Go, Vim\n") {
		t.Errorf("header does not name the two templates once each: %q", strings.SplitN(got, "\n", 2)[0])
	}
}

func TestNamesJoinedByCommasAreSeveralNames(t *testing.T) {
	_, src := newRepo(t, sample())
	got, err := show(t, src, "go,macos", " node ")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"### Go ###", "### macOS ###", "### Node ###"} {
		if !strings.Contains(got, h) {
			t.Errorf("answer lacks %s", h)
		}
	}
}

func TestAPathNamesATemplateWithOrWithoutItsExtensionAndItsCommunityFolder(t *testing.T) {
	_, src := newRepo(t, sample())
	for _, name := range []string{"Global/macOS", "global/macos", "Global/macOS.gitignore", "GLOBAL/MACOS.GITIGNORE"} {
		got, err := show(t, src, name)
		if err != nil || !strings.Contains(got, ".DS_Store") {
			t.Errorf("%q: %v / %q", name, err, got)
		}
	}
	for _, name := range []string{"community/Python/JupyterNotebooks", "Python/JupyterNotebooks", "python/jupyternotebooks"} {
		got, err := show(t, src, name)
		if err != nil || !strings.Contains(got, ".ipynb_checkpoints") {
			t.Errorf("%q: %v / %q", name, err, got)
		}
	}
}

func TestABareNameThatTwoKindsShareMeansTheMainOne(t *testing.T) {
	_, src := newRepo(t, sample())
	got, err := show(t, src, "Racket")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "compiled/") || strings.Contains(got, "racket-community/") {
		t.Errorf("Racket was the community one: %q", got)
	}
	got, err = show(t, src, "community/Racket")
	if err != nil || !strings.Contains(got, "racket-community/") {
		t.Errorf("community/Racket: %v / %q", err, got)
	}
}

func TestABareNameThatTwoCommunityFoldersShareIsRefusedWithBothPaths(t *testing.T) {
	repo, src := newRepo(t, sample())
	_, err := show(t, src, "ColdBox")
	ve := asViewError(t, err)
	if ve.Code != "gitignore.template.ambiguous" ||
		!strings.Contains(ve.Message, "community/BoxLang/ColdBox") || !strings.Contains(ve.Message, "community/CFML/ColdBox") {
		t.Errorf("error = %+v", ve)
	}
	if !strings.Contains(ve.Hint, "community/BoxLang/ColdBox") {
		t.Errorf("hint %q does not show the way", ve.Hint)
	}
	if repo.got("raw") != 0 {
		t.Error("a file was fetched for a name that was refused")
	}
}

func TestANicknameStandsForItsTemplateOnlyWhereNothingElseMatches(t *testing.T) {
	_, src := newRepo(t, sample())
	for nick, want := range map[string]string{"vscode": "### VisualStudioCode ###", "golang": "### Go ###", "mac": "### macOS ###", "idea": "### JetBrains ###", "py": "### Python ###"} {
		got, err := show(t, src, nick)
		if err != nil || !strings.Contains(got, want) {
			t.Errorf("%s: %v / %q", nick, err, got)
		}
	}
	// An exact name wins over a nickname that could also have been meant.
	files := sample()
	files["Mac.gitignore"] = "mac-exact/\n"
	_, src = newRepo(t, files)
	got, err := show(t, src, "mac")
	if err != nil || !strings.Contains(got, "mac-exact/") {
		t.Errorf("the exact name lost to its nickname: %v / %q", err, got)
	}
}

func TestAnUnknownTemplateSaysWhatItMightHaveBeen(t *testing.T) {
	repo, src := newRepo(t, sample())
	cases := map[string]string{
		"golng":  "Go",               // a typo of a nickname
		"pythom": "Python",           // a typo of a name
		"jet":    "JetBrains",        // a part of one
		"visual": "VisualStudioCode", // a part of one
	}
	for typed, want := range cases {
		_, err := show(t, src, typed)
		ve := asViewError(t, err)
		if ve.Code != "gitignore.template.unknown" || !strings.Contains(ve.Hint, "did you mean "+want) {
			t.Errorf("%s: error %+v, hint should suggest %s", typed, ve, want)
		}
	}
	_, err := show(t, src, "qqqqqqqq")
	ve := asViewError(t, err)
	if strings.Contains(ve.Hint, "did you mean") || !strings.Contains(ve.Hint, "gitignore list") {
		t.Errorf("a name near nothing was given a suggestion: %q", ve.Hint)
	}
	if repo.got("raw") != 0 {
		t.Error("a file was fetched for a name that was refused")
	}
}

// What a caller types reaches a URL only through the listing: a name that is
// not in it is refused before anything is requested, whatever it spells.
func TestNothingACallerTypesIsPutInAURLUnlessTheListingHoldsIt(t *testing.T) {
	repo, src := newRepo(t, sample())
	for _, typed := range []string{"../../etc/passwd", "Global/../Go", "https://example.com/x", "Go.gitignore?x=1", "%2e%2e/Go", "Go/", "/Go"} {
		_, err := show(t, src, typed)
		if err == nil {
			t.Errorf("%q was accepted", typed)
			continue
		}
		if ve := asViewError(t, err); ve.Code != "gitignore.template.unknown" {
			t.Errorf("%q: code %s", typed, ve.Code)
		}
	}
	if n := repo.got("raw"); n != 0 {
		t.Errorf("%d files were requested for names the listing does not hold", n)
	}
}

func TestTooManyTemplatesAreRefusedBeforeAnythingIsAsked(t *testing.T) {
	repo, src := newRepo(t, sample())
	names := make([]string, maxTemplates+1)
	for i := range names {
		names[i] = "Go" + strconv.Itoa(i)
	}
	_, err := show(t, src, names...)
	if ve := asViewError(t, err); ve.Code != "gitignore.templates.toomany" {
		t.Errorf("code = %s", ve.Code)
	}
	if repo.got("tree") != 0 {
		t.Error("the listing was asked for before the count was checked")
	}
}

func TestNamingNothingIsRefusedWithTheWayOn(t *testing.T) {
	_, src := newRepo(t, sample())
	_, err := show(t, src, " ", ",")
	ve := asViewError(t, err)
	if ve.Code != "gitignore.templates.none" || !strings.Contains(ve.Hint, "gitignore list") {
		t.Errorf("error = %+v", ve)
	}
}

func TestASectionEndsInANewlineSoTheNextHeadingStandsAlone(t *testing.T) {
	files := sample()
	files["Go.gitignore"] = "*.exe" // no newline at the end
	_, src := newRepo(t, files)
	got, err := show(t, src, "Go", "Node")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "\n### Go ###\n*.exe\n\n### Node ###\n") {
		t.Errorf("sections run together: %q", got)
	}
}

// --- what a response can get wrong ---

func TestAFileThatIsNotTheOneTheListingNamedIsRefused(t *testing.T) {
	repo, src := newRepo(t, sample())
	repo.rawBody["Go.gitignore"] = "*.exe\n# a commit landed in between\n"
	_, err := show(t, src, "Go")
	ve := asViewError(t, err)
	if ve.Code != "gitignore.template.changed" || !ve.Retryable {
		t.Errorf("error = %+v, want a retryable gitignore.template.changed", ve)
	}
}

func TestAFileThatIsNotTextIsNotPrinted(t *testing.T) {
	files := sample()
	files["Go.gitignore"] = "\xff\xfe\x00binary"
	_, src := newRepo(t, files)
	_, err := show(t, src, "Go")
	if ve := asViewError(t, err); ve.Code != "gitignore.template.notplain" {
		t.Errorf("code = %s", ve.Code)
	}
}

// A file that is not plain text is refused outright: nothing is printed with
// the characters a renderer would remove left out of it, which would be a
// template that says something else.
func TestAFileWithAControlCharacterOtherThanATabOrALineBreakIsNotPrinted(t *testing.T) {
	for name, body := range map[string]string{
		"an escape sequence": "*.exe\n\x1b[31mred\n",
		"a form feed":        "a\fb\n",
		"a vertical tab":     "a\vb\n",
		"a delete":           "a\x7fb\n",
		"a C1 control":       "a\u009bb\n",
		"a NUL":              "a\x00b\n",
	} {
		files := sample()
		files["Go.gitignore"] = body
		_, src := newRepo(t, files)
		_, err := show(t, src, "Go")
		if ve := asViewError(t, err); ve.Code != "gitignore.template.notplain" {
			t.Errorf("%s: code = %s", name, ve.Code)
		}
	}
}

// Tabs, line feeds and carriage returns are what a .gitignore is written with,
// and a real one uses the last: the macOS template holds one inside the
// brackets of two patterns, and two others end their lines with one. They come
// back as they are.
func TestATemplateMayHoldTabsAndCarriageReturns(t *testing.T) {
	files := sample()
	files["Global/macOS.gitignore"] = "Icon[\r]\n.HFS+ Private Directory Data[\r]\n"
	files["Go.gitignore"] = "*.exe\r\n\t*.test\r\n"
	_, src := newRepo(t, files)
	got, err := show(t, src, "macOS", "Go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Icon[\r]\n", ".HFS+ Private Directory Data[\r]\n", "*.exe\r\n\t*.test\r\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("answer lacks %q: %q", want, got)
		}
	}
}

// --- a listing that stops being true ---

// A listing a long-lived process kept goes out of date when upstream commits,
// and a file it names is then not the one it describes. Asked again with the
// same listing, the file is refused the same way for as long as the listing is
// kept.
func TestAFileThatChangedSinceAKeptListingIsReadAgainFromAFreshOne(t *testing.T) {
	repo, src := newRepo(t, sample())
	if _, err := show(t, src, "Go"); err != nil {
		t.Fatal(err)
	}
	repo.files["Go.gitignore"] = "*.exe\n# a commit landed\n"
	got, err := show(t, src, "Go")
	if err != nil {
		t.Fatalf("the second call: %v", err)
	}
	if !strings.Contains(got, "# a commit landed") {
		t.Errorf("answer = %q", got)
	}
	if n := repo.got("tree"); n != 2 {
		t.Errorf("the listing was read %d times, want twice", n)
	}
}

// A listing read for this call that the file still contradicts is a host
// behind its own listing, which reading the listing again now does not mend.
// It is dropped all the same, so that asking again is a true thing to say.
func TestAFileThatDiffersFromAListingJustReadIsNotAskedForAgain(t *testing.T) {
	repo, src := newRepo(t, sample())
	repo.rawBody["Go.gitignore"] = "*.exe\n# the host lags\n"
	_, err := show(t, src, "Go")
	if ve := asViewError(t, err); ve.Code != "gitignore.template.changed" || !ve.Retryable {
		t.Errorf("error = %+v", ve)
	}
	if n := repo.got("tree"); n != 1 {
		t.Errorf("the listing was read %d times for one call, want once", n)
	}
	delete(repo.rawBody, "Go.gitignore")
	if _, err := show(t, src, "Go"); err != nil {
		t.Errorf("asking again: %v", err)
	}
	if n := repo.got("tree"); n != 2 {
		t.Errorf("the listing was read %d times after asking again, want it read afresh", n)
	}
}

func TestAFileThatKeepsDifferingIsAskedForOnceMoreAndNoMore(t *testing.T) {
	repo, src := newRepo(t, sample())
	if _, err := show(t, src, "Go"); err != nil {
		t.Fatal(err)
	}
	repo.rawBody["Go.gitignore"] = "*.exe\n# not what the listing says\n"
	_, err := show(t, src, "Go")
	if ve := asViewError(t, err); ve.Code != "gitignore.template.changed" {
		t.Errorf("error = %+v", ve)
	}
	if n := repo.got("tree"); n != 2 {
		t.Errorf("the listing was read %d times, want the kept one and one more", n)
	}
}

func TestAFileThatIsGoneDropsTheListingThatNamedIt(t *testing.T) {
	repo, src := newRepo(t, sample())
	if _, err := show(t, src, "Go"); err != nil {
		t.Fatal(err)
	}
	delete(repo.files, "Go.gitignore")
	_, err := show(t, src, "Go")
	if ve := asViewError(t, err); ve.Code != "gitignore.template.unknown" {
		t.Errorf("error = %+v, want the name to be unknown to the listing read afresh", ve)
	}
}

// --- waiting for the listing ---

func awaitHits(t *testing.T, repo *fakeRepo, kind string, n int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); repo.got(kind) < n; {
		if time.Now().After(deadline) {
			t.Fatalf("%d %s requests arrived, want %d", repo.got(kind), kind, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// One request for the listing is made at a time, and a caller that arrives
// while it is in flight waits for it. It waits only as long as its own context
// allows.
func TestACallerWaitingForTheListingLeavesWhenItsContextEnds(t *testing.T) {
	repo, src := newRepo(t, sample())
	repo.treeHold = make(chan struct{})
	release := sync.OnceFunc(func() { close(repo.treeHold) })
	defer release()

	first := make(chan error, 1)
	go func() {
		_, err := runListAt(context.Background(), request(t, listCapability(), nil), src)
		first <- err
	}()
	awaitHits(t, repo, "tree", 1)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	second := make(chan error, 1)
	go func() {
		_, err := runListAt(ctx, request(t, listCapability(), nil), src)
		second <- err
	}()
	select {
	case err := <-second:
		if ve := asViewError(t, err); ve.Code != "gitignore.request.failed" || !ve.Retryable {
			t.Errorf("error = %+v", ve)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a caller whose context ended was still waiting for another's request")
	}
	if n := repo.got("tree"); n != 1 {
		t.Errorf("%d requests for the listing, want the one in flight", n)
	}

	release()
	if err := <-first; err != nil {
		t.Fatalf("the caller that was asking: %v", err)
	}
	if _, err := list(t, src, nil); err != nil {
		t.Fatal(err)
	}
	if n := repo.got("tree"); n != 1 {
		t.Errorf("%d requests for the listing, want what the first read to be kept", n)
	}
}

// What a caller that was asking could not finish is not a failure of the ones
// waiting behind it: the next of them asks for itself.
func TestACallerThatGaveUpDoesNotFailTheOnesWaitingBehindIt(t *testing.T) {
	repo, src := newRepo(t, sample())
	repo.treeHold = make(chan struct{})
	release := sync.OnceFunc(func() { close(repo.treeHold) })
	defer release()

	askingCtx, giveUp := context.WithCancel(context.Background())
	asking := make(chan error, 1)
	go func() {
		_, err := runListAt(askingCtx, request(t, listCapability(), nil), src)
		asking <- err
	}()
	awaitHits(t, repo, "tree", 1)

	waiting := make(chan error, 1)
	go func() {
		_, err := runListAt(context.Background(), request(t, listCapability(), nil), src)
		waiting <- err
	}()
	giveUp()
	if err := <-asking; err == nil {
		t.Fatal("the caller that gave up was answered")
	}
	awaitHits(t, repo, "tree", 2)
	release()
	select {
	case err := <-waiting:
		if err != nil {
			t.Errorf("the caller that waited: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the caller that waited was never answered")
	}
}

func TestASpentQuotaSaysUntilWhenAndIsRetryable(t *testing.T) {
	repo, src := newRepo(t, sample())
	repo.treeStatus = http.StatusForbidden
	reset := time.Now().Add(20 * time.Minute)
	repo.treeHeader = http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {strconv.FormatInt(reset.Unix(), 10)}}
	_, err := list(t, src, nil)
	ve := asViewError(t, err)
	if ve.Code != "gitignore.ratelimited" || !ve.Retryable || !strings.Contains(ve.Message, " until ") {
		t.Errorf("error = %+v", ve)
	}
}

func TestA403ThatIsNotAboutTheQuotaIsNotDescribedAsOne(t *testing.T) {
	repo, src := newRepo(t, sample())
	repo.treeStatus = http.StatusForbidden
	_, err := list(t, src, nil)
	if ve := asViewError(t, err); ve.Code != "gitignore.request.status" {
		t.Errorf("a plain 403 was %s", ve.Code)
	}
}

// A host that says an address has used its share is not asked again until it
// says the share is back: a caller that honours "retryable" asks once a call,
// and every one of those is a request GitHub told it not to make.
func TestAHostThatSaidTheQuotaIsSpentIsNotAskedAgainUntilItIsBack(t *testing.T) {
	repo, src := newRepo(t, sample())
	repo.treeStatus = http.StatusForbidden
	reset := time.Now().Add(20 * time.Minute)
	repo.treeHeader = http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {strconv.FormatInt(reset.Unix(), 10)}}
	for range 3 {
		_, err := list(t, src, nil)
		ve := asViewError(t, err)
		if ve.Code != "gitignore.ratelimited" || !ve.Retryable || !strings.Contains(ve.Message, " until ") {
			t.Fatalf("error = %+v", ve)
		}
	}
	if n := repo.got("tree"); n != 1 {
		t.Errorf("the host was asked %d times after it said the quota was spent, want once", n)
	}

	// Once the time it named has passed it is asked again, and answers.
	repo.treeStatus, repo.treeHeader = 0, nil
	src.spent[strings.TrimPrefix(repo.srv.URL, "http://")] = time.Now().Add(-time.Second)
	if _, err := list(t, src, nil); err != nil {
		t.Errorf("after the quota was back: %v", err)
	}
	if n := repo.got("tree"); n != 2 {
		t.Errorf("the host was asked %d times in all, want twice", n)
	}
}

func TestAQuotaIsNotBelievedForLongerThanTheShareTakesToRenew(t *testing.T) {
	repo, src := newRepo(t, sample())
	repo.treeStatus = http.StatusTooManyRequests
	far := time.Now().Add(1000 * time.Hour)
	repo.treeHeader = http.Header{"Retry-After": {strconv.Itoa(int(time.Until(far).Seconds()))}}
	if _, err := list(t, src, nil); err == nil {
		t.Fatal("a 429 was answered")
	}
	until := src.spent[strings.TrimPrefix(repo.srv.URL, "http://")]
	if until.After(time.Now().Add(maxSpent + time.Minute)) {
		t.Errorf("the host is left alone until %s, which is %s from now", until, time.Until(until))
	}
}

func TestAServerErrorIsRetryableAndAMissingFileIsNot(t *testing.T) {
	repo, src := newRepo(t, sample())
	repo.treeStatus = http.StatusServiceUnavailable
	_, err := list(t, src, nil)
	if ve := asViewError(t, err); ve.Code != "gitignore.request.status" || !ve.Retryable {
		t.Errorf("503: %+v", ve)
	}
	repo.treeStatus = 0
	repo.rawStatus["Go.gitignore"] = http.StatusNotFound
	_, err = show(t, src, "Go")
	if ve := asViewError(t, err); ve.Code != "gitignore.response.notfound" || ve.Retryable {
		t.Errorf("404: %+v", ve)
	}
}

func TestWhenSeveralFilesFailTheFirstInTheOrderAskedIsTheOneReported(t *testing.T) {
	repo, src := newRepo(t, sample())
	repo.rawStatus["Go.gitignore"] = http.StatusNotFound
	repo.rawStatus["Node.gitignore"] = http.StatusInternalServerError
	for range 20 {
		_, err := show(t, src, "Go", "Node")
		if ve := asViewError(t, err); ve.Code != "gitignore.response.notfound" {
			t.Fatalf("reported %s, want the first one's", ve.Code)
		}
	}
}

func TestAHostThatCannotBeReachedIsRetryableAndNamesTheHosts(t *testing.T) {
	repo, src := newRepo(t, sample())
	repo.srv.Close()
	_, err := list(t, src, nil)
	ve := asViewError(t, err)
	if ve.Code != "gitignore.request.failed" || !ve.Retryable || !strings.Contains(ve.Hint, "api.github.com") {
		t.Errorf("error = %+v", ve)
	}
}

func TestATruncatedOrEmptyOrBrokenListingIsRefused(t *testing.T) {
	repo, src := newRepo(t, sample())
	repo.truncated = true
	_, err := list(t, src, nil)
	if ve := asViewError(t, err); ve.Code != "gitignore.response.truncated" {
		t.Errorf("truncated: %s", ve.Code)
	}
	repo.truncated = false
	repo.files = map[string]string{}
	_, err = list(t, src, nil)
	if ve := asViewError(t, err); ve.Code != "gitignore.response.empty" {
		t.Errorf("empty: %s", ve.Code)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "<html>") }))
	defer bad.Close()
	_, err = list(t, &source{api: bad.URL, raw: bad.URL, client: bad.Client()}, nil)
	if ve := asViewError(t, err); ve.Code != "gitignore.response.invalid" {
		t.Errorf("html: %s", ve.Code)
	}
}

func TestAResponseLargerThanTheLimitIsRefused(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", maxListing+10))
	}))
	defer big.Close()
	_, err := list(t, &source{api: big.URL, raw: big.URL, client: big.Client()}, nil)
	if ve := asViewError(t, err); ve.Code != "gitignore.response.toolarge" {
		t.Errorf("code = %s", ve.Code)
	}
}

func TestOnlyARedirectWithinTheSameHostOverHTTPSIsFollowed(t *testing.T) {
	mk := func(u string) *http.Request {
		parsed, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Request{URL: parsed}
	}
	first := mk("https://api.github.com/repos/github/gitignore/git/trees/main")
	via := []*http.Request{first}
	if err := sameHostOnly(mk("https://api.github.com/repositories/1/git/trees/main"), via); err != nil {
		t.Errorf("a redirect inside the host was refused: %v", err)
	}
	for _, elsewhere := range []string{"https://example.com/x", "http://api.github.com/x", "https://api.github.com:8443/x", "https://api.github.com.evil.example/x"} {
		if err := sameHostOnly(mk(elsewhere), via); err == nil {
			t.Errorf("a redirect to %s was followed", elsewhere)
		}
	}
	// A refusal names what differs, which for a downgrade or another port is
	// not the host.
	err := sameHostOnly(mk("http://api.github.com/x"), via)
	if err == nil || !strings.Contains(err.Error(), "https://api.github.com to http://api.github.com") {
		t.Errorf("a downgrade was refused as %v", err)
	}
	err = sameHostOnly(mk("https://api.github.com:8443/x"), via)
	if err == nil || !strings.Contains(err.Error(), "https://api.github.com to https://api.github.com:8443") {
		t.Errorf("another port was refused as %v", err)
	}
}

// The count is of redirects followed: three are, and the fourth is the one
// refused, which is what the refusal says.
func TestARequestFollowsThreeRedirectsAndNotAFourth(t *testing.T) {
	var hits, redirects atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n <= redirects.Load() {
			http.Redirect(w, r, "https://"+r.Host+"/hop"+strconv.Itoa(int(n)), http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "arrived")
	}))
	defer srv.Close()
	client := *srv.Client()
	client.CheckRedirect = sameHostOnly

	redirects.Store(3)
	resp, err := client.Get(srv.URL + "/start")
	if err != nil {
		t.Fatalf("a request that is redirected three times failed: %v", err)
	}
	_ = resp.Body.Close()
	if n := hits.Load(); n != 4 {
		t.Errorf("%d requests were made, want the first and three redirects", n)
	}

	hits.Store(0)
	redirects.Store(4)
	_, err = client.Get(srv.URL + "/start")
	if err == nil || !strings.Contains(err.Error(), "stopped after 3 redirects") {
		t.Fatalf("a fourth redirect ended with %v", err)
	}
	if n := hits.Load(); n != 4 {
		t.Errorf("%d requests were made, want the first and three redirects", n)
	}
}

func TestEveryRequestIdentifiesRtaAndAsksForWhatItReads(t *testing.T) {
	var seen []http.Header
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Clone())
		mu.Unlock()
		http.NotFound(w, r)
	}))
	defer srv.Close()
	_, _ = list(t, &source{api: srv.URL, raw: srv.URL, client: srv.Client()}, nil)
	if len(seen) != 1 {
		t.Fatalf("%d requests", len(seen))
	}
	h := seen[0]
	if !strings.HasPrefix(h.Get("User-Agent"), "rta") || h.Get("Accept") != "application/vnd.github+json" || h.Get("Authorization") != "" {
		t.Errorf("headers = %v", h)
	}
}

// --- the declaration ---

func TestEveryCapabilityIsAReadThatStaysOffTheDashboardUnasked(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.Safety != plugin.Read || !c.NoPreview || !c.Idempotent {
			t.Errorf("%s: safety %v, no-preview %v, idempotent %v", c.ID, c.Safety, c.NoPreview, c.Idempotent)
		}
	}
}
