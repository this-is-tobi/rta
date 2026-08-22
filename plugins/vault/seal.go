package main

import (
	"context"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func sealStatusCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "vault.seal.status",
		Summary:    "Whether Vault is initialized, sealed, and what it takes to unseal it",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "The same shape builtin/kv's kv.status has, for the same reason: where the " +
			"vault is and what it would take to open it, without opening it or touching a single " +
			"secret. Answerable before authentication even matters — a sealed Vault refuses every " +
			"token, including a valid one, so this is the first thing worth checking when " +
			"anything else here fails.",
		Run: runSealStatus,
	})
}

func runSealStatus(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(client *vaultapi.Client) (view.View, error) {
		status, err := client.Sys().SealStatusWithContext(ctx)
		if err != nil {
			return nil, classify(err, req)
		}
		kv := view.KeyValue{Pairs: []view.Pair{
			{Key: "initialized", Value: cell(status.Initialized)},
			{Key: "sealed", Value: cell(status.Sealed)},
			{Key: "version", Value: status.Version},
			{Key: "storage", Value: status.StorageType},
		}}
		if status.Sealed {
			kv.Pairs = append(kv.Pairs, view.Pair{
				Key: "progress", Value: cell(status.Progress) + " of " + cell(status.T) + " key shares",
			})
		}
		if status.ClusterName != "" {
			kv.Pairs = append(kv.Pairs, view.Pair{Key: "cluster", Value: status.ClusterName})
		}
		return kv, nil
	})
}
