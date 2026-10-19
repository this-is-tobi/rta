package tui

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/boxlist"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Live completion on a list input: the same tab, the same rules, and the
// element being typed is what is completed. A plugin turned a selector into a
// list (a bucket, a namespace) and lost the tab that listed them; these prove
// the box of a list is asked for the element after its last comma, only on the
// press, never offers what the box already lists, and holds the answer to the
// connection's fence.

// liveListModel is liveModel's s3 shape with the bucket a list, as a plugin
// that fences it by a config key declares it. The answers are what the service
// would say whatever the partial: narrowing to it is the box's job.
func liveListModel(t *testing.T, lr *liveRecorder, profiles map[string]config.Profile,
	answers ...string) (Model, plugin.Capability) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(config.Config{Profiles: profiles}); err != nil {
		t.Fatal(err)
	}
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil }
	c := plugin.Capability{
		ID: "s3.bucket.list", Summary: "list", Safety: plugin.Read, Run: run,
		Inputs: []plugin.Field{
			{Name: "bucket", Type: plugin.StringSlice, Config: "bucket", Live: true, Help: "buckets",
				Suggest: lr.suggest(answers...)},
			{Name: "region", Type: plugin.String, Config: "region", Help: "region"},
			{Name: "namespace", Type: plugin.StringSlice, Config: "namespace", Help: "namespaces"},
			{Name: "endpoint", Type: plugin.String, Config: "endpoint", Local: true,
				Endpoint: plugin.EndpointURL, Help: "endpoint"},
			{Name: "secret-key", Type: plugin.Secret, Local: true, EnvFallback: true, Help: "secret"},
		},
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "s3", Summary: "s3",
		Capabilities: []plugin.Capability{c}}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.width, m.height = 100, 40
	return m, c
}

// openLiveList opens the form with the cursor on the list box.
func openLiveList(t *testing.T, m Model, c plugin.Capability) Model {
	t.Helper()
	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	nm.form.form = startedForm(nm.form)
	return pastThePicker(t, nm)
}

// fetchLive is fetchFromCluster for a press whose answer the box has to draw:
// the credential the listing needs is typed first, as a person would have, and
// the command applying the answer returns is run, which is how the form comes
// to reload the suggestions it was told have changed (huh runs the suggestion
// function in a command, and a press that drops it leaves the old ghosts).
func fetchLive(t *testing.T, m Model) Model {
	t.Helper()
	*m.form.bindings["secret-key"] = "sk-999"
	saved := completeTimeout
	completeTimeout = 30 * time.Second
	t.Cleanup(func() { completeTimeout = saved })
	next, cmd := m.Update(tabKey)
	nm := next.(Model)
	if nm.flash != "completing…" {
		t.Fatalf("tab did not start a fetch (flash %q)", nm.flash)
	}
	msg, ok := cmd().(completeMsg)
	if !ok {
		t.Fatal("the fetch did not produce a completeMsg")
	}
	next, follow := nm.Update(msg)
	return landed(next.(Model), follow, 3)
}

// landed runs the commands a message asked for and feeds their answers back,
// a few rounds deep, the way a program does. A clock is left alone (resolveCmd).
func landed(m Model, cmd tea.Cmd, rounds int) Model {
	if cmd == nil || rounds == 0 {
		return m
	}
	msg, ok := resolveCmd(cmd)
	if !ok || msg == nil {
		return m
	}
	msgs := []tea.Msg{msg}
	if batch, isBatch := msg.(tea.BatchMsg); isBatch {
		msgs = nil
		for _, sub := range batch {
			if r, ok := resolveCmd(sub); ok && r != nil {
				msgs = append(msgs, r)
			}
		}
	}
	for _, r := range msgs {
		next, follow := m.Update(r)
		m = landed(next.(Model), follow, rounds-1)
	}
	return m
}

