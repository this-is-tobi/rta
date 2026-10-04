package pathguard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// stateUnder makes rta's data directory at data, holding grants.key, with
// its configuration out of the way, and returns the key's path.
func stateUnder(t testing.TB, data string) string {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", data)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(data, "grants.key")
	if err := os.WriteFile(key, []byte("seal"), 0o600); err != nil {
		t.Fatal(err)
	}
	return key
}

func lstat(t testing.TB, path string) fs.FileInfo {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// Bounds' Refuse knows rta's state by identity as well as by name: a hard
// link under a root to grants.key, made before the guard or after it, is
// refused as the key's own name is, in the name rule's words, and a file
// beside it is not.
func TestRefuseKnowsRtasStateByIdentity(t *testing.T) {
	root := t.TempDir()
	key := stateUnder(t, filepath.Join(t.TempDir(), "data"))
	early, late, note := filepath.Join(root, "early"), filepath.Join(root, "late"), filepath.Join(root, "note")
	if err := os.Link(key, early); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	g, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(key, late); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(note, []byte("seal"), 0o600); err != nil {
		t.Fatal(err)
	}
	refuse := g.Bounds().Refuse
	for _, p := range []string{early, late} {
		var verr *view.Error
		if err := refuse(p, lstat(t, p)); !errors.As(err, &verr) || verr.Code != "core.mcp.path.protected" {
			t.Errorf("%s, a hard link to grants.key: %v, want it refused as rta's own state", filepath.Base(p), err)
		}
	}
	if err := refuse(note, lstat(t, note)); err != nil {
		t.Errorf("a file beside them, holding the same bytes, was refused: %v", err)
	}
}

// A plugin index attached from a checkout on this machine is a git clone of
// it, and git clones a local repository by hard-linking its objects: every
// object file of the operator's repository is then also a file under rta's
// data directory. The clones are public content rta can fetch again, and
// they leave the identity set: the operator's repository, served under a
// root, is theirs to read, while a hard link to grants.key, the ledger or the
// kv store is still refused as the file itself is. The clones stay refused
// by name.
func TestALocalIndexCloneLeavesItsSourceOutOfRtasState(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(t.TempDir(), "data")
	key := stateUnder(t, data)
	object := filepath.Join(root, "repo", ".git", "objects", "ab", "cdef")
	clone := filepath.Join(data, "indexes", "local", ".git", "objects", "ab", "cdef")
	for _, dir := range []string{filepath.Dir(object), filepath.Dir(clone)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(object, []byte("blob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(object, clone); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
	own := []string{key}
	for _, name := range []string{"agent-log.jsonl", "kv.age"} {
		p := filepath.Join(data, name)
		if err := os.WriteFile(p, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		own = append(own, p)
	}
	links := make([]string, len(own))
	for i, p := range own {
		links[i] = filepath.Join(root, "link-to-"+filepath.Base(p))
		if err := os.Link(p, links[i]); err != nil {
			t.Fatal(err)
		}
	}
	g, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	refuse := g.Bounds().Refuse
	if err := refuse(object, lstat(t, object)); err != nil {
		t.Errorf("an object of the operator's repository, which a local index clone links to, was refused: %v", err)
	}
	for _, p := range links {
		var verr *view.Error
		if err := refuse(p, lstat(t, p)); !errors.As(err, &verr) || verr.Code != "core.mcp.path.protected" {
			t.Errorf("%s, a hard link under a root: %v, want it refused as rta's own state", filepath.Base(p), err)
		}
	}
	// As a walk reaches it: through the directories as they are, which the
	// guard resolved its denied paths through.
	clone, err = filepath.EvalSymlinks(clone)
	if err != nil {
		t.Fatal(err)
	}
	var verr *view.Error
	if err := refuse(clone, lstat(t, clone)); !errors.As(err, &verr) || verr.Code != "core.mcp.path.protected" {
		t.Errorf("the index clone's own name: %v, want it refused by name", err)
	}
}

// renumbered is a FileInfo for a file that has another file's number: what a
// filesystem that numbers files again (ext4, XFS) gives the next file made
// after rta replaced one of its own.
type renumbered struct {
	fs.FileInfo
	mod time.Time
}

func (r renumbered) ModTime() time.Time { return r.mod }

// The set the guard reads when it is made holds files rta replaces while it
// serves — a grant file it rewrites — and a filesystem that numbers files
// again gives the old number to the next file made, which may be under a
// root. That file is not rta's state, and is not refused as it: a number the
// guard saw only when it was made counts while the file it saw is unchanged,
// which a file moved out of the directory is, and a new one is not.
func TestANumberRtaHasSinceGivenUpIsNotItsState(t *testing.T) {
	root := t.TempDir()
	stateUnder(t, filepath.Join(t.TempDir(), "data"))
	out := filepath.Join(root, "build.out")
	if err := os.WriteFile(out, []byte("a build's output"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := lstat(t, out)
	then := renumbered{FileInfo: now, mod: now.ModTime().Add(-time.Hour)}
	own := &ownState{seen: &idSet{}}
	own.seen.add(then, false)
	if own.holds(now) {
		t.Error("a file with a number rta's state had when the guard was made was refused as rta's state")
	}
	own = &ownState{seen: &idSet{}}
	own.seen.add(now, false)
	if !own.holds(now) {
		t.Error("a file the guard saw in rta's state, unchanged since, was not refused")
	}
}

// What one call's reading of rta's state costs, taken the first time the
// call asks, on a data directory the size of one that has held the plugin
// index for a while: some 1,500 entries in 330 directories of index clone, as
// the machine this was written on had, which the reading passes over, beside
// a couple of hundred files of rta's own in a dozen directories — plugin
// store and cache, record segments, sessions, consent — which it reads.
func BenchmarkACallsReadingOfRtasState(b *testing.B) {
	root := b.TempDir()
	data := filepath.Join(b.TempDir(), "data")
	stateUnder(b, data)
	fill := func(under string, dirs, files int) {
		for f := range files {
			dir := filepath.Join(data, under, fmt.Sprint(f%dirs))
			if err := os.MkdirAll(dir, 0o700); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprint(f)), nil, 0o600); err != nil {
				b.Fatal(err)
			}
		}
	}
	fill("indexes", 330, 1186)
	fill("state", 12, 200)
	note := filepath.Join(root, "note")
	if err := os.WriteFile(note, nil, 0o600); err != nil {
		b.Fatal(err)
	}
	g, err := New(root)
	if err != nil {
		b.Fatal(err)
	}
	info := lstat(b, note)
	b.Run("first ask", func(b *testing.B) {
		for b.Loop() {
			if err := g.Bounds().Refuse(note, info); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("every ask after", func(b *testing.B) {
		refuse := g.Bounds().Refuse
		_ = refuse(note, info)
		for b.Loop() {
			if err := refuse(note, info); err != nil {
				b.Fatal(err)
			}
		}
	})
}
