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

**Nothing is attached until you say so**, and saying so is the command above or a yes to the question `rta plugin install pg` asks at a terminal when nothing is attached (`Attach the first-party index https://github.com/this-is-tobi/rta-plugins? [y/N]`, answered by `--yes` there; without a terminal it refuses with `plugin.index.none`). `official` is the one name rta knows: it resolves to [rta-plugins](https://github.com/this-is-tobi/rta-plugins), where the first-party plugins are built and released, and it is reserved for that repository — `rta plugin index add official <elsewhere>` is refused, so the word means the same thing on every machine. Any other index is attached by name and URL. rta shells out to your `git`, so your remotes, proxies and credentials keep working — with two exceptions rta owns: a repository may not be a `<transport>::<argument>` remote helper (`ext::` takes a command line, so such a URL is an execution), and it may not be fetched over `http://` or `git://`, since an index states the checksums every install verifies against.

An index is a directory of manifests and nothing else:

```
my-index/
└── index/
    ├── pg.yaml
    └── kube.yaml
```

Each manifest is generated from the plugin binary rather than written: `rta plugin manifest` reads the artifact's own declaration, so an entry cannot disagree with the plugin it describes. [Publishing a plugin](./24-testing-and-publishing.md#publishing-it) is the whole path — and an index of your own is a repository with that one directory in it, attached by the same command as anybody else's.

**Where a plugin lives decides who trusted it.** rta finds plugins on your `$PATH`, then in the system root, then in its own store. The system root — `RTA_SYSTEM_DIR`, `/usr/local/lib/rta` on Linux unless you say otherwise — is what a container image or a package filled at build time, laid out exactly like the store and read the way `/usr/lib` is read: by everyone, written by nobody afterwards. Its `trusted.json` counts, so a plugin the image installed runs without a `rta plugin trust` from you, and `rta plugin list` says it is `in the system root`, which `rta plugin rm` and `upgrade` do not reach. What it cannot do is decide for your machine: `rta plugin access add` is still yours to type, and it is recorded in your own data directory, never in the image's — an `allow` in the image's `trusted.json` is ignored. Nor can you withdraw the image's trust: `rta plugin untrust` takes back your own approval and the grant it carried, and a plugin the system root trusts as well keeps loading — the answer says so, and names the root. So does `rta plugin rm`, which deletes the store's copy of such a plugin and your approval with it, while a copy anywhere else goes on loading. A copy of the same plugin on your `$PATH` still wins, the way it always has. Set `RTA_SYSTEM_DIR` to an empty value to read no system root at all.

**A repository with no `index/` is not an index**, even when that is where the plugins are: a source tree keeps a directory per plugin under `plugins/`, an index keeps a manifest per plugin under `index/`, each folder its purpose — [rta-plugins](https://github.com/this-is-tobi/rta-plugins) carries both. `rta plugin index add` reads what it cloned before calling the attach a success, and refuses one that carries no manifest — naming what it found instead, a `plugins/` full of directories included. Nothing is left behind when it does, so the name is free for the next attempt. The clone is made beside the attached ones and moved in only once it has been read, so an index is attached whole or not at all: one listed is never a clone still under way, and an attach cut short, a forced exit included, leaves nothing attached. `rta plugin index update` reads what it pulled the same way, and an upstream commit that leaves no manifest to read — its `index/` gone, or turned into a link to somewhere else — is an update refused by name rather than reported done. The pull stays, since that is where the upstream now is, and search and install refuse the index until it is one again. An update of every index still pulls the rest, and names each one it refused.

```bash
rta plugin search postgres
```

Search answers from the manifests alone — nothing is fetched and nothing is executed. Every row is a **claim**, labelled with the index making it. `--safety` keeps the plugins claiming a capability of one class, `read`, `write` or `destructive`, so `rta plugin search --safety destructive` is the list to read before installing anything.

The safety column says what a plugin can do *and* what it asks to read, because both decide whether you want it. A row reading `all read · none needs a grant until a profile names one · asks for kubeconfig` is a plugin that changes nothing and wants your cluster credentials, and the first half alone would be true and misleading. "None needs a grant" is about a call that names no connection: a read costs a grant the moment it names a [profile](../20-using/40-profiles.md), which is how a plugin that talks to a service is nearly always used, so the row says until one does. The same clause ends `declares` on the install prompt and `reachable by default` on `rta grant list --detail` when none of them needs a grant and a capability in them takes a profile; a count above none is true as it stands, and the reads beside it are the profile's the same way. The count in a search row is of what the manifest claims, every capability that is not a read, because a manifest cannot say which ones refuse an agent outright. `declares` counts what an agent can be granted and says how many more are for a person at a terminal, so `5 need a grant` in the row is `3 need a grant · 2 are for a person at a terminal` once the bytes are in hand and the two lines agree. The receipt of `rta grant allow pg --profile staging` counts a third thing, every capability the grant covers, reads included: a call that names a profile needs a grant whatever its class.

### What a manifest may say

A manifest holds the keys rta reads and nothing else. A key rta does not read **refuses the manifest**, and the refusal names the key, so an author who writes `signature:` hears that nothing checks it instead of believing something does. A misspelling is heard the same way. The prose a manifest shows, which is its summaries, its homepage and the values of its annotations, is refused rather than cleaned when it holds a control or invisible character, so a claim displays as what it is.

Two keys are about the file and not about the plugin.

**`apiVersion`** says which version of this format the manifest is written in, and comes first in the file. A manifest that states none is read as `rta/v1`, and always will be, because every manifest an index already holds lacks the key. Stating `rta/v1` is accepted too. **`rta/v2`** adds no key: it says the plugin's settings need an rta that reads a key written flat or nested and refuses a section stating one the plugin does not read. An older rta ran such a plugin with a key it no longer reads silently ignored, so a plugin that renamed its [TLS](../95-reference/10-glossary.md#acronyms) mode ran at its default mode. `rta plugin manifest` states `rta/v2` for a plugin that renamed a key (`Renamed`) or declares a key beside another that starts with it (`database` and `database.list.limit`), and no `apiVersion` for any other, because a manifest that states one is refused by every release that does not know it.

A manifest that states a newer version, `rta/v3` say, is refused for that and nothing else, whatever else in it is new. The code is `plugin.index.apiversion`, and the message stands on its own: `apiVersion "rta/v3" is newer than rta/v2, which this rta (0.61.0) reads — upgrade rta`. Search lists the rest of the index and puts that line beside the rows. Install refuses with the same sentence, where it would otherwise say that no index carries the plugin. `rta plugin index update` names each manifest it cannot read once the pull is done. Any other value, such as `rta/v0`, `v1` or `rta.v1`, is a slip in the manifest and is refused as a malformed one.

The version is about reading the file and never about trust. What an install records and pins is the digest of the bytes rta hashed itself, whatever a manifest is stamped with.

**`annotations`** is the one place a key rta does not read may go. It is a map of strings, with at most 16 entries:

```yaml
apiVersion: rta/v1
name: mytool
version: 1.2.0
summary: What mytool does, in one line
homepage: https://github.com/you/rta-plugin-mytool
platforms:
  - os: linux
    arch: amd64
    url: https://github.com/you/rta-plugin-mytool/releases/download/v1.2.0/mytool_linux_amd64
    sha256: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
capabilities:
  - id: mytool.status
    safety: read
annotations:
  license: Apache-2.0
  example.com/owner: platform-team
  release: "1.10"
```

A key is `name` or `prefix/name`, 63 characters at most in all, in lowercase letters, digits, `-`, `_` and `.`, and each part begins and ends with a letter or digit. A value is one line of at most 120 characters. An annotation outside those bounds refuses the manifest and names the annotation. Quote the values: YAML reads an unquoted `1.10` as a number, and it arrives as `1.1`.

**Annotations mean nothing to rta.** Nothing reads one, compares it with the binary, records it or shows it. A `signature`, `trusted` or `verified` annotation is not a claim, and nothing hangs from it, so an index that wants a signature checked gets none from this.

**What is a field and what is an annotation.** Anything a reader must understand to decide safely is a field of a new `apiVersion`, never an annotation: a claim that is compared, a restriction, a changed meaning, a removal. A reader refuses a manifest it cannot read in full instead of skipping what it does not understand. An annotation is only ever something a reader loses nothing by missing. An rta from before these keys existed refuses a manifest stating either one, as an unknown key.

## Installing

```bash
rta plugin install community/pg
rta plugin upgrade pg
rta plugin rm pg
```

Install is where claims meet evidence. rta fetches the artifact — over `https`, from an [OCI](../95-reference/10-glossary.md#acronyms) registry, or from a file on this machine — hashes it, launches it in the same sandbox any load uses, and **refuses if what it declares is not what the index said** — naming the index that made the claim. What is compared is every capability the binary declares against every one the index lists, in both directions, with each one's [safety class](../95-reference/10-glossary.md#terms) and whether it needs a grant, and every credential location the plugin asks to read: an index cannot quietly leave out that a plugin wants your kubeconfig.

A registry artifact is pulled **anonymously**: rta sends no credential to any registry, ever. That is not a missing feature to fill in casually — an index is somebody else's repository, so authenticating to whatever host a manifest names would turn a search result into a way to spend your credentials. A private artifact is refused by name instead.

The install report shows what the plugin declares — a `declares` row listing its capabilities, with how many are reads, writes and destructive and how many need a grant — and, for a plugin that wants one, an `asks to read` row naming each credential location. You see both at the one moment you have the digest in front of you. Installing does not grant what it asks to read — `rta plugin access add` is still a separate decision, and this is what makes it an informed one.

Only then does it land: the managed store, the trust entry, and `rta.lock`, which records what rta *computed* rather than what anybody claimed. `rta.lock` is plain JSON beside the store, and every command that acts on what it records — `install`, `upgrade`, `remove`, `prune` and `index remove` — refuses one that does not parse (`plugin.lock.unreadable`) before it changes anything: each decides from the record, which one that does not parse cannot answer, and writing it back from what could be read would drop every other plugin's entry. `rta doctor` says so too. Fix the file, or remove it and install each plugin again.

**Installing is the trust decision.** There is no separate `rta plugin trust` afterwards — you approved the artifact by installing it, having seen it verified. The report ends on a `trust` row that says so, with the digest approved and `rta plugin untrust <name>` to take it back; under `--dry-run` the row says what installing would approve, and the row `to install it` says to run the command again without the flag.

```bash
rta plugin outdated
```

Lists what changed without upgrading anything: for each installed plugin, the version recorded at install time against what its index claims now, with `(older)` beside a claim below the installed version. Cheap like search — nothing is fetched — so it is a hint worth a look, never a verdict. `rta plugin upgrade <name>` is what actually re-verifies against the bytes; a plugin respun under an unchanged version number is invisible to `outdated` for the same reason it would be invisible to a signature.

### When an index moves a plugin back

```bash
rta plugin upgrade pg            # at a terminal: says both versions and asks
rta plugin upgrade pg --dry-run  # what moving back would change, without asking
rta plugin upgrade pg --yes      # moves it back, for a script
```

An index whose claim is now older than the version installed — a maintainer's revert, or an index pointed back at an earlier release — is not followed unasked. `rta plugin upgrade <name>` stops before fetching anything, says which two versions, and asks `Move pg back to 1.3.2? [y/N]`; `--yes` answers, and with nobody to ask it exits 3 (`core.confirm.required`) with nothing changed. The yes is to the version shown: an index that moves again before the move is refused. A move taken is reported as `downgraded`. A sweep never moves a plugin back, `--yes` or not: it holds it, as it holds one that would gain authority.

The versions are the index's claims, read by their first three numbers — `1.4.0` and `v1.4.0` are the same, `1.10.0` is newer than `1.9.9`. Anything after them, a pre-release or `git describe`'s `-3-gabc1234`, is shown and never ordered, and neither is a version that does not begin with three numbers: those move as before. This catches an index rolled back honestly. One that lies about a version is not stopped by it, and nothing is trusted because of a version — what you approve is still the digest.

### Upgrading everything at once

```bash
rta plugin upgrade --all --dry-run     # rehearse it
rta plugin upgrade --all               # sweep every installed plugin
rta plugin upgrade --all --index official
rta plugin outdated --index official
rta plugin rm --all --yes          # uninstall every managed plugin
```

A sweep visits every plugin in `rta.lock`, not the ones `outdated` lists — a respin under an unchanged version number is invisible to a manifest comparison, and skipping it would skip the one event upgrading re-verifies for. `--index` narrows the sweep to plugins installed from one index, so the supply chain you trust most can be upgraded without pulling from the one you trust least.

**A sweep holds back any plugin whose new declaration would hand it more than you last approved** — a capability appearing that is not an ungated read, a safety class rising, a capability that stops needing a grant (dropping the flag, or falling from a class above read to a read without it), a capability offered to agents that was the person's alone (`HumanOnly` dropped), a gated capability offered to remote callers that was this machine's alone (`HostSpecific` dropped), a capability that starts or stops declaring that its answer is a value a masked view withholds, a new credential location asked for. Those plugins keep their pin, their bytes are never placed, and the report names the change that stopped each one. So does a plugin whose index now claims an older version than the one installed, held before anything is fetched. Everything else upgrades, one unreachable index does not cost you the rest of the run, and the command exits non-zero if anything was held back or failed.

This is the point of the guard rather than an obstacle to route around: nobody reads a declaration diff scrolling past in a sweep, so the diff stops the sweep instead. Naming the plugin — `rta plugin upgrade pg` — upgrades it once you have read what changed, which is the same command you would have run anyway.

### What an upgrade leaves behind

```bash
rta plugin prune --dry-run             # the stored versions nothing runs
rta plugin prune --yes                 # drop them, and the trust on each
```

Every upgrade keeps the previous artifact in the store with your approval of it, so an index that moves back to it lands on bytes already approved, fetched again — and nothing took the older ones out, so a plugin followed through ten releases held ten copies. `prune` removes every stored version that is neither the one `bin/` points at nor the one `rta.lock` records, withdrawing trust from each the way `remove` does: the approval was for those bytes, and the bytes are going. A plugin whose store names no current version at all is left alone, and its row says so.

An upgrade also leaves the grants standing on the plugin covering nothing: each is bound to the build it was issued against, and the new build is another artifact. `rta grant list` marks every such grant `(replaced)` and `rta doctor` warns of them; `rta grant allow` issues each again for the build installed now.

It leaves behind what you let the old build read, too. `rta plugin access add` records a credential location, or a path named for `confine: strict`, against the build's digest, so the new build starts with none of it, and a call that needs the file fails until you give it again: `rta plugin access add kube`, the decision you made for the build it replaced, made for these bytes. An upgrade never makes it for you. Installing the new build is the decision to run it and no more, for the reason [Using plugins](./10-plugins.md#when-a-plugin-needs-one-of-those-locations) gives, and a sweep holds back a build that asks for a location the old one did not.

And it leaves your configuration pinned to the old build. A `plugins:` section whose heading names the old digest no longer applies, and rta refuses every call of the plugin until it names the build installed, as `core.config.unapplied`, rather than run them without it: without it a call goes to the plugin's own default destination, with whatever credentials you exported for the one the section named. A profile entry naming the old digest is refused the same way. Each has its command:

```bash
rta config repin --plugin pg                 # the plugins: section, every key kept, in the file it is in
rta profile repin --all --plugin pg          # every profile entry
```

A key the new build does not read, one it renamed or dropped, stops the plugin as any key nothing reads does, so the receipt below names each one your sections and profiles state, with what the build reads in its place, and both repins name them again after the move: `plugins.pg@1a2b3c4d5e6f states sslmode, which the installed pg does not read; it reads tls in its place`. A move back is told the older spelling of a key the newer build introduced. The repin is written all the same, since the section pinned to the old build stopped the plugin as surely.

A section written with no pin, or one too short to trust, is refused and pinned the same way. Two sections for the one plugin, the old pin left beside the new, are refused by `config repin` rather than folded together, since which one's keys you keep is yours to say. A config file read from the working directory is not honoured at all, and a call runs without its sections rather than refuse on the word of a file you did not name.

**The receipt names all of it.** `rta plugin upgrade pg` ends on a row for each thing the upgrade left on the build it replaced — `your profiles`, `your pin`, `your access`, `your paths` and `your grants` — with the command that moves it, and a sweep says the same in a `Left behind` section under its table, one line for each thing a plugin left. The access line names the locations to give back whenever the new build declares more than the old one was given, so the command it hands you never gives a plugin something you did not give it before. Nothing on it moves by itself. `rta doctor` goes on naming each one until it is done, except a named path, which only the receipt says: doctor reads what the build installed now was given, and the path was given to the one it replaced.

### In one script

On a machine with many plugins and profiles, the receipt's commands can run as one script:

> [!CAUTION]
> **This makes, unread, every decision an upgrade leaves to you.** Each profile's credentials and each `plugins:` section go to the new builds, and each plugin you name reads what you let its old build read — decisions about bytes nobody has looked at, which rta leaves behind on purpose. A sweep holds back a build that declares more than you approved; nothing holds back one that does something worse with the same declaration, and reading the receipt is how you would have caught it. Run this only against an index whose releases you would install unread anyway, and give access by name, as it does, never in a loop.

```bash
rta plugin index update
rta plugin upgrade --all
# plugin list's last column says where each plugin came from
for p in $(rta plugin list -o json | jq -r '.rows[] | select(.[-1] == "installed by rta") | .[0]'); do
  rta profile repin --all --plugin "$p"
  rta config repin --plugin "$p"
done
rta plugin access add kube
rta plugin access add cnpg
rta doctor
```

Both repins answer that there is nothing to do for a plugin no profile or section names, and leave alone a plugin the sweep held back, whose entries still name the build it runs. A path you named for `confine: strict` is not in it: the receipt's `your paths` line has the command for each. Grants are left out on purpose: issuing one is consent given to an agent, which a script run with nobody at the terminal must not give, and `rta doctor` names them at the end with whatever else is left.

## Related

- [Using plugins](./10-plugins.md) — trust and confinement
- [Testing, conventions and publishing](./24-testing-and-publishing.md#publishing-it) — building an index of your own

## Next

[The plugin inventory](./15-the-plugin-inventory.md) — the same state seen from inside the TUI.
