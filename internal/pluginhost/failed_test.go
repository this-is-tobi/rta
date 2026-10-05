package pluginhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/plugintrust"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/view"
)

// brokenPlugin is an approved artifact that exits before its handshake, put
// in dir under the name boom.
func brokenPlugin(t *testing.T, dir string) Identity {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, BinaryName("boom"))
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'boom stand-in' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := Identify(path)
	if err != nil {
		t.Fatal(err)
	}
	if verr := plugintrust.Add(id.Digest, "boom", id.Path); verr != nil {
		t.Fatal(verr)
	}
	return id
}

func loadBroken(t *testing.T) (*Host, []error) {
	t.Helper()
	h := New()
	t.Cleanup(h.CloseAll)
	return h, h.LoadInto(context.Background(), registry.New())
}

// A trusted plugin that cannot start is reported before every command, and the
// report stopped at what the process wrote: nothing in it said that approving
// an artifact is what makes rta launch it, or what undoes that. It ends with
// the command now, and the plugin is kept for `rta plugin list` and `rta
// doctor`, which listed only what had loaded and so showed an installed,
// approved, silent plugin as if it had never been installed.
func TestAPluginThatCannotStartSaysHowToStopLaunchingIt(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("RTA_SYSTEM_DIR", t.TempDir())
	id := brokenPlugin(t, dir)

	h, problems := loadBroken(t)
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want one", problems)
	}
	var verr *view.Error
	if !errors.As(problems[0], &verr) || verr.Code != "plugin.start" {
		t.Fatalf("problem = %v, want a coded plugin.start", problems[0])
	}
	if !strings.Contains(verr.Message, "exited before its handshake") || !strings.Contains(verr.Message, "boom stand-in") {
		t.Errorf("the account of the launch was lost: %q", verr.Message)
	}
	if !strings.Contains(verr.Hint, "`rta plugin untrust boom`") {
		t.Errorf("hint = %q, want it to name `rta plugin untrust boom`", verr.Hint)
	}

	failed := h.Failed()
	if len(failed) != 1 {
		t.Fatalf("Failed() = %+v, want the one plugin", failed)
	}
	f := failed[0]
	if f.Name != "boom" || f.Path != id.Path || f.Digest != id.Digest || f.Remedy != verr.Hint {
		t.Errorf("failed = %+v", f)
	}
	if strings.HasPrefix(f.Reason, id.Path) || !strings.HasPrefix(f.Reason, "exited before its handshake: exit status 1") ||
		!strings.Contains(f.Reason, "the plugin wrote: boom stand-in") || strings.Contains(f.Reason, "\n") {
		t.Errorf("reason = %q, want one line led by how it ended, without the path", f.Reason)
	}
}

// What takes a plugin out of the way depends on where it lives: approval for
// one on $PATH, the store for one rta installed, and nothing the operator can
// type for one the system root provides — a hint offering a command that
// cannot work is worse than none.
func TestTheWayOutDependsOnWhereThePluginLives(t *testing.T) {
	for _, tc := range []struct {
		name  string
		where func(t *testing.T) string
		want  string
	}{
		{"on PATH", func(t *testing.T) string {
			dir := t.TempDir()
			t.Setenv("PATH", dir)
			return dir
		}, "`rta plugin untrust boom`"},
		{"in the managed store", func(t *testing.T) string {
			t.Setenv("PATH", t.TempDir())
			return ManagedBin()
		}, "`rta plugin remove boom`"},
		{"in the system root", func(t *testing.T) string {
			t.Setenv("PATH", t.TempDir())
			return SystemBin()
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RTA_DATA_DIR", t.TempDir())
			t.Setenv("RTA_SYSTEM_DIR", t.TempDir())
			brokenPlugin(t, tc.where(t))

			h, problems := loadBroken(t)
			if len(problems) != 1 {
				t.Fatalf("problems = %v, want one", problems)
			}
			var verr *view.Error
			if !errors.As(problems[0], &verr) {
				t.Fatalf("problem = %v, want a coded one", problems[0])
			}
			if tc.want == "" && verr.Hint != "" || tc.want != "" && !strings.Contains(verr.Hint, tc.want) {
				t.Errorf("hint = %q, want %q", verr.Hint, tc.want)
			}
			if f := h.Failed(); len(f) != 1 || f[0].Remedy != verr.Hint {
				t.Errorf("Failed() = %+v, want the plugin recorded with the hint", f)
			}
		})
	}
}

// A refusal the machine makes stops every plugin in the same words, and the
// inventory rows say so with that refusal's hint: the row of a plugin that was
// fine said "`rta plugin untrust boom` stops rta launching it" after a TMPDIR
// that was too long, which is the one thing that would not have helped.
func TestAMachineWideRefusalIsRecordedWithItsOwnWayOut(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	brokenPlugin(t, dir)
	long := filepath.Join(t.TempDir(), strings.Repeat("d", maxSocketPath))
	if err := os.MkdirAll(long, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", long)

	h, problems := loadBroken(t)
	var verr *view.Error
	if len(problems) != 1 || !errors.As(problems[0], &verr) || verr.Code != "plugin.tmpdir.toolong" {
		t.Fatalf("problems = %v, want the TMPDIR refusal", problems)
	}
	f := h.Failed()
	if len(f) != 1 || f[0].Remedy != verr.Hint || !strings.Contains(f[0].Remedy, "shorter directory") ||
		strings.Contains(f[0].Remedy, "untrust") {
		t.Errorf("Failed() = %+v, want the plugin recorded with the refusal's own hint", f)
	}
}

// A plugin that loads is not in the list of the ones that did not, and one
// that is simply not approved is the other list's.
func TestOnlyAPluginThatCouldNotStartIsFailed(t *testing.T) {
	trustHello(t)
	dir := t.TempDir()
	installAs(t, dir, "rta-plugin-hello")
	if err := os.WriteFile(filepath.Join(dir, "rta-plugin-unapproved"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	h, problems := loadBroken(t)
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if f := h.Failed(); len(f) != 0 {
		t.Errorf("Failed() = %+v, want none", f)
	}
	if u := h.Untrusted(); len(u) != 1 || u[0].Name != "unapproved" {
		t.Errorf("Untrusted() = %+v", u)
	}
}
