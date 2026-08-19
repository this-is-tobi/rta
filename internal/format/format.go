// Package format holds tiny formatting helpers shared by built-in plugins.
// Views carry pre-formatted strings (pkg/view contract), so producers share
// one vocabulary for common quantities instead of each rolling their own.
package format

import "fmt"

// Bytes renders a byte count in binary units: "512 B", "2.0 KiB", "3.0 GiB".
func Bytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
