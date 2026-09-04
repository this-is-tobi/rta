// Command rta-plugin-etcd talks to an etcd v3 cluster: whether it is healthy,
// who is in it, what shape its keyspace has, and — behind the write tier —
// what a key holds and the whole keyspace as a file.
//
// # What this is for, and what it is not
//
// etcd is where a Kubernetes cluster keeps every object it has, including its
// Secrets, which are stored base64-encoded and not encrypted unless somebody
// turned that on. So the read/write split here is not a formality: the read
// tier tells you the cluster is healthy and what the keyspace looks like, and
// etcd.kv.get — the one capability that returns a stored value — is a write
// that needs a grant naming it.
//
// For anything Kubernetes-shaped, plugins/kube is the right tool and this is
// not: it goes through the API server, which means RBAC, admission and an
// audit log apply. Reaching a cluster's etcd directly goes around all three.
// This exists for etcd used as itself — service discovery, coordination,
// configuration — and for the cases where the API server is the thing that is
// broken.
//
// Build it and put it on your $PATH as `rta-plugin-etcd`:
//
//	cd plugins/etcd && go build -o ~/.local/bin/rta-plugin-etcd .
//
// State the connection once, in rta's config, under the artifact's own
// section — `rta explain etcd.overview` prints the exact heading including
// the digest:
//
//	plugins:
//	  etcd@<digest>:
//	    endpoint: etcd-0.internal:2379
//	    tls: true
//	    ca-file: /etc/etcd/ca.crt
//
// and export RTA_ETCD_PASSWORD if the cluster uses password auth. Every
// capability here reaches off the box, so none of them appear on the automatic
// dashboard on their own (see cap's comment); add one explicitly once you have
// decided polling it is fine:
//
//	dashboard:
//	  tiles:
//	    - id: etcd.overview
package main

import (
	"context"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func main() { sdk.Serve(Plugin()) }

// withClient is the shape every capability here has: connect, or return the
// classified error; run; close.
//
// The close matters more than usual: etcd's client holds a gRPC connection
// with its own keepalive goroutines, and one left open outlives the call that
// made it.
func withClient(ctx context.Context, req plugin.Request, fn func(context.Context, *clientv3.Client) (view.View, error)) (view.View, error) {
	client, verr := connect(ctx, req)
	if verr != nil {
		return nil, verr
	}
	defer func() { _ = client.Close() }()
	return fn(ctx, client)
}

// cap builds a capability with the shared connection inputs appended, so no
// declaration here can forget one and no two can disagree about a default.
//
// Every capability here is NoPreview because every one reaches off the box:
// the automatic dashboard runs Read capabilities unasked, and the store a
// production cluster depends on is not something this plugin gets to decide,
// on its own, is fine to poll every few seconds. An operator who has looked at
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
		Name:    "etcd",
		Summary: "etcd v3: cluster health, members, leases and the keyspace",
		Version: version,
		Capabilities: []plugin.Capability{
			overviewCapability(),
			memberListCapability(),
			leaseListCapability(),
			kvListCapability(),
			kvTreeCapability(),
			kvGetCapability(),
			snapshotCapability(),
		},
	}
}
