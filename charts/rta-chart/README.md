# rta-chart

Deploy rta MCP servers into Kubernetes, one instance per person.

![Version: 0.2.0](https://img.shields.io/badge/Version-0.2.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.17.0](https://img.shields.io/badge/AppVersion-0.17.0-informational?style=flat-square)

## What this deploys

rta's documentation names three worlds. In the first, the agent runs on your machine as you. In the second, it runs on your machine as something narrower. In the third it runs somewhere else entirely, with no credentials of its own, reaching your environments only through rta. The first two are a setting in the agent. The third is a deployment, and this chart is it.

Its unit is the **instance, not the release**. One server for everyone is what the design rejects: a shared instance costs per-person access control, a record that can say who did what, live consent that reaches the right person, `rta use`, and blast radius. So a platform team owns one release, and adding a person is one entry under `servers`, layered over `serverDefaults`.

Per instance: a Deployment, a Service, a ServiceAccount, a PersistentVolumeClaim, a ConfigMap, the token and roster Secrets, and optionally RBAC, an Ingress or HTTPRoute, a NetworkPolicy, a metrics Service and a ServiceMonitor.

**This README is the values reference.** The decisions to make before setting any of them, the posture worth deploying, how to give an instance access to the cluster it runs in, and what changes on day two are in [Kubernetes](https://github.com/this-is-tobi/rta/blob/main/docs/30-boundary/80-kubernetes.md).

## Quickstart

```bash
helm install rta oci://ghcr.io/this-is-tobi/rta/rta-chart \
  --namespace rta --create-namespace \
  --values my-values.yaml
```

A minimal `my-values.yaml` for one person authenticating through an IdP:

```yaml
servers:
  tobi:
    host: rta-tobi.example.com     # the Ingress host AND the operator identity
    roots: ["/work"]               # required: the path root otherwise defaults to "/"
    auth:
      oidc:
        enabled: true
        issuer: https://id.example.com
        audience: rta
        subjects: ["tobi@example.com"]
    operators:
      enabled: true
      keys:
        tobi:
          publicKey: "…"           # `rta operator status` prints the line to paste
    consent:
      enabled: true
    ingress:
      enabled: true
      className: nginx
      tlsSecretName: rta-tobi-tls
```

Values combinations that rta would refuse at startup are refused at render time instead, naming the key to fix — an OIDC issuer with no audience, `consent` without `operators`, an empty `roots`, a token under 16 characters, an operator URL that is not `https://`.

Setting up the identity provider is its own subject, including the Keycloak audience mapper without which every token is rejected: see [OIDC](https://github.com/this-is-tobi/rta/blob/main/docs/30-boundary/70-oidc.md).

## Two things in the logs that look wrong and are not

**`… is group-readable — anyone in that group can authenticate as every label it holds`, on every start.** This is the chart working. Kubernetes writes Secret volume files owned by `root`; the container runs as a non-root user. The obvious fix, `defaultMode: 0400`, is the wrong one: an owner-only file owned by root is one the process cannot open at all, trading a permission refusal for a missing credential. The chart mounts `0440` with an `fsGroup`, and rta leaves group-read outside its refusal mask precisely so a shared-group file can hand a credential to a service account. It warns and starts.

**`rta doctor` reporting the data directory's permissions.** `fsGroup` makes the volume group-writable, and that is what makes it writable by the container at all. A doctor finding, not a startup refusal.

## Storage, replicas and access modes

`replicas: 1` and `strategy: Recreate` are not defaults — they are not values at all.

Two pods on one ReadWriteOnce volume cannot co-mount it. Two pods on separate volumes hold divergent grant rosters and split the record, so a grant issued over the operator channel lands on one of them and the audit trail answers questions *wrongly* rather than not at all. And a rolling update on a single RWO volume deadlocks, because the new pod waits for a volume the old one has not released.

**ReadWriteMany is refused.** It would remove the deadlock and change none of the rest: two rta processes on one data directory still split the record. A shared-writer volume here makes a claim about the workload that is not true, and invites the conclusion that replicas can be raised — which this chart cannot catch, because `replicas` is not a value it reads. If RWX is the only storage class you have, set `persistence.existingClaim` to a claim you made yourself; that keeps the decision with the person who knows their storage.

There is deliberately no switch for running without a volume either. The grants and the record live there, and a restart without them is a machine with no memory of what you allowed.

## OpenShift

Set `openShift.enabled: true`. The chart then omits `runAsUser`, `runAsGroup` and `fsGroup` rather than setting them: the `restricted-v2` SCC assigns a UID from the namespace's own range and refuses a pod asking for one outside it, so the 65532 both official images run as — correct on every other distribution — is what would stop the pod being admitted. With the fields removed, SCC admission fills all three in, the Secret files stay readable through the group the process actually runs with, and the SCC's own `fsGroup` strategy chowns the data volume.

The Ingress is turned into a Route by the ingress operator, so no separate template is needed.

## Observability

`--observe` binds a **second listener** carrying `/livez`, `/readyz`, `/healthz` and `/metrics`. It is separate from the MCP listener on purpose: bearer authentication wraps the whole protocol handler there, and open paths beside it would turn a structural property into a matter of route matching. On the MCP port every path answers `401`, including `/livez`.

The probes are open and target that second port. `/livez` consults nothing — a liveness probe wired to the store would ask for a restart that comes back to the same detached volume. `/readyz` writes to the data directory and reports whether the record can be written at all, which is what notices a volume that went read-only under a running server.

`/metrics` sits behind the **same bearer check MCP does**. Be plain about what that means: there is one verifier, so a token that can read the counters is a token that can call the protocol. Binding the observation port privately is the outer control and the token is the inner one — keep the scraper off the MCP port with a NetworkPolicy rather than assuming this credential is metrics-only. With `observability.serviceMonitor.enabled`, the chart points `bearerTokenSecret` at the token labelled `observability.serviceMonitor.tokenLabel`, and refuses to render a ServiceMonitor that has nowhere to read a credential from.

### The bundled dashboard

`grafanaDashboard.enabled` ships a Grafana dashboard as a ConfigMap for a sidecar to pick up — one for the whole release, with an instance variable rather than a copy per server. Three rows, in the order an operator actually asks them:

- **Is the record trustworthy** — `rta_record_intact` is the series to alert on. A record that stops verifying is either a bug or somebody editing it. Beside it: calls parked waiting for a person, and what the record weighs on disk.
- **What agents are doing** — calls by outcome (`ran` / `failed` / `refused`), refusals by capability, busiest capabilities, calls by agent. `refused` is the boundary biting, not an error rate.
- **What authorised it** — calls by `open` / `grant` / `approved` / `declined` / `blocked` / `operator`, and the grants in force right now. That last table is the one to read before a review, and it should be shorter than you expect.

It needs the metrics to actually reach Prometheus, so `observability.metrics.enabled` and `observability.serviceMonitor.enabled` too — and those need a token, because `/metrics` is authenticated.

## The full image

`ghcr.io/this-is-tobi/rta-full` carries every first-party plugin and every external tool, which makes it the widest reach rta has — the image *is* the plugin allowlist, and that one allows everything. For an instance an agent talks to unattended, build a narrow image with the plugins that job needs. For a homelab exercising `kube`, `cnpg` and the rest against real infrastructure, it is the honest choice, and `image.allowFullImage: true` lifts the refusal.

It also requires `persistence.seedFromImage`. The full image records plugin **trust** inside its own data directory, and this chart's volume mounts over that path — without seeding, every plugin comes up `untrusted`, `rta cnpg list` answers `Unknown command "cnpg"`, and it reads as a broken image rather than a hidden file. An init container copies the image's data directory into the volume once, on first start.

`plugins.allow` renders `RTA_ALLOW_PLUGINS`, which answers a different question: whether a plugin may reach a credential location. Only an image using rta's `docker-entrypoint.sh` reads it.

## Kubernetes secret references

`serviceAccount.automountServiceAccountToken` defaults to `false`, and `secretRefs` — which narrows the instance's Role to named secrets — does nothing on the default image. rta resolves a `kube:<secret>/<key>` reference by shelling out to `kubectl` against a kubeconfig naming a context; the published narrow image is distroless and has neither. Those values earn their place on a derived image that ships `kubectl` and a kubeconfig, where they are what keeps one instance from reading another person's credentials.

## Values

## Values

### General

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| commonLabels | object | `{}` | Labels added to every resource the chart deploys. |
| enabled | bool | `true` | Master switch for the whole chart. When `false`, every template renders nothing - use this to keep a release/namespace registered with a deployment system (e.g. an ArgoCD Application that is always generated for every app/env combination) without deploying any resource into it. |
| extraObjects | object | `{}` | Map of extra manifests to deploy with the release. Each key is an arbitrary name, used only so a values override can add or remove a single entry by key instead of restating the whole list. |
| fullnameOverride | string | `""` | String to fully override the default fully-qualified name. |
| grafanaDashboard.annotations | object | `{}` | Extra annotations for the dashboard ConfigMap. |
| grafanaDashboard.enabled | bool | `false` | Ship the bundled Grafana dashboard as a ConfigMap for a dashboard sidecar to pick up.  One dashboard for the whole release rather than one per instance: it carries a `server` variable built from the metrics' own `service` label, so every instance in the release is a selection rather than a separate copy to keep in step.  It needs the metrics to be reaching Prometheus, which means `observability.metrics.enabled` and `observability.serviceMonitor.enabled` on the instances you want to see - and those in turn need a token, since `/metrics` is authenticated. |
| grafanaDashboard.folder | string | `""` | Grafana folder to file the dashboard under, via the `grafana_folder` annotation. |
| grafanaDashboard.label | string | `"grafana_dashboard"` | Label a dashboard sidecar watches for. The default is what kube-prometheus-stack and the Grafana chart use. |
| grafanaDashboard.labelValue | string | `"1"` | Value of that label. |
| grafanaDashboard.namespace | string | `""` | Namespace to place the ConfigMap in. Empty uses the release namespace; set it when Grafana's sidecar only watches its own. |
| nameOverride | string | `""` | Provide a name in place of the default chart name. |
| servers | object | `{}` | The instances this release deploys, one entry per person. Each key names the instance and, by default, the identity it authenticates as; each entry is layered over `serverDefaults`.  One server for everyone is what this shape exists to avoid. A shared instance costs per-person access control, a record that can say who did what, live consent that reaches the right person, `rta use`, and blast radius - so the unit here is the instance, not the release. A platform team owns one release; adding a person is one entry. |

### Global

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| global.imagePullSecrets | list | `[]` | Image pull secrets applied to every pod. |
| global.imageRegistry | string | `""` | Global container image registry, overriding `image.registry` for every pod in the chart. |
| openShift.enabled | bool | `false` | Render for OpenShift, which means dropping `runAsUser`, `runAsGroup` and `fsGroup` from the security contexts rather than setting them.  The `restricted-v2` SCC assigns a UID from the namespace's own range and refuses a pod asking for one outside it, so the 65532 both official images run as - correct on every other distribution - is what stops the pod being admitted here. Removed, SCC admission fills all three in, and the two properties the chart depends on still hold: the mounted Secret files stay readable through the group the process actually runs with, and SCC's own fsGroup strategy chowns the data volume.  Left as an explicit switch rather than detected from `.Capabilities`, because the rendering must be the same whether it happens in a cluster, in `helm template`, or in Argo CD - a chart that quietly renders two different pod specs depending on where it ran is one whose diff cannot be reviewed. |

### Image

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| image.allowFullImage | bool | `false` | Permit `ghcr.io/this-is-tobi/rta-full`, which the chart otherwise refuses.  The full image carries every first-party plugin and every external tool, which makes it the widest reach rta has - the documentation is explicit that it is the wrong thing to point an agent at, because the image IS the plugin allowlist and this one allows everything. For a person driving a console, or for a homelab where the point is to exercise `kube`, `cnpg` and the rest against real infrastructure, it is the honest choice; for an instance an agent talks to unattended, build a narrow image with the plugins that job needs.  Setting this to `true` has three effects: it lifts the refusal, it requires `serverDefaults.persistence.seedFromImage` (the full image bakes plugin *trust* into its own data directory, which the data volume would otherwise mask), and it makes `serverDefaults.plugins.allow` meaningful, since only that image's entrypoint reads it.  The seeding requirement is keyed off `repository` actually naming the published full image (matched by path, so a registry mirror is still recognised), not off this flag directly - a renamed image built from the full one would not be caught by that check even with this set. |
| image.digest | string | `""` | Image digest (`sha256:...`). Takes precedence over `tag`, pinning exact image content so a release can never resolve to a different build. Preferred over a mutable tag - it is the same reason `rta plugin trust` binds to a digest and never to a name. |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy. |
| image.registry | string | `"ghcr.io"` | Registry the rta image is pulled from. |
| image.repository | string | `"this-is-tobi/rta"` | Repository of the rta image. Defaults to the narrow image, which is the boundary: it carries rta and nothing else, so the set of plugins baked into it is the set of capabilities an agent reaching this server can ever use. See `allowFullImage` before pointing this at `rta-full`. |
| image.tag | string | `""` | Image tag. Defaults to the chart's appVersion. |

### Server defaults

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| serverDefaults | object | `{"affinity":{},"agentName":"","auth":{"oidc":{"audience":"","enabled":false,"issuer":"","subjects":[]},"tokens":{"enabled":false,"entries":{},"existingSecret":"","key":"tokens"}},"config":{},"consent":{"enabled":false,"wait":""},"containerPort":8080,"dnsConfig":{},"dnsPolicy":"","enabled":true,"env":[],"envFrom":[],"existingConfigMap":"","extraArgs":[],"extraContainers":[],"extraRules":[],"extraVolumeMounts":[],"extraVolumes":[],"host":"","hostAliases":[],"httpRoute":{"annotations":{},"enabled":false,"labels":{},"parentRefs":[],"rules":[]},"imagePullSecrets":[],"ingress":{"annotations":{},"className":"","enabled":false,"labels":{},"path":"/","pathType":"Prefix","tls":[],"tlsSecretName":""},"initContainers":[],"networkPolicy":{"annotations":{},"create":false,"egress":[],"ingress":[],"labels":{},"policyTypes":["Ingress"]},"nodeSelector":{},"observability":{"enabled":true,"metrics":{"annotations":{},"enabled":false},"port":9090,"serviceMonitor":{"annotations":{},"bearerTokenSecret":{"key":"","name":""},"enabled":false,"interval":"30s","labels":{},"metricRelabelings":[],"relabelings":[],"scrapeTimeout":"10s","tokenLabel":"prometheus"}},"operators":{"dashboard":{"expires":"","label":"dashboard","publicKey":""},"enabled":false,"existingSecret":"","key":"roster","keys":{},"url":""},"paths":{"configDir":"/etc/rta/config","dataDir":"/rta-home","rosterDir":"/etc/rta/roster","tokenDir":"/etc/rta/tokens"},"persistence":{"accessModes":["ReadWriteOnce"],"annotations":{},"existingClaim":"","seedFromImage":false,"seedSourcePath":"/rta-home","size":"1Gi","storageClass":""},"plugins":{"allow":[]},"podAnnotations":{},"podDisruptionBudget":{"annotations":{},"enabled":false,"maxUnavailable":1,"minAvailable":""},"podLabels":{},"podSecurityContext":{"fsGroup":65532,"fsGroupChangePolicy":"OnRootMismatch","runAsGroup":65532,"runAsNonRoot":true,"runAsUser":65532,"seccompProfile":{"type":"RuntimeDefault"}},"priorityClassName":"","probes":{"livenessProbe":{"failureThreshold":3,"httpGet":{"path":"/livez","port":"observe"},"periodSeconds":30,"timeoutSeconds":3},"readinessProbe":{"httpGet":{"path":"/readyz","port":"observe"},"periodSeconds":10,"timeoutSeconds":3},"startupProbe":{"failureThreshold":30,"httpGet":{"path":"/readyz","port":"observe"},"periodSeconds":2}},"resources":{"limits":{"cpu":"500m","memory":"256Mi"},"requests":{"cpu":"50m","memory":"64Mi"}},"revisionHistoryLimit":10,"roots":[],"secretRefs":[],"securityContext":{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"privileged":false,"readOnlyRootFilesystem":true,"runAsGroup":65532,"runAsNonRoot":true,"runAsUser":65532},"service":{"annotations":{},"port":80,"type":"ClusterIP"},"serviceAccount":{"annotations":{},"automountServiceAccountToken":false,"create":true,"name":""},"terminationGracePeriodSeconds":null,"tolerations":[],"topologySpreadConstraints":[]}` | Values every entry in `servers` inherits, deep-merged under it. This is what keeps adding a person to one values entry: the platform team states storage class, ingress class, resources, OIDC issuer and the rest once here, and a person's own entry carries only what is actually theirs - their name, their host, their subject, their roots. |
| serverDefaults.affinity | object | `{}` | Affinity for the pod. |
| serverDefaults.agentName | string | `""` | The identity passed to `--as`, which every line of the record is written against. Defaults to the entry's key in `servers`. 1-64 characters of `[A-Za-z0-9._-]`. |
| serverDefaults.auth.oidc.audience | string | `""` | Expected audience. Required when `enabled` - rta refuses an issuer with no audience, because a token minted for some other service would otherwise authenticate here. |
| serverDefaults.auth.oidc.enabled | bool | `false` | Authenticate callers against an OIDC issuer. This is what makes "authenticating as that person" real, rather than "whoever holds this string". |
| serverDefaults.auth.oidc.issuer | string | `""` | Issuer URL. Required when `enabled`. |
| serverDefaults.auth.oidc.subjects | list | `[]` | Accepted subjects. At least one is required when `enabled`: an issuer with no subject accepts every identity that IdP will ever mint. |
| serverDefaults.auth.tokens.enabled | bool | `false` | Authenticate callers with static bearer tokens. Honest for a CI or bot instance, and for anyone without an IdP; a token names whoever holds it, not a person. |
| serverDefaults.auth.tokens.entries | object | `{}` | Map of `label: token`. Each label is an identity in the record; tokens must be at least 16 characters (`rta gen token` makes one). These land in the values file AND in the Helm release secret in plain text - for anything you would not paste in a pull request, use `existingSecret` and have your secret manager materialise it. |
| serverDefaults.auth.tokens.existingSecret | string | `""` | Use a Secret managed outside this chart for the token file instead of rendering one from `entries`. It must hold the file under the `key` below, one `label token` per line. The pod's `checksum/tokens` annotation is computed from `entries`, so it cannot see an edit to an externally-managed Secret - restart the pod yourself when its content changes. |
| serverDefaults.auth.tokens.key | string | `"tokens"` | Key inside the token Secret holding the token file. |
| serverDefaults.config | object | `{}` | The contents of `config.yaml` - profiles, plugins, dashboard. Rendered into a ConfigMap and pointed at by `RTA_CONFIG`. Validated against rta's own config schema (see `values.schema.json`, whose `config` block is generated by `rta config schema`), which states the envelope; `rta doctor` remains the deep validator for what a plugin section may hold. A connection's `secrets:` map takes a reference, never a value (`kv:<entry>` or `kube:<secret>/<key>`) - this renders into a ConfigMap, not a Secret, so anything pasted here literally lands in plain text in the values file and the Helm release object. |
| serverDefaults.consent.enabled | bool | `false` | Park a call and wait for a person to answer it. Over HTTP this requires `operators`, since the enrolled operators are the only people positioned to be that person. |
| serverDefaults.consent.wait | string | `""` | How long a parked call waits (`--consent-wait`). rta's own default is 90s and it clamps anything above 10m. |
| serverDefaults.containerPort | int | `8080` | Port the MCP listener binds inside the pod. |
| serverDefaults.dnsConfig | object | `{}` | Pod DNS configuration, merged with `dnsPolicy` by the kubelet. |
| serverDefaults.dnsPolicy | string | `""` | Pod DNS policy. Empty leaves the Kubernetes default (`ClusterFirst`). |
| serverDefaults.enabled | bool | `true` | Render this instance. Set `false` on one entry to retire a person's server without deleting the values that describe it. |
| serverDefaults.env | list | `[]` | Environment variables for the rta container, in native Kubernetes list form. `RTA_CONFIG` and `RTA_DATA_DIR` are set by the chart and are not overridable here - with no config path rta falls back to a file it does not honour, and profiles, plugins and dashboard would be silently ignored. |
| serverDefaults.envFrom | list | `[]` | ConfigMap/Secret references loaded into the container's environment. |
| serverDefaults.existingConfigMap | string | `""` | Take `config.yaml` from a ConfigMap managed outside this chart instead of rendering one. The key must be `config.yaml`. The pod's `checksum/config` annotation is computed from `config`, so it cannot see an edit to an externally-managed ConfigMap - restart the pod yourself when its content changes. |
| serverDefaults.extraArgs | list | `[]` | Extra arguments appended to `rta mcp serve`. |
| serverDefaults.extraContainers | list | `[]` | Additional sidecar containers. |
| serverDefaults.extraRules | list | `[]` | Additional rules appended to this instance's Role. Rendered verbatim, with no validation of verbs or resources - unlike `secretRefs`, which is pinned to `get` on named Secrets by name. Still namespace-scoped only, since this chart never renders a ClusterRole: the worst an entry here can do is widen what this one instance's ServiceAccount reaches inside its own namespace. |
| serverDefaults.extraVolumeMounts | list | `[]` | Additional volume mounts for the rta container. |
| serverDefaults.extraVolumes | list | `[]` | Additional volumes. |
| serverDefaults.host | string | `""` | The public hostname this instance is served on. It is both the Ingress/HTTPRoute host and, unless `operators.url` overrides it, the `--operators-url` identity - so it is the exact string operators write in their `remotes.yaml`, never an in-cluster Service name. |
| serverDefaults.hostAliases | list | `[]` | Host aliases injected into the pod's /etc/hosts. Useful when a profile names an internal host the cluster's DNS does not resolve. |
| serverDefaults.httpRoute.annotations | object | `{}` | Annotations for the HTTPRoute. |
| serverDefaults.httpRoute.enabled | bool | `false` | Create a Gateway API HTTPRoute for this instance, as an alternative to `ingress`. |
| serverDefaults.httpRoute.labels | object | `{}` | Additional HTTPRoute labels. |
| serverDefaults.httpRoute.parentRefs | list | `[]` | Gateways to attach the HTTPRoute to. |
| serverDefaults.httpRoute.rules | list | `[]` | Rules for the HTTPRoute. Left empty, one rule is generated routing `/` to this instance's Service. |
| serverDefaults.imagePullSecrets | list | `[]` | Image pull secrets for this instance's pod, in addition to `global.imagePullSecrets`. |
| serverDefaults.ingress.annotations | object | `{}` | Ingress annotations. |
| serverDefaults.ingress.className | string | `""` | Ingress class. |
| serverDefaults.ingress.labels | object | `{}` | Additional Ingress labels. |
| serverDefaults.ingress.path | string | `"/"` | Ingress path. |
| serverDefaults.ingress.pathType | string | `"Prefix"` | Ingress path type. |
| serverDefaults.ingress.tls | list | `[]` | TLS blocks. Left empty with `tlsSecretName` unset, no TLS block is rendered - which is almost never what you want, since the operator URL must be `https://`. |
| serverDefaults.ingress.tlsSecretName | string | `""` | Shorthand for a single TLS block covering `host` with this secret. |
| serverDefaults.initContainers | list | `[]` | Additional init containers. |
| serverDefaults.networkPolicy.annotations | object | `{}` | Annotations for the NetworkPolicy. |
| serverDefaults.networkPolicy.create | bool | `false` | Create a NetworkPolicy for this instance. Off by default: the rules that matter are the egress ones, and only you know which databases this person's profiles reach - a chart that guessed would either allow everything, which is no policy, or cut the server off from its own capabilities in a way that surfaces as unexplained timeouts. The commented block below is the recommended posture; fill in the destinations and turn it on. |
| serverDefaults.networkPolicy.egress | list | `[]` | Egress rules. With `policyTypes` including `Egress` and this empty, the pod reaches nothing - not even DNS. Add the DNS rule first, then one rule per destination this instance's profiles actually name. |
| serverDefaults.networkPolicy.ingress | list | `[]` | Ingress rules. Left empty, one rule is generated allowing the MCP port from anywhere in the cluster, plus the observation port when `observability.metrics` is on. Naming the ingress controller and the monitoring namespace explicitly is stricter and better. |
| serverDefaults.networkPolicy.labels | object | `{}` | Labels for the NetworkPolicy. |
| serverDefaults.networkPolicy.policyTypes | list | `["Ingress"]` | Policy types. |
| serverDefaults.nodeSelector | object | `{}` | Node selector for the pod. |
| serverDefaults.observability.enabled | bool | `true` | Bind the second listener (`--observe`) carrying `/livez`, `/readyz`, `/healthz` and `/metrics`. The probes are open and answer with a status code and a word; `/metrics` sits behind the same bearer check MCP does. Turning this off falls back to `tcpSocket` probes, which prove the listener accepts connections and nothing more. |
| serverDefaults.observability.metrics.annotations | object | `{}` | Annotations for the metrics Service. |
| serverDefaults.observability.metrics.enabled | bool | `false` | Create a second, metrics-only Service publishing the observation port. Without it that port is reachable only by the kubelet's probes. |
| serverDefaults.observability.port | int | `9090` | Port the observation listener binds inside the pod. It is bound on 0.0.0.0 rather than loopback because a kubelet probe reaches the pod IP and cannot reach the pod's loopback; what keeps it private instead is that the main Service does not publish it. |
| serverDefaults.observability.serviceMonitor.annotations | object | `{}` | Additional ServiceMonitor annotations. |
| serverDefaults.observability.serviceMonitor.bearerTokenSecret.key | string | `""` | Key inside that Secret. |
| serverDefaults.observability.serviceMonitor.bearerTokenSecret.name | string | `""` | Secret Prometheus reads its bearer token from, when it is not the chart's own token Secret. Required if `tokenLabel` names nothing in `auth.tokens.entries` - an OIDC-only instance has no static credential for a scraper to present. |
| serverDefaults.observability.serviceMonitor.enabled | bool | `false` | Create a Prometheus Operator ServiceMonitor for `/metrics`. |
| serverDefaults.observability.serviceMonitor.interval | string | `"30s"` | Scrape interval. |
| serverDefaults.observability.serviceMonitor.labels | object | `{}` | Additional ServiceMonitor labels (e.g. the label your Prometheus selects on). |
| serverDefaults.observability.serviceMonitor.metricRelabelings | list | `[]` | Prometheus relabel configs applied before ingestion. |
| serverDefaults.observability.serviceMonitor.relabelings | list | `[]` | Prometheus relabel configs applied before scraping. |
| serverDefaults.observability.serviceMonitor.scrapeTimeout | string | `"10s"` | Scrape timeout. |
| serverDefaults.observability.serviceMonitor.tokenLabel | string | `"prometheus"` | Label in `auth.tokens.entries` whose token Prometheus should scrape with. The chart points `bearerTokenSecret` at that token's own key in the token Secret.  Worth being plain about what this credential is: there is one verifier, so a token that can read `/metrics` is a token that can call MCP. Binding the observation port privately is the outer control and the token is the inner one - keep Prometheus off the MCP port with a NetworkPolicy rather than assuming this token is metrics-only. |
| serverDefaults.operators.dashboard.expires | string | `""` | Expiry for the dashboard key, `YYYY-MM-DD`. Checked per call, so it needs no restart. |
| serverDefaults.operators.dashboard.label | string | `"dashboard"` | Roster label for the dashboard key. |
| serverDefaults.operators.dashboard.publicKey | string | `""` | Public key of a component that watches this server - a status page, a dashboard - rather than a person who answers. It is always enrolled `role=read`: status, grant.list, consent.list and lock.list, the verbs that change nothing. That is not a shorthand for a `keys` entry you could write yourself; the schema pins the role here, so the block naming a machine occupant cannot become a full operator through a values override or a copy-paste. |
| serverDefaults.operators.enabled | bool | `false` | Enable the operator channel, mounting `/operator/v1` beside the MCP handler. |
| serverDefaults.operators.existingSecret | string | `""` | Use a Secret managed outside this chart for the roster instead of rendering one. The pod's `checksum/roster` annotation is computed from `keys`/`dashboard`, so it cannot see an edit to an externally-managed Secret - restart the pod yourself when its content changes. |
| serverDefaults.operators.key | string | `"roster"` | Key inside the roster Secret holding the roster file. |
| serverDefaults.operators.keys | object | `{}` | Enrolled operator keys, as `label: {publicKey, role, expires}`. A bare entry is a full operator - enrolment is itself the trust decision, and the annotations only subtract. Editing this restarts the pod on purpose: the roster is read once at startup, so without that a key removed in Git would keep working. `expires` is the exception, checked per call against the running clock. |
| serverDefaults.operators.url | string | `""` | Overrides the `--operators-url` derived from `host`. It is signed into every operator request as this server's identity and is the anti-relay binding, so it must be the exact URL in operators' `remotes.yaml` and must be `https://`. |
| serverDefaults.paths.configDir | string | `"/etc/rta/config"` | Where the config ConfigMap is mounted. `RTA_CONFIG` points at `config.yaml` inside it. Deliberately outside `dataDir`, unlike the single-volume container recipe in the docs: a ConfigMap cannot be mounted inside a PersistentVolumeClaim. |
| serverDefaults.paths.dataDir | string | `"/rta-home"` | Where the data volume is mounted, and the value of `RTA_DATA_DIR`. The grants, the record, the sessions and plugin trust all live here. |
| serverDefaults.paths.rosterDir | string | `"/etc/rta/roster"` | Where the roster Secret is mounted. |
| serverDefaults.paths.tokenDir | string | `"/etc/rta/tokens"` | Where the token Secret is mounted. |
| serverDefaults.persistence.accessModes | list | `["ReadWriteOnce"]` | Access modes for the data volume. Single-writer only: `ReadWriteMany` is refused at render time, because this instance runs one replica on purpose and a shared-writer volume implies a scaling story that does not exist - two rta processes on one data directory split the record. If RWX is the only storage class you have, set `existingClaim` instead. |
| serverDefaults.persistence.annotations | object | `{}` | Annotations for the created PersistentVolumeClaim. |
| serverDefaults.persistence.existingClaim | string | `""` | Use an existing PersistentVolumeClaim instead of creating one. There is deliberately no switch for running without a volume: the grants and the record live there, and a restart without them is a machine with no memory of what you allowed. |
| serverDefaults.persistence.seedFromImage | bool | `false` | Seed the data volume from the image's own data directory on first start, with an init container. Required with `image.allowFullImage`, and for any derived image that bakes plugin trust: that trust is recorded inside the image's data directory, which this volume mounts over and therefore hides - the plugins would be present and untrusted, which looks like the image being broken. Impossible on the narrow distroless image, which has no shell to run the copy, and unnecessary there, since it bakes nothing. |
| serverDefaults.persistence.seedSourcePath | string | `"/rta-home"` | Path inside the image the seed init container copies from. This is a property of the image, not of `paths.dataDir`: both official images bake their state at /rta-home whatever the chart later mounts where. |
| serverDefaults.persistence.size | string | `"1Gi"` | Size of this instance's data volume. |
| serverDefaults.persistence.storageClass | string | `""` | StorageClass for the data volume. Empty uses the cluster default. |
| serverDefaults.plugins.allow | list | `[]` | Plugins to allow at startup, rendered as `RTA_ALLOW_PLUGINS`. Only an image using rta's `docker-entrypoint.sh` reads this - the full image does, the narrow image's entrypoint is rta itself and never sees it. This answers `allow` (may this plugin read a credential location), never `trust` (may this artifact run at all), which is baked into the full image and carried across restarts by the data volume. |
| serverDefaults.podAnnotations | object | `{}` | Annotations for the pod. The chart adds its own `checksum/*` annotations over the roster, the token file and the config, which are load-bearing rather than cosmetic: all three are read once at startup, so without them editing a key in Git would appear to do nothing. |
| serverDefaults.podDisruptionBudget.annotations | object | `{}` | Annotations for the PodDisruptionBudget. |
| serverDefaults.podDisruptionBudget.enabled | bool | `false` | Create a PodDisruptionBudget for this instance. |
| serverDefaults.podDisruptionBudget.maxUnavailable | int | `1` | Pods that may be unavailable after eviction. This is the field that makes sense here: `minAvailable: 1` over a single-replica workload makes the pod unevictable, so a node drain hangs forever - validation refuses that value rather than letting it stall an upgrade. |
| serverDefaults.podDisruptionBudget.minAvailable | string | `""` | Pods that must remain available after eviction. Has lower precedence than `maxUnavailable`, and `1` is refused; see above. |
| serverDefaults.podLabels | object | `{}` | Labels for the pod. |
| serverDefaults.podSecurityContext | object | `{"fsGroup":65532,"fsGroupChangePolicy":"OnRootMismatch","runAsGroup":65532,"runAsNonRoot":true,"runAsUser":65532,"seccompProfile":{"type":"RuntimeDefault"}}` | Pod-level security context. `fsGroup: 65532` is doing two jobs and neither is optional: it makes the data volume writable, because the image's own `chown` applies to the image layer and not to a volume mounted over it, and it makes the root-owned Secret files readable by the nonroot process through the group. Both official images run as 65532. |
| serverDefaults.priorityClassName | string | `""` | PriorityClass for the pod. |
| serverDefaults.probes.livenessProbe.failureThreshold | int | `3` | Consecutive failures tolerated before the container is restarted. |
| serverDefaults.probes.livenessProbe.httpGet.path | string | `"/livez"` | Liveness probe path. `/livez` consults nothing on purpose: a liveness probe wired to the store would ask for a restart that comes back to the same detached volume. |
| serverDefaults.probes.livenessProbe.httpGet.port | string | `"observe"` | Liveness probe port. |
| serverDefaults.probes.livenessProbe.periodSeconds | int | `30` | How often the liveness probe runs. |
| serverDefaults.probes.livenessProbe.timeoutSeconds | int | `3` | Liveness probe timeout. |
| serverDefaults.probes.readinessProbe.httpGet.path | string | `"/readyz"` | Readiness probe path. A volume that went read-only under a running server leaves it accepting connections and authenticating callers while failing at the one thing it is for, and this is what notices. |
| serverDefaults.probes.readinessProbe.httpGet.port | string | `"observe"` | Readiness probe port. |
| serverDefaults.probes.readinessProbe.periodSeconds | int | `10` | How often the readiness probe runs. |
| serverDefaults.probes.readinessProbe.timeoutSeconds | int | `3` | Readiness probe timeout. |
| serverDefaults.probes.startupProbe.failureThreshold | int | `30` | Consecutive failures tolerated before the container is restarted. |
| serverDefaults.probes.startupProbe.httpGet.path | string | `"/readyz"` | Startup probe path. `/readyz` writes to the data directory and reports whether the record can be written at all. |
| serverDefaults.probes.startupProbe.httpGet.port | string | `"observe"` | Startup probe port. |
| serverDefaults.probes.startupProbe.periodSeconds | int | `2` | How often the startup probe runs. |
| serverDefaults.resources | object | `{"limits":{"cpu":"500m","memory":"256Mi"},"requests":{"cpu":"50m","memory":"64Mi"}}` | Resource requests and limits. |
| serverDefaults.revisionHistoryLimit | int | `10` | Revision history limit for the Deployment. |
| serverDefaults.roots | list | `[]` | Paths this instance's filesystem capabilities may reach, passed as `--root` (repeatable). Required: rta's path root otherwise defaults to the working directory, which in a container is `/`, so an instance that states nothing here reaches the whole container. |
| serverDefaults.secretRefs | list | `[]` | Secrets this instance's profiles read, which become the `resourceNames` of its Role. Read `serviceAccount.automountServiceAccountToken` first: on the default image these rules grant something nothing in the pod can use. |
| serverDefaults.securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"privileged":false,"readOnlyRootFilesystem":true,"runAsGroup":65532,"runAsNonRoot":true,"runAsUser":65532}` | Container-level security context. `readOnlyRootFilesystem` holds because everything rta writes goes to the data volume or to /tmp. |
| serverDefaults.service.annotations | object | `{}` | Annotations for the MCP Service. |
| serverDefaults.service.port | int | `80` | Port the MCP Service listens on. |
| serverDefaults.service.type | string | `"ClusterIP"` | Type of the MCP Service. |
| serverDefaults.serviceAccount.annotations | object | `{}` | Annotations for the ServiceAccount (e.g. a workload-identity binding). |
| serverDefaults.serviceAccount.automountServiceAccountToken | bool | `false` | Mount the ServiceAccount token into the pod. Defaults to false, because the narrow image cannot use it: rta resolves a `kube:` secret reference by shelling out to `kubectl` against a kubeconfig naming a context, and the distroless image has neither. Enable it alongside `secretRefs` only on an image that ships kubectl and a kubeconfig. |
| serverDefaults.serviceAccount.create | bool | `true` | Create a ServiceAccount for this instance. One per instance, never one shared by the release: in a pod there is no caller's kubeconfig, so the pod's identity is the one a profile's Kubernetes secret reads happen under, and an instance that can read every Secret in the namespace has rebuilt the shared server this chart's shape exists to avoid. |
| serverDefaults.serviceAccount.name | string | `""` | Name of the ServiceAccount. Defaults to the instance's fully-qualified name. |
| serverDefaults.terminationGracePeriodSeconds | int | `null` (Kubernetes default of 30) | Grace period, in seconds, before the pod is killed. |
| serverDefaults.tolerations | list | `[]` | Tolerations for the pod. |
| serverDefaults.topologySpreadConstraints | list | `[]` | Topology spread constraints for the pod. |

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| this-is-tobi | <this-is-tobi@proton.me> | <https://this-is-tobi.com> |

## Sources

**Homepage:** <https://this-is-tobi.github.io/rta>

**Source code:**

* <https://github.com/this-is-tobi/rta>

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
