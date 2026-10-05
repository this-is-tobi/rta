# Hosting a server

Over stdio, `rta mcp serve` is a child process of one client on your own machine: no daemon, no port, nothing to start. `--http` is the other transport, for running rta somewhere that is not your own machine — a dev platform, a Codespace, a per-user pod. This page is that transport: how a caller proves who it is, how an orchestrator probes it, what it leaves out, and what stops being true. Why that instance should be **one per person** and not one for everyone is [Why not one server for everyone](./67-containers-and-images.md#why-not-one-server-for-everyone); the deployment is [Kubernetes](./80-kubernetes.md).

```bash
rta mcp serve --http 127.0.0.1:8443 --token-file tokens.txt --as work
```

A second transport, opt-in: the server listens on TCP instead of speaking stdio to a parent process, which is what running it somewhere other than your own machine actually needs.

Putting that somewhere is its own subject: [Kubernetes](./80-kubernetes.md) is the deployment, with a chart whose unit is the instance rather than the release.

A caller now has to prove who it is over the wire, since there is no parent process left to trust instead. Two mechanisms, usable together:

| Flag | Proves |
| --- | --- |
| `--token-file <path>` | A static, operator-issued token — one `label token` pair per line in a file only the operator can read; world-readable files are refused, and so is a token shorter than 16 characters (`rta gen token` makes one) |
| `--oidc-issuer`, `--oidc-audience`, `--oidc-subject` | A real identity provider's token, for one of the named subjects. An issuer and audience alone identify an application, not a person, so at least one `--oidc-subject` is required — [OIDC](./70-oidc.md) is the full setup, including the Keycloak audience mapper without which every token is rejected |

The file is the label and the token, so `rta gen token`, which prints the token among a few facts about it, is taken from its JSON answer:

```bash
printf 'work %s\n' "$(rta gen token -o json | jq -r '.pairs[] | select(.key == "token") | .value')" > tokens.txt
chmod 600 tokens.txt
```

The label is what [the record](./40-audit-trail.md) shows for the credential that authenticated a call, so give each caller its own line.

A rejected token is answered slower from the same address after five failures in a minute, doubling up to two seconds: a guess a second becomes a guess every two, and an operator who mistyped once never notices. Behind a reverse proxy every client shares the address, so a guessing attacker slows the operators beside it for as long as the guessing lasts — that trade is taken rather than trusting a `Forwarded` header the attacker writes.

`--http` refuses to start with neither configured. `--consent` over `--http` additionally requires `--operators` — a parked call waits for a person, enrolled operators answering over [the operator channel](./66-operators.md) are the only people positioned to be that person, and a control nobody can exercise must not be allowed to pretend it works.

TLS is not this process's job. Bind to a private address and put a reverse proxy, ingress or service mesh in front of it for termination. A bind that other machines can reach — `0.0.0.0`, `:8443`, an interface address — is announced at startup, because on that transport the token is the whole credential and it crosses the wire as it is.

Every request's verified identity is recorded a third way, beside `--as` and the client's own self-report: `rta agent log` shows which credential actually authenticated each call — a token's label, an OIDC subject — so more than one credential valid for an instance stays distinguishable instead of collapsing into one indistinguishable principal.

## Probes and counters, on a second listener

A hosted server needs to tell an orchestrator whether it is alive and whether it is ready, and a monitoring stack in the same cluster has no node_exporter to read [the counters](../90-recipes/01-readme.md#put-it-on-a-dashboard) out of a file with. `--observe` binds a second address for both:

```bash
rta mcp serve --as work --http :8443 --token-file tokens.txt --observe :9090
```

It is deliberately not more paths on the `--http` listener. Bearer authentication wraps that one whole, and adding open paths beside the protocol handler would turn a property of the wrapper into a property of route matching — where every handler added later is a chance to match wrongly. Kept apart, an operator can also bind this where the agent-facing port is not: loopback, or a pod port the Service never publishes.

| Path | Credential | Says |
| --- | --- | --- |
| `/livez` | none | the process is serving. It consults nothing on purpose — a liveness probe wired to the store asks for a restart that meets the same broken volume |
| `/readyz` | none | the config reads (one that does not parse leaves a server that refuses every capability a profile could change, so it is not sent traffic until it does, with no restart), and the record can actually be written: the data directory takes a file and, once there is a record, it takes an append — the question a call that needs a grant is asked, since one is refused when it cannot. A detached volume or a full disk leaves a server that still accepts connections and authenticates callers while failing at the one thing it is for. The verdict is kept for a second, so asking as often as the open address allows costs the server one check |
| `/healthz` | none | the same as `/readyz`, for tooling that asks by that name |
| `/metrics` | **the same bearer token as MCP** | the exposition format `rta agent metrics` prints |

`/metrics` is authenticated because the counters name which agent called what, and how often it was refused — a map of the machine's activity, not a health signal. Binding it somewhere private is the outer control and the token is the inner one; a Prometheus scrape config carries a bearer token without complaint, so keeping both costs nothing.

## What a remote server leaves out

`sys`, `fs`, `git`, `keys.list`, `kv.status`, the two `audit` checks that grade a project on this disk (`audit.deps`, `audit.why`), and the parts of `net` that read or change this host's own network configuration (`net.overview`, `net.listen`, `net.hosts.*`, `net.resolver.*`) answer for the machine rta happens to run on. Over HTTP those are never registered as tools at all — absent from `tools/list`, not refused when called — because a remote caller is never this machine. `rta mcp serve --http` says so at startup:

```
rta mcp server listening on http://127.0.0.1:8443
rta: every request needs a bearer token; TLS is not this process's job — put a reverse proxy, ingress or service mesh in front of it
path arguments confined to: /Users/you/projects
record: /Users/you/.local/share/rta/agent-log.jsonl (session 4e289252)
rta: remote transport hides 32 capabilities that describe this machine: audit.deps, audit.why, fs.hash, fs.tree, fs.usage, git.blame, …
```

Everything else in `net` — `ping`, `dns`, `trace`, `probe`, `send`, `port` — stays reachable, since those describe a caller-named target rather than this host. A result still reflects the vantage point of wherever rta is actually running, which is worth knowing rather than assuming.

## What is still true, and what stops being true

The credentials-move trade [the container recipe](./67-containers-and-images.md#in-a-container-for-a-hardened-server) rests on — "the caller's own kubeconfig, the caller's own RBAC" — needs restating here rather than assumed. A container on your own machine still has *your* kubeconfig mounted into it; a real network call has no caller-side credential at all, only whatever the server's own ambient identity is. Provision that identity as deliberately as any other production credential.

The `kv` store is exactly as strong remotely as locally, no stronger — "unlocks from this environment" (`rta doctor`) is equally true of a laptop and a gateway, with no hardware-backed second factor either way. Prefer a passphrase over a plaintext identity file sitting on a host other people can reach.

Plugin confinement (`rta doctor`'s "plugin confinement" row) is `sandbox-exec` on macOS and nothing on Linux — a deliberate, documented gap rather than an oversight, and Linux is the realistic OS for a remote gateway. A hardened deployment supplies its own process sandboxing there — containers, seccomp, a read-only root filesystem, an egress allowlist — since rta contributes none of its own on that platform.

Consent now has exactly one place to go. `--consent` combines with `--http` only when `--operators` names a roster, because answering a parked call needs a channel to reach a person, and [the operator channel](./66-operators.md) is the one built for it — a signed answer from an enrolled operator's own machine, never a bearer credential an agent could ride. What has *not* changed is the multi-user arithmetic: one queue, several operators, first answer wins, and the accountability question the [cost table](./10-the-boundary.md#what-this-means-for-a-team) raises for a shared server is answered only as far as the decision file naming which operator signed — the rest of what a shared server would take still stands.

`--network none` in [the container recipe](./67-containers-and-images.md#in-a-container-for-a-hardened-server) was only ever safe because stdio needs no network at all. A listener needs an inbound path: publish the container's port to wherever the reverse proxy in front of it reaches, and keep outbound scoped to what the enabled plugins actually call — not open, and not none.

## Next

[The operator channel](./66-operators.md) — how the people who run a hosted server issue grants and answer consent without a shell on it.
