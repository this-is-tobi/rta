# Glossary

The words this documentation uses in one particular way, and the acronyms it assumes you know, each with the chapter that says the rest. A term here means the same thing on every page; when a page seems to use one differently, the page is wrong, not the glossary.

## Terms

- **age** — the file encryption format and tool the secret store is built on, and the kind of key `rta kv init --generate` makes. A name, not an acronym. [Secrets](../20-using/50-secrets.md)
- **agent** — the name an AI client is registered under, `rta mcp serve --as claude`. Grants are issued to that name and a lock freezes it, so consent given while talking to one client does not follow another. Also, loosely, the AI tool itself. [Connect an agent](../10-getting-started/30-connect-an-agent.md#name-it)
- **capability** — one declared unit of work with an ID (`kv.get`, `pg.query`), typed inputs and a safety class. The CLI command, the TUI form and the MCP tool are all generated from that one declaration. [Introduction](../01-readme.md)
- **consent** — answering a call that needs a grant nobody issued, while you are at the machine, instead of having it refused. A server started with `--consent` parks such a call and `rta agent allow` or `rta agent deny` answers it. [Live consent](../30-boundary/30-grants.md#live-consent-when-you-would-rather-be-asked)
- **digest** — the hash of a plugin's binary. Trust, and a grant's binding to a plugin build, are to a digest and never to a name or a version, so a rebuild asks again. [Using plugins](../40-plugins/10-plugins.md)
- **forward** — a `kubectl port-forward` or an `ssh -L` that rta opens for one call when a profile says a service is reached through a cluster or a host, and closes afterwards. [Profiles](../20-using/40-profiles.md)
- **grant** — permission for one capability, optionally one record, that expires on its own. A read that stays on this machine is free; everything that changes anything, and a read aimed at a destination the agent names, costs a grant. [Grants](../30-boundary/30-grants.md)
- **guard** — the passphrase in front of issuing a grant, which makes "issue itself a grant" stop working for a process that only runs as you. Off by default. [Grants](../30-boundary/30-grants.md#the-guard-a-passphrase-in-front-of-issuance)
- **index** — a git repository holding one manifest per plugin, which `rta plugin index add` points rta at and `rta plugin install` reads. The first-party one is `official`. [Using plugins](../40-plugins/10-plugins.md)
- **instance** — one of several connections a profile holds for the same plugin, named `profile/instance` (`staging/analytics`). The profile's default connection has no label. [Profiles](../20-using/40-profiles.md#several-connections-to-one-plugin)
- **lock** — the instant no: freezes one agent, credential or operator now, ahead of any expiry or revocation, and lifts itself after a window if it was given one. [Locks](../30-boundary/45-stop-an-agent-now.md)
- **operator** — a person who manages a remote rta server by signing calls with a key only their passphrase can use, over the operator channel. [The operator channel](../30-boundary/66-operators.md)
- **plugin** — a program that returns a declaration of capabilities and serves it over gRPC. Some are built into the binary; the rest are separate binaries from an index, each approved to run by its digest. [Using plugins](../40-plugins/10-plugins.md) · [Writing a plugin](../40-plugins/20-writing-a-plugin.md)
- **policy** — a team's file, `.rta-policy.yaml`, that no `--ttl` can argue with: a longest lifetime, targets never granted, connections never named, targets that need a named record. [Team policy](../30-boundary/50-team-policy.md)
- **profile** — a named environment across every plugin that has something in it. `staging` points `pg`, `s3` and `vault` at staging at once, and is also what a grant is scoped to with `--profile`. [Profiles](../20-using/40-profiles.md)
- **record** — two things, told apart by the article. *The record* is the chained, sealed log of every call that arrived over MCP and every authority change an operator made, read with `rta agent log`. *A record* is the one thing a call names — a key, an object, a host, a table — and what a grant can be narrowed to. [The record](../30-boundary/40-audit-trail.md) · [Grants](../30-boundary/30-grants.md)
- **role** — a list of grants written once, in a policy or in your config, that `rta grant issue <role>` issues to one agent under one passphrase prompt. [Roles](../30-boundary/30-grants.md#roles-a-day-of-grants-under-one-word)
- **root** — a directory a path argument must sit under. The server's default is the directory it was started in, and `--root` widens it. [The path gate](./20-the-path-gate.md)
- **roster** — a list rta keeps of who may do what: the grant roster that `rta grant list` shows, and on a remote server the operator roster, the file of enrolled operator keys. [Grants](../30-boundary/30-grants.md) · [The operator channel](../30-boundary/66-operators.md)
- **safety class** — `read`, `write` or `destructive`, declared by each capability. It decides what an agent may reach without a person: reads by default, everything else with a grant. A capability that reveals a secret is `write` even though it changes nothing. [Writing a plugin](../40-plugins/20-writing-a-plugin.md#safety-is-a-claim-not-a-label)
- **store** — the encrypted local secret store behind `rta kv`, locked to an age key or a passphrase. [Secrets](../20-using/50-secrets.md)
- **surface** — a way of reaching a capability: the CLI, the TUI or MCP. `-o json`, csv and markdown are formats the CLI answers in, not surfaces of their own. [Introduction](../01-readme.md)

## Acronyms

- **CA** — certificate authority; the issuer a TLS certificate chains up to.
- **CLI** — command-line interface: `rta` typed at a shell.
- **CNPG** — CloudNativePG, the PostgreSQL operator for Kubernetes that the `cnpg` plugin reads.
- **CWE** — Common Weakness Enumeration, the numbered weaknesses (`CWE-250`) an `rta audit` finding cites beside its OWASP category.
- **DSN** — data source name, a database's connection string; a profile spells the same thing as separate `set:` keys.
- **gRPC** — the remote procedure call protocol a plugin serves its declaration over.
- **JSONC** — JSON with comments, the format VS Code's `mcp.json` is written in, which is why rta prints its block instead of editing the file.
- **JWK** — JSON Web Key, a key written as JSON; `rta codec jwk` reads one and says its type, size and thumbprint, and whether it is private.
- **MCP** — Model Context Protocol, how an AI client discovers and calls tools. `rta mcp serve` is the server. [MCP and the safety gate](../30-boundary/20-mcp.md)
- **OCI** — Open Container Initiative; a plugin or the Helm chart can be published to an OCI registry and fetched from there. [Using plugins](../40-plugins/10-plugins.md)
- **OIDC** — OpenID Connect; an HTTP-hosted server can require a token from an identity provider. [OIDC](../30-boundary/70-oidc.md)
- **OSV** — the open vulnerability database `rta audit deps` asks.
- **OWASP** — the Open Worldwide Application Security Project, whose Top 10 categories (`A01:2025`) an `rta audit` finding cites.
- **RBAC** — role-based access control; in Kubernetes, what decides who may read which resource, and what rta's `kube` plugin leaves to the cluster. [Kubernetes](../30-boundary/80-kubernetes.md)
- **RESP** — the REdis Serialization Protocol; the `redis` plugin speaks it itself rather than linking a client library.
- **REST** — the style of HTTP API that names a thing by its URL and acts on it with `GET`, `POST`, `PUT` and `DELETE`; `rta http` is a client for one.
- **RTA** — short for *Rule Them All*, the name of the project; `rta` is the binary and how it is typed.
- **RWX** — ReadWriteMany, a volume that several pods mount at once. The chart refuses it: two rta processes on one data directory would disagree about the grants. [Kubernetes](../30-boundary/80-kubernetes.md)
- **SBOM** — software bill of materials, the list of what a build contains; `rta audit deps` reads one, and a release publishes its own.
- **SIEM** — security information and event management, the system a log is shipped to; the codes in the record are what to match on there, not the wording.
- **SLSA** — Supply-chain Levels for Software Artifacts; the build provenance a release is published with. [Verify a download](./30-verify-a-download.md)
- **TLS** — Transport Layer Security, the encryption beneath `https` and most database connections.
- **TTL** — time to live; `--ttl 30m` is how long a grant, a profile or a lock stands before it lapses on its own.
- **TTY** — a terminal. A form "at a TTY" is one a person fills in, as opposed to a script or a pipe, which has none.
- **TUI** — terminal user interface: `rta` with no arguments. [The TUI](../20-using/20-tui.md)
- **UTC** — Coordinated Universal Time, the zone `rta time at` shows an instant in beside the local one.
