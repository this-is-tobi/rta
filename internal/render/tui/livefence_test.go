package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A live press reads the service with what the form holds in its other boxes,
// and the connection's fence is about what a call asks of the service: so a press
// is held to it the way the run is, before it reads anything.

// liveFenceModel is the cnpg shape: a live box that lists what exists in the
// namespaces a sibling box names, under a profile that reaches some of them. The
// live box comes first, so the form opens with the cursor on it, and a switch
// that widens the namespace sits beside the siblings for the case that reads past
// it without ever naming one. The live box is the namespace box itself in the
// cases about a fenced box being completed, which then has no sibling.
func liveFenceModel(t *testing.T, only map[string]any, live plugin.Field, siblings ...plugin.Field) (Model, plugin.Capability) {
	t.Helper()
	return liveFenceModelOn(t, config.Connection{Only: only}, live, siblings...)
}

// liveFenceModelOn is liveFenceModel for a connection that states more than its
// fence.
func liveFenceModelOn(t *testing.T, conn config.Connection, live plugin.Field, siblings ...plugin.Field) (Model, plugin.Capability) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	profiles := map[string]config.Profile{"staging": {Plugins: map[string]config.Connection{
		"cnpg": conn,
	}}}
	if err := config.Write(config.Config{Profiles: profiles}); err != nil {
		t.Fatal(err)
	}
	inputs := append([]plugin.Field{live}, siblings...)
	inputs = append(inputs,
		plugin.Field{Name: "all-namespaces", Type: plugin.Bool, Help: "every namespace", Widens: "namespace"},
		plugin.Field{Name: "secret-key", Type: plugin.Secret, Local: true, EnvFallback: true, Help: "secret"})
	c := plugin.Capability{
		ID: "cnpg.cluster.list", Summary: "list", Safety: plugin.Read,
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
		Inputs: inputs,
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "cnpg", Summary: "cnpg",
		Capabilities: []plugin.Capability{c}}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.width, m.height = 100, 40
	return m, c
}

func namespaceList() plugin.Field {
	return plugin.Field{Name: "namespace", Type: plugin.StringSlice, Config: "namespace", Help: "namespaces"}
}

func namespaceOne() plugin.Field {
	return plugin.Field{Name: "namespace", Type: plugin.String, Config: "namespace", Help: "namespace"}
}

func clusterList(lr *liveRecorder, answers ...string) plugin.Field {
	return plugin.Field{Name: "cluster", Type: plugin.StringSlice, Config: "cluster", Live: true,
		Help: "clusters", Suggest: lr.suggest(answers...)}
}

func clusterOne(lr *liveRecorder, answers ...string) plugin.Field {
	return plugin.Field{Name: "cluster", Type: plugin.String, Config: "cluster", Live: true,
		Help: "cluster", Suggest: lr.suggest(answers...)}
}

// setBox types text into a box the way a person who then left it would have: the
// widget and the value the form reads both hold it.
func setBox(cf *capForm, name, text string) {
	cf.syncs[name].Set(text)
	cf.inputs[name].Accessor(cf.syncs[name])
	cf.engaged[name] = true
}

// openFenced opens the form under the staging profile with the cursor on the live
// box and the namespace box holding exactly what the case gives it.
func openFenced(t *testing.T, m Model, c plugin.Capability, namespaces string) Model {
	t.Helper()
	switchOn(t, &m, "staging")
	nm := openLiveList(t, m, c)
	setBox(nm.form, "namespace", namespaces)
	return nm
}

// heldPress is one tab on the focused box with the credential typed, and whether
// it started a read. The command it returns is run, because that is what asks
// the Suggest: a press that refuses must have none to run, and the test counts the
// calls to say so.
func heldPress(t *testing.T, m Model) (Model, bool) {
	t.Helper()
	*m.form.bindings["secret-key"] = "sk-999"
	next, cmd := m.Update(tabKey)
	if cmd != nil {
		cmd()
	}
	return next.(Model), cmd != nil
}