// offeredIn is what the box would draw as ghosts right now, as whole-box texts.
func offeredIn(nm Model, name string) []string { return nm.form.offers[name]() }

// The element being typed is what a press asks about, and what the person
// typed ahead of it stays where it is.
//
// One flow because it is one rhythm: typing calls nothing, the press fetches
// with the element after the last comma under the field's own name, the answer
// leaves out what the box lists, and a tab with a matching candidate on offer
// replaces the element without a second call.
func TestTabOnALiveListAsksForTheElementBeingTyped(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveListModel(t, lr, nil, "backups\tprod data", "media", "logs", "metrics")
	nm := openLiveList(t, m, c)
	bucket := c.Inputs[0]

	nm.form.form = typeInto(nm.form.form, "backups,me")
	if got := nm.form.candidates(bucket); len(got) != 0 {
		t.Errorf("the keystroke channel offers %v before any press, want nothing", got)
	}
	if lr.calls() != 0 {
		t.Fatalf("typing called the live Suggest %d times", lr.calls())
	}

	nm = fetchLive(t, nm)
	if lr.calls() != 1 {
		t.Fatalf("tab called the live Suggest %d times, want once", lr.calls())
	}
	req := lr.last(t)
	if got := req.String("bucket"); got != "me" {
		t.Errorf("the request carries %q under the field, want the element being typed", got)
	}
	if got := req.StringSlice("bucket"); !slices.Equal(got, []string{"me"}) {
		t.Errorf("the request reads as the list %q, want just the element being typed", got)
	}
	if got := req.String("secret-key"); got != "sk-999" {
		t.Errorf("the listing ran with secret-key %q, want the typed credential", got)
	}
	if req.Surface() != plugin.SurfaceCompletion {
		t.Errorf("surface = %v, want SurfaceCompletion", req.Surface())
	}
	// What the box lists is not a candidate: backups is chosen already.
	if got, want := nm.form.suggested["bucket"], []string{"media", "logs", "metrics"}; !slices.Equal(got, want) {
		t.Errorf("suggested = %v, want %v", got, want)
	}
	if got, want := offeredIn(nm, "bucket"), []string{"backups,media", "backups,metrics"}; !slices.Equal(got, want) {
		t.Errorf("the box offers %q, want the answers that continue the element, as whole boxes", got)
	}
	if !strings.Contains(nm.flash, "3 bucket completions") {
		t.Errorf("flash = %q, want the count of candidates the box can still take", nm.flash)
	}

	// A candidate on offer: tab takes it, in place of the element and behind
	// the head, and costs no second call.
	next, _ := nm.Update(tabKey)
	nm = next.(Model)
	if got := *nm.form.bindings["bucket"]; got != "backups,media" {
		t.Errorf("tab with a candidate on offer left the box at %q, want backups,media", got)
	}
	if lr.calls() != 1 {
		t.Errorf("the accept called the service (%d calls), want the widget alone", lr.calls())
	}
}

