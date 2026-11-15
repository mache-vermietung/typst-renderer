# Security policy

## Reporting a vulnerability

Please report vulnerabilities **privately** through GitHub's
[private vulnerability reporting](https://github.com/mache-vermietung/typst-renderer/security/advisories/new)
("Report a vulnerability" on the Security tab). Do not open a public issue.

Include the affected version, a description of the impact and, if possible, a minimal bundle that
reproduces it. You will get an acknowledgement; fixes are released as a new version and announced
in the GitHub release notes.

## Scope

In scope: the renderer daemon, its gRPC contract and the published container image — in
particular anything that lets a template or request escape the sandbox described in the README
(read files outside the bundle, reach the network, exhaust resources beyond the configured limits).

Out of scope: vulnerabilities in Typst itself (report them to the
[Typst project](https://github.com/typst/typst)), and deployments that do not follow the isolation
guidance in the README (for example running with network access).

## Supported versions

Only the latest release receives fixes.
