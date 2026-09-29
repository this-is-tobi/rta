//go:build !unix

package pathguard

import "io/fs"

// fileID is empty where the platform names no identity idSet can key by.
type fileID struct{}

// identity is false everywhere but unix. Windows keeps a file's identity —
// its volume serial and file index — in fields of os's own FileInfo that only
// os.SameFile reads, so the set is compared one entry at a time there
// (idSet.other).
func identity(fs.FileInfo) (fileID, bool) { return fileID{}, false }
