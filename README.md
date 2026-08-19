# Rule Them All

> ⚠️ **Early development** — M0 and M1 are done (see Status below); M2 (the third-party plugin SDK) has not started. APIs change freely until v1.0. No packaged release yet — build from source.

`rta` is a single, extendable binary that gives sysadmins, developers and DevOps/SRE engineers one consistent interface over the tools they juggle daily — databases, object storage, secrets, networking, certificates, host telemetry, HTTP APIs.

**One capability model, rendered four ways.** Write a capability once; get a scriptable CLI command, an interactive TUI view, an MCP tool for AI agents, and (later) a web panel — for free.

See [PROJECT.md](./PROJECT.md) for the full blueprint (architecture, plugin system, AI strategy, roadmap).

## Status

| Milestone | State | Scope |
| --------- | ----- | ----- |
| M0 — Skeleton | ✅ | Capability model, View + Error contract, CLI/JSON/YAML/CSV renderers, exit codes, `sys` built-in |
| M1 — Four surfaces | ✅ | MCP server (`rta mcp serve` / `install claude`) · `cert`, `net`, `http`, `todo`, `note`, `kv` built-ins · `rta explain` / `plugins` / `doctor` / `init` · declared input **completion** — `Options` becomes a TUI picker, shell completion and an MCP schema `enum`; `Suggest` completes from what exists (your tags, your keys, your hosts file) on human surfaces only; `Path` inputs complete filesystem paths — the shell's own on the CLI, live directory-by-directory completion in TUI forms · TUI shell: live dashboard landing (full-width search bar with on-the-fly matches over every capability, scrolling three at a time; one tile per plugin that has something to show at a glance, scrollable grid, tile actions; `[`/`]` reorder, `H` hides and `p` opens the plugin inventory where any tile comes back), a capability catalogue rendered as a table grouped by plugin — one row each, columns for ID, permission and summary, filter intact, every pane bounded by the terminal and scrollable inside it (forms included, `ctrl+e` opens `$EDITOR` on a body, mouse wheel scrolls, `esc` leaves a slow run), actionable views (navigate rows; enter show / e edit prefilled / d done / x remove — from a list *and* from a record's own page, auto-refreshing), browse/filter, input forms, destructive confirm, re-run, copy-as-JSON · unified theme, semantic status colors, charts (`sys cpu --cores`, `net ping --graph`), glamour markdown · `todo`/`note`: tags, due dates, sub-tasks, `#N` cross-references, full-text search, tag counts, `todo reopen` (the undo for `done`) — detail pages composed of metadata + prose + relations rather than one markdown blob · `kv`: encrypted local store for secrets, certificates and key files (`age`-backed; passphrase **or** keys — `kv init --generate` makes a dedicated age key and then nothing needs a flag, `kv rekey` changes the lock afterwards — add a key, switch to one, or take a reader away, always refusing to lock you out — a passphrase-protected SSH key is asked for its passphrase the way `ssh` asks (ssh-agent cannot help: an agent signs and never decrypts), per-entry kind and description, `kv get --out` back to disk at 0600, `kv env` for `eval "$(rta kv env …)"`, and `kv status` — where the store is and what can open it, without unlocking it) · `grant`: time-boxed, per-record permissions for AI agents, enforced across every plugin · `sys overview` grouped host health (the default system tile; `--detail` composes the full report from sys.host/cpu/mem/load/disk/temp/ps) · `gen`: passwords, real key material, tokens and UUIDs — `gen overview` shows every shape side by side with the entropy it carries and the command that reproduces it · composite `Sections` views — detail pages are assembled from other capabilities' views through `plugin.Page`, not rebuilt · `net info` local overview (never phones home) · `audit web <host>` graded, OWASP/CWE-cited hardening audit (its own plugin, not a corner of `net`) · `net trace` unprivileged traceroute · `net probe` telnet-style banner/TLS inspection · `net dns` with `--type auto` and `--server` · `net hosts add/toggle/rm` and `net resolver set` — editing the system files in place, preserving everything else byte for byte, backing up first, and refusing a machine-generated resolv.conf · optional config (`rta init`, `RTA_OUTPUT`, dashboard tiles) |
| M2 — Plugin SDK | ⏳ | gRPC plugin host, `pkg/sdk`, conformance suite, `pg` plugin |

### AI agents (MCP)

```bash
rta mcp install claude    # one-liner registration in Claude Code
rta mcp serve             # or point any MCP client at this (stdio)
```

Read-only capabilities are exposed by default; `--allow-write` and `--allow-destructive <ids>` opt in more, enforced host-side. Safety classes map to MCP tool annotations, results are typed JSON view envelopes, and errors carry stable codes + actionable hints.

Those switches are decided once, when the server starts, for every call it will ever make. **Grants** are the other half: consent for one capability, optionally one record, expiring on its own.

```bash
rta grant allow kv.get db-password --ttl 15m   # one key, 15 minutes
rta grant allow todo.rm                        # any task, default 15m
rta grant list                                 # what can the agent do right now?
rta grant revoke kv                            # …or --all
```

Anything **destructive** needs one implicitly, and a capability can opt in when its safety class understates it: `kv get` is classified a **write** though it changes nothing — the model is about blast radius, and revealing a secret has blast radius. Grants expire (15m by default, 24h maximum), can only be issued from a human surface (an agent asking for one is refused), and are enforced in the MCP bridge rather than by each plugin — so `--allow-write` never silently means "and delete whatever you like".

One thing grants cannot do: protect a store whose key is already in reach. An MCP server inherits the environment it was launched from, and an identity file on disk can be read by any agent with a file-reading tool — which decrypts the store without ever calling `kv get`. Strongest first: **give the server no key material at all** (the right default — `kv get` then fails whatever grants exist), passphrase in its environment, identity file on disk. Never the SSH key you log into servers with. A passphrase-protected key sits between the last two: it is on disk, but nothing unattended can use it — `rta doctor` says which of these is currently true.

## Build & try

```bash
go install ./cmd/rta           # puts `rta` on $PATH (needs $GOBIN/$GOPATH/bin on it)
# or: make build                → ./rta, if you'd rather not touch $PATH yet

source <(rta completion zsh)   # or bash/fish — completion is generated from
                               # the capabilities themselves, nothing to regenerate

rta sys host                   # pretty output for humans
rta sys mem -o json            # structured output for pipes
rta sys disk                   # styled table
rta sys ps --limit 5           # flags per capability
```

(Building with `make build` instead of installing? Use `./rta` in place of `rta` throughout — everything below reads the same either way.)

Every capability works in a pipe (`-o json|yaml|csv`) and follows one grammar:

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
make hard      # -count=1 -race -shuffle=on — what CI runs
make check     # vet + hard
make coverage  # go test -coverpkg=./... — most of internal/ is exercised
               #   from other packages' tests, so plain per-package coverage
               #   understates it
```

- Architecture and decisions: [PROJECT.md](./PROJECT.md) · [docs/adr/](./docs/adr/)
- Layout: public plugin contract in [pkg/](./pkg/), host internals in [internal/](./internal/), built-in plugins in [builtin/](./builtin/)

## License

Apache-2.0 (see [PROJECT.md](./PROJECT.md) §12 D6; `NOTICE` will track the MPL-2.0 `go-plugin` dependency once the plugin host lands).
