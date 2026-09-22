# Using plugins

A plugin is a program that returns a declaration and serves it over gRPC. rta launches it and renders what it declares on every surface at once — CLI, TUI and MCP. There is no separate registration step, and no way for a plugin to appear on one surface but not another.

```bash
rta plugin list
```

Twelve first-party plugins live in [rta-plugins](https://github.com/this-is-tobi/rta-plugins). They are the proof the contract works, and each is a separate binary you install only if you want it — none of them is linked into `rta` itself, so the ones you skip cost you nothing.

| Plugin | Service |
| --- | --- |
| [`pg`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/pg) | PostgreSQL |
| [`mysql`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/mysql) | MySQL. It reaches a MariaDB server for the capabilities that connect in-process, but not for `dump`/`restore`, which pass MySQL 8's own flags to a client that has them |
| [`mariadb`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/mariadb) | MariaDB, adding Galera cluster state, replica status, and a `dump`/`restore` pair spelled the way that client spells it |
| [`etcd`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/etcd) | etcd v3: cluster health, members, leases, the keyspace, and a snapshot of the whole backend — the one datastore here whose backup has no restore beside it, because etcd's own API has none |
| [`qdrant`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/qdrant) | Qdrant: collections, their configuration and index health |
| [`redis`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/redis) | Redis: health, memory, persistence, replication, the keyspace and the slow log — over RESP spoken in-package, no client library |
| [`s3`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/s3) | S3-compatible object storage |
| [`vault`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/vault) | HashiCorp Vault |
| [`kube`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/kube) | Kubernetes |
| [`cnpg`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/cnpg) | CloudNativePG: which PostgreSQL clusters exist, what one will tell you about its own health, replication, recovery settings, backups and volumes, and asking for a backup now |
| [`docker`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/docker) | containers and images |
| [`keycloak`](https://github.com/this-is-tobi/rta-plugins/tree/main/plugins/keycloak) | Keycloak: users, clients, roles, flows, sessions and events, and a realm graded against named controls — acting as a service account with the `view-*` roles, never an administrator |

Every one of them draws the same line in the same place: the read tier describes the thing, and anything that returns a value somebody stored is a write. `mysql.schema` tells you a database's shape and `mysql.query` returns its rows; `etcd.kv.list` gives you key names and `etcd.kv.get` gives you what a key holds. That is what makes read worth granting.

## Built in, or a plugin

A capability ships built into `rta` when it needs no credential and no configuration, brings nothing outside the standard library, and reaches either nothing or one fixed public host that no input can redirect — `eol.check` asks endoflife.date, the `audit` plugin asks OSV and endoflife.date, and neither can be pointed anywhere else. It is a plugin the moment any of that stops being true: a client library the people who never use it should not carry, a credential location it has to declare, or a destination the caller chooses, which is the line `http.get` sits behind a grant for. Every plugin in rta-plugins fails at least one of those tests, and that is what put it there; `eol` passed all of them, and that is what brought it here.

## Getting the first-party ones

```bash
rta plugin index add official
rta plugin install pg
```

That is the whole path — [Indexes](#indexes) and [Installing](#installing) below are what happens inside it. Building from source instead is `make install` in [rta-plugins](https://github.com/this-is-tobi/rta-plugins), which puts `rta-plugin-<name>` beside your `rta` and approves nothing — which is the next section.

## Trust

**A binary on your `$PATH` is not consent.**

rta discovers anything named `rta-plugin-*` on your `$PATH`, and it loads a plugin by *running* it. So an unapproved binary would execute before anybody typed a command naming it — including during shell completion. That is why discovery and approval are separate:

```bash
rta plugin trust             # what was found and not run
rta plugin trust pg          # approve this artifact
rta plugin untrust pg        # withdraw approval
rta plugin untrust --all     # withdraw every approval you have recorded
```

There is deliberately no `rta plugin trust --all`. Approving is the decision the whole boundary exists to make somebody take one artifact at a time, and a flag that approved everything discovered on a `$PATH` would approve whatever appeared there this morning. The withdrawing direction has no such problem, which is why `--all` exists on that side alone — the same reason `rta plugin allow` has no bulk form either.

**Trust attaches to the artifact's content digest, not its name.** Rebuilding or replacing a plugin needs approving again. That is the feature, not friction: a plugin's bytes changing under a name you already approved is precisely the event worth stopping for.

```bash
rta doctor
```

```
plugin pg      ok   ~/.local/bin/rta-plugin-pg (9 capabilities, 685186a7c11a)
plugin trust   ok   8 artifacts approved to run
```

The same digest is recorded on every grant issued against that plugin:

```bash
rta grant allow hello.wipe --ttl 5m
rta grant list --detail          # shows the artifact the grant was issued against
```

So an authorisation attaches to an artifact rather than to a name a replacement would inherit — replace the binary and the grant stops covering anything.

## Confinement

Plugins run in a sandbox. On macOS that is `sandbox-exec`; `rta doctor` reports what it actually applied:

```
plugin confinement   ok   sandbox-exec: 2 paths denied read+write (rta's own state),
                          10 denied read (credential locations), 9 directories pinned
                          in place so a rename cannot move either out of its rule;
                          everything else is readable, and so is an installed plugin's
                          own directory under the store — reads only, the one place
                          inside rta's state that is, because a process that cannot
                          read its own directory cannot verify a certificate
```

Read that row rather than assuming it. It states what is denied on *this* machine, and it is honest about platforms where confinement is weaker.

That last clause is the one exception, and it is there because of what macOS does rather than because a plugin was trusted with something: the Security framework initialises from the main executable's location, so a process that cannot read its own directory cannot verify a TLS certificate at all. Without the carve-out no managed plugin could reach an `https://` address, while a copy of the same plugin on `$PATH` could. It is reads only, of that one directory, and only for an artifact rta installed there.

### When a plugin needs one of those locations

Some plugins exist to use a credential location. `kube` and `cnpg` shell out to `kubectl`, and `kubectl` cannot reach any cluster without `~/.kube/config` — so for them the denial is not caution, it is the plugin being unable to do the one thing it is for.

Such a plugin **declares** what it needs. Declaring is asking, never getting:

```bash
rta plugin allow                  # what each plugin asks for, and what it has
rta plugin allow cnpg             # allow everything it declares
rta plugin allow cnpg kubeconfig  # or just that one
rta plugin disallow cnpg          # take it back
```

This is deliberately a second decision, separate from `rta plugin trust`. Letting an artifact run and letting it read your cluster credentials are different questions, and an answer to the first must not stand in for the second. Both attach to the artifact's digest, so **rebuilding a plugin asks again** — for the credential grant exactly as for trust, and for the same reason.

You can only allow what the artifact asked for. A location a plugin never declared is access nobody requested, so there is no way to hand it over.

A plugin whose need is not granted still runs. It fails at the call that wanted the file, which is the honest outcome — and `rta doctor` names it as a decision rather than leaving you to read someone else's "operation not permitted":

```
plugin cnpg   warn   ~/.local/bin/rta-plugin-cnpg (5 capabilities, e1a4fcaacd73)
                     — asks to read kubeconfig and has not been allowed to;
                     calls that need it fail. `rta plugin allow cnpg`
```

It names the granted side too, because a standing permission is the thing worth being able to point at:

```
plugin cnpg   ok   ~/.local/bin/rta-plugin-cnpg (5 capabilities, 24757826bbf5)
                   — allowed to read kubeconfig;
                   `rta plugin disallow cnpg` takes it back
```

## Indexes

An index is a git repository of `index/<name>.yaml` manifests — claims about plugins, searchable without downloading anything.

```bash
rta plugin index add official        # the first-party index, known by name
rta plugin index add community https://github.com/someone/rta-plugins
rta plugin index add frozen https://github.com/someone/rta-plugins --ref 3f2a9c1
rta plugin index list
rta plugin index update
```

An index follows its default branch, and `rta plugin index update` moves it. `--ref` pins one at a commit, a tag or a branch instead: `update` leaves it where it was attached and `index list` shows the ref, so a build that must say which claims it consulted can. A pin is about the claims, not about trust — what an install records is the digest of the bytes it verified, and that binds to nothing a ref could change.

**Nothing is attached until you say so**, and `official` is the one name rta knows: it resolves to [rta-plugins](https://github.com/this-is-tobi/rta-plugins), where the first-party plugins are built and released, and it is reserved for that repository — `rta plugin index add official <elsewhere>` is refused, so the word means the same thing on every machine. Any other index is attached by name and URL. rta shells out to your `git`, so your remotes, proxies and credentials keep working — with two exceptions rta owns: a repository may not be a `<transport>::<argument>` remote helper (`ext::` takes a command line, so such a URL is an execution), and it may not be fetched over `http://` or `git://`, since an index states the checksums every install verifies against.

An index is a directory of manifests and nothing else:

```
my-index/
└── index/
    ├── pg.yaml
    └── kube.yaml
```

Each manifest is generated from the plugin binary rather than written: `rta plugin manifest` reads the artifact's own declaration, so an entry cannot disagree with the plugin it describes. [Publishing a plugin](./20-writing-a-plugin.md#publishing-it) is the whole path — and an index of your own is a repository with that one directory in it, attached by the same command as anybody else's.

**Where a plugin lives decides who trusted it.** rta finds plugins on your `$PATH`, then in the system root, then in its own store. The system root — `RTA_SYSTEM_DIR`, `/usr/local/lib/rta` on Linux unless you say otherwise — is what a container image or a package filled at build time, laid out exactly like the store and read the way `/usr/lib` is read: by everyone, written by nobody afterwards. Its `trusted.json` counts, so a plugin the image installed runs without a `rta plugin trust` from you, and `rta plugin list` says the image is who trusted it. What it cannot do is decide for your machine: `rta plugin allow` is still yours to type, and it is recorded in your own data directory, never in the image's. A copy of the same plugin on your `$PATH` still wins, the way it always has. Set `RTA_SYSTEM_DIR` to an empty value to read no system root at all.

**A repository with no `index/` is not an index**, even when that is where the plugins are: a source tree keeps a directory per plugin under `plugins/`, an index keeps a manifest per plugin under `index/`, each folder its purpose — [rta-plugins](https://github.com/this-is-tobi/rta-plugins) carries both. `rta plugin index add` reads what it cloned before calling the attach a success, and refuses one that carries no manifest — naming what it found instead, a `plugins/` full of directories included. Nothing is left behind when it does, so the name is free for the next attempt.

```bash
rta plugin search postgres
```

Search answers from the manifests alone — nothing is fetched and nothing is executed. Every row is a **claim**, labelled with the index making it.

The safety column says what a plugin can do *and* what it asks to read, because both decide whether you want it. A row reading `all read · none needs a grant · asks for kubeconfig` is a plugin that changes nothing and wants your cluster credentials, and the first half alone would be true and misleading.

## Installing

```bash
rta plugin install community/pg
rta plugin upgrade pg
rta plugin remove pg
```

Install is where claims meet evidence. rta fetches the artifact — over `https`, from an OCI registry, or from a file on this machine — hashes it, launches it in the same sandbox any load uses, and **refuses if what it declares is not what the index said** — naming the index that made the claim.

A registry artifact is pulled **anonymously**: rta sends no credential to any registry, ever. That is not a missing feature to fill in casually — an index is somebody else's repository, so authenticating to whatever host a manifest names would turn a search result into a way to spend your credentials. A private artifact is refused by name instead. Capability by capability, safety class and grant flag, and every credential location the plugin asks for: an index cannot quietly leave out that a plugin wants your kubeconfig.

The install report names what it asks for, at the one moment you have the digest in front of you. Installing does not grant it — `rta plugin allow` is still a separate decision, and this is what makes it an informed one.

Only then does it land: the managed store, the trust entry, and `rta.lock`, which records what rta *computed* rather than what anybody claimed.

**Installing is the trust decision.** There is no separate `rta plugin trust` afterwards — you approved the artifact by installing it, having seen it verified.

```bash
rta plugin outdated
```

Lists what changed without upgrading anything: for each installed plugin, the version recorded at install time against what its index claims now. Cheap like search — nothing is fetched — so it is a hint worth a look, never a verdict. `rta plugin upgrade <name>` is what actually re-verifies against the bytes; a plugin respun under an unchanged version number is invisible to `outdated` for the same reason it would be invisible to a signature.

### Upgrading everything at once

```bash
rta plugin upgrade --all --dry-run     # rehearse it
rta plugin upgrade --all               # sweep every installed plugin
rta plugin upgrade --all --index official
rta plugin outdated --index official
rta plugin remove --all --yes          # uninstall every managed plugin
```

A sweep visits every plugin in `rta.lock`, not the ones `outdated` lists — a respin under an unchanged version number is invisible to a manifest comparison, and skipping it would skip the one event upgrading re-verifies for. `--index` narrows the sweep to plugins installed from one index, so the supply chain you trust most can be upgraded without pulling from the one you trust least.

**A sweep holds back any plugin whose new declaration would hand it more than you last approved** — a capability appearing that is not an ungated read, a safety class rising, a capability that stops needing a grant, a new credential location asked for. Those plugins keep their pin, their bytes are never placed, and the report names the change that stopped each one. Everything else upgrades, one unreachable index does not cost you the rest of the run, and the command exits non-zero if anything was held back or failed.

This is the point of the guard rather than an obstacle to route around: nobody reads a declaration diff scrolling past in a sweep, so the diff stops the sweep instead. Naming the plugin — `rta plugin upgrade pg` — upgrades it once you have read what changed, which is the same command you would have run anyway.

### What an upgrade leaves behind

```bash
rta plugin prune --dry-run             # the stored versions nothing runs
rta plugin prune --yes                 # drop them, and the trust on each
```

Every upgrade keeps the previous artifact in the store, so a rollback is a re-install rather than a re-download — and nothing took the older ones out, so a plugin followed through ten releases held ten copies. `prune` removes every stored version that is neither the one `bin/` points at nor the one `rta.lock` records, withdrawing trust from each the way `remove` does: the approval was for those bytes, and the bytes are going. A plugin whose store names no current version at all is left alone, and its row says so.

## What a plugin can and cannot do

| A plugin… | Can it? |
| --- | --- |
| Adds capabilities to all three surfaces | ✅ automatic |
| Declares its own safety classes | ✅ — and rta enforces them |
| Names a secret it wants from your store | ❌ never — [you write that mapping](../20-using/40-profiles.md#secrets-holds-a-reference-never-a-value) |
| Invents an output format | ❌ — one envelope, so `--output` works everywhere |
| Runs before you approve its digest | ❌ |
| Bypasses grants, path roots or the safety gate | ❌ — all enforced host-side |

A plugin's write and destructive capabilities cost a grant, exactly like a built-in's, and that grant is pinned to the plugin's artifact. Nothing about being a plugin buys extra reach.

## Configuring one

Plugin configuration is a declared input, so it is validated the same way a flag is:

```yaml
plugins:
  pg:
    host: db.internal
    database: app
```

Per environment, use [profiles](../20-using/40-profiles.md) instead — the same grammar, one level up, and the thing a grant can name.

## Writing one

```bash
rta plugin new mytool      # scaffolds one that builds, passes its suite, and runs
rta plugin dev             # compile and load it through the identical spawn path
rta plugin dev -- mytool greet world
```

`plugin new` writes a plugin that works as it stands rather than a skeleton with TODOs, so the first run succeeds and every edit after it changes something known-good. `plugin dev` loads it exactly as an installed plugin is loaded — sandbox included — without installing anything.

See [Writing a plugin](./20-writing-a-plugin.md) for the SDK and the `sdktest` conformance suite.

## Next

- [Profiles](../20-using/40-profiles.md) — configuring a plugin per environment
- [Grants](../30-boundary/30-grants.md) — bounding what an agent reaches through one
- [Writing a plugin](./20-writing-a-plugin.md)
