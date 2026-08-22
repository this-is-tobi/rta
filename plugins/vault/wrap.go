package main

import (
	"context"
	"sort"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// wrapSetCapability and wrapGetCapability are D13/ADR 0006's answer to
// "share a secret within your own infra": Vault's native response-wrapping
// (sys/wrapping/wrap) as a single-use, TTL'd cubbyhole token, a thin
// pass-through rather than a new transport — it reaches only someone who can
// already reach this Vault deployment, which PROJECT.md §7 is explicit is
// "share within your infra", not "send to anybody" (that is builtin/share,
// Wave 3, built on magic-wormhole instead).
func wrapSetCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "vault.wrap.set",
		Summary:    "Wrap data into a single-use, TTL'd token",
		Safety:     plugin.Write,
		NeedsGrant: true,
		Idempotent: false,
		Description: "The recipient calls vault.wrap.get with the token this returns, once — " +
			"Vault destroys the cubbyhole the instant it is read, by anyone, so a second read (an " +
			"eavesdropper, a retry) gets nothing. No Scope: this wraps whatever data it is handed " +
			"rather than acting on a record that already exists, so there is nothing to name one " +
			"grant against the way vault.kv.get names a path.",
		Run: runWrapSet,
	}, plugin.Field{Name: "data", Type: plugin.StringSlice, Required: true,
		Help: "key=value, repeated for more than one field"},
		plugin.Field{Name: "ttl", Type: plugin.String, Default: "5m",
			Help: "how long the token stays valid, unread — not the data's own lifetime"})
}

func runWrapSet(ctx context.Context, req plugin.Request) (view.View, error) {
	data, verr := dataFields(req.StringSlice("data"))
	if verr != nil {
		return nil, verr
	}
	return withClient(req, func(client *vaultapi.Client) (view.View, error) {
		ttl := req.String("ttl")
		client.SetWrappingLookupFunc(func(operation, path string) string { return ttl })
		secret, err := client.Logical().WriteWithContext(ctx, "sys/wrapping/wrap", data)
		if err != nil {
			return nil, classify(err, req)
		}
		if secret.WrapInfo == nil {
			return nil, view.Errorf("vault.wrap.failed", "%s did not wrap the response", req.String("address"))
		}
		return view.KeyValue{Pairs: []view.Pair{
			{Key: "token", Value: secret.WrapInfo.Token},
			{Key: "ttl", Value: cell(secret.WrapInfo.TTL) + "s"},
		}}, nil
	})
}

func wrapGetCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "vault.wrap.get",
		Summary:    "Unwrap a single-use token, once",
		Safety:     plugin.Write,
		NeedsGrant: true,
		Scope:      "wrapping-token",
		Idempotent: false,
		Description: "Consumes the token: a second call against the same token gets Vault's own " +
			"\"wrapping token is not valid or does not exist\" refusal, from Vault itself rather " +
			"than anything this plugin tracks. --dry-run must not spend the one read this token " +
			"has, so it calls sys/wrapping/lookup instead — creation time, path and TTL, without " +
			"the payload and without consuming anything.",
		Run: runWrapGet,
	}, plugin.Field{Name: "wrapping-token", Type: plugin.String, Positional: true, Required: true,
		Help: "the wrapping token vault.wrap.set returned — a different token from --token, " +
			"this plugin's own auth credential"})
}

func runWrapGet(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(client *vaultapi.Client) (view.View, error) {
		if req.DryRun {
			secret, err := client.Logical().WriteWithContext(ctx, "sys/wrapping/lookup",
				map[string]interface{}{"token": req.String("wrapping-token")})
			if err != nil {
				return nil, classify(err, req)
			}
			kv := view.KeyValue{}
			keys := []string{"creation_path", "creation_time", "creation_ttl"}
			sort.Strings(keys)
			for _, k := range keys {
				if v, ok := secret.Data[k]; ok {
					kv.Pairs = append(kv.Pairs, view.Pair{Key: k, Value: cell(v)})
				}
			}
			return kv, nil
		}

		secret, err := client.Logical().UnwrapWithContext(ctx, req.String("wrapping-token"))
		if err != nil {
			return nil, classify(err, req)
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
