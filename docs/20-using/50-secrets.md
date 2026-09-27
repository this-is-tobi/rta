# Secrets (`kv`)

An encrypted local store for the things you keep re-pasting: database passwords, API tokens, certificates, private key files.

It is `age`-backed, it lives beside your config, and it never writes a value to a log, an argv or the terminal unless you ask it to.

```bash
rta kv set db-password --file db-password.txt
rta kv get db-password
rta kv list
```

## Choosing the lock, once

A passphrase store needs no setup — that is what you get by default. Everything else is a one-time decision:

```bash
rta kv init --generate
```

`--generate` makes a **dedicated age key for this store** and locks it to that. After it, nothing needs a flag and nothing needs typing: no passphrase, no `--identity`. And unlike your SSH login key, losing it costs you this store and nothing else.

```bash
rta kv init --identity ~/.ssh/id_ed25519     # a key you already have
rta kv init --recipient age1abc...            # let somebody else read it too
```

`--identity` accepts age or SSH keys. A passphrase-protected SSH key is asked for its passphrase the way `ssh` asks — ssh-agent cannot help here, because an agent signs and never decrypts.

`RTA_KV_PASSPHRASE` and `RTA_KV_IDENTITY` supply either without a flag, which keeps them out of shell history.

In [the TUI](./20-tui.md), a passphrase typed into an unlock form is kept in that TUI's memory for fifteen minutes past its last use, so a sitting costs one unlock rather than one per action. That is the only place an unlock outlives the command that made it: the CLI asks once per command, and a server unlocks from nothing but what its environment holds.

## Storing things

```bash
rta kv set api-token --file token.txt         # in no argv and no shell history
rta kv set tls-cert --file server.pem
rta kv set db-password --description "staging replica"
```

`kv set` never prompts for a value, and one typed after the key stays in your shell history. So a secret comes from `--file` — `--file /dev/stdin` from a pipe, as [the CLI](./10-cli.md) shows — or from the `kv set` form in [the TUI](./20-tui.md), which masks it as you type.

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

## Seeing the store without opening it

```bash
rta kv status
rta kv list
rta kv show db-password     # everything about it except the value
rta kv tree                 # the store by the folders its names share
rta kv recipients           # which public keys can decrypt this store
```

`kv status` answers "where is the store and what can open it" **without unlocking it**, which makes it safe to run anywhere — including in a script that is checking whether a machine is set up.

## Changing the lock afterwards

```bash
rta kv rekey --recipient age1colleague...            # add a reader
rta kv rekey --generate                              # move to a dedicated key
rta kv rekey --only --recipient age1me...            # drop every other reader
```

`rekey` always refuses to lock you out. That is not a courtesy — it is the difference between a key-rotation command you can use and one you use once.

## What this means for agents

This is the part to read before connecting an MCP client.

**`kv.get` is classified as a write**, even though it only reads the store — revealing a secret is the sensitive act, not the lookup. An MCP agent needs a grant naming `kv.get` — and ideally the one key it should read — before the call goes anywhere, and on top of that the store still has to open, which is a separate question from calling the capability.

```bash
rta doctor
```

```
kv store   info   unlocks from this environment — an MCP server started here
                  can read secrets, bounded only by grants
```

If you locked the store with `--generate` or an identity that is available in your shell, then a server started from that shell **can** open it. That is not a bug; it is what "no passphrase to type" means. What bounds it is grants:

```bash
rta grant allow kv.get db-password --ttl 15m --max-uses 1
```

Without a grant naming it, an agent's `kv.get` is refused and the refusal is written down.

**Reach for `--max-uses 1` here more than anywhere else.** A secret an agent needs once is the clearest case in the whole model: one key, one read, then the grant is gone whether or not you remember it.

Four further bounds worth knowing:

- **`requireScope: [kv.get]`** in a [team policy](../30-boundary/50-team-policy.md) makes `rta grant allow kv.get` — which would cover the entire store — an error. Only a grant naming one key is accepted.
- **A rename needs a grant for both names.** A key's name decides which grants can read it, so moving `prod/db-password` to `scratch/db-password` is a question about where it lands as much as about what moves: with a read grant on `scratch/`, a rename checked only at its source would be a read of the prod secret. `rta grant allow kv.rename prod/` covers moves inside `prod/`; a grant naming one key needs a second naming the new name. A rename grant naming no key moves any key to any name, so beside a read grant it reads as far as that grant reaches — narrow it to a folder.
- **A key is written as the call names it.** A grant is compared with the name a call sends, white space included, so `kv set` and `kv rename` refuse a name with white space around it rather than write the name without it: the key an agent was allowed to write is the key written.
- **Values are masked in the record.** [`rta agent log`](../30-boundary/40-audit-trail.md) shows that `kv.get db-password` happened, not what came back.

## Next

- [Grants](../30-boundary/30-grants.md) — bounding what an agent can read
- [Profiles](./40-profiles.md) — pointing connection credentials at stored entries