// A namespace outside the fence holds the press: nothing is read, and the flash
// line says which input is outside — without listing what the fence does reach,
// and without repeating what was typed.
func TestAPressWithASiblingOutsideTheFenceReadsNothing(t *testing.T) {
	noHistory(t)
	for name, tc := range map[string]struct {
		live      func(*liveRecorder, ...string) plugin.Field
		namespace func() plugin.Field
	}{
		"a list live box":    {clusterList, namespaceList},
		"a single live box":  {clusterOne, namespaceList},
		"a single namespace": {clusterList, namespaceOne},
	} {
		t.Run(name, func(t *testing.T) {
			lr := &liveRecorder{}
			m, c := liveFenceModel(t, map[string]any{"namespace": []any{"shop", "jobs"}},
				tc.live(lr, "orders-db", "billing-db"), tc.namespace())
			nm := openFenced(t, m, c, "payroll")

			nm, started := heldPress(t, nm)
			if started || lr.calls() != 0 {
				t.Fatalf("a namespace outside the fence still started a read (command %v, %d calls)", started, lr.calls())
			}
			for _, want := range []string{"namespace", "staging", "outside"} {
				if !strings.Contains(nm.flash, want) {
					t.Errorf("flash = %q, want it to name %q", nm.flash, want)
				}
			}
			for _, leaked := range []string{"shop", "jobs", "payroll"} {
				if strings.Contains(nm.flash, leaked) {
					t.Errorf("flash = %q names %q: the line says which input, not what the fence holds", nm.flash, leaked)
				}
			}
			if nm.flash != nm.flashBad {
				t.Errorf("flash %q is not drawn as a refusal", nm.flash)
			}
			if got := nm.form.suggested["cluster"]; len(got) != 0 {
				t.Errorf("suggested = %v after a held press, want nothing", got)
			}
		})
	}
}

// A namespace the fence holds is a press like any other: the same request, the
// same answer. A String live box is no different from a list.
func TestAPressWithASiblingInsideTheFenceStillFetches(t *testing.T) {
	noHistory(t)
	for name, live := range map[string]func(*liveRecorder, ...string) plugin.Field{
		"a list live box":   clusterList,
		"a single live box": clusterOne,
	} {
		t.Run(name, func(t *testing.T) {
			lr := &liveRecorder{}
			m, c := liveFenceModel(t, map[string]any{"namespace": []any{"shop", "jobs"}},
				live(lr, "orders-db", "billing-db"), namespaceList())
			nm := openFenced(t, m, c, "shop, jobs")

			nm = fetchLive(t, nm)
			if lr.calls() != 1 {
				t.Fatalf("the press made %d calls, want one", lr.calls())
			}
			if got := lr.last(t).StringSlice("namespace"); !slices.Equal(got, []string{"shop", "jobs"}) {
				t.Errorf("the request carries namespace %q, want the box", got)
			}
			if got, want := nm.form.suggested["cluster"], []string{"orders-db", "billing-db"}; !slices.Equal(got, want) {
				t.Errorf("suggested = %v, want %v", got, want)
			}
		})
	}
}

// A fenced input left blank resolves to nothing the fence holds, which a run
// refuses as the widest reach there is, so the press that would read with it is
// held too — and says so as a blank.
func TestAPressWithABlankFencedSiblingReadsNothing(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveFenceModel(t, map[string]any{"namespace": []any{"shop", "jobs"}},
		clusterList(lr, "orders-db"), namespaceOne())
	nm := openFenced(t, m, c, "")

	nm, started := heldPress(t, nm)
	if started || lr.calls() != 0 {
		t.Fatalf("a blank fenced namespace still started a read (command %v, %d calls)", started, lr.calls())
	}
	if !strings.Contains(nm.flash, "namespace") || !strings.Contains(nm.flash, "blank") {
		t.Errorf("flash = %q, want the blank namespace named", nm.flash)
	}
	if strings.Contains(nm.flash, "shop") || strings.Contains(nm.flash, "jobs") {
		t.Errorf("flash = %q lists what the fence reaches", nm.flash)
	}
}

