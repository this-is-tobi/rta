# External tools

The binary is self-contained and the core needs nothing. Some capabilities shell out to a tool you already have, rather than linking a client library — that is a deliberate trade, and it is why your existing credentials, proxies, contexts and credential helpers keep working without rta learning about any of them.

Nothing here is required to install or run rta. A missing tool costs you exactly the capabilities that use it, and the refusal names the tool.

| Tool                      | Needed for                                                                                                                                | If it is missing                                                                                                             |
| ------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| `git`                     | `rta plugin index add/update`, the `git.*` capabilities                                                                                   | Indexes cannot be attached; `git.*` is unavailable                                                                           |
| `kubectl`                 | the `kube` and `cnpg` plugins, `audit.kube.*`, and `kube:` tunnel targets                                                                 | Those capabilities refuse, naming kubectl                                                                                    |
| `pg_dump`                 | `pg.dump` only — `pg.query` and the rest of the `pg` plugin connect in-process and need nothing                                           | `pg.dump` refuses; every other `pg.*` capability is unaffected                                                               |
| `pg_restore`, `psql`      | `pg.restore` — `pg_restore` reads custom and directory dumps, `psql` replays plain SQL                                                    | `pg.restore` refuses, naming whichever one the dump's format needs                                                           |
| `mysqldump`, `mysql`      | `mysql.dump` and `mysql.restore` only — every other `mysql.*` capability connects in-process                                              | Those two refuse; the rest of the plugin is unaffected                                                                       |
| `mariadb-dump`, `mariadb` | `mariadb.dump` and `mariadb.restore`, same split. The legacy `mysqldump`/`mysql` symlinks every distribution still ships are accepted too | Those two refuse, naming both spellings                                                                                      |
| `docker`                  | the `docker` plugin                                                                                                                       | Those capabilities refuse                                                                                                    |
| `ssh`                     | `ssh:` tunnel targets                                                                                                                     | Those targets cannot be resolved                                                                                             |
| `cosign`                  | verifying a plugin artifact's signature, when an index states one                                                                         | The outcome is recorded as unverifiable; **an install is never blocked, because a signature is recorded and never required** |

Version policy is the same for all of them: rta uses what is on your `$PATH` and adopts none of their maintenance. There is no preflight check — a capability looks its tool up when you run it, and refuses by name if it is not there.

**MySQL and MariaDB share tool names, and that is the one place having a tool is not enough.** MariaDB ships `mysqldump` and `mysql` as symlinks onto its own binaries, so a lookup by name succeeds against either fork and the flags decide what happens next: `mysql.dump` passes MySQL 8 spellings (`--ssl-mode`, `--set-gtid-purged`, `--no-tablespaces`) that MariaDB's client refuses by name, and `mariadb.dump` passes the `--ssl` family that MySQL's client refuses the same way. Neither produces a worse dump — it is a refusal at the first flag, graded as `*.dump.toolskew`, and its hint tells you which client you really have and which plugin drives it. Point each plugin at its own fork and this never comes up.

Two consequences worth knowing. The primary container image is distroless, so it carries none of these — a containerised `rta mcp serve` covers the capabilities that need no external tool, and [Containers and images](../30-boundary/67-containers-and-images.md) says which. And a plugin that reads a credential location, such as kubectl's `~/.kube/config`, still needs `rta plugin allow` before it may: having the tool is not being granted the file.

`ghcr.io/this-is-tobi/rta-full` is the same rta with every first-party plugin and the tools from the table above already in it, for a person at a terminal who wants them rather than the narrowness; what it costs, and why an agent should never be pointed at it, is on [Containers and images](../30-boundary/67-containers-and-images.md#rta-full-the-console).

## Related

- [Installation](../10-getting-started/10-installation.md) — putting rta on your `$PATH`
- [Containers and images](../30-boundary/67-containers-and-images.md) — which of these a container carries
