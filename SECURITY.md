# Security

baton is pre-release. No released version has a security maintenance promise.
Fixes currently target the repository's main branch.

## Report a vulnerability

Use the repository's [private vulnerability reporting page](https://github.com/sushilk1991/baton/security/advisories/new)
if GitHub offers it. Private reporting availability has not been verified.
If it is unavailable, open a public issue requesting a private security
contact without describing the vulnerability or including exploit material.
No dedicated reporting email or response-time commitment is established.

Include the affected commit, OS/architecture, prerequisites, a minimal
reproduction in temporary HOME/XDG directories, expected behavior, and
impact. Redact secrets, user paths, agent settings, and process arguments.
Do not send an integration manifest; it can contain original credentials.

## Boundaries

Advisory locks coordinate cooperating processes; baton is not a sandbox.
It does not enforce RAM/CPU limits, isolate commands, authorize tool calls,
or prevent same-user filesystem races. Do not run untrusted commands through
it expecting isolation. Keep state and data directories private and local.

`init` can modify global agent instructions and opt-in Claude hooks. Preview
changes, preserve its private manifest, and resolve uninstall conflicts
before removing it. Hooks retain the agent's ordinary permission checks.
`reap --yes`, `gc --yes`, and `cache prune` terminate or delete selected
resources; defaults and dry-run boundaries are documented in
[cleanup](docs/cleanup.md) and [cache](docs/cache.md).

The shared browser exposes local CDP access and uses a private temporary
profile. Other local processes can control that browser. Do not load
sensitive authenticated sessions into it or expose its port remotely.
Use the normal browser sandbox; baton does not disable it.
