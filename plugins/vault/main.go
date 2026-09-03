// Command rta-plugin-vault talks to a HashiCorp Vault deployment: kv
// read/write/list, token and lease info, seal status, policy view, transit
// encrypt/decrypt, and response-wrapping as a within-your-infra share
// Response-wrapping is the answer to sharing a secret within your own infra.
//
// Build it and put it on your $PATH as `rta-plugin-vault`:
//
//	cd plugins/vault && go build -o ~/.local/bin/rta-plugin-vault .
//
// State the address once, in rta's config, under the artifact's own section
// — `rta explain vault.seal.status` prints the exact heading including the
// digest:
//
//	plugins:
//	  vault@<digest>:
//	    address: https://vault.internal:8200
//
// and export RTA_VAULT_TOKEN. Every capability here reaches off the box, so
// none of them — including vault.overview — appear on the automatic
// dashboard on their own (see cap's comment); add one explicitly once you
// have decided polling it is fine:
//
//	dashboard:
//	  tiles:
//	    - id: vault.overview
package main

import (
	vaultapi "github.com/hashicorp/vault/api"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func main() { sdk.Serve(Plugin()) }

// withClient is the shape every capability here has: connect, or return the
// classified error; run.
func withClient(req plugin.Request, fn func(*vaultapi.Client) (view.View, error)) (view.View, error) {
	client, verr := connect(req)
	if verr != nil {
		return nil, verr
	}
	return fn(client)
}

// cap builds a capability with the shared connection inputs appended, so no
// declaration here can forget one and no two can disagree about a default —
// the same helper plugins/pg's main.go documents at length. Every
// capability here reaches off the box for the same reason pg's does, so
// every one is NoPreview for the same reason: the automatic dashboard runs
// Read capabilities unasked, and a live Vault deployment is not a database
// this plugin gets to decide, on its own, is fine to poll every few
// seconds. An operator who has looked at their own deployment and decided
// otherwise still can — dashboard.tiles accepts any capability regardless
// of NoPreview, because naming one in a config file is the asking.
func cap(c plugin.Capability, own ...plugin.Field) plugin.Capability {
	c.Inputs = append(own, connFields()...)
	c.NoPreview = true
	return c
}

func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "vault",
		Summary: "HashiCorp Vault: secrets, tokens, leases, policies and transit encryption",
		Version: "0.1.0",
		Capabilities: []plugin.Capability{
			overviewCapability(),
			sealStatusCapability(),
			kvListCapability(),
			kvTreeCapability(),
			kvGetCapability(),
			kvSetCapability(),
			tokenStatusCapability(),
			leaseShowCapability(),
			policyListCapability(),
			policyGetCapability(),
			transitEncryptCapability(),
			transitDecryptCapability(),
			wrapSetCapability(),
			wrapGetCapability(),
			snapshotCapability(),
			restoreCapability(),
		},
	}
}
