package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// wipe is a destructive capability whose handler records whether each call
// was a dry run, so a test can say which of the two the screen caused.
func wipe(runs *[]bool) plugin.Capability {
	return plugin.Capability{
		ID: "demo.wipe", Summary: "wipe a target", Safety: plugin.Destructive,
		Inputs: []plugin.Field{
			{Name: "target", Type: plugin.String, Positional: true, Required: true},
			{Name: "token", Type: plugin.Secret},
		},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			*runs = append(*runs, req.DryRun)
			if req.DryRun {
				return view.Text{Body: "would wipe " + req.String("target")}, nil
			}
			return view.Text{Body: "wiped " + req.String("target")}, nil
		},
	}
}

// messagesOf runs a Cmd tree and returns every leaf message it produces.
func messagesOf(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, sub := range msg {
			out = append(out, messagesOf(sub)...)
		}
		return out
	case nil:
		return nil
	default:
		return []tea.Msg{msg}
	}
}

func previewOf(t *testing.T, cmd tea.Cmd) previewMsg {
	t.Helper()
	for _, msg := range messagesOf(cmd) {
		if p, ok := msg.(previewMsg); ok {
			return p
		}
	}
	t.Fatal("the dry run produced no preview")
	return previewMsg{}
}

func confirmModel(t *testing.T, c plugin.Capability) Model {
	t.Helper()
	noHistory(t)
	m := New(fastFormRegistry(t, c), config.Dashboard{}, nil)
	um, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	nm := um.(Model)
	nm.origin = modeBrowse
	return nm
}

// The destructive gate is the dry run on screen, and enter is the consent.
// The form used to end in a Confirm field saying "This cannot be undone" —
// for every capability, including the ones with an undo — and the run
// followed from a field default. Now nothing runs until the screen showing
// what would happen has been answered, and the handler sees exactly two
// calls: the dry run, then the real one.
func TestADestructiveRunPreviewsItsDryRunAndWaitsForEnter(t *testing.T) {
	var runs []bool
	c := wipe(&runs)
	m := confirmModel(t, c)
	cmd := m.startConfirm(c, map[string]any{"target": "db1"})
	if m.mode != modeRunning || !m.previewing {
		t.Fatalf("startConfirm: mode=%v previewing=%v, want the dry run in flight", m.mode, m.previewing)
	}
	preview := previewOf(t, cmd)
	if len(runs) != 1 || !runs[0] {
		t.Fatalf("handler calls = %v, want exactly one dry run", runs)
	}
	shown, _ := m.Update(preview)
	sm := shown.(Model)
	if sm.mode != modeConfirm {
		t.Fatalf("mode after the preview = %v, want the confirmation screen", sm.mode)
	}
	body := plain(sm.confirmView())
	for _, want := range []string{"would wipe db1", "nothing has run yet", "enter run", "e edit inputs", "esc cancel"} {
		if !strings.Contains(body, want) {
			t.Errorf("the confirmation lacks %q:\n%s", want, body)
		}
	}
	ran, next := sm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	rm := ran.(Model)
	if rm.mode != modeRunning || rm.previewing || !rm.lastYes {
		t.Fatalf("enter: mode=%v previewing=%v yes=%v, want the consented run in flight", rm.mode, rm.previewing, rm.lastYes)
	}
	var result *resultMsg
	for _, msg := range messagesOf(next) {
		if r, ok := msg.(resultMsg); ok {
			result = &r
		}
	}
	if result == nil || result.err != nil {
		t.Fatalf("the consented run produced %v", result)
	}
	if len(runs) != 2 || runs[1] {
		t.Fatalf("handler calls = %v, want a dry run and then a real one", runs)
	}
	if text, _ := result.view.(view.Text); text.Body != "wiped db1" {
		t.Errorf("the real run returned %q", text.Body)
	}
}

