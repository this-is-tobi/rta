package pluginhost

import "io/fs"

// ExeSuffix is what this platform puts on the end of a program's filename.
//
// Windows has no execute bit. What makes a file a program there is its
// extension appearing in PATHEXT, and `.exe` is the only one a Go build ever
// produces — GoReleaser appends it too, so a published Windows archive holds
// `rta-plugin-pg.exe` and nothing else would run if it did not.
//
// So the suffix is not decoration. It is the executability test, which is why
// Namespace matches on it and why runnable below has nothing left to ask.
const ExeSuffix = ".exe"

// runnable — see ExeSuffix; matching the name has already answered this.
//
// The Unix test (`Mode()&0o111 != 0`) is not merely unhelpful here, it is
// wrong for every file: Go derives a Windows FileMode from the read-only
// attribute, so an ordinary file is 0666 and no file ever carries an execute
// bit. Discovery applied that test on every platform and therefore found no
// plugin on Windows at all — not "installs were broken", nothing was ever
// loaded.
func runnable(fs.FileInfo) bool { return true }
