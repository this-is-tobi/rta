//go:build !unix

package paths

import "io/fs"

// ownedByUs is true where the platform's temporary directory is the account's
// own, as it is on Windows; the mode check does the rest.
func ownedByUs(fs.FileInfo) bool { return true }
