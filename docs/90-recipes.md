# Recipes

Worked examples. Each one is a real shape rather than a demonstration of a flag.

## Pair with an agent on a staging database, for an hour

The common case, and the one worth learning first. You want your editor's agent to help debug a slow query against staging — not production, not forever, and with a record afterwards.

```bash
# 1. Take production off the table entirely, for as long as you are working.
rta use staging --for 2h

# 2. Let one named agent read that one environment.
rta grant allow pg.query --profile staging --agent claude --ttl 1h \
  --rate 30/1h --note "slow query on orders"

# 3. Work. Then see what actually happened.
rta agent log --limit 50
```

Three bounds, each doing a different job:

- **`rta use staging`** means `rta mcp serve` refuses every other profile, whatever grants exist. It cannot grant anything — only take away — so it is safe to reach for first.
- **`--agent claude`** keeps the grant off your other MCP clients.
- **`--rate 30/1h`** is the one people skip. A time bound and a use bound both fail the same way: an agent in a loop exhausts them correctly and instantly. A rate limit turns a runaway into something you can notice while it is happening.

When the hour is up, all of it lapses on its own.

## Hand over one secret, once

```bash
rta grant allow kv.get deploy-token --ttl 5m --max-uses 1
```

One key, five minutes, one read. The clearest case in the whole model — and the record shows that `kv.get deploy-token` happened without showing what came back.

To make the broad form impossible for everyone on the team, put it in the repository:

```yaml
# .rta-policy.yaml
requireScope:
  - kv.get
```

Now `rta grant allow kv.get` — which would cover the entire store — is an error. Only a grant naming one key is accepted. See [Team policy](./23-team-policy.md).

## A ceiling a repository carries

Commit this next to your code and every clone inherits it:

```yaml
# .rta-policy.yaml
maxTTL: 15m
never:
  - pg.dump
  - vault.kv.get
neverProfile:
  - production
requireScope:
  - kv.get
  - s3.object.get
```

No seal, no key distribution, no trust in how it reached the machine — because **it can only subtract**. The worst a hostile edit achieves is making rta refuse more than you wanted.

A subdirectory may tighten it and cannot loosen it, so a service inside a monorepo can be stricter than the repository root without any mechanism for that.

## Morning triage

```bash
rta sys overview
rta agent overview
rta doctor
```

`sys overview` is host health grouped rather than seven commands. `agent overview` is the last hour of agent calls, how many were refused, and anything parked waiting on you. `doctor` is what rta can reach — read the `info` rows, not just the failures.

Then the detail on whatever looked wrong:

```bash
rta sys overview --detail
rta agent log --refused
```

## Certificate expiry as a cron job

```bash
rta cert expiry example.com api.example.com -o json \
  | jq -r '.rows[] | select(.[2] | tonumber < 30) | .[0] + " expires in " + .[2] + " days"'
```

Exit codes make it a gate rather than a report:

```bash
rta cert expiry example.com || echo "check failed" >&2
```

## Dependency review before a release

```bash
rta audit deps -o md >> release-notes.md
rta audit why some-package
```

`audit deps` checks what you already declare against OSV, and each hit says whether **you** asked for that package or something else pulled it in — which is the difference between a fix you make and a fix you wait for. `audit why` draws the whole route from the lockfile.

## A security review you can paste into an issue

```bash
{
  rta audit web example.com -o md
  rta audit mail example.com -o md
  rta cert chain example.com -o md
} > review.md
```

Every finding cites the OWASP Top 10 category and the CWE it comes from, so the output is reviewable by somebody who was not in the room.

## Fill a shell with credentials, without them touching disk

```bash
eval "$(rta kv env --prefix APP_)"
```

Or for a tool that wants a file:

```bash
rta kv get tls-cert --out /tmp/server.pem   # written at 0600
```

## Answer "what changed here" without parsing porcelain

```bash
rta git overview
rta git status -o json | jq '.rows'
rta git blame internal/grant/grant.go
```

`git overview` is the branch, what it tracks, how far it has drifted, any rebase or merge left half-finished, staged/modified/untracked told apart, and the last commit's age. It works against a local checkout or a remote URL cloned in memory, and it is read-only.

## Run an agent against a scratch directory only

```bash
cd /tmp/scratch
rta mcp serve --as sandbox --root /tmp/scratch
```

Every path argument must sit under a root, and the default root is the directory the server started in. rta prints the roots at startup rather than leaving them to be discovered from a refusal.

Widen deliberately, never by default:

```bash
rta mcp serve --as sandbox --root ~/projects --root /tmp/scratch
```

## Be asked instead of refused, while you are at the machine

```bash
rta mcp serve --as claude --consent --consent-notify --allow-write todo
```

A call needing a grant nobody issued parks rather than failing:

```bash
rta agent pending
rta agent show 3       # including what it would do, from its own --dry-run
rta agent allow 3
```

Answering `allow` runs that one call and creates no standing grant.

**Only turn this on when you are actually present.** A call parked in a server nobody is watching is worse than a refusal: the agent hangs and the timeout is the only thing that resolves it. That is why it is off by default.

## Ship the record somewhere durable

```bash
rta agent log -o csv --limit 1000 >> ~/audit/rta-$(date +%F).csv
```

The record is hash-chained, so an edited or missing entry is visible:

```bash
rta agent log --detail
```

That makes tampering *visible*, not impossible — which is the realistic goal for a local file, and enough to catch the quiet single-line edit.

## Check a machine is set up, without unlocking anything

```bash
rta kv status      # where the store is and what can open it
rta profile list   # configured environments, and whether each is usable
rta doctor
```

`kv status` answers without unlocking the store, which makes it safe in a provisioning script.

## Next

- [Grants](./21-grants.md) · [Team policy](./23-team-policy.md) · [The record](./22-audit-trail.md)
- [Writing a plugin](./writing-a-plugin.md) — if the capability you want does not exist yet
