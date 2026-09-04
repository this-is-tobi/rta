# Installation

## From a release

Every release ships prebuilt binaries for Linux, macOS and Windows on both `amd64` and `arm64`, as `rta_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows), plus `.deb`/`.rpm`/`.apk` packages for Linux. With the [gh CLI](https://cli.github.com):

```bash
gh release download --repo this-is-tobi/rule-them-all \
    --pattern "rta_*_$(uname -s | tr A-Z a-z)_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz" \
    --pattern checksums.txt
```

Or with nothing but curl — the asset names carry the version, so ask the API which one is latest first:

```bash
tag=$(curl -s https://api.github.com/repos/this-is-tobi/rule-them-all/releases/latest | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
os=$(uname -s | tr A-Z a-z); arch=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -fsSLO "https://github.com/this-is-tobi/rule-them-all/releases/download/v${tag}/rta_${tag}_${os}_${arch}.tar.gz"
curl -fsSLO "https://github.com/this-is-tobi/rule-them-all/releases/download/v${tag}/checksums.txt"
```

**Verify before you extract.** The checksum proves the download is intact; the attestation proves the archive was built by this repository's release workflow from the tagged commit — provenance, not just integrity:

```bash
shasum -a 256 -c checksums.txt --ignore-missing     # sha256sum on Linux
gh attestation verify rta_*_"${os}"_"${arch}".tar.gz --owner this-is-tobi
```

Then put the binary on your `$PATH`:

```bash
tar -xzf rta_*.tar.gz rta
install -m 0755 rta /usr/local/bin/rta    # or ~/.local/bin, anywhere on $PATH
rta --version
```

On a Debian, RPM or Alpine machine the package does the `$PATH` half for you: download the matching `.deb`, `.rpm` or `.apk` the same way and hand it to `dpkg -i` / `rpm -i` / `apk add --allow-untrusted`.

Upgrading is the same download again — the new binary replaces the old one, and your config, store and grants live in your own directories, untouched (see "Where rta keeps things" below).

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

## Installing plugins from an index

`rta` is one binary and every plugin is a separate module, so neither `go install` nor a release archive brings them along. An **index** is how one arrives: a git repository of `plugins/<name>.yaml` manifests, each generated from a plugin binary's own declaration. Attach one, search it, install from it.

```bash
rta plugin index add official
rta plugin search vault
rta plugin install vault
```

Install is where the claim meets the evidence: rta fetches the artifact, hashes it, launches it in the same sandbox any load uses, and refuses it if what it declares is not what the index said — naming the index that got it wrong. Installing *is* the trust decision, so there is no separate approval step afterwards.

**Nothing is attached until you say so.** `official` is the one index rta knows by name — [rta-plugins](https://github.com/this-is-tobi/rta-plugins), where the first-party plugins are built, released and described — and the name is reserved for it, so `rta plugin index add official <elsewhere>` is refused. Any other index is `rta plugin index add <name> <repository>`, and a source repository is not one: a source tree's `plugins/` holds a directory per plugin, an index holds a manifest per plugin, and `rta plugin index add` reads what it cloned and says so rather than attaching something that would answer every search with silence.

Two ways to skip the index entirely. **From source**, `make install` in [rta-plugins](https://github.com/this-is-tobi/rta-plugins) puts `rta-plugin-<name>` beside your `rta` and `make trust` there approves the ones built — a binary on your `$PATH` is not consent, and [Trust](../40-plugins/10-plugins.md#trust) is why. **Already in an image**, `ghcr.io/this-is-tobi/rta-full` carries every first-party plugin already trusted; [Container image](#container-image) has the tradeoff.

[Plugins](../40-plugins/10-plugins.md#indexes) is the whole model.

## Container image

```bash
docker run --rm ghcr.io/this-is-tobi/rta:latest --version
```

Distroless, non-root, multi-arch (`amd64`/`arm64`), published with every release alongside SLSA provenance, an SBOM and a cosign signature. `latest` tracks the newest release; a release `1.2.3` is also tagged `1.2` and `1`, so you can pin as loosely or as tightly as you want. Verify what you pulled:

```bash
gh attestation verify oci://ghcr.io/this-is-tobi/rta:latest --owner this-is-tobi
```

Two shapes of use:

- **An MCP server** — [In a container, for a hardened server](../30-boundary/20-mcp.md#in-a-container-for-a-hardened-server) has the full `docker run` recipe: read-only root, dropped capabilities, no network by default.
- **A one-shot command**, anywhere `docker run` reaches, including inside a cluster: `kubectl run --rm -it rta-debug --image=ghcr.io/this-is-tobi/rta:latest -- net probe db.internal:5432`.

## Verify

```bash
rta --version
rta doctor
```

`doctor` is worth running now and worth running again whenever something behaves oddly. It reports what rta can see and — more usefully — what it can *reach*:

```
CHECK                STATUS  DETAIL
capabilities         ok      18 plugins, 115 capabilities
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
| `mysqldump`, `mysql` | `mysql.dump` and `mysql.restore` only — every other `mysql.*` capability connects in-process | Those two refuse; the rest of the plugin is unaffected |
| `mariadb-dump`, `mariadb` | `mariadb.dump` and `mariadb.restore`, same split. The legacy `mysqldump`/`mysql` symlinks every distribution still ships are accepted too | Those two refuse, naming both spellings |
| `docker` | the `docker` plugin | Those capabilities refuse |
| `ssh` | `ssh:` tunnel targets | Those targets cannot be resolved |
| `cosign` | verifying a plugin artifact's signature, when an index states one | The outcome is recorded as unverifiable; **an install is never blocked, because a signature is recorded and never required** |

Version policy is the same for all of them: rta uses what is on your `$PATH` and adopts none of their maintenance. There is no preflight check — a capability looks its tool up when you run it, and refuses by name if it is not there.

**MySQL and MariaDB share tool names, and that is the one place having a tool is not enough.** MariaDB ships `mysqldump` and `mysql` as symlinks onto its own binaries, so a lookup by name succeeds against either fork and the flags decide what happens next: `mysql.dump` passes MySQL 8 spellings (`--ssl-mode`, `--set-gtid-purged`, `--no-tablespaces`) that MariaDB's client refuses by name, and `mariadb.dump` passes the `--ssl` family that MySQL's client refuses the same way. Neither produces a worse dump — it is a refusal at the first flag, graded as `*.dump.toolskew`, and its hint tells you which client you really have and which plugin drives it. Point each plugin at its own fork and this never comes up.

Two consequences worth knowing. The primary container image is distroless, so it carries none of these — a containerised `rta mcp serve` covers the capabilities that need no external tool, and [MCP and the safety gate](../30-boundary/20-mcp.md) says which. And a plugin that reads a credential location, such as kubectl's `~/.kube/config`, still needs `rta plugin allow` before it may: having the tool is not being granted the file.

If you want the tools rather than the narrowness, `ghcr.io/this-is-tobi/rta-full` is the same rta with every first-party plugin and the tools from the table above already in it — Alpine-based rather than distroless, because a distroless image has no package manager to put them there. Roughly 120 MB against the primary image's 12, and the plugins arrive already trusted, since they were built from the same source in the same build.

One row of that table it cannot carry, for the reason just above: Alpine has no Oracle MySQL client — its `mysql-client` package is MariaDB's — so the image carries `mariadb-client`, and `mysql.dump`/`mysql.restore` are the two capabilities in it that will not run. They refuse at the first flag with the skew message, which is the diagnosis rather than a mystery; bring Oracle's client yourself if you need them.

**Reach for it when you are the one at the keyboard, and for the primary image when something else is.** That is not a style preference: [the image is the plugin allowlist](../30-boundary/20-mcp.md) — a plugin that is not in the image is one an agent cannot reach at all — so the full image is the widest reach rta has, and handing it to an agent gives up a boundary the narrow one enforces for free. For a team that wants three plugins and not eleven, derive from the primary image instead; [the recipe](../30-boundary/20-mcp.md) is a dozen lines. What the full image does *not* do is answer the credential question for you: `kube` and `cnpg` still show `warn` until you run `rta plugin allow`, on your machine, against your own kubeconfig. Mount a state volume at `/rta-home` and that answer sticks, the same as on a laptop.

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

Nothing in the config grants anything. It holds connection profiles, dashboard preferences and theme — see [Profiles](../20-using/40-profiles.md).

## Where rta keeps things

| What | Where | Notes |
| --- | --- | --- |
| Config | `~/.config/rta/config.yaml` | `RTA_CONFIG` overrides |
| Encrypted store | beside the config | [Secrets](../20-using/50-secrets.md) |
| Grants | `~/.local/share/rta/grants.json` | Sealed against tampering |
| Agent record | beside the grants | Hash-chained; [The record](../30-boundary/40-audit-trail.md) |
| Team policy | `.rta-policy.yaml`, walking up from the working directory | [Team policy](../30-boundary/50-team-policy.md) |

Exact paths differ per platform. `rta doctor` prints the real ones rather than the documented ones, which is the answer to use when they disagree.

## Next

- [Quick start](./20-quickstart.md) — the first ten minutes
- [MCP and the safety gate](../30-boundary/20-mcp.md) — if you came here to connect an agent
