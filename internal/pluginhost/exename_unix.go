//go:build !windows

package pluginhost

import "io/fs"

// ExeSuffix is what this platform puts on the end of a program's filename.
// Nothing, on every Unix, so a name built from Prefix is already whole.
const ExeSuffix = ""

// runnable answers "would this operating system run it". The question is
// genuinely different per platform rather than portability boilerplate, and
// exename_windows.go is the half that explains why.
//
// On Unix a file is a program because a bit says so, and that bit is the whole
// test: a file named exactly right with mode 0644 is something somebody forgot
// to chmod, not a plugin, and running it would fail anyway.
func runnable(info fs.FileInfo) bool { return info.Mode()&0o111 != 0 }
