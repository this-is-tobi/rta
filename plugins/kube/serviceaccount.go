package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// kube.serviceaccount.provision/.list/.revoke: minting a narrow, short-lived
// Kubernetes identity for an AI agent, instead of handing it the operator's
// own ambient kubeconfig — the same shape rta already gives a Postgres
// password (kept out of an agent's hands entirely) applied to the one
// credential type that has to be minted fresh rather than merely withheld.
//
// See the plan's "What changed from the original sketch" for why this does
// not write into rta's own kv store or read a grant's TTL: neither is
// reachable from a plugin process, and the shape here — return the result to
// the operator's own terminal, or write it to a file they name — is what
// keys.backup already established for exactly this situation.

// minTokenTTL matches the Kubernetes TokenRequest API's own hardcoded floor
// (MinTokenExpirationSeconds upstream, 600s) — not a policy this or any
// particular cluster chose. Every standard distribution's API server rejects
// a shorter --duration outright: "may not specify a duration less than 10
// minutes", confirmed directly against a real cluster rather than assumed.
// Checked here so a too-short --ttl is refused before any cluster write, the
// same fail-fast discipline the parse-error case already gets — without it,
// the ServiceAccount/Role/RoleBinding get created and only the token mint
// fails, leaving a partial provision the operator has to notice and clean up.
const minTokenTTL = 10 * time.Minute