// A switch that reads past a fenced input is outside the fence whatever the input
// says, for a press as for a run.
func TestAPressWithAWideningSwitchReadsNothing(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveFenceModel(t, map[string]any{"namespace": []any{"shop", "jobs"}},
		clusterList(lr, "orders-db"), namespaceList())
	nm := openFenced(t, m, c, "shop")
	*nm.form.bools["all-namespaces"] = true

	nm, started := heldPress(t, nm)
	if started || lr.calls() != 0 {
		t.Fatalf("a widening switch still started a read (command %v, %d calls)", started, lr.calls())
	}
	if !strings.Contains(nm.flash, "all-namespaces") || !strings.Contains(nm.flash, "namespace") {
		t.Errorf("flash = %q, want the switch and the input it reads past", nm.flash)
	}
	if strings.Contains(nm.flash, "shop") || strings.Contains(nm.flash, "jobs") {
		t.Errorf("flash = %q lists what the fence reaches", nm.flash)
	}
}

// A connection that fences another input than the ones the press is held to
// holds nothing of it: the namespace typed is nobody's to refuse.
func TestAPressUnderNoFenceIsNotHeld(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveFenceModel(t, map[string]any{"cluster": []any{"orders-db"}},
		clusterList(lr, "orders-db"), namespaceList())
	nm := openFenced(t, m, c, "payroll")

	fetchLive(t, nm)
	if lr.calls() != 1 {
		t.Errorf("a connection that fences another input held the press (%d calls)", lr.calls())
	}
}

// The live box can be the fenced input itself: the namespace box, which lists the
// namespaces and is held to the fence on a press as the box of any other input is.

func namespaceListLive(lr *liveRecorder, answers ...string) plugin.Field {
	return plugin.Field{Name: "namespace", Type: plugin.StringSlice, Config: "namespace", Live: true,
		Help: "namespaces", Suggest: lr.suggest(answers...)}
}

func namespaceOneLive(lr *liveRecorder, answers ...string) plugin.Field {
	return plugin.Field{Name: "namespace", Type: plugin.String, Config: "namespace", Live: true,
		Help: "namespace", Suggest: lr.suggest(answers...)}
}

// shopFence is a profile that reaches three namespaces.
var shopFence = map[string]any{"namespace": []any{"shop", "jobs", "audit"}}

// openBox opens the form under the staging profile on the live box holding text.
func openBox(t *testing.T, m Model, c plugin.Capability, text string) Model {
	t.Helper()
	switchOn(t, &m, "staging")
	nm := openLiveList(t, m, c)
	setBox(nm.form, "namespace", text)
	return nm
}

// A press that is refused has read nothing, the credential included: the
// connection maps its secret onto a `kube:` Secret, and resolving it before the
// check made a keypress that then did nothing an access in the cluster's audit log. The poison kubectl is the assertion, and the press inside the fence is the
// proof that this fixture would have run it.
func TestAHeldPressResolvesNoCredential(t *testing.T) {
	noHistory(t)
	conn := config.Connection{
		Only:        map[string]any{"namespace": []any{"shop", "jobs"}},
		SecretsFrom: "homelab/databases",
		Secrets:     map[string]string{"secret-key": "kube:cnpg-creds/secret-key"},
	}
	pressed := func(t *testing.T, namespace string) (kubectlRan bool) {
		t.Helper()
		ran := poisonKubectl(t)
		lr := &liveRecorder{}
		m, c := liveFenceModelOn(t, conn, clusterList(lr, "orders-db"), namespaceOne())
		nm := openLiveList(t, m, c)
		setBox(nm.form, "namespace", namespace)
		*nm.form.bindings[profileInput] = "staging"

		nm.Update(tabKey)
		if lr.calls() != 0 {
			t.Fatalf("the service was asked %d times, with the credential unresolved", lr.calls())
		}
		_, err := os.Stat(ran)
		return err == nil
	}

	if !pressed(t, "shop") {
		t.Fatal("a press inside the fence did not resolve the credential: this fixture proves nothing")
	}
	if pressed(t, "payroll") {
		t.Error("a press outside the fence read the cluster Secret before it was refused")
	}
}