// An empty box asks with an empty partial and lists the names, and the element
// after a comma asks with an empty one too — with the elements ahead of it out
// of the answer.
func TestALiveListIsAskedForAnEmptyElement(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveListModel(t, lr, nil, "backups", "media", "logs")
	nm := openLiveList(t, m, c)

	nm = fetchLive(t, nm)
	if got := lr.last(t).String("bucket"); got != "" {
		t.Errorf("partial = %q on an empty box, want empty", got)
	}
	if got := lr.last(t).StringSlice("bucket"); got != nil {
		t.Errorf("an empty partial reads as the list %q, want none", got)
	}
	if !strings.Contains(nm.flash, "backups, media, logs") {
		t.Errorf("flash = %q, want the names themselves on an empty box", nm.flash)
	}

	nm.form.form = typeInto(nm.form.form, "backups, ")
	// The earlier fetch left media and logs, which continue the new element, so
	// this tab takes one rather than asking again.
	if got, want := offeredIn(nm, "bucket"), []string{"backups, media", "backups, logs"}; !slices.Equal(got, want) {
		t.Fatalf("the box offers %q, want the rest after backups", got)
	}
	next, _ := nm.Update(tabKey)
	nm = next.(Model)
	if got := *nm.form.bindings["bucket"]; got != "backups, media" {
		t.Errorf("tab left the box at %q, want the first of the rest", got)
	}
	if lr.calls() != 1 {
		t.Errorf("%d calls, want the one the empty box made", lr.calls())
	}

	// logs is still on offer for the next element, so this takes it as well...
	nm.form.form = typeInto(nm.form.form, ", ")
	next, _ = nm.Update(tabKey)
	nm = next.(Model)
	if got := *nm.form.bindings["bucket"]; got != "backups, media, logs" {
		t.Fatalf("tab left the box at %q, want the last of the rest", got)
	}
	// ... and with every bucket listed there is nothing on offer, so the next
	// element asks again, with an empty partial, and is told there is nothing.
	nm.form.form = typeInto(nm.form.form, ", ")
	nm = fetchLive(t, nm)
	if lr.calls() != 2 || lr.last(t).String("bucket") != "" {
		t.Errorf("the next element was not asked for with an empty partial (%d calls)", lr.calls())
	}
	if nm.flash != "no bucket completions" {
		t.Errorf("flash = %q, want the answer that every bucket is chosen already", nm.flash)
	}
}

// A person walks a list a name at a time, and the arrows walk what a press
// fetched the way they walk any list box: from where an element begins, over
// the ones the box does not list.
func TestTheArrowsWalkWhatALivePressFetched(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveListModel(t, lr, nil, "backups", "media", "logs")
	nm := openLiveList(t, m, c)
	nm.form.form = typeInto(nm.form.form, "backups, ")
	nm = fetchLive(t, nm)

	nm = pressKey(t, nm, downKey)
	if got := *nm.form.bindings["bucket"]; got != "backups, media" {
		t.Fatalf("down placed %q, want the first candidate after the head", got)
	}
	nm = pressKey(t, nm, downKey)
	if got := *nm.form.bindings["bucket"]; got != "backups, logs" {
		t.Errorf("a second down placed %q, want the neighbour", got)
	}
	if lr.calls() != 1 {
		t.Errorf("walking the candidates called the service (%d calls)", lr.calls())
	}
}

// The elements a box lists are the ones it reads, not the ones it splits at
// every comma: a name holding a comma that was chosen as one element is left out
// of the answer whole, and not offered again as a second pair of names.
func TestALiveAnswerLeavesOutAListedElementHoldingAComma(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveListModel(t, lr, nil, "a,b", "c")
	nm := openLiveList(t, m, c)
	nm.form.form = typeInto(nm.form.form, `"a,b", `)
	nm = fetchLive(t, nm)

	if got := lr.last(t).String("bucket"); got != "" {
		t.Errorf("partial = %q, want the empty element after the comma", got)
	}
	if got, want := nm.form.suggested["bucket"], []string{"c"}; !slices.Equal(got, want) {
		t.Errorf("suggested = %q, want the answers without the listed a,b", got)
	}
	if got, want := offeredIn(nm, "bucket"), []string{`"a,b", c`}; !slices.Equal(got, want) {
		t.Errorf("the box offers %q, want the rest as a whole box", got)
	}
}

// The element being typed is not yet chosen: a name that is exactly what is
// typed stays on offer, so a tab on a complete name still has it to take, while
// the one ahead of it is out.
func TestALiveAnswerKeepsTheElementBeingTyped(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveListModel(t, lr, nil, "backups", "media", "metrics")
	nm := openLiveList(t, m, c)
	nm.form.form = typeInto(nm.form.form, "backups,media")
	nm = fetchLive(t, nm)

	if got, want := nm.form.suggested["bucket"], []string{"media", "metrics"}; !slices.Equal(got, want) {
		t.Errorf("suggested = %q, want the element being typed among the answers and backups out", got)
	}
	if got, want := offeredIn(nm, "bucket"), []string{"backups,media"}; !slices.Equal(got, want) {
		t.Errorf("the box offers %q, want the name typed out whole", got)
	}
}

