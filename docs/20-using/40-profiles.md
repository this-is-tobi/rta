# Profiles

A profile is a **named environment across every plugin that has something in it**. `staging` points `pg`, `s3` and `vault` at staging at once, rather than three sets of flags you retype.

It is also the unit of consent. `rta grant allow pg.query --profile staging` lets an agent reach that one environment and no other. Both halves name the same thing on purpose.

## Defining one

**From a script, one command per plugin:**

```bash
rta profile set staging --note "the shared staging stack" --ttl 8h \
    --plugin pg \
    --set host=db.staging.internal --set database=app \
    --secret password=kv:staging-db-password
```

It is idempotent — running it twice is running it once — so it belongs in a provisioning script, a Dockerfile, or a dotfiles repository as easily as in a terminal. `rta profile rm` is the other half.

**Or from the form, which is the shortest path at a keyboard.** Open the TUI, press `f` for profiles, then `n`:

```bash
rta
```

| Key | What it does |
| --- | --- |
| `f` | the profiles pane |
| `n` | a new profile — name it, then add a plugin to it |
| `enter` | open one, to add or edit the plugins inside it |
| `c` or `e` | edit whatever is selected |
| `tab` | in the plugin field, what is installed — already pinned to its artifact, because nobody should type a digest |

Both are generated from each plugin's own declared inputs — the same declaration the CLI flags and the MCP schema come from — so both show exactly the keys that plugin reads, with their types, defaults and bounds. Both know which inputs are credentials, and neither will let one land in `set:`.

Everything below is what they write, and is worth reading whether or not you use them: the file is yours to edit, and a profile is the thing a grant names.

Profiles live in your config (`rta init` creates it; `rta doctor` prints its path):

```yaml
profiles:
  staging:
    note: the shared staging stack
    ttl: 8h
    plugins:
      pg:
        set:
          host: db.staging.internal
          database: app
        secrets:
          password: kv:staging-db-password
      s3:
        set:
          endpoint: https://s3.staging.internal
        secrets:
          secret-key: kv:staging-s3-secret
      vault:
        set:
          address: https://vault.staging.internal
        secrets:
          token: kv:staging-vault-token
```

| Key | What it sets |
| --- | --- |
| `note` | What this environment is, shown by `profile list` |
| `ttl` | A deadline the profile brings with it when switched on |
| `plugins.<name>.set` | Overlays that plugin's own configuration |
| `plugins.<name>.secrets` | Maps a declared input onto **where its value comes from** |
| `plugins.<name>.kube` | Reach it through a `kubectl port-forward` |
| `plugins.<name>.ssh` | Reach it through an SSH jump host |
| `plugins.<name>.secrets-from` | Which cluster and namespace a `kube:` secret is read from, when the connection opens no forward |

## Several connections to one plugin

An environment rarely has exactly one of everything: staging holds the main database *and* the analytics one, two buckets, sometimes two Vault mounts. One profile holds them all, as **instances** — a label inside the key you already write:

```yaml
profiles:
  staging:
    plugins:
      pg:                     # the default instance — a bare key means what it always meant
        set: {host: db.staging.internal}
        secrets: {password: kv:staging-db-password}
      pg/analytics:           # a second database, same plugin
        set: {host: analytics.staging.internal}
        secrets: {password: kv:staging-analytics-password}
      s3/assets:
        set: {bucket: shop-assets}
      s3/logs:
        set: {bucket: shop-logs}
```

A call picks one with the same string everywhere — the flag, the MCP argument, the grant:

```bash
rta pg query --profile staging "select 1"             # the default instance
rta pg query --profile staging/analytics "select 1"   # the labeled one
rta profile set staging --plugin pg/analytics --set host=…   # stating it from a script
```

The resolution rules are small and fail closed:

- A bare `--profile staging` runs the **default** instance — the unlabeled entry, which is written as one.
- With no unlabeled entry and exactly one labeled, that one is unambiguous and wins.
- With several labeled entries and no default — the `s3` above — a bare name is **refused with the list**, never resolved by sort order: `staging/assets` or `staging/logs`, your call. Completion offers the refs directly, described by each instance's address.

