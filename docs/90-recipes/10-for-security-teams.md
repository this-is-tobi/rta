# For a security team

You own the boundary rather than the work done inside it: where rta runs, the ceiling every grant fits under, the bundles people issue, and the record of what happened. Each section is one task — the commands, a sentence on why, and the chapter that explains it. Nothing here repeats those chapters.

## Find out which world each machine is in

```bash
rta audit clients
rta audit clients --fix
```

An agent with an unrestricted shell is not bounded by rta at all, so this comes before any policy. `audit clients` grades every AI client configured on the machine — a `Bash` allowlist, `bypassPermissions`, a credential sitting in a config file — and `--fix` prints the edit for each, including a deny list for rta's own authority-expanding commands. It prints and never writes. [What rta actually bounds](../30-boundary/10-the-boundary.md) is why this is the first task and not the last.

## Deploy one instance per person

```bash
helm install rta oci://ghcr.io/this-is-tobi/rta/rta-chart \
  --namespace rta --create-namespace --values rta-values.yaml
```

The chart runs rta as an HTTP MCP server, one entry under `servers:` per person, each authenticating as that person — the shape where an agent holds nothing and rta is the only route. Verify the chart before the first install ([Installation](../10-getting-started/10-installation.md#kubernetes) has the commands), pin the image by digest, and make the decisions [Kubernetes](../30-boundary/80-kubernetes.md) lists before setting a value. The image is the plugin allowlist: [An agent in a cluster](./30-an-agent-in-a-cluster.md) builds one that carries `kube` and nothing else, and hands it a minted, expiring identity instead of a ClusterRole.

## Commit a ceiling, and make machines require it

```bash
rta policy init        # a commented .rta-policy.yaml here, every axis named
rta policy require     # and this machine now refuses to run without one
rta policy show        # what is in force, and where rta looked
```

`policy init` goes in each repository an agent works in, and gets committed: the file can only subtract, so it needs no seal and travels with every clone. `policy require` is per machine and belongs in the provisioning script, because a deleted policy file fails open and only a demand kept outside the repository notices. [Team policy](../30-boundary/50-team-policy.md).

## Hand out bundles, not one grant at a time

```yaml
# .rta-policy.yaml, beside the ceiling that caps it
roles:
  dev:
    ttl: 8h
    grants:
      - kv.get db-password
      - note --rate 100/1h
```

```bash
rta grant roles                      # every role this machine can issue, and the window each really gets
rta grant issue dev --agent claude   # a person issues it, every line at once
```

A role grants nothing by being in the file. A person issues it at a terminal, where its lines are printed before the guard's passphrase is asked for — with the guard off, `--yes` stands in once `rta grant roles dev` has been read — and the ceiling beside it caps every line. [Roles](../30-boundary/30-grants.md#roles-a-day-of-grants-under-one-word).

## Put a passphrase in front of issuance

```bash
rta grant guard on
rta grant guard status
```

With the guard off, anything that can run commands as a person can issue that person's grants. With it on, issuing asks for a passphrase that lives in the person's head, and a grant it did not sign is not honoured; revoking never asks. A hosted instance takes the other shape, whose keys are the operators' own: [remote mode](../30-boundary/30-grants.md#remote-mode-a-guard-whose-keys-are-elsewhere).

## Review what is allowed right now

```bash
rta grant list                 # every standing grant, what is left of each bound, and who it is for
rta grant list --detail        # and what an agent reaches with no grant, and what would need one
rta grant list --server tobi   # an instance's roster, over the operator channel
rta doctor
```

An Origin column appears the day a grant was issued with nobody at a terminal, and `rta doctor` says the same in a sentence. `--server` takes a name from `remotes.yaml`, and answers once the instance enrolls your operator key. [Grants](../30-boundary/30-grants.md) · [where a grant came from](../30-boundary/10-the-boundary.md#what-a-grant-says-about-where-it-came-from) · [the operator channel](../30-boundary/66-operators.md).

## Read the record

```bash
rta agent overview        # the last hour: calls, refusals, anything parked
rta agent log --refused   # what agents asked for and did not get
rta agent log --detail    # the full view, and whether the hash chain still verifies
```

A refusal is the designed outcome, not an error, and the `code` column is the part to match on — it is stable across versions where the wording is not. [The record](../30-boundary/40-audit-trail.md).

## Keep a copy the machine cannot rewrite

The chain makes an edit visible, not impossible: anything running as the person can rewrite and reseal the whole file. A copy elsewhere is the defence against that, and the recipes already have both halves — [shipping the record](./01-readme.md#ship-the-record-somewhere-durable) with a cursor that picks up exactly where the last run stopped, and [the counters](./01-readme.md#put-it-on-a-dashboard) with the one alert worth having, `rta_record_intact == 0`.

## Stop an agent now

```bash
rta lock add claude --note "paused while we read the refusals"
rta grant revoke --all
rta lock list
rta lock rm claude
```

Revoking takes grants back and leaves the ungated reads open; a lock refuses every call the agent makes, from its next one, with no restart. `--server` places either on an instance, and `--kind operator` freezes an operator key that should no longer be trusted. [Locks](../30-boundary/45-stop-an-agent-now.md).

## Audit what the agents work on

```bash
rta audit deps                 # a checkout's declared dependencies against OSV
rta audit kube rbac            # cluster-admin bindings and wildcard rules
rta audit kube podsecurity     # pods privileged, in a host namespace, or free to run as root
rta audit web example.com      # TLS, headers, cookies, exposure
```

Each check is graded against a named OWASP or CWE control, so a report is reviewable by somebody who was not in the room. The recipes turn these into [a release gate](./01-readme.md#dependency-review-before-a-release), [a sweep of every repository a team owns](./01-readme.md#audit-every-repository-a-team-owns), [a cluster's end-of-life report](./01-readme.md#what-in-the-cluster-is-out-of-support) and [a review to paste into an issue](./01-readme.md#a-security-review-you-can-paste-into-an-issue).

## Next

- [For a developer](./20-for-developers.md) — the same boundary, from the side that works inside it
- [What rta actually bounds](../30-boundary/10-the-boundary.md) · [Team policy](../30-boundary/50-team-policy.md) · [The record](../30-boundary/40-audit-trail.md)
