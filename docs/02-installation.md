# Installation

## From source

You need Go 1.26 or newer.

```bash
git clone https://github.com/this-is-tobi/rule-them-all.git
cd rule-them-all
make install
```

That runs `go install ./cmd/rta`, putting `rta` in `$(go env GOPATH)/bin`. If that directory is not on your `$PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

To build into the current directory instead of installing:

```bash
make build      # ./rta
```

`make help` lists every target the repository has — building, formatting, the test gates, a release rehearsal, and the plugin equivalent of each.

## Container image

```bash
docker run --rm ghcr.io/this-is-tobi/rta:latest --version
```

Distroless, non-root, multi-arch (`amd64`/`arm64`), published with every release alongside SLSA provenance, an SBOM and a cosign signature. `latest` tracks the newest release; a release `1.2.3` is also tagged `1.2` and `1`, so you can pin as loosely or as tightly as you want. Verify what you pulled:

```bash
gh attestation verify oci://ghcr.io/this-is-tobi/rta:latest --owner this-is-tobi
```

Two shapes of use:

- **An MCP server** — [In a container, for a hardened server](./20-mcp.md#in-a-container-for-a-hardened-server) has the full `docker run` recipe: read-only root, dropped capabilities, no network by default.
- **A one-shot command**, anywhere `docker run` reaches, including inside a cluster: `kubectl run --rm -it rta-debug --image=ghcr.io/this-is-tobi/rta:latest -- net probe db.internal:5432`.

## Verify

```bash
rta --version
rta doctor
```

`doctor` is worth running now and worth running again whenever something behaves oddly. It reports what rta can see and — more usefully — what it can *reach*:

```
CHECK                STATUS  DETAIL
capabilities         ok      16 plugins, 101 capabilities
config               ok      ~/.config/rta/config.yaml
kv store             info    unlocks from this environment — an MCP server
                             started here can read secrets, bounded only by grants
plugin confinement   ok      sandbox-exec: 2 paths denied read+write (rta's own
                             state), 10 denied read (credential locations), 8
                             directories pinned in place so a rename cannot move
                             either out of its rule
agent log            ok      the record is intact
```

Those `info` rows are not noise. "The store unlocks from this environment" is a real statement about what an agent started from this shell inherits, and it is the kind of thing worth knowing before you connect one.

## Shell completion

rta's completion is not just subcommands — capabilities that declare `Options` complete to their allowed values, and `Suggest` inputs complete from what actually exists on your machine (your tags, your keys, your hosts file).

```bash
# zsh
rta completion zsh > "${fpath[1]}/_rta"

# bash
rta completion bash > /etc/bash_completion.d/rta

# fish
rta completion fish > ~/.config/fish/completions/rta.fish
```

Restart your shell afterwards.

## External tools

The binary is self-contained and the core needs nothing. Some capabilities shell out to a tool you already have, rather than linking a client library — that is a deliberate trade, and it is why your existing credentials, proxies, contexts and credential helpers keep working without rta learning about any of them.

Nothing here is required to install or run rta. A missing tool costs you exactly the capabilities that use it, and the refusal names the tool.

| Tool | Needed for | If it is missing |
| --- | --- | --- |
| `git` | `rta plugin index add/update`, the `git.*` capabilities | Indexes cannot be attached; `git.*` is unavailable |
| `kubectl` | the `kube` and `cnpg` plugins, `audit.kube.*`, and `kube:` tunnel targets | Those capabilities refuse, naming kubectl |
| `pg_dump` | `pg.dump` only — `pg.query` and the rest of the `pg` plugin connect in-process and need nothing | `pg.dump` refuses; every other `pg.*` capability is unaffected |
| `pg_restore`, `psql` | `pg.restore` — `pg_restore` reads custom and directory dumps, `psql` replays plain SQL | `pg.restore` refuses, naming whichever one the dump's format needs |
| `docker` | the `docker` plugin | Those capabilities refuse |
| `ssh` | `ssh:` tunnel targets | Those targets cannot be resolved |
| `cosign` | verifying a plugin artifact's signature, when an index states one | The outcome is recorded as unverifiable; **an install is never blocked, because a signature is recorded and never required** |

Version policy is the same for all of them: rta uses what is on your `$PATH` and adopts none of their maintenance. There is no preflight check — a capability looks its tool up when you run it, and refuses by name if it is not there.

Two consequences worth knowing. The primary container image is distroless, so it carries none of these — a containerised `rta mcp serve` covers the capabilities that need no external tool, and [MCP and the safety gate](./20-mcp.md) says which. And a plugin that reads a credential location, such as kubectl's `~/.kube/config`, still needs `rta plugin allow` before it may: having the tool is not being granted the file.

If you want the tools rather than the narrowness, `ghcr.io/this-is-tobi/rta-full` is the same rta with every first-party plugin and every tool in the table above already in it — Alpine-based rather than distroless, because a distroless image has no package manager to put them there. Roughly 100 MB against the primary image's 12, and the plugins arrive already trusted, since they were built from the same source in the same build.

**Reach for it when you are the one at the keyboard, and for the primary image when something else is.** That is not a style preference: [the image is the plugin allowlist](./20-mcp.md) — a plugin that is not in the image is one an agent cannot reach at all — so the full image is the widest reach rta has, and handing it to an agent gives up a boundary the narrow one enforces for free. For a team that wants three plugins and not eleven, derive from the primary image instead; [the recipe](./20-mcp.md) is a dozen lines. What the full image does *not* do is answer the credential question for you: `kube` and `cnpg` still show `warn` until you run `rta plugin allow`, on your machine, against your own kubeconfig. Mount a state volume at `/rta-home` and that answer sticks, the same as on a laptop.

For the throwaway case where it cannot stick — `docker run --rm`, with no volume — the entrypoint takes `RTA_ALLOW_PLUGINS`, and it is off unless you set it:

```bash
docker run --rm -e RTA_ALLOW_PLUGINS=kube,cnpg \
  -v ~/.kube:/rta-home/.kube:ro ghcr.io/this-is-tobi/rta-full kube pod list
```

`all` covers every bundled plugin that asks for something. Naming a plugin that asks for nothing is an error rather than a no-op, because you typed it expecting it to mean something. It can only ever grant what a plugin already declares — `rta plugin allow` cannot invent a location the artifact never asked for — and setting it is visible in the command, the compose file or the pod spec that launched the container, which is the point of it not being a default.

## Configuration

rta runs with no configuration at all. When you want some:

```bash
rta init
```

That writes `~/.config/rta/config.yaml` (or the platform equivalent — `rta doctor` prints the exact path). `RTA_CONFIG` overrides the location, which is what portable setups and test harnesses use.

Nothing in the config grants anything. It holds connection profiles, dashboard preferences and theme — see [Profiles](./40-profiles.md).

## Where rta keeps things

| What | Where | Notes |
| --- | --- | --- |
| Config | `~/.config/rta/config.yaml` | `RTA_CONFIG` overrides |
| Encrypted store | beside the config | [Secrets](./30-secrets.md) |
| Grants | `~/.local/share/rta/grants.json` | Sealed against tampering |
| Agent record | beside the grants | Hash-chained; [The record](./22-audit-trail.md) |
| Team policy | `.rta-policy.yaml`, walking up from the working directory | [Team policy](./23-team-policy.md) |

Exact paths differ per platform. `rta doctor` prints the real ones rather than the documented ones, which is the answer to use when they disagree.

## Next

- [Quick start](./03-quickstart.md) — the first ten minutes
- [MCP and the safety gate](./20-mcp.md) — if you came here to connect an agent