Two things follow from an instance being *one connection*:

- **Grants name exactly one.** `rta grant allow pg.query --profile staging/analytics` consents to the analytics database and nothing else; asking for a bare `--profile staging` when several pg instances exist is refused with the same list, because "analytics, not the main database" is precisely the decision consent is recording. A policy `neverProfile: [prod]` covers every instance inside prod. `rta use staging` still switches — and bounds — the whole environment, instances included.
- **A labeled instance's credentials come from `secrets:` references** (`kv:` or `kube:`), not from `RTA_PROFILE_*` variables — that channel belongs to the default instance only, because a variable name for `staging/analytics` would be forgeable by a carefully-named profile.

## `secrets:` holds a reference, never a value

```yaml
secrets:
  password: kv:staging-db-password
  token: kube:postgres-creds/password
```

A config file is plaintext, read on every invocation, with nobody watching. So this block holds a **name**, and the value is fetched at resolution time — reaching neither this file, nor an environment variable, nor an argv.

A `kube:` reference needs to know *which* cluster and namespace to read from. A `kube:` coordinate answers that as a side effect of naming a forward — but a connection that reaches its service directly has no forward to name, and stating one purely to locate a Secret would drag the call through a port-forward it never needed and quietly override `set: host`. `secrets-from:` is that answer on its own:

```yaml
pg:
  set:
    host: db.example.com     # reached directly, no forward
  secrets:
    password: kube:pg-creds/password
  secrets-from: homelab/databases
```

Two segments, `context/namespace` — a service and a port belong in `kube:`, which also opens a forward. State one or the other: a coordinate already names the namespace its Secrets come from, so both together are refused rather than ranked. The namespace is always the connection's own and never a caller's, which is what keeps one connection's reference from becoming a general-purpose cluster reader — and because moving it changes *which credential authenticates*, a standing grant does not survive the edit.

You write the mapping; a plugin never does. A plugin that could name the entry it wanted could name *any* entry in your store. All it declares is that it has a secret input.

**A stated value wins over a mapping**, so the two blocks must not target the same input:

```yaml
set:
  user: app          # this is what authenticates
secrets:
  user: kv:app-user  # never fetched
```

That is reported, and it matters most in the direction you would actually hit it: moving a credential out of `set:` and into `secrets:` while leaving the old line behind changes nothing, and the plaintext one is still in force. The report names the plaintext line as the one to remove.

## Reaching things that are not directly reachable

```yaml
plugins:
  pg:
    kube: staging/db/svc/postgres:5432
```

A call filled from that connection runs through a `kubectl port-forward` that rta raises and tears down again. The plugin sees an ordinary local address and never learns a tunnel was there — which is why no service plugin needs changing to gain this.

```yaml
plugins:
  pg:
    ssh: bastion.example.com/db.internal:5432
```

The same fact, spelled for a service behind a jump host rather than in a cluster. The head is an `~/.ssh/config` alias, and everything your SSH config says about it keeps working — rta shells out to `ssh`.

One forward per call, torn down afterwards. A cached port-forward outlives the pod it points at, and a stale tunnel to a rescheduled pod fails in a way nobody can read.

A connection states **at most one** of `kube` and `ssh`; both at once is refused.

### When the far side speaks TLS on its own

```yaml
plugins:
  vault:
    kube: homelab/vault-operator-system/svc/vault:8200
    tunnelTLS: true
    set:
      ca-file: ~/.config/rta/vault-ca.crt
```

`kubectl port-forward` and `ssh -L` are both a raw byte pipe from `127.0.0.1` straight into whatever the destination socket speaks — neither terminates a request the way a proxy would. So the plain `http://` a forward fills in by default is correct for the ordinary case (a plaintext service behind a TLS-secured cluster or bastion hop) and silently wrong for a service whose own listener speaks TLS, Vault's being the common example: the forward carries the TLS bytes through unchanged, and a plain HTTP client sending a request into them gets a connection that closes with nothing readable back.

