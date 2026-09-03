package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The other half of vault.snapshot — the file back into a Vault.
//
// **It refuses MCP for the snapshot's reason run in reverse.** The snapshot
// refuses because everything would leave; a restore is everything arriving —
// the whole storage replaced by what the file holds, mounts and policies and
// tokens included. Neither direction has a blast radius a grant could name,
// so both belong to the person at the keyboard. Destructive besides, because
// there is nothing more destructive in this plugin: unlike a database
// restore, which lands on one database among many, this replaces the world
// the server runs on.
//
// **The fact the receipt must say out loud: the restore replaces the auth
// state too.** Tokens and leases become the snapshot's, which means the very
// token that authorized this restore may stop existing the moment it
// succeeds. That is not a failure — it is what restoring a Vault means — but
// it reads exactly like one to whoever runs it, so the receipt says it
// before they discover it. The read-back afterwards uses seal-status, the
// one endpoint that answers without a token, for precisely this reason.
//
// **--force is Vault's own escape hatch for a snapshot from a different
// cluster**, not a convenience flag: without it Vault verifies the snapshot
// against this cluster's identity and refuses a foreign one, and with it
// that check is skipped — after which the Vault can only be unsealed with
// the *source* cluster's unseal keys or KMS. The refusal for a mismatched
// snapshot names the flag and the key consequence together, because reaching
// for the flag without the keys bricks the Vault as surely as losing them.

func restoreCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:      "vault.restore",
		Summary: "Restore a vault.snapshot file into a Vault, for a person at a terminal",
		// Destructive, because nothing here is more so: the whole storage is
		// replaced, auth state included. The class buys the --yes gate a
		// person should have to type through.
		Safety:     plugin.Destructive,
		Idempotent: false,
		Description: "The other half of vault.snapshot — the file back into a Vault. **Refuses MCP " +
			"outright** for the snapshot's reason run in reverse: the snapshot refuses because " +
			"everything would leave, and a restore is everything arriving — the whole storage " +
			"replaced, mounts, policies, tokens and leases included. Neither direction has a " +
			"blast radius a grant could name, so both belong to the person at the keyboard.\n\n" +
			"**The restore replaces the auth state too**: tokens and leases become the " +
			"snapshot's, so the token that authorized the restore may stop existing the moment " +
			"it succeeds. The receipt says so, and the read-back afterwards uses seal-status — " +
			"the endpoint that answers without a token.\n\n" +
			"A snapshot from a different cluster is refused by Vault itself unless --force " +
			"skips the identity check — after which the Vault can only be unsealed with the " +
			"source cluster's unseal keys or KMS. The refusal names the flag and that " +
			"consequence together, because the flag without the keys bricks the Vault. Needs " +
			"raft (integrated) storage, like the snapshot it restores.",
		Run: runRestoreSnapshot,
	},
		plugin.Field{Name: "file", Type: plugin.Path, Local: true, Positional: true,
			Required: true,
			Help:     "the snapshot to restore — what vault.snapshot wrote"},
		plugin.Field{Name: "force", Type: plugin.Bool,
			Help: "restore a snapshot from a different cluster (skips Vault's identity check; " +
				"unsealing afterwards needs the source cluster's unseal keys or KMS)"})
}

