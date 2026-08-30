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

| Trusting it | [Grant file](./21-grants.md#the-file-and-why-it-is-sealed) | Policy file |
| --- | --- | --- |
| A forged line… | **adds** permission | **removes** permission |
| So it needs… | a tamper seal | nothing |
| Which means… | keys, distribution, verification | `git add .rta-policy.yaml` |

A hostile edit to a policy file makes rta refuse *more* than you wanted. That is loud, immediate, and safe. So the file needs no seal, no key distribution and no trust in how it reached you — which is exactly why it can live in a repository and travel with a clone.

## The thing that property does not cover

Read that table again and notice what it is about: a forged **line**. It says nothing about the file not being there at all, and that is a different attack with the opposite failure mode.

| What happens to `.rta-policy.yaml` | What rta does |
| --- | --- |
| A line is added or tightened | refuses more — safe, and the point |
| It is corrupted so it will not parse | **every grant load fails**, loudly |
| It is deleted | *(without the setting below)* runs with no ceiling |
| Its contents are replaced with `{}` | *(without the setting below)* runs with no ceiling |

The clumsy edit fails closed. The clean ones fail open, and quietly — because a machine whose policy vanished is indistinguishable from a machine that never had one. Deletion is not exotic either: a branch that predates the file, a bad merge, a `git clean -xdf`, a sparse checkout, or an MCP client that launched rta from your home directory rather than the repository.

**A file in the repository cannot defend against this**, because a policy demanding a policy is deleted along with its own demand. So the demand lives somewhere else:

```bash
rta policy require
```

That writes `requireRepoPolicy: true` into **your own** policy file, beside your config and outside every repository. From then on, a missing or empty `.rta-policy.yaml` is a refusal that names the directory rta searched:

```
ERROR policy.repo.missing  no .rta-policy.yaml found from /home/you/project, and this machine requires one
HINT  either you are not in the repository you meant to be in — an MCP client launches rta
      from a directory it chooses, not one you do — or the file was removed.
```

It requires the file to **constrain something**, not merely to exist, because `{}` parses perfectly well.

This keeps the subtract-only property: it can only ever cause more refusals.

### The other half, which needs no mechanism at all

Your own policy file is a policy file. Anything you put in it intersects with the repository's and survives the repository's disappearing:

```yaml
# ~/.config/rta/policy.yaml — yours, not the team's
maxTTL: 1h
neverProfile:
  - production
```

Now the repository can tighten those and cannot loosen them, and if `.rta-policy.yaml` is deleted you still have a ceiling. `rta policy require` catches the *disappearance*; your own floor limits the *damage*. They are worth having together.

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

What the argument covers is a file being **found** somewhere unexpected. It says nothing about one not being found at all, which is the direction that actually loses you a ceiling — see [above](#the-thing-that-property-does-not-cover).

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

## Setting one up

```bash
rta policy init        # a commented .rta-policy.yaml here, every axis named
rta policy require     # and this machine now refuses to run without one
rta policy show        # what is in force, and where rta looked
```

`policy init` writes all four axes with one active, so tightening it later is an edit rather than a trip back to this page. Commit it.

## Checking what is in force

```bash
rta policy show
```

```
searched from      /home/you/project
repository policy  /home/you/project/.rta-policy.yaml
your own policy    /home/you/.config/rta/policy.yaml
maxTTL             15m0s
never              pg.dump, vault.snapshot
requireRepoPolicy  yes
```

`rta doctor` carries the same fact in one line — **including when there is nothing in force**, which is the case that needs saying:

```
team policy   info   none in force — no .rta-policy.yaml found from /home/you, and no
                     policy beside your config. `rta policy require` makes a missing one
                     an error instead of silence
```

Grants suppressed by a ceiling are reported rather than hidden, so "why can't the agent do this" has an answer that does not require reading the file.

## Where an MCP server looks, and why it may surprise you

The walk up starts at the working directory, and **for an MCP server that directory is chosen by the client, not by you.** A team can commit `.rta-policy.yaml`, wire up a client that starts in `$HOME`, and get no ceiling at all.

So `rta mcp serve` says which one it found, next to the path roots and for the same reason:

```
rta mcp server listening on stdio
path arguments confined to: /Users/you/projects
rta: team policy: /Users/you/projects/.rta-policy.yaml
```

If that line reads `none in force`, the policy you committed is not the one bounding this agent. `rta policy require` turns that from a line somebody has to notice into a server that will not start.

## What it is not

**It is not a substitute for the startup gate.** A policy cannot expose a write capability, and cannot widen a path root. It only ever narrows what a grant may say.

**It is not RBAC.** There are no roles, no subjects and no allow rules, and that is a deliberate refusal. A policy engine that can grant has to be trusted, distributed and verified; one that can only refuse needs none of that. The cost is that it cannot express "these five people may do this" — and the benefit is everything in the table at the top of this page.

**It is not a control over a machine somebody else owns.** An operator owns their binary, their config and their filesystem, so they can always unset `requireRepoPolicy` and delete the file. Saying so is the point rather than a caveat: a bound that reports itself without being enforced is worse than no bound at all.

What all of this defends against is the failure teams actually have — the 3am "allow everything for twenty-four hours so it stops failing", and the quieter version where a ceiling somebody committed months ago stopped applying and nobody noticed. Neither of those is an attacker. Both of them are Tuesday.

## Next

- [Grants](./21-grants.md) — what the ceiling is bounding
- [The record](./22-audit-trail.md) — what happened inside those bounds
