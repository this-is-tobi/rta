package main

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// connFields are the inputs every capability here shares — the same three
// things `vault` itself needs told (VAULT_ADDR, VAULT_NAMESPACE, VAULT_TOKEN),
// resolved explicitly through plugin.Request rather than left to the
// client library's own environment-variable defaults, so a handler's
// behavior never depends on what happens to be exported in the process it
// runs in. The same shape plugins/pg's connFields documents.
//
// Every field here is Local, and that is a security property rather than a
// detail — the same reasoning plugins/pg's own connFields documents at
// length (PROJECT.md D94). `address` names which Vault this call reaches and
// `namespace` names which tenant inside it; an MCP caller may not choose
// either, or an agent could point rta at a Vault it controls and have the
// host supply $RTA_VAULT_TOKEN beside it. Both remain Config-backed and
// remain ordinary flags for a person at a terminal. The token differs only
// in also declaring EnvFallback, which is for values that genuinely are
// credentials (D74).
func connFields() []plugin.Field {
	return []plugin.Field{
		{Name: "address", Type: plugin.String, Default: "http://127.0.0.1:8200", Config: "address",
			Local: true, Help: "Vault server address"},
		{Name: "namespace", Type: plugin.String, Default: "", Config: "namespace",
			Local: true, Help: "Vault Enterprise namespace — empty for OSS or the root namespace"},
		{Name: "token", Type: plugin.Secret, Local: true, EnvFallback: true,
			Help: "Vault token"},
	}
}

// connect builds a client from the resolved inputs. Vault's own client
// constructor also reads VAULT_ADDR/VAULT_TOKEN/VAULT_NAMESPACE from the
// process environment as its own defaults — harmless here since every field
// this sets is overwritten immediately after, but worth naming: nothing this
// plugin does depends on that fallback, on purpose.
func connect(req plugin.Request) (*vaultapi.Client, *view.Error) {
	cfg := vaultapi.DefaultConfig()
	if cfg.Error != nil {
		return nil, classify(cfg.Error, req)
	}
	cfg.Address = req.String("address")
	client, err := vaultapi.NewClient(cfg)
	if err != nil {
		return nil, classify(err, req)
	}
	client.SetToken(req.String("token"))
	if ns := req.String("namespace"); ns != "" {
		client.SetNamespace(ns)
	}
	return client, nil
}

// classify turns a client error into something an operator can act on — the
// same job plugins/pg's classify does for a driver error, against Vault's
// own error shapes instead of PostgreSQL's.
func classify(err error, req plugin.Request) *view.Error {
	addr := req.String("address")

	var respErr *vaultapi.ResponseError
	if errors.As(err, &respErr) {
		switch respErr.StatusCode {
		case 403:
			return view.Errorf("vault.denied", "%s refused: %s", addr, joinErrors(respErr)).
				WithHint("the token's policy does not allow this, or the token itself is invalid — " +
					"`rta vault token lookup` shows what the current token can do")
		case 404:
			return view.Errorf("vault.notfound", "nothing at that path on %s", addr).
				WithHint("check the path and the mount — a KV v2 mount is not always named \"secret\"")
		case 400:
			return view.Errorf("vault.badrequest", "%s rejected the request: %s", addr, joinErrors(respErr)).
				WithHint("this is Vault refusing, not rta")
		case 412:
			return view.Errorf("vault.sealed", "%s is sealed or not yet initialized", addr).
				WithHint("`rta vault seal status` shows which")
		}
		return view.Errorf("vault.request.failed", "%s: %s", addr, joinErrors(respErr)).
			WithHint(fmt.Sprintf("HTTP %d", respErr.StatusCode))
	}

	if errors.Is(err, vaultapi.ErrSecretNotFound) {
		return view.Errorf("vault.notfound", "nothing at that path on %s", addr).
			WithHint("check the path and the mount — a KV v2 mount is not always named \"secret\"")
	}

	var netErr *net.OpError
	if errors.As(err, &netErr) || strings.Contains(err.Error(), "connection refused") {
		return view.Errorf("vault.conn.refused", "nothing is listening on %s", addr).
			WithHint("is the server up, and is the address right?")
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return view.Errorf("vault.host.unknown", "no address for %s", addr).
			WithHint("`rta net dns` on the host part of --address shows what DNS returns")
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return view.Errorf("vault.conn.timeout", "%s did not answer in time", addr).
			WithHint("a firewall that drops rather than refuses looks exactly like this")
	}
	var certErr x509.UnknownAuthorityError
	if errors.As(err, &certErr) {
		return view.Errorf("vault.tls.untrusted", "%s presented a certificate rta does not trust", addr).
			WithHint("this is a real TLS trust failure, not something to work around here")
	}
	return view.Errorf("vault.conn.failed", "could not reach %s: %v", addr, err).
		WithHint("`rta explain vault.seal.status` lists every input and where each can come from")
}

// joinErrors renders a ResponseError's Errors slice the way Vault's own CLI
// does — one line, since a view.Error's Message is a line, not a list.
func joinErrors(respErr *vaultapi.ResponseError) string {
	if len(respErr.Errors) == 0 {
		return respErr.Error()
	}
	return strings.Join(respErr.Errors, "; ")
}

// dataFields parses a repeated key=value input into a map, the shape every
// KV-writing capability here needs — the same convention `kubectl create
// secret generic --from-literal` uses, chosen because a Vault secret is a
// small document (several fields), not the single value builtin/kv stores,
// and plugin.Field has no map type to ask for one directly.
func dataFields(pairs []string) (map[string]interface{}, *view.Error) {
	data := make(map[string]interface{}, len(pairs))
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, view.Errorf("vault.data.invalid", "%q is not key=value", pair).
				WithHint("each --data is one key=value pair, repeated for more than one")
		}
		data[key] = value
	}
	return data, nil
}
