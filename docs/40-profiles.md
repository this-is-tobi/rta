# Profiles

A profile is a **named environment across every plugin that has something in it**. `staging` points `pg`, `s3` and `vault` at staging at once, rather than three sets of flags you retype.

It is also the unit of consent. `rta grant allow pg.query --profile staging` lets an agent reach that one environment and no other. Both halves name the same thing on purpose.

## Defining one

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

| Key | |
| --- | --- |
| `note` | What this environment is, shown by `profile list` |
| `ttl` | A deadline the profile brings with it when switched on |
| `plugins.<name>.set` | Overlays that plugin's own configuration |
| `plugins.<name>.secrets` | Maps a declared input onto **where its value comes from** |
| `plugins.<name>.kube` | Reach it through a `kubectl port-forward` |
| `plugins.<name>.ssh` | Reach it through an SSH jump host |

## `secrets:` holds a reference, never a value

```yaml
secrets:
  password: kv:staging-db-password
  token: kube:postgres-creds/password
```

A config file is plaintext, read on every invocation, with nobody watching. So this block holds a **name**, and the value is fetched at resolution time — reaching neither this file, nor an environment variable, nor an argv.

You write the mapping; a plugin never does. A plugin that could name the entry it wanted could name *any* entry in your store. All it declares is that it has a secret input.

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

A [team policy](./23-team-policy.md) can forbid a connection outright, which is the blunt instrument for "not production, ever":

```yaml
neverProfile:
  - production
```

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

- [Grants](./21-grants.md) — `--profile` as a bound
- [Secrets](./30-secrets.md) — what `kv:` references point at
- [Using plugins](./50-plugins.md) — the plugins a profile configures
