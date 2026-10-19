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
