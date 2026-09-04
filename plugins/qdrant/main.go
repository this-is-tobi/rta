// Command rta-plugin-qdrant talks to a Qdrant vector database: what it is,
// what collections it holds, how each is configured and how healthy its
// indexes are — and, behind the write tier, the points themselves.
//
// # Why the split here is not a formality
//
// A vector collection holds two things worth protecting, and they need saying
// separately because only one of them is obvious.
//
// The payloads are the obvious half: whatever was indexed, which for most
// deployments is chunks of documents — support tickets, internal wikis,
// contracts, customer records. Reading points is reading those.
//
// The vectors are the half people forget. An embedding is not a hash: it is a
// lossy but reversible-enough encoding, and inversion attacks recover
// substantial parts of the source text from embeddings alone. So handing back
// raw vectors is closer to handing back the documents than to handing back a
// checksum, and this plugin treats it that way.
//
// Which is why the read tier here describes collections — their configuration,
// their size, whether their indexes are built — and returns not one point.
// qdrant.points.scroll is the only capability that returns stored data to a
// caller, and it is a write that needs a grant naming it. qdrant.dump and
// qdrant.restore move whole collections as snapshot files, and they refuse
// MCP outright — see dump.go for why that pair belongs to a person at a
// terminal.
//
// Build it and put it on your $PATH as `rta-plugin-qdrant`:
//
//	cd plugins/qdrant && go build -o ~/.local/bin/rta-plugin-qdrant .
//
// State the connection once, in rta's config, under the artifact's own
// section — `rta explain qdrant.overview` prints the exact heading including
// the digest:
//
//	plugins:
//	  qdrant@<digest>:
//	    endpoint: qdrant.internal:6333
//	    tls: true
//
// and export RTA_QDRANT_API_KEY. Every capability here reaches off the box,
// so none of them appear on the automatic dashboard on their own (see cap's
// comment); add one explicitly once you have decided polling it is fine:
//
//	dashboard:
//	  tiles:
//	    - id: qdrant.overview
package main

import (
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk"
)

func main() { sdk.Serve(Plugin()) }

// cap builds a capability with the shared connection inputs appended, so no
// declaration here can forget one and no two can disagree about a default.
//
// Every capability here is NoPreview because every one reaches off the box:
// the automatic dashboard runs Read capabilities unasked, and an instance
// somebody's search depends on is not something this plugin gets to decide, on
// its own, is fine to poll every few seconds. An operator who has looked at
// their own deployment and decided otherwise still can — dashboard.tiles
// accepts any capability regardless of NoPreview, because naming one in a
// config file is the asking.
func cap(c plugin.Capability, own ...plugin.Field) plugin.Capability {
	c.Inputs = append(own, connFields()...)
	c.NoPreview = true
	return c
}

// version is what this build claims to be, stamped by whatever built it:
// `-X main.version=`, which is the Makefile's flag and GoReleaser's own
// default. A build nobody stamped says "dev" rather than claiming a release
// number that was never cut — an index entry carries this verbatim, and a
// version is a fact about a release, not about the source it came from.
var version = "dev"

func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "qdrant",
		Summary: "Qdrant: collections, their configuration and index health",
		Version: version,
		Capabilities: []plugin.Capability{
			overviewCapability(),
			collectionListCapability(),
			collectionShowCapability(),
			pointsCountCapability(),
			pointsScrollCapability(),
			dumpCapability(),
			restoreCapability(),
		},
	}
}
