package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The Vault backup, and the two decisions that shape it.
//
// **It takes a raft snapshot rather than exporting the KV secrets**, and
// that is the same call pg.dump makes when it shells out to pg_dump instead
// of writing its own SQL. A Vault is not its secrets: it is mounts, auth
// methods, policies, entities, leases, tokens and the transit keys nothing
// can re-derive. Walking the KV tree and writing what comes back produces a
// file that looks like a backup, restores none of that, and is discovered to
// be worthless on the day it is needed — a backup you cannot restore is not
// a backup, it is a belief about one. The snapshot endpoint is Vault's own
// answer to this question and it is the one that round-trips.
//
// **And the snapshot is encrypted by the barrier, which the export would not
// have been.** What lands on disk is Vault's storage as Vault stores it,
// still sealed: restoring it needs the same unseal keys or the same KMS. So
// the artifact this writes is categorically safer than the one somebody
// reaches for when this capability does not exist — `vault kv get` in a
// shell loop, redirected to a file, in the clear. That is worth having as
// the easy path.
//
// It refuses MCP outright for the reason pg.dump does: a whole-Vault
// snapshot has no blast radius a grant could name, so a grant covering it
// would be a rubber stamp with an expiry date rather than consent.
// NeedsGrant stays unset deliberately — keys.backup's rule, that a grant
// which can never be exercised is an entry in `grant list` meaning nothing.

func snapshotCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:      "vault.snapshot",
		Summary: "Write a raft storage snapshot, for a person at a terminal",
		Safety:  plugin.Write,
		// Running it twice at the same --out refuses rather than overwriting.
		Idempotent: false,
		Description: "Vault's own backup: the whole storage, as Vault stores it. **Refuses MCP " +
			"outright** rather than asking for a grant, the line keys.backup and kv.copy draw — a " +
			"snapshot of everything has no blast radius a grant could name, and an agent that " +
			"needs a secret asks for vault.kv.get with a grant naming that path.\n\n" +
			"A snapshot rather than a KV export, and the difference is whether it restores. A " +
			"Vault is not its secrets: it is mounts, auth methods, policies, entities, leases and " +
			"transit keys that nothing can re-derive. Walking the KV tree and writing what comes " +
			"back produces a file that looks like a backup and restores none of that.\n\n" +
			"It is also the safer artifact. What lands on disk is still sealed — restoring needs " +
			"the same unseal keys or the same KMS — where an export would be every secret in the " +
			"clear. Needs raft (integrated) storage and a token allowed to read " +
			"sys/storage/raft/snapshot; with any other storage backend Vault has no such endpoint " +
			"and this says so. Written with O_EXCL at mode 0600, never over an existing file, and " +
			"a run that fails takes its partial file with it.",
		Run: runSnapshot,
	},
		plugin.Field{Name: "out", Type: plugin.Path, Local: true,
			Help: "file to write the snapshot to; refused if it already exists"})
}

// humanOnly is this plugin's copy of the gate builtin/keys opens with, and
// pg.dump repeats. It comes first, before a client is built, so an agent's
// call never spends the operator's token on a question that was always going
// to be answered no.
func humanOnly(req plugin.Request, id string) *view.Error {
	if req.Surface() != plugin.SurfaceMCP {
		return nil
	}
	return view.Refusef("vault.human", "%s can only be run by a person at a terminal", id).
		WithHint("a snapshot of the whole Vault has no blast radius a grant could name — its " +
			"one authorized use is everything. Ask for the path you need with vault.kv.get, " +
			"which takes a grant naming that path")
}

