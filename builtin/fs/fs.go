// Package fs is the built-in filesystem plugin: what is taking up the space,
// what does this directory actually contain, and is this file the one it
// claims to be.
//
// It is not a file manager and does not try to be find(1). Three questions
// get answered, each one chosen because its shell equivalent is a pipeline
// people look up every time: `du -sh * | sort -h | tail -20`, `tree -L 2`
// with sizes it does not show, and `shasum -a 256 file` compared against a
// checksum by eye. None of them is hard; all of them are fiddly enough that
// the answer arrives slower than the question deserves.
//
// Everything here is read-only, and deliberately so. Deleting files is the
// one operation on this list with no undo, `rm` is already in everybody's
// fingers, and a capability that removes things is a capability an AI agent
// can be talked into removing things with.
package fs

import (
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// pathField is the same input everywhere: what to look at, defaulting to
// where you are.
func pathField(help string) plugin.Field {
	return plugin.Field{Name: "path", Type: plugin.Path, Positional: true, Default: ".", Help: help}
}

// Plugin returns the fs plugin declaration.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "fs",
		Summary: "Filesystem answers: what is using space, what is here, what is this file",
		Capabilities: []plugin.Capability{
			{
				ID:           "fs.usage",
				Summary:      "Show what is using space under a path, biggest first",
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				Detailed:     true,
				// A recursive scan of the working directory, repeated on the
				// dashboard's refresh tick, is cheap in a source tree and
				// ruinous in a home directory. fs.tree is the fs tile: same
				// question, bounded by construction.
				NoPreview: true,
				Description: "Totals every entry under a path and ranks them, which is the question " +
					"`du -sh * | sort -h | tail` is always asked to answer. Directories are summed " +
					"recursively; the share column is of the scanned total, not of the disk, so it " +
					"adds up to what you are looking at. Hidden entries are included — they are " +
					"usually the answer. With --detail: the ranking, the largest individual files " +
					"found anywhere beneath, and what was skipped. Follows no symlinks and crosses " +
					"no filesystem boundary, so a scan cannot loop or wander onto a network mount.",
				Inputs: []plugin.Field{
					pathField("directory to measure"),
					{Name: "limit", Type: plugin.Int, Config: "limit", Default: 20, Min: 1, Max: 1000, Help: "how many entries to rank"},
					{Name: "depth", Type: plugin.Int, Config: "depth", Default: 0,
						Help: "how deep to descend when totalling (0 = no limit)"},
				},
				Run: runUsage,
			},
			{
				ID:           "fs.tree",
				Summary:      "Show a directory as a tree, with sizes",
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				// fs.tree is the fs tile (fs.usage is NoPreview), so this is
				// the page the dashboard opens into. Without it fs was the
				// one plugin whose tile expanded to exactly what the tile
				// already showed.
				Detailed: true,
				Description: "The shape of a directory, a few levels at a time, with each entry's " +
					"size beside it. Entries are ordered directories first and then by name, the " +
					"order a person reads a listing in. Truncated branches say how many entries they " +
					"are hiding rather than trailing off, so the tree never claims a directory is " +
					"smaller than it is. With --detail: what the walk covered, the tree, and " +
					"everything it left out — depth, per-directory limit, hidden entries, mount " +
					"points and unreadable directories gathered in one place instead of scattered " +
					"through the branches they happened in.",
				Inputs: []plugin.Field{
					pathField("directory to show"),
					{Name: "depth", Type: plugin.Int, Config: "depth", Default: 2, Min: 1, Max: 12, Help: "how many levels to show"},
					{Name: "limit", Type: plugin.Int, Config: "limit", Default: 12, Min: 1, Max: 500, Help: "entries to show per directory"},
					{Name: "all", Type: plugin.Bool, Config: "all", Help: "include hidden entries"},
				},
				Run: runTree,
			},
			{
				ID:           "fs.hash",
				Summary:      "Checksum a file, and compare it against an expected value",
				Safety:       plugin.Read,
				HostSpecific: true,
				Idempotent:   true,
				Description: "Hashes a file and, given --expect, says plainly whether it matches — " +
					"which is the actual task, and the one comparing two hex strings by eye is bad " +
					"at. The comparison is case-insensitive and tolerates the \"sha256:\" prefix and " +
					"the surrounding whitespace that come with a pasted checksum. It is not a " +
					"signature check: it says a file is the one somebody described, not that the " +
					"description came from anybody in particular.",
				Inputs: []plugin.Field{
					{Name: "path", Type: plugin.Path, Positional: true, Required: true, Help: "file to hash"},
					{Name: "algo", Type: plugin.String, Config: "algo", Default: "sha256", Options: []string{"sha256", "sha512", "sha1", "md5"},
						Help: "hash algorithm"},
					{Name: "expect", Type: plugin.String, Help: "checksum to compare against"},
				},
				Run: runHash,
			},
		},
	}
}
