# AGENTS.md

Guidance for AI coding agents (and anyone using one) working in this repository — rta, a security boundary for AI agents: one binary exposing capabilities (databases, storage, secrets, networking, git, host info…) over a CLI, a TUI, and an MCP server, each gated by per-capability consent, scoping, and an audit trail. Read [docs/19-the-boundary.md](docs/19-the-boundary.md) and [docs/20-mcp.md](docs/20-mcp.md) before touching anything MCP- or capability-related — they carry the design reasoning a diff alone won't show.

## Priority order

1. Security fixes and bug fixes before any new feature.
2. Investigate the cost, benefit and implications of a new feature — and say so — before writing code for it. An exploratory question gets a recommendation and the main tradeoff, not an implementation, until agreed.
3. Prefer refactoring or improving an existing feature over adding a new one, unless the new feature is critical for user needs or security.
4. Extendable by design. The reference shapes are Mise's registry model and the Kubernetes API + CRD pattern — a plugin system people can build on, not a closed list rta alone maintains.
5. Everything battle-tested: run the tests, run `make ci`, don't report something as working without having run it.
6. Watch binary size when adding a dependency or a feature; it's a stated constraint, not an afterthought.

## Working here while the project is private and pre-1.0

Breaking changes are welcome when they buy security, performance, binary size or extendability — no compatibility shims, no deprecation cycles, no versioned config migrations. Take the cleaner shape outright. This note stops applying the moment the project has real external users; don't assume it still holds without checking.

## Git workflow

- Never mention AI, an agent, or a specific AI tool in a commit message or PR description.
- Conventional Commits — enforced in CI (`lint-commits.yml`).
- Commit locally as normal; small, focused commits are expected. Never `git push`, open a PR, or suggest either without an explicit go-ahead, even once a remote exists.
- Changes to CI/CD (`.github/workflows/*`, release-please config, GoReleaser config) get an explicit review before even a local commit. This repo's CI/CD is what would ship a compromised release — treat it with the same care as the product, not as ordinary tooling.
- Before any command that could discard uncommitted work (`git checkout`/`restore`/`reset`/`clean`), run `git status` first.

## Code style

- No comments explaining *what* code does — names should already say that. When the *why* is genuinely non-obvious (a hidden constraint, a bug a naive reading would reintroduce, a tradeoff a future reader would otherwise redo from scratch), write it, at whatever length that takes. This codebase's own comments are the calibration: they run long when the reasoning is long. Read a few near what you're touching before adding your own, and match their depth rather than defaulting to a one-liner.
- Match existing patterns before introducing a new one. If two approaches already coexist in the codebase, ask which is preferred rather than adding a third.
- Errors are `*view.Error` (`pkg/view`), named by a dotted code and addressed to whoever can act on the message — not a generic wrapped `error`.

## Before implementing

- Read the relevant existing code first. Design rationale lives in the comments, not in the diff that produced them, and this codebase has a lot of it.
- `.local/` holds working notes and ADRs and is untracked. Never cite one from a shipped code comment or doc — it won't exist for whoever reads that comment later.
- Plugin trust binds to a digest, never to a name or a version (`rta plugin trust`, `rta plugin install`). A manifest's `version` field is the index's claim, recorded for display, and nothing resolves through it — don't build a feature that treats it as an enforced guarantee without calling that out as the design change it would be.

## Verifying changes

- `make ci` runs the gates CI runs (lint, race-mode tests, build, cross-compile). Capture its exit code directly — `make ci > log 2>&1; echo $?` — never infer success from a pipe or a partial log tail.
- No heredocs (`cat <<EOF`) for generating file content — one mangled a Go test file once. Use a file-writing tool or a small script instead.
