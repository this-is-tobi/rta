# Secrets (`kv`)

An encrypted local store for the things you keep re-pasting: database passwords, API tokens, certificates, private key files.

It is `age`-backed, it lives in rta's data directory beside your grants (`kv status` prints the path), and it never writes a value to a log, an argv or the terminal unless you ask it to. The one thing that sits beside your config instead is the dedicated key `kv init --generate` makes.

```bash
rta kv set db-password
rta kv get db-password
rta kv list
```

## Choosing the lock, once

A passphrase store needs no setup — that is what you get by default, and the first `rta kv set` makes it. With no store yet it says what a passphrase commits you to: you type it every time, it cannot be recovered, and an agent's server can open the store only with `RTA_KV_PASSPHRASE` set in its environment. It asks for the passphrase twice, then for the value, and a new passphrase needs at least eight characters, counted as characters and not bytes; a store locked under a shorter one before that rule keeps opening. On a store that exists it asks for the passphrase first, so a wrong one costs nothing you had typed. Everything else is a one-time decision:

```bash
rta kv init --generate
```

`--generate` makes a **dedicated age key for this store** and locks it to that. After it, nothing needs a flag and nothing needs typing: no passphrase, no `--identity`. And unlike your SSH login key, losing it costs you this store and nothing else. It is kept beside the config rather than beside the store so that a copy of the data directory is ciphertext and nothing else, which makes it the other half of a backup: on a machine that has the store and not the key, `kv list` answers `kv.identity.required`, and the hint names the file it looks for.

```bash
rta kv init --identity ~/.ssh/id_ed25519     # a key you already have
rta kv init --recipient age1abc...            # let somebody else read it too
```

`--identity` accepts age or SSH keys. A passphrase-protected SSH key is asked for its passphrase the way `ssh` asks — ssh-agent cannot help here, because an agent signs and never decrypts.

`RTA_KV_PASSPHRASE` and `RTA_KV_IDENTITY` supply either without a flag, which keeps them out of shell history.

In [the TUI](./20-tui.md), a passphrase typed into an unlock form is kept in that TUI's memory for fifteen minutes past its last use, so a sitting costs one unlock rather than one per action. While the store is unlocked there, the key boxes of the `kv` forms complete from its keys; completing only looks at the unlock, so it does not extend it. That is the only place an unlock outlives the command that made it: the CLI asks once per command, and a server unlocks from nothing but what its environment holds.

## Storing things

```bash
rta kv set api-token                          # asks for it, and echoes nothing
rta kv set tls-cert --file server.pem         # in no argv and no shell history
rta kv set db-password --description "staging replica"
```

At a terminal, `kv set` given no value asks for it without echoing what you type, so the secret reaches neither your shell history nor `ps` — where one typed after the key reaches both. A value typed after the key at a terminal is still stored, and a line on stderr says that it is in both places and that `rta kv set <key>` asks for it instead. `--description` or `--kind` alone relabel an entry the store holds and ask for nothing; for a key it does not hold yet, they label the entry the typed value makes. The prompt reads one line, of any length. A value that spans lines, or one already in a file, comes from `--file`, and one another command prints from `--file /dev/stdin`, as [the CLI](./10-cli.md) shows. Pasted at the prompt, a value that spans lines is refused as `kv.set.multiline` and nothing is stored: the prompt reads on, echo still off, while the paste is still arriving — until the terminal has been quiet for 200 ms — and drops what came after the first line, so none of it reaches your shell and its history. For a paste slower than that, the rest goes on to your shell; a first line that only opens a value — a PEM block's, or the lone `{` of a JSON credential — is still refused as `kv.set.multiline` rather than stored as though it were the whole. In [the TUI](./20-tui.md) the `kv set` form masks it as you type. Nothing is asked where nobody is at a terminal: a script whose standard input is not one, and an agent, are refused with `kv.set.novalue` and give the value with `--file` or the `value` argument. An empty answer is no value, and is refused the same way.

rta detects what kind of thing it is — string, JSON, certificate, private key, SSH key, file — and `kv list` shows the kind and description without ever showing a value — and, once a value has been replaced, how many earlier ones `kv history` still keeps. `--kind` overrides the detection. A value piped in through `--file /dev/stdin` is labelled by what it holds, as a typed one is — a password is a string, not a file called `stdin`, and bytes that are not text are a file — and `kv list --detail` gives its source as `piped`, where one read from a file on disk gives `file:` and the file's name.

