//go:build windows

package fs

import "os"

// deviceOfInfo has no answer on Windows, where FileInfo.Sys() is a
// *syscall.Win32FileAttributeData and carries no device number. Reporting
// "unknown" makes sameDevice fall through to "same device", which is the
// documented degradation: a scan counts everything under the path it was
// given rather than refusing to descend and reporting zero.
//
// Volume mount points are the case this cannot see. Naming that here rather
// than leaving the file to look like an oversight: crossing one on Windows
// counts space on another volume, and the fix is a GetVolumePathName call
// that would make this package depend on golang.org/x/sys.
func deviceOfInfo(os.FileInfo) (uint64, bool) { return 0, false }
