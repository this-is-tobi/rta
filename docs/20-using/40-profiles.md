# Profiles

A profile is a **named environment across every plugin that has something in it**. `staging` points `pg`, `s3` and `vault` at staging at once, rather than three sets of flags you retype.

It is also the unit of consent. `rta grant allow pg.query --profile staging` lets an agent reach that one environment and no other — down to the one namespace or database, where the profile [fences it](#only-fences-an-input-to-the-values-a-connection-reaches), for the inputs a call carries ([what a fence does not hold](./41-profiles-in-depth.md#what-a-fence-does-not-hold)). Both halves name the same thing on purpose.

## Defining one

**From a script, one command per plugin:**

```bash
rta profile set staging --note "the shared staging stack" --ttl 8h \
    --plugin pg \
    --set host=db.staging.internal --set database=app \
    --secret password=kv:staging-db-password
```

It is idempotent — running it twice is running it once — so it belongs in a provisioning script, a Dockerfile, or a dotfiles repository as easily as in a terminal. `rta profile rm` is the other half.

`kv:staging-db-password` names an entry in [the store](./50-secrets.md), and the profile does not make it: `rta kv set staging-db-password` does, and makes the store with it on a machine that has none. Until there is a store, `profile set` ends with a `warning` row saying so, `profile list` puts the same sentence in its `Note` column and marks the profile `warn` (`on` while it is switched on), `profile show` repeats it, the TUI's profiles pane grades the profile `warn` with it, and `rta doctor` says it in the same words. Whether an entry is in a store that exists is something only its passphrase can say, so a listing does not say it, and a call that reads a missing one fails at the call and says which entry. `rta doctor` says it sooner where it can. With a store that exists it notes when a server started from the shell it runs in could not open it — a passphrase store without `RTA_KV_PASSPHRASE` set, or a key a passphrase still locks — which a server given the variable in its own environment is not subject to. When that shell opens the store by itself, with a key file or the variable, it warns about each entry the store does not hold and about a passphrase that does not fit. `rta config check` does not say it: it holds the file, which reads the same on a machine that has a store and on one that has not made it yet, and a check that failed the file on one and passed it on the other would not fit a pre-commit hook.

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
| `dashboard` | The screen drawn while it is switched on: the name of a [view](./25-dashboard-and-theme.md#views-a-screen-per-environment), or [the screen itself](./25-dashboard-and-theme.md#a-screen-written-in-the-profile), written in the entry with the keys a view has, [sections](./25-dashboard-and-theme.md#sections-group-tiles-and-fold-the-group) included |
| `plugins.<name>.set` | Overlays that plugin's own configuration — a default, which a caller can override |
| `plugins.<name>.only` | Fences an input to the values this connection reaches, per config key — a list a call cannot go outside |
| `plugins.<name>.secrets` | Maps a declared input onto **where its value comes from** |
| `plugins.<name>.kube` | Reach it through a `kubectl port-forward` |
| `plugins.<name>.ssh` | Reach it through an SSH jump host |
| `plugins.<name>.secrets-from` | Which cluster and namespace a `kube:` secret is read from, when the connection opens no forward |

**A call names its scope; a profile does not.** An input a capability declares as the record it works on — `kv.get`'s key, a bucket, a namespace — is the one a grant is checked against, before anything is resolved, so it is named by the call and never filled from a profile. `set:` is a default for every other input and nothing for that one. kube's `namespace` is where this shows: `kube.pod.list`, `kube.deployment.list`, `kube.event.list`, `kube.quota.list`, `kube.pvc.list`, `kube.cert.list`, `kube.metrics.pod` and `kube.serviceaccount.list` take it as their scope, so `--set namespace=payments` is not what they read, and `rta kube pod list --profile payments-staging` reads the context's own namespace unless the call says `--namespace payments`. `kube.serviceaccount.provision` and `kube.serviceaccount.revoke` work in a namespace rather than on a record in it, and those two do read the profile's. The line is not refused, since the two read it; `rta profile set`, `rta profile show`, `rta profile list`, `rta doctor` and `rta config check` each say which capabilities it does not reach, and the connection editor says it under the box.

`rta profile list` has a `Screen` column — `ops` for a profile that selects the view of that name, `inline: 3 tiles` for one that writes its screen in its entry — and `rta profile show` says the same beside the command that lists the tiles. `rta profile set staging --dashboard ops` selects a view and refuses a profile whose screen is written in its entry, rather than throw the tiles away; `rta profile rm` takes the screen with the entry and says so. The TUI's profiles pane names the screen on each environment's line and keeps it through every form that edits the entry.

## `secrets:` holds a reference, never a value

```yaml
secrets:
  password: kv:staging-db-password
  token: kube:postgres-creds/password
```

A config file is plaintext, read on every invocation, with nobody watching. So this block holds a **name**, and the value is fetched at resolution time — reaching neither this file, nor an environment variable, nor an argv. It does not reach an agent either: one granted the profile has the credential read on this machine for each call and never receives it, which is why a `kv.get` grant on the entry is never part of using the connection, and would be the one thing that hands it over.

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
problem   under pg: nothing in pg reads "password" — `rta explain <capability>` lists the config keys it reads
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
rta pg query 'select 1' --profile staging   # one command against it
rta dashboard add pg.overview --profile staging   # a tile about it, whatever is switched on
```

Or switch your whole machine to it:

```bash
rta use staging
rta use staging --for 2h
rta use                          # what is on
rta use --off
rta pg query 'select 1' --no-profile --host db.other --user me   # this one call, without it
```

While a profile is on, every later command for a plugin it covers runs against it with no `--profile` at all, and says so first: at a terminal each prints `● staging 59m left` on stderr before its result, the bullet the TUI's header draws for it. A pipe, `-o json` and the other machine-readable formats never get the line, and a command that names its own `--profile` has already said which environment it means.

**One call can leave it.** `--no-profile` runs a single command as if nothing were switched on: none of the environment's values, none of its credentials, and none of its instances to choose among. What the call runs on is the flags you type and the base configuration. It is on every command that takes `--profile`, it is refused beside `--profile` (two answers to which environment), and it is the per-call twin of `rta use --off`, which switches the environment off for everything until you switch it on again. Without it, a typed `--host` or `--context` replaces the one value the environment states and keeps the rest of it, and an environment holding several connections to a plugin refuses a call that names none of them — `--no-profile` is how a call says it wants none. The line above the result says so: `● ad hoc — without staging`. A command with no environment on has nothing to leave and prints nothing.

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

The badge prints above the result while that environment is on, in the colour you gave it, with the deadline beside it when there is one. The TUI's dashboard header carries the same badge, and the colour is the `primary` of what the CLI and the TUI draw while the environment is in force — titles, keys, headings and the selection — unless the profile states a `primary` of its own in its [`theme:`](./25-dashboard-and-theme.md#a-palette-per-environment), which can also recolour the rest.

Four things about it are deliberate:

- **It is a mark and an identity colour, and never a status.** `color:` paints the profile's name and becomes the `primary` of what is drawn, which is the one colour that says *you are here*. It does not touch `good`, `warn` or `bad`, and a colour that is exactly one of them is left out of the palette (`rta doctor` names it), because an environment marked in the colour of a failure would put its selection in it, and a mark that reads as a status teaches the eye to ignore red. A close match is not refused: only the colours themselves are compared, and the badge is always drawn.
- **A colour is what makes it louder, not what makes it appear.** An environment without a `color:` prints a plain green bullet before the commands it acts on — a command of a plugin it covers, run without a `--profile` of its own — and stays out of `rta use`, `rta doctor` and the plugins it says nothing about, which is what keeps the line meaning something. Marking one is you saying *this* is the environment worth interrupting you about, wherever you are, so a marked one prints before every command.
- **It never reaches machine-readable output.** `-o json` and `-o yaml` get exactly what they got before, because the output you read off your screen is the output you paste into a parser.
- **It says what you typed over.** A value typed on the command line wins over the environment's own, and the rest of the environment — its user, its database, its credential — still applies. So `rta kube pod list --context lab` under `rta use staging` prints `● staging, except --context lab`: it went to `lab`, with whatever else `staging` says, and the line no longer names an environment the result did not come from. Only a value the environment states and the command changes is listed. An address is shown without its credentials or its query, a long value is cut, and a credential typed never appears: config is refused on one, so it is not part of where a call went. A command that names its own environment with `--profile` prints no line, since it has said so itself. A call that wants none of the environment says `--no-profile`, and its line reads `● ad hoc — without staging`.

An environment can go further than one colour: `rta profile set shop-prod --theme label='#C792EA'` gives it a palette, and [a palette per environment](./25-dashboard-and-theme.md#a-palette-per-environment) says how it lies over yours and what it may not do. `rta profile show` prints it, `rta profile list` counts it, and the TUI's `t` edits it.

`--no-color` keeps the badge and drops the paint: `[ shop-prod ]`, which is also what a session under `NO_COLOR` or `TERM=dumb` gets, in the TUI's header as on the command line, for an environment with a colour and one without. A colour rta cannot read marks nothing, so the environment announces itself as an unmarked one does, rather than in one you did not choose; it is reported by `rta profile list`, `rta profile show` and `rta doctor`, and never stops the environment from being switched to or used. Under a locale that does not name `UTF-8` the bullet is an asterisk, as everything rta draws is plain ASCII there (see [the CLI](./10-cli.md)).

## `only:` fences an input to the values a connection reaches

`set:` is a default, and the caller always wins: `rta pg query --profile staging --database elsewhere` connects to `elsewhere`. That is the right rule for a person who typed it and the wrong one for "this environment is that database and no other", because an agent granted `staging` could name any database the connection can see — one a sibling profile pins among them — or send an empty string over the one `set:` states. `only:` is the other half. Per config key, the values the connection reaches:

```yaml
profiles:
  staging:
    plugins:
      pg:
        set:
          database: app
        only:
          database: [app, app-jobs]
```

Whatever a call resolves `database` to — what was typed or sent, the profile's own `set:`, an exported `RTA_PROFILE_STAGING_DATABASE`, the plugin's section, the default — has to be one of those, or the call is refused before anything is fetched, opened or dialled. `rta profile set` states it with `--only database=app --only database=app-jobs` and takes it off with `--unfence`; `rta profile show` prints it, and `rta profile list`, `show` and `rta doctor` report a list the host could not hold a call to. [Profiles in depth](./41-profiles-in-depth.md#what-a-fence-holds-exactly) has the exact rules.

A fence only ever narrows. It is a key of the profile, so the grant that names the profile is a grant on the connection *as fenced*: editing the list is a different connection, and a standing grant asks again, the way it does for a changed host. Over [MCP](../95-reference/10-glossary.md#acronyms) an agent is told which input went outside what the connection reaches, or that it was left blank, and nothing else — not the list, not the profile's name — and the record carries the sentence a person would have been given. The form that edits a connection in the TUI edits `set:` and not `only:`: a value typed into a box outside that box's list is refused where it is typed — `must be one of app, app-jobs` beside the key, the cursor staying on the box — and the list is changed with `rta profile set --only`.

## Switching authorizes nothing

This is the part worth getting right.

`rta use staging` does not grant anything. What it does is the opposite: **while a profile is on, `rta mcp serve` refuses every profile but that one**, whatever grants exist.

So it is the fastest way to take an environment away from an agent —

```bash
rta use staging        # agents can now reach staging and nothing else
```

— and it can only ever take away, never give. An agent still needs a grant to reach `staging` itself; switching just guarantees it cannot reach `production` while you are working in staging, without you auditing a single grant.

The agent is refused in the words an ungranted call gets, so that it learns nothing about where you are working, and you read the difference where you read everything else. `rta grant list` keeps every grant that stands on the [roster](../95-reference/10-glossary.md#terms) and says, beside it, how many of them the environment that is on is holding inert and which environments they name; `rta agent log --refused` notes, on a call a grant would have covered, that the grant exists and which environment is on, with `rta use` as the way to switch. Issuing the grant again changes nothing.

The selection is sealed like the grants are, with a key beside it in the data directory, so a file that was edited, truncated, replaced or removed is one rta does not believe. A selection it does not believe is not "nothing is on": it is held shut, and every profile is refused to agents until `rta use <profile>` or `rta use --off` writes it again. `rta doctor` reports it as `profile selection`, and the record notes it on each refused call. The seal is a tamper alarm and not a lock — whatever reads the key can seal a selection of its own, as it could for the grants — which is why the answer to that threat stays the grants and the [guard](../30-boundary/35-roles-consent-and-the-guard.md#the-guard-a-passphrase-in-front-of-issuance).

## Grants and profiles together

```bash
rta grant allow pg.query --profile staging --agent claude --ttl 1h
```

Everything an environment covers can be allowed at once with `rta grant issue --profile staging --agent claude`, which issues one grant for each capability on each connection the environment holds, reads unless `--safety` says more. See [roles](../30-boundary/35-roles-consent-and-the-guard.md#everything-an-environment-covers-without-writing-the-role).

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
profile   info   staging is switched on with no deadline — while it is,
                 `rta mcp serve` refuses every other profile, whatever grants exist
profile   ok     staging → pg@685186a7f1c2, s3@a586c1f19b04 — pg.password from kv:staging-db-password
```

The first line is the one to read, and doctor puts it first for that reason. A profile switched on with no deadline is a state you chose; rta just makes sure you know you are in it.

## Related

- [Profiles in depth](./41-profiles-in-depth.md) — several connections to one plugin, writing a profile from a script, types, and editor completion
- [Reaching private services](./42-reaching-private-services.md) — a connection that goes through `kubectl port-forward` or `ssh`
- [Grants](../30-boundary/30-grants.md) — `--profile` as a bound
- [Using plugins](../40-plugins/10-plugins.md) — the plugins a profile configures

## Next

[Secrets](./50-secrets.md) — what `kv:` references point at.