func TestCancellingTheConfirmationRunsNothing(t *testing.T) {
	var runs []bool
	c := wipe(&runs)
	m := confirmModel(t, c)
	cmd := m.startConfirm(c, map[string]any{"target": "db1"})
	shown, _ := m.Update(previewOf(t, cmd))
	back, _ := shown.(Model).Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	bm := back.(Model)
	if bm.mode != modeBrowse {
		t.Fatalf("esc went to %v, want back where the capability was opened", bm.mode)
	}
	if len(runs) != 1 || !runs[0] {
		t.Fatalf("handler calls = %v, want only the dry run", runs)
	}
	if !strings.Contains(bm.flash, "nothing ran") {
		t.Errorf("flash = %q, want it to say nothing ran", bm.flash)
	}
}

func TestEditFromTheConfirmationReopensTheInputs(t *testing.T) {
	var runs []bool
	c := wipe(&runs)
	m := confirmModel(t, c)
	cmd := m.startConfirm(c, map[string]any{"target": "db1"})
	shown, _ := m.Update(previewOf(t, cmd))
	edited, _ := shown.(Model).Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	em := edited.(Model)
	if em.mode != modeForm || em.form == nil {
		t.Fatalf("e: mode=%v, want the form reopened", em.mode)
	}
	if got := em.form.values()["target"]; got != "db1" {
		t.Errorf("the reopened form lost the value: %v", got)
	}
}

// A plugin from outside this binary makes a claim rta cannot check, and a
// dry run that lies is the one thing that must not happen before anyone has
// said yes — the same line internal/mcp's propose draws for the consent
// queue. Its confirmation shows the inputs, masks the credential, says why
// there is no preview, and still asks.
func TestAnExternalCapabilityIsConfirmedOnItsInputsAlone(t *testing.T) {
	noHistory(t)
	var runs []bool
	c := wipe(&runs)
	reg := registry.New()
	err := reg.RegisterFrom(plugin.Plugin{Name: "demo", Summary: "demo plugin", Capabilities: []plugin.Capability{c}},
		registry.Origin{Path: "/opt/bin/rta-plugin-demo", Digest: "0123456789abcdef0123456789abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	um, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	nm := um.(Model)
	cmd := nm.startConfirm(c, map[string]any{"target": "db1", "token": "hunter2"})
	if cmd != nil || nm.mode != modeConfirm {
		t.Fatalf("an external capability was dry-run before consent (mode %v, cmd %v)", nm.mode, cmd != nil)
	}
	if len(runs) != 0 {
		t.Fatalf("the plugin's handler ran %d time(s) before anyone confirmed", len(runs))
	}
	body := plain(nm.confirmView())
	for _, want := range []string{"will run with", "target", "db1", "No preview", "outside this binary", "enter run"} {
		if !strings.Contains(body, want) {
			t.Errorf("the inputs-only confirmation lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "hunter2") {
		t.Errorf("a secret input is shown on the confirmation:\n%s", body)
	}
}

// A dry run that fails says so and still asks — the same rule the consent
// queue applies: a preview that fails blocks nothing, and the person decides
// with the failure in front of them rather than with nothing.
func TestAFailedDryRunIsShownAndStillAsks(t *testing.T) {
	c := plugin.Capability{ID: "demo.wipe", Summary: "s", Safety: plugin.Destructive,
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			if req.DryRun {
				return nil, view.Errorf("demo.wipe.preview", "no such target")
			}
			return view.Text{Body: "wiped"}, nil
		}}
	m := confirmModel(t, c)
	cmd := m.startConfirm(c, nil)
	preview := previewOf(t, cmd)
	if preview.err == nil {
		t.Fatal("the failing dry run did not come back as a preview error")
	}
	shown, _ := m.Update(preview)
	sm := shown.(Model)
	if sm.mode != modeConfirm {
		t.Fatalf("mode = %v, want the confirmation screen", sm.mode)
	}
	if body := plain(sm.confirmView()); !strings.Contains(body, "no such target") || !strings.Contains(body, "enter run") {
		t.Errorf("the failed preview is not shown with the question still open:\n%s", body)
	}
}
