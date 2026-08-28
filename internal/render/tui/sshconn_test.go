package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
)

// The editor and run-form rules about a forward were written under `kube:`
// and re-derive from one predicate (config.Connection.Tunnelled) — these
// tests hold each of them under `ssh:`, the second scheme, so the predicate
// is pinned rather than trusted. D119 is why: the last time a rule lived on
// a scheme's own field, its twin went unwritten.

const sshEditorTarget = "tobi@bastion.internal:2222/postgres.internal:5432"

// Under an ssh connection the endpoint boxes are not asked, exactly as under
// a coordinate: the tunnel fills them per call, and the picker's one line
// says so.
func TestEndpointFieldsUnderAnSSHConnectionAreNotAsked(t *testing.T) {
	m := endpointEditorModel(t, config.Connection{SSH: sshEditorTarget})
	switchOn(t, &m, "staging")
	c := m.plugins[0].plugin.Capabilities[0]

	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	for _, name := range []string{"host", "port", "sslmode"} {
		if _, asked := nm.form.bindings[name]; asked {
			t.Errorf("%s has a box under an ssh connection — the tunnel fills it, "+
				"and an empty box is a question", name)
		}
	}
	if picker := nm.form.fields[0]; !strings.Contains(picker.Help, "tunnel, which fills host, port, sslmode") {
		t.Errorf("picker help = %q — nothing on screen says where the endpoint went", picker.Help)
	}
}

// The conn editor offers the ssh box seeded from the file, hides the set.
// boxes its forward fills, and an untouched save repairs the dead keys with
// the receipt — the kube editor test, under the second scheme.
func TestTheConnEditorHidesWhatTheSSHForwardFills(t *testing.T) {
	m := endpointEditorModel(t, config.Connection{
		SSH: sshEditorTarget,
		Set: map[string]any{"host": "stale.internal", "sslmode": "prefer"},
	})
	model, _ := m.startConnForm("db")
	nm := model.(Model)
	if got := *nm.form.bindings[profileSSHField]; got != sshEditorTarget {
		t.Fatalf("ssh box = %q, want the file's target on screen", got)
	}
	for _, name := range []string{"host", "port", "sslmode"} {
		if _, offered := nm.form.bindings[profileSetPrefix+name]; offered {
			t.Errorf("set.%s has a box beside an ssh target — an invitation to state the "+
				"losing destination", name)
		}
	}
	if _, offered := nm.form.bindings[profileSetPrefix+"schema"]; !offered {
		t.Error("set.schema lost its box — only the endpoint keys are the forward's to fill")
	}
	model, _ = nm.saveConnForm()
	nm = model.(Model)
	if !strings.Contains(nm.flash, "removed set.host, set.sslmode") {
		t.Errorf("flash = %q, want the removal named", nm.flash)
	}
	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	saved := onDisk.Profiles["staging"].Plugins["db"]
	if saved.SSH != sshEditorTarget {
		t.Errorf("ssh = %q, want the target kept", saved.SSH)
	}
	for _, name := range []string{"host", "sslmode"} {
		if v, still := saved.Set[name]; still {
			t.Errorf("set.%s = %v survived the save — the file still names two destinations", name, v)
		}
	}
}

// Typing an ssh target over a direct connection repairs the endpoint keys and
// the shadowed mapping at save, keeps the credential mapping, and the flash
// is the receipt.
func TestSavingAnSSHTargetRemovesTheKeptEndpointKeys(t *testing.T) {
	m := endpointEditorModel(t, config.Connection{
		Set:     map[string]any{"host": "stale.internal", "sslmode": "prefer"},
		Secrets: map[string]string{"host": "kv:db-host", "password": "kv:db-pass"},
	})
	model, _ := m.startConnForm("db")
	nm := model.(Model)
	if _, offered := nm.form.bindings[profileSetPrefix+"host"]; !offered {
		t.Fatal("a direct connection's set.host has no box")
	}
	*nm.form.bindings[profileSSHField] = sshEditorTarget
	model, _ = nm.saveConnForm()
	nm = model.(Model)
	if !strings.Contains(nm.flash, "removed set.host, set.sslmode") ||
		!strings.Contains(nm.flash, "secrets.host") {
		t.Errorf("flash = %q, want every removal named", nm.flash)
	}
	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	saved := onDisk.Profiles["staging"].Plugins["db"]
	if saved.SSH != sshEditorTarget {
		t.Errorf("ssh = %q, want the target the person typed", saved.SSH)
	}
	for _, name := range []string{"host", "sslmode"} {
		if _, still := saved.Set[name]; still {
			t.Errorf("set.%s survived beside the ssh target", name)
		}
	}
	if _, still := saved.Secrets["host"]; still {
		t.Error("secrets.host was written beside the ssh target — fetched and discarded per call")
	}
	if saved.Secrets["password"] != "kv:db-pass" {
		t.Errorf("secrets.password = %v, want the credential mapping kept", saved.Secrets["password"])
	}
}

