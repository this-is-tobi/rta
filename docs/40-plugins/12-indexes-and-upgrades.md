# Indexes and upgrades

[Using plugins](./10-plugins.md) covers what a plugin is, how rta decides to run one and what it may touch once it does. This page is how one arrives and stays current: an index you attach, a search over what it claims, an install that checks the claim against the bytes, and an upgrade.

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

Each manifest is generated from the plugin binary rather than written: `rta plugin manifest` reads the artifact's own declaration, so an entry cannot disagree with the plugin it describes. [Publishing a plugin](./24-testing-and-publishing.md#publishing-it) is the whole path — and an index of your own is a repository with that one directory in it, attached by the same command as anybody else's.

**Where a plugin lives decides who trusted it.** rta finds plugins on your `$PATH`, then in the system root, then in its own store. The system root — `RTA_SYSTEM_DIR`, `/usr/local/lib/rta` on Linux unless you say otherwise — is what a container image or a package filled at build time, laid out exactly like the store and read the way `/usr/lib` is read: by everyone, written by nobody afterwards. Its `trusted.json` counts, so a plugin the image installed runs without a `rta plugin trust` from you, and `rta plugin list` says the image is who trusted it. What it cannot do is decide for your machine: `rta plugin allow` is still yours to type, and it is recorded in your own data directory, never in the image's — an `allow` in the image's `trusted.json` is ignored. Nor can you withdraw the image's trust: `rta plugin untrust` takes back your own approval and the grant it carried, and a plugin the system root trusts as well keeps loading — the answer says so, and names the root. So does `rta plugin remove`, which deletes the store's copy of such a plugin and your approval with it, while a copy anywhere else goes on loading. A copy of the same plugin on your `$PATH` still wins, the way it always has. Set `RTA_SYSTEM_DIR` to an empty value to read no system root at all.

**A repository with no `index/` is not an index**, even when that is where the plugins are: a source tree keeps a directory per plugin under `plugins/`, an index keeps a manifest per plugin under `index/`, each folder its purpose — [rta-plugins](https://github.com/this-is-tobi/rta-plugins) carries both. `rta plugin index add` reads what it cloned before calling the attach a success, and refuses one that carries no manifest — naming what it found instead, a `plugins/` full of directories included. Nothing is left behind when it does, so the name is free for the next attempt. The clone is made beside the attached ones and moved in only once it has been read, so an index is attached whole or not at all: one listed is never a clone still under way, and an attach cut short, a forced exit included, leaves nothing attached. `rta plugin index update` reads what it pulled the same way, and an upstream commit that leaves no manifest to read — its `index/` gone, or turned into a link to somewhere else — is an update refused by name rather than reported done. The pull stays, since that is where the upstream now is, and search and install refuse the index until it is one again. An update of every index still pulls the rest, and names each one it refused.

```bash
rta plugin search postgres
```

Search answers from the manifests alone — nothing is fetched and nothing is executed. Every row is a **claim**, labelled with the index making it. `--safety` keeps the plugins claiming a capability of one class, `read`, `write` or `destructive`, so `rta plugin search --safety destructive` is the list to read before installing anything.

The safety column says what a plugin can do *and* what it asks to read, because both decide whether you want it. A row reading `all read · none needs a grant · asks for kubeconfig` is a plugin that changes nothing and wants your cluster credentials, and the first half alone would be true and misleading.

## Installing

```bash
rta plugin install community/pg
rta plugin upgrade pg
rta plugin remove pg
```

Install is where claims meet evidence. rta fetches the artifact — over `https`, from an [OCI](../95-reference/10-glossary.md#acronyms) registry, or from a file on this machine — hashes it, launches it in the same sandbox any load uses, and **refuses if what it declares is not what the index said** — naming the index that made the claim.

A registry artifact is pulled **anonymously**: rta sends no credential to any registry, ever. That is not a missing feature to fill in casually — an index is somebody else's repository, so authenticating to whatever host a manifest names would turn a search result into a way to spend your credentials. A private artifact is refused by name instead. Capability by capability, [safety class](../95-reference/10-glossary.md#terms) and grant flag, and every credential location the plugin asks for: an index cannot quietly leave out that a plugin wants your kubeconfig.

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

**A sweep holds back any plugin whose new declaration would hand it more than you last approved** — a capability appearing that is not an ungated read, a safety class rising, a capability that stops needing a grant, a capability that starts or stops declaring that its answer is a value a masked view withholds, a new credential location asked for. Those plugins keep their pin, their bytes are never placed, and the report names the change that stopped each one. Everything else upgrades, one unreachable index does not cost you the rest of the run, and the command exits non-zero if anything was held back or failed.

This is the point of the guard rather than an obstacle to route around: nobody reads a declaration diff scrolling past in a sweep, so the diff stops the sweep instead. Naming the plugin — `rta plugin upgrade pg` — upgrades it once you have read what changed, which is the same command you would have run anyway.

### What an upgrade leaves behind

```bash
rta plugin prune --dry-run             # the stored versions nothing runs
rta plugin prune --yes                 # drop them, and the trust on each
```

Every upgrade keeps the previous artifact in the store, so a rollback is a re-install rather than a re-download — and nothing took the older ones out, so a plugin followed through ten releases held ten copies. `prune` removes every stored version that is neither the one `bin/` points at nor the one `rta.lock` records, withdrawing trust from each the way `remove` does: the approval was for those bytes, and the bytes are going. A plugin whose store names no current version at all is left alone, and its row says so.

An upgrade also leaves the grants standing on the plugin covering nothing: each is bound to the build it was issued against, and the new build is another artifact. `rta grant list` marks every such grant `(replaced)` and `rta doctor` warns of them; `rta grant allow` issues each again for the build installed now.

## Related

- [Using plugins](./10-plugins.md) — trust and confinement
- [Testing, conventions and publishing](./24-testing-and-publishing.md#publishing-it) — building an index of your own

## Next

[The plugin inventory](./15-the-plugin-inventory.md) — the same state seen from inside the TUI.
