package main

import (
	"context"
	"sort"
	"strings"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// Live completion (Field.Live, ADR 0018 §8 as amended): each Suggest here is
// a listing Vault already answers for this token, visible in Vault's own
// audit log like any other request by the same identity. Names only by
// construction — LIST is Vault's names-only operation and sys/mounts is a
// table of engines — so no value moves for a completion, the same line the
// kube Secret-key listing holds with its keys-only template.
//
// vault.kv.get and vault.kv.set gate their runs behind a grant; their path
// completes ungated because the identical listing is vault.kv.list's whole
// ungated answer — completion reveals nothing the Read surface does not
// already hand out. Silent on every failure, like any Suggest: the run that
// follows classifies the same failure with a code and a hint.

// completionCap bounds one listing's answer — an assist is a screenful, not
// an inventory.
const completionCap = 60

// suggestMounts lists the mounts of one engine type, trailing slash trimmed
// to the form the mount inputs take.
func suggestMounts(engine string) func(context.Context, plugin.Request) []string {
	return func(ctx context.Context, req plugin.Request) []string {
		client, verr := connect(req)
		if verr != nil {
			return nil
		}
		mounts, err := client.Sys().ListMountsWithContext(ctx)
		if err != nil {
			return nil
		}
		var out []string
		for path, m := range mounts {
			if m.Type == engine {
				out = append(out, strings.TrimSuffix(path, "/"))
			}
		}
		sort.Strings(out)
		return capped(out)
	}
}

// suggestPaths walks a KV mount one "/" segment at a time. LIST names the
// entries under the typed prefix's folder, relative — so each one is
// re-rooted onto that folder to extend the box, and a folder keeps its
// trailing "/", which is what lets the next press fetch deeper instead of
// re-accepting (needsFetch's extends-or-fetch rule, the kube coordinate's
// own convention).
func suggestPaths(ctx context.Context, req plugin.Request) []string {
	client, verr := connect(req)
	if verr != nil {
		return nil
	}
	partial := req.String("path")
	dir := ""
	if i := strings.LastIndex(partial, "/"); i >= 0 {
		dir = partial[:i+1]
	}
	secret, err := client.Logical().ListWithContext(ctx, req.String("mount")+"/metadata/"+dir)
	if err != nil {
		return nil
	}
	names := listedNames(secret)
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, dir+n)
	}
	sort.Strings(out)
	return capped(out)
}

// suggestTransitKeys lists the key names under the transit mount — flat,
// because transit keys do not nest.
func suggestTransitKeys(ctx context.Context, req plugin.Request) []string {
	client, verr := connect(req)
	if verr != nil {
		return nil
	}
	secret, err := client.Logical().ListWithContext(ctx, req.String("mount")+"/keys")
	if err != nil {
		return nil
	}
	out := listedNames(secret)
	sort.Strings(out)
	return capped(out)
}

// suggestPolicies lists policy names — what vault.policy.list prints.
func suggestPolicies(ctx context.Context, req plugin.Request) []string {
	client, verr := connect(req)
	if verr != nil {
		return nil
	}
	names, err := client.Sys().ListPoliciesWithContext(ctx)
	if err != nil {
		return nil
	}
	sort.Strings(names)
	return capped(names)
}

// listedNames unpacks a LIST response's keys — nil-safe, because Vault
// answers a missing path with no secret rather than an error.
func listedNames(secret *vaultapi.Secret) []string {
	if secret == nil {
		return nil
	}
	raw, ok := secret.Data["keys"].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, k := range raw {
		if s, ok := k.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func capped(out []string) []string {
	if len(out) > completionCap {
		return out[:completionCap]
	}
	return out
}
