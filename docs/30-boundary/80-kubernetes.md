# Kubernetes

[What rta actually bounds](./10-the-boundary.md) names three worlds. In the first two the agent runs on your machine, and moving between them is a setting in the agent rather than a feature request here. The third is different: the agent runs somewhere else entirely, holds no credentials of its own, and reaches your environments only through rta.

That one is not a setting. It is a deployment, and this chapter is how to make it.

[`charts/rta-chart`](https://github.com/this-is-tobi/rta/tree/main/charts/rta-chart) is the chart. Its README is the values reference — every key, every default — and this page deliberately does not repeat it. What is here is the part a values table cannot tell you: the decisions to make before you set anything, the posture worth deploying, and what changes on day two.

## The unit is the instance, not the release

[Why not one server for everyone](./20-mcp.md#why-not-one-server-for-everyone) is the argument, and it is the single thing to internalise before writing a values file. A shared server costs per-person access control, a record that can say who did what, live consent that reaches the right person, `rta use`, and blast radius.

So the chart's unit is the instance. One entry per person under `servers:`, each authenticating as that person, each with its own ServiceAccount, its own data volume and its own record. A platform team owns one release; adding somebody is one entry, layered over `serverDefaults`.

The shape that looks tempting and is not is a single instance behind a load balancer with several people's tokens in it. It deploys, it works, and every line of the record then names the same principal — which is the one question the record exists to answer.

## Decisions to make first

**How callers prove who they are.** [OIDC](./70-oidc.md) makes "authenticating as that person" real, because the record then carries a subject your IdP resolves to a human. A static token names whoever holds the string, which is honest for a CI or bot instance and weak for a person. Both can be enabled together, and then the endpoint's strength is the weaker of the two.

**Whether a person can be reached.** `consent.enabled` parks a call until somebody answers it. Over HTTP the enrolled operators are the only people positioned to be that person, so consent requires the operator channel — rta refuses the pairing, and the chart refuses it at render time. If nobody is going to answer within `--consent-wait`, live consent is a timeout with extra steps; grants are the mechanism that fits an unattended instance.

**What the instance may reach on disk.** `roots` is required by the chart precisely because rta's own default is the working directory, which in a container is `/`. An instance that states nothing here has filesystem capabilities over its whole container.

**Which image.** The image *is* the plugin allowlist — the set of plugins baked in is the set of capabilities anything reaching this server can ever use. The narrow image plus the plugins that job needs is the boundary. `rta-full` carries everything, which is the widest reach rta has and is gated behind `image.allowFullImage` for that reason.

## Quickstart

```bash
helm install rta oci://ghcr.io/this-is-tobi/rta/rta-chart \
  --namespace rta --create-namespace \
  --values rta-values.yaml
```

```yaml
servers:
  tobi:
    host: rta-tobi.example.com       # ingress host AND the operator identity
    roots: ["/work"]
    auth:
      oidc:
        enabled: true
        issuer: https://keycloak.example.com/realms/main
        audience: rta
        subjects: ["8f14e45f-ceea-467a-9c1a-2d7c4f0b1a33"]
    operators:
      enabled: true
      keys:
        tobi:
          publicKey: "…"             # rta operator status prints the line
    consent:
      enabled: true
    ingress:
      enabled: true
      className: nginx
      tlsSecretName: rta-tobi-tls
```

Every combination rta would refuse at startup is refused at render time instead, naming the values key rather than arriving later as a CrashLoopBackOff: an issuer with no audience, consent without operators, an empty `roots`, a token under 16 characters, an operator URL that is not `https://`.

**The chart is published the way the image is.** Every release pushes it to `ghcr.io/this-is-tobi/rta/rta-chart` with SLSA build provenance and a cosign signature bound to the digest, the same pair the binaries and the image carry. It moves on its own version stream rather than the app's — a chart `0.2.0` deploying rta `0.17.0` — because a values default or a template fix is a chart release with no new rta in it; `appVersion` is where the app's version lives. `helm install` from a registry checks neither the provenance nor the signature on its own, so check before you install:

```bash
gh attestation verify oci://ghcr.io/this-is-tobi/rta/rta-chart:<version> --owner this-is-tobi
```

Then point the client at it — [Connecting your AI tool](./60-ai-clients.md) covers the per-client detail, and the server is an HTTP MCP endpoint like any other.

## Two log lines that look wrong and are not

**`… is group-readable — anyone in that group can authenticate as every label it holds`, on every start.** Kubernetes writes Secret files owned by `root` and the container runs as a non-root user, so the group is how the process is handed the credential at all. The tighter-looking `0400` is worse, not better: an owner-only file owned by root is one the process cannot open, trading a permission refusal for a missing credential. rta leaves group read outside its refusal mask for exactly this case.

**`rta doctor` reporting the data directory's permissions.** `fsGroup` is what makes the volume writable by the container. A doctor finding, not a refusal.

Both are the deployment working. Chasing either is an afternoon lost.

## The posture worth deploying

The chart's defaults are safe; these are the ones you have to choose.

- **One instance per person.** Repeated because every shortcut here is a shortcut past the record.
- **OIDC over static tokens** for anybody human. Keep static tokens for machines that cannot do an OIDC exchange — a Prometheus scraper is the honest example — and give them distinct labels so the record stays legible.
- **Pin the image by digest.** `image.digest` beats `image.tag` for the same reason `rta plugin trust` binds to a digest: a tag can be repointed at a different build, a digest cannot. On a security boundary that is not pedantry.
- **Turn on the NetworkPolicy, and write the egress rules.** It is off by default because only you know which databases an instance's profiles reach, and a chart that guessed would either allow everything or cut the server off in a way that surfaces as unexplained timeouts. The ingress rules matter less than the egress ones.
- **Keep the scraper off the MCP port.** `/metrics` sits behind the same verifier MCP does, so a token that reads the counters can call the protocol. Binding the observation listener privately is the outer control and the token is the inner one; a NetworkPolicy is what stops the outer one being decorative.
- **Watch `rta_record_intact`.** A record that stops verifying is either a bug or somebody editing it, and both are worth knowing about in minutes. `grafanaDashboard.enabled` ships a dashboard built around that series.
- **Do not raise replicas, and do not reach for ReadWriteMany.** The chart does not expose replicas and refuses RWX. Two rta processes on one data directory hold divergent grant rosters and split the record, so the audit trail answers questions *wrongly* rather than not at all. RWX would remove the rolling-update deadlock and change nothing else.
- **On OpenShift, set `openShift.enabled`.** It omits the UID, GID and fsGroup rather than setting them, because `restricted-v2` assigns a UID from the namespace's range and refuses a pod that asks for one outside it — the correct value is what blocks admission.

## Giving an instance access to the cluster it runs in

The `kube` and `cnpg` plugins are the obvious thing to want from a pod, and getting there is four problems of which RBAC is the last.

rta runs plugins with a deliberately narrow environment — `PATH`, `HOME`, `TMPDIR`, `TZ`, `LANG`, `SSL_CERT_*`, `LC_*` and nothing else. Those plugins shell out to `kubectl`, so `KUBECONFIG` never reaches them; and because `KUBERNETES_SERVICE_HOST` and `KUBERNETES_SERVICE_PORT` are stripped too, kubectl's own in-cluster fallback cannot fire either. A pod with a perfectly good projected ServiceAccount token gets *"there is no kubeconfig on this machine"*.

What works is to synthesise one. All four pieces are needed together:

1. **The full image**, or a derived image carrying `kubectl` — the narrow one is distroless and has no binary to run.
2. **`HOME` set explicitly**, since kubectl resolves only `$HOME/.kube/config`.
3. **A kubeconfig ConfigMap** pointing at the in-cluster endpoint and reading the projected token, mounted at `$HOME/.kube`.
4. **`automountServiceAccountToken: true`**, plus a ClusterRole bound to the instance's own ServiceAccount.

```yaml
serverDefaults:
  serviceAccount:
    automountServiceAccountToken: true
  plugins:
    allow: ["kube", "cnpg"]
  env:
    - name: HOME
      value: /home/rta
  extraVolumes:
    - name: kubeconfig
      configMap:
        name: rta-in-cluster-kubeconfig
  extraVolumeMounts:
    - name: kubeconfig
      mountPath: /home/rta/.kube
      readOnly: true

extraObjects:
  kubeconfig:
    apiVersion: v1
    kind: ConfigMap
    metadata:
      name: rta-in-cluster-kubeconfig
    data:
      config: |
        apiVersion: v1
        kind: Config
        clusters:
        - name: in-cluster
          cluster:
            server: https://kubernetes.default.svc
            certificate-authority: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
        users:
        - name: sa
          user:
            tokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
        contexts:
        - name: in-cluster
          context:
            cluster: in-cluster
            user: sa
        current-context: in-cluster
```

The ClusterRole is the part to think about rather than copy. `plugins/kube/rbac.go` in [rta-plugins](https://github.com/this-is-tobi/rta-plugins) already maps each capability to the rules it needs — it is what `kube.serviceaccount.provision` uses — and its header says what it deliberately leaves out. Two of those omissions are worth keeping out here too:

- **`nodes/proxy`**, needed by `kube.metrics.pressure` and `kube.pvc.usage`. It cannot be subdivided, and it is effectively code execution against every pod on the node. Left out, those two capabilities degrade per node with `could not be read — Forbidden` rather than failing the call, which is the right shape.
- **`secrets: get,list`**, needed by `kube.cert.list`. RBAC cannot scope a Secret rule by type, so granting it to read TLS Secrets grants reading all of them. Left out, the capability refuses with `kube.forbidden` and a hint naming the missing permission.

A read-only starting point, cluster-scoped because `namespace.list`, `node.list` and `metrics.node` are:

```yaml
rules:
  - apiGroups: [""]
    resources: ["namespaces", "nodes", "pods", "services", "events",
                "persistentvolumeclaims", "resourcequotas", "limitranges"]
    verbs: ["get", "list"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get", "list"]
  - apiGroups: ["metrics.k8s.io"]
    resources: ["pods", "nodes"]
    verbs: ["get", "list"]
  - apiGroups: ["postgresql.cnpg.io"]
    resources: ["clusters", "backups"]
    verbs: ["get", "list"]
```

Bind it to the instance's ServiceAccount — `<release>-<chart>-<instance>` — with a ClusterRoleBinding, and remember that this is the one place the per-person story leaks: a ClusterRole is cluster-wide, so two instances bound to the same one can see the same things. Narrow it per instance if that matters, and prefer a namespaced Role wherever the capability allows it.

Note this is a different mechanism from `kube:` credential references in a profile, which the main process resolves — also through `kubectl`, and also needing a kubeconfig with a named context.

## Day two

**What a values change actually restarts.** The roster, the token file and the config are all read once at startup, so the chart puts a checksum of each in the pod annotations and editing one restarts the pod. Without that, removing a key in Git would appear to do nothing — the pod would keep honouring the roster it read at boot.

**The one exception is `expires=`.** An operator key's expiry is checked per call against the running clock, so an expired key stops working with no restart, and restarting will not be what evicted it.

**Rotating a credential** is an edit to `auth.tokens.entries` or `operators.keys` and a `helm upgrade`; the checksum does the rest. Revoking is removing the entry — and if you need it gone *now* rather than at next rollout, `rta lock` is the mechanism that does not wait for a deployment.

**Backups are the data volume.** The grants and the record live there and nothing else does. A restored volume is a restored instance; a lost one is a machine with no memory of what you allowed, which is why the chart has no switch for running without one.

**Upgrading rta** is the image tag or digest, and the `Recreate` strategy means a short gap rather than an overlap — deliberately, since two processes briefly sharing one data directory is the thing being avoided.

**When a pod will not start**, the useful order is: `kubectl logs` first, because every credential failure prints there and returns a generic 401 to the caller; then `rta doctor` in the pod; then the two log lines above that are not problems.
