# Rule Them All

> ⚠️ **Early development** — M0 and M1 are done, M2 (the third-party plugin SDK) is well underway (see Status below). APIs change freely until v1.0. No packaged release yet — build from source.

`rta` is a single, extendable binary that gives sysadmins, developers and DevOps/SRE engineers one consistent interface over the tools they juggle daily — databases, object storage, secrets, networking, certificates, host telemetry, HTTP APIs.

**One capability model, rendered four ways.** Write a capability once; get a scriptable CLI command, an interactive TUI view, an MCP tool for AI agents, and (later) a web panel — for free.

See [PROJECT.md](./PROJECT.md) for the full blueprint (architecture, plugin system, AI strategy, roadmap).

## Status

| Milestone | State | Scope |
| --------- | ----- | ----- |
| M0 — Skeleton | ✅ | Capability model, View + Error contract, CLI/JSON/YAML/CSV renderers, exit codes, `sys` built-in |
| M1 — Four surfaces | ✅ | MCP server (`rta mcp serve` / `install claude`) · **15 built-in plugins, 90 capabilities** (`rta plugin list`): `sys`, `net`, `http`, `cert`, `fs`, `kv`, `todo`, `note`, `gen`, `codec`, `audit`, `grant`, `debug`, `keys`, `git` · `rta explain` / `plugin list` / `doctor` / `init` · declared input **completion** — `Options` becomes a TUI picker, shell completion and an MCP schema `enum`; `Suggest` completes from what exists (your tags, your keys, your hosts file) on human surfaces only; `Path` inputs complete filesystem paths — the shell's own on the CLI, live directory-by-directory completion in TUI forms · TUI shell: live dashboard landing (full-width search bar with on-the-fly matches over every capability, scrolling three at a time; one tile per plugin that has something to show at a glance, scrollable grid, tile actions; `[`/`]` reorder, `H` hides and `p` opens the plugin inventory where any tile comes back), a capability catalogue rendered as a table grouped by plugin — one row each, columns for ID, permission and summary, filter intact, every pane bounded by the terminal and scrollable inside it (forms included, `ctrl+e` opens `$EDITOR` on a body, mouse wheel scrolls, `esc` leaves a slow run), actionable views (navigate rows; enter show / e edit prefilled / d done / x remove — from a list *and* from a record's own page, auto-refreshing), browse/filter, input forms, destructive confirm, re-run, copy-as-JSON · unified theme, semantic status colors, charts (`sys cpu --cores`, `net ping --graph`), glamour markdown · `todo`/`note`: tags, due dates, sub-tasks, `#N` cross-references, full-text search, tag counts, `todo reopen` (the undo for `done`) — detail pages composed of metadata + prose + relations rather than one markdown blob · `kv`: encrypted local store for secrets, certificates and key files (`age`-backed; passphrase **or** keys — `kv init --generate` makes a dedicated age key and then nothing needs a flag, `kv rekey` changes the lock afterwards — add a key, switch to one, or take a reader away, always refusing to lock you out — a passphrase-protected SSH key is asked for its passphrase the way `ssh` asks (ssh-agent cannot help: an agent signs and never decrypts), per-entry kind and description, `kv get --out` back to disk at 0600, `kv env` for `eval "$(rta kv env …)"`, and `kv status` — where the store is and what can open it, without unlocking it) · `grant`: time-boxed, per-record permissions for AI agents, enforced across every plugin · `sys overview` grouped host health (the default system tile; `--detail` composes the full report from sys.host/cpu/mem/load/disk/temp/ps) · `gen`: passwords, real key material, tokens and UUIDs — `gen overview` shows every shape side by side with the entropy it carries and the command that reproduces it · composite `Sections` views — detail pages are assembled from other capabilities' views through `plugin.Page`, not rebuilt · `net info` local overview (never phones home) · `audit`, its own plugin rather than a corner of `net`: `audit web <host>` (TLS, headers, cookies, exposure), `audit mail <domain>` (can somebody send mail as you? SPF/DKIM/DMARC/MTA-STS), `audit deps` (what you already declare, checked against OSV — each hit saying whether you asked for that package or something else pulled it in), `audit why <package>` (the whole route, drawn from the lockfile) — graded, and every finding cites the OWASP Top 10 category and CWE it comes from · `fs usage` / `tree` / `hash` — what is using space, what is here, what is this file · `codec` base64/hex/URL/JWT · `git status` / `log` / `diff` / `branches` / `blame` — a repository's state as structured views instead of parsed porcelain, read-only, against a local checkout or a remote URL cloned in memory; `git config` (system/global/local, layered) and `git hooks` (which scripts would actually fire, not just what's on disk) round out the inspection; `git overview` for the tile · `net trace` unprivileged traceroute · `net probe` telnet-style banner/TLS inspection, with `net send` as its gated write half (ADR 0009) · `net dns` with `--type auto` and `--server` · `net hosts add/toggle/rm` and `net resolver set` — editing the system files in place, preserving everything else byte for byte, backing up first, and refusing a machine-generated resolv.conf · optional config (`rta init`, `RTA_OUTPUT`, dashboard tiles) |
| M2 — Plugin SDK | ⏳ | `proto/v1` frozen · gRPC plugin host · **plugin trust** — a binary on your `$PATH` is not consent, and rta loads a plugin by *running* it, so an artifact executes only once `rta plugin trust <name>` has approved that exact digest; a rebuild needs approving again · `pkg/sdk` and a public `sdktest` conformance suite · `rta plugin new` / `dev` · `examples/plugin-hello` · four external plugins proving the contract: `pg` (connection health, schema, rows, activity), `eol` (endoflife.date checks), `vault` (kv secrets, tokens, leases, seal status, policies, transit encryption, response-wrapping), `s3` (buckets, objects, presigned URLs, bucket policy — AWS/MinIO/R2/Ceph) |

### AI agents (MCP)

```bash
rta mcp install claude    # one-liner registration in Claude Code
rta mcp serve             # or point any MCP client at this (stdio)
```

**Writing a plugin:** `rta plugin new <name>` scaffolds one that builds, passes its conformance suite and runs; `rta plugin dev` compiles and loads it through the identical spawn path (sandbox included) without installing anything. See [docs/writing-a-plugin.md](docs/writing-a-plugin.md).

Read-only capabilities are exposed by default; `--allow-write <plugins>` and `--allow-destructive <ids>` opt in more, enforced host-side — writes are opened one namespace at a time rather than registry-wide, and a destructive capability from an external plugin is pinned to that binary's digest, so an authorisation attaches to an artifact rather than to a name a replacement would inherit. Safety classes map to MCP tool annotations, results are typed JSON view envelopes, and errors carry stable codes + actionable hints.

Those switches are decided once, when the server starts, for every call it will ever make. **Grants** are the other half: consent for one capability, optionally one record, expiring on its own.

```bash
rta grant allow kv.get db-password --ttl 15m   # one key, 15 minutes
rta grant allow todo.rm                        # any task, default 15m
rta grant list                                 # what can the agent do right now?
rta grant revoke kv                            # …or --all
```

Anything **destructive** needs one implicitly, and a capability can opt in when its safety class understates it: `kv get` is classified a **write** though it changes nothing — the model is about blast radius, and revealing a secret has blast radius. Grants expire (15m by default, 24h maximum), can only be issued from a human surface (an agent asking for one is refused), and are enforced in the MCP bridge rather than by each plugin — so `--allow-write` never silently means "and delete whatever you like".

One thing grants cannot do: protect a store whose key is already in reach. An MCP server inherits the environment it was launched from, and an identity file on disk can be read by any agent with a file-reading tool — which decrypts the store without ever calling `kv get`. Strongest first: **give the server no key material at all** (the right default — `kv get` then fails whatever grants exist), passphrase in its environment, identity file on disk. Never the SSH key you log into servers with. A passphrase-protected key sits between the last two: it is on disk, but nothing unattended can use it — `rta doctor` says which of these is currently true.

### Environments

A **profile** is one environment across every plugin that has something in it — its database, its bucket, its vault — under one name.

```yaml
profiles:
  proj1-staging:
    note: project one, staging
    ttl: 8h                                   # every switch to it lasts this long
    plugins:
      pg@1b6093cf90ce:
        set: {host: staging.db.internal, database: app}
        secrets: {password: kv:proj1-staging-db}   # a reference; never a value
      s3@7f12b8cbdf29:
        set: {endpoint: minio.staging:9000}
```

```bash
rta use proj1-staging          # pg, s3 and vault all point at staging — no flags after this
rta use --for 30m              # …or give this switch its own deadline
rta use                        # what am I in, and for how long?
rta use --off                  # back to the base configuration
```

The same name is what an agent's permission is granted against, so one command covers the whole environment and renewing is one more:

```bash
rta grant allow pg --profile proj1-staging --ttl 1h
rta grant renew                # moves the deadline, nothing else
```

**Naming a profile is itself what requires consent** — `rta pg status` against your own localhost needs none, and the same call against staging needs a grant, because that is the moment reach leaves the machine.

**And consent is to the connection, not to its name.** A grant records a fingerprint of the connection you were looking at when you allowed it, so editing that environment afterwards — a different `host`, a different `secrets:` mapping — stops it covering calls on that name instead of quietly following the name wherever it now points. `rta grant list` marks such a grant `(changed)` and `rta doctor` says what to do: `rta grant allow` again, which is a fresh decision. `rta grant renew` moves the deadline and deliberately will not adopt a connection you have not seen.

**And switching bounds agents.** While an environment is switched on, `rta mcp serve` refuses every *other* profile whatever grants exist — so moving to a different project takes the last one away from every agent, with nothing revoked and nothing to re-issue when you move back. It can only ever subtract: the agent still names the profile in the call, and a grant is still what permits it.

`rta use --off` is the *absence* of that bound, not a stronger one: it goes back to the base configuration, and grants alone decide again. To take reach away, take it away — `rta grant revoke --all`.

`rta profile list` / `show <name>` says what you have and whether each one is usable; `f` in the TUI is the same thing with `u` to switch, `s` to attach a credential and a header badge that says where you are. A profile is only honoured from a config path you named (`$RTA_CONFIG` or the user config dir), never from a `./.rta.yaml` a repository could ship.

## Build & try

```bash
go install ./cmd/rta           # puts `rta` on $PATH (needs $GOBIN/$GOPATH/bin on it)
# or: make build                → ./rta, if you'd rather not touch $PATH yet

source <(rta completion zsh)   # or bash/fish — completion is generated from
                               # the capabilities themselves, nothing to regenerate

rta sys host                   # pretty output for humans
rta sys mem -o json            # structured output for pipes
rta sys disk -o md             # markdown, for a report that leaves the terminal
rta sys ps --limit 5           # flags per capability
rta                            # bare, on a TTY: the interactive shell
```

(Building with `make build` instead of installing? Use `./rta` in place of `rta` throughout — everything below reads the same either way.)

Every capability works in a pipe (`-o json|yaml|csv|md` — `md` is how a report gets out of the terminal and into an issue) and follows one grammar:

```
rta <namespace> [<noun>] <verb> [args] [flags]
```

### Exit codes

| Code | Meaning |
| ---- | ------- |
| 0 | success |
| 1 | capability error (structured: code, message, hint) |
| 2 | usage error |
| 3 | confirmation required/declined (`--yes` to confirm, `--dry-run` to preview) |

## Development

```bash
make build     # build ./rta
make install   # go install ./cmd/rta — puts rta on $PATH
make test      # run all tests
make hard      # -count=1 -race -shuffle=on: no cache, no ordering luck, no races
make check     # vet + hard
make ci        # everything the pipeline runs: vet, hard, coverage, gofmt
make coverage  # go test -coverpkg=./... — most of internal/ is exercised
               #   from other packages' tests, so plain per-package coverage
               #   understates it
```

- Architecture and decisions: [PROJECT.md](./PROJECT.md) · [docs/adr/](./docs/adr/)
- Layout: public plugin contract in [pkg/](./pkg/), host internals in [internal/](./internal/), built-in plugins in [builtin/](./builtin/)

## License

Apache-2.0 (see [PROJECT.md](./PROJECT.md) §12 D6; `NOTICE` will track the MPL-2.0 `go-plugin` dependency once the plugin host lands).