// A switch that reads past the box being completed holds the press as it holds a
// run of the same values: what the box holds is the start of a value and is left
// out of the check, and a switch that widens it is not. A fence on the box alone
// is the case, since taking its entry out of the connection took the switches
// with it.
func TestAPressOnAFencedBoxIsHeldByAWideningSwitch(t *testing.T) {
	noHistory(t)
	for name, live := range map[string]func(*liveRecorder, ...string) plugin.Field{
		"a list live box":   namespaceListLive,
		"a single live box": namespaceOneLive,
	} {
		t.Run(name, func(t *testing.T) {
			lr := &liveRecorder{}
			m, c := liveFenceModel(t, shopFence, live(lr, "shop", "jobs"))
			nm := openBox(t, m, c, "sh")
			*nm.form.bools["all-namespaces"] = true

			nm, started := heldPress(t, nm)
			if started || lr.calls() != 0 {
				t.Fatalf("a widening switch still started a read (command %v, %d calls)", started, lr.calls())
			}
			for _, want := range []string{"all-namespaces", "namespace", "staging"} {
				if !strings.Contains(nm.flash, want) {
					t.Errorf("flash = %q, want it to name %q", nm.flash, want)
				}
			}
			for _, leaked := range []string{"shop", "jobs", "audit"} {
				if strings.Contains(nm.flash, leaked) {
					t.Errorf("flash = %q names %q: the line says which input, not what the fence holds", nm.flash, leaked)
				}
			}
			if !flashedAsRefusal(nm) {
				t.Errorf("flash %q is not drawn as a refusal", nm.flash)
			}
		})
	}
}

// What the box being completed holds is handed to the plugin, and a Suggest may
// read at it (a bucket's keys under the prefix typed), so a fenced box is held when
// its text is not the start of anything the fence allows: the filter on the answer
// cannot unread the listing. Text that continues an entry, and text equal to one,
// is a press like any other; so is an empty box. A list box is held by the element
// being typed and not by the ones before it, which the plugin is never handed.
func TestAPressOnAFencedBoxIsHeldWhenItsTextContinuesNoEntry(t *testing.T) {
	noHistory(t)
	for name, tc := range map[string]struct {
		live func(*liveRecorder, ...string) plugin.Field
		text string
		// held: the press reads nothing. Otherwise the request carries partial.
		held    bool
		partial string
	}{
		"a string that continues an entry":     {namespaceOneLive, "sh", false, "sh"},
		"a string equal to an entry":           {namespaceOneLive, "shop", false, "shop"},
		"an empty string":                      {namespaceOneLive, "", false, ""},
		"a string outside":                     {namespaceOneLive, "payroll", true, ""},
		"a string that runs past an entry":     {namespaceOneLive, "shopping", true, ""},
		"a string in another case":             {namespaceOneLive, "SH", true, ""},
		"a list element that continues one":    {namespaceListLive, "shop, au", false, "au"},
		"a list element equal to an entry":     {namespaceListLive, "shop, jobs", false, "jobs"},
		"an empty list element":                {namespaceListLive, "shop, ", false, ""},
		"a list element outside":               {namespaceListLive, "shop, payroll", true, ""},
		"an earlier element outside":           {namespaceListLive, "payroll, jo", false, "jo"},
		"a list holding one element outside":   {namespaceListLive, "payroll", true, ""},
		"a list element in another case":       {namespaceListLive, "shop, AU", true, ""},
		"an empty list":                        {namespaceListLive, "", false, ""},
		"a list element that runs past one":    {namespaceListLive, "shop, auditing", true, ""},
		"a string that is the end of an entry": {namespaceOneLive, "op", true, ""},
	} {
		t.Run(name, func(t *testing.T) {
			lr := &liveRecorder{}
			m, c := liveFenceModel(t, shopFence, tc.live(lr, "shop", "jobs", "payroll"))
			nm := openBox(t, m, c, tc.text)

			nm, started := heldPress(t, nm)
			if !tc.held {
				if !started || lr.calls() != 1 {
					t.Fatalf("a text that can still become an entry was held (command %v, %d calls)", started, lr.calls())
				}
				if got := lr.last(t).String("namespace"); got != tc.partial {
					t.Errorf("the request carries %q, want %q", got, tc.partial)
				}
				return
			}
			if started || lr.calls() != 0 {
				t.Fatalf("a text outside the fence still started a read (command %v, %d calls)", started, lr.calls())
			}
			for _, want := range []string{"namespace", "staging", "outside", "tab moves on"} {
				if !strings.Contains(nm.flash, want) {
					t.Errorf("flash = %q, want it to name %q", nm.flash, want)
				}
			}
			for _, leaked := range []string{"shop", "jobs", "audit", "payroll", "SH", "AU", "auditing"} {
				if strings.Contains(nm.flash, leaked) {
					t.Errorf("flash = %q names %q: the line says which input, not what the fence holds or what was typed", nm.flash, leaked)
				}
			}
			if !flashedAsRefusal(nm) {
				t.Errorf("flash %q is not drawn as a refusal", nm.flash)
			}
			if !nm.form.settled("namespace", tc.text) {
				t.Error("a held press did not settle the box, so the next tab would ask again instead of moving on")
			}
		})
	}
}