func runSnapshot(ctx context.Context, req plugin.Request) (view.View, error) {
	if verr := humanOnly(req, "vault.snapshot"); verr != nil {
		return nil, verr
	}

	out := strings.TrimSpace(req.String("out"))
	if out == "" {
		return nil, view.Errorf("vault.snapshot.nooutput", "say where the snapshot should be written").
			WithHint("--out ./vault.snap — a whole Vault is a file, not something to read in a " +
				"terminal")
	}
	path, err := expandHome(out)
	if err != nil {
		return nil, view.Errorf("vault.snapshot.path", "resolving --out: %v", err)
	}
	// A friendly early refusal before anything opens a connection. It is not
	// the guarantee — O_EXCL below is, and it still catches the race this
	// cannot.
	if _, err := os.Stat(path); err == nil {
		return nil, snapshotExists(path)
	}

	if req.DryRun {
		return view.Text{Body: "would write a raft snapshot of " +
			req.String("address") + " to " + path}, nil
	}

	return withClient(req, func(client *vaultapi.Client) (view.View, error) {
		// O_EXCL is the no-overwrite guarantee in one syscall, and 0600 is set
		// at creation rather than chmod'd after, so there is no instant where
		// the file is both complete and readable by everyone. Sealed or not,
		// this is the whole Vault.
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			return nil, snapshotExists(path)
		}
		if err != nil {
			return nil, view.Errorf("vault.snapshot.create", "creating %s: %v", path, err)
		}

		started := time.Now()
		// Streamed into the file rather than buffered: this is the one thing
		// here with no bound, on purpose, and holding a Vault's storage in
		// memory first would put a ceiling on it that has nothing to do with
		// the disk being written to.
		//
		// **The client verifies as it streams, and the removal below is what
		// makes that verification worth anything.** It tees the body past a
		// reader looking for a non-empty SHA256SUMS.sealed — the last entry
		// in the archive, and the one a seal failure midstream leaves out —
		// but it checks *after* copying, so by the time it reports
		// ErrIncompleteSnapshot the bytes are already in this file. That file
		// is a well-formed-looking archive that cannot be restored, which is
		// the single worst artifact this capability could produce, so it goes.
		snapErr := client.Sys().RaftSnapshotWithContext(ctx, f)
		closeErr := f.Close()
		if snapErr != nil {
			_ = os.Remove(path)
			return nil, classifySnapshot(snapErr, req)
		}
		if closeErr != nil {
			_ = os.Remove(path)
			return nil, view.Errorf("vault.snapshot.write", "finishing %s: %v", path, closeErr)
		}

		var size int64
		if info, err := os.Stat(path); err == nil {
			size = info.Size()
		}

		return view.KeyValue{Pairs: []view.Pair{
			{Key: "wrote", Value: path},
			{Key: "size", Value: format.Bytes(uint64(size))},
			{Key: "took", Value: time.Since(started).Round(time.Millisecond).String()},
			{Key: "from", Value: req.String("address")},
			// The property that makes this artifact different from an export,
			// said where somebody is looking at the file they just made.
			{Key: "at rest", Value: "sealed by the barrier — restoring needs the same unseal " +
				"keys or KMS, mode 0600"},
			{Key: "restore with", Value: "vault operator raft snapshot restore " + path},
		}}, nil
	})
}

func snapshotExists(path string) *view.Error {
	return view.Errorf("vault.snapshot.exists", "%s already exists", path).
		WithHint("a snapshot is never written over an existing file — name a new one, or move " +
			"that one aside")
}

// classifySnapshot names the failures particular to this endpoint before
// falling back to the shared classifier.
//
// The one worth catching is storage: `sys/storage/raft/snapshot` exists only
// on a Vault using integrated storage, so on a Consul-backed or file-backed
// Vault it 404s — and "not found" against a URL nobody typed is a message
// that sends somebody looking for a path problem.
func classifySnapshot(err error, req plugin.Request) *view.Error {
	// **The failure that produces a file rather than an error**, if nobody
	// deletes it. Vault streams the archive and writes SHA256SUMS.sealed
	// last, encrypted by the seal; when the seal is unavailable partway
	// through — a KMS that stopped answering, an HSM that went away — the
	// archive arrives complete-looking and missing exactly that entry. The
	// client catches it, and it catches it after the bytes have been written,
	// so what makes this safe is that the partial file has already been
	// removed by the time this runs.
	if errors.Is(err, vaultapi.ErrIncompleteSnapshot) {
		return view.Errorf("vault.snapshot.incomplete",
			"%s returned a snapshot that stops short of its checksums", req.String("address")).
			WithHint("the archive is missing SHA256SUMS.sealed, which Vault writes last and " +
				"encrypts with the seal — so the seal was unavailable partway through. " +
				"`rta vault seal status` is the next thing to look at. The partial file has " +
				"been removed rather than left looking like a backup")
	}

	var respErr *vaultapi.ResponseError
	if errors.As(err, &respErr) && respErr.StatusCode == 404 {
		return view.Errorf("vault.snapshot.unsupported",
			"%s does not offer raft snapshots", req.String("address")).
			WithHint("the endpoint exists only on integrated (raft) storage — with Consul or " +
				"another backend, back up that system instead. rta will not substitute a KV " +
				"export: it would restore none of the mounts, policies, leases or transit keys")
	}
	return classify(err, req)
}

// expandHome resolves a leading ~ the way every other consumer of a Path
// input in this codebase does. Mirrors builtin/kv, builtin/keys, plugins/s3
// and plugins/pg's own copies rather than centralizing a shared helper: an
// external plugin cannot reach internal/pathguard, and the rule is ten lines.
func expandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return filepath.Abs(p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}