// The box is read as it stands, not trimmed: the space ahead of the first
// element is what the whole boxes on offer begin with, so a press on it takes
// the name on offer where a trimmed box would match nothing, fetch again, and
// leave the cursor where it was.
func TestALiveListBoxIsReadWithTheSpaceItWasTypedWith(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveListModel(t, lr, nil, "backups", "media")
	nm := openLiveList(t, m, c)
	nm.form.form = typeInto(nm.form.form, " ba")
	nm = fetchLive(t, nm)

	if got := lr.last(t).String("bucket"); got != "ba" {
		t.Errorf("partial = %q, want the element without the space ahead of it", got)
	}
	if got, want := offeredIn(nm, "bucket"), []string{" backups"}; !slices.Equal(got, want) {
		t.Fatalf("the box offers %q, want the name as the whole box it makes", got)
	}
	next, _ := nm.Update(tabKey)
	nm = next.(Model)
	if got := *nm.form.bindings["bucket"]; got != " backups" {
		t.Errorf("tab left the box at %q, want the name on offer taken", got)
	}
	if lr.calls() != 1 {
		t.Errorf("the accept called the service (%d calls), want the widget alone", lr.calls())
	}
}

// A name holding a comma goes in as an element the box reads back as itself.
func TestALiveCandidateHoldingACommaIsWrittenAsAnElement(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveListModel(t, lr, nil, "a,b")
	nm := openLiveList(t, m, c)
	nm.form.form = typeInto(nm.form.form, "x,")
	nm = fetchLive(t, nm)

	next, _ := nm.Update(tabKey)
	nm = next.(Model)
	got, err := boxlist.Split(*nm.form.bindings["bucket"])
	if err != nil || !slices.Equal(got, []string{"x", "a,b"}) {
		t.Errorf("the box %q reads as %q (%v), want the two elements", *nm.form.bindings["bucket"], got, err)
	}
}

// A quote that opened the element is answered with one, and the partial is the
// element as it reads, quote removed.
func TestALiveListElementOpenedWithAQuoteIsAskedForWithoutIt(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveListModel(t, lr, nil, "my bucket", "other")
	nm := openLiveList(t, m, c)
	nm.form.form = typeInto(nm.form.form, `x,"my b`)
	nm = fetchLive(t, nm)

	if got := lr.last(t).String("bucket"); got != "my b" {
		t.Errorf("partial = %q, want the element as it reads, without the opening quote", got)
	}
	next, _ := nm.Update(tabKey)
	nm = next.(Model)
	if got := *nm.form.bindings["bucket"]; got != `x,"my bucket"` {
		t.Errorf("tab left the box at %q, want the candidate in quotes", got)
	}
}

// emptyBox empties a box the form opened holding a default. A fence fills a list
// whole, so the box of a fenced list opens holding the allowlist, and these cases
// are about the box somebody typed into.
func emptyBox(cf *capForm, name string) {
	cf.syncs[name].Set("")
	cf.inputs[name].Accessor(cf.syncs[name])
}

// stagingFencing is a staging environment whose s3 connection reaches three of
// the buckets, which is what a fenced list is held to.
func stagingFencing() map[string]config.Profile {
	return map[string]config.Profile{"staging": {Plugins: map[string]config.Connection{
		"s3": {Only: map[string]any{"bucket": []any{"backups", "logs", "audit"}}},
	}}}
}