func serviceAccountCapabilities() []plugin.Capability {
	return []plugin.Capability{
		cap(plugin.Capability{
			ID:      "kube.serviceaccount.provision",
			Summary: "Mint a scoped ServiceAccount, Role and token for an agent to use",
			// Safety: Write, deliberately with no NeedsGrant or Scope. This
			// mirrors keys.backup, not kube.context.set: refusing SurfaceMCP
			// outright is chosen *instead of* a grant, not alongside one —
			// NeedsGrant would trigger a full consent/notification flow in
			// internal/mcp/bridge.go *before* Run ever executes, for a call
			// that this Run refuses unconditionally the moment it starts.
			// That would spend an operator's attention on a decision that
			// cannot matter. The real gate is the SurfaceMCP check below:
			// operator-only, from the CLI/TUI, same as keys.backup/.restore.
			//
			// One consequence worth being explicit about: this combination
			// means no CLI/TUI confirmation prompt either — that prompt is
			// wired strictly to Safety == Destructive in this codebase, which
			// this capability deliberately is not. keys.add accepts the same
			// tradeoff for a locally-written file; this accepts it for a
			// capability that creates live, network-visible cluster objects —
			// still gated by "you are a person at this operator's terminal",
			// the same bar every other ungated CLI action in rta accepts.
			Safety: plugin.Write,
			Description: "Creates a ServiceAccount, a Role built from exactly the grants named in " +
				"--grant (nothing broader — an unmapped name refuses the whole request rather than " +
				"silently granting less than asked), a RoleBinding, and a token scoped to --ttl. " +
				"A grant is either a kube.* capability ID (what that capability reads) or a bare " +
				"word naming a cluster permission the minted identity needs but rta has no " +
				"capability for: logs, workloads, services, and — the one write — rollout, which " +
				"carries patch on workloads and is meant for environments where changing what runs " +
				"is acceptable. Returns the assembled kubeconfig — to the terminal, or to --out, " +
				"which refuses an existing file unless --force says to replace it. Refuses to run " +
				"anywhere but a person's own CLI/TUI: an agent must never be able to mint its own " +
				"parallel credential. There is no link enforced between --ttl and any `grant allow` " +
				"TTL issued elsewhere — matching them is the operator's convention to keep, not " +
				"something this checks.",
			Inputs: []plugin.Field{
				{Name: "name", Type: plugin.String, Positional: true, Required: true,
					Help: "name for the new ServiceAccount, Role and RoleBinding (all three share it)"},
				{Name: "namespace", Type: plugin.String, Positional: true, Required: true,
					// Live for nsFields' reason: this Suggest contacts the
					// cluster, so it answers a deliberate completion press
					// only, never a keystroke.
					Help: "namespace to provision the identity in",
					Live: true, Suggest: suggestNamespaces},
				{Name: "grant", Type: plugin.StringSlice, Required: true,
					// Options rather than Suggest: rulesFor fails closed on
					// anything outside the table, so the set really is closed
					// — and computing it from the table keeps the picker, the
					// validation and the enforcement one list.
					Options: provisionableNames(),
					Help:    "what the identity may do: a kube.* capability ID, or logs, workloads, services, rollout — repeatable"},
				{Name: "ttl", Type: plugin.String, Required: true,
					Help: "how long the minted token should last, e.g. 15m, 1h, 24h"},
				{Name: "out", Type: plugin.Path, Local: true,
					Help: "write the kubeconfig to this file (0600) instead of printing it"},
				{Name: "force", Type: plugin.Bool, Local: true,
					Help: "replace --out's file if it already exists"},
			},
			Run: runServiceAccountProvision,
		}),
		cap(plugin.Capability{
			ID:      "kube.serviceaccount.list",
			Summary: "ServiceAccounts this plugin has provisioned, and whether they look expired",
			Description: "Only ServiceAccounts carrying provision's own label — not every " +
				"ServiceAccount in the namespace. A minted token cannot be queried directly (Kubernetes " +
				"does not persist a TokenRequest token as an object), so \"expired\" here is computed " +
				"from the --ttl and issue time provision recorded as annotations at mint time — a " +
				"best-effort estimate, not a live check against the API server.",
			Safety:     plugin.Read,
			Idempotent: true,
			Scope:      "namespace",
			Run:        runServiceAccountList,
		}, nsFields()...),
		cap(plugin.Capability{
			ID:      "kube.serviceaccount.revoke",
			Summary: "Delete a provisioned ServiceAccount, Role and RoleBinding",
			// Destructive, MCP-reachable, no SurfaceMCP refusal: the opposite
			// of provision on purpose. Revoking takes access away rather than
			// minting it, and Destructive already forces the ordinary
			// consent/grant gate regardless of NeedsGrant — the same pattern
			// net.hosts.rm and resolver.set already use for a destructive
			// local mutation. Scope is the ServiceAccount name, so a grant to
			// revoke one provisioned identity does not cover another.
			Safety:     plugin.Destructive,
			NeedsGrant: true,
			Scope:      "name",
			Description: "A TokenRequest bearer token has no independent early-revocation API — it " +
				"stays valid until its own --ttl regardless of anything rta does. Deleting the " +
				"ServiceAccount is what invalidates every token minted against it immediately, and " +
				"because provision never reuses one ServiceAccount across grants, this always means " +
				"\"this one identity\", never \"every agent's access at once\". Refuses to touch " +
				"anything not carrying provision's own label, so this cannot be used to delete an " +
				"unrelated ServiceAccount that happens to share a name. Tolerates any of the three " +
				"objects already being gone (a previous partial provision, or a previous revoke run " +
				"twice) rather than refusing on the first missing piece.",
			Inputs: []plugin.Field{
				{Name: "name", Type: plugin.String, Positional: true, Required: true,
					Help: "the ServiceAccount to revoke"},
				{Name: "namespace", Type: plugin.String, Positional: true, Required: true,
					// Live for nsFields' reason: a cluster read answers a
					// press, not a keystroke.
					Help: "namespace it was provisioned in",
					Live: true, Suggest: suggestNamespaces},
			},
			Run: runServiceAccountRevoke,
		}),
	}
}

// validatedIdentity reads and checks name/namespace, the two fields every
// capability in this file requires.
func validatedIdentity(req plugin.Request) (name, namespace string, verr *view.Error) {
	name = strings.TrimSpace(req.String("name"))
	if verr = checkName("serviceaccount", name); verr != nil {
		return "", "", verr
	}
	if name == "" {
		return "", "", view.Errorf("kube.serviceaccount.name.empty", "name the ServiceAccount")
	}
	namespace = strings.TrimSpace(req.String("namespace"))
	if verr = checkName("namespace", namespace); verr != nil {
		return "", "", verr
	}
	if namespace == "" {
		return "", "", view.Errorf("kube.serviceaccount.namespace.empty", "name the namespace")
	}
	return name, namespace, nil
}

