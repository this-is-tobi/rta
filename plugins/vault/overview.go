package main

import (
	"context"
	"sort"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// vault.overview composes seal status, the current token and the policy
// list through the one client its own Run already built — the same reason
// pg.overview calls its sections directly rather than through
// plugin.Page.AddAs, which would open one connection per section for what
// is supposed to be a single glance.
//
// NoPreview like every capability here (cap's job): the automatic dashboard
// must not decide on its own that a Vault deployment is worth polling every
// few seconds. `dashboard.tiles` still accepts it explicitly — naming a
// capability in a config file is the asking.

func overviewCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "vault.overview",
		Summary:    "Seal state, the current token and the policy list at a glance",
		Safety:     plugin.Read,
		Idempotent: true,
		Detailed:   true,
		Description: "Whether this Vault is worth talking to at all, and what the configured " +
			"token can do — never a secret value. --detail adds the full policy list to the same " +
			"page.",
		Run: runOverview,
	})
}

func runOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(client *vaultapi.Client) (view.View, error) {
		if req.Bool("detail") {
			return detailedOverview(ctx, client, req)
		}
		return compactOverview(ctx, client, req)
	})
}

func compactOverview(ctx context.Context, client *vaultapi.Client, req plugin.Request) (view.View, error) {
	kv := view.KeyValue{}
	add := func(key, value string) {
		if value != "" {
			kv.Pairs = append(kv.Pairs, view.Pair{Key: key, Value: value})
		}
	}

	if status, err := client.Sys().SealStatusWithContext(ctx); err == nil {
		state := "unsealed"
		if status.Sealed {
			state = "sealed"
		}
		if !status.Initialized {
			state = "not initialized"
		}
		add("state", state+" · "+status.Version)
	}
	if secret, err := client.Auth().Token().LookupSelfWithContext(ctx); err == nil {
		if policies, ok := secret.Data["policies"]; ok {
			add("token policies", cell(policies))
		}
		if ttl, ok := secret.Data["ttl"]; ok {
			add("token ttl (seconds)", cell(ttl))
		}
	}

	if len(kv.Pairs) == 0 {
		return nil, view.Errorf("vault.overview.unavailable", "nothing could be read")
	}
	return kv, nil
}

func detailedOverview(ctx context.Context, client *vaultapi.Client, req plugin.Request) (view.View, error) {
	p := plugin.NewPage(ctx, req)
	put := func(title string, v view.View, err error) {
		if err != nil {
			p.Warn(view.AsError(err, "page.section.failed"))
			return
		}
		p.Put(title, v)
	}

	kv, err := compactOverview(ctx, client, req)
	put("status", kv, err)

	names, err := client.Sys().ListPoliciesWithContext(ctx)
	if err == nil {
		sort.Strings(names)
		t := view.Table{Columns: []view.Column{{Name: "Name"}}}
		for _, n := range names {
			t.Rows = append(t.Rows, []string{n})
		}
		t.Total = len(t.Rows)
		put("policies", t, nil)
	} else {
		put("policies", nil, err)
	}

	if p.Empty() {
		return nil, view.Errorf("vault.overview.unavailable", "nothing could be read")
	}
	return p.View(), nil
}