// A box the profile does not fence is never held by its text, whatever the fence
// holds for the other inputs: there is nothing the text could fail to continue.
func TestAPressOnAnUnfencedBoxIsNotHeldByItsText(t *testing.T) {
	noHistory(t)
	for name, live := range map[string]func(*liveRecorder, ...string) plugin.Field{
		"a list live box":   namespaceListLive,
		"a single live box": namespaceOneLive,
	} {
		t.Run(name, func(t *testing.T) {
			lr := &liveRecorder{}
			m, c := liveFenceModel(t, map[string]any{"cluster": []any{"orders-db"}}, live(lr, "shop"),
				plugin.Field{Name: "cluster", Type: plugin.String, Config: "cluster", Help: "cluster"})
			nm := openBox(t, m, c, "payroll")
			setBox(nm.form, "cluster", "orders-db")

			if _, started := heldPress(t, nm); !started || lr.calls() != 1 {
				t.Errorf("an unfenced box was held by its text (command %v, %d calls)", started, lr.calls())
			}
		})
	}
}

// A held press has nothing to offer, like a press the service answered with
// nothing, and tab then moves on: the line says so, and the next press leaves the
// box instead of refusing again. Without it a live box above a fenced one still
// blank was a box the person could only leave by enter.
func TestATabAfterAHeldPressMovesOn(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveFenceModel(t, map[string]any{"namespace": []any{"shop", "jobs"}},
		clusterList(lr, "orders-db"), namespaceOne())
	nm := openFenced(t, m, c, "")
	box := nm.form.form.GetFocusedField()

	nm, _ = heldPress(t, nm)
	if !strings.Contains(nm.flash, "tab moves on") {
		t.Errorf("flash = %q, want it to say what the next tab does", nm.flash)
	}
	if nm.form.form.GetFocusedField() != box {
		t.Fatal("the held press itself left the box")
	}

	next, cmd := nm.Update(tabKey)
	if cmd == nil {
		t.Fatal("the tab after a held press did nothing: the dead key this rule exists to remove")
	}
	next, _ = next.Update(cmd())
	if next.(Model).form.form.GetFocusedField() == box {
		t.Error("the tab after a held press stayed in the box")
	}
	if lr.calls() != 0 {
		t.Errorf("the service was asked %d times", lr.calls())
	}
}

// A fenced box being completed: the service may not be able to list what the
// profile reaches. A credential bound by a Role in each namespace cannot list
// them cluster-wide, so its answer is empty, or an error the plugin keeps silent,
// and "no completions" over a box whose fence names the namespaces says nothing
// true. The keystroke channel already offers the allowlist there.

