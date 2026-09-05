package pluginhost

import (
	"crypto/hmac"
	"crypto/sha256"
	"os"
	"path/filepath"
	"sort"

	"google.golang.org/protobuf/proto"

	"github.com/this-is-tobi/rta/internal/paths"
	rtav1 "github.com/this-is-tobi/rta/proto/rta/v1"
)

// A plugin's declaration is cached on disk so that rta does not launch every
// installed plugin on every invocation.
//
// Measured before this existed, on an 18 MB plugin: 42 ms per plugin per
// invocation, of which 8.7 ms is hashing the binary and the rest is fork,
// exec, sandbox-exec, the handshake, mTLS and one Describe. That is tolerable
// at one plugin and it is not the number that matters — shell completion runs
// rta on every press of the tab key, so ten installed plugins would put
// roughly half a second between the key and the suggestions, which reads as a
// broken shell rather than a slow one.
//
// **Keyed by the content digest, and sealed.** The two do different jobs and
// the cache needs both.
//
// The digest makes it *fresh*: a hit means the bytes are identical to the
// ones that produced the entry, so there is no staleness question to get
// wrong — no mtime to forge, no size to collide, no invalidation rule to be
// subtly incorrect. It costs the 8.7 ms hash, which has to happen anyway for
// an authorisation to be attached to an artifact rather than a name.
//
// The seal makes it *authentic*, and without it the digest is worth nothing.
// A cache entry is an assertion about a binary made without the binary, so an
// unsealed one lets anything that can write this directory change what rta
// believes a plugin declares while its digest stays exactly as it was — which
// is the one thing hashing the binary was supposed to prevent. cacheseal.go
// has the reproduction and the bound.
//
// The process is still launched, just not until something actually calls the
// plugin. That works because Client.live already had to handle a process that
// is not there — it was written for the crashed-plugin case — so "never
// started" and "died" are one path rather than two.

const cacheDir = "plugin-cache"

// cacheEntries bounds the directory. Entries are a few kilobytes and one
// accumulates per plugin *version* ever seen, so this is housekeeping rather
// than a limit anybody reaches: 128 is more distinct plugin builds than a
// machine plausibly has, and pruning the oldest keeps a developer rebuilding
// a plugin in a loop from growing the directory without bound.
const cacheEntries = 128

func cachePath(digest string) string {
	return filepath.Join(paths.Data(), cacheDir, digest+".pb")
}

// readCache returns the declaration recorded for this digest.
//
// Every failure is a miss — unreadable, corrupt, truncated, or carrying a
// seal this rta cannot verify. That policy predates the seal and is what
// makes adding one cheap: a rejected entry costs a process launch and
// produces the identical answer, so there is no case where refusing an entry
// is worse than trusting it.
//
// A failed seal is therefore silent and self-healing: the launch that follows
// overwrites the entry with a sealed one. It is deliberately not reported,
// because rta cannot tell a tampered entry from a damaged one, and the
// surfaces that would carry the warning are the ones a person reads for
// something they can act on.
func readCache(digest string) (*rtav1.Plugin, bool) {
	data, err := os.ReadFile(cachePath(digest))
	if err != nil || len(data) < sha256.Size {
		return nil, false
	}
	key := sealKey(false)
	if key == nil {
		// An entry exists with no key beside it, so it was not written by
		// this rta. Nothing is honoured and nothing is reported: unlike a
		// grant file, the fallback is to ask the process.
		return nil, false
	}
	mac, body := data[:sha256.Size], data[sha256.Size:]
	if !hmac.Equal(mac, sealFor(key, digest, body)) {
		return nil, false
	}
	var p rtav1.Plugin
	if err := proto.Unmarshal(body, &p); err != nil {
		return nil, false
	}
	if p.GetName() == "" {
		// Unmarshal accepts empty input as an empty message, which would
		// register a nameless plugin with no capabilities rather than miss.
		return nil, false
	}
	return &p, true
}

// writeCache records a declaration. Failures are ignored: a cache that cannot
// be written is a slower rta, not a broken one, and a read-only data
// directory is a legitimate way to run.
func writeCache(digest string, p *rtav1.Plugin) {
	data, err := proto.Marshal(p)
	if err != nil {
		return
	}
	key := sealKey(true)
	if key == nil {
		// No key, no entry. Writing an unsealed one would be writing
		// something readCache is now guaranteed to reject.
		return
	}
	dir := filepath.Join(paths.Data(), cacheDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	// Written to a temp file and renamed, so a reader never sees a partial
	// entry. Without it a concurrent rta could read half a declaration and,
	// because a truncated proto often unmarshals cleanly into a shorter
	// message, register a plugin with some of its capabilities missing.
	tmp, err := os.CreateTemp(dir, ".decl-*.tmp")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name()) // no-op once the rename succeeds
	if _, err := tmp.Write(append(sealFor(key, digest, data), data...)); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp.Name(), cachePath(digest)); err != nil {
		return
	}
	pruneCache(dir)
}

// pruneCache drops the oldest entries once the directory grows past the cap.
func pruneCache(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= cacheEntries {
		return
	}
	type aged struct {
		name string
		mod  int64
	}
	files := make([]aged, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, aged{e.Name(), info.ModTime().UnixNano()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod < files[j].mod })
	for i := 0; i < len(files)-cacheEntries; i++ {
		_ = os.Remove(filepath.Join(dir, files[i].name))
	}
}
