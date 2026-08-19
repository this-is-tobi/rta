# Rule Them All — Project Blueprint

> Working document. Status: **draft v0.4** · Research date: **2026-08-17**
> All version numbers were verified against the Go module proxy and GitHub API on that date.
> v0.2: added DX section (§5), upgraded AI strategy (§6), converted open questions into decisions (§12), tightened for KISS.
> v0.3: mined `this-is-tobi/tools` + `dotfiles` for plugin candidates (§7.4); promoted **tunnels** to a profile-level concept (§4.6, D10).
> v0.4 (2026-08-18): M0 and M1 shipped — `sys`/`cert`/`net`/`http`/`todo`/`note`/`kv`/`grant` built-ins, all four surfaces, `rta doctor`/`explain`/`plugins`/`init`. Ahead of the M1 scope as originally written: the MCP trust boundary (`Surface`, `Field.Local`, time-boxed grants — D11, ADR 0005), declared-input completion (`Options`/`Suggest`/`Path` — shell completion, TUI pickers, MCP schema enums), and `Sections` as the one fixed composite view (§4.3, ADR 0001 update). M2 (plugin SDK) not started.
> v0.5 (2026-08-18): a design pass ahead of M2 — six candidate additions investigated (multi-agent research + adversarial review) before any of them were built, in keeping with §1.2's "decisions are made, not kept open." Two shipped immediately as Wave 1 built-ins since neither needs the plugin SDK: `gen` (password/token/UUID) and `codec` (base64/hex/URL/JWT, the plugin already designed in §7.5). `internal/grant` gained `MaxUses`/`Uses` (D12) — a grant can now expire after N successful calls, not only after a TTL. `kube` and `docker` are designed (§7, Wave 2) but not built. One-shot/shared secret access split into three separable answers (D13, ADR 0006): the grant extension above for AI-agent-facing "read this once", `vault.wrap.*` riding the already-planned `vault` plugin for same-infra sharing, and a new `share` plugin (Wave 3, deliberately late) for arbitrary-recipient transfer — a hosted relay RTA itself would operate was considered and rejected outright. An "AI agent management" plugin was investigated and explicitly not designed further — see §12, "still genuinely open". Then a hardening/UX pass driven by real use: `net.audit` crashed on a CSP ending in `;` (one of the most ordinary inputs there is) and became its own `audit` plugin in the fixing — `audit web <host>`, since it happens to speak HTTP but answers "is this safe to expose" (D16, ADR 0008). `plugin.Page` now composes every detail page from the views sibling capabilities already return, so the four decisions each one used to make independently (which inputs an embedded call inherits, whether a failing section sinks the page, whether `detail` leaks into the parts) are answered once. `gen.overview`, `kv.status --detail` and `audit.web --detail` are the first pages built on it. And an upgrade-shaped bug found in a real store: every `kv` store written before `entry.Value` became `[]byte` had stopped opening — decryptable throughout, refused at the JSON parse. Both formats read now, and the format is versioned so the guess is never needed again.

---

## 1. Vision

**Rule Them All** (`rta`) is a single, extendable binary that gives sysadmins, developers and DevOps/SRE engineers one consistent interface over the dozen tools they otherwise juggle daily — databases, object storage, secrets, service discovery, networking, certificates, host telemetry, HTTP APIs.

The pitch is not "a bag of subcommands". It is:

> **One capability model, rendered four ways.** Write a capability once; get a scriptable CLI command, an interactive TUI view, an MCP tool for AI agents, and (later) a web panel — for free.

That single idea is the project's differentiator and it drives every architectural decision below.

### 1.1 Principles

| #   | Principle                             | Consequence                                                                                                                                          |
| --- | ------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| P1  | **DX is the product**                 | Two audiences, two SLAs: an end user gets value in **< 60 s** from install; a plugin author ships a working plugin in **< 15 min** (§5).             |
| P2  | **Scriptable first, pretty second**   | Every capability works in a pipe with `--output json`. TUI is a *renderer*, never the only path.                                                     |
| P3  | **The core renders, plugins compute** | Plugins return typed *data views*, not ANSI. Guarantees a consistent look and lets us restyle everything at once.                                    |
| P4  | **AI-native, not AI-bolted**          | The capability registry *is* the MCP surface. Safety classes become MCP annotations; interactive flows become elicitation. No parallel AI codepath.  |
| P5  | **Safe by default**                   | Capabilities declare `read` / `write` / `destructive`. Mutating ops need explicit confirmation or `--yes`. AI exposure is read-only unless opted in. |
| P6  | **No second-class plugins**           | Built-in features implement the *same* interface as external plugins. If the built-ins feel better than plugins, the plugin API is wrong.            |
| P7  | **Boring distribution**               | Static binaries, one Docker image, no runtime dependency, no daemon, **no telemetry, no background network calls**.                                  |
| P8  | **Own your escape hatch**             | A `rta-*` binary on `$PATH` always works, with zero SDK, in any language.                                                                            |

### 1.2 What KISS means here

KISS is not "few features" — it is **few concepts**. The whole system is three nouns (`Plugin → Capability → View`) and everything else is a renderer or a service around them. Concretely:

- One plugin mechanism carries the weight (gRPC subprocess); the others are either free (`$PATH`) or deferred (WASM).
- One config format (YAML), one layering rule, one secret-reference syntax.
- One verb vocabulary across all plugins (§5.3) — learn `rta pg` and you already know `rta s3`.
- Decisions are made (§12), not kept open. An open question is complexity on layaway.

### 1.3 Non-goals

- Not a replacement for `psql`, `kubectl`, `vault` — it is a *fast path* for the 80% of common operations.
- Not a monitoring/alerting system (no agent, no daemon, no time-series storage).
- Not a config-management or orchestration tool.
- Not a general-purpose scripting runtime. (Explicitly: **no embedded Lua/Starlark plugin tier** — see §4.4.)
- **No telemetry, ever.** No phone-home, no update pings. Version checks happen only on explicit `rta plugin update` / `rta upgrade`.

---

## 2. Prior art & positioning

| Tool                             | What it does                                                                                               | What we take / avoid                                                                                  |
| -------------------------------- | ---------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `kubectl` plugins / `krew`       | `kubectl-<name>` binaries on `$PATH`                                                                       | ✅ Take the discovery convention as our exec-tier escape hatch. ❌ Zero UI integration.                 |
| `gh` extensions                  | `gh-<name>` binaries + `~/.local/share/gh/extensions`, script *and* binary kinds, background update checks | ✅ Take the managed install dir. ❌ Not the background update checks (P7).                              |
| **Steampipe**                    | Plugins are separate binaries over **hashicorp/go-plugin gRPC**, distributed as **OCI artifacts**          | ✅ Closest structural match to what we want. Take both the transport *and* the OCI distribution model. |
| Terraform providers              | go-plugin gRPC, versioned protocol, registry, **`dev_overrides` for local development**                    | ✅ Proof the model scales. ✅ Take `dev_overrides` as our `rta plugin dev` (§5.4).                      |
| `k9s`                            | Great TUI, but "plugins" are just shell-outs defined in YAML                                               | ❌ Too weak — no typed data, no reuse.                                                                 |
| `crush` (Charm)                  | Consumes **MCP** (stdio/http/sse) + LSP for extensibility; permissions model with allow/deny/`--yolo`      | ✅ Take the permissions model wholesale. ✅ Validates MCP as the AI integration surface.                |
| DevToys / "ops-tools" style SAKs | Monolithic grab-bags                                                                                       | ❌ No extension story; they stop growing when the maintainer stops.                                    |

**Gap we fill:** nobody currently offers a Charm-quality TUI *and* a typed third-party plugin API *and* first-class AI-agent exposure in one static binary. Steampipe has the plugin architecture but is SQL-shaped and has no TUI; k9s has the TUI but no real plugins; crush has the AI but is a coding agent, not an ops tool.

---

## 3. Charmbracelet ecosystem audit

Full org scan (56 repos) performed 2026-08-17. **Headline: the Charm v2 wave has landed.** Building on v2 now is the right call — these are fresh, tagged, stable releases, not betas.

### 3.1 Build on these (maintained, tagged, recent)

> **Import-path note (found during M0):** all Charm v2 modules live under the **`charm.land/*` vanity path** (`charm.land/bubbletea/v2`, `charm.land/fang/v2`, `charm.land/lipgloss/v2`, …) — the `github.com/charmbracelet/*/v2` paths do not resolve as modules.

| Repo           | Version                 | Use in RTA                                                                                                                                       |
| -------------- | ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| `bubbletea/v2` | **v2.0.8** (2026-07-03) | TUI runtime. Core of the interactive renderer.                                                                                                   |
| `lipgloss/v2`  | **v2.0.6** (2026-08-11) | Styling/layout. Single source of truth for the theme.                                                                                            |
| `bubbles/v2`   | **v2.1.1** (2026-07-04) | table, list, viewport, spinner, textinput, paginator.                                                                                            |
| `fang/v2`      | **v2.0.1** (2026-03-11) | **Cobra wrapper** — styled help/errors, `--version` from build info, manpages (mango), shell completions, theming. Removes ~all CLI boilerplate. |
| `huh/v2`       | **v2.0.3** (2026-03-10) | Forms/prompts. Backs the `Form` view type (§4.3) and connection wizards.                                                                         |
| `glamour/v2`   | **v2.0.1** (2026-06-12) | Markdown rendering — `Text{markdown:true}` views, `--help` extended docs.                                                                        |
| `log/v2`       | **v2.0.0** (2026-03-09) | Structured logging, human + JSON handlers.                                                                                                       |
| `ultraviolet`  | untagged (2026-08-12)   | Low-level TUI primitives under bubbletea v2. **Do not import directly yet** — no semver tag.                                                     |
| `x/*`          | rolling                 | See §3.2 — several of these are load-bearing for us.                                                                                             |
| `fantasy`      | **v0.41.1** (2026-08-12), Apache-2.0 | Multi-provider Go agent framework (powers crush). Tools + streaming. Our inbound-AI engine — **wrapped** (§6.3).                    |
| `catwalk`      | **v0.51.27** (2026-08-14) | Community-curated LLM provider/model catalogue. Free model list, no hardcoding.                                                                |
| `vhs`          | v0.11.0                 | **Not a dependency — a doc tool.** Scripted terminal GIFs for README and per-plugin demos, wired into CI via `vhs-action`.                       |
| `freeze`       | v0.2.2                  | Render output to PNG/SVG. Powers a future `--export png`.                                                                                        |
| `wish/v2`      | v2.0.3                  | Serve the TUI over SSH. Very strong fit for a bastion/jumphost story (see roadmap M5).                                                           |

### 3.2 `charmbracelet/x` packages worth knowing

These are the quiet gems and two of them change the architecture:

- **`x/pony`** — declarative XML+Go-template TUI markup, renders via Ultraviolet. ⚠️ Self-described *"EXPERIMENTAL, primarily AI-generated"*. **Do not depend on it.** But it independently validates our View-contract idea (§4.3) — worth re-reading its element vocabulary (`vstack`/`hstack`/`box`/`flex`/`scrollview`/`badge`/`progressview`) when we design ours.
- **`x/vt`** — virtual terminal emulator. This is the answer to *"what if a plugin wants its own full custom TUI?"* → run it in a PTY, composite the emulated screen into a host pane. Keeps exec-tier plugins from being visually second-class. (See §4.5.)
- `x/mosaic` — image → terminal rendering. Charts/graphs fallback, QR codes, cert fingerprint viz.
- `x/exp/teatest` — golden-file testing for Bubble Tea programs. **Our TUI test harness.**
- `x/exp/charmtone` — Charm's palette. Base for the default theme.
- `x/vcr` — HTTP record/replay. **Our test harness for the S3/Vault/API plugins.**
- `x/editor` — open `$EDITOR`. Needed for "edit this row / edit this secret" flows.
- `x/ansi`, `x/term`, `x/xpty`, `x/input` — low-level, pulled in transitively.

