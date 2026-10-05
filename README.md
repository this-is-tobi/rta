# RTA :ring:

**RTA**, short for *Rule Them All* and written `rta` on the command line, is one binary over the tools you already juggle: databases, object storage, secrets, networking, certificates, host telemetry, HTTP APIs. Every capability is written once and rendered on three surfaces: a scriptable CLI, an interactive TUI, and an [MCP](./docs/95-reference/10-glossary.md#acronyms) server for AI agents.

## It works with your agent, not instead of it

rta is not another AI CLI, and it does not want to be the thing you talk to. It is the layer underneath the one you already use — Claude Code, Codex, Cursor, Copilot, Gemini — the part that decides what those agents are actually allowed to touch.

That is the whole proposition. Handing an agent a shell is **one decision that covers everything it will ever do**. Pointing it at rta is a different shape: read-only by default, everything else granted per capability, narrowed to one record, expiring on its own, and written down. You keep your agent; it gets a smaller blast radius and you get a record.

Which is why the security pages below are not an appendix, and why every one of them is also usable by a person at a terminal. The same capability serves both — nothing here is an agent-only feature bolted on.

## Install

```bash
# a release binary, with the gh CLI (macOS or Linux, amd64 or arm64)
gh release download --repo this-is-tobi/rta --pattern "rta_*_$(uname -s | tr A-Z a-z)_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz" && tar -xzf rta_*.tar.gz rta && install -m 0755 rta /usr/local/bin/rta
# or from source, with Go 1.26 or newer
git clone https://github.com/this-is-tobi/rta.git && cd rta && make install
# or the container image
docker run --rm ghcr.io/this-is-tobi/rta:latest --version
```

[Installation](./docs/10-getting-started/10-installation.md) has the curl form, the Linux packages and how to verify a download. rta runs on macOS and Linux, on both `amd64` and `arm64`, and [Supported platforms](./docs/10-getting-started/10-installation.md#supported-platforms) lists where the two differ. It does not run natively on Windows; the Linux build under the Windows Subsystem for Linux, version 2, has not been tested.

## Try it

```bash
rta sys overview          # your machine at a glance: cpu, memory, disk, load
rta                       # the interactive dashboard, with a search over every capability
rta mcp install claude    # give Claude Code rta's tools: it reads what is local and is refused anything else
```

Ask Claude Code to add a note and the refusal carries the exact line that would allow it, which is the loop the rest of rta is built around. [Quick start](./docs/10-getting-started/20-quickstart.md) walks it in ten minutes, and [Start here](./docs/01-readme.md) lays out four tracks: try it, give an agent access safely, run it for a team, and extend it.

## Documentation

**Website:** <https://this-is-tobi.com/rta/introduction>

**Table of Contents** *- md sources*:

*Getting started*
- [Installation](./docs/10-getting-started/10-installation.md) *- Download it, put it on `$PATH`, check it works, and where the two supported systems differ*
- [Quick start](./docs/10-getting-started/20-quickstart.md) *- Ten minutes: the CLI, the TUI, and an agent you connect, refuse and allow*
- [Connect an agent](./docs/10-getting-started/30-connect-an-agent.md) *- Registering a client, naming it, and checking that it reached rta*
- [Other ways to install](./docs/10-getting-started/40-other-ways-to-install.md) *- From source, the container image, the Helm chart, and the plugins that arrive from an index*

*Using it*
- [The CLI](./docs/20-using/10-cli.md) *- Output formats, exit codes, `--dry-run`, `explain`, scripting*
- [The TUI](./docs/20-using/20-tui.md) *- The dashboard, answering agents, the catalogue, forms and confirmations*
- [Dashboard and theme](./docs/20-using/25-dashboard-and-theme.md) *- Stating the dashboard in the config file, and the colours*
- [Seeing the shape of things](./docs/20-using/30-trees.md) *- Mapping a directory, a bucket, a Vault mount or an etcd keyspace in one call*
- [Profiles](./docs/20-using/40-profiles.md) *- Naming an environment once and pointing every plugin at it*
- [Profiles in depth](./docs/20-using/41-profiles-in-depth.md) *- Several connections to one plugin, a profile from a script, types, and editor completion*
- [Reaching private services](./docs/20-using/42-reaching-private-services.md) *- A connection through `kubectl port-forward` or `ssh`, and the certificate rules that come with it*
- [Secrets (`kv`)](./docs/20-using/50-secrets.md) *- An encrypted local store for passwords, certificates and key files*

*The boundary*
- [What rta actually bounds](./docs/30-boundary/10-the-boundary.md) *- Read this first before giving an agent access: an agent with a shell is not bounded by rta, and this is how to be in the configuration where it is*
- [MCP and the safety gate](./docs/30-boundary/20-mcp.md) *- What a connected agent can reach before you grant anything, and what every call is checked against*
- [Grants](./docs/30-boundary/30-grants.md) *- Time-boxed permission for one capability, optionally one record*
- [Roles, consent and the guard](./docs/30-boundary/35-roles-consent-and-the-guard.md) *- A day of grants under one word, answering a parked call, and a passphrase in front of issuance*
- [The record](./docs/30-boundary/40-audit-trail.md) *- What agents asked for, what they got, and what is waiting on you*
- [Stop an agent now](./docs/30-boundary/45-stop-an-agent-now.md) *- Locks: the instant no, ahead of any expiry or revocation*
- [Team policy](./docs/30-boundary/50-team-policy.md) *- A ceiling a repository can commit, which can only ever subtract*
- [Connecting your AI tool](./docs/30-boundary/60-ai-clients.md) *- Claude Code, VS Code, Cursor, Codex, Gemini, Copilot, and anything else that speaks MCP*
- [Hosting a server](./docs/30-boundary/65-hosting-a-server.md) *- `--http`, tokens, probes, and what a remote server leaves out*
- [The operator channel](./docs/30-boundary/66-operators.md) *- Issuing grants and answering consent on a server you have no shell on*
- [Containers and images](./docs/30-boundary/67-containers-and-images.md) *- The hardened server recipe, sharing an image, and why not one server for everyone*
- [OIDC](./docs/30-boundary/70-oidc.md) *- Naming the person behind a call with the identity provider you already have: what rta verifies, a Keycloak walkthrough, and where the failure messages go*
- [Kubernetes](./docs/30-boundary/80-kubernetes.md) *- Deploying the boundary as a chart, one instance per person: the decisions to make before setting a value, the posture worth choosing, and day two*

*Plugins*
- [Using plugins](./docs/40-plugins/10-plugins.md) *- Discovery, trust and what a plugin may touch*
- [Indexes and upgrades](./docs/40-plugins/12-indexes-and-upgrades.md) *- Attaching an index, searching and installing from it, and upgrading what you installed*
- [The plugin inventory](./docs/40-plugins/15-the-plugin-inventory.md) *- What the TUI's plugin pane shows, and the plugins rta found and refused to run*
- [Writing a plugin](./docs/40-plugins/20-writing-a-plugin.md) *- Fifteen minutes from `plugin new` to a plugin that runs: the loop, what an answer is, and worked examples*
- [Declaring inputs](./docs/40-plugins/21-declaring-inputs.md) *- What a capability takes, and how a connection is declared once for every capability of a plugin*
- [Answers, errors and hints](./docs/40-plugins/22-answers-errors-and-hints.md) *- Hints in the reader's own words, connection failures, refusals and warnings*
- [Safety, credentials and what rta does to your process](./docs/40-plugins/23-safety-and-credentials.md) *- The claims a capability makes about itself, and the checks on declared text*
- [Testing, conventions and publishing](./docs/40-plugins/24-testing-and-publishing.md) *- The conformance suite, the habits worth keeping, and publishing it to an index*

*Recipes*
- [Recipes](./docs/90-recipes/01-readme.md) *- Worked end-to-end examples: incident triage, a scoped agent, CI checks, backups*
- [For a security team](./docs/90-recipes/10-for-security-teams.md) *- Deploying rta, committing a ceiling, handing out roles, reviewing grants and the record, stopping an agent*
- [For a developer](./docs/90-recipes/20-for-developers.md) *- A day's work: environments, secrets, grants for your agent, the TUI*
- [An agent in a cluster](./docs/90-recipes/30-an-agent-in-a-cluster.md) *- From a profile to an agent connected over MCP, with a minted, expiring ServiceAccount token the only thing between them and the cluster*

*Reference*
- [Glossary](./docs/95-reference/10-glossary.md) *- The words these pages use in one particular way — grant, record, profile, role, plugin — and the acronyms they assume*
- [The path gate](./docs/95-reference/20-the-path-gate.md) *- Roots, symbolic links, hard links and `git`: exactly where the line falls*
- [Verify a download](./docs/95-reference/30-verify-a-download.md) *- Checksums, build attestations and signatures for the archive, the image and the chart*
- [External tools](./docs/95-reference/40-external-tools.md) *- The programs some capabilities shell out to, and what is lost without each*
- [Where rta keeps things](./docs/95-reference/50-where-rta-keeps-things.md) *- The config file, the data directory and every file in them*

## What is in it

**20 built-in plugins, 128 capabilities** in the default build, and `rta plugin list` is the inventory:

`sys` · `net` · `http` · `cert` · `fs` · `kv` · `note` · `gen` · `codec` · `time` · `audit` · `grant` · `agent` · `operator` · `lock` · `pkg` · `debug` · `keys` · `git` · `eol`

Plus anything you install. Twelve first-party plugins live in [rta-plugins](https://github.com/this-is-tobi/rta-plugins) as proof the contract works — `pg`, `mysql`, `mariadb`, `etcd`, `qdrant`, `redis`, `s3`, `vault`, `kube`, `cnpg`, `docker` and `keycloak` — each a separate binary from a separate module, so the ones you skip cost you nothing, and `rta plugin index add official` is how they arrive.
