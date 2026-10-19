# Declaring inputs

[Writing an rta plugin](./20-writing-a-plugin.md) is the fifteen-minute loop. This page is what a capability takes: its inputs, their types, how each surface asks for them, and how a plugin that talks to a service declares the connection once.

## Declaring inputs

```go
Inputs: []plugin.Field{
	{Name: "city", Type: plugin.String, Positional: true, Required: true, Help: "which city"},
	{Name: "units", Type: plugin.String, Default: "c", Options: []string{"c", "f"}},
	{Name: "key", Type: plugin.Secret, Local: true, EnvFallback: true, Help: "API key"},
	{Name: "out", Type: plugin.Path, Local: true, Help: "write the report here"},
}
```

`Type` is mandatory and closed — there is no inference from the name, because `Secret` is what makes a value masked and `Path` is what makes it completable, and neither is guessable. The host holds every value to it before your handler runs: one your accessor could not read — text where a `Bool` is declared, a boolean where a `String` is, a block where a list is — is refused on every surface, a value from the operator's config or a profile included, and never handed to you as the zero. Nothing is coerced: `"true"` is not read as true, because the next question is `yes`, and the one after that decides whether a connection is encrypted.

A credential is declared `Local: true, EnvFallback: true` as well as `Secret`, as `key` is above. `Secret` masks the value on every surface, and `Local` keeps the input out of an agent's schema, so no tool call is ever asked to carry an API key: the operator's own environment (`EnvFallback`) or a [profile](#a-connection-a-profile-can-fill) supplies it, and the capability is still one an agent can call. A `Secret` that is not `Local` is for a value an agent legitimately hands you to store — a password field on a record, a value to write — and it reaches an agent as an ordinary string argument, masked wherever it is shown afterwards.

Your handler reads each input by name, as the type it declared: `req.String("city")`, `req.Int("limit")`, `req.Float("ratio")`, `req.Bool("shout")`, `req.StringSlice("tag")`, `req.Duration("timeout")`. An input nobody gave and nothing filled reads as the zero of its type, which is why `Required` and `Default` are declarations and not something to check for in the handler.

A credential a caller may give more than once is `SecretSlice`, not `StringSlice`. It is `Secret`'s masking with `StringSlice`'s shape, and declaring the list type alone is the mistake worth naming: the value is then written to the completion shortlist and, over [MCP](../95-reference/10-glossary.md#acronyms), into the record in cleartext, because every sink that hides a credential asks whether the *type* is one. `vault.kv.set --data 'password=…'` is the shape.

A length of time is `Duration`, not an `Int` called `timeout`: `{Name: "timeout", Type: plugin.Duration, Default: "30s", Min: "1s", Max: "5m"}`. An `Int` never said whether it counted seconds or milliseconds, and the answer lived in a `Help` string, so `--timeout 30` was half a minute in one capability and thirty milliseconds in another. A `Duration` is written with its unit on every surface — `30s`, `5m`, `2h`, `1d`, `1h30m`; Go's units plus `d` and `w` — carried as that text, never as a number, and read with `req.Duration("timeout")`, which is a `time.Duration`. A bare number is refused with the spelling that is accepted, not read as seconds, since the host would be guessing for the plugin that meant something else. An agent's schema publishes it as a string with the grammar as a `pattern` and the range in its description, and a JSON number sent to it is told that it needs a unit. `Default`, `Min` and `Max` are written the same way, as text; the plugin fails to load when one is not a duration written that way, a `time.Duration` included, which every surface would print as nanoseconds. `sdktest` notes an `Int` named like a time (`timeout`, `ttl`, `interval`, …) and fails one whose `Help` never says its unit.

What each one buys you:

- **`Options`** becomes a TUI picker, shell completion, and an `enum` in the MCP schema. Use it for a fixed set of text, on a `String` or a `StringSlice`: declared on any other type, the plugin fails to load, because a number or a switch already says what it takes — `Min` and `Max`, or being a `Bool`. The host holds every value to it before your handler runs: one naming none of them is refused, on every surface, including a value from the operator's config. On the CLI, in the TUI and in config, a value naming an option in another case arrives spelled the way you declared it; over MCP the published enum is held exactly, and the refusal says so. A `Default` has to be one of the options.
- **`Min`** and **`Max`** bound an `Int`, a `Float` or a `Duration` (written as text, `Max: "5m"`), and on an `Int` they are whole numbers: a fractional one fails the load, since an `Int` reads no fraction, its bounds included. A value the caller sends outside them is refused with the range named, never moved inside it. A number from the operator's config or a profile is held inside them instead, because one config key serves every capability that declares it and each may bound it differently. Either way your handler can take the bound as given, and a value that is not a number at all is refused, as every value of the wrong type is.
- **`Suggest`** is a function, `func(ctx context.Context, req plugin.Request) []string`, returning what exists *right now* — your tags, their hostnames. An entry may carry a description after a tab, `"alpha\tin north"`, which shell completion shows and the other surfaces drop. It runs on human surfaces only, never for an agent: the list itself is information. It must be cheap and silent on failure, because it fires on a keystroke — no network call, no prompt, no connection opened. It receives what the caller has supplied so far, on the CLI and in a TUI form alike, so a suggestion can depend on a sibling field being typed above it. Tab completes it on both surfaces. Not accepted on a `Secret`, a `SecretSlice` or a `Text` input: the list renders in plain text beside the box, which defeats a mask, and a body is written in `$EDITOR` rather than completed.
- **`Live: true`**, on a field that declares a `Suggest`, says the list has to ask the service — a bucket's keys, a mount table — rather than read something local, and changes when it is asked. It is never called per keystroke and never by shell completion: only when a person presses Tab in a TUI form on a box with nothing left to accept, in your own process, within three seconds, and with the values the call would run with, credentials included and already resolved — the one case where a `Suggest` sees a `Secret`, since the same process receives them a moment later when the call runs. What has been typed in the box is in the request under the field's own name, so `req.String("prefix")` is what to list under, and an entry ending in a separator (`"backups/"`) composes: accepting it stops the box extending, and the next press asks one level deeper. It stays read-only and silent on failure. Registration refuses `Live` without a `Suggest`, beside `Options`, or on anything but a `String`. It is a different switch from `Live` on a capability, which re-runs a `Read` view while it is on screen.
- **`Local: true`** means the value names something on *this* machine, so it is refused over MCP. `--out` is the example: a grant authorises revealing a value, not choosing where on the operator's disk it lands.
- **`Required`** means your handler never runs without the input. The host refuses a call that has no value for it once every layer has had its say — the caller, a profile, the operator's config — on every surface, before your handler, as `core.input.missing`. Not your `Default`: one beside `Required` fills the input before the host looks, so it is never missing, and the plugin fails to load — declare one or the other. An empty one, `""` or an empty list, fails the same way: it fills nothing, and the tool schema would publish it as the value an input on its own required list gets when it is left out. Left out, empty text and an empty list all count as no value, because your accessor reads each of them exactly as it reads an input nobody gave. The refusal names the input the way the caller types it: `--host` or `<table>` at a terminal, the argument `"table"` to an agent, the box in a form. So a required input with a `Config` key is satisfied by the file, and a required one that is also `Local` by the operator alone. With neither a `Config` key nor `EnvFallback`, that is a person at a terminal alone: every call over MCP is refused, so the capability is listed to every agent and none can use it — declare it `HumanOnly` to keep it out of the tool list, or give the input a key or a fallback. `rta plugin dev` says `no — needs <input>` in its `Agents` column, and `sdktest` notes it. If an empty value means something to your handler, leave the input optional and decide there.
- **`Positional`** makes the input an argument on the command line rather than a flag. Arguments are taken in the order the usage line shows them: the required ones first, in the order you declare them, then the optional ones — `rta git blame <file> [path]` whichever of the two is declared first. A list argument takes every argument after it, so it has to be the last in that order, and the plugin fails to load otherwise: make the other a flag, or the list the last argument.
- **`Short`** is the one-letter form of the flag at a terminal — `Short: "n"` makes `--limit` also `-n`. Declare it for the few inputs a person types every day and leave the rest long. It is one ASCII letter, never on a positional input, never one the host owns (`h`, `o` and `y`: `--help`, `--output`, `--yes`) and never one of the capability's own twice; the plugin fails to load otherwise. Every other surface ignores it.
- **`Piped`** is for rta's built-ins: `sdk.Serve` refuses a plugin declaring it before it serves, and the host refuses one when it loads it. It marks an input rta's own CLI reads from standard input when the call leaves it out — the token `codec jwt` decodes — and which every other surface therefore requires. Your plugin runs in a process of its own that never sees that pipe, so the marker would be a promise nothing keeps. Declare the input `Required`, or leave it optional and refuse an empty value in your handler.
- **`Remote: true`** marks an input that points the call at another machine — the `server` a queue is parked on. A TUI form opened by acting on one of this machine's own records omits it, and every input declared **`With`** it (`PassphraseField.OnlyWith("server")`), since a box for a remote server on the screen answering the local queue is a box for a different call. `rta explain` says "only with --server" on such an input.
- **`Widens`**, on a `Bool`, names the sibling input the switch reads past: `{Name: "all-namespaces", Type: plugin.Bool, Widens: "namespace"}`. Declare it on every switch whose effect is to ignore the value of another input — `--all-namespaces`, `--all-databases` — because the host cannot read your handler to find that out, and an operator who has pinned the input it widens expects the switch not to undo the pin. A call that sets the switch while a profile fences the input it widens is refused before anything is dialled, whatever the call says about the input itself — see [what a fence holds](../20-using/41-profiles-in-depth.md#what-a-fence-holds-exactly) — and a switch left undeclared is a hole in a boundary the operator believes is closed. It names one input, so a switch that reads past two is declared against the one an operator is likelier to fence. Only a `Bool` may declare it, naming another input of the same capability; the plugin fails to load otherwise, and `rta explain` says "widens --namespace" on the switch.
- **`Path`** inputs are confined to the server's roots on an agent's call, whether the agent sent the path or it came from your `Default` or the operator's config, and your handler receives the path as the host resolved it: the path as it was when judged, which your handler opens by name in its own process, so [the path gate](../95-reference/20-the-path-gate.md) says what that leaves to a caller who can write inside a root. A `Local` one arrives as typed, so a leading `~` is yours to resolve: `plugin.ExpandHome` does it the way the host does, and only for a leading `~` alone or before a `/` — `~alice` and a file called `~notes` are left alone.
- **`Config`** names a key in the operator's configuration this input may be filled from when nobody passed it, so a connection is stated once instead of retyped. Precedence is caller, then config, then `Default`, and your handler cannot tell which it got.

```go
{Name: "host", Type: plugin.String, Required: true, Config: "host"},
{Name: "port", Type: plugin.Int, Default: 5432, Config: "port"},
{Name: "mode", Type: plugin.String, Default: "prefer", Config: "tls.mode"},
```

The operator writes it under your plugin's own section, which is pinned to the binary they installed rather than to the namespace you declare — anything on `$PATH` can claim a namespace, and their stated values must not go to whoever won that race:

```yaml
plugins:
  weather@1a2b3c4d5e6f:
    host: db.internal
```

`rta doctor` prints the exact line, digest included, and says so again if you upgrade and the pin goes stale. **`Config` is refused on a `Secret` or `SecretSlice` input** — configuration is a plaintext file read on every invocation, and a `Secret`'s default is published in your MCP tool schema. Use `Local: true` and let the host resolve it from its own environment.

**Declare it generously.** Anything a person would set once and keep — the scope a call works in (a cluster, a namespace, a bucket, a collection), a limit, a depth, a parallelism, a [TTL](../95-reference/10-glossary.md#acronyms) convention, a storage class, a dump format — wants a key, because every one of them is a thing an operator otherwise retypes on every call and gets wrong on one. The caller always wins, so a configured value is a default, never a lock — except where an operator fences the input in a profile with [`only:`](../20-using/41-profiles-in-depth.md#what-a-fence-holds-exactly), a list the value a call resolves to has to be in. An input that is a list (`StringSlice`), and that a grant does not name as its scope, reads every value the fence allows when a call names none, so a connection fenced to a dozen namespaces is read across all of them and a handler loops over the list it is given; an input that is a single value needs the call to name which of several. Three kinds of input do not get one: a **selector of one record** (a key, a path, a container, a backup name — there is no sensible default), a **cursor** (`--after`, `--offset`), and a **destructive switch** (`--force`, `--overwrite`, `--replace`, `--clean`): a default that skips a safety check, in a file nobody is watching, is exactly the footgun the check exists to prevent.

**The scope is a flag, the record is the argument.** A config-backed input cannot be positional — arguments bind left to right, so a config-filled first argument would change what a typed one means — which is why `--cluster`, `--namespace` and `--bucket` are flags with keys while the pod, the object and the backup stay positional. kubectl draws the same line with `-n`.

**The letters follow each product's own tool.** The first-party plugins give an input a short letter where the tool a person already types has one: kubectl's `-n` for a namespace, psql's `-U`, `-p` and `-d`, mysql's `-u`, `-P` and `-D`. So a letter does not mean one thing across plugins — `-n` is `limit` in most of them and `namespace` in `kube` and `cnpg`, and `-d` is a database in `pg` and a depth in four others — and nothing holds one plugin's letters to another's: `rta <plugin> <capability> --help` says what a letter is for the one in front of you. Every letter the first-party plugins declare, and the input it stands for there:

| Letter | Input | Plugins |
| --- | --- | --- |
| `-A` | `all-namespaces` | `cnpg`, `kube` |
| `-a` | `all` | `docker` |
| `-b` | `bucket` | `s3` |
| `-c` | `clean` | `pg` |
| `-c` | `client` | `keycloak` |
| `-c` | `cluster` | `cnpg` |
| `-c` | `collection` | `qdrant` |
| `-c` | `columns` | `pg` |
| `-D` | `database` | `mariadb`, `mysql` |
| `-d` | `database` | `pg` |
| `-d` | `depth` | `etcd`, `redis`, `s3`, `vault` |
| `-e` | `exact` | `qdrant` |
| `-F` | `format` | `pg` |
| `-f` | `out` | `pg` |
| `-j` | `jobs` | `pg` |
| `-n` | `limit` | `etcd`, `keycloak`, `mariadb`, `mysql`, `pg`, `qdrant`, `redis`, `s3` |
| `-n` | `namespace` | `cnpg`, `kube` |
| `-O` | `no-owner` | `pg` |
| `-P` | `port` | `mariadb`, `mysql` |
| `-p` | `port` | `pg` |
| `-p` | `prefix` | `s3` |
| `-r` | `realm` | `keycloak` |
| `-r` | `recursive` | `s3` |
| `-s` | `schema` | `pg` |
| `-t` | `table` | `mariadb`, `mysql` |
| `-t` | `ttl` | `s3` |
| `-t` | `type` | `keycloak` |
| `-U` | `user` | `pg` |
| `-u` | `user` | `keycloak`, `mariadb`, `mysql` |

## Group the boxes of a form

A service's connection alone is a dozen inputs. `Group` names the block an input belongs to, so the inputs that share a name are shown together under it: a TUI form draws each block under its name on one row, the first row of the block's first box, so the name is on screen when the cursor enters the block. An input in no group is the call's own and comes first, then the blocks a call input names, then the connection. It is a heading and nothing more — it changes nothing about who may supply the input, what a profile fences or what an agent is offered, so a `Secret` is grouped like any other input — and is one to 24 printable ASCII characters, lowercase, with no space at either end. A connection input that declares none is placed by its name and its type, so a plugin released before the field existed reads as grouped: a `Secret` or `SecretSlice`, or `user`, `username`, `password`, `token`, `access-key`, `secret-key`, `client-id` or `client-secret`, is `credentials`; a name holding `tls`, `ssl`, `ca-file`, `cert` or `key-file` is `tls`; `address`, `host`, `port`, `url`, `socket`, `endpoint`, `context` and `region` are `network`; anything else is `target`. A declared group always wins, any other short name is a block of your own, and `rta explain` prints each input's as `group network`. The conventional four:

| Group | What is in it |
| --- | --- |
| `network` | where the service is: address, host, port, url, socket, endpoint, context, region |
| `tls` | how the server is verified: sslmode, ca-file, tls-server-name, certificates and keys |
| `credentials` | who is asking: user, password, token, access and secret keys, client id and secret |
| `target` | what is selected inside the service: database, realm, mount, namespace, db |

## A connection a profile can fill

A plugin that talks to a service has a connection — where it is, and what authenticates to it — and an operator states one once, in a [profile](../20-using/40-profiles.md), instead of retyping it on every call. There is nothing profile-specific for you to write: a profile fills inputs you already declared, by rules rta applies to the declaration, and `rta explain <capability>` shows the config key each fillable input answers to.

- **A `Config` key makes an input fillable.** A profile's `set:` block for your plugin uses the same keys as the `plugins:` section above, so `set: {region: north}` fills the input declared `Config: "region"`, and the caller's own value still wins. A `set:` key no input declares is refused by `rta profile set` as one nothing reads, and `rta explain <capability>` lists the keys that exist.
- **A credential is a `Secret` declared `Local: true, EnvFallback: true`.** `Config` is refused on a `Secret`, so the operator maps it instead: `secrets: {token: kv:staging-token}` in the profile is a reference rta resolves at call time from the store (or `kube:` for a cluster's Secret) and hands you as the input's value, never written to a file. The default connection of a profile also reads `RTA_PROFILE_<PROFILE>_<INPUT>` for any input it can fill; a labeled instance reads only its `set:` and `secrets:`. You see a value, and cannot tell which source it came from.
- **A TUI form asks the connection last, in blocks.** The inputs that are `Local` and have a `Config` key, with the `Local` credentials a profile fills beside them, come after the capability's own inputs, wherever you declared them, each block under the name of its [group](#group-the-boxes-of-a-form); the call's own inputs keep the order you gave them, so put the one a caller needs first. A capability with no `Local` input that has a `Config` key has no connection to set apart, and its inputs are asked in declaration order.
- **Never a `Path`, and never the input a grant is scoped by** — your `Scope` and `ScopeAlso` inputs. A profile chooses where a call goes, not which file it reads or writes, and not the record a grant was checked against.
- **A service reached over the network can have a forward stand in for its address.** A profile may state `kube:` or `ssh:` for a connection, and rta opens a `kubectl port-forward` or an `ssh -L` for the call, hands your plugin a local address, and closes it afterwards; you never learn a tunnel was there. To receive that address, say which inputs hold it: `Endpoint: plugin.EndpointHost` together with `plugin.EndpointPort` (an `Int`), or `plugin.EndpointAddress` for one input holding `host:port`, or `plugin.EndpointURL` for one holding a URL. Name each for its role — `host` and `port`, `address`, `url` — as every first-party plugin does, so a profile's `set:` reads the same whichever plugin it fills. Each such input has to be `Local` — an address an agent could choose is a credential carried wherever it likes — and has a `Config` key, at most one input takes each role, and registration refuses a plugin that declares half an address. A plugin with no endpoint input cannot be given a forward, and a profile that states one for it is refused rather than opened and ignored. The same declaration is what gives a capability's command the flags that state a connection for one call — `--kube`, `--secret` and `--secrets-from` — so a plugin does nothing to offer them, a capability with no endpoint input does not have them, and they are on no tool an agent is offered: an input of your own may not take those names, since the host's flag would shadow it. The same holds for `--no-profile`, which the host adds wherever it adds `--profile`: the flag that leaves the environment that is switched on for one call, and on no tool an agent is offered.
- **A forward is a raw byte pipe, so [TLS](../95-reference/10-glossary.md#acronyms) over it is the host's question.** Mark the input that holds your TLS switch `plugin.EndpointTLS`: a `Bool`, or a `String` whose `Options` include one of `disable`, `off`, `false`, `none` or `insecure`, which is what the host fills in over a forward. A [CA](../95-reference/10-glossary.md#acronyms) file, a client certificate or its key — anything that only matters once TLS is negotiated — is declared `TLSAdjacent: true`, and is refused beside a forward instead of silently doing nothing. [Profiles](../20-using/42-reaching-private-services.md#when-the-far-side-speaks-tls-on-its-own) is what the operator reads about this, and what to name in a hint.

A profile can only name a plugin that is installed — `rta profile set` refuses one that is not registered — so `rta plugin dev` does not apply one. To try one on the plugin you are writing, build it onto `$PATH`, approve it with `rta plugin trust <name>`, and state the profile with `rta profile set dev --plugin <name> --set region=north`; `rta <name> <capability> --profile dev` then runs through it, and `rta profile show dev` says where each value came from.

## Next

[Answers, errors and hints](./22-answers-errors-and-hints.md) — what a handler returns beyond the basics: hints in the reader's own words, warnings, and the one error that is a refusal.
