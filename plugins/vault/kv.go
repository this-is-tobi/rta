package main

import (
	"context"
	"sort"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// mountField is shared by every capability that talks to a KV v2 engine.
// Vault's own `vault kv` CLI defaults to "secret" because `vault server -dev`
// mounts one there, not because every real deployment does — a production
// Vault routinely has several KV mounts under different names, so this is a
// Field with that default rather than a literal baked into the path.
func mountField() plugin.Field {
	return plugin.Field{Name: "mount", Type: plugin.String, Default: "secret", Config: "kv-mount",
		Help: "the KV v2 secrets engine's mount path"}
}

func pathField(help string) plugin.Field {
	return plugin.Field{Name: "path", Type: plugin.String, Positional: true, Required: true, Help: help}
}

func kvListCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "vault.kv.list",
		Summary:    "List secret names at a path — never values",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "The structured equivalent of `vault kv list`: names only, the same " +
			"Read/Write split builtin/kv's kv.list and kv.get already draw. A name ending in " +
			"\"/\" is itself a path, one level further to list.",
		Run: runKVList,
	}, mountField(),
		plugin.Field{Name: "path", Type: plugin.String, Positional: true, Default: "",
			Help: "list under this path; empty lists the mount's root"})
}

func runKVList(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(client *vaultapi.Client) (view.View, error) {
		secret, err := client.Logical().ListWithContext(ctx, req.String("mount")+"/metadata/"+req.String("path"))
		if err != nil {
			return nil, classify(err, req)
		}
		t := view.Table{Columns: []view.Column{{Name: "Name"}}}
		if secret != nil {
			if keys, ok := secret.Data["keys"].([]interface{}); ok {
				names := make([]string, 0, len(keys))
				for _, k := range keys {
					if s, ok := k.(string); ok {
						names = append(names, s)
					}
				}
				sort.Strings(names)
				for _, n := range names {
					t.Rows = append(t.Rows, []string{n})
				}
			}
		}
		t.Total = len(t.Rows)
		return t, nil
	})
}

func kvGetCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "vault.kv.get",
		Summary:    "Reveal a secret's current version",
		Safety:     plugin.Write,
		NeedsGrant: true,
		Scope:      "path",
		Idempotent: true,
		Description: "Write, the same as builtin/kv's kv.get, for the same reason: revealing a " +
			"secret's plaintext has blast radius even though nothing here is modified. A deleted " +
			"(but not destroyed) version reports which, rather than an empty secret that looks the " +
			"same as one that was never there.",
		Run: runKVGet,
	}, mountField(), pathField("the secret's path within the mount"))
}

func runKVGet(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(client *vaultapi.Client) (view.View, error) {
		secret, err := client.KVv2(req.String("mount")).Get(ctx, req.String("path"))
		if err != nil {
			return nil, classify(err, req)
		}
		if secret.Data == nil {
			return view.KeyValue{Pairs: []view.Pair{
				{Key: "status", Value: "deleted — the metadata survives but this version's data does not"},
			}}, nil
		}
		kv := view.KeyValue{}
		keys := make([]string, 0, len(secret.Data))
		for k := range secret.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			kv.Pairs = append(kv.Pairs, view.Pair{Key: k, Value: cell(secret.Data[k])})
		}
		return kv, nil
	})
}

func kvSetCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "vault.kv.set",
		Summary:    "Set (or overwrite) a secret",
		Safety:     plugin.Write,
		NeedsGrant: true,
		Scope:      "path",
		Idempotent: false,
		Description: "The same overwrite risk builtin/kv's kv.set carries, needing the same " +
			"grant, and the same name: this always creates a brand new version rather than " +
			"merging into the current one — vault.kv.list and vault.kv.get already show what is " +
			"there before this replaces it.",
		Run: runKVSet,
	}, mountField(), pathField("the secret's path within the mount"),
		plugin.Field{Name: "data", Type: plugin.StringSlice, Required: true,
			Help: "key=value, repeated for more than one field"})
}

func runKVSet(ctx context.Context, req plugin.Request) (view.View, error) {
	data, verr := dataFields(req.StringSlice("data"))
	if verr != nil {
		return nil, verr
	}
	return withClient(req, func(client *vaultapi.Client) (view.View, error) {
		secret, err := client.KVv2(req.String("mount")).Put(ctx, req.String("path"), data)
		if err != nil {
			return nil, classify(err, req)
		}
		return view.KeyValue{Pairs: []view.Pair{
			{Key: "path", Value: req.String("path")},
			{Key: "version", Value: cell(secret.VersionMetadata.Version)},
			{Key: "created", Value: secret.VersionMetadata.CreatedTime.Format("2006-01-02T15:04:05Z07:00")},
		}}, nil
	})
}
