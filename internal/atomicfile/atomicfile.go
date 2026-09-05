// Package atomicfile makes a file appear whole or not at all — a reader never
// sees a half-written one, and a crash never leaves one behind.
//
// Two operations, because rta needs two guarantees. Write replaces contents:
// the path holds the old bytes or the new ones. Publish creates them once:
// the path does not exist, or it holds everything the first writer put there,
// and a second writer is told it lost rather than overwriting.
//
// Five places in rta persist state a user would be upset to lose — the
// note store, the encrypted kv store, the grant file, /etc/hosts and
// friends, and the config file — and four of them had grown their own copy of
// this same twelve lines. The fifth, config, had not: it used os.WriteFile,
// which truncates the target and then writes, and it is on the hottest write
// path in the program. Every `[`, `]` or `H` in the dashboard re-reads the
// config and writes it straight back, so the one file written on a keystroke
// was the one file that could be left truncated.
package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// ReadCapped reads a file rta wrote, refusing one larger than rta writes.
//
// **This is the read half of the same problem Write solves, and it was
// missing for as long as Write has existed.** These files sit under
// paths.Data(), a directory whose threat model internal/consent states
// plainly: "a directory whose whole threat model is that somebody else can
// write there". Write makes rta's own writes whole; nothing made rta's own
// *reads* survive a file rta did not write. An unbounded os.ReadFile on a
// path a lower-trust process can replace is a way to take the operator's
// terminal — or the server that reads this before every gated call — out with
// a single large write, no seal forgery required, because the read happens
// long before anything checks the seal.
//
// io.ReadFull into max+1 rather than a Stat: a Stat is a separate syscall
// from the read, so the file can grow between them, and the check would be
// on a size that is no longer the size. Reading one byte past the limit and
// refusing on that byte cannot be raced — the bytes counted are the bytes
// taken.
//
// The cap belongs to the caller because only the caller knows what its own
// format writes. Size it as "larger than anything rta would ever put here",
// not as "as small as possible": the point is to refuse a file that is
// evidence of tampering, not to police a format that grew a field.
func ReadCapped(path string, max int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, max+1)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	if n > max {
		return nil, fmt.Errorf("%s is larger than anything rta writes there (over %d bytes)", path, max)
	}
	return buf[:n], nil
}