func runRestoreSnapshot(ctx context.Context, req plugin.Request) (view.View, error) {
	if verr := humanOnly(req, "vault.restore",
		"a restore replaces the whole Vault's storage with the file's — a blast radius no "+
			"grant could name, in the direction that overwrites. The snapshot this file came "+
			"from was made by a person at a terminal, and it goes back the same way"); verr != nil {
		return nil, verr
	}

	path, err := expandHome(strings.TrimSpace(req.String("file")))
	if err != nil {
		return nil, view.Errorf("vault.restore.path", "resolving the snapshot path: %v", err)
	}
	if verr := checkSnapshotFile(path); verr != nil {
		return nil, verr
	}

	if req.DryRun {
		what := "verifying it came from this cluster"
		if req.Bool("force") {
			what = "skipping the cluster identity check (--force)"
		}
		return view.Text{Body: fmt.Sprintf(
			"would replace the storage of %s with %s, %s",
			req.String("address"), path, what)}, nil
	}

	return withClient(req, func(client *vaultapi.Client) (view.View, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, view.Errorf("vault.restore.unreadable", "opening %s: %v", path, err)
		}
		defer func() { _ = f.Close() }()

		started := time.Now()
		// Streamed, like the snapshot was: the file's size is the Vault's,
		// and holding it in memory first would put a ceiling on it that has
		// nothing to do with either disk.
		if err := client.Sys().RaftSnapshotRestoreWithContext(ctx, f, req.Bool("force")); err != nil {
			return nil, classifyRestore(err, req)
		}

		return view.KeyValue{Pairs: []view.Pair{
			{Key: "restored", Value: path},
			{Key: "into", Value: req.String("address")},
			{Key: "took", Value: time.Since(started).Round(time.Millisecond).String()},
			// Said here, not left to be discovered as a mysterious 403 on the
			// next call: the auth state is the snapshot's now.
			{Key: "guarantee", Value: "the storage was replaced wholesale — mounts, policies, " +
				"tokens and leases are the snapshot's now, and the token that ran this restore " +
				"may be among what was replaced"},
			{Key: "afterwards", Value: sealStateAfter(ctx, client, req)},
		}}, nil
	})
}

// checkSnapshotFile refuses the two files that would waste a destructive
// call: one that is not there, and one that is empty — the server would
// reject an empty upload anyway, but "the snapshot did not finish being
// written" is the answer, and the server does not know it.
func checkSnapshotFile(path string) *view.Error {
	info, err := os.Stat(path)
	if err != nil {
		return view.Errorf("vault.restore.missing", "no snapshot at %s", path).
			WithHint("`rta vault snapshot --out <path>` writes one; this restores what that wrote")
	}
	if info.IsDir() {
		return view.Errorf("vault.restore.notafile", "%s is a directory", path).
			WithHint("a vault.snapshot file is a single archive")
	}
	if info.Size() == 0 {
		return view.Errorf("vault.restore.empty", "%s is empty", path).
			WithHint("an empty file holds no Vault — if this was a snapshot, it did not finish")
	}
	return nil
}

// sealStateAfter reads what the server says of itself once the restore has
// landed — through seal-status, the endpoint that answers without a token,
// because the token this client holds may have just been replaced along with
// everything else. Best effort: a restore that succeeded is not failed
// retroactively because the read-back could not be made.
func sealStateAfter(ctx context.Context, client *vaultapi.Client, req plugin.Request) string {
	status, err := client.Sys().SealStatusWithContext(ctx)
	if err != nil {
		return "the server could not be read back (" + err.Error() + ") — `rta vault seal status` " +
			"is the next thing to run"
	}
	if status.Sealed {
		return "the Vault is sealed — unseal it with the keys of the cluster the snapshot came from"
	}
	name := status.ClusterName
	if name == "" {
		name = req.String("address")
	}
	return fmt.Sprintf("%s is unsealed and serving the snapshot's data", name)
}

// classifyRestore names the failures particular to this endpoint before
// falling back to the shared classifier.
func classifyRestore(err error, req plugin.Request) *view.Error {
	var respErr *vaultapi.ResponseError
	if errors.As(err, &respErr) {
		switch {
		case respErr.StatusCode == 404:
			return view.Errorf("vault.restore.unsupported",
				"%s does not offer raft snapshot restore", req.String("address")).
				WithHint("the endpoint exists only on integrated (raft) storage — with Consul or " +
					"another backend, restore that system's own backup instead")
		// **The refusal that protects the unseal keys.** Vault verifies the
		// snapshot against this cluster's identity and answers 400 when they
		// do not match — most often "could not verify hash file", because the
		// checksums were sealed by a different cluster's keys.
		case respErr.StatusCode == 400 && containsAny(respErr.Error(),
			"could not verify", "unseal key", "hash file"):
			return view.Errorf("vault.restore.mismatch",
				"this snapshot did not come from the cluster at %s", req.String("address")).
				WithHint("--force skips the identity check and restores it anyway — do that only " +
					"holding the source cluster's unseal keys or KMS, because they are what " +
					"unseals the Vault afterwards. Without them, --force bricks it")
		}
	}
	return classify(err, req)
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
