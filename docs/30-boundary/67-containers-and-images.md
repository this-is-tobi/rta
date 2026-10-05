# Containers and images

Two images ship with every release and you can build a third. `ghcr.io/this-is-tobi/rta` is the narrow one: distroless, non-root, multi-arch (`amd64`/`arm64`), carrying the rta binary and nothing else. `ghcr.io/this-is-tobi/rta-full` is the console: every first-party plugin and the external tools they shell out to. The one you build is the narrow image plus exactly the plugins and profiles a team needs. Which of them an agent should be pointed at is the question this page answers, and it is not the same answer for all three.

```bash
docker run --rm ghcr.io/this-is-tobi/rta:latest --version
```

Every release publishes them with SLSA provenance, an SBOM and a cosign signature; `latest` tracks the newest release, and a release `1.2.3` is also tagged `1.2` and `1`. [Verify a download](../95-reference/30-verify-a-download.md#the-container-image) is how to check what you pulled. A one-shot command needs nothing more than `docker run`, inside a cluster as well: `kubectl run --rm -it rta-debug --image=ghcr.io/this-is-tobi/rta:latest -- net probe db.internal 5432`.

## In a container, for a hardened server

The binary is static and needs almost nothing at runtime — almost, because `cert`, `http`, `audit web` and every plugin that dials TLS (`pg`, `s3`, `vault`, `qdrant`...) still need a CA bundle to verify against, which a bare `scratch` image does not have. [`ghcr.io/this-is-tobi/rta`](https://github.com/this-is-tobi/rta/pkgs/container/rta) is built `FROM gcr.io/distroless/static-debian12:nonroot` instead: that CA bundle and the `/etc/passwd` entry for its nonroot user, and nothing else — still no shell, no package manager, no libc for anything to reach. Published multi-arch (`amd64`/`arm64`) with every release, with SLSA provenance, an SBOM and a cosign signature attached to the image digest. Point the client at `docker` instead of at `rta`:

```json
{
  "mcpServers": {
    "rta": {
      "command": "docker",
      "args": [
        "run", "--rm", "-i",
        "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
        "--network", "none",
        "-v", "rta-home:/rta-home",
        "-v", "${workspaceFolder}:/work:ro",
        "-e", "RTA_CONFIG=/rta-home/config.yaml",
        "-e", "RTA_DATA_DIR=/rta-home",
        "-w", "/work",
        "ghcr.io/this-is-tobi/rta:latest", "mcp", "serve", "--as", "sandboxed", "--root", "/work"
      ]
    }
  }
}
```

What each part is doing, since a hardening flag nobody can explain is a hardening flag somebody deletes:

| Flag | Why |
| --- | --- |
| `-i` | stdio is the transport; without it the client and the server never meet |
| `--read-only`, `--cap-drop ALL`, `--security-opt no-new-privileges` | The server needs none of it, so it gets none of it |
| `--network none` | The strongest setting, and it turns off every capability that reaches the network — `audit web`, `net dns`, `pg` against anything remote. Drop it when you want those |
| `-v rta-home:/rta-home` | Grants and the record have to outlive the container, or every restart is a machine with no memory of what you allowed |
| `-e RTA_CONFIG`, `-e RTA_DATA_DIR` | **Not optional.** With no config directory the config path falls back to `./.rta.yaml`, and a working-directory file is not honoured — `profiles:`, `plugins:` and `dashboard:` are all ignored, so a plugin would run with its declared defaults |
| `-w /work` + `--root /work` | The path root defaults to the working directory, which in a container is `/` unless you say otherwise |

`rta audit clients` grades a declaration against this recipe, so none of it has to be checked by eye. A data directory with nothing mounted at it, a missing `--root`, and any of the three hardening flags being absent all warn — as does pointing an agent at `rta-full`, where the bundled plugins are trusted at build time and a read needs no grant, so a dozen plugins' reads are reachable with no consent step. Every gate still applies there; what widens is how much sits behind none of them.

Missing `RTA_CONFIG`/`RTA_DATA_DIR` is the one graded by which image you run. Against the published narrow image it fails, because that image's environment is defined here and sets neither, so the settings really are being ignored. Against any other image — your own build, the recipe below, a fork — it is only reported: a derived image may set them itself, reading its environment would mean pulling it, and a private deployment building its own image is the ordinary case rather than a suspicious one. `--network none` is treated as a bonus rather than a baseline, and is the one check that reports its presence instead of its absence: it turns off every capability that reaches the network, so most people running rta for what rta is for cannot use it. Closing the network is confirmed when you have done it; leaving it open earns no row at all, because a row on the ordinary correct setup reads as a deficiency and teaches people to skim past the ones that matter.

**The image is the plugin allowlist.** A plugin is a separate binary, so a plugin that is not in the image is a plugin the agent cannot reach — no trust decision, no digest, no `$PATH` to search. Building the image with two plugins in it is the narrowest reach rta can be given.

Which is exactly why **`ghcr.io/this-is-tobi/rta-full` is the wrong image to point an agent at.** It carries every first-party plugin and every external tool, so it is the widest reach rta has, and pointing an MCP client at it throws away the one boundary this section is about. It exists for a person at a terminal who wants a console; for an agent, build the narrow image with the plugins that job needs — the recipe is [further down](#a-team-share-the-configuration-not-the-process).

The trade is real and worth stating: a containerized server sees the container's filesystem and network, so `fs tree` maps what you mounted and nothing else, and `git status` sees `/work`. That is the point, and it is also the reason this is not the default.

## `rta-full`: the console

If you want the tools rather than the narrowness, `ghcr.io/this-is-tobi/rta-full` is the same rta with every first-party plugin and the tools from [the external tools table](../95-reference/40-external-tools.md) already in it — Alpine-based rather than distroless, because a distroless image has no package manager to put them there. Roughly 120 MB against the primary image's 12, and the plugins arrive already trusted: the image installed them from the official index at a commit it names, verified each against the index's sha256, and keeps them with their trust under `/usr/local/lib/rta`, the read-only system root a state volume on `/rta-home` cannot hide. Its version tag names the rta inside; the plugins are the official index's at the time of the build, and the image is rebuilt and republished under that tag when they move — so pin the digest, as with any image, if you need the exact set.

One row of that table it cannot carry: Alpine has no Oracle MySQL client — its `mysql-client` package is MariaDB's — so the image carries `mariadb-client`, and `mysql.dump`/`mysql.restore` are the two capabilities in it that will not run. They refuse at the first flag with the skew message, which is the diagnosis rather than a mystery; bring Oracle's client yourself if you need them.

**Reach for it when you are the one at the keyboard, and for the primary image when something else is.** That is not a style preference: [the image is the plugin allowlist](#in-a-container-for-a-hardened-server) — a plugin that is not in the image is one an agent cannot reach at all — so the full image is the widest reach rta has, and handing it to an agent gives up a boundary the narrow one enforces for free. For a team that wants three of the plugins and not all of them, derive from the primary image instead; [the recipe](#a-team-share-the-configuration-not-the-process) is a dozen lines. What the full image does *not* do is answer the credential question for you: `kube` and `cnpg` still show `warn` until you run `rta plugin allow`, on your machine, against your own kubeconfig. Mount a state volume at `/rta-home` and that answer sticks, the same as on a laptop.

For the throwaway case where it cannot stick — `docker run --rm`, with no volume — the entrypoint takes `RTA_ALLOW_PLUGINS`, and it is off unless you set it:

```bash
docker run --rm -e RTA_ALLOW_PLUGINS=kube,cnpg \
  -v ~/.kube:/rta-home/.kube:ro ghcr.io/this-is-tobi/rta-full kube pod list
```

`all` covers every bundled plugin that asks for something. Naming a plugin that asks for nothing is an error rather than a no-op, because you typed it expecting it to mean something. It can only ever grant what a plugin already declares — `rta plugin allow` cannot invent a location the artifact never asked for — and setting it is visible in the command, the compose file or the pod spec that launched the container, which is the point of it not being a default.

## A team: share the configuration, not the process

The want is real and worth stating plainly: a team has environments — dev and staging for app A, staging and production for app B — everyone has their own agent, and nobody wants to configure the same six profiles on eight laptops. What people reach for is one shared MCP server everyone points at.

**Share the image instead.** A profile is written by a command, so it can be baked in at build time, and every member starts with the environments already there and nothing to configure. Two things go into the image and neither goes under the state volume: the plugins with their trust, into rta's read-only system root, and the profiles, into a config file the image carries. The state volume each member mounts on `/rta-home` then holds only what is theirs — grants and the record — and hides nothing the image put there:

```dockerfile
FROM alpine:3.20 AS setup
COPY --from=ghcr.io/this-is-tobi/rta:latest /usr/local/bin/rta /usr/local/bin/rta
COPY rta-plugin-pg /usr/local/bin/
ENV RTA_CONFIG=/etc/rta/config.yaml
RUN mkdir -p /etc/rta && install -d -m 0700 -o 65532 -g 65532 /rta-home && \
    RTA_DATA_DIR=/usr/local/lib/rta rta plugin trust pg --yes && \
    chmod -R a+rX /usr/local/lib/rta && \
    rta profile set app-a-staging --note "app A, staging" --ttl 8h \
      --plugin pg --set database=app-a \
      --kube staging/app-a/svc/postgres:5432 \
      --secret password=kube:postgres-creds/password && \
    rta profile set app-b-prod --note "app B, production" --ttl 1h \
      --plugin pg --set database=app-b \
      --kube prod/app-b/svc/postgres:5432 \
      --secret password=kube:postgres-creds/password

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=setup /usr/local/bin/ /usr/local/bin/
COPY --from=setup /usr/local/lib/rta /usr/local/lib/rta
COPY --from=setup /etc/rta /etc/rta
COPY --from=setup --chown=65532:65532 /rta-home /rta-home
ENV RTA_CONFIG=/etc/rta/config.yaml RTA_DATA_DIR=/rta-home RTA_SYSTEM_DIR=/usr/local/lib/rta PATH=/usr/local/bin
ENTRYPOINT ["/usr/local/bin/rta"]
```

The `setup` stage needs Alpine's shell to run `rta plugin trust`/`rta profile set` at build time — it never ships. `rta plugin trust` writes into whatever `RTA_DATA_DIR` names, so pointing it at `/usr/local/lib/rta` for that one command is what puts the trust record beside the plugin, in the root the run time reads as `RTA_SYSTEM_DIR` and a volume cannot mask. `/rta-home` ships empty and owned by `nonroot`, so a fresh named volume mounted there starts out writable by rta, the same way the published images arrange it. The final stage starts over from the same distroless base the published image uses, for the same reason: `pg` over TLS needs a CA bundle to verify against, same as the primary recipe above. The full image, `ghcr.io/this-is-tobi/rta-full`, is built this way too — every first-party plugin installed from the official index into that same root.

Each member wires their client to `docker run` on that image, exactly as in the recipe above, mounting their own `~/.kube` and their own state volume. What the image carries is a *reference*, never a value:

```yaml
secrets:
  password: kube:postgres-creds/password
```

So the image is safe to publish to your internal registry. The credential is read at call time from the cluster, **with the caller's own kubeconfig and their own RBAC** — the access control your organisation already runs keeps applying, per person, unchanged. The same is true of the `kube:` forward: reaching the database at all requires that member's cluster access.

That is the whole of "without any config", and everything else stays where it belongs. Grants are theirs. The record says what *they* did. `rta use` bounds *their* agents. Nothing is shared that a person has to be accountable for.

Add [`.rta-policy.yaml`](./50-team-policy.md) to the repositories they work in and the team also gets a ceiling — committed, travelling with a clone, needing no seal because it can only ever subtract.

## Why not one server for everyone

Because "a shared server that can reach everything anybody is authorised for" is a single process holding the union of every environment's credentials, reachable by every member's agent. Concretely, it costs you five things:

| What breaks | Why |
| --- | --- |
| Per-person access control | The server authenticates as itself. Your cluster RBAC, database roles and cloud IAM stop distinguishing between eight people and start seeing one service account |
| The record | rta logs the agent name a person typed on their own machine. On a shared process every client is a client of the same process — two members' agents both log as `claude`, and nobody can answer who ran `pg dump` |
| Live consent | `--consent` parks a call and waits for the person at the machine. On a shared server, which person? Whose desktop notification rings, and who is accountable for the answer? |
| `rta use` | It exists to *subtract* — switching to staging takes production away from every agent. Shared, one person switching takes it away from everyone, silently |
| Blast radius | One compromised agent on one laptop reaches the union of every environment, because the union is what the server was configured with |

The transport was the smaller problem, and smaller than it first looked: [Hosting a server](./65-hosting-a-server.md) is `--http` — authenticated over the wire instead of by parent-process trust, so "who is on the other end" has a standard answer. What it does not change is anything in the table above — authenticating five people to one process that holds the union of their environments tells you which of them is calling and leaves every other row exactly as it is.

None of that is an argument against rta running somewhere other than a laptop. Run it in your dev platform, in a Codespace, in a per-user pod — **one instance per person, authenticating as that person**, built from the shared image. That is the same convenience with none of the collapse.

## Next

[Kubernetes](./80-kubernetes.md) — the same idea as a chart: one instance per person, the decisions to make before setting a value, and day two.