## Getting them back

```bash
rta kv get db-password                # to stdout
rta kv get tls-cert --out server.pem  # to a file, at 0600
rta kv copy db-password               # to the clipboard, never displayed
rta kv edit config-json               # opens $EDITOR, re-encrypts on save
```

And for a whole shell environment:

```bash
eval "$(rta kv env --prefix APP_)"
rta kv env --format dotenv > .env
```

A key's name becomes the variable's by upper-casing it and turning anything but a letter, a digit or `_` into `_`, so `db-password` is `DB_PASSWORD`. Two keys that come out the same — `prod/db-password` and `prod-db-password` — are refused rather than both printed, since whichever line a shell or a `.env` loader kept would be a secret under the other's name: rename one, or export them in separate calls with different prefixes.

## Undoing a mistake

```bash
rta kv history db-password                # what it held before, and when each value was replaced
rta kv restore db-password --revision 1   # put an earlier value back
rta kv rm old-token --yes                 # set aside, not destroyed
rta kv restore old-token                  # and back, history included
rta kv rm old-token --purge --yes         # the one removal that is final
```

Every write over an existing key keeps what it replaced — the last five values, inside the same encrypted store — and `kv rm` keeps the whole entry aside rather than destroying it. A paste over the wrong key, a rotation that broke something, a mis-click on the wrong row: each is undone by name. A restore pushes the value it replaces into the history in turn, so a restore is undoable by the same command. `--purge` is the one removal that is final, and it also finishes off a key removed earlier.

## Seeing what is in the store

```bash
rta kv overview             # what is in it, how it is locked, and whether this shell can open it
rta kv overview --detail    # and the folders its names share, and who can open it
rta kv status               # where it is and what can open it, without decrypting it
rta kv list
rta kv list db/             # only the keys in one folder
rta kv show db-password     # everything about it except the value
rta kv tree                 # the store by the folders its names share
rta kv recipients           # which public keys can decrypt this store
```

A folder is what keys named with slashes share: `db/password` and `db/user` are in `db/`. It is never a key itself, so `kv get db` is refused as `kv.notfound`, and the hint says `db/` is a folder and names the keys in it.

`kv overview` is the one page about the store: how many keys it holds, in how many folders, how many of each kind, how many bytes of values, when the newest and the oldest were set, and what it keeps beside the live keys — earlier values, and removed keys that `kv restore` brings back — and then whether it is locked with a passphrase or to keys and how many, whether this shell can open it, where the file is, how big, and when it last changed. The count comes first because the dashboard cuts a tile from the bottom, and on the tile how the store is locked comes straight after it, with the kinds, the ages and what is kept after that, and what is kept said in fewer words: `3 earlier values, 2 removed`. Names, counts and sizes, never a value or a piece of one, which is the line `kv list` draws. The folder count counts every folder at every depth, so `prod/db/password` is in `prod/` and in `prod/db/`, and when some are nested the line says how many are at the top. `--detail` adds a table of the eight biggest top-level folders with their key counts and sizes, with the keys that are in no folder as its last row, and a line under it saying how many top-level folders there are and that `kv tree` draws every one; for a store locked to keys it also says which public keys can open it. A folder is named by whoever wrote a key in it, so one whose name holds a newline, an escape sequence, a right-to-left override or any character you could not see is shown in quotes with that character written out, the way `fs tree` shows a file: it cannot add a row of its own or read as another folder, and a name that reads as itself is shown as it is.

