# Security policy

## Reporting a vulnerability

If you believe you have found a security issue in `koanf-validate`, please
report it through GitHub's
[private vulnerability advisory](https://github.com/uded/koanf-validate/security/advisories/new)
flow rather than opening a public issue. The maintainer will acknowledge
receipt within five working days and aim to confirm the issue (or explain
why it isn't one) within ten working days.

If GitHub's private advisory flow is not available to you, email the
maintainer at the address listed in the repo's [LICENSE](./LICENSE) /
`git log` author lines with a subject line beginning `[koanf-validate
security]`. PGP is not required.

Please include:

- A description of the vulnerability and its impact.
- A minimal reproducer (Go code, struct definitions, the call that
  triggers the issue).
- The version of `koanf-validate` and `validator/v10` involved.
- Any mitigations you have already identified.

## What's in scope

This library validates Go structs populated by koanf. Scope includes:

- Secret leakage through the public error surface (`*FieldError`,
  `*MultiError`, the `errors.As` chain reaching
  `validator.FieldError.Value()`).
- Reflection panics escaping into the host process.
- Resource exhaustion via adversarial struct shapes (cycles, depth,
  pathological tag values).
- Concurrent-safety violations under documented public-API usage.
- Walker / translator behaviors that misroute errors to the wrong koanf
  path in a security-relevant way.

The library does not handle authentication, authorization, network
traffic, or persistent storage; vulnerabilities in those layers belong
to your application or to upstream dependencies.

## Supported versions

Security fixes target the latest minor release line. Pre-1.0 the project
moves quickly — please upgrade to the latest minor before reporting an
issue unless the report concerns the latest release.

| Version | Status |
|---|---|
| `0.x` (current) | Active security support |
| `< 0.x` | Not supported — upgrade to current |

## Coordinated disclosure

Once a fix is ready, it ships in a patch release. The advisory is
published the same day on GitHub Security Advisories and referenced in
`CHANGELOG.md`. Credit is given to the reporter unless anonymity is
explicitly requested.