// What the service could not say, the fence does: the allowlist narrowed to what is
// typed, the line says whose answer it is, and a tab takes one without asking again.
// The line says why as well, and truthfully: a service that gave names none of which
// the connection reaches did answer, and "no answer" over it would be false — nor
// does the line list the names the fence left out, which are the service's whole view.
func TestAFencedLiveBoxOffersTheFenceWhenTheServiceHasNoAnswer(t *testing.T) {
	noHistory(t)
	const noAnswer = "the service gave no answer"
	const outside = "none of the service's names are in the profile's fence"
	for name, tc := range map[string]struct {
		answers []string
		lead    string
		absent  []string
	}{
		"a failed answer": {nil, noAnswer, nil},
		"an empty answer": {[]string{}, noAnswer, nil},
		"an answer outside the fence": {[]string{"unrelated", "elsewhere"}, outside,
			[]string{"no answer", "unrelated", "elsewhere"}},
	} {
		answers := tc.answers
		t.Run(name, func(t *testing.T) {
			lr := &liveRecorder{}
			m, c := liveFenceModel(t, shopFence, namespaceListLive(lr, answers...))
			nm := openBox(t, m, c, "")

			nm = fetchLive(t, nm)
			if lr.calls() != 1 {
				t.Fatalf("the service was asked %d times, want once", lr.calls())
			}
			if got, want := nm.form.suggested["namespace"], []string{"shop", "jobs", "audit"}; !slices.Equal(got, want) {
				t.Errorf("suggested = %v, want the fence's own entries", got)
			}
			for _, want := range []string{tc.lead, "the profile's entries for namespace", "shop, jobs, audit"} {
				if !strings.Contains(nm.flash, want) {
					t.Errorf("flash = %q, want it to say %q", nm.flash, want)
				}
			}
			for _, leaked := range tc.absent {
				if strings.Contains(nm.flash, leaked) {
					t.Errorf("flash = %q says %q", nm.flash, leaked)
				}
			}
			// A guide's colour: the service did not confirm that any of it exists.
			if !flashedAsGuide(nm) {
				t.Errorf("flash %q is not drawn as a guide (guide %q, refusal %q)", nm.flash, nm.flashGuide, nm.flashBad)
			}

			// Narrowed to what is typed, the elements already listed left out.
			again, c2 := liveFenceModel(t, shopFence, namespaceListLive(lr, answers...))
			nm = openBox(t, again, c2, "shop, j")
			nm = fetchLive(t, nm)
			if got, want := nm.form.suggested["namespace"], []string{"jobs"}; !slices.Equal(got, want) {
				t.Errorf("suggested = %v after typing, want only the entry that continues j", got)
			}
			if want := tc.lead + "; 1 of the profile's entries for namespace"; !strings.Contains(nm.flash, want) {
				t.Errorf("flash = %q, want it to say %q", nm.flash, want)
			}
			for _, leaked := range tc.absent {
				if strings.Contains(nm.flash, leaked) {
					t.Errorf("flash = %q says %q", nm.flash, leaked)
				}
			}
			if !flashedAsGuide(nm) {
				t.Errorf("flash %q is not drawn as a guide", nm.flash)
			}
			if got, want := offeredIn(nm, "namespace"), []string{"shop, jobs"}; !slices.Equal(got, want) {
				t.Errorf("the box offers %q, want the entry once", got)
			}

			// Taken in place, with no further call.
			calls := lr.calls()
			next, _ := nm.Update(tabKey)
			nm = next.(Model)
			if got := *nm.form.bindings["namespace"]; got != "shop, jobs" {
				t.Errorf("tab left the box at %q, want the entry taken", got)
			}
			if lr.calls() != calls {
				t.Errorf("the accept called the service (%d calls)", lr.calls())
			}
		})
	}
}

// The same on a box that holds one value, where the typed text is the whole box.
func TestAFencedLiveStringBoxOffersTheFenceWhenTheServiceHasNoAnswer(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveFenceModel(t, shopFence, namespaceOneLive(lr))
	nm := openBox(t, m, c, "j")

	nm = fetchLive(t, nm)
	if got, want := nm.form.suggested["namespace"], []string{"jobs"}; !slices.Equal(got, want) {
		t.Errorf("suggested = %v, want the entry that continues j", got)
	}
	if !strings.Contains(nm.flash, "profile") || !strings.Contains(nm.flash, "no answer") {
		t.Errorf("flash = %q, want it to say whose answer this is", nm.flash)
	}
}

