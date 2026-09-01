# The record

Grants say what may happen next. The record says what already did — one line per call that arrived over MCP, refusals included.

```bash
rta agent overview    # the last hour at a glance
rta agent log         # one line per call
rta agent pending     # anything parked, waiting on you
```

## What a line carries

Every call that arrived over MCP is written down: the capability, the arguments with every input declared `Secret` or `SecretSlice` masked, the profile it resolved through, what happened, and **how it was authorized** — no grant needed, a standing grant, or you answering live.

```bash
rta agent log --limit 50
rta agent log --refused        # only the calls rta would not make
rta agent log --detail         # the full view, and the chain's integrity
```

`--refused` is the one to reach for first when something is not working. A refusal is a normal, designed outcome here, not an error condition — an agent asking for something it does not have is the system behaving correctly, and the log is where you find out what it wanted.

## Who asked

Two names appear, and they are not the same kind of thing:

- **The agent name** — the one you typed at `rta mcp serve --as` or `rta mcp install`. This is what authorizes. It is your word.
- **The client's own claim**, in parentheses — what the MCP client announced about itself in the protocol handshake.

rta records the claim for provenance and authorizes on the name you gave, because *a name a thing chooses for itself is not an identity*. A client can call itself anything; only you can say which principal it is.

## The chain

The record is **hash-chained**: each entry commits to the one before it, so an entry that was edited or removed breaks the chain visibly.

```bash
rta agent log --detail
```

```
agent log   warn   the record breaks at entry 24 — nothing records where this
                   record is supposed to end, so entries could have been removed
                   without trace
```

`rta doctor` surfaces the same finding without you asking.

**What this does and does not buy you.** It makes tampering *visible*, not impossible. Anything that can write the file can rewrite the whole chain from the break onward. What it prevents is the quiet edit — removing one embarrassing line and leaving the rest intact — which is the realistic threat for a local file, and it is exactly the sort of thing an agent with filesystem access might attempt.

## History, not policy

The distinction is worth keeping straight:

| Question | Command |
| --- | --- |
| What may happen next? | `rta grant list` |
| What already happened? | `rta agent log` |
| What is waiting on me right now? | `rta agent pending` |

The log never authorizes anything. Deleting it takes away your evidence and grants nothing.

## Parked calls

With `rta mcp serve --consent`, a call needing a grant nobody issued is parked instead of refused:

```bash
rta agent pending
rta agent show 3
rta agent allow 3
rta agent deny 3
```

`rta agent show` includes what the call **would do** — rta runs the capability's own `--dry-run` and puts the result on the parked request. That changes the question from *"may this agent call `todo.rm`"* to *"may it remove **this task**"*, which is the question you can actually answer.

Preview is on by default (`--consent-preview`). Turn it off for a capability whose dry run is expensive — something an operator discovers, not something rta can know in advance.

Answering `allow` runs that one call and creates no standing grant. Ask again, get asked again.

## Reading it as data

Like every rta command, the log renders in whatever shape you need:

```bash
rta agent log -o json | jq '.rows[] | select(.[3] == "refused")'
rta agent log -o csv >> ~/audit/$(date +%F).csv
```

Which makes "ship the record somewhere durable" a cron line rather than a feature request.

## Next

- [Grants](./21-grants.md) — what the record is a record of
- [Team policy](./23-team-policy.md) — bounds nobody on the team can raise