func runServiceAccountProvision(ctx context.Context, req plugin.Request) (view.View, error) {
	if req.Surface() == plugin.SurfaceMCP {
		return nil, view.Refusef("kube.serviceaccount.mcp",
			"minting a Kubernetes identity is not available over MCP").
			WithHint("run this from the CLI or TUI — an agent must never be able to mint its own parallel credential")
	}

	name, namespace, verr := validatedIdentity(req)
	if verr != nil {
		return nil, verr
	}

	ttlStr := strings.TrimSpace(req.String("ttl"))
	ttl, err := time.ParseDuration(ttlStr)
	if err != nil || ttl < minTokenTTL {
		return nil, view.Errorf("kube.serviceaccount.ttl.invalid", "%q is not a usable duration", ttlStr).
			WithHint("use a Go-style duration of at least 10m — the TokenRequest API's own floor, " +
				"e.g. 15m, 1h or 24h")
	}

	rules, verr := rulesFor(req.StringSlice("grant"))
	if verr != nil {
		return nil, verr
	}

	// selectionOf reads "namespace" itself, the same field validatedIdentity
	// already checked — s.Namespace is that same validated value.
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}

	// **Checked here, before the first cluster write, and again at the write
	// itself.** The second check is the race-free one — O_EXCL is what
	// guarantees an existing file is never replaced — but on its own it fires
	// only after the ServiceAccount, Role, RoleBinding and token have all been
	// created, so pointing --out at a path that already exists would burn a
	// name, leave three objects behind, and discard a freshly minted token
	// that cannot be recovered. That is the same shape as minting against a
	// too-short --ttl, which is checked up here for the same reason.
	//
	// So this is not a duplicate of the O_EXCL below: it is the difference
	// between refusing and refusing *cheaply*.
	out := strings.TrimSpace(req.String("out"))
	force := req.Bool("force")
	if out != "" && !force {
		if _, err := os.Stat(expandHome(out)); err == nil {
			return nil, view.Errorf("kube.serviceaccount.out.exists",
				"%s already exists", expandHome(out)).
				WithHint("name a path that does not exist yet, or pass --force to replace it — " +
					"checked before anything was created on the cluster")
		}
	}

	if req.DryRun {
		return dryRunProvision(name, namespace, rules, ttlStr), nil
	}

	labels := provisionLabels()
	now := time.Now().UTC()

	sa := serviceAccountManifest{
		APIVersion: "v1", Kind: "ServiceAccount",
		Metadata: objectMeta{
			Name: name, Namespace: namespace, Labels: labels,
			Annotations: map[string]string{
				"rta.dev/ttl":       ttlStr,
				"rta.dev/issued-at": now.Format(time.RFC3339),
			},
		},
	}
	// Refused rather than reused: `create` fails on a name that already
	// exists, which is what makes "never shared" real rather than aspirational
	// — an operator who reuses a name gets a clear collision error, not a
	// silently-modified existing identity.
	if verr := createManifest(ctx, s, sa); verr != nil {
		return nil, verr.WithHint("if this failed partway through a previous attempt, " +
			"`kube.serviceaccount.revoke " + name + " -n " + namespace + "` cleans up whatever it left behind")
	}

	role := roleManifest{
		APIVersion: "rbac.authorization.k8s.io/v1", Kind: "Role",
		Metadata: objectMeta{Name: name, Namespace: namespace, Labels: labels},
		Rules:    rules,
	}
	if verr := createManifest(ctx, s, role); verr != nil {
		return nil, verr.WithHint("the ServiceAccount was created but the Role was not — " +
			"`kube.serviceaccount.revoke " + name + " -n " + namespace + "` cleans it up")
	}

	rb := roleBindingManifest{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "RoleBinding"}
	rb.Metadata = objectMeta{Name: name, Namespace: namespace, Labels: labels}
	rb.RoleRef.APIGroup = "rbac.authorization.k8s.io"
	rb.RoleRef.Kind = "Role"
	rb.RoleRef.Name = name
	rb.Subjects = []struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}{{Kind: "ServiceAccount", Name: name, Namespace: namespace}}
	if verr := createManifest(ctx, s, rb); verr != nil {
		return nil, verr.WithHint("the ServiceAccount and Role were created but the RoleBinding was not — " +
			"`kube.serviceaccount.revoke " + name + " -n " + namespace + "` cleans up both")
	}

	tokenOut, verr := run(ctx, s.args("create", "token", name, "--duration="+ttlStr)...)
	if verr != nil {
		return nil, verr.WithHint("the ServiceAccount, Role and RoleBinding were created but no token " +
			"was minted — `kube.serviceaccount.revoke " + name + " -n " + namespace + "` cleans up all three")
	}
	token := strings.TrimSpace(string(tokenOut))

	rawCfg, verr := readRawClusterConfig(ctx)
	if verr != nil {
		return nil, verr
	}
	coords, verr := coordinatesFor(rawCfg, s)
	if verr != nil {
		return nil, verr
	}
	kubeconfigYAML, verr := assembleKubeconfig(coords, name, namespace, token)
	if verr != nil {
		return nil, verr
	}

	grantedExpiry := "unknown — could not read the token's own expiry"
	if exp, ok := tokenExpiry(token); ok {
		grantedExpiry = exp.Format(time.RFC3339)
		if exp.Sub(now) < ttl {
			// The cluster's own service-account-max-token-expiration clamped
			// the request silently — the token itself is the only place that
			// shows up, so surface it rather than let --ttl's promise stand
			// unchallenged.
			grantedExpiry += " (shorter than the --ttl requested — this cluster's own token expiry ceiling clamped it)"
		}
	}

	summary := view.KeyValue{Pairs: []view.Pair{
		{Key: "serviceaccount", Value: name},
		{Key: "namespace", Value: namespace},
		{Key: "granted", Value: strings.Join(req.StringSlice("grant"), ", ")},
		{Key: "requested ttl", Value: ttlStr},
		{Key: "actual token expiry", Value: grantedExpiry},
	}}

	if out == "" {
		return view.Sections{Items: []view.Section{
			{ID: "summary", Title: "summary", View: summary},
			{ID: "kubeconfig", Title: "kubeconfig", View: view.Text{Body: kubeconfigYAML}},
		}}, nil
	}

	path := expandHome(out)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, view.Errorf("kube.serviceaccount.out.unwritable", "creating %s: %v", filepath.Dir(path), err)
	}
	if verr := writeKubeconfig(path, []byte(kubeconfigYAML), force); verr != nil {
		return nil, verr
	}
	summary.Pairs = append(summary.Pairs, view.Pair{Key: "wrote kubeconfig to", Value: path})
	return summary, nil
}

