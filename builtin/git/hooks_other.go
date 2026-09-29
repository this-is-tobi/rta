//go:build !unix

package git

import "os"

// mayExecute reads an execute bit off mode where there is no access(2) to
// ask (hookStatus).
func mayExecute(_ boundDir, _ string, mode os.FileMode) bool { return mode&0o111 != 0 }