// The fence narrows a list the way it narrows a String: the allowlist is what
// the keystroke channel offers, and the service's answer is held to it, so a
// press never offers a value the call would be refused for.
func TestAFencedLiveListOffersOnlyWhatTheFenceHolds(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveListModel(t, lr, stagingFencing(), "backups", "media", "logs", "metrics")
	switchOn(t, &m, "staging")
	nm := openLiveList(t, m, c)
	emptyBox(nm.form, "bucket")
	bucket := c.Inputs[0]

	if got := strings.Join(nm.form.candidates(bucket), ","); got != "backups,logs,audit" {
		t.Errorf("a fenced list offers %q per keystroke, want the allowlist", got)
	}
	if lr.calls() != 0 {
		t.Fatalf("the keystroke channel called the live Suggest (%d calls)", lr.calls())
	}

	nm = fetchLive(t, nm)
	if got, want := nm.form.suggested["bucket"], []string{"backups", "logs"}; !slices.Equal(got, want) {
		t.Errorf("suggested = %v, want the answers the fence holds", got)
	}

	// With the first element chosen, the answer is the fence's and the box's
	// together: what the service said, held to the fence and without backups,
	// beside the rest of the allowlist.
	again, c2 := liveListModel(t, lr, stagingFencing(), "backups", "media", "logs", "metrics")
	switchOn(t, &again, "staging")
	nm = openLiveList(t, again, c2)
	emptyBox(nm.form, "bucket")
	nm.form.form = typeInto(nm.form.form, "backups,")
	nm = fetchLive(t, nm)
	if got, want := nm.form.suggested["bucket"], []string{"logs"}; !slices.Equal(got, want) {
		t.Errorf("suggested = %v, want what the fence holds and the box does not list", got)
	}
	if got, want := offeredIn(nm, "bucket"), []string{"backups,logs", "backups,audit"}; !slices.Equal(got, want) {
		t.Errorf("the box offers %q, want the rest of the allowlist beside the fetched answer", got)
	}
}

// One press has one fence. The request a press makes and the answer it is held to
// both come from the connection the press resolved, the one whose credentials the
// listing runs with and whose allowlist the run will be gated by, and not from the
// copy the form took when it was built: a config edited under an open form moves
// them together, never one without the other.
func TestAPressTakesTheFenceOfTheRequestAndOfTheAnswerFromOneConnection(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	opened := map[string]config.Profile{"staging": {Plugins: map[string]config.Connection{
		"s3": {Only: map[string]any{
			"bucket": []any{"backups", "logs"}, "region": []any{"eu-west-1"},
		}},
	}}}
	m, c := liveListModel(t, lr, opened, "backups", "media", "logs")
	switchOn(t, &m, "staging")
	nm := openLiveList(t, m, c)
	emptyBox(nm.form, "bucket")
	if got := strings.Join(nm.form.candidates(c.Inputs[0]), ","); got != "backups,logs" {
		t.Fatalf("the form opened on the fence %q, want backups,logs", got)
	}

	if err := config.Write(config.Config{Profiles: map[string]config.Profile{"staging": {
		Plugins: map[string]config.Connection{"s3": {Only: map[string]any{
			"bucket": []any{"media", "audit"}, "region": []any{"us-east-1"},
		}}},
	}}}); err != nil {
		t.Fatal(err)
	}

	nm = fetchLive(t, nm)
	if got := lr.last(t).String("region"); got != "us-east-1" {
		t.Errorf("the request carries region %q, want the one the fence the press resolved gives", got)
	}
	if got, want := nm.form.suggested["bucket"], []string{"media"}; !slices.Equal(got, want) {
		t.Errorf("suggested = %v, want the answers the same fence holds", got)
	}
}

