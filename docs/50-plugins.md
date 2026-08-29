# Using plugins

A plugin is a program that returns a declaration and serves it over gRPC. rta launches it and renders what it declares on every surface at once — CLI, TUI and MCP. There is no separate registration step, and no way for a plugin to appear on one surface but not another.

```bash
rta plugin list
```

Four plugins ship in this repository as proof the contract works: `pg` (PostgreSQL), `s3` (S3-compatible object storage), `vault` (HashiCorp Vault), `kube` (Kubernetes), `docker`, and `eol` (end-of-life checks).

## Trust

**A binary on your `$PATH` is not consent.**

rta discovers anything named `rta-plugin-*` on your `$PATH`, and it loads a plugin by *running* it. So an unapproved binary would execute before anybody typed a command naming it — including during shell completion. That is why discovery and approval are separate:

```bash
rta plugin trust             # what was found and not run
rta plugin trust pg          # approve this artifact
rta plugin untrust pg        # withdraw approval
```

**Trust attaches to the artifact's content digest, not its name.** Rebuilding or replacing a plugin needs approving again. That is the feature, not friction: a plugin's bytes changing under a name you already approved is precisely the event worth stopping for.

```bash
rta doctor
```

```
plugin pg      ok   ~/.local/bin/rta-plugin-pg (6 capabilities, 685186a7c11a)
plugin trust   ok   8 artifacts approved to run
```

The same digest is what pins a destructive capability at the MCP boundary:

```bash
rta mcp serve --allow-destructive hello.wipe@5dae737f8845
```

So an authorisation attaches to an artifact rather than to a name a replacement would inherit.

## Confinement

Plugins run in a sandbox. On macOS that is `sandbox-exec`; `rta doctor` reports what it actually applied:

```
plugin confinement   ok   sandbox-exec: 2 paths denied read+write (rta's own state),
                          10 denied read (credential locations), 9 directories pinned
                          in place so a rename cannot move either out of its rule
```

Read that row rather than assuming it. It states what is denied on *this* machine, and it is honest about platforms where confinement is weaker.

## Indexes

An index is a git repository of `plugins/<name>.yaml` manifests — claims about plugins, searchable without downloading anything.

```bash
rta plugin index add community https://github.com/someone/rta-plugins
rta plugin index list
rta plugin index update
```

**There is no default index.** The first one you attach is your decision, not one rta made for you. rta shells out to your `git`, so your remotes, proxies and credentials keep working.

```bash
rta plugin search postgres
```

Search answers from the manifests alone — nothing is fetched and nothing is executed. Every row is a **claim**, labelled with the index making it.

## Installing

```bash
rta plugin install community/pg
rta plugin upgrade pg
rta plugin remove pg
```

Install is where claims meet evidence. rta fetches the artifact, hashes it, launches it in the same sandbox any load uses, and **refuses if what it declares is not what the index said** — naming the index that made the claim.

Only then does it land: the managed store, the trust entry, and `rta.lock`, which records what rta *computed* rather than what anybody claimed.

**Installing is the trust decision.** There is no separate `rta plugin trust` afterwards — you approved the artifact by installing it, having seen it verified.

## What a plugin can and cannot do

| | |
| --- | --- |
| Adds capabilities to all three surfaces | ✅ automatic |
| Declares its own safety classes | ✅ — and rta enforces them |
| Names a secret it wants from your store | ❌ never — [you write that mapping](./40-profiles.md#secrets-holds-a-reference-never-a-value) |
| Invents an output format | ❌ — one envelope, so `--output` works everywhere |
| Runs before you approve its digest | ❌ |
| Bypasses grants, path roots or the safety gate | ❌ — all enforced host-side |

A plugin's write capabilities are opened one namespace at a time (`--allow-write pg`), and its destructive ones individually and by digest. Nothing about being a plugin buys extra reach.

## Configuring one

Plugin configuration is a declared input, so it is validated the same way a flag is:

```yaml
plugins:
  pg:
    host: db.internal
    database: app
```

Per environment, use [profiles](./40-profiles.md) instead — the same grammar, one level up, and the thing a grant can name.

## Writing one

```bash
rta plugin new mytool      # scaffolds one that builds, passes its suite, and runs
rta plugin dev             # compile and load it through the identical spawn path
rta plugin dev -- mytool greet world
```

`plugin new` writes a plugin that works as it stands rather than a skeleton with TODOs, so the first run succeeds and every edit after it changes something known-good. `plugin dev` loads it exactly as an installed plugin is loaded — sandbox included — without installing anything.

See [Writing a plugin](./writing-a-plugin.md) for the SDK and the `sdktest` conformance suite.

## Next

- [Profiles](./40-profiles.md) — configuring a plugin per environment
- [Grants](./21-grants.md) — bounding what an agent reaches through one
- [Writing a plugin](./writing-a-plugin.md)
