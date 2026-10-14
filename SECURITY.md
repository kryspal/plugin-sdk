# Security policy

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub's private vulnerability reporting for this repository
(the **Security** tab, **Report a vulnerability**). Do not open a public issue.

Include what you found, how to reproduce it and the version or commit you tested. We will acknowledge the report,
keep you informed while we work on a fix, and credit you in the release notes unless you prefer otherwise.

## Supported versions

Until 1.0, only the latest `v0.x` release receives security fixes.

## Scope

In scope:

- the handshake and cookie handling in the SDK;
- the `Host` client and its token handling;
- the conformance suite and the examples;
- the protocol and catalog signature rules in `PROTOCOL.md`.

The Kryspal Media server itself is not developed in this repository. If you find a vulnerability in the server,
report it through the same channel and we will route it.
