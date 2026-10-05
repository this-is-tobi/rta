# The operator channel

A remote server closes the agent out of `grant allow` — and closes you out with it: its grant roster lived behind whatever infrastructure access reaches the machine. The operator channel is the way back in that an agent cannot use.

## Enroll a key

```bash
# on your machine, once
rta operator init --label tobi        # mints your key; prints the line below
# on the server, in a file only its owner can write
tobi 4Jx…base64…Qk=                   # one "label base64-pubkey" per line
# start the server with it
rta mcp serve --http :8443 --token-file tokens.txt \
  --operators operators.txt --operators-url https://rta.example.com
```

The label is how the roster and [the record](./40-audit-trail.md) name you, as `operator:tobi` in its credential column. `--label` sets it when the key is minted (it is `operator` otherwise), and `rta operator status --label <name>` prints the roster line for another label from the same key, without minting anything.

`--operators-url` is the server's canonical identity — the exact URL operators write in their `remotes.yaml` — and it is signed into every operator request. That is the anti-relay binding: a hostile server you also talk to could present another server's challenge as its own, but the envelope it collects names the server you were actually addressing and verifies nowhere else.

## Talk to the server

`--operators` mounts `/operator/v1` beside the MCP endpoint. Name the server in `remotes.yaml` beside your config —

```yaml
servers:
  work:
    url: https://rta.example.com
```

— and the existing verbs grow a `--server` flag. `rta grant list --server work` reads that server's roster, each grant's plugin build judged by the server against its own plugins; `rta operator status --server work` asks who it is (version, agent name, guard state, enrolled operators); `rta grant revoke kv --server work` takes authority back, with `--dry-run` previewed by the server's own store rather than guessed at from here; `rta agent pending --server work` reads its parked queue, and `rta agent allow`/`deny` answer it. Each call names its target, because an ambient "current server" is how a staging command lands on prod. One thing about the caller does not travel: the surface it called from. The envelope carries a verb and its arguments, and what comes back is worded on the server for a command line, so a TUI form given a server reads a remote refusal in flags rather than in its own boxes.

## Issue grants from your machine

Issuing remotely takes one more provisioning step, because a grant is authority and authority needs a signature the server will honour. On the server, once: `rta grant guard remote operators.txt --url https://rta.example.com` enrolls the roster's keys as the machine's [guard](./30-grants.md), bound to its canonical URL — after which a grant is honoured only when an enrolled operator signed it *for this server*, and `rta grant allow` at the server's own shell has no key to unlock, by construction. The binding is what keeps a fleet sharing one roster from becoming one trust domain: a grant signed for staging verifies on no other machine, however its bytes travel. Then, from your machine:

```bash
rta grant allow kv.get db-password --agent claude --ttl 15m --server work
```

`--agent` is the name the server's own `--as` was given, and it is always typed here: the agents this machine knows are this machine's, so none is filled in for a grant that is for the server's, and the call is refused before the passphrase is asked for. `rta operator status --server work` says what the server runs as.

The server *prepares* the grant — validation, TTL clamping against its policy, profile pinning, attribution — under its own config and catalogue; your rta then checks the draft against what you asked before anything is signed, field by field, with the server licensed only to clamp the lifetime downward — a compromised server must not be a signing oracle for authority nobody requested. Your passphrase unlocks the operator key; what survived the check is signed byte-for-byte and submitted; and the stored row carries `operator:<label>` in its Origin column, so a multi-operator server's listing names who issued what. The server re-checks everything on submission — attribution against the caller the envelope proved, untouched consumption bookkeeping, clock skew, expiry, both TTL ceilings — and the guard's own load-time enforcement then verifies the signature and its server binding on every read, like any other guard-signed row.

## Answer consent from your machine

[Live consent](./30-grants.md#live-consent-when-you-would-rather-be-asked) travels the same way, and it is what makes `--consent` legal beside `--http` at all: start the server with both plus `--operators`, and a call that parks waits for an enrolled operator rather than for nobody. `rta agent pending --server work` lists the queue, `rta agent show <id> --server work` reads one call in full, and `rta agent allow`/`deny` answer it — every answer signed under your passphrase, the one-shot included. That last part is a deliberate asymmetry with the local flow, where a bare `agent allow` is passphrase-free because it releases a call an agent with a shell could have run directly: that shell-equivalence argument does not travel a network, so remotely there is no passphrase-free answer. The binding is the digest the local flow already rests on, made to cross the wire: your machine derives it from the fields it *displayed* — never copies it from what the server sent — and the server compares it against the parked file at the moment the sealed decision is minted, so a queue entry that changed after you read it, or a server that showed you one call while parking another, produces a refusal instead of an approval. (`--ttl` and `--role` stay out of a remote answer, and are refused beside `--server`: a standing grant is the prepare-and-sign flow above, with its own review step.)

## Why an agent cannot ride it

What makes this channel one an agent cannot ride: every call is an ed25519 signature over the server's canonical URL, a single-use nonce the server just issued, the verb and its payload — and the signing key exists on your machine only inside a passphrase, the [guard](./30-grants.md)'s own mechanics pointed outward. The passphrase arrives through a prompt or the TUI's masked field, never from the environment, and is refused on the command line; so an agent that reads every file you own still cannot sign, a captured request replays nowhere and verifies on no other server, and an agent's bearer token opens nothing here — the two mechanisms never meet. The server, for its part, holds only public keys: compromising it forges no operator's hand.

## What lands in the record

Everything the channel *changes* is written into [the record](./40-audit-trail.md) beside the agent's own calls: a revocation, an issued grant, an answered consent, a lock placed or lifted — one line each, `operator.` in front of the verb, attributed `operator:<label>` in the credential column, refusals included. The record that shows a parked call `approved` therefore also shows who approved it, from which enrolled key. Reads stay off the record: a watching dashboard polls `status` every few seconds, and recording polls would churn real history out of the record's retention.

## The roster

The roster is the token file's kind of trust anchor and gets the same treatment: rta never writes it, weak permissions refuse startup, and it is read once — a rewrite behind a running server's back changes nothing until the next deliberate restart. Plain `http://` in `remotes.yaml` is refused for anything but loopback, and for the OIDC issuer's reason: the signature protects what you send, TLS protects what you *read* — a grant listing rewritten in transit is decisions made on a lie.

A roster line is `label base64-pubkey` — the exact line `rta operator status` prints on the operator's own machine — optionally annotated `role=read` and/or `expires=YYYY-MM-DD`. A read-only key answers `status`, `grant.list`, `consent.list` and `lock.list` and nothing else: no revocation, no issuance, no consent answers, and `grant guard remote` never enrolls it as grant-signing trust, so even its stolen key mints nothing. The intended occupant is a component rather than a person — a status page or dashboard watching the queue and the grants under its own key, with a blast radius of reads. A bare line stays what it has always been, a full operator; and anything unrecognized in the annotation position refuses the whole file, because a typo that silently meant "full" is the one failure a restriction must not have.

`expires=` turns a departure everyone knows is coming — a contractor's end date, a component being retired — from a memory problem into a clock problem: the key stops working when that day arrives, checked per call against the running server's clock, so this is the one roster edit that needs no restart to take effect. It only subtracts. The row still shows on the status page after its day, marked `expired`, because that row is a chore: deleting the line is still the real eviction, and an already-expired key stays out of what `grant guard remote` would enroll. A date rta cannot read refuses the whole file, same as any other annotation typo. For a departure nobody saw coming, that is not this — that is a [lock](./45-stop-an-agent-now.md).

## Next

[Containers and images](./67-containers-and-images.md) — the image a hosted instance runs from, and the one an agent should never be pointed at.
