# Security policy

## Reporting a vulnerability

Report a vulnerability privately through GitHub:
[Report a vulnerability](https://github.com/instruxi-io/graphwatch/security/advisories/new).
Please do not open a public issue for it. Include the version
(`graphwatch --version`), the platform, and the steps that reproduce it.

Vulnerabilities in the Enforcer Graph service or its API, rather than in this
client, go through the same form; we will route them.

## Supported versions

Only the latest release is supported. Fixes ship as a new release; upgrade by
downloading it or with `go install github.com/instruxi-io/graphwatch@latest`.

## What graphwatch does on your machine

- It sends requests only to the API origin (`--base`, `GRAPH_BASE_URL`, the
  `enforcer` plugin's saved `base_url`, else `https://api.instruxi.dev`). It has
  no telemetry and no update check.
- It never prints a key, token or `Authorization` header, and never writes a
  credential file. `GRAPH_AUTH_HELPER` runs the command you give it, through
  `sh -c`, to obtain request headers.
- Every command is read-only except `decide`, `resolve`, `attend` (only when you
  type an answer) and `--demo` (creates and works a graph in your workspace).
- Text from the API is printed with control characters escaped, so a hostile
  graph title cannot drive your terminal.

## Verifying a release

Each release binary carries a SLSA build provenance attestation and a line in
`SHA256SUMS`:

```bash
gh attestation verify graphwatch-linux-amd64 --repo instruxi-io/graphwatch
grep graphwatch-linux-amd64 SHA256SUMS | sha256sum -c
```

Releases are built by `.github/workflows/release.yml` from the tagged commit,
after `go vet`, the tests and `govulncheck` pass.
