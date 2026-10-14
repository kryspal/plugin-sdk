# Contributing

Thank you for helping with the Kryspal Media Plugin SDK. Issues and pull requests are welcome.

## Sign your commits (DCO)

We use the [Developer Certificate of Origin](https://developercertificate.org/) (DCO) instead of a contributor license
agreement. By adding a `Signed-off-by` line to a commit you certify that you wrote the change or otherwise have the
right to submit it under the project's license:

```sh
git commit -s -m "fix(sdk): ..."
```

The line must carry your real name and an email address you can be reached at. Pull requests with unsigned commits
cannot be merged.

Contributions are licensed under the Apache License 2.0, as section 5 of the license describes; there is no separate
agreement to sign.

## What we can accept

- Your own work, or code under a license compatible with Apache 2.0 (for example MIT, BSD or Apache 2.0) with its
  copyright notice and attribution kept.
- No code copied from GPL, LGPL, AGPL or MPL projects, and no code of unknown origin.
- No new dependencies under copyleft licenses; CI checks dependency licenses.

## Protocol rules

The wire protocol is versioned (`kryspal.plugin.v1`). Within v1, changes must be compatible additions only: new
fields, RPCs, services, enum values and capabilities. Field numbers and types never change and removed numbers are
never reused. CI runs `buf breaking` against the last release. Incompatible changes need a new major protocol
version and are discussed in an issue first.

## Before you open a pull request

- `gofmt`, `go vet ./...` and `go test -race ./...` pass.
- New behavior has tests; protocol changes update `PROTOCOL.md` and `CHANGELOG.md`.
- Generated code is regenerated with `./gen.sh` (pinned generator versions) and committed.
- Documentation and code comments are in English.

## Security issues

Do not open public issues for vulnerabilities; see [SECURITY.md](SECURITY.md).
