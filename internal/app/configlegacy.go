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
	old := legacyConfigDir()
	if old == "" {
		return
	}
	var held, both []string
	own := paths.OwnConfigDir()
	for _, name := range configDirFiles {
		if _, err := os.Lstat(filepath.Join(old, name)); err != nil {
			continue
		}
		held = append(held, name)
		if _, err := os.Lstat(filepath.Join(own, name)); err == nil {
			both = append(both, name)
		}
	}
	if len(held) == 0 {
		return
	}
	detail := old + " holds " + strings.Join(held, ", ") + ", and rta reads " + own +
		" now, so nothing in it applies"
	if len(both) > 0 {
		add("old config", "warn", detail+" — "+strings.Join(both, ", ")+" "+
			format.Plural(len(both), "is", "are")+" in both: keep the one you mean in "+own+
			", and remove the other")
		return
	}
	add("old config", "warn", detail+" — `mkdir -p -m 700 "+shellquote.Arg(own)+" && mv -n "+
		shellquote.Arg(old)+"/* "+shellquote.Arg(own)+"/` moves "+format.Plural(len(held), "it", "them"))
}