// The one entry that continues what is typed is already in the box: no completions,
// said the usual way. (Text that continues no entry at all never gets this far: it
// holds the press.)
func TestAFencedLiveBoxWithNothingLeftToContinueTheTextSaysSo(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveFenceModel(t, shopFence, namespaceListLive(lr))
	nm := openBox(t, m, c, "shop, sh")

	nm = fetchLive(t, nm)
	if nm.flash != "no namespace completions" || nm.flash != nm.flashBad {
		t.Errorf("flash = %q, want the plain refusal", nm.flash)
	}
	if got := nm.form.suggested["namespace"]; len(got) != 0 {
		t.Errorf("suggested = %v, want nothing", got)
	}
}

// The fence's own entries are offered by the same rule the service's answer is
// held to: a list box takes each name once, so the ones it lists already are not on
// offer, and a box that lists every entry has nothing left to say but that.
func TestTheFencesOwnEntriesLeaveOutWhatTheListBoxAlreadyHolds(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveFenceModel(t, shopFence, namespaceListLive(lr))
	nm := openBox(t, m, c, "shop, ")

	nm = fetchLive(t, nm)
	if got, want := nm.form.suggested["namespace"], []string{"jobs", "audit"}; !slices.Equal(got, want) {
		t.Errorf("suggested = %v, want the entries the box does not list", got)
	}
	if want := "the service gave no answer; 2 of the profile's entries for namespace"; !strings.Contains(nm.flash, want) {
		t.Errorf("flash = %q, want it to count %q", nm.flash, want)
	}

	again, c2 := liveFenceModel(t, shopFence, namespaceListLive(lr))
	nm = openBox(t, again, c2, "shop, jobs, audit, ")
	nm = fetchLive(t, nm)
	if nm.flash != "no namespace completions" || !flashedAsRefusal(nm) {
		t.Errorf("flash = %q, want the plain refusal for a box that lists every entry", nm.flash)
	}
	if got := nm.form.suggested["namespace"]; len(got) != 0 {
		t.Errorf("suggested = %v, want nothing", got)
	}
}

// An entry that would display as something else is not offered either, however it
// got into the fence: a completion is typed into the box as it is offered, and one
// holding a bidi override reads as another name than the one the run is checked for.
func TestAFenceEntryThatWouldDisplayAsSomethingElseIsNotOffered(t *testing.T) {
	noHistory(t)
	deceptive := "jobs" + string(rune(0x202e)) + "dua"
	fence := map[string]any{"namespace": []any{"shop", deceptive, "audit"}}

	lr := &liveRecorder{}
	m, c := liveFenceModel(t, fence, namespaceListLive(lr))
	nm := openBox(t, m, c, "")
	nm = fetchLive(t, nm)
	if got, want := nm.form.suggested["namespace"], []string{"shop", "audit"}; !slices.Equal(got, want) {
		t.Errorf("suggested = %q, want only the entries that display as themselves", got)
	}

	again, c2 := liveFenceModel(t, fence, namespaceListLive(lr))
	nm = openBox(t, again, c2, "jo")
	nm = fetchLive(t, nm)
	if nm.flash != "no namespace completions" {
		t.Errorf("flash = %q, want the plain refusal: the only entry that continues jo is not offered", nm.flash)
	}
	if got := nm.form.suggested["namespace"]; len(got) != 0 {
		t.Errorf("suggested = %q, want nothing", got)
	}
}

// An answer with entries the fence holds is the answer, as it always was: the
// fence's other entries do not join it, and the line does not talk of the profile.
func TestAFencedLiveBoxKeepsAnAnswerThatHasEntries(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveFenceModel(t, shopFence, namespaceListLive(lr, "shop", "unrelated", "audit"))
	nm := openBox(t, m, c, "")

	nm = fetchLive(t, nm)
	if got, want := nm.form.suggested["namespace"], []string{"shop", "audit"}; !slices.Equal(got, want) {
		t.Errorf("suggested = %v, want the answer held to the fence", got)
	}
	if strings.Contains(nm.flash, "profile") || !strings.Contains(nm.flash, "shop, audit") {
		t.Errorf("flash = %q, want the plain listing of the answer", nm.flash)
	}
}

