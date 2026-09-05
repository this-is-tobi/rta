# Secrets (`kv`)

An encrypted local store for the things you keep re-pasting: database passwords, API tokens, certificates, private key files.

It is `age`-backed, it lives beside your config, and it never writes a value to a log, an argv or the terminal unless you ask it to.

```bash
rta kv set db-password
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

## Storing things

```bash
rta kv set api-token                          # prompts, nothing in history
rta kv set tls-cert --file server.pem
rta kv set db-password --description "staging replica"
```

rta detects what kind of thing it is — string, JSON, certificate, private key, SSH key, file — and `kv list` shows the kind and description without ever showing a value. `--kind` overrides the detection.

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

## Undoing a mistake



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

**`kv.get` is classified as a write**, even though it only reads the store — revealing a secret is the sensitive act, not the lookup. An MCP agent needs `--allow-write kv` before it can even attempt the call, and on top of that the store still has to open, which is a separate question from calling the capability.

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

Two further bounds worth knowing:

- **`requireScope: [kv.get]`** in a [team policy](../30-boundary/50-team-policy.md) makes `rta grant allow kv.get` — which would cover the entire store — an error. Only a grant naming one key is accepted.
- **Values are masked in the record.** [`rta agent log`](../30-boundary/40-audit-trail.md) shows that `kv.get db-password` happened, not what came back.

## Next

- [Grants](../30-boundary/30-grants.md) — bounding what an agent can read
- [Profiles](./40-profiles.md) — pointing connection credentials at stored entries