// dryRunProvision describes what would be created, before any cluster call —
// including the token mint, which is why this branches ahead of everything
// in runServiceAccountProvision rather than only around the final write, the
// way cert.pem's --out dry-run does: every step here is a mutation, not just
// the last one.
func dryRunProvision(name, namespace string, rules []policyRule, ttlStr string) view.View {
	ruleLines := make([]string, len(rules))
	for i, r := range rules {
		ruleLines[i] = strings.Join(r.APIGroups, ",") + "/" + strings.Join(r.Resources, ",") +
			": " + strings.Join(r.Verbs, ",")
	}
	return view.Text{Body: "would create ServiceAccount/Role/RoleBinding " + name + " in namespace " +
		namespace + ", mint a token valid for " + ttlStr + ", and return the assembled kubeconfig.\n\n" +
		"Role rules:\n  " + strings.Join(ruleLines, "\n  ")}
}

// createManifest JSON-encodes obj and hands it to `kubectl create -f -`.
// create, not apply: this plugin never wants "update the existing thing
// with this name", it wants "fail loudly if something with this name is
// already here" — see the ServiceAccount collision comment above.
func createManifest(ctx context.Context, s selection, obj any) *view.Error {
	body, err := json.Marshal(obj)
	if err != nil {
		return view.Errorf("kube.serviceaccount.encode", "building the manifest: %v", err)
	}
	_, verr := runStdin(ctx, body, s.args("create", "-f", "-")...)
	return verr
}

// provisionedSAItem is the shape read back for kube.serviceaccount.list —
// only what the annotations provision itself wrote need reading.
type provisionedSAItem struct {
	Metadata struct {
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace"`
		CreationTimestamp time.Time         `json:"creationTimestamp"`
		Annotations       map[string]string `json:"annotations"`
	} `json:"metadata"`
}

