# The record

Grants say what may happen next. The record says what already did — one line per call that arrived over [MCP](../95-reference/10-glossary.md#acronyms), one per authority change an operator made over [the remote channel](./66-operators.md), refusals included.

```bash
rta agent overview    # the last hour at a glance
rta agent log         # one line per call, oldest first — the latest is at the bottom
rta agent pending     # anything parked, waiting on you — see Grants for live consent
```

## What a line carries

Every call that arrived over MCP is written down: the capability, the records it named, the arguments with every input declared `Secret` or `SecretSlice` masked, the profile it resolved through, what happened, and **how it was authorized** — no grant needed, a standing grant, or you answering live.

The records are the ones a grant is compared with, kept exactly as the call spelled them whether the call needed a grant or ran without one, and `agent log` shows them in a `record` column the way [grants](./30-grants.md) are shown: quoted, each unseen character named by its code point, when one holds white space or a character that draws as nothing. The arguments are kept as a model may read them, cleaned of the zero-width, direction and tag characters, so the record column is the one that says which record a call named. A credential that an argument carries by its shape, in a field that is not declared a `Secret` because it is not one by its type, is masked too, in the arguments, the records and the reason a refusal gives: the userinfo of a URL (`https://***@host/`), a query parameter named like a token, key, password or signature, and a header such as `Authorization` or `Cookie` among a list of them. The host, the path and the rest of each header stay, since they are what the record is for. That is a heuristic for the common shapes, and a credential that looks like anything else is not recognised: the answer for one is a field declared for it. In the file itself, every character a reader would not see is written as its JSON escape, so `tail -f` shows it rather than hiding or reordering the line, and anything that parses the line reads the same value.

```bash
rta agent log --limit 50
rta agent log --refused        # only the calls rta would not make
rta agent log --since today    # a day, a span like 3d, or a date
rta agent log --agent claude   # one agent's calls
rta agent log --detail         # the full view, and the chain's integrity
```

At a terminal each call is one line: when, which capability, the record it named once any call named one, and what became of it — `ran`, `ran · by grant`, `ran · you approved`, `refused · no grant`, `refused · nobody answered`, `failed · <code>`. The agent joins the line when more than one agent is in what is shown, and the session when one agent's servers were calling in turn. The line leaves the arguments, the profile and the sentence for `--detail`, a pipe or `-o json`, which carry every field of every call. Past what the limit shows, a line under the table says how many older calls are not shown and what shows them.

`--refused` is the one to reach for first when something is not working. A refusal is a normal, designed outcome here, not an error condition — an agent asking for something it does not have is the system behaving correctly, and the log is where you find out what it wanted.

Refusals come from two depths, and the authorization column, which `--detail` and every machine format carry, tells them apart: `blocked` means the call never cleared rta's own gates — a missing grant, a bad argument, a locked principal — while `open` or `grant` on a refused row means the gates allowed it and something past them still said no: the capability's own policy (the way `agent.*` and `lock.*` refuse any caller over MCP, or `pg.dump` refuses to hand a whole database to an agent), or a value your config or the profile supplies that the capability does not take, whose reason names the key it came from and whose grant use is given back. Both are refusals, not failures: `failed` is reserved for calls that were allowed and then broke.

## Who asked

Two names appear, and they are not the same kind of thing:

- **The agent name** — the one you typed at `rta mcp serve --as` or `rta mcp install`. This is what authorizes. It is your word.
- **The client's own claim**, in parentheses — what the MCP client announced about itself in the protocol handshake.

rta records the claim for provenance and authorizes on the name you gave, because *a name a thing chooses for itself is not an identity*. A client can call itself anything; only you can say which principal it is.

## Operators appear too

A remote operator's mutations land in the same record: a revoked or issued grant, an answered consent, a lock placed or lifted — each on its own line with `operator.` in front of the verb (`operator.lock.add`), the credential column naming the enrolled key (`operator:dash`), and `operator` in the authorization column, because the signature was the authorization, not any grant. So the row that shows an agent's call `approved` has a partner row naming who approved it, from which key. A grant issued or revoked on a record keeps that record in the `record` column, exactly as the signed call spelled it, as an agent's call keeps its own.

The channel's reads stay out, as do the commands you type at the machine itself: the record answers "what happened while I was away", and a status poll every few seconds would churn real history out of retention to say nothing.

## The chain

The record is **hash-chained**: each entry commits to the one before it, so an entry that was edited or removed breaks the chain visibly.

```bash
rta agent log --detail
```

The calls come first, and under them a section about the record itself, which is where an edited line shows:

```
THE RECORD ITSELF
file     /home/you/.local/share/rta/agent-log.jsonl
entries  323
size     223.1 KiB
chain    BROKEN at entry 323 — its contents do not match its seal
```

A whole record says `chain  whole — every entry follows the one before it, matches its seal, and the record ends where rta last left it`. `rta doctor` surfaces a break without you asking:

```
agent log   warn   the record of agent calls breaks at entry 323 — its contents do not match its
                   seal; `rta agent log --detail` shows it
```

**What this does and does not buy you.** It makes tampering *visible*, not impossible. The seals are checked against `agent-log.key` beside the record, so anything that can read that file — any process running as you — can rewrite the whole chain and reseal it, and deleting every file leaves nothing to notice with. What it prevents is the quiet edit — removing one embarrassing line and leaving the rest intact — which is the realistic threat for a local file, and it is exactly the sort of thing an agent with filesystem access might attempt. A mark records where the record ends, so a truncation shows; if the mark itself goes, the next call writes a sealed admission into the chain (`end mark was missing before entry N` under `--detail`) rather than healing it in silence. The only defence against deletion is a copy elsewhere: see [Ship the record somewhere durable](../90-recipes/01-readme.md#ship-the-record-somewhere-durable).

The record keeps about 64 MB — eight files of 8 MB — and drops the oldest past that, recording what it dropped as a sealed `retired` note the chain still verifies across. A day with no rows before a certain hour is not a day where nothing happened; it is where retention starts, and the recipe above is how you keep more.

A refusal is a row too, so a caller looping on a tool that refuses it would be writing that history away. Past twenty refusals in a minute, each further one from the same caller is answered slower, doubling up to five seconds — which turns minutes of churn into weeks, and costs a caller that reads its refusals nothing.

A call that succeeds is a row too, and a loop of free reads wrote some 1,800 of them a second: the whole 64 MB was gone in about two minutes. A call that needs no grant is therefore held to a rate per caller — 600 calls in hand, regained at five a second — and one that finds none waits, before it runs, for the call it owes. At most sixteen calls of one caller wait at once, so a client that sends its calls without waiting for the answers is held to the same rate as one that waits for each. An agent working a task, whose calls come seconds apart because a model sits between them, never meets it; a loop is held to five rows a second, which turns minutes of churn into about twelve hours. A call spending a grant or answering a question you were asked is not paced: it is already counted by the grant and decided by you.

**A call that needs a grant is not run when the record cannot be written.** A full disk, a directory gone read-only, or something sitting where the record goes would otherwise have let a granted write or destroy run with no row, and the one trace was a count on the next row that did get written. Before it spends a grant, asks you or runs, such a call writes a scratch row's worth to the data directory and checks the record takes an append; when that fails the call is refused as `core.record.unwritable`, with `rta doctor` as the way on and no path in what the agent reads, and the grant keeps its use. A read that needs no grant still runs: it spends nothing, and refusing every read of a machine whose disk is full would take `sys disk`, the tool that finds the full disk, with it. Those reads are not recorded, which `rta doctor` reports as an error and `rta agent overview` as a `recording` row, and the count of what was not written rides on the first row that is. The check cannot promise the write that follows it: space can run out in between, and that call is the one the count is for.

## History, not policy

The distinction is worth keeping straight:

| Question | Command |
| --- | --- |
| What may happen next? | `rta grant list` |
| What already happened? | `rta agent log` |
| What is waiting on me right now? | `rta agent pending` |

The log never authorizes anything. Deleting it takes away your evidence and grants nothing.

## Reading it as data

Like every rta command, the log renders in whatever shape you need:

```bash
rta agent log --refused -o json          # the refusals, as data
rta agent log -o csv >> ~/audit/$(date +%F).csv
```

Every refused or failed row carries the cause twice, deliberately split: a `code` column holding just the dotted, stable code (`core.grant.required`, `agent.surface`), and a `why` column holding the sentence — for a call refused because a grant it ran on is spent or has lapsed, `the grant for <target> ended (1 of 1 use)`, and for any other missing grant `no active grant for <target>`. Match on the code — it is the contract a [SIEM](../95-reference/10-glossary.md#acronyms) or jq rule can rely on across versions; the wording is not.

Which makes "ship the record somewhere durable" a cron line rather than a feature request.

## Related

- [Grants](./30-grants.md) — what the record is a record of
- [Team policy](./50-team-policy.md) — bounds nobody on the team can raise

## Next

[Stop an agent now](./45-stop-an-agent-now.md) — a lock, the instant no, ahead of any expiry or revocation.
