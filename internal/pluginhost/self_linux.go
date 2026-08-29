package pluginhost

import "golang.org/x/sys/unix"

// HardenSelf marks the rta process non-dumpable.
//
// Without it, `/proc/<rta-pid>/environ`, `/proc/<rta-pid>/mem` and the maps
// beside them are readable by any process at the same uid, and rta's address
// space is where the interesting things are: the age identity that unlocks
// the store once `kv` has opened it, the grant seal key, the passphrase
// somebody just typed, and any secret a capability is holding on its way to a
// renderer. PR_SET_DUMPABLE=0 hands ownership of those files to root and
// makes the process an invalid ptrace target for a non-privileged peer.
//
// It is set on rta and NOT on plugin processes, and there is nowhere in the
// spawn path to fix that: the dumpable flag is reset by execve, so the only
// place it can be applied to a plugin is inside the plugin, after exec —
// which is `pkg/sdk`'s Serve, cooperatively, and only for plugins that use
// the Go SDK. That is a decision rather than an omission.
//
// This is a floor and not a boundary, in the same sense §4.7.10 already says
// about the age identity: an attacker at this uid can read rta's binary, its
// config, its data directory and the store's ciphertext without touching the
// process at all. What it closes is the cheapest read of the most valuable
// bytes — a shell one-liner against /proc — and it closes core dumps carrying
// secrets to disk, which is worth having on its own.
//
// Failure is ignored. This runs on every rta invocation, and a kernel that
// refuses the prctl is a reason to be slightly less hardened, never a reason
// to refuse to run.
func HardenSelf() {
	_ = unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
}
