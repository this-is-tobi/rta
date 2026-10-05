# Other ways to install

The [release archive](./10-installation.md#install-a-release) is the shortest road. These are the others: building from source, running the container image, and deploying the Helm chart. [Verify a download](../95-reference/30-verify-a-download.md) covers checking the image and the chart before you run them.

## From source

You need Go 1.26 or newer. The checkout carries a `mise.toml` naming the exact version the pipeline builds with, so with [mise](https://mise.jdx.dev) installed, `mise trust && mise install` fetches it and nothing else has to be on the machine.

```bash
git clone https://github.com/this-is-tobi/rta.git
cd rta
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

Distroless, non-root, multi-arch (`amd64`/`arm64`), published with every release alongside SLSA provenance, an SBOM and a cosign signature. `latest` tracks the newest release; a release `1.2.3` is also tagged `1.2` and `1`, so you can pin as loosely or as tightly as you want.

[Verify a download](../95-reference/30-verify-a-download.md#the-container-image) has the attestation and signature checks for it.

Two shapes of use:

- **An MCP server** — [In a container, for a hardened server](../30-boundary/67-containers-and-images.md#in-a-container-for-a-hardened-server) has the full `docker run` recipe: read-only root, dropped capabilities, no network by default.
- **A one-shot command**, anywhere `docker run` reaches, including inside a cluster: `kubectl run --rm -it rta-debug --image=ghcr.io/this-is-tobi/rta:latest -- net probe db.internal 5432`.

## Kubernetes

```bash
helm install rta oci://ghcr.io/this-is-tobi/rta/rta-chart \
  --namespace rta --create-namespace \
  --values rta-values.yaml
```

The chart deploys rta as an MCP server that other machines reach — one instance per person, each authenticating as that person. It is published the way the image is: with every release, to the same registry, with SLSA provenance and a cosign signature bound to its digest. It moves on its own version stream, and the rta it deploys is its `appVersion`. Verify it the way you verify the image:

[Verify a download](../95-reference/30-verify-a-download.md#the-helm-chart) has the checks for the chart.

This is the third of the three worlds [What rta actually bounds](../30-boundary/10-the-boundary.md) describes — the agent runs somewhere else, holds no credentials of its own, and reaches your environments only through rta — and unlike the other two it is a deployment rather than a setting. [Kubernetes](../30-boundary/80-kubernetes.md) is that deployment: the decisions to make before setting any value, the posture worth deploying, and what changes on day two. The chart's own README is the values reference.

## Plugins from an index

`rta` is one binary and every plugin is a separate module, so neither `go install` nor a release archive brings them along. An **index** is how one arrives: a git repository of `index/<name>.yaml` manifests, each generated from a plugin binary's own declaration. Attach one, search it, install from it.

```bash
rta plugin index add official
rta plugin search vault
rta plugin install vault
```

**Nothing is attached until you say so.** `official` is the one index rta knows by name — [rta-plugins](https://github.com/this-is-tobi/rta-plugins), where the first-party plugins are built, released and described — and the name is reserved for it, so `rta plugin index add official <elsewhere>` is refused. Any other index is `rta plugin index add <name> <repository>`, and a repository with no `index/` is not one — a source tree keeps a directory per plugin under `plugins/`, an index keeps a manifest per plugin under `index/` — so `rta plugin index add` reads what it cloned and says so rather than attaching something that would answer every search with silence.

Two ways to skip the index entirely. **From source**, `make install` in [rta-plugins](https://github.com/this-is-tobi/rta-plugins) puts `rta-plugin-<name>` beside your `rta` and `make trust` there approves the ones built — a binary on your `$PATH` is not consent, and [Trust](../40-plugins/10-plugins.md#trust) is why. **Already in an image**, `ghcr.io/this-is-tobi/rta-full` carries every first-party plugin already trusted; [Container image](#container-image) has the tradeoff.

[Plugins](../40-plugins/10-plugins.md#indexes) is the whole model.

## Next

[Quick start](./20-quickstart.md) — ten minutes with the binary you just installed.