### 3.3 Archived / stale — rewrite & absorb candidates

| Repo              | State                                                | Verdict                                                                                                                                                                                                                                                                                                          |
| ----------------- | ---------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **`mods`**        | **ARCHIVED 2026-03-09** (last v1.8.1, 2025-07-10), 4.5k ⭐ | 🔥 **Best rewrite candidate.** "AI on the command line" — pipe-friendly LLM (`cat err.log \| mods "explain"`). Charm killed it in favour of crush, but crush is an *interactive coding agent* — the Unix-pipe use case is orthogonal and now orphaned. Reborn as our `ai` plugin (§6.3) on `fantasy` + `catwalk`. |
| **`charm`**       | **ARCHIVED** (last v0.12.6, **2023-07-26**), 2.5k ⭐  | ⚠️ **Ambitious rewrite candidate.** SSH-identity accounts, encrypted KV + FS sync — exactly the shape of "sync my `rta` profiles across machines". High value, high effort. Park for M5; do **not** let it block v1.                                                                                               |
| `skate`           | v1.0.1 (2025-03-06), 1.8k ⭐                          | ⚠️ **Orphaned by dependency** — KV store built on the archived `charm`. Either a cheap `kv` plugin for us, or a warning sign. Verify its `go.mod` before touching.                                                                                                                                                 |
| `melt`            | v0.6.2 (2024-08-15)                                  | ✅ Small, self-contained, genuinely useful: Ed25519 SSH key backup/restore via seed words. Ideal absorbed feature for a `keys` plugin. Low effort, high delight.                                                                                                                                                   |
| `sequin`          | v0.3.1 (2025-01-27)                                  | ✅ Human-readable ANSI sequence explainer. Nice `rta debug ansi` capability; also useful to *us* while debugging the renderer.                                                                                                                                                                                     |
| `wishlist`        | v0.15.2 (2025-06-23)                                 | ✅ SSH directory/launcher. Fits an `ssh` plugin, and pairs with the `wish` bastion story.                                                                                                                                                                                                                          |
| `harmonica`       | v0.2.0 (**2022-04-15**) but pushed 2026-08-13        | ✅ **Not dead — done.** Physics animations, stable API. Use as-is.                                                                                                                                                                                                                                                 |
| `inspo`, `wizard-tutorial` | archived / dead                             | ❌ Ignore.                                                                                                                                                                                                                                                                                                         |

> **Strategic caution:** Charm archives things (`mods`, `charm`, `inspo`). Depend on the *libraries* (bubbletea, lipgloss, bubbles, fang, huh) — those are load-bearing for Charm's own commercial products and safe. Treat `fantasy`/`catwalk` (both pre-1.0, fast-moving) as **wrapped behind our own interface** so they are swappable.

---

## 4. Architecture

### 4.1 The core model

Three concepts, and only three:

```
Plugin  ──has many──▶  Capability  ──returns──▶  View
```

- **Plugin** — a unit of distribution + a namespace. Declares metadata, config schema, and its capabilities.
- **Capability** — one operation. Has a stable ID (`pg.table.list`), a **JSON-Schema'd input**, a **safety class** (`read` | `write` | `destructive`) plus an `idempotent` flag, and a handler. *This is the atom of the system.*
- **View** — the typed, renderer-agnostic result. Never contains ANSI, never contains layout.

Everything else is a renderer over that model:

```
                            ┌──────────────────────┐
                            │  Capability Registry │
                            │  (built-ins + plugins)│
                            └──────────┬───────────┘
                                       │  View
        ┌──────────────┬───────────────┼───────────────┬──────────────┐
        ▼              ▼               ▼               ▼              ▼
   CLI renderer   TUI renderer   MCP server      JSON/YAML      Web UI
   (cobra+fang)   (bubbletea)   (go-sdk)         (--output)     (later)
```

**Why this matters:** adding a capability to a plugin makes it simultaneously scriptable, browsable, and callable-by-an-AI. No per-surface work. This is the whole design.

Capability IDs are **stable forever**; input schemas evolve **additively only** (new optional fields). A breaking input change is a new capability ID. This keeps scripts, MCP clients and saved TUI state working across upgrades without a deprecation dance.

**Inputs declare what they accept, and every surface honours it.** A field carries two ways of saying what may go in it, and both exist so the answer is an affordance rather than a sentence in the help text:

- **`Options`** — the closed set. Becomes a picker in the TUI form, shell completion on the CLI, and an `enum` in the MCP schema. That last one is the one that pays: a model guessing `PTR` at a field that wants `ptr` currently learns so by failing, and an enum turns a round trip into a schema.
- **`Suggest`** — what exists *right now*: the tags you have used, the keys in your store, the hostnames in your hosts file. Not exhaustive — anything may still be typed — so it helps without constraining. Entries may carry a tab-separated description (`3\tship the release`), which shell completion shows in its own column.

`Suggest` runs on **human surfaces only** (shell completion, the TUI form). It is never offered to an MCP caller, because the list itself is information — the names of your secrets are worth something without their values — and an agent that legitimately needs it can call the capability that returns them and be gated (§4.7.11) accordingly. It is called on a keystroke, so it must be cheap and silent on failure: a completion that cannot answer should slow nobody down. `kv` is the sharp case — resolving a passphrase may *prompt*, and a prompt fired by the tab key would hang a shell mid-command-line, so it checks that the store can be opened from the environment and otherwise offers nothing.

The `grant` plugin shows what this composes into: `rta grant allow <tab>` lists the capabilities that need a grant (derived from the registry, never a second list), and `rta grant allow kv.get <tab>` lists your key names — because `kv.get` declares that its scope is a `key` and how to complete one. Nothing in `grant` knows what a key is.

### 4.2 Plugin tiers — decision matrix

We evaluated four mechanisms. The conclusion is *tiered*, not singular — but one tier carries the weight.

| | **Built-in** | **Plugin (gRPC subprocess)** ⭐ | **WASM** (deferred) | **Exec (`$PATH` binary)** |
|---|---|---|---|---|
| Mechanism | compiled in | `hashicorp/go-plugin` v1.8.0 (MPL-2.0) | `wazero` v1.12.0 | `rta-<name>` on `$PATH` |
| Any language | Go only | ✅ any (gRPC) | ✅ any → Wasm | ✅ any |
| **Network access** (TCP to PG/S3/Vault) | ✅ | ✅ | ❌ **blocker** — WASI p2 sockets still experimental | ✅ |
| Crash isolation | ❌ | ✅ | ✅ | ✅ |
| Sandboxing | ❌ | ⚠️ OS-level only | ✅ strong | ❌ |
| Host callbacks (prompt/secrets) | native | ✅ via broker, bidirectional | ⚠️ awkward | ❌ |
| Typed View contract | ✅ | ✅ | ✅ | ❌ (passthrough only) |
| Startup cost | 0 | ~ms process spawn | ~ms + compile cache | process spawn |
| Distribution | in binary | OCI artifact / release asset | single `.wasm` | anything |

**Decision: the gRPC subprocess tier (`hashicorp/go-plugin`) is the primary plugin mechanism.**

Rationale:

1. **WASM cannot do the job today.** Nearly every plugin on the wishlist (Postgres, S3, Vault, etcd, Qdrant, REST, network probes) needs raw sockets. WASI Preview 2 sockets remain experimental, and wazero has *no CPU bound and no memory cap below its 4 GiB linear-memory ceiling* — so the sandbox story is weaker than it first appears. **Additional risk:** `extism/go-sdk` is at v1.7.1 (2025-03-02) with **no repository activity since 2025-05-14** — a stale dependency for a load-bearing subsystem. If we ever do WASM, target **wazero directly**, not Extism.
2. **go-plugin is proven at exactly our scale** — Terraform providers and Steampipe plugins. It is actively maintained (pushed 2026-08-17), gives us process isolation, protocol versioning, mTLS, checksum verification, and — critically — a **bidirectional broker** so plugins can call *back* into the host for secrets, confirmation prompts, and progress reporting.
3. Its documented limits are irrelevant to us: "local machine only" (we are a local CLI) and "slower than shared libraries" (we are I/O-bound on network calls, not RPC round-trips).

⚠️ **License note:** `hashicorp/go-plugin` is **MPL-2.0**. It is a separate library, dynamically referenced — no copyleft reach into our Apache code — but MPL requires that modifications *to those files* be published. We will not fork it. Record this in `NOTICE`.

**Exec tier** (`rta-<name>` on `$PATH`, kubectl/gh convention) ships in v1 too. It costs almost nothing, guarantees nobody is ever *blocked* by our SDK, and is the natural on-ramp for shell scripts. Threat model is explicitly "same as `git`/`kubectl` plugins: the user controls their `$PATH`." Exec plugins that print JSON matching the View schema get typed rendering for free; anything else is passed through raw.

**WASM tier** is a revisit trigger, not a roadmap item: reconsider when WASI p2 sockets stabilise.

### 4.3 The View contract (the crown jewels)

This is the piece to get right first, because it is the hardest thing to change later.

```go
// One of these, discriminated. Protobuf on the wire, Go union in-process.
type View interface{ isView() }

type Text     struct{ Body string; Markdown bool }
type KeyValue struct{ Pairs []Pair; Redacted []string }
type Table    struct{ Columns []Column; Rows [][]string; Total int; Page *Cursor } // pre-formatted cells
type Tree     struct{ Roots []Node }              // buckets, key prefixes, cert chains
type Chart    struct{ Series []Series; Kind ChartKind } // line | bar — shipped
type Sections struct{ Items []Section }           // composite: [{Title, View}] — shipped, see below
type Stream   struct{ /* server-streaming events: kind = progress | log | metric */ } // not yet built
type Form     struct{ Fields []Field }            // → renders via huh, returns FormResult — not yet built
type Confirm  struct{ Prompt string; Impact string } // not yet built
```

**Views compose (the component model).** `Sections` holds other views, so a
rich page is not bespoke rendering — it is an arrangement of the views
capabilities already return. `sys overview --detail` *is* `sys.host` +
`sys.cpu --cores` + `sys.mem` + `sys.load` + `sys.disk` + `sys.temp` +
`sys.ps` assembled by one `add(title, handler, values)` call each; the
per-core bar chart, the disk table and the JSON shape all come along for
free, and a section whose capability fails on this platform is simply absent.
One implementation per fact, on every surface.

Two rules keep it honest: children are wrapped in their own `Envelope`, so
`"type"` discriminates at any depth (agents can walk a composite); and
renderers recurse through the same pipeline, so composites of composites work
without special cases.

**`plugin.Page` is the one way to build one.** Every `Detailed` capability
assembling its own `Sections` literal was the same four decisions made
independently each time — which inputs an embedded call inherits, whether a
failing section sinks the whole page, whether an empty section deserves a
heading, and whether `detail` leaks down into the parts. `Page` answers them
once: embedded calls inherit the page's inputs (so a `kv` inventory can reach
the unlock key its caller supplied, and a per-host check its host), `detail`
is cleared for the parts (a section that expanded into its own full report
would nest a page inside a page), and a section whose handler fails is
dropped rather than fatal — a partial report beats a failed one. When the
absence *is* the finding, `Put` states it instead. `sys.overview`,
`net.info`, `audit.web`, `gen.overview` and `kv.status` all run through it.

This is also the **a2tea/A2UI seam** (§3, "Track this repo"): a `Section` is a
component with a typed payload, so emitting A2UI later is a mapping over an
existing tree rather than a re-architecture.

**Capabilities may declare `Detailed`.** The host sets a `detail` request
value when it owns the whole screen (opening a dashboard tile, a browse or
search selection) and leaves it false for compact previews; the CLI exposes it
as `--detail`. That is how one capability serves both a dense 6-line tile and
a full-page report without two implementations.

And one first-class **error contract** — errors are part of DX, not an afterthought:

```go
type Error struct {
    Code      string // stable, namespaced: "pg.conn.refused"
    Message   string // what happened, one line
    Hint      string // what to do next: "Is the tunnel up? Try: rta pg doctor --profile prod"
    Retryable bool
}
```

Every renderer uses it: the CLI prints `Message` + a styled `Hint`, the TUI shows it inline without dying, JSON emits it structurally, and **an AI agent gets a stable `Code` + `Hint` it can act on** — error messages are prompts now. Exit codes are fixed: `0` ok, `1` capability error, `2` usage error, `3` confirmation declined.

Design rules (non-negotiable):

- **No styling in the contract.** No colours, no widths, no borders. Semantic hints only (`Column.Kind = duration|bytes|timestamp|status`) — the host decides how those look.
- **Every View is degradable.** Each type must have a sane rendering in plain text, in JSON, and as an MCP tool result. If a proposed View type can't be expressed as JSON, reject it.
- **`Table` is paginated at the contract level.** Plugins must not stream 10M rows into a slice.
- **`Redacted` is explicit.** Secrets are marked by the plugin and masked by the host — never the other way around.
- **No *general* layout type.** An arbitrary multi-pane `Composite` is still cut — layout is where contracts go to die. `Sections` shipped anyway, and is not an exception to that rule so much as the one fixed shape that earns its keep: a flat, ordered list of titled child Views, nothing about position or size. It is what `sys overview --detail`, `todo show` and `note show` are built from. The TUI shell still owns everything about arrangement and screen composition (§5.2); the contract only ever says "here are the parts", never where they go.

`Form` and `Confirm` are the interesting ones: they are Views that flow *backwards*. The plugin returns `Form`, the host renders it with `huh/v2` (or MCP elicitation, §6.1), and the answer returns to the plugin over the go-plugin broker. That is how a plugin gets an interactive wizard without linking a single TUI library.

> Cross-check against `x/pony`'s element vocabulary before freezing v1 of this contract. It is not a dependency, but it's free design review.

### 4.4 Rejected: embedded scripting

Considered and **rejected** for v1: Lua, Starlark (`go.starlark.net`), Risor (`risor-io/risor` v1.8.1, last release 2025-05-15).

Reasoning: it adds a third calling convention, a sandbox to audit, and a language for users to learn — to solve a problem the exec tier already solves with `#!/bin/bash`. Revisit only if a concrete demand for *in-process, user-authored transforms* appears (e.g. custom column formatters).

### 4.5 Host services (plugin → host callbacks)

Exposed over the go-plugin broker. This is what makes plugins feel native rather than shelled-out:

| Service    | Purpose                                                                                                                          |
| ---------- | -------------------------------------------------------------------------------------------------------------------------------- |
| `Config`   | Read resolved config for this plugin's namespace (no direct file access).                                                        |
| `Secrets`  | Fetch a credential by reference. **Plugins never see the keyring or a raw file path.**                                           |
| `Prompt`   | Ask the user (`Form` / `Confirm`) mid-operation.                                                                                 |
| `Progress` | Report progress for long ops → host renders a bar.                                                                               |
| `Log`      | Structured logs, merged into the host's `log/v2` stream at the right level.                                                      |
| `HTTP`     | Optional shared client — proxy config, timeouts, TLS policy, and **request recording for tests** (`x/vcr`).                      |

Every capability handler receives a `context.Context`; host-side timeouts and Ctrl-C cancellation propagate over gRPC. A plugin that ignores cancellation gets its process killed — the user's terminal always comes back.

For an exec-tier plugin that insists on its own full-screen UI, the host can allocate a PTY and composite it with **`x/vt`** rather than blanking the screen. This keeps the escape hatch from looking broken.

### 4.6 Configuration & profiles