`tunnelTLS: true` says the far end terminates TLS itself, so the forward should be addressed as `https://` — refused if the connection states neither `kube:` nor `ssh:`, since there is then no forward for it to describe. It changes *scheme only*. Certificate verification still runs as normal, against whatever this machine already trusts: a self-signed or cluster-internal CA (an operator-generated root, a private issuer) still refuses, exactly as it would over a direct connection, and `tunnelTLS: true` does not become `--insecure` under any configuration.

**Named apart from any plugin's own `tls` or `sslmode`.** etcd, qdrant and s3 each read `set: {tls: ...}`; pg reads `set: {sslmode: ...}` — that is the plugin's *own* on/off toggle, in its own client library's vocabulary, and the host forces it off over a tunnel (see [Types are part of the declaration](#types-are-part-of-the-declaration) below for that mechanism). `tunnelTLS` is a different fact at a different layer: not a plugin's setting, but what the host must know about the coordinate itself to address it correctly, before any plugin config is even read. The two can sit beside each other in the same entry without conflict — they answer different questions — but they would not if they shared a word.

Where that CA lives is a plugin's own concern rather than the tunnel's, because reading it is a file-read primitive and the plugin declares — or does not — that it trusts a caller-named path for it: a PEM bundle read from this machine, never from the cluster, and never fillable by an MCP caller. `rta explain <capability>` lists whether a given plugin offers one. Not a secret either — a CA certificate is the public half of a key pair, the half a CA hands out for wide distribution so anyone can verify what it signed, the same reason an OS trust store ships thousands of them in the clear. It needs no more protection than `address` does, and reading it through `kv:`/`kube:` the way a credential is would be reaching for the wrong tool.

The field is named for what it mirrors, plugin by plugin, rather than one word forced everywhere:

| Plugin | Field | Why that name |
| --- | --- | --- |
| `etcd`, `vault`, `s3`, `qdrant` | `ca-file` | No existing library vocabulary to mirror, so these four agree with each other instead |
| `pg` | `sslrootcert` | Its own `sslmode` already commits this plugin to libpq's vocabulary, and `sslrootcert` is libpq's own keyword for exactly this — pgx's DSN parser reads it directly |

**`pg`'s `sslrootcert` only matters for a connection reached directly — no `kube:`, no `ssh:`.** A tunnelled one forces `sslmode` to `disable` regardless of what is written under `set:`: `sslmode` carries `plugin.EndpointTLS`, the role a forward's own hop takes over unconditionally (the same mechanism etcd's, qdrant's and s3's own `tls` field go through, and the reasoning is the same one — PostgreSQL's TLS kills a `kubectl port-forward` on the clean disconnect). `disable` never attempts TLS at all, so `sslrootcert` sits in the connection string built and unread. This is not new with `sslrootcert` — it is the standing rule that governs everything `set:` states about transport security under a coordinate — but a CA is the one value here that is easy to type expecting it to survive, since nothing about the DSN complains that it did not.

For a directly-reached server — a managed Postgres, an on-prem instance with no forward in front of it — `sslmode` is exactly what `set:` states:

```yaml
plugins:
  pg:
    host: pg.example.internal
    set:
      sslmode: verify-ca
      sslrootcert: ~/.config/rta/pg-ca.crt
```

**`sslmode` does not follow `sslrootcert` automatically, and that is worth reading twice even here.** `sslmode`'s own default, `prefer`, tells the driver to skip certificate verification regardless of what `sslrootcert` names — filling in a CA and leaving `sslmode` untouched builds a trust store and then never consults it. This is deliberate rather than a gap: which axis to move — the CA, how strict to be about it — is two separate decisions, and silently elevating one because the other was set would be a second, undocumented way `sslmode`'s value changes (the codebase already argues against exactly that kind of surprise — see the type-coercion section below). Set both, as above.

## Writing one from a script

`rta profile set` states a profile from flags. Nothing about it needs a terminal, which is the point: before it existed the only alternatives were a TTY form and hand-written YAML, and a team that cannot script its setup ships the YAML — the path where nothing checks the block until something tries to use it.

