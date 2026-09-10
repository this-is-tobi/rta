{{/*
Expand the name of the chart.
*/}}
{{- define "rta.name" -}}
{{- (.Values.nameOverride | default .Chart.Name) | trunc 63 | trimSuffix "-" }}
{{- end }}


{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "rta.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := .Values.nameOverride | default .Chart.Name }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- (printf "%s-%s" .Release.Name $name) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}


{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "rta.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}


{{/*
Fully qualified name of one server instance's resources (`<release fullname>-<server name>`),
truncated to the 63-char DNS limit so a long release name can't produce a name the API server
rejects. Every per-instance object - Deployment, Service, ServiceAccount, RBAC, PVC, Secrets,
ConfigMap - derives its name from here, so they always agree.
Parameters:
- root: The root context.
- name: The server's key in `.Values.servers`.
*/}}
{{- define "rta.serverFullname" -}}
{{- printf "%s-%s" (include "rta.fullname" .root) .name | trunc 63 | trimSuffix "-" -}}
{{- end -}}


{{/*
Resolve one server entry's effective values by layering it over `.Values.serverDefaults`.

This is what makes "adding a person is one values entry" true rather than aspirational: the
platform team states resources, storage class, ingress class, OIDC issuer and the rest once, and a
new person's entry carries only what is actually theirs (their name, their host, their subject).
Without it every entry would restate the whole block, and the entries would drift apart silently
as the shared parts were updated in some and not others.

`deepCopy` on both sides is load-bearing, not defensive habit: `mergeOverwrite` mutates its first
argument in place, so merging straight into `.Values.serverDefaults` would leave the *previous*
server's values baked into the defaults for every later iteration of the range - the kind of bug
that only shows up once a second server is added, and then looks like a values-file mistake.
Parameters:
- root: The root context.
- server: The raw entry from `.Values.servers`.
*/}}
{{- define "rta.server.values" -}}
{{- $defaults := .root.Values.serverDefaults | default dict -}}
{{- $server := .server | default dict -}}
{{- toYaml (mergeOverwrite (deepCopy $defaults) (deepCopy $server)) -}}
{{- end -}}


{{/*
Build the container image reference.
- `global.imageRegistry` overrides `image.registry`, and an empty registry is omitted rather than
  rendering a leading "/" (which would be an invalid reference);
- `digest` wins over `tag`, pinning exact image content - a tag can be repointed at a different
  build, a digest cannot. For a security boundary that is the form to prefer, and `rta plugin
  trust` binds to a digest for the same reason;
- `tag` falls back to the chart's appVersion.
Parameters:
- root: The root context.
*/}}
{{- define "rta.image" -}}
{{- $image := .root.Values.image -}}
{{- $registry := .root.Values.global.imageRegistry | default $image.registry -}}
{{- $ref := ternary (printf "%s/%s" $registry $image.repository) $image.repository (not (empty $registry)) -}}
{{- if $image.digest -}}
{{- printf "%s@%s" $ref $image.digest -}}
{{- else -}}
{{- printf "%s:%s" $ref ($image.tag | default .root.Chart.AppVersion | toString) -}}
{{- end -}}
{{- end -}}


{{/*
Whether the configured repository is the published full image, which carries every first-party
plugin and every external tool. Returns a non-empty string for true, "" for false - the form an
`if` reads. Matched on the repository path so a tag or a digest does not change the answer, and so
a mirror of the same image (`registry.internal/mirror/rta-full`) is still recognised.
*/}}
{{- define "rta.isFullImage" -}}
{{- if regexMatch "(^|/)rta-full$" .Values.image.repository -}}
true
{{- end -}}
{{- end -}}


{{/*
Whether the configured repository is the published narrow image. Deliberately not "not full": a
derived image built from the narrow one (the "share the image" recipe) is neither, and the checks
that use this must not fire on it.
*/}}
{{- define "rta.isNarrowImage" -}}
{{- if regexMatch "(^|/)rta$" .Values.image.repository -}}
true
{{- end -}}
{{- end -}}


{{/*
The pod-level security context.

On OpenShift the UID, GID and fsGroup are removed rather than set. The `restricted-v2` SCC assigns
a UID from the namespace's own range and refuses a pod that asks for one outside it, so a chart
that hardcodes 65532 - correct everywhere else, since both official images run as that user - is a
chart that will not admit on OpenShift at all. Dropping the three fields lets SCC admission fill
them in, and the pieces that matter still hold: the Secret files stay group-readable to the group
the process actually runs with, and SCC's own `MustRunAs` fsGroup strategy chowns the data volume.
Parameters:
- root: The root context.
- server: The server's resolved values.
*/}}
{{- define "rta.server.podSecurityContext" -}}
{{- $ctx := .server.podSecurityContext | default dict -}}
{{- if .root.Values.openShift.enabled -}}
{{- $ctx = omit $ctx "runAsUser" "runAsGroup" "fsGroup" -}}
{{- end -}}
{{- if $ctx -}}
{{- toYaml $ctx -}}
{{- end -}}
{{- end -}}


{{/*
The container-level security context, with the same OpenShift treatment as the pod-level one.
Parameters:
- root: The root context.
- server: The server's resolved values.
*/}}
{{- define "rta.server.securityContext" -}}
{{- $ctx := .server.securityContext | default dict -}}
{{- if .root.Values.openShift.enabled -}}
{{- $ctx = omit $ctx "runAsUser" "runAsGroup" -}}
{{- end -}}
{{- if $ctx -}}
{{- toYaml $ctx -}}
{{- end -}}
{{- end -}}


{{/*
The init container that seeds an empty data volume from the image's own data directory.

The full image runs `rta plugin trust` at build time, and trust is recorded in the data directory -
so the image ships a populated one, and mounting a fresh volume over that path hides it. The pod
then starts with every plugin present and none of them trusted, which presents as a broken image
rather than as a hidden file. Copying once, on first start, is what makes the volume the thing that
carries trust forward across restarts, which is where it belongs anyway.

Idempotent by marker file rather than by emptiness: a freshly provisioned volume is not reliably
empty (ext4 leaves lost+found), and re-copying over a live data directory on every restart would
overwrite grants and the record with the image's build-time state.
Parameters:
- root: The root context.
- server: The server's resolved values.
*/}}
{{- define "rta.server.seedInitContainer" -}}
{{- $root := .root -}}
{{- $s := .server -}}
- name: seed-data
  image: {{ include "rta.image" (dict "root" $root) | quote }}
  imagePullPolicy: {{ $root.Values.image.pullPolicy | quote }}
  command:
  - /bin/sh
  - -c
  - |
    set -eu
    if [ -f /rta-seed/.rta-seeded ]; then
      echo "data volume already seeded, leaving it alone"
      exit 0
    fi
    if [ -d {{ $s.persistence.seedSourcePath }} ]; then
      cp -a {{ $s.persistence.seedSourcePath }}/. /rta-seed/ 2>/dev/null || true
    fi
    touch /rta-seed/.rta-seeded
    echo "seeded the data volume from {{ $s.persistence.seedSourcePath }}"
  {{- with include "rta.server.securityContext" (dict "root" $root "server" $s) }}
  securityContext: {{- . | nindent 4 }}
  {{- end }}
  resources: {{- toYaml $s.resources | nindent 4 }}
  volumeMounts:
  - name: data
    mountPath: /rta-seed
{{- end -}}


{{/*
Common labels.
*/}}
{{- define "rta.commonLabels" -}}
helm.sh/chart: {{ include "rta.chart" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: {{ include "rta.fullname" . }}
{{- with .Values.commonLabels }}
{{ . | toYaml }}
{{- end }}
{{- end }}


{{/*
Selector labels for one server instance.
Parameters:
- root: The root context.
- name: The server's key in `.Values.servers`.
*/}}
{{- define "rta.selectorLabels" -}}
app.kubernetes.io/name: {{ include "rta.serverFullname" (dict "root" .root "name" .name) }}
app.kubernetes.io/instance: {{ .root.Release.Name | trunc 63 | trimSuffix "-" }}
{{- end -}}


{{/*
Full labels for one server instance.
Parameters:
- root: The root context.
- name: The server's key in `.Values.servers`.
*/}}
{{- define "rta.labels" -}}
{{ include "rta.commonLabels" .root }}
{{ include "rta.selectorLabels" (dict "root" .root "name" .name) }}
app.kubernetes.io/component: {{ .name }}
{{- end -}}


{{/*
The URL operators bind to, and the anti-relay identity.

`--operators-url` is signed into every operator request, so it is an identity rather than an
address: it must be character-for-character the URL operators wrote in their `remotes.yaml`, which
means it derives from the public host the ingress serves, never from the in-cluster Service DNS
name. A chart that defaulted it to `http://<service>.<ns>.svc` would produce a server that
verifies nowhere, and the failure would surface as operator requests being rejected rather than as
a configuration error.
Parameters:
- server: The server's resolved values.
*/}}
{{- define "rta.server.operatorsURL" -}}
{{- $s := .server -}}
{{- if $s.operators.url -}}
{{- $s.operators.url -}}
{{- else -}}
{{- printf "https://%s" $s.host -}}
{{- end -}}
{{- end -}}


{{/*
The roster file: one `label base64-pubkey [role=...] [expires=...]` line per enrolled operator key.

`operators.dashboard` renders last and always carries `role=read`. It is not sugar for an entry in
`operators.keys` that a caller could write by hand: the values schema pins its role, so the block
that names a watching component cannot become a full operator through a values override or a
copy-paste. A person who answers consent goes in `operators.keys`; a thing that watches goes here.
Parameters:
- server: The server's resolved values.
*/}}
{{- define "rta.server.roster" -}}
{{- $s := .server -}}
{{- $lines := list -}}
{{- range $label, $key := $s.operators.keys -}}
{{- $line := printf "%s %s" $label $key.publicKey -}}
{{- with $key.role }}{{ $line = printf "%s role=%s" $line . }}{{ end -}}
{{- with $key.expires }}{{ $line = printf "%s expires=%s" $line . }}{{ end -}}
{{- $lines = append $lines $line -}}
{{- end -}}
{{- with $s.operators.dashboard -}}
{{- if .publicKey -}}
{{- $line := printf "%s %s role=read" (.label | default "dashboard") .publicKey -}}
{{- with .expires }}{{ $line = printf "%s expires=%s" $line . }}{{ end -}}
{{- $lines = append $lines $line -}}
{{- end -}}
{{- end -}}
{{- join "\n" $lines -}}
{{- end -}}


{{/*
The token file: one `label token` line per static credential.
Parameters:
- server: The server's resolved values.
*/}}
{{- define "rta.server.tokenFile" -}}
{{- $lines := list -}}
{{- range $label, $token := .server.auth.tokens.entries -}}
{{- $lines = append $lines (printf "%s %s" $label $token) -}}
{{- end -}}
{{- join "\n" $lines -}}
{{- end -}}


{{/*
Name of the Secret holding this instance's token file, whether the chart created it or the
operator brought their own.
Parameters:
- root: The root context.
- name: The server's key in `.Values.servers`.
- server: The server's resolved values.
*/}}
{{- define "rta.server.tokenSecretName" -}}
{{- if .server.auth.tokens.existingSecret -}}
{{- .server.auth.tokens.existingSecret -}}
{{- else -}}
{{- printf "%s-tokens" (include "rta.serverFullname" (dict "root" .root "name" .name)) -}}
{{- end -}}
{{- end -}}


{{/*
Name of the Secret holding this instance's roster.
Parameters:
- root: The root context.
- name: The server's key in `.Values.servers`.
- server: The server's resolved values.
*/}}
{{- define "rta.server.rosterSecretName" -}}
{{- if .server.operators.existingSecret -}}
{{- .server.operators.existingSecret -}}
{{- else -}}
{{- printf "%s-roster" (include "rta.serverFullname" (dict "root" .root "name" .name)) -}}
{{- end -}}
{{- end -}}


{{/*
Name of the ConfigMap holding this instance's config.yaml.
Parameters:
- root: The root context.
- name: The server's key in `.Values.servers`.
- server: The server's resolved values.
*/}}
{{- define "rta.server.configMapName" -}}
{{- if .server.existingConfigMap -}}
{{- .server.existingConfigMap -}}
{{- else -}}
{{- include "rta.serverFullname" (dict "root" .root "name" .name) -}}
{{- end -}}
{{- end -}}


{{/*
The argv for `rta mcp serve`.

Two binds, never one. `--observe` is a second listener rather than four more paths on `--http`,
because bearer authentication wraps the whole protocol handler on the `--http` one and open paths
beside it would make that structural property a matter of route matching instead. The chart binds
it to 0.0.0.0 rather than loopback - which the flag's own documentation offers as the tighter
choice - because a kubelet HTTP probe reaches the pod IP and cannot reach the pod's loopback, so
loopback would mean no probes at all. The narrowing that replaces it is that the main Service does
not publish this port: reaching it takes a deliberate second Service, which `observability.metrics`
creates only when asked.
Parameters:
- root: The root context.
- name: The server's key in `.Values.servers`.
- server: The server's resolved values.
*/}}
{{- define "rta.server.args" -}}
{{- $s := .server -}}
- mcp
- serve
- --as
- {{ $s.agentName | default .name | quote }}
{{- range $s.roots }}
- --root
- {{ . | quote }}
{{- end }}
- --http
- {{ printf "0.0.0.0:%v" $s.containerPort | quote }}
{{- if $s.observability.enabled }}
- --observe
- {{ printf "0.0.0.0:%v" $s.observability.port | quote }}
{{- end }}
{{- if $s.auth.tokens.enabled }}
- --token-file
- {{ printf "%s/%s" $s.paths.tokenDir $s.auth.tokens.key | quote }}
{{- end }}
{{- if $s.auth.oidc.enabled }}
- --oidc-issuer
- {{ $s.auth.oidc.issuer | quote }}
- --oidc-audience
- {{ $s.auth.oidc.audience | quote }}
{{- range $s.auth.oidc.subjects }}
- --oidc-subject
- {{ . | quote }}
{{- end }}
{{- end }}
{{- if $s.operators.enabled }}
- --operators
- {{ printf "%s/%s" $s.paths.rosterDir $s.operators.key | quote }}
- --operators-url
- {{ include "rta.server.operatorsURL" (dict "server" $s) | quote }}
{{- end }}
{{- if $s.consent.enabled }}
- --consent
{{- with $s.consent.wait }}
- --consent-wait
- {{ . | quote }}
{{- end }}
{{- end }}
{{- with $s.extraArgs }}
{{- toYaml . }}
{{- end }}
{{- end -}}


{{/*
Fail fast on values combinations that rta itself refuses at startup, or that render a pod which
starts and then behaves wrongly. Called from templates/validation.yaml so the message arrives at
`helm template`/`helm lint`/`helm install` time - naming the values key to fix - instead of as a
CrashLoopBackOff whose cause is one line in a log nobody has fetched yet.

Everything checked here is a documented refusal in rta or a documented property of the image. The
combination rules JSON Schema cannot express live here; the shape rules live in values.schema.json.
Parameters:
- root: The root context.
- name: The server's key in `.Values.servers`.
- server: The server's resolved values.
*/}}
{{- define "rta.server.validate" -}}
{{- $root := .root -}}
{{- $name := .name -}}
{{- $s := .server -}}
{{- $prefix := printf "servers.%s" $name -}}

{{- /* The image is the plugin allowlist, so reaching for the full one is a decision about how much
this instance can do, not about convenience. It stays available - a homelab exercising `kube`,
`cnpg` and the rest against real infrastructure wants exactly that image - but behind a flag, so
nobody arrives at the widest reach rta has by editing one line and not noticing which line. */ -}}
{{- if and (include "rta.isFullImage" $root) (not $root.Values.image.allowFullImage) -}}
{{- fail (printf "image.repository is %q. The full image carries every first-party plugin and every external tool, which makes it the widest reach rta has - the opposite of what an agent-facing instance is for, since the image IS the plugin allowlist. Set image.allowFullImage=true if that is deliberate, or build a narrow image with the plugins this job needs." $root.Values.image.repository) -}}
{{- end -}}

{{- /* The full image records plugin *trust* inside its own data directory, which the data volume
mounts over. Without seeding, the plugins are present and untrusted, `rta plugin list` says so, and
it reads as the image being broken rather than as the volume hiding half of it. */ -}}
{{- if and (include "rta.isFullImage" $root) (not $s.persistence.seedFromImage) -}}
{{- fail (printf "%s.persistence.seedFromImage must be true with the full image: its plugin trust is baked into the image's data directory, and this instance's volume mounts over that path - the plugins would start up present but untrusted." $prefix) -}}
{{- end -}}

{{- /* The reverse of the check above: the narrow image has no shell to run the seed init
container's copy, and nothing baked into it worth seeding in the first place. Refused here rather
than left to CrashLoopBackOff the init container. */ -}}
{{- if and $s.persistence.seedFromImage (include "rta.isNarrowImage" $root) -}}
{{- fail (printf "%s.persistence.seedFromImage is true against the narrow image, which has no shell to run the copy and bakes no plugin trust to seed - the init container would crash instead of starting. Drop it, or point image.repository at an image that actually needs seeding." $prefix) -}}
{{- end -}}

{{- /* RTA_ALLOW_PLUGINS is read by rta's docker-entrypoint.sh, which the narrow image does not
carry - its entrypoint is rta itself. Refused only for the published narrow image, since a derived
image may well ship that entrypoint. */ -}}
{{- if and $s.plugins.allow (include "rta.isNarrowImage" $root) -}}
{{- fail (printf "%s.plugins.allow is set, but the narrow image's entrypoint is rta itself and never reads RTA_ALLOW_PLUGINS - the setting would be silently ignored. It applies to the full image, or to a derived image shipping rta's docker-entrypoint.sh." $prefix) -}}
{{- end -}}

{{- /* The shipped securityContext default is the whole hardening story for this pod, and unlike
`image.allowFullImage` there is no flag that says "I meant to weaken it" - a values override merges
straight through `mergeOverwrite` and lands in the pod spec unexamined. Refusing the three fields
that actually matter turns a silent weakening into a values key named at render time. */ -}}
{{- $sc := $s.securityContext | default dict -}}
{{- if eq $sc.privileged true -}}
{{- fail (printf "%s.securityContext.privileged is true. This chart's default forbids it deliberately - a privileged container is a decision to make with its own explicit override, not one to arrive at by merging a single field over the safe default." $prefix) -}}
{{- end -}}
{{- if eq $sc.allowPrivilegeEscalation true -}}
{{- fail (printf "%s.securityContext.allowPrivilegeEscalation is true. The shipped default forbids it deliberately - only set this if you understand exactly why this instance needs it." $prefix) -}}
{{- end -}}
{{- if eq $sc.readOnlyRootFilesystem false -}}
{{- fail (printf "%s.securityContext.readOnlyRootFilesystem is false. Everything rta writes goes to the data volume or /tmp, so this should never need widening - if a plugin genuinely needs to write elsewhere, mount it with extraVolumes/extraVolumeMounts instead." $prefix) -}}
{{- end -}}

{{- /* `--as` is the audit identity: every record line names it. grant.CheckAgent bounds it. */ -}}
{{- $agent := $s.agentName | default $name -}}
{{- if not (regexMatch "^[A-Za-z0-9._-]{1,64}$" $agent) -}}
{{- fail (printf "%s.agentName (%q) must be 1-64 characters of [A-Za-z0-9._-] - it is the identity every record line is written against" $prefix $agent) -}}
{{- end -}}

{{- /* The path root defaults to the working directory, which in this image is `/`. An instance
that never states its roots is one whose filesystem capabilities reach the whole container. */ -}}
{{- if not $s.roots -}}
{{- fail (printf "%s.roots is empty - rta's path root defaults to the working directory, which in a container is \"/\". State the paths this instance may reach." $prefix) -}}
{{- end -}}

{{- /* rta refuses `--http` with neither credential; catching it here names the values keys. */ -}}
{{- if and (not $s.auth.tokens.enabled) (not $s.auth.oidc.enabled) -}}
{{- fail (printf "%s.auth needs oidc.enabled or tokens.enabled - `rta mcp serve --http` refuses to start with no way to authenticate a caller" $prefix) -}}
{{- end -}}

{{- if $s.auth.oidc.enabled -}}
{{- if not $s.auth.oidc.issuer -}}
{{- fail (printf "%s.auth.oidc.issuer is required when oidc is enabled" $prefix) -}}
{{- end -}}
{{- /* Both are rta startup refusals, not chart opinions: an audience-less token is one minted for
another service, and a subject-less issuer would accept every identity that issuer will ever
mint - which is the whole namespace of that IdP, not this person. */ -}}
{{- if not $s.auth.oidc.audience -}}
{{- fail (printf "%s.auth.oidc.audience is required when oidc is enabled - rta refuses an issuer with no audience, because a token minted for another service would otherwise authenticate here" $prefix) -}}
{{- end -}}
{{- if not $s.auth.oidc.subjects -}}
{{- fail (printf "%s.auth.oidc.subjects needs at least one entry - an issuer with no subject accepts every identity it will ever mint, which is the opposite of authenticating as one person" $prefix) -}}
{{- end -}}
{{- end -}}

{{- if $s.auth.tokens.enabled -}}
{{- if and (not $s.auth.tokens.entries) (not $s.auth.tokens.existingSecret) -}}
{{- fail (printf "%s.auth.tokens needs entries or existingSecret when enabled" $prefix) -}}
{{- end -}}
{{- range $label, $token := $s.auth.tokens.entries -}}
{{- if not (regexMatch "^[A-Za-z0-9._-]{1,64}$" $label) -}}
{{- fail (printf "%s.auth.tokens.entries has label %q - a token label must be 1-64 characters of [A-Za-z0-9._-], it is the identity the record names" $prefix $label) -}}
{{- end -}}
{{- if lt (len $token) 16 -}}
{{- fail (printf "%s.auth.tokens.entries[%q] is %d characters - rta refuses a bearer token shorter than 16, because it is the whole credential on this transport. `rta gen token` makes one." $prefix $label (len $token)) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- /* Over HTTP a parked consent call has nobody to answer it but an enrolled operator, so rta
refuses the pairing. The chart validates it rather than letting the pod crash-loop. */ -}}
{{- if and $s.consent.enabled (not $s.operators.enabled) -}}
{{- fail (printf "%s.consent requires operators.enabled - over HTTP the enrolled operators are the only people positioned to answer a parked call" $prefix) -}}
{{- end -}}

{{- if $s.operators.enabled -}}
{{- if and (not $s.operators.keys) (not $s.operators.dashboard.publicKey) (not $s.operators.existingSecret) -}}
{{- fail (printf "%s.operators needs keys, dashboard.publicKey or existingSecret when enabled - rta refuses an empty roster" $prefix) -}}
{{- end -}}
{{- $url := include "rta.server.operatorsURL" (dict "server" $s) -}}
{{- if not (hasPrefix "https://" $url) -}}
{{- fail (printf "%s.operators.url resolves to %q - it must be https://, because it is signed into every operator request as this server's identity and remotes.yaml refuses plain http for anything but loopback. Set servers.%s.host, or operators.url explicitly." $prefix $url $name) -}}
{{- end -}}
{{- if and (not $s.operators.url) (not $s.host) -}}
{{- fail (printf "%s needs host (or operators.url) when operators are enabled - the operator URL is the exact string operators wrote in their remotes.yaml, so it derives from the public host, never from the in-cluster Service name" $prefix) -}}
{{- end -}}
{{- if and $s.operators.dashboard.publicKey (hasKey ($s.operators.keys | default dict) ($s.operators.dashboard.label | default "dashboard")) -}}
{{- fail (printf "%s.operators has both a dashboard block and a keys entry labelled %q - one label is one identity in the record, and rta refuses a roster that enrols the same label twice" $prefix ($s.operators.dashboard.label | default "dashboard")) -}}
{{- end -}}
{{- range $label, $key := $s.operators.keys -}}
{{- if not $key.publicKey -}}
{{- fail (printf "%s.operators.keys[%q] has no publicKey" $prefix $label) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- if and $s.ingress.enabled (not $s.host) -}}
{{- fail (printf "%s.ingress is enabled but host is empty" $prefix) -}}
{{- end -}}

{{- /* `rta mcp serve --http` binds plain TCP by design and terminates nothing itself, so an Ingress
with no TLS block ships a bearer token or an OIDC flow across the wire in clear. Refused rather than
left to the README's "almost never what you want". */ -}}
{{- if and $s.ingress.enabled (not $s.ingress.tls) (not $s.ingress.tlsSecretName) -}}
{{- fail (printf "%s.ingress is enabled with no TLS: rta mcp serve --http binds plain TCP by design, so a bearer token or OIDC flow would cross the wire in clear. Set ingress.tlsSecretName, or a full ingress.tls block." $prefix) -}}
{{- end -}}

{{- if and $s.httpRoute.enabled (not $s.host) -}}
{{- fail (printf "%s.httpRoute is enabled but host is empty" $prefix) -}}
{{- end -}}

{{- /* Compared as strings so a deliberate `0` counts as set. A budget with neither field does not
restrict evictions at all, which is a resource that looks like protection and is not. */ -}}
{{- if and $s.podDisruptionBudget.enabled (eq (toString $s.podDisruptionBudget.minAvailable) "") (eq (toString $s.podDisruptionBudget.maxUnavailable) "") -}}
{{- fail (printf "%s.podDisruptionBudget needs minAvailable or maxUnavailable when enabled" $prefix) -}}
{{- end -}}
{{- if and $s.podDisruptionBudget.enabled (eq (toString $s.podDisruptionBudget.minAvailable) "1") -}}
{{- fail (printf "%s.podDisruptionBudget.minAvailable=1 over a single-replica workload makes this pod unevictable, so a node drain hangs and the cluster upgrade behind it stalls. Use maxUnavailable: 1 - the eviction is allowed and the instance is briefly down, which is what one replica on one volume means either way." $prefix) -}}
{{- end -}}

{{- /* Single-writer access modes only, and ReadWriteMany refused rather than quietly accepted.

The chart already takes the step RWO needs: the strategy is Recreate, because a rolling update on a
single RWO volume deadlocks - the new pod waits for a volume the old one has not released. RWX
removes that deadlock, and that is the whole of what it buys. It does not unlock a second replica,
because the reason for one replica was never the storage: two rta processes on one data directory
hold divergent grant rosters and split the record, so the audit trail answers questions wrongly
rather than not at all.

So a shared-writer volume here is a claim about this workload that is not true, and the failure it
invites - someone concluding replicas can now be raised - is one this chart cannot catch, since
`replicas` is not a value it reads. Refusing is cheap and says so out loud.

A cluster whose only storage class is RWX is not blocked: point `persistence.existingClaim` at a
claim you made yourself. That keeps the decision where it belongs, with the person who knows their
storage, instead of hiding it in a default. */ -}}
{{- if not $s.persistence.accessModes -}}
{{- fail (printf "%s.persistence.accessModes is empty" $prefix) -}}
{{- end -}}
{{- if has "ReadWriteMany" $s.persistence.accessModes -}}
{{- fail (printf "%s.persistence.accessModes names ReadWriteMany. This instance runs one replica on purpose - two rta processes on one data directory split the record and hold divergent grant rosters - so a shared-writer volume buys nothing and suggests a scaling story that does not exist. Use ReadWriteOnce, or set persistence.existingClaim if RWX is the only storage class you have." $prefix) -}}
{{- end -}}

{{- /* A scrape of /metrics presents the same bearer credential MCP does - there is one verifier,
and that is deliberate. So a ServiceMonitor with nowhere to read a token from would render, be
accepted by the API server, and then fail every scrape with a 401. */ -}}
{{- if $s.observability.serviceMonitor.enabled -}}
{{- if not $s.observability.enabled -}}
{{- fail (printf "%s.observability.serviceMonitor requires observability.enabled - without --observe there is no /metrics to scrape" $prefix) -}}
{{- end -}}
{{- $bts := $s.observability.serviceMonitor.bearerTokenSecret | default dict -}}
{{- $label := $s.observability.serviceMonitor.tokenLabel -}}
{{- $fromChart := and $label $s.auth.tokens.enabled (hasKey ($s.auth.tokens.entries | default dict) $label) -}}
{{- if and (not $fromChart) (not (and $bts.name $bts.key)) -}}
{{- fail (printf "%s.observability.serviceMonitor needs a credential: /metrics sits behind the same bearer check MCP does. Either add auth.tokens.entries[%q], or point bearerTokenSecret at a secret holding a token this server accepts." $prefix ($label | default "prometheus")) -}}
{{- end -}}
{{- end -}}
{{- end -}}


{{/*
The pod template shared by the Deployment.

The securityContext here is the one thing in this chart most likely to be "corrected" into
something that does not start, so the reasoning is worth stating where it is edited:

- `fsGroup: 65532` does two jobs. It makes the PVC at the data directory writable, because the
  image's `chown 65532:65532 /rta-home` applies to the image layer and not to a volume mounted
  over it; and it makes the Secret volumes readable, because Kubernetes writes Secret files owned
  by root and the container is nonroot.
- Secret volumes therefore mount `0440`, not `0400`. The tighter-looking mode is the wrong one:
  an owner-only file owned by root is one this process cannot open at all, which trades a
  permission refusal for a missing-credential failure. rta's refusal mask (`mode&0o037`) leaves
  group read out deliberately, precisely so a shared-group file can hand a credential to a service
  account. It does warn, on every start - `is group-readable` in the log is this arrangement
  working as designed, not a misconfiguration to chase.
- `readOnlyRootFilesystem` holds because everything rta writes goes to the PVC or to /tmp.
Parameters:
- root: The root context.
- name: The server's key in `.Values.servers`.
- server: The server's resolved values.
*/}}
{{- define "rta.server.podTemplate" -}}
{{- $root := .root -}}
{{- $name := .name -}}
{{- $s := .server -}}
{{- $fullname := include "rta.serverFullname" (dict "root" $root "name" $name) -}}
metadata:
  annotations:
    {{- /* The roster and the token file are read once, at startup: a rewrite behind a running
    server's back changes nothing until the next deliberate restart. These checksums are what
    make editing a key in Git actually evict it, so they are load-bearing rather than the usual
    cosmetic Helm habit. The one thing that does NOT need them is `expires=`, which is checked
    per call against the running clock. */}}
    checksum/roster: {{ include "rta.server.roster" (dict "server" $s) | sha256sum }}
    checksum/tokens: {{ include "rta.server.tokenFile" (dict "server" $s) | sha256sum }}
    checksum/config: {{ $s.config | toYaml | sha256sum }}
    {{- with $s.podAnnotations }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
  labels: {{- include "rta.labels" (dict "root" $root "name" $name) | nindent 4 }}
    {{- with $s.podLabels }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
spec:
  {{- with (concat ($root.Values.global.imagePullSecrets | default list) ($s.imagePullSecrets | default list)) }}
  imagePullSecrets: {{- toYaml . | nindent 2 }}
  {{- end }}
  serviceAccountName: {{ $s.serviceAccount.name | default $fullname }}
  automountServiceAccountToken: {{ $s.serviceAccount.automountServiceAccountToken }}
  enableServiceLinks: false
  {{- with include "rta.server.podSecurityContext" (dict "root" $root "server" $s) }}
  securityContext: {{- . | nindent 4 }}
  {{- end }}
  {{- with $s.priorityClassName }}
  priorityClassName: {{ . | quote }}
  {{- end }}
  {{- with $s.terminationGracePeriodSeconds }}
  terminationGracePeriodSeconds: {{ . }}
  {{- end }}
  {{- with $s.dnsPolicy }}
  dnsPolicy: {{ . | quote }}
  {{- end }}
  {{- with $s.dnsConfig }}
  dnsConfig: {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- with $s.hostAliases }}
  hostAliases: {{- toYaml . | nindent 2 }}
  {{- end }}
  {{- if or $s.persistence.seedFromImage $s.initContainers }}
  initContainers:
  {{- if $s.persistence.seedFromImage }}
  {{- include "rta.server.seedInitContainer" (dict "root" $root "server" $s) | nindent 2 }}
  {{- end }}
  {{- with $s.initContainers }}
  {{- tpl (toYaml .) $root | nindent 2 }}
  {{- end }}
  {{- end }}
  containers:
  - name: rta
    {{- with include "rta.server.securityContext" (dict "root" $root "server" $s) }}
    securityContext: {{- . | nindent 6 }}
    {{- end }}
    image: {{ include "rta.image" (dict "root" $root) | quote }}
    imagePullPolicy: {{ $root.Values.image.pullPolicy | quote }}
    args: {{- include "rta.server.args" (dict "root" $root "name" $name "server" $s) | nindent 4 }}
    ports:
    - containerPort: {{ $s.containerPort }}
      name: mcp
      protocol: TCP
    {{- if $s.observability.enabled }}
    - containerPort: {{ $s.observability.port }}
      name: observe
      protocol: TCP
    {{- end }}
    env:
    {{- /* Both are set unconditionally, never exposed as a block somebody can omit. The image
    sets PATH and nothing else, deliberately, and with no config path rta falls back to
    ./.rta.yaml - which is not honoured, so profiles, plugins and dashboard would be silently
    ignored and plugins would run with their declared defaults. That is the worst failure mode
    available here, because it looks like it worked. */}}
    - name: RTA_CONFIG
      value: {{ printf "%s/config.yaml" $s.paths.configDir | quote }}
    - name: RTA_DATA_DIR
      value: {{ $s.paths.dataDir | quote }}
    {{- with $s.plugins.allow }}
    {{- /* Answers `allow` - whether a plugin may reach a credential location - and never `trust`,
    which is a decision about an artifact and is carried by the data volume. */}}
    - name: RTA_ALLOW_PLUGINS
      value: {{ join "," . | quote }}
    {{- end }}
    {{- with $s.env }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
    {{- with $s.envFrom }}
    envFrom: {{- toYaml . | nindent 4 }}
    {{- end }}
    {{- if $s.observability.enabled }}
    {{- /* /livez consults nothing by design: a liveness probe wired to the store would ask for a
    restart that comes back to the same detached volume. /readyz writes to the data directory and
    reports whether the record can be written at all - a volume that went read-only under a running
    server leaves it accepting connections and authenticating callers while silently failing at the
    one thing it is for. */}}
    {{- with $s.probes.startupProbe }}
    startupProbe: {{- toYaml . | nindent 6 }}
    {{- end }}
    {{- with $s.probes.readinessProbe }}
    readinessProbe: {{- toYaml . | nindent 6 }}
    {{- end }}
    {{- with $s.probes.livenessProbe }}
    livenessProbe: {{- toYaml . | nindent 6 }}
    {{- end }}
    {{- else }}
    {{- /* Without --observe there is no open path to probe: every route on the MCP listener meets
    the bearer wall and answers 401, which Kubernetes counts as a failed probe, and the image has
    no shell for an exec probe. A TCP check proves the listener is up and accepting, and is honest
    about proving nothing more. */}}
    readinessProbe:
      tcpSocket:
        port: mcp
      initialDelaySeconds: 5
      periodSeconds: 10
    livenessProbe:
      tcpSocket:
        port: mcp
      initialDelaySeconds: 30
      periodSeconds: 30
    {{- end }}
    resources: {{- toYaml $s.resources | nindent 6 }}
    volumeMounts:
    - name: data
      mountPath: {{ $s.paths.dataDir | quote }}
    - name: config
      mountPath: {{ $s.paths.configDir | quote }}
      readOnly: true
    {{- if $s.auth.tokens.enabled }}
    - name: tokens
      mountPath: {{ $s.paths.tokenDir | quote }}
      readOnly: true
    {{- end }}
    {{- if $s.operators.enabled }}
    - name: roster
      mountPath: {{ $s.paths.rosterDir | quote }}
      readOnly: true
    {{- end }}
    - name: tmp
      mountPath: /tmp
    {{- with $s.extraVolumeMounts }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
  {{- with $s.extraContainers }}
  {{- tpl (toYaml .) $root | nindent 2 }}
  {{- end }}
  volumes:
  - name: data
    persistentVolumeClaim:
      claimName: {{ $s.persistence.existingClaim | default (printf "%s-data" $fullname) }}
  - name: config
    configMap:
      name: {{ include "rta.server.configMapName" (dict "root" $root "name" $name "server" $s) }}
  {{- if $s.auth.tokens.enabled }}
  - name: tokens
    secret:
      secretName: {{ include "rta.server.tokenSecretName" (dict "root" $root "name" $name "server" $s) }}
      defaultMode: 0440
  {{- end }}
  {{- if $s.operators.enabled }}
  - name: roster
    secret:
      secretName: {{ include "rta.server.rosterSecretName" (dict "root" $root "name" $name "server" $s) }}
      defaultMode: 0440
  {{- end }}
  - name: tmp
    emptyDir: {}
  {{- with $s.extraVolumes }}
  {{- tpl (toYaml .) $root | nindent 2 }}
  {{- end }}
  {{- with $s.nodeSelector }}
  nodeSelector: {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- with $s.affinity }}
  affinity: {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- with $s.tolerations }}
  tolerations: {{- toYaml . | nindent 2 }}
  {{- end }}
  {{- with $s.topologySpreadConstraints }}
  topologySpreadConstraints: {{- toYaml . | nindent 2 }}
  {{- end }}
{{- end -}}
