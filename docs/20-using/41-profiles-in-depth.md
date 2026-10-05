# Profiles in depth

[Profiles](./40-profiles.md) is the everyday page: defining an environment, using it, switching it on. This one is what comes after, when one environment is not enough or the config is written by something other than you: several connections to one plugin, a profile from a script, why every value has a type, and completion for the file in your editor.

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
rta profile show staging/analytics                    # that one connection, inside its environment
```

The resolution rules are small and fail closed:

- A bare `--profile staging` runs the **default** instance — the unlabeled entry, which is written as one.
- With no unlabeled entry and exactly one labeled, that one is unambiguous and wins.
- With several labeled entries and no default — the `s3` above — a bare name is **refused with the list**, never resolved by sort order: `staging/assets` or `staging/logs`, your call. Completion offers the refs directly, described by each instance's address.

Two things follow from an instance being *one connection*:

- **Grants name exactly one.** `rta grant allow pg.query --profile staging/analytics` consents to the analytics database and nothing else; asking for a bare `--profile staging` when several pg instances exist is refused with the same list, because "analytics, not the main database" is precisely the decision consent is recording. A policy `neverProfile: [prod]` covers every instance inside prod. `rta use staging` still switches — and bounds — the whole environment, instances included.
- **A labeled instance's credentials come from `secrets:` references** (`kv:` or `kube:`), not from `RTA_PROFILE_*` variables — that channel belongs to the default instance only, because a variable name for `staging/analytics` would be forgeable by a carefully-named profile.

## Writing one from a script

`rta profile set` states a profile from flags. Nothing about it needs a terminal, which is the point: before it existed the only alternatives were a TTY form and hand-written YAML, and a team that cannot script its setup ships the YAML — the path where nothing checks the block until something tries to use it.

```bash
rta profile set <name> [--note ...] [--ttl 8h|none] [--color "#dd3333"|none]
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
| `--set port=six-thousand`, `--set tls=yes` | the declared type cannot hold it (see below) — `core.profile.set.type` |
| `--set encoding=b64` on an input with a closed set | no call would accept it — `core.profile.set.option`. A listed value in another case is written the way the plugin declares it |
| `--set ping.count=0` | it is outside the widest range of any capability reading the key — `core.profile.set.range`. Inside it, each capability holds the value to its own range |
| `--set hsot=…` | nothing in that plugin reads the key |
| `--kube …` and `--ssh …` together | a call opens one forward |
| `--tunnel-tls` with neither `--kube` nor `--ssh` in effect (this run or already stored) | it states something about the far side of a forward that does not exist — `--direct` clears a stored `tunnelTLS: true` for the same reason |
| a profile named after an installed plugin | a profile name and a namespace share a command line |
| writing where the file is not honoured | with no config directory the config path falls back to `./.rta.yaml` — ordinary in a container or in CI — and nothing in a working-directory file is honoured: `profiles:`, `plugins:` and `dashboard:` are all ignored, because that file could have come from a repository you cloned. Set `$RTA_CONFIG` |

Neither credential refusal echoes the value it was given. If you did pass a real one, it is in your shell history — rta will not put it anywhere else.

`--plugin pg` is enough; the artifact pin is filled in from what is installed. A digest is not something anyone should type, and typing one wrong is exactly the failure the pin exists to prevent.

### Repinning after a plugin rebuild

Trust binds to the artifact's digest, never to a name or a version — see [the boundary](../30-boundary/10-the-boundary.md) — so `rta plugin upgrade pg` changes what "pg" pins to, and every profile still naming the old digest starts refusing with `this profile's pin does not match the installed "pg"` the next time it is resolved. `rta profile set staging --plugin pg` fixes one profile at a time, resolving the new digest itself rather than asking for one typed in. `rta profile repin` makes the same rewrite across every profile at once, touching nothing else stored beside the entry — the `set:`, `secrets:`, `kube:` and `ssh:` blocks all come along unchanged.

```bash
rta plugin upgrade pg
rta profile repin --all --plugin pg               # every profile with a pg entry
rta profile repin staging --plugin pg             # just one profile
rta profile repin staging --plugin pg/analytics   # just one instance of it
```

Name a profile, or pass `--all` — one or the other is required, so a repin's scope is always stated rather than assumed. `--dry-run` reports what would change without writing it. An entry already pinned to what is installed is reported and left alone.

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

A forward fills the endpoint inputs itself, so a stated host beside a coordinate is a line no run reads. A host given *on the call* — typed into the form, or passed as a flag — is different: it connects directly and no forward is opened, which is the override for a coordinate that is wrong. A TLS switch on the call — `--sslmode`, `--tls` — says how to talk, never where, so it opens no such way out: the call still goes through the forward, which turns that switch off, and a value asking for TLS is refused as `core.profile.tls.forward` before the forward opens, rather than dropped or taken somewhere else. Leave the switch out to go through the forward, or give the host and port as well to reach the server directly with it — the only place TLS you ask for is negotiated end to end. `--kube` on a connection that already sets one drops the keys it replaces and says so:

```
removed  set.host, set.port — the forward fills those, so nothing read them
```

Stating both in the same command is refused instead. Quietly dropping half of what you just typed is a different thing from clearing a line you are not looking at.

## Types are part of the declaration

Every value in `set:` is read back as the type the plugin declared, by a type assertion — so a value of the wrong shape would be read as the **zero**, and the host refuses it instead, on every call that reads it:

```yaml
set:
  tls: "true"     # a string where a boolean is declared. Refused — read, it would be false.
  tls: yes        # also a string — YAML 1.2. Refused the same way.
  port: "5432"    # a string. Refused — never run as port 0.
  sslmode: true   # a boolean where text is declared. Refused — read, it would be empty.
```

Each of these would otherwise leave a connection running somewhere, or without the transport security, its own configuration does not state. `rta profile list` and `rta doctor` report all four, and a mistyped profile refuses to resolve rather than connecting somewhere unexpected:

```
profiles.staging.pg: `set: tls` is written as text where a boolean is declared — every call reading it is refused
  (write it unquoted as `true` or `false` — a quoted `"true"` is a string, and so is a bare `yes`)
```

There is deliberately no coercion. Reading `"true"` as true would then have to answer for `yes`, `on`, `1` and `TRUE`, and every answer is a guess about a value that decides whether a connection is encrypted.

`rta profile set` cannot produce this: a flag argument is always text, so it converts to the declared type before writing, and refuses what will not convert.

The same rule covers the base `plugins:` block: `rta doctor` reports the line, and every call reading it is refused, naming the key it came from under the heading you wrote — at a terminal, `` `rta mysql status` takes text for --tls, not a boolean, which the config's plugins.mysql@f5074594a1c3.tls sets ``.

A number of the right shape outside what every capability reading its key takes is different: each capability holds it to its own nearest bound, so the profile still resolves — but not as written. `rta profile set` refuses to write one; written by hand, `rta profile list` shows the profile as `warn` — or as `on`, with the same note, while it is switched on — and `rta profile show` and `rta doctor` name the range to write instead.

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

## Related

- [Profiles](./40-profiles.md) — the everyday page
- [Grants](../30-boundary/30-grants.md) — a grant names a profile and, with an instance, one connection inside it

## Next

[Reaching private services](./42-reaching-private-services.md) — a connection that goes through `kubectl port-forward` or `ssh`, and the TLS rules that come with it.
