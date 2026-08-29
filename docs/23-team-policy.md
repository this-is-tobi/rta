# Team policy

A grant is one person's decision on one machine. A **policy** is the boundary that decision has to fit inside — a file a team commits, that nobody's `--ttl` can argue with.

```yaml
# .rta-policy.yaml
maxTTL: 15m
never:
  - pg.dump
  - vault.snapshot
neverProfile:
  - production
requireScope:
  - kv.get
```

## The one property that makes it work

**A policy can only ever subtract.**

There is no `allow:` key and there never will be. Every axis in the file removes something an operator could otherwise have done: caps a duration, forbids a target, forbids a connection, or demands that a grant name one record instead of covering everything.

That single property is what makes this shareable. Consider what it takes to trust the two files:

| | [Grant file](./21-grants.md#the-file-and-why-it-is-sealed) | Policy file |
| --- | --- | --- |
| A forged line… | **adds** permission | **removes** permission |
| So it needs… | a tamper seal | nothing |
| Which means… | keys, distribution, verification | `git add .rta-policy.yaml` |

A hostile edit to a policy file makes rta refuse *more* than you wanted. That is loud, immediate, and safe. So the file needs no seal, no key distribution and no trust in how it reached you — which is exactly why it can live in a repository and travel with a clone.

## The four axes

| Key | Effect |
| --- | --- |
| `maxTTL` | Caps how long any grant may stand. A `--ttl` above it is clamped, and the operator is told which rule did it |
| `never` | Targets — capability IDs or plugin names — that may not be granted at all |
| `neverProfile` | Connections no grant may name. The blunt instrument for "not production, ever" |
| `requireScope` | Targets that may only be granted against a named record, never wholesale |

`requireScope` is the subtle one and the most useful. `rta grant allow kv.get` covers the entire store; `rta grant allow kv.get db-password` covers one key. Listing `kv.get` under `requireScope` makes the first form an error, so a hurried grant cannot accidentally be the broad one.

## Where the file is found

Three sources, all optional, and **every one that is found intersects** — none overrides:

1. `RTA_POLICY`, an explicit path.
2. The operator's own, beside their config.
3. `.rta-policy.yaml`, walking up from the working directory.

```
repo/.rta-policy.yaml          maxTTL: 15m   never: [pg.dump]
repo/services/api/.rta-policy.yaml   maxTTL: 24h   never: [vault.snapshot]

  → effective:  maxTTL 15m,  never: [pg.dump, vault.snapshot]
```

A subdirectory may tighten and must not loosen. The inner file's `24h` is ignored because it is looser; its extra prohibition is honoured because it is tighter. Strictest wins on every axis, which needs no separate mechanism — it is the subtract-only property again.

`RTA_POLICY` is the one exception to "all optional": naming a file that is not there is a mistake somebody made, not a machine without a policy, so it fails rather than running with no ceiling.

## Why walking up from the working directory is safe

Ordinarily, reading a config file out of whatever directory you happen to be standing in would be a bad idea. Here it is safe **because of** the subtract-only property: the worst a file planted somewhere can achieve is making rta refuse more than you wanted, which you find out immediately.

rta already reads `./.rta.yaml` as a config fallback, which is a strictly larger trust surface than this one.

## Enforced on the way out, not at issue

The ceiling is applied where every authorization path already goes — when a grant is *loaded* — rather than when one is issued.

The difference matters. A ceiling checked only at issue would live in the CLI, and any grant that predated the policy would escape it. Checking on the way out makes the cap **a property of what a grant can do**, rather than of how one was asked for. Add a policy today and yesterday's over-broad grants are bounded by it from the next call onward.

```bash
rta grant allow pg.query --ttl 2h
```

```
✓ granted pg.query for 15m
  clamped from 2h by maxTTL in ./.rta-policy.yaml
```

It names which rule did it and where that rule lives, because a bound that silently shortens is indistinguishable from one you mistyped.

## Checking what is in force

```bash
rta doctor
```

```
team policy   ok   ./.rta-policy.yaml — maxTTL 15m, 2 targets never, 1 profile never
```

Grants suppressed by a ceiling are reported rather than hidden, so "why can't the agent do this" has an answer that does not require reading the file.

## What it is not

**It is not a substitute for the startup gate.** A policy cannot expose a write capability, and cannot widen a path root. It only ever narrows what a grant may say.

**It is not RBAC.** There are no roles, no subjects and no allow rules, and that is a deliberate refusal. A policy engine that can grant has to be trusted, distributed and verified; one that can only refuse needs none of that. The cost is that it cannot express "these five people may do this" — and the benefit is everything in the table at the top of this page.

## Next

- [Grants](./21-grants.md) — what the ceiling is bounding
- [The record](./22-audit-trail.md) — what happened inside those bounds
