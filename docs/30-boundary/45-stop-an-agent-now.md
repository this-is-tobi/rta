# Stop an agent now

Three things take reach away from an agent, and they differ in how much they stop and how fast:

| You want to… | Run | What it does | What it leaves alone |
| --- | --- | --- | --- |
| End what you allowed | `rta grant revoke --all` | Takes every grant back, now | Reads that need no grant still run, and the agent's token still opens them |
| Make one agent do nothing at all | `rta lock add claude` | Refuses every call it makes from its next one, reads included, with no restart | The grants stay on file; `rta lock rm claude` lifts it |
| Keep other environments out of reach while you work | `rta use staging` | Refuses every profile but that one, whatever grants exist | Everything that names no profile |

The lock is the one for an incident, and it is the rest of this page. Revoking and switching are in [Grants](./30-grants.md#seeing-and-taking-back) and [Profiles](../20-using/40-profiles.md).

## Locks: the instant no

Expiry and revocation both leave a gap that only shows during an incident: revoking every grant still leaves a misbehaving agent's bearer token opening the ungated read tools, and a compromised operator key stays enrolled until someone edits the [roster](../95-reference/10-glossary.md#terms) and restarts — the roster is deliberately read once. A **lock** is the instant path:

```bash
rta lock add claude --note "runaway loop, ping me"       # on the machine
rta lock add dash --kind operator --server work          # or from your machine, signed
rta lock list
rta lock rm claude
rta lock claude                                          # the same as lock add: a name after lock is enough
rta lock add --all                                       # every agent, when you do not know which one
rta lock rm --all
```

`--all` is the handle for the night you cannot tell which agent it is: it refuses every agent on its next call, the ones running and any that connects later, and `rta lock rm --all` lifts only that lock and names the locks on single agents that stand. A name no agent has used here is locked all the same, because a lock may be placed before the agent exists, and the answer says so and offers the name it knows nearest, so a misspelling in a hurry is seen when it is made and not when the real agent is still running.

A locked *agent* or *credential* is refused on every tool call before any other gate (the protocol's own handshake and catalogue listing still answer — nothing executes through them) — never parked as a consent question, because a lock is the "stop asking me" control — and a locked *operator* label gets no verb on the channel at all. Running servers pick a lock up on their next request, no restart, and the note travels to the locked party on every refusal. Locks only subtract, so placing one asks for no passphrase: revoking never asks, and an incident is the wrong moment to demand a secret. `--ttl 2h` makes one lift itself; without it a lock stands until `rta lock rm`.

Two edges worth knowing before you need them. First lock wins: a locked operator cannot unlock anyone, themselves included, so a fully locked-out roster is recovered at the machine's own terminal — where `rta lock` always works, because the person standing there is the authority locks answer to. And the lock file is sealed like the grant file, with the guard's failure direction: a running server that saw locks keeps enforcing them even if the file is deleted out from under it, so the `rm` that would quietly restore access restores nothing for the process the attacker is talking through. A server that has no verified set to keep, started after the file was written over, refuses every call as `core.lock.unverified` until the file verifies: a file that is there and is not rta's is not the absence a machine with no locks has. Lifting a lock is `rta lock rm`, on the machine or as a signed operator call — never a file deletion.

Locking an operator freezes the key, not what it already signed — pair it with `rta grant revoke` for anything that key issued. It silences the key's verbs, not its ink: a locked key's mutation attempts keep landing in [the record](./40-audit-trail.md) as refusals, which is the evidence trail working — and also why a key you believe compromised is one to remove from the roster (edit the `--operators` file, restart), not merely to lock forever: enrollment is what lets it make the server write anything at all. And `rta lock` is on the harness deny list `rta audit clients --fix` prints, for the expanding half: an agent that could run `lock rm` would be unfreezing itself.

## Next

[Team policy](./50-team-policy.md) — a ceiling nobody on the team can raise, which is where this track ends and running it for others begins.