// Two tunnels in the editor is a refusal, not a repair — both boxes are plain
// text, so "empty one" is an action the widget can always perform, which is
// exactly the line D119 draws between the two answers. Nothing is written.
func TestTheConnEditorRefusesTwoTunnels(t *testing.T) {
	m := endpointEditorModel(t, config.Connection{Set: map[string]any{"schema": "public"}})
	model, _ := m.startConnForm("db")
	nm := model.(Model)
	*nm.form.bindings[profileKubeField] = "homelab/databases/svc/postgres:5432"
	*nm.form.bindings[profileSSHField] = sshEditorTarget
	model, _ = nm.saveConnForm()
	nm = model.(Model)
	if !strings.Contains(nm.flash, "one tunnel") {
		t.Errorf("flash = %q, want the one-tunnel refusal", nm.flash)
	}
	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	saved := onDisk.Profiles["staging"].Plugins["db"]
	if saved.Kube != "" || saved.SSH != "" {
		t.Errorf("kube=%q ssh=%q were written by a refused save", saved.Kube, saved.SSH)
	}
}

// A typo in the target is caught at the keyboard — the one screen where
// somebody is typing it — not by `rta doctor` after the profile is saved
// broken.
func TestAMalformedSSHTargetIsRefusedAtTheKeyboard(t *testing.T) {
	m := endpointEditorModel(t, config.Connection{})
	model, _ := m.startConnForm("db")
	nm := model.(Model)
	*nm.form.bindings[profileSSHField] = "bastion.internal"
	model, _ = nm.saveConnForm()
	nm = model.(Model)
	if !strings.Contains(nm.flash, "not an ssh target") {
		t.Errorf("flash = %q, want the parse refusal with the form", nm.flash)
	}
	onDisk, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	if saved := onDisk.Profiles["staging"].Plugins["db"]; saved.SSH != "" {
		t.Errorf("ssh = %q was written, and it is not a target", saved.SSH)
	}
}

// The credential editor under an ssh tunnel: endpoint inputs are not mapping
// targets (the forward fills them), and the cluster source is not offered —
// an ssh tunnel reaches a TCP port, not an apiserver.
func TestTheCredentialEditorUnderAnSSHTunnel(t *testing.T) {
	m := endpointEditorModel(t, config.Connection{SSH: sshEditorTarget})
	model, _ := m.startCredentialForm()
	nm := model.(Model)
	opts := optionsOf(nm.form, credInputField)
	for _, name := range []string{"host", "port", "sslmode"} {
		if slices.Contains(opts, name) {
			t.Errorf("%q is offered as a mapping target under an ssh tunnel (offered: %v)", name, opts)
		}
	}
	if !slices.Contains(opts, "password") {
		t.Errorf("password lost its place (offered: %v)", opts)
	}
	if sources := optionsOf(nm.form, credSourceField); slices.Contains(sources, credSourceKube) {
		t.Errorf("the cluster source is offered under an ssh tunnel (sources: %v) — there is "+
			"no coordinate to read a Secret through", sources)
	}
}

// Tab on a live field under an ssh connection is refused before anything is
// resolved or spawned — the coordinate refusal's twin, with the flash naming
// the scheme. The poisoned ssh proves no keypress ever launches one.
func TestALiveFetchUnderAnSSHTargetIsRefused(t *testing.T) {
	noHistory(t)
	poison := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(poison, 0o755); err != nil {
		t.Fatal(err)
	}
	ran := filepath.Join(poison, "ssh-ran")
	if err := os.WriteFile(filepath.Join(poison, "ssh"),
		[]byte("#!/bin/sh\ntouch "+ran+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", poison+string(os.PathListSeparator)+os.Getenv("PATH"))

	lr := &liveRecorder{}
	m, c := liveModel(t, lr, map[string]config.Profile{
		"homelab": {Plugins: map[string]config.Connection{
			"s3": {SSH: "tobi@bastion.internal/minio.internal:9000"},
		}},
	})
	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	nm.form.form = startedForm(nm.form)
	nm.form.form = settleForm(nm.form.form, tea.KeyPressMsg{Code: tea.KeyEnter})
	if nm.form.form.GetFocusedField() != huh.Field(nm.form.inputs["bucket"]) {
		t.Fatal("enter from the picker did not land on the bucket field")
	}
	*nm.form.bindings[profileInput] = "homelab"

	next, cmd := nm.Update(tabKey)
	nm = next.(Model)
	if cmd != nil {
		t.Error("tab under an ssh target started a fetch anyway")
	}
	if !strings.Contains(nm.flash, "homelab's ssh target") {
		t.Errorf("flash = %q, want the refusal naming the picked environment and the scheme", nm.flash)
	}
	if lr.calls() != 0 {
		t.Errorf("the live Suggest ran %d times against an endpoint that does not exist", lr.calls())
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("the tab spawned ssh before refusing — a keypress must not open a tunnel")
	}
}