```bash
rta profile set <name> [--note ...] [--ttl 8h|none]
                       [--plugin <name> [--set k=v ...] [--secret input=kv:entry ...]
                                        [--kube ...] [--ssh ...] [--direct] [--tunnel-tls]]
rta profile rm  <name> [--plugin <name>]
```

**Each block is replaced by what the flags state, and a block no flag mentions is left alone.** So `--set` states that plugin's whole `set:` block — omit a key to remove it — while its `secrets:` stays exactly as it was, and a run with neither touches neither. That is the only reading under which running the command twice and running it once are the same thing, which is what makes it safe in a script that runs on every boot.

**Anything a restatement leaves out is named.** Changing one key of four is also how `sslmode: require` disappears from the line beside it, so the loss is reported rather than assumed to be intended:

```
dropped  set.database, set.sslmode — the flags state the whole block, and these were not among them
```

One plugin per invocation. A profile spanning three plugins is three lines, and each of them is independently re-runnable — including in parallel: every writer of the config file takes a lock across the whole read-modify-write, so concurrent runs cannot lose each other's profiles.

`rta profile rm <name>` removes the environment, switches it off if it was on, and **revokes every grant naming it**. That last part is not tidiness: a grant naming a profile nothing can look up authorizes nothing, so leaving it behind is a row in `rta grant list` that reads like access and is not. `--plugin` removes one entry and keeps the environment.

### What it refuses

| What you typed | Why it is refused |
| --- | --- |
| `--set password=…` on a declared credential | `set:` is a plaintext value in a world-readable file. It names `--secret` instead |
| `--secret password=hunter2` | that block takes a **reference**, never a value — and the refusal does not repeat what you passed |
| `--set port=six-thousand`, `--set tls=yes` | the declared type cannot hold it (see below) |
| `--set hsot=…` | nothing in that plugin reads the key |
| `--kube …` and `--ssh …` together | a call opens one forward |
| `--tunnel-tls` with neither `--kube` nor `--ssh` in effect (this run or already stored) | it states something about the far side of a forward that does not exist — `--direct` clears a stored `tunnelTLS: true` for the same reason |
| a profile named after an installed plugin | a profile name and a namespace share a command line |
| writing where the file is not honoured | with no config directory the config path falls back to `./.rta.yaml` — ordinary in a container or in CI — and nothing in a working-directory file is honoured: `profiles:`, `plugins:` and `dashboard:` are all ignored, because that file could have come from a repository you cloned. Set `$RTA_CONFIG` |

Neither credential refusal echoes the value it was given. If you did pass a real one, it is in your shell history — rta will not put it anywhere else.

`--plugin pg` is enough; the artifact pin is filled in from what is installed. A digest is not something anyone should type, and typing one wrong is exactly the failure the pin exists to prevent.

**Tab completion knows the keys.** With `--plugin` on the line, `--set <tab>` offers exactly what that plugin reads, with its help text and — for a closed set — its accepted values. `--secret <tab>` offers the inputs a mapping may target, marking which of them are credentials. A credential never appears under `--set`, because it cannot be a config key at all:

```
$ rta profile set staging --plugin pg --set <tab>
database=   database to connect to
host=       database host
port=       database port
sslmode=    disable|prefer|require|verify-ca|verify-full
user=       role to connect as
```

### Adding a forward to an existing connection

A forward fills the endpoint inputs itself, so a stated host beside a coordinate is a line no run reads. `--kube` on a connection that already sets one drops the keys it replaces and says so:

```
removed  set.host, set.port — the forward fills those, so nothing read them
```

Stating both in the same command is refused instead. Quietly dropping half of what you just typed is a different thing from clearing a line you are not looking at.

## Types are part of the declaration

Every value in `set:` is read back as the type the plugin declared, by a type assertion. A value of the wrong shape is therefore neither refused nor ignored — it is read as the **zero**:

```yaml
set:
  tls: "true"     # a string. The handler reads false.
  tls: yes        # also a string — YAML 1.2. The handler reads false.
  port: "5432"    # a string. The handler reads 0, and the declared default is gone.
```

Both `tls` spellings leave a connection running without the transport security its own configuration states. `rta profile list` and `rta doctor` now report all three, and a mistyped profile refuses to resolve rather than connecting somewhere unexpected:

