// Package format holds the formatting vocabulary a view producer needs.
//
// A view carries pre-formatted strings (pkg/view contract): view.ColumnKind
// selects alignment and styling, never a number's rendering, so whoever
// builds the view formats it. This package exists so that everybody who has
// to do that says the same thing.
//
// In pkg rather than internal, because "everybody" is mostly not in this
// repository. It lived under internal while the only producers were built-in,
// which left every external plugin held to a contract whose vocabulary it
// could not import — so the first one to show a byte count showed
// `1392640`.
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
