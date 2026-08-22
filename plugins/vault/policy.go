package main

import (
	"context"
	"sort"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func policyListCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "vault.policy.list",
		Summary:    "Every ACL policy defined on this Vault",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "Names, not rules — vault.policy.get shows one policy's own document. A " +
			"policy names paths and capabilities, not secret values, so listing and reading policy " +
			"documents stays Read the way builtin/kv's kv.recipients (public keys, not secrets) does.",
		Run: runPolicyList,
	})
}

func runPolicyList(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(client *vaultapi.Client) (view.View, error) {
		names, err := client.Sys().ListPoliciesWithContext(ctx)
		if err != nil {
			return nil, classify(err, req)
		}
		sort.Strings(names)
		t := view.Table{Columns: []view.Column{{Name: "Name"}}}
		for _, n := range names {
			t.Rows = append(t.Rows, []string{n})
		}
		t.Total = len(t.Rows)
		return t, nil
	})
}

func policyGetCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "vault.policy.get",
		Summary:    "One policy's own rules, as HCL",
		Safety:     plugin.Read,
		Idempotent: true,
		Run:        runPolicyGet,
	}, plugin.Field{Name: "name", Type: plugin.String, Positional: true, Required: true,
		Help: "the policy's name, as vault.policy.list shows it"})
}

func runPolicyGet(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(client *vaultapi.Client) (view.View, error) {
		rules, err := client.Sys().GetPolicyWithContext(ctx, req.String("name"))
		if err != nil {
			return nil, classify(err, req)
		}
		if rules == "" {
			return nil, view.Errorf("vault.policy.notfound", "no policy named %q", req.String("name")).
				WithHint("`rta vault policy list` shows what exists")
		}
		return view.Text{Body: rules}, nil
	})
}
