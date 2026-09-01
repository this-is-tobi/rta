package main

import (
	"context"
	"encoding/base64"
	"fmt"

	vaultapi "github.com/hashicorp/vault/api"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// transitMountField mirrors mountField for the transit engine, whose
// deployments name it "transit" far more consistently than KV mounts settle
// on "secret" — still a Field, not a literal, for the deployment that
// renamed it.
func transitMountField() plugin.Field {
	return plugin.Field{Name: "mount", Type: plugin.String, Default: "transit", Config: "transit-mount",
		Help: "the transit secrets engine's mount path", Live: true, Suggest: suggestMounts("transit")}
}

func transitEncryptCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "vault.transit.encrypt",
		Summary:    "Encrypt caller-supplied plaintext with a Vault-managed key",
		Safety:     plugin.Write,
		Idempotent: false,
		Description: "Write, not Read+NeedsGrant like vault.kv.get: nothing here is revealed to " +
			"the caller that they did not already hand over — the plaintext is theirs, only the " +
			"ciphertext comes back — but the key material is materially used to produce it, which " +
			"is what keeps this off Read (the same distinction gpg.sign's design draws for a " +
			"signature: no key exposure, real use). The key itself never leaves Vault; that is the " +
			"whole point of the transit engine.",
		Run: runTransitEncrypt,
	}, transitMountField(),
		plugin.Field{Name: "key", Type: plugin.String, Positional: true, Required: true,
			Help: "the transit key's name", Live: true, Suggest: suggestTransitKeys},
		// Secret, not Text. The whole point of handing something to
		// transit.encrypt is that it is worth encrypting, so the plaintext
		// is a credential by construction — and Text is not Sensitive, so it
		// was written verbatim into the sealed agent log on every MCP call
		// and into the completion shortlist from a terminal. The mask costs
		// nothing here: nothing completes a value nobody has typed before.
		plugin.Field{Name: "plaintext", Type: plugin.Secret, Required: true,
			Help: "the value to encrypt"})
}

func runTransitEncrypt(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(client *vaultapi.Client) (view.View, error) {
		path := req.String("mount") + "/encrypt/" + req.String("key")
		// Encrypting is a write to the key: it advances the key's usage and,
		// on a convergent or auto-rotating key, is not free of consequence.
		// The plaintext is the caller's own, so a preview owes them only the
		// terms.
		if req.DryRun {
			return view.Text{Body: fmt.Sprintf("would encrypt %s with %s",
				format.Bytes(uint64(len(req.String("plaintext")))), path)}, nil
		}
		secret, err := client.Logical().WriteWithContext(ctx, path, map[string]interface{}{
			"plaintext": base64.StdEncoding.EncodeToString([]byte(req.String("plaintext"))),
		})
		if err != nil {
			return nil, classify(err, req)
		}
		ciphertext, _ := secret.Data["ciphertext"].(string)
		return view.KeyValue{Pairs: []view.Pair{{Key: "ciphertext", Value: ciphertext}}}, nil
	})
}

func transitDecryptCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:         "vault.transit.decrypt",
		Summary:    "Decrypt ciphertext back to its plaintext",
		Safety:     plugin.Write,
		NeedsGrant: true,
		Scope:      "key",
		Idempotent: true,
		Description: "The reveal half of transit: whoever holds the ciphertext gets the " +
			"plaintext back, which is exactly vault.kv.get's blast radius against a different " +
			"store — same Write+NeedsGrant answer.",
		Run: runTransitDecrypt,
	}, transitMountField(),
		plugin.Field{Name: "key", Type: plugin.String, Positional: true, Required: true,
			Help: "the transit key's name", Live: true, Suggest: suggestTransitKeys},
		plugin.Field{Name: "ciphertext", Type: plugin.Text, Required: true,
			Help: "the vault:v#:... ciphertext to decrypt"})
}

func runTransitDecrypt(ctx context.Context, req plugin.Request) (view.View, error) {
	return withClient(req, func(client *vaultapi.Client) (view.View, error) {
		path := req.String("mount") + "/decrypt/" + req.String("key")
		secret, err := client.Logical().WriteWithContext(ctx, path, map[string]interface{}{
			"ciphertext": req.String("ciphertext"),
		})
		if err != nil {
			return nil, classify(err, req)
		}
		encoded, _ := secret.Data["plaintext"].(string)
		plaintext, decErr := base64.StdEncoding.DecodeString(encoded)
		if decErr != nil {
			return nil, view.Errorf("vault.transit.malformed", "decrypted but the plaintext was not valid base64: %v", decErr)
		}
		return view.KeyValue{Pairs: []view.Pair{{Key: "plaintext", Value: string(plaintext)}}}, nil
	})
}
