package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/this-is-tobi/rta/internal/paths"
	"github.com/this-is-tobi/rta/internal/shellquote"
	"github.com/this-is-tobi/rta/pkg/format"
)

// configDirFiles is every file rta keeps in its config directory, which is
// what a move from one directory to another has to carry.
var configDirFiles = []string{"config.yaml", "policy.yaml", "remotes.yaml", "kv.identity"}

// legacyConfigDir is paths.LegacyConfigDir, overridable so a test can be a
// macOS machine on any host.
var legacyConfigDir = paths.LegacyConfigDir

// doctorLegacyConfig names what is left in the directory earlier builds kept
// the configuration in.
//
// **A diagnostic, not a compatibility shim.** The configuration moved to the
// directory the data already lived beside (paths.OwnConfigDir), nothing reads
// the old one, and nothing carries a file across. What it must not be is
// silent: a config.yaml that stops applying after an upgrade leaves every
// command succeeding, so the only evidence is behaviour that quietly differs
// from what the file says, and the kv.identity that stays behind is the key
// the store was made with.
//
// The move is handed over as the command that makes it, with `-n` so a file
// already in the new directory is never replaced by an older one; when a name
// is in both, it says so instead, since which of the two is right is the one
// thing only the person knows.
func doctorLegacyConfig(add func(check, status, detail string)) {
	left := findLegacyConfig()
	if left == nil {
		return
	}
	detail := left.old + " holds " + strings.Join(left.held, ", ") + ", and rta reads " + left.own +
		" now, so nothing in it applies"
	if len(left.both) > 0 {
		add("old config", "warn", detail+" — "+strings.Join(left.both, ", ")+" "+
			format.Plural(len(left.both), "is", "are")+" in both: keep the one you mean in "+left.own+
			", and remove the other")
		return
	}
	add("old config", "warn", detail+" — `mkdir -p -m 700 "+shellquote.Arg(left.own)+" && mv -n "+
		shellquote.Arg(left.old)+"/* "+shellquote.Arg(left.own)+"/` moves "+format.Plural(len(left.held), "it", "them"))
}

// legacyConfig is what the directory earlier builds kept the configuration in
// still holds.
type legacyConfig struct {
	old, own   string
	held, both []string
}

// findLegacyConfig is nil when nothing of rta's is left in the old directory.
func findLegacyConfig() *legacyConfig {
	old := legacyConfigDir()
	if old == "" {
		return nil
	}
	left := &legacyConfig{old: old, own: paths.OwnConfigDir()}
	for _, name := range configDirFiles {
		if _, err := os.Lstat(filepath.Join(old, name)); err != nil {
			continue
		}
		left.held = append(left.held, name)
		if _, err := os.Lstat(filepath.Join(left.own, name)); err == nil {
			left.both = append(left.both, name)
		}
	}
	if len(left.held) == 0 {
		return nil
	}
	return left
}
