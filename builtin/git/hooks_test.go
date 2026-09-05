package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/builtin/internal/gitclone"
)

func writeHook(t *testing.T, dir, name string, executable bool) {
	t.Helper()
	hooksDir := filepath.Join(dir, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o644)
	if executable {
		mode = 0o755
	}
	if err := os.WriteFile(filepath.Join(hooksDir, name), []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
}

// PlainInit writes no hook templates at all (unlike the real git binary,
// which drops in *.sample files for every hook git knows about) — this
// fixture builds all three states by hand rather than relying on any of
// them pre-existing.
func TestHooksClassifiesActiveSampleAndDisabled(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeHook(t, dir, "pre-commit", true)
	writeHook(t, dir, "pre-push.sample", false)
	writeHook(t, dir, "commit-msg", false)

	tbl := table(t, runHooks, req(t, dir, nil))
	if got := rowFor(t, tbl, "Name", "pre-commit")[1]; got != "active" {
		t.Errorf("pre-commit status = %q, want active", got)
	}
	if got := rowFor(t, tbl, "Name", "pre-push")[1]; got != "sample" {
		t.Errorf("pre-push status = %q, want sample — the .sample suffix must be stripped from Name", got)
	}
	if got := rowFor(t, tbl, "Name", "commit-msg")[1]; got != "disabled" {
		t.Errorf("commit-msg status = %q, want disabled — present but not executable, so git skips it", got)
	}
	if tbl.Total != len(tbl.Rows) {
		t.Errorf("Total = %d, want %d", tbl.Total, len(tbl.Rows))
	}
}

func TestHooksOnARepositoryWithNoHooksDirectoryReturnsEmptyNotAnError(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")

	tbl := table(t, runHooks, req(t, dir, nil))
	if len(tbl.Rows) != 0 {
		t.Errorf("rows = %v, want none — PlainInit never creates a hooks directory", tbl.Rows)
	}
}

func TestHooksFilesystemRefusesAMemoryBackedClone(t *testing.T) {
	bareDir := bareRepo(t)
	repo, verr := gitclone.InMemory(context.Background(), bareDir, gitclone.Options{})
	if verr != nil {
		t.Fatal(verr)
	}

	_, verr = hooksFilesystem(repo)
	if verr == nil {
		t.Fatal("expected an error — a memory-backed clone has no on-disk hooks directory")
	}
	if verr.Code != "git.hooks.unavailable" {
		t.Errorf("code = %q, want git.hooks.unavailable", verr.Code)
	}
}

func TestHooksOnANonRepositoryFailsWithAClearError(t *testing.T) {
	dir := t.TempDir()
	_, err := runHooks(context.Background(), req(t, dir, nil))
	if err == nil {
		t.Fatal("expected an error opening a non-repository")
	}
}