// What a run would fill from a fence is in the request a press makes, so a
// listing that depends on a sibling sees the sibling the call will have: the
// one value a fence of one entry gives, and the whole list a fence gives a list
// that the box leaves blank.
func TestALiveRequestCarriesTheSiblingsAFenceFills(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveListModel(t, lr, map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{
			"s3": {Only: map[string]any{
				"region": []any{"eu-west-1"}, "namespace": []any{"shop", "jobs"},
			}},
		}},
	}, "backups")
	nm := openLiveList(t, m, c)
	*nm.form.bindings[profileInput] = "staging"

	nm = fetchLive(t, nm)
	req := lr.last(t)
	if got := req.String("region"); got != "eu-west-1" {
		t.Errorf("the request carries region %q, want the one the fence gives the call", got)
	}
	if got := req.StringSlice("namespace"); !slices.Equal(got, []string{"shop", "jobs"}) {
		t.Errorf("the request carries namespace %q, want the whole list the fence gives the call", got)
	}
}

// Typing is never a press, however long the list: a box that took a dozen
// elements made no call, and the one press made one.
func TestTypingIntoALiveListNeverCallsTheService(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveListModel(t, lr, nil, "backups", "media")
	nm := openLiveList(t, m, c)
	nm.form.form = typeInto(nm.form.form, "a,b,c,d,e,f,g,h,i,j,k,l,m")
	nm.form.candidates(c.Inputs[0])
	if lr.calls() != 0 {
		t.Fatalf("typing into a live list called the Suggest %d times", lr.calls())
	}
	nm = fetchLive(t, nm)
	if lr.calls() != 1 {
		t.Errorf("one press made %d calls", lr.calls())
	}
}

// A press that brought back nothing the box can still take is the end of
// completion for that text: the next tab moves on, as it does from any box, and
// the service is not asked again about what it was just asked.
func TestTabMovesOnFromALiveListWhenThePressFoundNothing(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveListModel(t, lr, nil, "backups")
	nm := openLiveList(t, m, c)
	nm.form.form = typeInto(nm.form.form, "backups,")
	nm = fetchLive(t, nm) // the only bucket is listed already: nothing to take
	if got := nm.form.suggested["bucket"]; len(got) != 0 {
		t.Fatalf("suggested = %v, want nothing", got)
	}

	before := nm.form.form.GetFocusedField()
	next, cmd := nm.Update(tabKey)
	nm = next.(Model)
	if cmd == nil {
		t.Fatal("tab did nothing on a list the press found nothing for")
	}
	next, _ = nm.Update(cmd())
	nm = next.(Model)
	if nm.form.form.GetFocusedField() == before {
		t.Error("the cursor is stuck on a list its completer has nothing more to say about")
	}
	if lr.calls() != 1 {
		t.Errorf("the service was asked %d times, want once", lr.calls())
	}
}

// An entry ending in a separator composes inside an element as it does in a
// String box: accepting it leaves nothing that extends the element, so the next
// press asks one level deeper, with the element so far as the partial.
func TestALiveListElementComposesAtASeparator(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveListModel(t, lr, nil)
	deeper := func(_ context.Context, req plugin.Request) []string {
		lr.mu.Lock()
		lr.reqs = append(lr.reqs, req)
		lr.mu.Unlock()
		if req.String("bucket") == "logs/" {
			return []string{"logs/2024/", "logs/2025/"}
		}
		return []string{"logs/", "media"}
	}
	c.Inputs[0].Suggest = deeper
	nm := openLiveList(t, m, c)
	nm.form.form = typeInto(nm.form.form, "x,lo")
	nm = fetchLive(t, nm)

	next, _ := nm.Update(tabKey)
	nm = next.(Model)
	if got := *nm.form.bindings["bucket"]; got != "x,logs/" {
		t.Fatalf("tab left the box at %q, want the first segment accepted", got)
	}
	nm = fetchLive(t, nm)
	if got := lr.last(t).String("bucket"); got != "logs/" {
		t.Errorf("the deeper press asked about %q, want the element so far", got)
	}
	if got, want := offeredIn(nm, "bucket"), []string{"x,logs/2024/", "x,logs/2025/"}; !slices.Equal(got, want) {
		t.Errorf("the box offers %q, want the next level", got)
	}
}