// WriteFrom is Write for a stream: the same temporary-file-then-rename in
// the target's own directory, the same enforced perm, without holding the
// whole payload in memory. A release binary can be a hundred megabytes, and
// buffering one to place it is the wrong shape for a file that is already a
// stream on the way in.
func WriteFrom(path string, r io.Reader, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name())

	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmp.Name(), err)
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return fmt.Errorf("setting permissions on %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// Write replaces path with data, atomically, at exactly perm.
//
// The temporary file is created in the target's own directory rather than in
// TMPDIR, because rename is only atomic within a filesystem — across one it
// degrades to a copy, which is the torn write this exists to prevent.
//
// perm is enforced rather than merely requested: it is applied with Chmod, so
// no umask can widen a file rta declared 0600 or narrow one it declared 0644.
// os.WriteFile could not promise either — its mode argument applies only when
// the file is created, so a rewrite silently keeps whatever mode the path
// already had. A caller that *wants* to keep an existing file's mode stats it
// first and passes what it found, which is worth deciding visibly at the call
// site rather than by default here: /etc/hosts and config.yaml want that,
// grants and the kv store emphatically do not.
//
// The chmod happens before the rename, which costs nothing and closes the
// only window there could be. It is a narrow claim: CreateTemp already makes
// the file 0600, so a caller asking for 0600 was never exposed either way —
// what the ordering guarantees is that a mode is never *widened* on a path
// anything else can already open by name.
func Write(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	// A no-op once the rename succeeds, and the cleanup on every path that
	// does not — including the panic of a caller further up.
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmp.Name(), err)
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return fmt.Errorf("setting permissions on %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// Nothing here calls Sync, and that is measured rather than assumed.
//
// The guarantee on offer is against a *process* dying — the crash that
// actually happens — and rename gives that whole: a reader sees the old file
// or the new one, and a writer that dies leaves its temporary file behind for
// the deferred Remove or, failing that, for the next run. Surviving a power
// loss needs an fsync before the rename, and on macOS os.File.Sync is
// F_FULLFSYNC, which asks the drive to flush its cache: the write above goes
// from ~0.21ms to ~5.3ms, a 26x cost (bench_test.go), on the path a dashboard
// keystroke takes — config.Write on every `[`, `]` and `H` — and on the path
// every gated MCP call takes twice, the grant lock acquired and released. A
// version of this package did sync, until that was measured.
//
// So it is not here, deliberately and at the cost of a power-loss window that
// the five hand-rolled implementations this package replaced all had too. A
// caller whose file is genuinely irreplaceable and rarely written can sync it
// itself; none currently need to.

// Publish creates path holding data, exactly once, and returns whatever ended
// up there: data when this call created the file, the existing contents when
// another writer got there first. It never overwrites.
//
// This is the discipline a key file needs and Write cannot give it. Write is
// last-writer-wins, and a second key silently replacing the first invalidates
// every seal made with the first — so the loser of the race has to be told it
// lost and adopt the winner's key rather than its own.
//
// The mechanism is Link rather than O_EXCL, and the difference is not
// academic: it was caught by a test on its second run. O_EXCL creates the file
// *before* anything is written to it, so a process losing that race opens a
// real, empty file and reads nothing — the "no key here" answer, from a path
// that has a perfectly good key on its way. Link publishes a fully-written
// inode under a name that cannot already exist, so the only two states a
// reader can observe are absent and complete. Not Rename, which would
// cheerfully overwrite the winner and leave two processes disagreeing about
// which key is in force.
//
// Callers validate what comes back. Publish is byte-agnostic and will hand
// back a two-byte file that some earlier, less careful writer left behind.
func Publish(path string, data []byte, perm fs.FileMode) ([]byte, error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("setting permissions on %s: %w", tmp.Name(), err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("writing %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("closing %s: %w", tmp.Name(), err)
	}
	// Retried only while the path is observed absent — see below. Bounded
	// because each retry needs another process to complete an entire
	// create-and-remove inside the two syscalls between this Link and the
	// Read after it; needing that many in a row is not contention, it is
	// something wrong, and spinning on it forever would hide that.
	for range 10 {
		if err := os.Link(tmp.Name(), path); err == nil {
			return data, nil
		}
		// Somebody else got there first, or something was already there —
		// but only a regular file gets read as though it were that: Link
		// only ever creates a regular file at path, so anything else found
		// there (a symlink above all — planted, left over from a restore,
		// or simply a dangling one somebody's dotfile tool cleans up badly)
		// is not a prior publication and must not be treated as one.
		// os.ReadFile follows symlinks, so an unchecked call here would hand
		// back whatever file the symlink resolves to — as though it were
		// this path's own, whole, validated contents — or, for a dangling
		// link, fail with ENOENT and fall into the "go round again" retry
		// below every time, spinning to the same false "gave up" error a
		// genuinely contended publish would give. Lstat, which does not
		// follow, is what tells the two apart before either can happen.
		info, serr := os.Lstat(path)
		if serr != nil {
			if !os.IsNotExist(serr) {
				return nil, fmt.Errorf("checking %s: %w", path, serr)
			}
			// Gone already — same "go round again" as a Read that races the
			// same gap; fall through to the retry below.
		} else if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("publishing %s: refusing a symlink where a published file belongs", path)
		} else if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("publishing %s: refusing a non-regular file where a published file belongs", path)
		} else if existing, rerr := os.ReadFile(path); rerr == nil {
			return existing, nil
		} else if !os.IsNotExist(rerr) {
			return nil, fmt.Errorf("reading %s: %w", path, rerr)
		}
		// It existed for the Link and was gone for the Read. Nothing ended up
		// there, so neither answer is available yet and the postcondition is
		// still reachable: go round again.
		//
		// This is the shape a lock file has and a key file does not. A key is
		// written once and never removed, so its Publish never sees this; the
		// grant lock is created and released constantly, and treating a
		// released lock as a failure to publish made every contended acquire
		// an error instead of a retry.
	}
	return nil, fmt.Errorf("publishing %s: gave up after repeatedly losing and re-losing the race", path)
}
