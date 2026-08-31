# Security policy

rta's whole job is deciding what an AI agent may touch — a vulnerability here is not an ordinary bug. If you find one, please report it privately rather than opening a public issue.

## Reporting a vulnerability

Email **this-is-tobi@proton.me** with a description and, if you have one, a reproduction. You should get an acknowledgement within a few days.

Please don't open a public GitHub issue for a security report until a fix is available — the usual reason not to.

## Supported versions

The project is pre-1.0 and under active development. Only the latest release is supported; there is no backport policy yet.

## Scope

In scope: the `rta` binary and its built-in capabilities, the plugin trust and confinement model, and the MCP server. See [docs/19-the-boundary.md](docs/19-the-boundary.md) for what rta claims to guarantee and [docs/20-mcp.md](docs/20-mcp.md) for the MCP-specific trust model — a report that a documented tradeoff behaves as documented isn't a vulnerability, but a gap between what's documented and what actually happens is.

A third-party plugin is outside this repository's scope unless the issue is in rta's own verification of it (digest checking, declaration checking, confinement).