- **Format:** YAML, layered via `knadh/koanf/v2` (v2.3.6). Precedence: `flags > env (RTA_*) > ./.rta.yaml > ~/.config/rta/config.yaml > defaults`. *(Explicitly not adopting crush's Bash-based `crushrc` — clever, but wrong for a tool whose configs people will template and commit.)*
- **Zero config is a valid config.** `sys`, `net`, `cert`, `http` work with no file at all. Config is introduced when the user first needs a profile — via wizard, not hand-written YAML (§5.1).
- **Profiles:** named connection sets — `rta --profile prod pg table list`. Profiles are the primary UX unit; most users will never type a connection string.
- **Secrets:** never in the config file. Reference indirection only:

  ```yaml
  profiles:
    prod:
      production: true # gates write-confirmation, see §4.7
      postgres:
        host: db.internal
        password: ${secret:keyring/rta/prod-db} # or ${env:PGPASSWORD}, ${secret:vault:kv/db#pass}
    homelab:
      postgres:
        tunnel: kube://homelab-ctx/db-ns/svc/postgres:5432 # or ssh://bastion/db.internal:5432
        password: ${secret:keyring/rta/homelab-db}
  ```

  Backends: OS keyring → env → Vault (dogfooding our own vault plugin) → 1Password/`op` (later).
- **Tunnels are a profile concern, not a plugin concern** (D10). The host dials `ssh://` or `kube://` (port-forward via client-go), hands the plugin a plain local `host:port`, and tears the tunnel down after. Every service plugin becomes remote-cluster-capable with **zero plugin-side code** — this single feature replaces the whole `backup-kube-{pg,mariadb,qdrant,vault}.sh` family of scripts (§7.4) and is a direct product of mining our own tooling.
- **Schema:** each plugin ships a JSON Schema for its config; `rta config validate` checks everything, and we publish a merged schema for editor autocomplete (`# yaml-language-server: $schema=` line emitted by the wizard).

### 4.7 Security model

Borrowed nearly wholesale from crush, because it's the right shape:

1. **Safety classes.** Every capability is `read`, `write`, or `destructive`, plus an `idempotent` flag. Enforced by the host, declared by the plugin.
2. **Confirmation.** `destructive` always confirms interactively; `--yes` overrides; `write` confirms only when the profile is marked `production: true`.
3. **Dry-run.** Every `write`/`destructive` capability must implement `--dry-run` (report what would happen as a normal View). The conformance suite enforces it. This is a real invariant and not a formality: `http post/put/delete` shipped without it and **sent the request anyway**, which is worse than having no dry-run at all — it reports what "would" happen after it has already happened. It now renders the request it did not send, headers included, with anything carrying a credential masked, so the point is inspecting a request before it leaves and not reprinting the token you were about to send. `GET`/`HEAD` still execute under `--dry-run`: they change nothing, and refusing to run them would be theatre.
4. **Allow/deny lists** per profile — the crush `permissions allow` / `deny` model.
5. **Plugin trust.** Checksum pinning in a lockfile (`rta.lock`), signature verification (cosign) for the official registry, and a loud first-run prompt for unsigned third-party plugins.
6. **Redaction.** Views mark secret fields; the host masks them in TUI, CLI, logs *and* MCP output. Audit log records capability + args-with-secrets-stripped.
7. **AI is read-only by default.** See §6.1.
8. **Local inputs.** `Field.Local` marks an input the host resolves from its own environment and never accepts from a remote caller — the passphrase that unlocks a store, not the payload going into it. Local fields are omitted from MCP tool schemas *and* stripped from incoming MCP arguments (the name is guessable even when the schema hides it). An agent must never be invited to supply, invent, or echo back a credential: one that reaches a model's context has already leaked, whatever happens next. CLI and TUI offer them normally — there is a person there, and it is their credential.
9. **Caller surface.** `plugin.Request` carries the renderer it arrived through (`cli`/`tui`/`mcp`), stamped at each renderer boundary. Handlers must **not** branch on it to change *what* they do — one handler serving every surface is the point of the model (P1). Its only legitimate use is trust: a capability whose blast radius is "an AI agent reads your secret" can require that a human authorized it first. Two things use it — the grant gate (item 10) and `grant.allow` itself, which refuses to run over MCP.
10. **Encryption is not a gate against a local agent.** A store is only protected while the key is somewhere the reader is not. An MCP server inherits the environment it was launched from, so `RTA_KV_PASSPHRASE` or `RTA_KV_IDENTITY` set in that shell means the agent's reach is bounded by grants alone — and an *on-disk* identity is weaker still, because an agent with any file-reading tool can take the key and the ciphertext and decrypt the store itself, never touching `kv.get` or its grants. The order, strongest first: **hand the server no key material at all** (`kv.get` then fails whatever grants exist, which is the right default for most people); passphrase in the server's environment; identity file on disk. Never the SSH key you log into servers with — one leaked file should not cost you both. `rta doctor` reports which of these is true right now, because "can the agent read my secrets?" should not require reasoning about environment inheritance.
11. **Grants: consent that expires.** Safety classes are per-capability, static, and decided once when the server is launched; a grant is per-*record*, time-boxed, and decided at the moment it matters. `--allow-write` says "this agent may change things"; a grant says "this agent may read *this key* for the next 15 minutes". Issued only from a human surface — a capability that let an agent grant itself access would be theatre — and stored in plaintext (it holds no secret: capability names, record names, timestamps) so "what can the agent do right now?" is answerable without unlocking anything.

    **It is core, not a `kv` feature.** It started in `kv`, because a leaked password is the most obvious harm; it is not the only one. An agent that empties a task list or repoints `/etc/hosts` has also done something nobody asked for. So the mechanism lives in `internal/grant`, the commands live in the `grant` plugin, and enforcement happens **once, in the MCP bridge**, before `Run` — never in each handler, which would make the gate a thing a plugin author can forget. Two declarations put a capability behind it:

    - `Safety: Destructive` — implicit, no opt-in. What permanently removes something is exactly what a standing allowlist should not be enough for.
    - `NeedsGrant: true` — for capabilities whose class understates them. `kv.get` mutates nothing and is a leak.

    `Capability.Scope` names the input identifying the record (`key`, `id`, `hostname`), which is what lets a grant narrow: "read the staging token" must not have to mean "read every secret I own". A grant naming no record covers the whole capability, so a *scoped* grant deliberately does **not** authorize a call that names no record — `kv env` with no arguments is a wider ask than `kv env db-password`, and the wider ask needs the wider grant. Every record a call names must be covered: two thirds of a leak is a leak. `Validate` rejects a `Scope` naming no input, because that typo would silently stop narrowing and start covering everything.

---

## 5. Developer experience

DX is P1, so it gets its own section with measurable targets. Two audiences.

### 5.1 End user: zero to value in 60 seconds

The target flow — no Homebrew tap or release pipeline exists yet; today's
entry point is `go install ./cmd/rta` (README), and `rta init`'s wizard is
plainer than described below.

```bash
brew install this-is-tobi/tap/rta
rta                        # no config, no args → TUI dashboard: sys + net + cert live, right now
rta sys cpu                # same data, scriptable
rta cert inspect example.com --output json | jq .expiry
rta init                   # huh-powered wizard → first profile, keyring-backed secret, schema-annotated YAML
```

The rules that make this work:

- **The built-ins need zero config** — that's *why* `sys`/`net`/`cert`/`http` are Wave 1 (§7). First contact must not start with editing YAML.
- **`rta init`** creates profiles interactively (huh forms, connection test before save, secret stored to keyring — never echoed, never written to file).
- **`rta doctor`** — one command that checks: config validity against schemas, secret backends reachable, installed plugins vs. lockfile, protocol version mismatches, `$PATH` exec plugins found, terminal capabilities. Every `Error.Hint` in the system may point here.
- **Helpful failure is a feature.** Unknown command → closest-match suggestion ("did you mean `rta pg table list`?"). Every error carries a `Hint` (§4.3). `--help` on every level, rendered by fang, with real examples.
- **Completions everywhere** — shell completions (free via cobra/fang) complete *dynamic* values too, declared on the field itself (§4.1): `rta kv get <tab>` lists your keys, `rta todo rm <tab>` lists your tasks with their titles, `rta net dns x --type <tab>` lists the record types.
- **`rta plugins`** — the inventory. `rta explain` lists every capability across every plugin, which answers a different question: scrolling a capability list to work out which plugins you have is reading a phone book to learn which towns exist. One line per plugin, what it is for, how much it offers, and — the column worth having — the worst safety class it holds, because "which of these can change my machine?" is the question you ask before handing an agent a server.

### 5.2 The TUI shell

The TUI is one app shell that hosts plugin views — plugins never own the screen:

- **Dashboard** — zero-config landing: one tile per plugin that has something to show at a glance (`sys`, `net`, `todo`, `note`, `kv`, `grant`), a full-width live search bar over every capability, `[`/`]` reorders tiles, `H` hides one and `p` opens the plugin inventory where any hidden tile comes back.
- **Browse** (`b` or `:`) — the capability catalogue, grouped under plugin headers with permissions as a right-hand column. This is the discovery mechanism: browse *is* `rta explain` made interactive and filterable.
- **Panes** — the shell composes Views (list → detail, table → row inspector). Composition lives here, not in the contract (§4.3).
- **Shipped keys** — `/` focuses search, `enter`/`e`/`d`/`x`/`o` act on the selected row where the capability offers show/edit/done/remove/reopen, `y` copies the current result as JSON, `ctrl+e` opens `$EDITOR` on a `Text` field while filling a form, `esc` leaves a running capability or steps back a level. Not built: a universal `?` help overlay and a single global command-palette binding independent of mode — `b`/`:` currently doubles as both entry point and browse key.
- Charts via `guptarohit/asciigraph` on every surface, CLI and TUI alike — the one implementation, not a fallback. (`ntcharts` was considered for a Bubble Tea-native TUI chart and was not adopted; revisit if asciigraph's fixed-width rendering becomes a real limitation.)


**The dashboard shows every plugin that has something to show.** One tile each, automatically — a plugin whose capabilities you have never run is exactly the plugin you most need to be reminded exists, and a landing screen that omits half the binary teaches you the binary is smaller than it is. The qualifier is doing real work, though: a tile is a thing you *glance* at, so it has to answer a question nobody had to ask first. `sys`, `net`, `todo`, `note`, `kv` and `grant` can; `cert` needs a hostname and `http` needs a URL, and there is no useful default for either. Those get no tile. The alternatives — the same error every five seconds, or a static menu of capability names — both spend a panel of attention to say nothing, and the search bar already puts them one keystroke away, which is where you go once you *do* have a hostname in mind.

`kv`'s tile is `kv status`, not `kv list`: the list needs the store's passphrase and would spend every refresh rendering the same error, while status reads the file's metadata and the (public) recipient list — never the ciphertext — and answers what you actually want at a glance: is the store there, and can this shell open it. `grant list` is its own tile for the same reason it is its own plugin: what an agent may do right now is a fact worth seeing without asking for it.

**The search bar shows three matches and scrolls through the rest.** It is three *lines*, not three *results* — a query matching eleven capabilities that silently reports three is a lie you cannot see, and the capability you wanted looks like it does not exist. The window follows the selection, carets on the first and last line say the list continues, and the header counts (`4/11`).

**Nothing renders past the bottom of the screen.** Every pane is bounded by the terminal and scrolls inside it: the result pane is a viewport, the dashboard windows its tile rows, the search bar windows its matches — and the input form, which is the one that was wrong, now gets an explicit height so huh scrolls its group. A form is as tall as the capability has inputs (`kv set` asks eight questions) and a terminal is as tall as it is; without a bound the last fields were painted off-screen, **including the destructive confirmation**, which is the one nobody may miss. The frame is a guarantee rather than an estimate: the panel clips to the screen and the form scrolls within it, so no combination of fields can overflow. Forms re-fit on resize, and the mouse wheel scrolls whatever is under it, because people try the wheel before they look for a key and a pane that ignores it reads as stuck.

**Text inputs open `$EDITOR` on `ctrl+e`** — a note body is written in the editor you write in. huh provides it; the field description says so, because an affordance nobody knows about is not an affordance.

**A `p` pane says what is installed, and what is on the dashboard.** Hiding was one-way: `H` took a tile off and the only way back was finding the config file and guessing a capability ID — exactly the friction in-dashboard arranging was meant to delete. A hide you cannot undo is not a preference, it is a mistake waiting to happen. The pane lists every plugin with its tile state, `space` toggles it, and the change writes through like every other arrangement edit. It answers the other question in the same breath: a tile grid cannot tell you what you have installed, because plugins with nothing glanceable have no tile — so this is the one screen where `cert` and `http` are as visible as `todo`, and it says *why* they have no tile rather than leaving you to wonder.

**The catalogue is grouped, and permissions are a column.** Dozens of capabilities in one flat list is a phone book: everything is there and nothing is findable. `b` now groups them under plugin headers — the plugin is the unit people think in ("what can `net` do?") — and the cursor skips the headers, since a label is not a destination. The safety class moved out of a glyph glued to the ID (`kv.rm ⚠`, which reads as decoration) into its own right-hand column in the vocabulary the rest of the system uses, with the grant requirement beside it. "What could an agent do here?" is now answered by looking down one column, on both the catalogue and the search bar.

**A list that hides part of its own data owes you a way to see the rest.** `todo list` hides completed tasks, which made the re-open action unreachable: there was no row to act on. `A` on the list re-runs it with `--all` flipped, and the toggle shows which way it is pointing. The trail remembers it, so an action launched from a toggled list comes back to the list as you left it.

**A run can be left.** `esc` on the running screen abandons the capability in flight and lands back where it was launched from. A traceroute is thirty hops of two seconds, and a shell you cannot leave until it finishes has taken your terminal hostage. Cancelling bumps a sequence number, so the result that eventually arrives is recognised as belonging to a run nobody is waiting for and dropped, rather than painted over whatever the user moved on to.

**Curating it happens inside it.** `[` / `]` move the selected tile, `H` hides it, and both write straight through to the config file, so the next run opens on what you left. The arrangement is a visual thing; needing to quit, find a YAML file and guess capability IDs to reorder a grid you are looking at is the kind of friction this project exists to delete. Edits are stored as *adjustments* to the automatic set (`dashboard.hidden`, `dashboard.order`) rather than as a frozen list — otherwise moving one tile today would make a plugin installed next month invisible forever. `dashboard.tiles` remains the escape hatch for stating the whole dashboard by hand, and replaces the automatic set entirely.

### 5.3 One grammar for everything

```
rta <ns> <noun> <verb> [args] [flags]      # capability ID = <ns>.<noun>.<verb>
rta pg table list
rta s3 object cp s3://a/x s3://b/x
rta vault kv get secret/db
rta <ns>                                   # → TUI scoped to that namespace
```

A **shared verb vocabulary** — `list`, `get`, `set`, `rm`, `cp`, `run`, `watch`, `inspect`, `backup`, `restore` — is part of the SDK. The conformance suite warns on novel verbs; learn one plugin and you've learned them all. Global flags, as shipped: `-o/--output pretty|json|yaml|csv`, `-y/--yes`, `--dry-run`, `--no-color`, `-v/--version`, `-h/--help`. `--profile` and `--quiet` are designed, not built — profiles land with M2 (`internal/config` says so today); `table` was never a distinct format from `pretty` and the flag help should stop implying one.

### 5.4 Plugin author: hello-plugin in 15 minutes

The four-command loop, start to published:

```bash
rta plugin new mything          # scaffold: go.mod, main.go w/ one working capability,
                                #   conformance test, goreleaser config, CI workflow, VHS tape
rta plugin dev .                # dev override (terraform-style): builds, registers the local
                                #   binary for its namespace, bypasses lockfile with a loud
                                #   banner — live in CLI, TUI *and* MCP immediately
go test ./...                   # sdktest conformance suite runs against your plugin
rta plugin publish              # goreleaser build → ORAS push to any OCI registry → cosign sign
```

SDK ergonomics — one capability is ~20 lines, no gRPC visible:

```go
func main() {
    sdk.Serve(sdk.Plugin{
        Name: "mything",
        Capabilities: []sdk.Capability{{
            ID:     "mything.item.list",
            Safety: sdk.Read,
            Input:  sdk.Schema[ListArgs](),
            Run: func(ctx context.Context, host sdk.Host, in ListArgs) (view.View, error) {
                return view.NewTable(...), nil
            },
        }},
    })
}
```

What makes this honest rather than aspirational:

- **`sdktest`** — the conformance suite is a public Go package (`pkg/sdk/sdktest`), not an internal CI job. `sdktest.Run(t, plugin)` checks: schemas valid, views degradable to JSON, `--dry-run` implemented for mutating capabilities, verbs from the shared vocabulary, cancellation respected. Passing it is the definition of "a correct plugin".
- **First-party plugins are the proof** (P6): `pg`/`s3`/`vault` live in `plugins/` with their own `go.mod`, consuming the SDK exactly as a stranger would. `examples/plugin-hello` is the canonical minimal plugin *and* the scaffold template source.
- **`rta plugin dev` reaches all surfaces at once** — the moment your capability compiles, it's in the command palette and callable from Claude Code via the running MCP server. That feedback loop is the DX pitch in one sentence.

---

## 6. AI strategy

"Strong AI powers" means two directions with one registry behind both. The outbound direction ships first because it is nearly free and immediately useful.

### 6.1 Outbound: RTA *as* an MCP server ⭐ (M1)

```bash
rta mcp serve              # stdio — for Claude Code, Crush, Cursor, any MCP host
rta mcp serve --http       # streamable HTTP
rta mcp install claude     # writes the client config for you (claude mcp add rta -- rta mcp serve)
```

Every capability in the registry is auto-exposed as an MCP tool, **generated from the same JSON Schema the CLI already uses**. Zero per-capability work. Library: **`modelcontextprotocol/go-sdk` v1.7.0** (2026-07-27), official, maintained with Google.

Three mappings make this *good* rather than merely present:

1. **Safety classes → MCP tool annotations.** Our safety model translates directly into the spec's risk vocabulary, so MCP hosts drive their own permission UX correctly:

   | RTA declaration | MCP annotation                              |
   | --------------- | ------------------------------------------- |
   | `read`          | `readOnlyHint: true`                        |
   | `write`         | `readOnlyHint: false, destructiveHint: false` |
   | `destructive`   | `destructiveHint: true`                     |
   | `idempotent`    | `idempotentHint: true`                      |

2. **`Confirm`/`Form` views → MCP elicitation.** Interactive flows survive the AI boundary: when a capability needs confirmation mid-run, the agent's host surfaces a real prompt to the human instead of the tool call failing. Annotations are hints, not enforcement — so the host-side gate below still applies regardless of client behaviour.

3. **`Error{Code, Hint}` → agent-actionable failures.** A hint like "run `rta pg doctor`" is something an agent can *do*.

**Safety gate (host-enforced, not annotation-trusted):** only `read` capabilities are exposed by default. `write` requires `rta mcp serve --allow write`; `destructive` requires an explicit per-capability allowlist in config. An LLM must not be one hallucination away from `DROP TABLE`.

Beyond tools:

- **MCP resources:** capability catalogue (`rta://capabilities`), per-plugin docs, config JSON Schemas — so agents can *read about* the tool surface, not just call it.
- **MCP prompts:** canned workflows ("triage this host", "why is this cert failing") shipped by plugins.
- **`rta explain <capability>`** — prints the capability card (schema, safety class, examples) as markdown or JSON. Works for humans, works pasted into a prompt, and is what the resources serve. The repo also ships an `AGENTS.md` snippet users can drop into their projects.

Demo line: **"give Claude Code eyes on your Postgres, S3, Vault and certs — safely."**

### 6.2 Inbound: an agent *inside* RTA (M3)

Pulled forward from "later" — this is half of the product's AI identity, and its engine (`fantasy`) is proven in crush:

- An `ai` plugin using **`fantasy` v0.41.1** (agent loop, multi-provider, streaming) + **`catwalk` v0.51.27** (model catalogue — never hardcode a model list).
- The agent's tools are *RTA's own capabilities* — same registry, same safety classes, same confirmation prompts. The agent asks before `write`, exactly like a human session.
- **This is the `mods` rewrite** (§3.3): `cat error.log | rta ai "what broke?"` restores the archived pipe-first UX on a maintained foundation. Also: `rta ai` as a TUI pane — ask questions about what you're currently looking at (current View is serialized as context — free, because Views are data).
- BYOK: provider keys resolve through the same `${secret:…}` indirection as everything else.

### 6.3 Wrap the moving parts

`fantasy` is v0.41 and moving fast; `catwalk` likewise. Both hide behind a thin internal `agent.Provider` interface — swapping engines must stay a day's work, not a rewrite. This is the same discipline as wrapping go-plugin behind `pluginhost`.

### 6.4 Watch, don't adopt

**`charmbracelet/a2tea`** (created ~2026-08-15, 0 ⭐, untagged) — an A2UI-over-MCP bridge letting a remote agent emit declarative UI that a Bubble Tea host renders. It is the *same architectural bet* we're making in §4.3, from Charm, over MCP. Far too new to depend on — but if A2UI gains traction, teaching our MCP server to emit A2UI would make RTA's views render natively inside other agents' TUIs. **Track this repo.**

---

## 7. Plugin catalogue

Namespaces are short — they get typed constantly.

### Wave 1 — built-in, ship in v0.1

Zero external service dependency, zero config: they prove the whole pipeline (capability → 4 renderers) without a docker-compose in the loop, and they make the first-run dashboard useful (§5.1).

| ns     | Capabilities                                                                                        | Libraries                                                                                            |
| ------ | --------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `sys`  | grouped overview (`sys overview`: one dense line per subsystem — the dashboard's system tile), cpu, mem, disk, load, temps, processes, uptime | `shirou/gopsutil/v4` **v4.26.7**                                                                     |
| `net`  | local overview (`net info`: interfaces, DNS, proxies with masked credentials, throughput — local facts only, never phones home), `net ping`, `net dns` (nslookup-grade: `--type auto` picks address records for a name and PTR for an address; `--server` queries a specific resolver, which is how "the record is wrong" is told apart from "my resolver is stale"), `net trace` (real traceroute over **unprivileged** ICMP — hop replies matched to their probe by the sequence number recovered from the packet each ICMP error quotes back, since the echo ID is kernel-chosen on datagram sockets), `net probe` (what `telnet host port` is actually used for: connect, time the handshake, show the banner; `--tls` reports the negotiated version/cipher/cert, `--send` pokes protocols that expect the client to speak first), port scan, speedtest, latency graph, and the system files themselves: `net hosts list/add/toggle/rm` (`manage-etc-hosts.sh` reborn — entries can be *parked* rather than deleted, since the part everyone forgets is taking an override out again) and `net resolver list/set`. Those four are classified **destructive**, not write: they rewrite a root-owned file that governs name resolution for every process on the host, and a wrong entry reroutes traffic instead of failing, so an agent needs a per-capability allowlist rather than blanket `--allow-write`. Every edit preserves the rest of the file byte for byte (comments and spacing are somebody's notes), backs up first into rta's own state directory, and writes atomically beside the target so a torn write is impossible. `net resolver set` **refuses a machine-generated file** — a symlink into `/run`, a `Generated by` header — because silently editing one is worse than refusing: the change works, then vanishes at the next reboot with nothing pointing at why. `--file` operates on a container's or chroot's copy. | `prometheus-community/pro-bing` v0.9.1, `golang.org/x/net/icmp`+`ipv4`/`ipv6` (traceroute), stdlib `net`/`crypto/tls`, `showwin/speedtest-go` v1.7.11 |
| `cert` | inspect x509, chain validation, expiry watch, TLS handshake/cipher report, SNI, CT-log lookup       | stdlib `crypto/tls`, `crypto/x509`                                                                   |
| `audit` | the hardening toolbox: checks that grade something you run against a *named* security control rather than merely describing it. `audit web <host>` — a security audit of a web host: one HTTPS request, plus a probe `Origin` header to catch CORS reflection, graded: TLS version/cipher/cert chain/expiry/signature algorithm; CSP checked for the specific weak directives OWASP's own cheat sheet calls out, not presence alone; X-Frame-Options graded together with CSP `frame-ancestors` as its modern replacement; HSTS graded against real thresholds, not presence alone; cross-origin isolation headers; cookie flags; version/stack exposure. Every finding cites an OWASP Top 10:2025 category and a MITRE CWE, verified against cwe.mitre.org/the OWASP ZAP alert database rather than assumed — a hardening tool citing the wrong control is worse than citing none. Not a replacement for a real scanner (ZAP, Nikto, testssl.sh) — one request, no crawling, no active exploitation, the fast path for "how is this host doing right now". With `--detail` the findings group into one section per area (transport & TLS, security headers, cross-origin, cookies, information exposure) and the page ends with the distinct controls it cited plus a `cwe.mitre.org` URL for each — a finding you can go and read up, not just a label. Two rules bound the plugin as it grows, stated in its own package doc: it does not reimplement scanners (one interaction, read-only, safe against production — when a finding warrants ZAP or testssl.sh, saying so is the job), and every finding cites its source. It lives in its own namespace rather than under `net` because it happens to speak HTTP but the question it answers is "is this safe to expose", not "where does this name point" — and DNS-level email authentication, TLS depth and host posture are the obvious next entries, none of which belong under networking either. ADR 0008. | stdlib `net/http`, `crypto/tls`, `crypto/x509` |
| `http` | request any REST endpoint, auth (basic/bearer/OAuth2/mTLS/AWS SigV4), saved collections, diff responses | stdlib + `x/vcr` for tests                                                                        |
| `todo` | local task list, GitHub-issue-shaped: title + optional markdown body, tags (`--tag`, filterable, `todo tags` for counts), due dates (`--due today\|tomorrow\|friday\|2026-08-25`, graded OVERDUE/WARN/ok), sub-tasks (`--parent`, progress suffix, `todo rm` re-parents children instead of orphaning them), cross-references (`#12` in a body resolves both ways), full-text `todo search`, and `todo reopen` — the undo for `done`, because marking the wrong task complete is a one-keystroke mistake and a list you cannot take something back out of is a list people stop trusting (`o` on the list and on a task's own page, one key from `d`). `todo show` is a **composed page** (`view.Sections`), not one markdown blob: structured metadata, then the prose alone, then sub-tasks and references as real tables — so neither half drowns the other and a machine caller reads a due date without parsing prose. Interactive edit prefills current content (`Capability.Prefill`); a task is editable from its own page as well as from the list. First mutating built-in — exercises the full safety model. **AI trajectory:** exposed over MCP (`--allow-write`) so agents can capture/complete tasks; later the `ai` plugin can delegate ("work on todo 3"), rewrite, or triage entries. | shared `builtin/internal/itemstore` (JSON, XDG data dir) |
| `note` | local markdown notes on the same item store: tags, cross-references, `note search`, `note tags` — a task without status/due/parent. Same composed detail page as `todo` (metadata apart from content), edit-in-place prefill, glamour rendering, and full create/edit/remove from the list *and* from a note's own page. **AI trajectory:** notes improvement/styling via the `ai` plugin. | shared `builtin/internal/itemstore` |
| `kv`   | encrypted local store for secrets, certificates and key files — `kv set/get/list/rm/env/recipients/status/show/init/rekey`, ten capabilities. One `age`-encrypted file, unlocked by a passphrase (`--passphrase`, `RTA_KV_PASSPHRASE`, or a terminal prompt) **or** by keys you already own (`--identity` unlocks; a locked SSH key is asked for its own passphrase the way `ssh` asks, since ssh-agent cannot decrypt). Entries carry a **kind** (certificate/private-key/json/file/…, detected from content) and a **description**, because six months later "api-token-2" tells you nothing. `kv get --out` returns a file to disk at 0600 without it passing through scrollback — and only from the CLI/TUI, since `--out` is `Local`: a grant on `kv.get` authorizes revealing one value, not choosing which file on the host it gets written to. `kv env` prints shell exports for `eval "$(rta kv env db-password)"`, single-quoted so a secret can neither break the eval nor smuggle a command into it. `kv list`/`kv show` return names and metadata only, **never** a value or a preview. **`kv init` sets the lock once.** `--generate` makes a dedicated age key at `<config-dir>/kv.identity` (0600) and locks the store to it; nothing needs a flag or a passphrase afterwards, because `identityPath` looks there last. `--identity` locks it to a key you already own. A passphrase store needs no init — that is the default. `init` refuses a store that already exists, because re-initialising would write a recipients file describing a store none of those recipients can open — changing the lock on an existing store is **`kv rekey`** (Destructive: it can take every reader's access away at once), which decrypts first and so has to prove you can, refuses to leave the caller themselves locked out, and never runs as a side effect of an ordinary write — `kv set`/`kv rm` re-encrypt to exactly the recipients already on record and refuse outright if the plaintext `kv.recipients` file disagrees with what the store's own (encrypted) record says it was last locked to, since that file has no cryptographic tie to the ciphertext and is editable by anyone with data-directory write access, key or no key. The generated key lives beside the *config*, not beside the store, so a copy of the data directory (a backup, a synced folder, a container volume) is ciphertext and nothing else.

**`--identity` alone means "lock it with my key".** It used to answer "no passphrase provided", which asks for the very thing the caller was avoiding. The identity's own public half is a perfectly good recipient.

**`ssh-agent` cannot unlock the store, and the refusal says so.** An agent signs; it never decrypts, and it will not surrender the key material age needs to derive the shared secret (age's own README: "ssh-agent is not supported"). A passphrase-protected SSH key therefore needs its passphrase supplied, or a key that needs none — which is what `--generate` is for. Guessing at this in an error message costs somebody an afternoon.

`kv status` reports where the store is, how it is locked and whether this shell can open it, without touching the ciphertext — the one kv capability that always works, and therefore the dashboard tile. `kv get`/`kv env` are deliberately **Write**, not Read (§4.7 blast radius), and on top of that declare `NeedsGrant` (§4.7.11). Absorbs `skate`'s idea (§3.3). `kv status --detail` is a page rather than a line: the same summary, who can open the store, and the inventory itself — `kv.list`, which returns names, kinds and sizes and never a value, which is exactly what makes it safe to put on a status page. It opens the store with whatever key the caller already holds and never prompts; with none available it says so and the rest of the page still renders, because a status page that blocks on a passphrase is not a status page. The on-disk format is **versioned** as of v0.5: `entry.Value` became `[]byte` so binary values survive a round trip, which silently changed the encoding (`encoding/json` writes a `[]byte` as base64 and a string as itself) and left every store written before it failing to open with "illegal base64 data at input byte 0" — decryptable the whole time, refused at the last step, found on a real store. Both formats are read now and every write stamps the version, so the one guess is only ever made about stores older than the stamp. | `filippo.io/age` **v1.3.1** (`ScryptRecipient`/`ScryptIdentity`, `agessh` for SSH keys), `golang.org/x/crypto/ssh` |
| `grant` | time-boxed permissions for AI agents — `grant allow/list/revoke`. The human half of §4.7.11: `rta grant allow kv.get db-password --ttl 15m` names one capability and optionally one record; a plugin name (`kv`) covers all of it. `--max-uses N` (D12) expires the grant after N successful calls, on top of the TTL, whichever comes first — `--max-uses 1` for a value that should be read exactly once; `0` (the default) is unlimited within the TTL, exactly today's behavior. Refuses to run over MCP, so an agent can only ask a person. Re-allowing extends rather than stacking; revoking a plugin takes back every grant inside it, because typing it in a hurry has to mean nothing survives. `grant list` is readable without unlocking anything and is the dashboard tile; `grant list --detail` is a page: what is currently allowed, then the whole catalogue split by what it takes to reach each part — nothing, `--allow-write` on the server, or a grant a person issues. Those are three distinct gates and the page keeps them apart rather than showing one "not granted" bucket, which would read as if a write were as freely reachable as a read; all three are derived from the registry, so a capability cannot be added and quietly go unlisted. `rta doctor` reports the same thing, since a grant issued yesterday and forgotten is exactly what a health check should surface. | `internal/grant`, enforced in the MCP bridge |
| `gen`   | local, offline generation: `gen password` (configurable charset, `--exclude-ambiguous`, entropy reported in bits), `gen token` (crypto/rand bytes, `--encoding hex\|base64\|base64url\|base32` — base32 at the right length is already a TOTP secret, no separate `gen.totp`), `gen uuid` (`--version 4\|7`). `crypto/rand` only, everywhere — unbiased alphabet selection (`rand.Int` against the exact alphabet size, never `rand.Intn`/modulo). All **Read**: nothing here reveals a secret the caller did not already have or uses one on their behalf, the `kv.get` line (§4.7) is about crossing from "protected at rest" to "revealed", and there is no "at rest" for freshly synthesized material. `gen overview` is the sampler and the dashboard tile: one of each common shape side by side — a login password, one with symbols, one without look-alikes, real 32-byte key material in hex *and* base64, a URL-safe token, a TOTP secret, UUID v4/v7 — each labelled with what it is for, the entropy it actually carries, and the command that reproduces it, because the hard part is not generating a random string, it is knowing which shape to ask for. `--detail` adds every preset plus a section on why a "32-character key" is not a 32-byte one (~204 bits against this alphabet, not 256 — computed from the very alphabet the row above it used, so the correction cannot drift away from the value it explains). A tile at all is reversed from the original call here of no tile: that first call was never actually wired into the auto-tile picker, so `gen.password` was already showing up by accident (every capability here is Read with no required input — exactly the shape the picker looks for) before anyone decided it should, caught live when the user described watching a real password regenerate on their own dashboard every five seconds. Revisited deliberately rather than reverted quietly: the tile is now *named* in `preferredTile`, so it is a decision on record rather than a side effect of ordering, and `H` (hide) is the accepted mitigation — the same one `kv.status` and `grant.list` already rest on. | `crypto/rand`, `google/uuid` v1.6.0 |
| `codec` | mechanical encode/decode: `codec b64`/`codec hex`/`codec url` (`--decode` reverses direction), `codec jwt` (decode header + claims for inspection, **unverified by default** and says so in its own output — for debugging a token, not authenticating one). All **Read** — unlike `kv.get`, the caller already possesses the encoded input, so decoding it does not hand them anything new. `codec b64 --decode` is forgiving about which base64 dialect produced the input (standard/URL-safe, padded/unpadded) rather than requiring the caller to know. | stdlib only: `encoding/base64`, `encoding/hex`, `net/url`, `encoding/json` |

### Wave 2 — first external plugins, the ones that validate the SDK

| ns      | Capabilities                                                                                                                                  | Library                                                                     |
| ------- | --------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| `pg`    | explore schemas/tables/rows, run queries, EXPLAIN, active connections, sizes, **backup/restore** (`pg_dump`/`pg_restore` orchestration + progress) | `jackc/pgx/v5` **v5.10.0**                                              |
| `s3`    | ls/cp/mv/rm, presign, bucket policy, multipart, sync, storage class                                                                           | `minio/minio-go/v7` **v7.3.0** (Apache-2.0; AWS/MinIO/R2/Ceph)              |
| `vault` | kv read/write/list, token/lease info, seal status, policy view, transit encrypt/decrypt. `vault.wrap.set`/`vault.wrap.get` (D13, ADR 0006): Vault's native response-wrapping (`sys/wrapping/wrap`, `-wrap-ttl`) as a single-use, TTL'd cubbyhole token — a thin pass-through, no new dependency. Reaches only people who can already reach the sender's Vault deployment; documented honestly as "share within your infra", not "send to anybody". Both **Write**+`NeedsGrant`; `wrap.get` consumes its token on read, so `--dry-run` must not actually unwrap. | `hashicorp/vault/api` v1.23.0                                               |
| `kube`  | fast path over the context/namespace/workload landscape, deliberately read-first like `git` (§7.5): `kube.context.list/get`, `kube.namespace.list`, `kube.pod.list`, `kube.deployment.list`, `kube.overview` (composed `Sections`, mirrors `sys.overview`) — no describe, no logs, no exec, no scale, no delete; those stay `kubectl`'s job (§1.3 non-goal). The one deliberate mutation, `kube.context.set` (rewrites `~/.kube/config`'s current-context), is **Write**+Idempotent, not Destructive — but earns `NeedsGrant` anyway on a third trigger beyond §4.7's two documented ones (leak; large-blast-radius RCE): a quiet, reversible state mutation that silently redirects every subsequent command, human or agent, until someone notices. Default TTL is the right shape for it; no use-count grant needed. `kube.node.list` deliberately held for a fast-follow, not the initial cut. Exercises the same kubeconfig-context-resolution code the D10 `kube://` tunnel resolver will need, months before that work starts, and pairs with a tunneled `pg`/`vault` as the strongest available end-to-end demo of D10 — but does not, by itself, make `kube://` demonstrable (it never touches `tools/portforward`). | `k8s.io/client-go` **v0.36.3** — official, the library `kubectl` itself consumes |
| `docker` | fast path over local Docker state — `docker.overview` (dashboard tile, degrades to a clear error when the daemon is unreachable rather than a hard failure), `container.list/inspect/logs`, `image.list/inspect`, `volume.list/inspect`, `network.list/inspect`, `docker.disk`. Unlike `kube`/`git`, deliberately **not** read-only: `container.stop/restart/rm` are bounded, single-purpose, and the literal janitorial loop a dev runs daily — a judgment call worth ratifying as its own ADR line rather than leaving implicit. `container.inspect` is **Write**+`NeedsGrant` (default TTL, no use-count needed), not Read+redacted like `net.info`: its `Env` field carries plaintext secrets by convention, and name-pattern redaction is only a heuristic, not the syntactic certainty `net.info`'s masking relies on. `image.prune`/`volume.prune`/`network.prune` are **deliberately deferred out of the initial cut**: the Engine API has no server-side dry-run for them, so a correct preview means the plugin reproducing `dockerd`'s own filter logic client-side — exactly the failure shape the `http post/put/delete` dry-run postmortem (§4.7) already warns about — and they ship only after a cross-Engine-version conformance test proves that reproduction doesn't drift. | `github.com/moby/moby/client` **v0.5.1** — the Engine API client, split from the monolithic `moby/moby` module in 2026 into its own semver'd `go.mod`; pure Go, no CGO |

`pg` is deliberately first: it exercises **every** View type (Tree for schemas, Table for rows, Stream for backup progress, Form for connection, Confirm for destructive ops, Error with hints for the classic connection failures). If the contract survives `pg`, it survives anything.

### Wave 3 — community-shaped

| ns       | Notes                                                                                          |
| -------- | ---------------------------------------------------------------------------------------------- |
| `ai`     | §6.2 — the `mods` rewrite. **Promoted to M3.**                                                 |
| `etcd`   | `go.etcd.io/etcd/client/v3` v3.7.1. Key browser, watch, lease, cluster health, compaction.     |
| `qdrant` | `qdrant/go-client` v1.19.0. Collections, points, vector search + score view. Backup/snapshot capabilities replace `backup-kube-qdrant.sh` + `monitor-kube-qdrant.sh` via tunnels. |
| `kc`     | **Keycloak** — users list/add/extract, clients add, token get, required-actions & ToC checks. Mined from 7 (!) `keycloak-*.sh` scripts (§7.4); pure REST admin API, no heavy deps. |
| `reg`    | **OCI registry ops** — list/inspect tags, purge by pattern/age, delete images (GHCR & friends). Mined from `delete-ghcr-image.sh` / `purge-ghcr-tags.sh`; dogfoods our own `internal/oci` + `oras-go`. |
| `eol`    | End-of-life checks via the [endoflife.date](https://endoflife.date) API — `rta eol check postgres 15`. Mined from `eol-infos.sh`; pairs naturally with `cert`'s expiry-watch spirit. Tiny, great first community-plugin example. |
| `keys`   | Absorb `melt` — SSH key backup to seed words. Small, self-contained, delightful.               |
| `debug`  | Absorb `sequin` — explain ANSI sequences. Useful to us during development too.                 |
| `mariadb`, `redis` | Mirrors of `pg`'s shape for the other databases we already back up in bash (`backup-kube-mariadb.sh`, `monitor-kube-redis.sh`). After `pg` proves the contract, these are mostly mechanical. |
| `share`  | One-shot secret transfer to an arbitrary recipient — the answer to "send anybody a secret" that isn't already covered by `vault.wrap.*` (§7, Wave 2), for people who don't share a Vault deployment (D13, ADR 0006). `share.secret.set`/`share.secret.get`/`share.secret.status`, built on the magic-wormhole protocol (`psanford/wormhole-william` **v1.0.8**, MIT — protocol-interoperable with the reference Python `wormhole` CLI, so the recipient needs no `rta` install at all); wrapped behind an internal interface (D7 discipline) because the Go binding itself has gone quiet for over a year even though the protocol and relay ecosystem it rides are actively maintained. This is **the single highest-blast-radius write in the whole catalogue** — a claimed secret leaves the local machine and trust boundary permanently, with no revocation and no far-end log — so `share.secret.set`/`get` refuse `SurfaceMCP` outright, the same precedent `grant.allow`/`grant.revoke` already set, rather than relying on a grant alone; and `share` is excluded from `rta plugin install --recommended` and the `:full` Docker image (§9, D5), so acquiring it is always a separate, visible decision. Deliberately last in Wave 3, not first, given the risk profile — realistically M4-tier. A hosted relay `rta` itself would operate was considered and **rejected outright**: exactly the "mandatory hosted backend we must operate and keep available forever" P7 warns against, and the first plugin in this catalogue where `rta` itself, not something the user already runs, would be the backend. |

### 7.4 Mined from our own tooling (`this-is-tobi/tools` + `dotfiles`)

Reviewed both repos (locally, 2026-08-17). `tools/shell/` holds ~30 bash scripts — a literal backlog of real, personally-validated workflows. Mapping:

| Existing script(s)                                                        | Fate in RTA                                                                                                                                     |
| ------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| `backup-kube-{pg,mariadb,qdrant,vault}.sh`, `monitor-kube-{cnpg,qdrant,redis,vault}.sh` | **The tunnel insight (§4.6/D10):** all 8 are *service tool × kube access path*. Tunnels in profiles + the service plugins replace the entire family — the strongest dogfooding validation in this doc. |
| `keycloak-*.sh` (×7)                                                       | → `kc` plugin (Wave 3). Seven scripts for one service is a loud demand signal.                                                                   |
| `delete-ghcr-image.sh`, `purge-ghcr-tags.sh`                               | → `reg` plugin (Wave 3).                                                                                                                         |
| `eol-infos.sh`                                                             | → `eol` plugin (Wave 3) — also the ideal "your first plugin" tutorial subject: one REST API, one Table view.                                     |
| `manage-etc-hosts.sh`                                                      | → `net hosts` capability (Wave 1).                                                                                                               |
| `vault` monitoring (seal status, leader, storage)                          | Already in the `vault` plugin scope (Wave 2) — confirmed by real usage.                                                                          |
| `github-{configure-repo,create-app,create-ruleset}.sh`, `helm-template.sh`, `export-{kube,argocd}-resources.sh`, `trivy-report.sh`, `compose-to-matrix.sh`, kind/act helpers | **Stay in bash** (or exec tier later). `gh`, `helm`, `kubectl`, ArgoCD and k9s already own these surfaces — rebuilding them violates non-goals. KISS is also knowing what *not* to absorb. |
| `dotfiles` backup/restore/profiles                                         | Not a plugin. But it is the concrete use case for the M5 encrypted-sync bet (the `charm` rewrite): syncing RTA profiles is the same problem as syncing dotfiles. |

Two systemic take-aways beyond the catalogue: every script re-implements the same arg-parsing/colors/defaults boilerplate — exactly the boilerplate the capability model deletes; and half of them compose *through Kubernetes*, which is why tunnels earned a place in the core config model rather than in any single plugin.

### 7.5 Next core tools: filesystem, search, ssh, gpg, git, codec

Designed here (concrete capabilities, libraries, safety classes); not yet built. Library choices below are sourced from a fresh (2026-08-18) survey of the Go ecosystem, not assumed.

**A new safety principle, established by `kv.get` (§7, Wave 1) and reused throughout this section:** the three-tier safety model (Read/Write/Destructive, PROJECT.md §4.7) gates *blast radius*, and reading a secret has blast radius even though it mutates nothing. The rule that generalizes: **a capability that reveals a secret's plaintext, or that materially uses a secret (a private key to sign, an SSH credential to authenticate), is `Write`** — it needs an operator's explicit `--allow-write` before an MCP agent can call it — **even when it has no side effect on disk.** A capability that only ever operates on values the caller already possesses (decoding a base64 string they handed you) does not cross a trust boundary and stays `Read`. Every row below states which side of that line it's on and why.

**Since shipping `kv`, that principle has a sharper second instrument: grants (§4.7.11)** — and it is now core, so these plugins inherit it rather than each building their own. Safety classes are static and per-capability; a grant is per-record and expires. Where a row below is `Write` *because it reveals or uses a secret* — `gpg.decrypt`, `gpg.sign`, `ssh.exec` — the plugin declares `NeedsGrant: true` and a `Scope` naming the key or host input, and the MCP bridge does the rest. Anything `Destructive` needs a grant already, with nothing declared. `gpg.sign` in particular wants it: "this agent may sign as me, once, in the next five minutes" is a sentence an operator can reason about, where "this agent may sign as me" is not.

| ns | Capabilities | Safety | Library |
| -- | ------------ | ------ | ------- |
| `fs` | Filesystem: `fs.ls`/`fs.tree`/`fs.stat`/`fs.du` (usage per directory), `fs.find` (name match, `.gitignore`-aware), `fs.grep` (content search — shells to `rg`/`fd` when on `$PATH`, falls back to `filepath.WalkDir`+`regexp`: no maintained Go-native ripgrep-equivalent exists, and a hand-rolled one loses by ~40x on real workloads). `fs.cp`/`fs.mv`/`fs.chmod` (Write), `fs.rm` (Destructive). "Search" folds into `fs` rather than becoming its own namespace — one mental model, one less plugin to maintain, matching the low-overhead principle already applied throughout this catalogue. | ls/tree/stat/du/find/grep: Read · cp/mv/chmod: Write · rm: Destructive | stdlib `os`/`path/filepath`; shell out to `rg`/`fd` opportunistically; `go-git/go-git/v5/plumbing/format/gitignore` for `.gitignore` matching (already a dependency via `git`, below, and better-maintained than `sabhiram/go-gitignore`, stale since 2024) |
| `git` | Deliberately **read-only**: `git.status`, `git.log`, `git.diff`, `git.branches`, `git.blame` — structured `Table`/`Tree` views an agent can consume without parsing `git status --porcelain`. Explicitly **not** `git.commit`/`git.push`/`git.clone`: the `git` CLI already owns mutation well (same non-goals reasoning as `gh`/`helm`/`kubectl` in §7.4 — "KISS is also knowing what not to absorb"). The differentiator isn't reimplementing git; it's a uniform structured view, potentially across many repos at once. | all Read | `go-git/go-git/v5` **v5.19.2** — production-viable for read-only introspection (used this way by Gitea and others); go-git's known memory/speed gap vs the `git` binary shows up in clone/fetch/large-diff, which this plugin never does |
| `ssh` | `ssh.hosts` (list `~/.ssh/config` entries — host, user, port, identity file), `ssh.exec <host> <cmd>` (one-off remote command). Ad-hoc, direct SSH — the ssh:// tunnel machinery for other plugins to ride (databases, kube) is a profile-level concern already specified in §4.6/D10, not this plugin's job. | hosts: Read · exec: Write (runs an arbitrary remote command — unbounded blast radius by definition, same tier as `http.post`) | `golang.org/x/crypto/ssh` **v0.55.0** (the standard client; part of the Go team's own extended stdlib) · `kevinburke/ssh_config` **v1.6.0** for config parsing (the de facto standard; known gap: no `Match` directive support) |
| `gpg` | OpenPGP without shelling to `gpg`: `gpg.encrypt`, `gpg.decrypt`, `gpg.sign`, `gpg.verify`. | encrypt (produces ciphertext, reveals nothing): Read · **decrypt: Write** (reveals plaintext — the `kv.get` precedent exactly) · **sign: Write** (materially uses a private key to produce an attestation under the user's identity — even though the key itself never appears in the output, an agent freely able to sign things as the user is a real blast radius) · verify (checks a public signature, reveals nothing): Read | `github.com/ProtonMail/go-crypto/openpgp` **v1.4.1** — the maintained successor; `golang.org/x/crypto/openpgp` is formally deprecated (GO-2026-5932, unsafe by design) and must not be used |
| `codec` | Mechanical encode/decode: `codec.b64`, `codec.hex`, `codec.url` (encode + decode each), `codec.jwt` (decode and pretty-print claims — **unverified by default**, clearly labeled; the point is inspecting a token during debugging, not authenticating it). | all Read — unlike `kv.get`, decoding doesn't cross a trust boundary: the caller already possesses the encoded value, so revealing its decoded form doesn't hand them anything new | stdlib only: `encoding/base64`, `encoding/hex`, `net/url`, `encoding/json` |

`encrypt`/`decrypt` as a standalone pair (the user's literal words) is `gpg` plus `kv`'s existing `age`-backed file mode (§7, Wave 1) — no third plugin needed. Two systems (`age` for the local secrets store and passphrase-based file encryption, OpenPGP for anything that needs to interoperate with the wider PGP ecosystem — signed releases, existing `.gpg` files, other people's public keys) is already the minimum, not a gap to close later.

---

## 8. Repository layout

Module: `github.com/this-is-tobi/rule-them-all` · Binary: `rta`

```
rule-them-all/
├── cmd/rta/                    # main; wires cobra + fang/v2
├── internal/
│   ├── app/                    # bootstrap, DI, lifecycle
│   ├── registry/               # capability registry, resolution, safety enforcement
│   ├── config/                 # koanf layering, profiles, schema validation
│   ├── secrets/                # keyring / env / vault resolvers
│   ├── render/
│   │   ├── cli/                # View -> text/table/json/yaml/csv
│   │   ├── tui/                # View -> bubbletea/v2 (the app shell: dashboard, palette, panes)
│   │   └── theme/              # lipgloss/v2 tokens; single styling source of truth
│   ├── mcp/                    # capability -> MCP tool/resource/prompt bridge
│   ├── grant/                  # per-record, time-boxed agent consent (D11, ADR 0005) — shipped
│   ├── pluginhost/             # go-plugin client, discovery, lifecycle, health, dev overrides
│   ├── oci/                    # plugin install/pull via oras-go
│   └── audit/                  # audit log, redaction
├── pkg/                        # PUBLIC API — semver-locked, this is the SDK contract
│   ├── plugin/                 # Plugin/Capability interfaces + go-plugin serve helpers
│   ├── view/                   # the View + Error types (§4.3)
│   └── sdk/                    # ergonomic helpers for plugin authors
│       └── sdktest/            # public conformance suite (§5.4)
├── proto/v1/                   # frozen gRPC contract; breaking change = proto/v2 + negotiation
├── plugins/                    # first-party plugins (own go.mod each, own release cadence)
│   ├── pg/  s3/  vault/  ai/  etcd/  qdrant/
├── builtin/                    # built-ins: sys, cert, net, audit, http, todo, note, kv, grant, gen, codec
│   └── all/                    # the catalogue itself — one list, upstream of every renderer
├── examples/plugin-hello/      # canonical minimal plugin — scaffold template + conformance fixture
├── docs/
│   ├── adr/                    # architecture decision records (seeded from §12)
│   └── plugins/authoring.md
├── tapes/                      # VHS .tape files -> README GIFs, run in CI
└── .goreleaser.yaml
```

**Layout rationale:** `pkg/` is the only public surface and is where semver discipline applies — everything a plugin author imports lives there and nowhere else. First-party plugins keep separate `go.mod` files so they release independently *and* so we are forced to consume our own SDK exactly as a third party would (P6).

---

## 9. Distribution & release

- **`goreleaser/v2` v2.17.1** — one config, all targets.
- **Binaries:** linux/darwin/windows × amd64/arm64. Static, CGO off.
- **Docker:**
  - `ghcr.io/this-is-tobi/rta:latest` — distroless, core only.
  - `ghcr.io/this-is-tobi/rta:full` — bundles first-party plugins + `pg_dump`/`pg_restore` (needed for `pg` backup capabilities; document the version-matching caveat).
- **Package managers:** Homebrew tap, Scoop bucket, `nfpm` deb/rpm/apk, AUR, Nix. *(Charm's own repo list is the template — they maintain `homebrew-tap`, `scoop-bucket`, `nur`, `winget-pkgs`.)* `rta` is collision-free on Homebrew and GitHub as of 2026-08-17.
- **Plugins as OCI artifacts** — the Steampipe model, via **`oras.land/oras-go/v2` v2.6.2**:

  ```bash
  rta plugin install ghcr.io/this-is-tobi/rta-plugin-pg:v1
  rta plugin install --recommended        # the curated starter set, one command
  rta plugin list / update / remove
  ```

  Any OCI registry works — no bespoke registry to run. Signed with cosign; checksums pinned in `rta.lock`. First-party plugins are **install-on-demand** (D5): the core binary stays small, `--recommended` and the `:full` image cover "just give me everything".
- **Supply chain:** SBOM (syft) + provenance attestations + cosign signatures on both binaries and plugin artifacts. Dependabot/renovate on all `go.mod`s.
- **Versioning:** the **plugin protocol version is independent of the CLI version**. `proto/v1/` is frozen once published; breaking changes mean `proto/v2/` and a negotiation window where the host speaks both.

---

## 10. Testing strategy

| Layer             | Approach                                                                                                                                   |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| Capability logic  | Plain table-driven unit tests.                                                                                                             |
| View rendering    | **Golden files** — every View type × every renderer (cli/tui/json/mcp). Catches accidental format breaks, which are our real regression risk. |
| TUI               | **`x/exp/teatest`** — drive the Bubble Tea program, assert on final frame.                                                                 |
| Plugin protocol   | **`sdktest` conformance suite** (§5.4) run against `examples/plugin-hello` in our CI, and by every plugin author in theirs.                |
| Service plugins   | `testcontainers-go` for pg/etcd/qdrant/minio/vault.                                                                                        |
| HTTP-based        | **`x/vcr`** record/replay — no live creds in CI.                                                                                           |
| MCP surface       | Golden-file the generated tool list + annotations; round-trip an elicitation flow against the go-sdk client.                               |
| E2E CLI           | Binary invocation, `--output json`, assert on parsed structures + exit codes.                                                              |
| Docs              | **VHS** tapes re-recorded in CI; a failing tape means the UX changed silently.                                                             |

---

## 11. Roadmap

| Milestone                    | Goal                       | Exit criteria                                                                                                                                                                                                                                              |
| ---------------------------- | -------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **M0 — Skeleton**            | Prove the core loop        | cobra+`fang/v2`; `View` + `Error` v0; CLI + JSON renderers; exit codes; `sys` built-in. One capability, two renderers, in a pipe.                                                                                                                          |
| **M1 — Four surfaces + first-run DX** | The differentiator, proven | TUI shell (dashboard, palette, panes); **`rta mcp serve` with annotations + `rta mcp install claude`**; `net` + `cert` + `http`; `rta init` + `rta doctor`. **Gate: install → value < 60 s; same capability demoed from shell, TUI and Claude Code.** |
| **M2 — Plugin SDK + author DX** | Third parties possible  | `proto/v1` frozen; go-plugin host; `pkg/plugin` + `pkg/sdk`; host services; **`sdktest` public**; `rta plugin new` / `dev`; `examples/plugin-hello`; **`pg` as first external plugin** (exercises every View type). **Gate: stranger-simulated hello-plugin < 15 min.** |
| **M3 — Distribution + inbound AI** | Installable, extendable, agentic | goreleaser matrix; Docker images; OCI install + `--recommended`; `rta.lock` + cosign; exec-tier `$PATH` discovery; `s3` + `vault`; **profile tunnels (`ssh://`, `kube://`)** — retires the `backup-kube-*` scripts; **`ai` plugin (the `mods` rewrite)** incl. `rta ai` TUI pane. |
| **M4 — v1.0**                | Stable contract            | Semver commitment on `pkg/` + `proto/v1`; full docs + VHS demos; authoring guide; `rta plugin publish`; `etcd` + `qdrant`.                                                                                                                                 |
| **M5 — Beyond**              | Optional bets, by demand   | Web UI over the same registry; SSH-served TUI via `wish/v2`; encrypted profile sync (the `charm` rewrite); A2UI emission if it gains traction; WASM tier **only if** WASI p2 sockets have stabilised.                                                       |

**Critical path:** M1's MCP server is deliberately *before* the plugin API. It is a few days of work, it makes the tool immediately useful in a daily Claude Code workflow, and it forces the View contract to be genuinely renderer-agnostic before any third party depends on it.

---

## 12. Decisions (ADR seeds)

Formerly "open questions" — KISS means making the calls. Each becomes an ADR in `docs/adr/` when implementation starts.

| #   | Decision                                                                                                                                                                        | Rationale                                                                                     |
| --- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------- |
| D1  | **No *general* layout type in the View contract — no position, no size, no multi-pane.** `Sections` shipped as the one fixed exception: a flat, ordered list of titled child Views. The TUI shell owns everything about where things actually go on screen. | Layout-as-data is where contracts go to die; a fixed "list of titled parts" shape gets composed detail pages (§4.3) without reopening that door. |
| D2  | **One `Stream` type** with event kinds (`progress` \| `log` \| `metric`), not three types.                                                                                     | One streaming path in the proto; kinds are cheap, types are forever.                          |
| D3  | **Plugin lifetime: spawn-per-invocation in CLI; kept alive while a TUI session or `mcp serve` holds it.**                                                                      | Simple + safe for scripts; fast where latency is felt. Health-check only the long-lived case. |
| D4  | **Name: `rta`.** No Homebrew formula or notable CLI collision (checked 2026-08-17). Module `github.com/this-is-tobi/rule-them-all`.                                            | Lock early: binary, registry namespace, tap formula all inherit it.                           |
| D5  | **First-party plugins install on demand**, with `rta plugin install --recommended` and the `:full` Docker image for "everything now".                                           | Small core binary, honest plugin story, one-command fat path.                                  |
| D6  | **License: Apache-2.0** (+ `NOTICE` entry for MPL-2.0 `go-plugin`).                                                                                                            | Patent grant, corporate-friendly; matches fantasy/minio deps.                                 |
| D7  | **Wrap `fantasy`/`catwalk` behind `agent.Provider`; wrap go-plugin behind `pluginhost`.**                                                                                      | Pre-1.0 deps stay swappable; a swap must be a day's work.                                     |
| D8  | **Shared verb vocabulary enforced (as warnings) by `sdktest`.**                                                                                                                | Cross-plugin consistency is the DX moat; hard errors would fight legitimate domain verbs.     |
| D9  | **No telemetry.** Network calls happen only on explicit user action.                                                                                                           | Trust is a feature for a tool that holds DB and Vault credentials.                            |
| D10 | **Tunnels (`ssh://`, `kube://`) live in profiles, resolved by the host** — plugins only ever see a local `host:port`.                                                          | One orthogonal feature × N plugins; replaces the whole `backup-kube-*.sh` script family (§7.4). `kube://` lands with Wave 2 (it's what makes `pg`/`vault` useful in our own homelab). |
| D11 | **Three mechanisms gate what an MCP agent may do, composed rather than one: `Surface` (whether a call is allowed), `Field.Local` (credentials a remote caller may never supply), and time-boxed per-record `grant`s a human alone may issue or revoke.** Shipped ahead of schedule, in M1. | Safety class alone cannot express "leaks, though it mutates nothing" (`kv.get`) or "a person decided, for the next 15 minutes, on this one record". ADR 0005. |
| D12 | **Grants gained `MaxUses`/`Uses`: a grant can expire after N successful calls, on top of its TTL.** `MaxUses:0` (the default) is unlimited within the TTL — every grant issued before this field existed, unaffected. Consumption happens once, after the guarded call actually succeeds, never at the authorization check itself — a transient failure must not spend a one-time grant that revealed nothing. Shipped. | "TTL access" and "send to anybody" (the user's own words) turned out to be two different asks; this is the small, purely-local half — the transport half is D13/ADR 0006. |
| D13 | **One-shot/shared secret access splits into three separate answers, not one feature:** D12's grant `MaxUses` for an AI agent's own bounded, repeatable access; `vault.wrap.*` (Wave 2) for sharing within a Vault deployment the recipient can already reach; a new `share` plugin (Wave 3, deliberately late) built on the magic-wormhole protocol for an arbitrary, infrastructure-free recipient. A hosted relay `rta` itself would operate was considered and **rejected** — the "mandatory hosted backend we must operate forever" P7 warns against. `share.secret.set`/`get` refuse `SurfaceMCP` outright (the `grant.allow`/`grant.revoke` precedent) and are excluded from `--recommended`/`:full` (D5), since a claimed share is permanent, unrevocable, and has no far-end log — the highest-blast-radius write in the catalogue is not a case for the standard grant-alone gate. ADR 0006. | Investigated via multi-agent research + adversarial review (2026-08-18) after the user asked specifically whether "send one shot secret to anybody" was a real use case; it is, but conflates a local access-control problem with a genuinely new transport one. |
| D14 | **`kube` is read-first like `git` (§7.5), with one narrow, justified exception:** context/namespace/pod/deployment listing plus a composed overview, no describe/logs/exec/scale/delete — those stay `kubectl`'s job. `kube.context.set` is the one mutation, `Write`+`NeedsGrant` on a third trigger beyond §4.7's documented two: a quiet, reversible state change whose risk is what it silently enables afterward, not a leak or an RCE. Designed (§7, Wave 2), not yet built. ADR 0007. | Same reasoning the `git` plugin already established, applied to a second daily-use, high-temptation-to-overreach surface; the third `NeedsGrant` trigger is new and worth naming explicitly rather than stretching the existing two to fit. |
| D15 | **`docker` is *not* read-only, unlike `kube`/`git` — a bounded, individually-justified mutation set instead:** `container.stop/restart/rm` ship in the initial cut (the literal daily janitorial loop); `image/volume/network.prune` are **deferred** pending a cross-Engine-version conformance test, since the Engine API has no server-side dry-run for them and a wrong preview is worse than none (the `http` dry-run precedent, §4.7). `container.inspect` is `Write`+`NeedsGrant` (the `kv.get` precedent extended to a heuristic, not syntactic, redaction case: `Env` carries secrets by convention). Designed (§7, Wave 2), not yet built. ADR 0007. | Adversarial review of the initial design found the read/write line correctly reasoned in principle but under-applied to the plugin's own mutating half; ratifying the line here rather than leaving it implicit in a catalogue table, as that review asked. |
| D16 | **The security audit is its own plugin, not a corner of `net`** — `net.audit` → `audit.web`, taken now (v0.5, no external consumers, one golden-file diff) rather than after 1.0 when the same move costs a deprecation cycle. Two rules bound the namespace as it grows, in its package doc: it does not reimplement scanners (one interaction, read-only, safe against production — naming when to reach for ZAP or testssl.sh *is* the job), and every finding cites an OWASP Top 10:2025 category and a MITRE CWE. A one-capability plugin is an accepted starting state: waiting for a second capability would land it in the wrong namespace first and pay for the same break twice. ADR 0008. | The plugin was placed by its implementation (it speaks HTTP) rather than its purpose (is this app safe to expose). DNS-level email authentication, TLS depth and host posture are the obvious next entries and none of them belong under networking either. |

**Still genuinely open** (fine to defer — none block M0/M1):

1. `Stream` proto details (flow control, backpressure) — settle while building `pg backup`.
2. `x/vt` PTY compositing for exec-tier TUIs — M3 spike, not a commitment.
3. Whether `http` collections deserve their own file format or live in profiles — decide when building `http`.
4. **An "AI agent management" plugin — investigated (2026-08-18) and explicitly not designed further**, the same treatment as the WASM tier (§4.2): RTA's safety-class-plus-grant model gates actions RTA's own registry executes and can dry-run/audit; a capability that hands a task to an external, RTA-uncontrolled agent session has its real blast radius happen somewhere the model structurally cannot reach, which is a different problem from "needs a bigger grant". Distinct from the M3 `ai` plugin (§6.2), which is RTA driving its own bounded agent loop over RTA's own gated capabilities — not the same idea under a different name. Revisit only if a mainstream agent harness ships a stable, documented, external session-listing/control surface (the bar `git`/`gh`/`kubectl` already clear) *and* real demand shows up from RTA's stated sysadmin/DevOps/SRE audience, not solely from this project's own dogfooding context.

---

## 13. Sources

- [charmbracelet org](https://github.com/charmbracelet) — full 56-repo scan, GitHub API, 2026-08-17
- [fang](https://github.com/charmbracelet/fang) · [fantasy](https://github.com/charmbracelet/fantasy) · [bubbletea](https://github.com/charmbracelet/bubbletea) · [crush](https://github.com/charmbracelet/crush) · [a2tea](https://github.com/charmbracelet/a2tea) · [charmbracelet/x](https://github.com/charmbracelet/x)
- [hashicorp/go-plugin](https://github.com/hashicorp/go-plugin) · [RPC-based plugins in Go — Eli Bendersky](https://eli.thegreenplace.net/2023/rpc-based-plugins-in-go/)
- [Steampipe: Writing Plugins](https://steampipe.io/docs/develop/writing-plugins) · [Managing Plugins (OCI distribution)](https://steampipe.io/docs/managing/plugins)
- [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) · [MCP SDK betas for the 2026-07-28 spec](https://blog.modelcontextprotocol.io/posts/sdk-betas-2026-07-28/) · [MCP tool annotations as risk vocabulary](https://blog.modelcontextprotocol.io/posts/2026-03-16-tool-annotations/) · [Testing MCP tool annotations](https://sunpeak.ai/blogs/testing-mcp-tool-annotations/)
- [extism/go-sdk](https://pkg.go.dev/github.com/extism/go-sdk) · [Extensible Wasm Applications with Go](https://go.dev/blog/wasmexport) · [Wazero Hardening for Go Embedders](https://www.systemshardening.com/articles/wasm/wazero-hardening/) · [knqyf263/go-plugin](https://github.com/knqyf263/go-plugin) · [Helm HIP-0026: Wasm plugin system](https://helm.sh/community/hips/hip-0026/)
- [Extend kubectl with plugins](https://www.kubernetes.io/docs/tasks/extend-kubectl/kubectl-plugins/) · [gh extension management](https://deepwiki.com/cli/cli/4.6-extension-management)
- [this-is-tobi/tools](https://github.com/this-is-tobi/tools) · [this-is-tobi/dotfiles](https://github.com/this-is-tobi/dotfiles) — reviewed locally for §7.4, 2026-08-17
- Module versions verified via `proxy.golang.org` `@latest`, 2026-08-17
