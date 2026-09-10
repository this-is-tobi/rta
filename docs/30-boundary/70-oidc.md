# OIDC

`rta mcp serve --http` refuses to start without a way to verify who is calling. There are two, and they answer different questions. A static token from `--token-file` names whoever holds the string. An OIDC issuer names a person your identity provider already knows, which is what makes "one instance per person, authenticating as that person" mean anything — the record then carries a subject your IdP can resolve back to a human, not a label somebody chose.

This page is what rta actually verifies, a Keycloak walkthrough because it is the provider with the sharpest edge, and the failure messages, which go somewhere you might not look.

## The three flags

```bash
rta mcp serve --http 0.0.0.0:8080 \
  --oidc-issuer https://id.example.com/realms/main \
  --oidc-audience rta \
  --oidc-subject 8f14e45f-ceea-467a-9c1a-2d7c4f0b1a33
```

All three are required together, and the two refusals say why:

- `an --oidc-issuer needs --oidc-audience` — without it, a token minted for any other service by the same issuer authenticates here.
- `an --oidc-issuer needs at least one --oidc-subject — an issuer and audience alone identify an application, not a person` — without it, every identity that issuer will ever mint is accepted, which is the whole namespace of your IdP rather than one person.

`--oidc-subject` is repeatable and comma-splitting.

## What rta checks, exactly

**Discovery happens once, at startup.** rta fetches `<issuer>/.well-known/openid-configuration` before accepting anything, with a 15-second timeout, and **refuses to start if the issuer is unreachable**. That is deliberate: a server that starts and then rejects every call looks healthy to an orchestrator and is not. The listener is bound before discovery, so a port clash reports first.

The discovered document's own `issuer` field must equal what you passed. A trailing slash difference is a mismatch.

**The issuer must be `https://`**, or `http://` for a loopback address only — discovery and the key set are fetched from wherever it points, so plain http anywhere else is refused: `--oidc-issuer %q must be https — plain http is only accepted for a loopback address`.

**Signing keys are fetched lazily and re-fetched on an unknown `kid`.** There is no polling interval; the first fetch happens on the first token. A key rotation is picked up on the first token signed by the new key, so rotation needs no restart.

**Accepted algorithms** are your provider's advertised `id_token_signing_alg_values_supported` intersected with RS256/384/512, ES256/384/512, PS256/384/512 and EdDSA. `none` and HMAC algorithms are not in that set, and the token's own `alg` header never widens it.

**Claims:**

| Claim | Checked how |
| --- | --- |
| `iss` | Exact string equality with the configured issuer. No leeway. |
| `aud` | Exact, case-sensitive membership of `--oidc-audience`. A string `aud` and a single-element array behave identically. |
| `sub` | Exact, case-sensitive match against one of the `--oidc-subject` values. |
| `exp` | Required, and **zero leeway** — no clock skew is forgiven here. |
| `nbf` | Checked only when present, with five minutes of leeway. |
| `iat` | Parsed and **not validated**. |
| `azp` | **Never consulted.** See below. |

**`azp` is not `aud`, and this is the one that catches people.** Keycloak puts the requesting client's id in `azp` and, by default, does not add it to `aud` at all. rta reads `aud` and nothing else, so a Keycloak token that looks correct in a JWT decoder is rejected until the client is given an audience mapper. That is the next section.

**Only `sub` is matched.** Not `email`, not `preferred_username`, not `groups`. If your IdP's `sub` is an opaque UUID — Keycloak's is — then that UUID is what goes in `--oidc-subject`.

## Keycloak

Keycloak is the worked example because two of its defaults are wrong for this, both quietly.

**1. The issuer is realm-scoped.** It is `https://keycloak.example.com/realms/<realm>`, not the host. Getting this wrong produces `oidc: id token issued by a different provider`.

**2. The audience needs a mapper.** Create a client — call it `rta` — then add a mapper that puts its own id into `aud`:

*Clients → rta → Client scopes → rta-dedicated → Add mapper → By configuration → Audience*

- Name: `rta-audience`
- Included Client Audience: `rta`
- Add to access token: **On**

Without it the token carries `"azp": "rta"` and an `aud` of `account`, and rta rejects it with `oidc: expected audience "rta" got ["account"]`. Nothing in the Keycloak UI suggests this is missing.

**3. Find the subject.** Keycloak's `sub` is the user's internal UUID, visible in *Users → the user → Details → ID*, or by decoding a token:

```bash
rta codec jwt "$TOKEN"
```

That is the value for `--oidc-subject`. It is stable across username and email changes, which is the reason to prefer it, and opaque, which is the reason to write a comment next to it in your values file.

**4. Check the whole thing before deploying anything:**

