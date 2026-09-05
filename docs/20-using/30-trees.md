# Seeing the shape of things

Four capabilities across rta answer the same question about four different stores: **what is in here, and how is it arranged?** They exist because the alternative is running a listing, reading it, picking a name, and running the listing again one level deeper — which is how somebody who does not already know where things are gives up and asks a colleague.

| Command | What it maps |
| --- | --- |
| `rta fs tree` | a directory on this machine |
| `rta s3 object tree` | a bucket, with objects and bytes per prefix |
| `rta vault kv tree` | a KV mount |
| `rta etcd kv tree` | an etcd keyspace |

All four are **reads that return names, never values**. That is not a coincidence and it is not a coincidence they are all bounded either — both fall out of the same two rules, below.

## Names are a read; contents are not

A tree tells you a secret called `staging-db-password` exists. It does not tell you what it is. That is the same line `vault kv list` and `vault kv get` already draw, and walking a whole mount does not move it: **fewer round trips is not a wider permission.** The blast radius of a list of names is that same list of names.

Which is why these are ungated while the matching `get` is not:

```bash
rta vault kv tree                 # ungated: the shape of the mount
rta vault kv get staging-db-password   # needs a grant naming it
```

What the tree *does* change is your record. Two hundred listings become one line in the ledger, so an operator reading what an agent did sees "mapped the mount" rather than a wall of individual calls — which is the more honest summary of what happened.

## Every one of them is bounded, and says so

A tree nobody can read is not a better answer than no tree. Each of these stops somewhere and tells you where:

```
stopped at 500 paths; narrow it with --path
```

A branch that was not expanded says so in place rather than trailing off:

```
nightly/    412 objects, 8.1 GiB — not expanded, raise --depth
```

The count survives the collapse, which is usually the answer you wanted anyway. **A tree that quietly ended would claim a directory is smaller than it is** — the same failure mode as a listing that silently returns its first two hundred rows.

## `fs tree`

```bash
rta fs tree docs --depth 1
```

```
docs/ /Users/you/project/docs
├── examples/ 1 entries
├── 02-installation.md 3.2 KiB
├── 03-quickstart.md 3.9 KiB
├── 10-cli.md 4.4 KiB
```

Directories first, then by name — the order a person reads a listing in.

`--detail` is the one worth knowing about. It reports what the walk *covered* and everything it left out — depth, per-directory limits, hidden entries, mount points and unreadable directories — gathered in one place instead of scattered through whichever branches they happened in:

```bash
rta fs tree / --detail
```

## `s3 object tree`

```bash
rta s3 object tree --bucket build-artifacts
```

```
build-artifacts    1,284 objects, 41.2 GiB
├── releases/      312 objects, 38.7 GiB
│   ├── v1.2.0/    12 objects, 1.4 GiB
│   └── v1.3.0/    12 objects, 1.5 GiB
├── nightly/       971 objects, 2.4 GiB — not expanded, raise --depth
└── index.json     4.1 KiB
```

Every prefix carries its **recursive object count and total size**, which is the question a flat listing cannot answer: where the space went. `s3 object list` groups on `/` and gives you one level, so finding the four terabytes means retyping `--prefix` a level deeper over and over.

This costs one request however deep the result goes. S3 keys are flat — the folders are a fiction the `/` delimiter creates — so rta reads the prefix once with a recursive listing and builds the levels on this side. The bound is therefore on how much of that stream is read (`--limit`), not on round trips.

```bash
rta s3 object tree --bucket build-artifacts --prefix releases/ --depth 3
```

## `vault kv tree`

```bash
rta vault kv tree
rta vault kv tree --path apps --depth 6
```

Unlike the S3 case this genuinely costs one `LIST` per folder, because that is the only way Vault will answer. So it is bounded in **both** directions — 500 paths and 200 listings — and the second bound is the one that matters, because every request costs somebody else money and lands in an audit device.

A folder your token may not list is **marked and stepped over**, not fatal:

```
apps/
├── web/        3 secrets
└── billing/    permission denied
```

A policy that grants part of a mount is the normal case in a shared Vault, and ending the walk at the first refusal would turn "here is the half you can see" into nothing at all.

## `etcd kv tree`

```bash
rta etcd kv tree /registry --depth 4
```

etcd keys are flat too, and everything treats them as paths anyway — `/registry/pods/default/api-1` is a hierarchy that only the `/` makes visible.

One request, like S3's, and it passes `WithKeysOnly`. That is not decoration: without it etcd sends every *value* over the wire, so a names-only capability would have read the entire keyspace's contents and merely declined to print them. For the same reason there is no size column — etcd carries no length field, and measuring would mean fetching.

That matters more here than elsewhere. A Kubernetes cluster keeps every object it has in etcd, and its Secrets are stored base64-encoded rather than encrypted unless somebody turned encryption at rest on.

## Next

- [Using plugins](../40-plugins/10-plugins.md) — where `s3`, `vault` and `etcd` come from
- [Grants](../30-boundary/30-grants.md) — why the matching `get` needs one and the tree does not
- [The record](../30-boundary/40-audit-trail.md) — what one call looks like versus two hundred