func runServiceAccountList(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	raw, verr := run(ctx, s.args("get", "serviceaccounts",
		"-l", provisionedByLabel+"="+provisionedByValue, "-o", "json")...)
	if verr != nil {
		return nil, verr
	}
	var sas list[provisionedSAItem]
	if err := json.Unmarshal(raw, &sas); err != nil {
		return nil, view.Errorf("kube.unreadable", "kubectl's answer for serviceaccounts could not be read: %v", err)
	}

	t := view.Table{Columns: []view.Column{
		{Name: "Namespace"}, {Name: "Name"}, {Name: "Age"}, {Name: "TTL"}, {Name: "Status", Kind: view.KindStatus},
	}}
	for _, item := range sas.Items {
		ttlStr := item.Metadata.Annotations["rta.dev/ttl"]
		issuedStr := item.Metadata.Annotations["rta.dev/issued-at"]
		status := "unknown — not stamped at provision time"
		if ttlStr != "" && issuedStr != "" {
			if ttl, err := time.ParseDuration(ttlStr); err == nil {
				if issued, err := time.Parse(time.RFC3339, issuedStr); err == nil {
					if time.Now().After(issued.Add(ttl)) {
						status = "likely expired"
					} else {
						status = "likely active"
					}
				}
			}
		}
		t.Rows = append(t.Rows, []string{
			item.Metadata.Namespace, item.Metadata.Name, age(item.Metadata.CreationTimestamp), ttlStr, status,
		})
	}
	t.Total = len(t.Rows)
	return t, nil
}

func runServiceAccountRevoke(ctx context.Context, req plugin.Request) (view.View, error) {
	name, namespace, verr := validatedIdentity(req)
	if verr != nil {
		return nil, verr
	}
	// selectionOf reads "namespace" itself, the same field validatedIdentity
	// already checked — s.Namespace is that same validated value.
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}

	raw, verr := run(ctx, s.args("get", "serviceaccount", name, "-o", "json")...)
	if verr != nil {
		if verr.Code == "kube.notfound" {
			return nil, view.Errorf("kube.serviceaccount.notfound",
				"no ServiceAccount named %q in namespace %q", name, namespace)
		}
		return nil, verr
	}
	var sa struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &sa); err != nil {
		return nil, view.Errorf("kube.unreadable", "reading %s: %v", name, err)
	}
	if sa.Metadata.Labels[provisionedByLabel] != provisionedByValue {
		return nil, view.Errorf("kube.serviceaccount.notprovisioned",
			"%q was not created by kube.serviceaccount.provision", name).
			WithHint("this only deletes ServiceAccounts it minted itself — remove an unrelated one with plain kubectl")
	}
	if req.DryRun {
		return view.Text{Body: "would delete ServiceAccount/Role/RoleBinding " + name +
			" in namespace " + namespace + " — every token minted against it would stop working immediately"}, nil
	}

	// The ServiceAccount's own label was just checked above, but the Role and
	// RoleBinding sharing its name were not — a real gap a review caught: this
	// deleted them by bare name with no ownership check of their own. Reached
	// in practice by provision's own recovery hint (a failed create of one of
	// these, e.g. a name collision with something this plugin never made,
	// tells the operator to run this exact revoke to clean up) — which would
	// otherwise have deleted the unrelated object it collided with. Checked
	// per-object now, and skipped rather than deleted when it exists but is
	// not this plugin's, the same refusal the ServiceAccount itself already
	// gets, just not escalated to a hard failure: the ServiceAccount — the
	// object that actually matters for "every token stops working" — still
	// gets deleted either way.
	var skipped []string
	for _, kind := range []string{"rolebinding", "role"} {
		owned, verr := ownedByProvision(ctx, s, kind, name)
		if verr != nil {
			return nil, verr
		}
		if !owned {
			skipped = append(skipped, kind)
			continue
		}
		if _, verr := run(ctx, s.args("delete", kind, name)...); verr != nil && verr.Code != "kube.notfound" {
			return nil, verr
		}
	}
	if _, verr := run(ctx, s.args("delete", "serviceaccount", name)...); verr != nil && verr.Code != "kube.notfound" {
		return nil, verr
	}

	body := "revoked " + name + " in namespace " + namespace +
		" — its ServiceAccount is gone, and every token minted against it stops working immediately"
	if len(skipped) == 0 {
		body += "; its Role and RoleBinding are gone too"
	} else {
		body += ".\n\nLeft alone: " + strings.Join(skipped, " and ") + " named " + name +
			" exist but were not created by kube.serviceaccount.provision, so this did not touch them"
	}
	return view.Text{Body: body}, nil
}

