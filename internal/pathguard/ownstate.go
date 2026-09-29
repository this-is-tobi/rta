package pathguard

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// rta's own state is refused by name (check, refuser), and a name is not
// what an open reads. Two things a caller who can write inside a root can
// do leave every name it gives outside the data directory and still open a
// file inside it:
//
//   - a hard link, made anywhere under the root to grants.key, is another
//     name for the same file: judged by name it is a note in a project, and
//     fs.hash, a walk's listing and git.diff read the seal key through it;
//   - a rename of the file itself onto a name already judged, between the
//     judging and the open, puts the key where a harmless file was, and no
//     link is ever made for the open to refuse.
//
// So what an opener opened, and what a walk reaches, is also compared by
// identity — the device and number a file keeps under every name, which is
// what os.SameFile compares — with everything the denied paths hold, and
// refused as a path naming it is. The name rule stays in front, for the
// refusal that says which path it was, without a walk of anything.
//
// The set is read once per call, when the call first asks (ownState), and
// never per file: a walk that asks at every entry may reach a million of
// them. What a call reads of it (now) is compared by identity alone: nothing
// but a second name for one of those files, or the file itself, can have that
// identity while it is there.
//
// The plugin index clones are left out of the set, and only out of it: the
// name rule refuses them as it refuses the rest of the data directory. `rta
// plugin index add` of a checkout on this machine is a git clone of a local
// repository, which git makes by hard-linking the repository's object files,
// so each of them is a file of the operator's own repository as well — and a
// set that held them refused that repository as rta's state, git.log and
// git.status of it and fs.hash of its objects, under a root the operator had
// drawn around it. What a clone holds is public content rta can fetch again,
// which nothing gains by reading through a second name, and it was the bulk
// of the set: some 1,500 of its entries on a machine that had used the index
// for a while, read at every call's first ask. Nothing else of the operator's
// reaches the data directory under its own identity: a plugin is installed
// by copying it into the store, and the links rta makes there — a file
// published in place (internal/atomicfile), a stale lock examined before it
// is broken (internal/filelock) — are of its own files.
//
// A call's own reading misses a file moved out of the directory before the
// call looked, which is what a rename that holds the key at the judged name
// for the length of a call does. So the guard reads the set once more, when
// it is made (seen), before any caller has done anything, and a file that
// set holds is refused too — only while it is unchanged since, its size and
// modification time as they were. A filesystem that numbers files again once
// they are deleted (ext4, XFS) gives the number of a file rta has since
// replaced, a grant file it rewrote, to whatever file is made next, which may
// be under a root, and refusing that one as rta's own state would refuse a
// build's output for as long as the server ran. A rename changes neither the
// size nor the time.
//
// The directories the denied paths name are the exception, and count by
// identity alone (stamp.kept): rta never gives one up while it serves, so
// their numbers are never handed on, and the stamp would let the whole data
// directory through. Moved under a root by another name — one rename, by a
// caller who can write in the directory holding it — it held the ledger,
// the grant records and every other file rta had written since the server
// started, each of them changed since the guard's reading and none of them
// in the call's own, which looks where the directory was. Refused itself,
// nothing is opened through it: every open goes down each directory on its
// way (pathin), and git lists nothing of it (builtin/git's boundDir).
//
// What is not covered, said plainly, is a file that has left the directories
// by the time a call reads them and was not in them, as it is now, when the
// server started. One is a file moved out on its own, or with its directory
// by a caller who also holds a hard link to it, once it was made or written
// to after the server started: that takes write access inside rta's own
// directory, where anything can be done to its state anyway. The other takes
// only a hard link: a file rta rewrites whole — the grant records, the
// ledger's head — leaves its last version behind at the link, and a version
// made after the server started is in no reading. The keys rta seals with
// are made once and never rewritten, so neither reaches them unless they are
// moved. A number that counted for good would close both and refuse a
// build's output instead; what would tell the two apart is a file's birth
// time, which Linux reports only through statx, never in the FileInfo an
// opener or a walk has to hand.

// idSet is the identities of a set of files, with what each was when the set
// was read.
type idSet struct {
	ids map[fileID]stamp
	// other is every file whose identity the platform does not name,
	// compared one by one with os.SameFile: all of them, on Windows.
	other []seenFile
}

type seenFile struct {
	info fs.FileInfo
	kept bool
}

// stamp is what a file was when a set was read: what a rename leaves as it
// was, and writing to it does not. kept is a directory a denied path names,
// which counts by identity alone (the package's reasons, above).
type stamp struct {
	size int64
	mod  time.Time
	kept bool
}

func stampOf(info fs.FileInfo, kept bool) stamp { return stamp{info.Size(), info.ModTime(), kept} }

func (s *idSet) add(info fs.FileInfo, kept bool) {
	if id, ok := identity(info); ok {
		if s.ids == nil {
			s.ids = make(map[fileID]stamp)
		}
		s.ids[id] = stampOf(info, kept)
		return
	}
	// os.SameFile reads a Windows file's identity from its name the first
	// time it is asked and keeps it, so it is asked now: the identity is then
	// that of what the name held while the set was read, not of whatever it
	// holds when an open is compared with it later.
	_ = os.SameFile(info, info)
	s.other = append(s.other, seenFile{info, kept})
}

// find is what the set saw of the file info describes, and whether it saw
// it at all.
func (s *idSet) find(info fs.FileInfo) (stamp, bool) {
	if id, ok := identity(info); ok {
		if was, ok := s.ids[id]; ok {
			return was, true
		}
	}
	for _, seen := range s.other {
		if os.SameFile(seen.info, info) {
			return stampOf(seen.info, seen.kept), true
		}
	}
	return stamp{}, false
}

// readState is the identity of everything under each of denied, the
// directories themselves among it, links as themselves — a link is a file a
// hard link can name as well — but for what the directories public name hold,
// and those directories themselves (the package's reasons, above).
func readState(denied, public []string) *idSet {
	s := &idSet{}
	for _, d := range denied {
		_ = filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				// A denied path that is not there holds nothing, and a
				// directory this process cannot list holds nothing it can
				// open either: the rest is read on.
				return nil //nolint:nilerr // see above
			}
			if e.IsDir() && slices.Contains(public, p) {
				return filepath.SkipDir
			}
			if info, err := e.Info(); err == nil {
				s.add(info, p == d && info.IsDir())
			}
			return nil
		})
	}
	return s
}

// ownState is rta's own state as one call knows it: the set the guard read
// when it was made, and the call's own reading of it, taken the first time
// the call asks.
type ownState struct {
	denied, public []string
	seen           *idSet
	once           sync.Once
	now            *idSet
}

// holds reports whether info describes a file of rta's own state, by
// identity (the package's reasons, above).
func (s *ownState) holds(info fs.FileInfo) bool {
	s.once.Do(func() { s.now = readState(s.denied, s.public) })
	if _, ok := s.now.find(info); ok {
		return true
	}
	was, ok := s.seen.find(info)
	return ok && (was.kept || was.size == info.Size() && was.mod.Equal(info.ModTime()))
}
