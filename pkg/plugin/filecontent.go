package plugin

import "slices"

// FileContent is what a file a setting names holds (Field.File), read by the
// host for one call from where the operator's profile says it is kept — a
// cluster's Secret, the store — rather than from a file. It crosses to the
// plugin as it is, and the plugin's server writes it to a file of the plugin's
// own for the length of the call and hands the handler that file's path
// (Request.Temporary), so a handler reads the setting as it always did.
//
// **Opaque, and named by where it came from.** Its bytes are reached through
// Bytes, and it prints as the reference that read it, kube:pg-ca/ca.crt, never
// as what it holds: a surface showing a value it was handed, a form box seeded
// from a profile or a confirmation page, shows the operator's own words. That
// name stays on this machine. The wire carries the bytes alone, since which
// Secret or store entry holds a CA is not a plugin's to learn, nor to repeat to
// an agent in a refusal.
type FileContent struct {
	data []byte
	from string
}

// NewFileContent is data, read by the reference from.
func NewFileContent(data []byte, from string) FileContent {
	return FileContent{data: slices.Clone(data), from: from}
}

// Bytes is what the file holds, as a copy.
func (f FileContent) Bytes() []byte { return slices.Clone(f.data) }

// String names where the content was read from, and never what it holds.
func (f FileContent) String() string {
	if f.from == "" {
		return "a file read for this call"
	}
	return f.from
}