```
profiles.staging.pg: `set: tls` is written as text where a boolean is declared — the handler would read false
  (write it unquoted as `true` or `false` — a quoted `"true"` is a string, and so is a bare `yes`)
```

There is deliberately no coercion. Reading `"true"` as true would then have to answer for `yes`, `on`, `1` and `TRUE`, and every answer is a guess about a value that decides whether a connection is encrypted.

`rta profile set` cannot produce this: a flag argument is always text, so it converts to the declared type before writing, and refuses what will not convert.

The same rule covers the base `plugins:` block, which `rta doctor` reports.

## A secret in the wrong block

`set:` holds values and `secrets:` holds references, and putting a credential in the first one is the mistake this grammar invites. It is inert — nothing reads it, and `profile show` says so:

```
problem   nothing in pg reads "password" — `rta explain <capability>` lists the config keys it reads
```

The value itself is redacted in that output, because the config file is written world-readable on the documented basis that it holds no secrets. Move it:

```yaml
secrets:
  password: kv:staging-db-password
```

## Using one

```bash
rta profile list                 # what is configured, and whether each is usable
rta profile show staging         # what it sets, and where each value comes from
rta pg query --profile staging   # one command against it
```

Or switch your whole machine to it:

```bash
rta use staging
rta use staging --for 2h
rta use                          # what is on
rta use --off
```

While a profile is on, every later command for a plugin it covers runs against it with no `--profile` at all.

**The deadline is real.** `--for` overrides it, a profile's own `ttl:` supplies it, and when it lapses everything falls back to the base configuration on its own. A deadline that depended on a process staying alive would not be a deadline.

## Switching authorizes nothing

This is the part worth getting right.

`rta use staging` does not grant anything. What it does is the opposite: **while a profile is on, `rta mcp serve` refuses every profile but that one**, whatever grants exist.

So it is the fastest way to take an environment away from an agent —

```bash
rta use staging        # agents can now reach staging and nothing else
```

— and it can only ever take away, never give. An agent still needs a grant to reach `staging` itself; switching just guarantees it cannot reach `production` while you are working in staging, without you auditing a single grant.

## Grants and profiles together

```bash
rta grant allow pg.query --profile staging --agent claude --ttl 1h
```

Profile matching is **exact in both directions**. A grant naming `staging` matches a call resolving through `staging`, and nothing else. An empty profile on a grant is not a wildcard — it matches a call that resolved through no profile.

A [team policy](../30-boundary/50-team-policy.md) can forbid a connection outright, which is the blunt instrument for "not production, ever":

```yaml
neverProfile:
  - production
```

## Editor completion for the file

The config file has a JSON Schema, and rta prints it:

```bash
rta config schema > schema.json   # next to the config file — `rta doctor` prints where that is
```

Then put one modeline at the top of the config file:

```yaml
# yaml-language-server: $schema=schema.json
```

VS Code's YAML extension (`redhat.vscode-yaml`) and every other editor speaking yaml-language-server now complete each key, flag unknown ones, and show the explanation on hover — `tunnelTLS` tells you it is about the destination and not the hop without leaving the file.

The schema states the envelope, deliberately. What a `plugins:` section or a `set:` overlay may hold *inside* is each plugin's own declaration, which the schema cannot know without knowing every plugin — `rta explain <ns>` lists those keys, and `rta doctor` stays the deep validator for everything the envelope cannot see.

## Checking it

```bash
rta doctor
```

```
profile   ok     staging → pg@685186a7, s3@a586c1f1 — pg.password from kv:staging-db-password
profile   info   staging is switched on with no deadline — while it is,
                 `rta mcp serve` refuses every other profile, whatever grants exist
```

That second line is the one to read. A profile switched on with no deadline is a state you chose; rta just makes sure you know you are in it.

## Next

- [Grants](../30-boundary/30-grants.md) — `--profile` as a bound
- [Secrets](./50-secrets.md) — what `kv:` references point at
- [Using plugins](../40-plugins/10-plugins.md) — the plugins a profile configures