```bash
TOKEN=$(curl -s -X POST \
  https://keycloak.example.com/realms/main/protocol/openid-connect/token \
  -d grant_type=password -d client_id=rta \
  -d username=you -d password=… | jq -r .access_token)

rta codec jwt "$TOKEN"
```

Read three fields off that output: `iss` must equal `--oidc-issuer` exactly, `aud` must contain `--oidc-audience`, and `sub` must be a value you passed to `--oidc-subject`. If all three match and the call is still refused, the reason is in the server's stderr — see below.

## Any other provider

Nothing above is Keycloak-specific except the mapper. For a generic provider:

- **Issuer**: whatever `.well-known/openid-configuration` lives under. Auth0 uses `https://<tenant>.auth0.com/`, and the trailing slash is part of it. Okta uses `https://<org>.okta.com/oauth2/<server>`. Google uses `https://accounts.google.com`.
- **Audience**: the API identifier or client id your provider puts in `aud`. Providers that distinguish an "API" from a "client" (Auth0, Okta) want the API identifier, and the token request usually has to ask for it explicitly.
- **Subject**: whatever that provider puts in `sub`. Google's is a numeric string; Auth0's looks like `auth0|65f…`.

Decode a real token before writing the flags. Every field rta checks is visible in it.

## Both mechanisms at once

`--token-file` and `--oidc-issuer` can be set together, and a bearer token that the static verifier rejects falls through to the OIDC one. That is useful — an OIDC instance can still hold one static token for a Prometheus scraper, which cannot perform an OIDC exchange on a scrape interval.

It is worth being clear about what it costs. Two verifiers on one endpoint means the endpoint's strength is the *weaker* of the two, and a static token in a file is weaker than an identity your IdP can revoke. Use it for the machine that has no other option, keep that token's label distinct so the record says which one called, and do not let it become the way people log in.

## When it does not work

**The client is told nothing.** Every verification failure returns the same generic invalid-token response — a caller cannot learn whether the audience was wrong or the subject was unknown, because that difference is a probing oracle. The reason goes to the server's **stderr**:

```
rta: mcp http: oidc token rejected: oidc: expected audience "rta" got ["account"]
rta: mcp http: oidc token subject "9c1a…" is not in the allowed list
rta: mcp http: bearer token rejected by every configured verifier: …
```

In Kubernetes that is `kubectl logs`. If you are looking at a 401 and the logs say nothing at all, the request never reached the verifier — check that you sent `Authorization: Bearer …` and that you are talking to the MCP port rather than the observation one.

| Message | Cause |
| --- | --- |
| `oidc: expected audience "X" got ["Y"]` | No audience mapper, or the wrong `--oidc-audience`. |
| `oidc: id token issued by a different provider, expected "X" got "Y"` | Issuer mismatch — usually a missing realm path or a trailing slash. |
| `oidc: token is expired` | Zero leeway on `exp`; also check the pod's clock. |
| `failed to verify signature` | Token signed by a key the issuer does not advertise. |
| `subject "…" is not in the allowed list` | `sub` is not one of the `--oidc-subject` values. Decode the token. |
| `reaching the --oidc-issuer` at startup | Discovery failed. The server did not start. |

**Repeated failures are slowed down.** After five refusals in a minute from one address, responses are delayed 250ms, doubling to two seconds. A misconfigured client retrying in a loop will look slow before it looks broken.

## What lands in the record

Two names, and they are not the same thing.

`Agent` is the `--as` name — the identity grants are issued to and policy is written against. `Credential` is the OIDC `sub`, recorded beside it, bounded to 64 bytes with a hash suffix if longer.

So the record answers both "which agent was this" and "which human's token authenticated it", and `rta lock add` can target either. This is why an instance per person is worth the trouble: with a shared server both columns collapse to the same uninformative value.

## In Kubernetes

[`charts/rta-chart`](https://github.com/this-is-tobi/rta/tree/main/charts/rta-chart) takes the same three values per instance:

```yaml
servers:
  tobi:
    auth:
      oidc:
        enabled: true
        issuer: https://keycloak.example.com/realms/main
        audience: rta
        subjects:
          - 8f14e45f-ceea-467a-9c1a-2d7c4f0b1a33  # Keycloak user ID for tobi
```

The chart refuses to render an issuer with no audience or no subject, so those two startup refusals arrive at `helm install` time instead of as a CrashLoopBackOff.

One deployment note that follows from discovery happening at startup: if your IdP and rta start together, rta can lose the race and refuse to start. It restarts, and a `startupProbe` with room to retry covers it — but a NetworkPolicy that never lets the pod reach the IdP produces the same symptom permanently, so check egress before blaming the ordering.
