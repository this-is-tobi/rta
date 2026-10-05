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

**Or from the form, which is the shortest path at a keyboard.** Open the TUI, press an arrow to select a tile, press `f` for profiles, then `n`:

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

Both are generated from each plugin's own declared inputs — the same declaration the CLI flags and the [MCP](../95-reference/10-glossary.md#acronyms) schema come from — so both show exactly the keys that plugin reads, with their types, defaults and bounds. Both know which inputs are credentials, and neither will let one land in `set:`.

Everything below is what they write, and is worth reading whether or not you use them: the file is yours to edit, and a profile is the thing a grant names.

Profiles live in your config (`rta doctor` prints its path):

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
rta dashboard add pg.overview --profile staging   # a tile about it, whatever is switched on
```

Or switch your whole machine to it:

```bash
rta use staging
rta use staging --for 2h
rta use                          # what is on
rta use --off
```

While a profile is on, every later command for a plugin it covers runs against it with no `--profile` at all, and says so first: at a terminal each prints `● staging 59m left` on stderr before its result, the bullet the TUI's header draws for it. A pipe, `-o json` and the other machine-readable formats never get the line, and a command that names its own `--profile` has already said which environment it means.

**The deadline is real.** `--for` overrides it (a length of time as everywhere else, in days too: `8h`, `1d`), a profile's own `ttl:` supplies it, and when it lapses everything falls back to the base configuration on its own. A deadline that depended on a process staying alive would not be a deadline.

### Mark the ones you would rather not be in by accident

A switch outlives the command that made it — that is the whole point of it — so the twentieth command afterwards runs against production with nothing but your memory saying so. Every environment that is on announces itself before the commands it can change, and giving one a colour makes it say so before every command, in that colour:

```yaml
profiles:
  shop-prod:
    color: "#FF6B7A"
    ttl: 1h
    plugins:
      pg:
        set: { host: db.internal }
```

```
 shop-prod  47m left
hostname   db.internal
...
```

The badge prints above the result while that environment is on, in the colour you gave it, with the deadline beside it when there is one. The TUI's dashboard header carries the same badge.

Three things about it are deliberate:

- **It paints the profile's name and nothing else.** A profile that could repaint the palette would put its colour on keys, labels and selection — right beside the ones that mean ok, warn and failed — and an environment marked red would draw healthy rows in the colour of a failure. That is worse than no marking at all, because it teaches the eye to ignore red.
- **A colour is what makes it louder, not what makes it appear.** An environment without a `color:` prints a plain green bullet before the commands it acts on — a command of a plugin it covers, run without a `--profile` of its own — and stays out of `rta use`, `rta doctor` and the plugins it says nothing about, which is what keeps the line meaning something. Marking one is you saying *this* is the environment worth interrupting you about, wherever you are, so a marked one prints before every command.
- **It never reaches machine-readable output.** `-o json` and `-o yaml` get exactly what they got before, because the output you read off your screen is the output you paste into a parser.

`--no-color` keeps the badge and drops the paint: `[ shop-prod ]`, which is also what a session under `NO_COLOR` or `TERM=dumb` gets. A colour rta cannot read marks nothing, so the environment announces itself as an unmarked one does, rather than in one you did not choose; it is reported by `rta profile list`, `rta profile show` and `rta doctor`, and never stops the environment from being switched to or used. Under a locale that does not name `UTF-8` the bullet is an asterisk, as everything rta draws is plain ASCII there (see [the CLI](./10-cli.md)).

## Switching authorizes nothing

This is the part worth getting right.

`rta use staging` does not grant anything. What it does is the opposite: **while a profile is on, `rta mcp serve` refuses every profile but that one**, whatever grants exist.

So it is the fastest way to take an environment away from an agent —

```bash
rta use staging        # agents can now reach staging and nothing else
```

— and it can only ever take away, never give. An agent still needs a grant to reach `staging` itself; switching just guarantees it cannot reach `production` while you are working in staging, without you auditing a single grant.

The selection is sealed like the grants are, with a key beside it in the data directory, so a file that was edited, truncated, replaced or removed is one rta does not believe. A selection it does not believe is not "nothing is on": it is held shut, and every profile is refused to agents until `rta use <profile>` or `rta use --off` writes it again. `rta doctor` reports it as `profile selection`, and the record notes it on each refused call. The seal is a tamper alarm and not a lock — whatever reads the key can seal a selection of its own, as it could for the grants — which is why the answer to that threat stays the grants and the [guard](../30-boundary/35-roles-consent-and-the-guard.md#the-guard-a-passphrase-in-front-of-issuance).

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

## Checking it

```bash
rta doctor
```

```
profile   ok     staging → pg@685186a7f1c2, s3@a586c1f19b04 — pg.password from kv:staging-db-password
profile   info   staging is switched on with no deadline — while it is,
                 `rta mcp serve` refuses every other profile, whatever grants exist
```

That second line is the one to read. A profile switched on with no deadline is a state you chose; rta just makes sure you know you are in it.

## Related

- [Profiles in depth](./41-profiles-in-depth.md) — several connections to one plugin, writing a profile from a script, types, and editor completion
- [Reaching private services](./42-reaching-private-services.md) — a connection that goes through `kubectl port-forward` or `ssh`
- [Grants](../30-boundary/30-grants.md) — `--profile` as a bound
- [Using plugins](../40-plugins/10-plugins.md) — the plugins a profile configures

## Next

[Secrets](./50-secrets.md) — what `kv:` references point at.