For a program the sentences are not the answer: `rta kv overview -o json` (and `-o yaml`, and `kv_overview` over [MCP](../95-reference/10-glossary.md#acronyms)) carries the numbers they were made from under `numbers`, so `rta kv overview -o json | jq .numbers.keys` is the whole of it. The names are `keys`, `folders` (every depth) and `top_level_folders`, `value_bytes`, `earlier_values`, `removed_keys`, one `kind_<kind>` for each kind held (`kind_private_key`), `newest_set_unix` and `oldest_set_unix` as seconds since the epoch, `file_bytes`, `changed_unix`, and `readers` for a store locked to keys. The file's own are there for a store the page could not open, and the inventory's only when it was opened: a script tells the two apart by the name being there. The counts need the store open, and `kv overview` never asks for what opens it. With no key at hand it still says where the store is and how it is locked, then says in one line that the inventory needs a key and what supplies one — `RTA_KV_PASSPHRASE` or `--passphrase` for a passphrase store, `--identity` or `RTA_KV_IDENTITY` for a store locked to keys — and ends there; `kv list` is the command that asks. That is what lets an agent, or anything else that runs it where nobody is there to answer, call it safely. The `unlocks here` line tells the same story from the other side: at a terminal it says `on request` and that `kv list` is the one that asks, and when a passphrase was at hand and did not open the store it says that the passphrase was not accepted, where `kv status`, which never opens the store, can only say that one is at hand. Both name where a passphrase came from — `from the environment` is `RTA_KV_PASSPHRASE`, and one given as `--passphrase` or typed in the unlock form is `given`.

It is also the secrets tile on [the dashboard](./20-tui.md), which re-runs it every thirty seconds rather than every few: opening a passphrase store costs a third of a second of key derivation and a quarter of a gigabyte of memory, and a store changes when somebody changes it. The tile counts the store when the environment can open it, from `RTA_KV_PASSPHRASE` or a key found for it. The passphrase of a store you unlocked in the TUI is not used by it, since a tile on a timer that used it would keep that unlock from ever lapsing; the tile looks at the session instead and says `unlocked in this session`, as the header's `store unlocked` badge does, and `enter` on it is the use, which the page that follows answers with the counts. With nothing unlocked the tile says what it needs, and that `enter` opens the page and `e` there the unlock form, because the tile has no passphrase box of its own. `s` lists the secrets from the tile and `a` adds one.

`kv status` answers "where is the store and what can open it" **without unlocking it**, which makes it safe to run anywhere — including in a script that is checking whether a machine is set up.

## Changing the lock afterwards

```bash
rta kv rekey --recipient age1colleague...            # add a reader
rta kv rekey --generate                              # move to a dedicated key
rta kv rekey --only --recipient age1me...            # drop every other reader
```

**Moving a passphrase store to a generated key is a trade, and both pages that offer it say so.** The passphrase stops opening the store, and the key file, which needs no passphrase, opens it for anything running as you; `kv recipients` says it beside the offer, and the preview and the result of `rekey --generate` say it again before and after.

`rekey` always refuses to lock you out. That is not a courtesy — it is the difference between a key-rotation command you can use and one you use once.

## Backing up, and restoring

The store is a few files, and which of them matter depends on how it is locked. `rta kv status` prints where the store file is and what can open it, without opening it.

| Locked with | The files | A copy of them needs |
| --- | --- | --- |
| A passphrase, the default | `kv.age`, in the data directory | The passphrase and nothing else: it opens the file on any machine |
| `kv init --generate` | `kv.age` and `kv.recipients` in the data directory, and the key `kv.identity` in the config directory, beside `config.yaml` | All three: the first two are ciphertext and public keys, and the key is what opens them |
| `--identity` or `--recipient` | `kv.age` and `kv.recipients` in the data directory | The private key of one of the recipients, which for an SSH key is the file you already have |

The key is kept out of the data directory on purpose, so that a copy of the data directory — a backup, a synced folder, a container volume — holds ciphertext and nothing that opens it. It also makes those two files not enough to restore from: on a machine given only them, `rta kv status` says `unlocks here  no identity given (--identity, or set RTA_KV_IDENTITY)` and `rta kv list` is refused as `kv.identity.required`. A store made with `--generate` finds its key as `kv.identity` beside the config file, so that is the file a restore is missing. Back the key up separately, to somewhere that is not beside the store: losing it loses every secret in the store, and nobody can help you. rta makes it owner-only (`0600`), and a copy of it should be too.

Everything the store holds is in `kv.age`: the live keys, the last five earlier values of each, and the keys removed and not yet purged. A restored copy has them all, and `kv restore` brings back an earlier value or a removed key from it as it did before. Copy `kv.age` and `kv.recipients` together, from the same moment: an older `kv.age` beside a newer `kv.recipients` (a `kv rekey` in between) still reads, and its next write is refused as `kv.recipients.mismatch`.

To restore, put `kv.age` and `kv.recipients` in the directory of the store file `rta kv status` prints, and the key in the directory of the config file `rta config path` prints, or keep the key anywhere and name it with `--identity` or `RTA_KV_IDENTITY`. Then check, first without unlocking anything and then with the key or the passphrase:

```bash
rta kv status    # where it is, how it is locked, and whether this machine can open it
rta kv list      # opens it, and lists the keys
```

There is no verb that exports or imports a store whole, and `rta kv restore` is not one: it brings back a removed key, or an earlier value of one, in a store that is there ([Undoing a mistake](#undoing-a-mistake)). The files are the backup. A second reader is the other safeguard for a dedicated key: `rta kv rekey --recipient <a public key you keep elsewhere> --yes` adds one, and either key then opens the store ([Changing the lock afterwards](#changing-the-lock-afterwards)).

## What this means for agents

This is the part to read before connecting an [MCP](../95-reference/10-glossary.md#acronyms) client.

**`kv.get` is classified as a write**, even though it only reads the store — revealing a secret is the sensitive act, not the lookup. An MCP agent needs a grant naming `kv.get` — and ideally the one key it should read — before the call goes anywhere, and on top of that the store still has to open, which is a separate question from calling the capability.

```bash
rta doctor
```

```
kv store   info   unlocks from this environment — an MCP server started here
                  can read secrets, bounded only by grants
```

A server inherits the environment it was started from. If you locked the store with `--generate` or an identity that is available in your shell, then a server started from that shell **can** open it. That line is the whole warning: it is not a misconfiguration and not a bug, it is what "no passphrase to type" means, and it is worth knowing before you connect a client. What bounds it is grants:

```bash
rta grant allow kv.get deploy-token --agent claude --ttl 15m --max-uses 1
```

Without a grant naming it, an agent's `kv.get` is refused and the refusal is written down.

**An agent that only has to use a service needs no `kv.get` at all.** A [profile](./40-profiles.md#secrets-holds-a-reference-never-a-value) maps a credential to a stored entry (`--secret password=kv:staging-db-password`), rta reads it on this machine when the call runs, and an agent granted `pg.query --profile staging` queries the database and never receives the password. A `kv.get` grant on that entry would hand it exactly what the profile keeps from it, so grant `kv.get` for a secret the agent has to hold itself, such as a deploy token it passes to another tool.

What needs no grant is the shape of the store. `kv.list`, `kv.tree` and `kv.overview` answer an agent with names, kinds, sizes and counts — never a value or a piece of one — whenever the environment its server was started in can open the store. That is the line `kv.list` has always drawn, and `kv.overview` stays on it: it adds how many keys and folders there are, how old the values are, and how many removed keys and earlier values the store keeps, and no more than the listing already tells.

**Reach for `--max-uses 1` here more than anywhere else.** A secret an agent needs once is the clearest case in the whole model: one key, one read, then the grant is gone whether or not you remember it.

Four further bounds worth knowing:

- **`requireScope: [kv.get]`** in a [team policy](../30-boundary/50-team-policy.md) makes `rta grant allow kv.get` — which would cover the entire store — an error. Only a grant naming one key is accepted.
- **A rename needs a grant for both names.** A key's name decides which grants can read it, so moving `prod/db-password` to `scratch/db-password` is a question about where it lands as much as about what moves: with a read grant on `scratch/`, a rename checked only at its source would be a read of the prod secret. `rta grant allow kv.rename prod/` covers moves inside `prod/`; a grant naming one key needs a second naming the new name. A rename grant naming no key moves any key to any name, so beside a read grant it reads as far as that grant reaches — narrow it to a folder.
- **A key is written as the call names it.** A grant is compared with the name a call sends, white space included, so `kv set` and `kv rename` refuse a name with white space around it rather than write the name without it: the key an agent was allowed to write is the key written.
- **Values are masked in the record.** [`rta agent log`](../30-boundary/40-audit-trail.md) shows that `kv.get db-password` happened, not what came back.

## Related

- [Profiles](./40-profiles.md) — pointing connection credentials at stored entries

## Next

[Grants](../30-boundary/30-grants.md) — bounding what an agent can read.