// The box being completed is not held by what is typed in it, and the request
// carries it as the partial: the answer is the fence's to filter.
func TestAPressOnAFencedBoxIsNotHeldByItsOwnText(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveFenceModel(t, shopFence, namespaceOneLive(lr, "shop", "shoes"))
	nm := openBox(t, m, c, "sh")

	nm = fetchLive(t, nm)
	if got := lr.last(t).String("namespace"); got != "sh" {
		t.Errorf("the request carries %q, want the text typed", got)
	}
	if got, want := nm.form.suggested["namespace"], []string{"shop"}; !slices.Equal(got, want) {
		t.Errorf("suggested = %v, want the answer held to the fence", got)
	}
}

// A box the profile does not fence has nothing to fall back on.
func TestAnUnfencedLiveBoxWithNoAnswerStaysEmptyHanded(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveFenceModel(t, shopFence, namespaceListLive(lr))
	nm := openLiveList(t, m, c)
	setBox(nm.form, "namespace", "")

	nm = fetchLive(t, nm)
	if nm.flash != "no namespace completions" {
		t.Errorf("flash = %q, want the plain refusal with no profile in force", nm.flash)
	}
}

// A value the service answers twice is offered once. The same cluster name in two
// namespaces is one name to put in a box, and counting it twice told the person
// there were more to choose from than there are.
func TestALiveAnswerListsAValueOnce(t *testing.T) {
	noHistory(t)
	for name, live := range map[string]func(*liveRecorder, ...string) plugin.Field{
		"a list live box":   clusterList,
		"a single live box": clusterOne,
	} {
		t.Run(name, func(t *testing.T) {
			lr := &liveRecorder{}
			m, c := liveFenceModel(t, map[string]any{"cluster": []any{"orders-db", "billing-db", "audit-db"}},
				live(lr, "orders-db\tin shop", "billing-db\tin shop", "orders-db\tin jobs", "billing-db", "audit-db"),
				namespaceList())
			nm := openFenced(t, m, c, "shop, jobs")
			setBox(nm.form, "cluster", "")

			nm = fetchLive(t, nm)
			if got, want := nm.form.suggested["cluster"], []string{"orders-db", "billing-db", "audit-db"}; !slices.Equal(got, want) {
				t.Errorf("suggested = %v, want each name once", got)
			}
			if want := "orders-db, billing-db, audit-db"; !strings.Contains(nm.flash, want) {
				t.Errorf("flash = %q, want %q", nm.flash, want)
			}

			// And the count of a box with text in it is of names, not of answers.
			again, c2 := liveFenceModel(t, map[string]any{"cluster": []any{"orders-db", "billing-db", "audit-db"}},
				live(lr, "orders-db\tin shop", "orders-db\tin jobs", "billing-db"), namespaceList())
			nm = openFenced(t, again, c2, "shop, jobs")
			setBox(nm.form, "cluster", "o")
			nm = fetchLive(t, nm)
			if want := "2 cluster completions"; !strings.Contains(nm.flash, want) {
				t.Errorf("flash = %q, want %q", nm.flash, want)
			}
		})
	}
}

// What the keystroke channel offers does not move with the press: a fenced live
// box offers its allowlist and calls nothing, an unfenced one offers nothing, and a
// press landing an answer does not change what typing is offered.
func TestTheKeystrokeChannelOfALiveBoxIsUnchanged(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveFenceModel(t, shopFence, namespaceListLive(lr, "shop", "shop", "unrelated"))
	nm := openBox(t, m, c, "")
	field := c.Inputs[0]

	if got, want := nm.form.candidates(field), []string{"shop", "jobs", "audit"}; !slices.Equal(got, want) {
		t.Errorf("a fenced live box offers %v per keystroke, want its allowlist", got)
	}
	if lr.calls() != 0 {
		t.Fatalf("the keystroke channel called the live Suggest (%d calls)", lr.calls())
	}
	nm = fetchLive(t, nm)
	if got, want := nm.form.candidates(field), []string{"shop", "jobs", "audit"}; !slices.Equal(got, want) {
		t.Errorf("after a press the box offers %v per keystroke, want the allowlist once, and not the answer beside it", got)
	}
	if lr.calls() != 1 {
		t.Errorf("typing and the press made %d calls, want the press's one", lr.calls())
	}
}
