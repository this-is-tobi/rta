package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Live completion, driven the way a person drives it: tab through the real
// Update path. The contract package proves the channels' rules — Candidates
// gates Live, LiveRequest carries credentials — and these prove the TUI
// honours them end to end: the tab key reaches a plugin's Live Suggest with
// what the run would get, and typing never reaches it at all.

// liveRecorder records every request a Live Suggest answers, so a test can
// assert what crossed and how often.
type liveRecorder struct {
	mu   sync.Mutex
	reqs []plugin.Request
}

func (lr *liveRecorder) suggest(answers ...string) func(context.Context, plugin.Request) []string {
	return func(_ context.Context, req plugin.Request) []string {
		lr.mu.Lock()
		defer lr.mu.Unlock()
		lr.reqs = append(lr.reqs, req)
		return answers
	}
}

func (lr *liveRecorder) calls() int {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	return len(lr.reqs)
}

func (lr *liveRecorder) last(t *testing.T) plugin.Request {
	t.Helper()
	lr.mu.Lock()
	defer lr.mu.Unlock()
	if len(lr.reqs) == 0 {
		t.Fatal("no live request was recorded")
	}
	return lr.reqs[len(lr.reqs)-1]
}

// liveModel is a model whose plugin declares a Live bucket field beside a
// secret and an endpoint role — the s3 shape, which is what the feature is
// for.
func liveModel(t *testing.T, lr *liveRecorder, profiles map[string]config.Profile) (Model, plugin.Capability) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(config.Config{Profiles: profiles}); err != nil {
		t.Fatal(err)
	}
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil }
	c := plugin.Capability{
		ID: "s3.object.list", Summary: "list", Safety: plugin.Read, Run: run,
		Inputs: []plugin.Field{
			{Name: "bucket", Type: plugin.String, Live: true, Help: "bucket",
				Suggest: lr.suggest("backups\tprod data", "media/")},
			{Name: "endpoint", Type: plugin.String, Config: "endpoint", Local: true,
				Endpoint: plugin.EndpointURL, Help: "endpoint"},
			// EnvFallback, as every real credential input declares:
			// without it the input is not ProfileFillable, so a `secrets:`
			// mapping onto it is refused — which checkSecretRefs now says at
			// the page, and this fixture found out the day it landed.
			{Name: "secret-key", Type: plugin.Secret, Local: true, EnvFallback: true,
				Help: "secret"},
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

// Tab asks the service; typing never does.
//
// One flow, because it is one rhythm: the keystroke channel offers nothing
// and calls nothing (Candidates gates Live), the first tab fetches with the
// credentials the run would get, the fetched list survives the keystroke
// channel's re-evaluation, tab with a matching suggestion accepts without a
// second fetch, and tab past the last answer fetches deeper with the box's
// text as the partial — the compose rhythm the trailing separator exists
// for.
func TestTabAsksTheServiceAndTypingNeverDoes(t *testing.T) {
	noHistory(t)
	lr := &liveRecorder{}
	m, c := liveModel(t, lr, nil)
	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	nm.form.form = startedForm(nm.form)

	// A credential the person already typed, exactly what the listing needs.
	*nm.form.bindings["secret-key"] = "sk-999"

	bucket := c.Inputs[0]
	if got := nm.form.candidates(bucket); len(got) != 0 {
		t.Errorf("the keystroke channel offers %v before any fetch, want nothing", got)
	}
	if lr.calls() != 0 {
		t.Fatalf("the live Suggest ran %d times without a completion press", lr.calls())
	}

	nm = fetchFromCluster(t, nm) // empty box: tab fetches
	if lr.calls() != 1 {
		t.Fatalf("tab called the live Suggest %d times, want once", lr.calls())
	}
	req := lr.last(t)
	if got := req.String("secret-key"); got != "sk-999" {
		t.Errorf("the listing ran with secret-key %q, want the typed credential — "+
			"without it the plugin cannot authenticate", got)
	}
	if req.Surface() != plugin.SurfaceCompletion {
		t.Errorf("surface = %v, want SurfaceCompletion", req.Surface())
	}
	if got := req.String("bucket"); got != "" {
		t.Errorf("partial = %q on an empty box, want empty", got)
	}
	if got := nm.form.suggested["bucket"]; len(got) != 2 || got[0] != "backups" || got[1] != "media/" {
		t.Fatalf("suggested = %v, want the two answers with descriptions stripped", got)
	}
	if !strings.Contains(nm.flash, "backups, media/") {
		t.Errorf("flash = %q, want the names themselves on an empty box", nm.flash)
	}

	// The fetch survives the keystroke channel's re-evaluation: without the
	// liveGot merge, the next keystroke in any box wipes the landed list off
	// the widget.
	if got := nm.form.candidates(bucket); len(got) != 2 || got[0] != "backups" {
		t.Errorf("the keystroke channel offers %v after the fetch, want the fetched list kept", got)
	}

	// Type until one answer extends the box: that tab is the accept, and it
	// costs no second call.
	nm.form.form = typeInto(nm.form.form, "ba")
	next, _ := nm.Update(tabKey)
	nm = next.(Model)
	if got := *nm.form.bindings["bucket"]; got != "backups" {
		t.Fatalf("tab with a suggestion on offer did not accept it (field %q)", got)
	}
	if lr.calls() != 1 {
		t.Errorf("the accept called the service (%d calls), want the widget alone", lr.calls())
	}

	// Nothing on offer extends "backups", so this tab fetches deeper, and the
	// box's text is the partial the listing narrows on.
	nm = fetchFromCluster(t, nm)
	if lr.calls() != 2 {
		t.Fatalf("tab past the last answer called the service %d times total, want 2", lr.calls())
	}
	if got := lr.last(t).String("bucket"); got != "backups" {
		t.Errorf("the deeper fetch's partial = %q, want the accepted text", got)
	}
}

// A live fetch under a cluster connection is refused with the reason — for
// the environment the picker names, and before anything is fetched.
//
// Three rules in one flow, each found the hard way. The forward opens per
// call and a completion is not a call, so the coordinate is a
// refusal rather than a listing against a loopback port nothing listens on.
// The environment is the picker's answer: a first cut stripped the picker
// before resolving, so tab completed against the switched-on environment
// while the picker above the field named another. And the refusal comes
// before any credential is resolved: this connection references a `kube:`
// Secret, and resolving first meant the tab read it out of the cluster — a
// real access in its audit log — and then refused anyway. The poison
// kubectl is the assertion that no completion press ever does that again.
func TestALiveFetchUnderACoordinateIsRefused(t *testing.T) {
	noHistory(t)
	poison := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(poison, 0o755); err != nil {
		t.Fatal(err)
	}
	ran := filepath.Join(poison, "kubectl-ran")
	if err := os.WriteFile(filepath.Join(poison, "kubectl"),
		[]byte("#!/bin/sh\ntouch "+ran+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", poison+string(os.PathListSeparator)+os.Getenv("PATH"))

	lr := &liveRecorder{}
	m, c := liveModel(t, lr, map[string]config.Profile{
		"homelab": {Plugins: map[string]config.Connection{
			"s3": {Kube: "homelab/storage/svc/minio:9000",
				Secrets: map[string]string{"secret-key": "kube:s3-creds/secret-key"}},
		}},
	})
	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	nm.form.form = startedForm(nm.form)

	// The picker sits first; enter moves to the bucket box the way a person
	// does. Nothing is switched on, so the endpoint box exists and the pick
	// below is the only thing that says homelab.
	nm.form.form = settleForm(nm.form.form, tea.KeyPressMsg{Code: tea.KeyEnter})
	if nm.form.form.GetFocusedField() != huh.Field(nm.form.inputs["bucket"]) {
		t.Fatal("enter from the picker did not land on the bucket field")
	}
	*nm.form.bindings[profileInput] = "homelab"

	next, cmd := nm.Update(tabKey)
	nm = next.(Model)
	if cmd != nil {
		t.Error("tab under a coordinate started a fetch anyway")
	}
	if !strings.Contains(nm.flash, "homelab's coordinate") {
		t.Errorf("flash = %q, want the refusal naming the picked environment — resolving "+
			"the switch instead of the pick is completing with the wrong credentials", nm.flash)
	}
	if lr.calls() != 0 {
		t.Errorf("the live Suggest ran %d times against a connection whose endpoint does not exist", lr.calls())
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("the tab read a cluster Secret before refusing — an audit-visible access " +
			"caused by a keypress that then did nothing")
	}
}