// ownedByProvision reports whether the named object exists and carries this
// plugin's own provisioning label. "Does not exist" and "exists but is not
// this plugin's" are both false — the caller decides what each means
// (skip-and-note vs. tolerate-as-already-gone), which is why this returns a
// bool rather than folding the two into one error.
func ownedByProvision(ctx context.Context, s selection, kind, name string) (bool, *view.Error) {
	raw, verr := run(ctx, s.args("get", kind, name, "-o", "json")...)
	if verr != nil {
		if verr.Code == "kube.notfound" {
			return false, nil
		}
		return false, verr
	}
	var obj struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return false, view.Errorf("kube.unreadable", "reading %s/%s: %v", kind, name, err)
	}
	return obj.Metadata.Labels[provisionedByLabel] == provisionedByValue, nil
}

// writeKubeconfig puts the minted credential at path, refusing to replace
// whatever is already there unless the operator said to.
//
// **Refusing is the majority rule for a credential in this tree**, not a
// preference: pg's backup, vault's snapshot, s3's object and download, and
// kv's crypt all open their --out O_EXCL and refuse. cert.pem overwrites, and
// is the reason this originally did too — but cert.pem writes a public
// certificate, which is not the same kind of thing as a bearer token. The file
// a silent overwrite destroys here is most often a working kubeconfig, and by
// the time this runs the ServiceAccount backing the new token already exists
// on the cluster, so the mistake is not recoverable by re-running: that name
// is taken now.
//
// --force follows net.resolver.set's own `force` field rather than inventing a
// spelling for the same idea. Forcing goes through writeAtomically so that
// replacing is still all-or-nothing; refusing does not need to, because
// O_EXCL's guarantee is that there was nothing to lose.
func writeKubeconfig(path string, data []byte, force bool) *view.Error {
	if force {
		if err := writeAtomically(path, data, 0o600); err != nil {
			return view.Errorf("kube.serviceaccount.out.unwritable", "writing %s: %v", path, err)
		}
		return nil
	}
	// 0600 at creation rather than chmod'd after, so there is no instant where
	// the file holds a token at a wider mode. Not cert.pem's 0644: a
	// certificate is public by design, a bearer token is not.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return view.Errorf("kube.serviceaccount.out.exists", "%s already exists", path).
			WithHint("name a path that does not exist yet, or pass --force to replace it")
	}
	if err != nil {
		return view.Errorf("kube.serviceaccount.out.unwritable", "creating %s: %v", path, err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return view.Errorf("kube.serviceaccount.out.unwritable", "writing %s: %v", path, err)
	}
	return nil
}

// writeAtomically is internal/atomicfile.Write, restated here for the same
// reason expandHome below is: this plugin is its own Go module and cannot
// import an internal package, however small.
//
// Worth the duplication rather than a plain os.WriteFile, which truncates
// before it writes. --out is frequently an existing kubeconfig, and a
// truncating write that fails partway leaves the operator with neither the
// credential they just minted nor the one that was there before — at a moment
// when the ServiceAccount backing the new token already exists on the cluster,
// so the failure is not even recoverable by re-running. Temp-file-plus-rename
// makes the file either wholly old or wholly new, and never a half-written
// credential another process can read.
//
// No Sync, matching atomicfile's own measured decision: rename already covers
// a dying process, and surviving power loss is not what this is protecting.
func writeAtomically(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmp.Name(), err)
	}
	// Chmod before the rename, never after: the window where a credential sits
	// at the temp file's own mode is the window this is closing.
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return fmt.Errorf("setting permissions on %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// expandHome mirrors builtin/cert/cert.go's own helper — the shell expands an
// unquoted ~, not a quoted one, and --out is exactly the flag somebody
// quotes. Duplicated rather than imported: cert lives in a different Go
// module, unreachable from here the same way every other cross-boundary
// helper in this plugin already is.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}
