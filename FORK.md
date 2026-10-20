# Transport maintenance fork

This distribution starts from upstream commit
`28f3d8b9a21470c35269fa24f3907626270ab1dc` (CycleTLS 2.0.5).
Changes prepared on 2026-10-20. The source and modifications are licensed under
GPL-3.0-only; see LICENSE. Original authorship notices are retained.

## Changes

- CONNECT response reads and proxy TLS handshakes obey request cancellation.
- SOCKS operations obey request cancellation; late connections are closed.
- TLS handshakes obey request context and failed connections are closed.
- HTTP/2 redials use a bounded timeout when the callback has no request context.
- Disabling connection reuse closes idle connections after every exchange.
- Transport errors retain a static connection phase.
- Wrapped syscall, DNS and address failures preserve their upstream status
  and error prefix. Unknown errors return 502; no error returns status zero.
- Error responses redact URL credentials and no longer append the raw cause.
- Release builds package seven pinned native binaries with SHA256 checksums.

ClientHello/JA3, header ordering, HTTP/2 settings, certificate validation,
the public JavaScript API, and the WebSocket protocol are unchanged.
Dependency versions in Go modules are unchanged; only toolchain directives pin
Go 1.24.13.

The changes are preserved as separate commits for upstream review and rebasing:

- [Add cancellable transport primitives and phase error metadata](https://github.com/k0t0fey/CycleTLS/commit/cbc46cf81db386c5f84262a4c3249ddadd1f0078)
- [Preserve wrapped syscall statuses and redact error credentials](https://github.com/k0t0fey/CycleTLS/commit/105a450f61318aa7d4ce93c0a6fa2642c30531e9)
- [Bound HTTP CONNECT and HTTPS proxy establishment by context](https://github.com/k0t0fey/CycleTLS/commit/60a183ec826a340bc1a314ba59b76823fbcc2ecf)
- [Cancel SOCKS waits and close connections returned after cancellation](https://github.com/k0t0fey/CycleTLS/commit/12bff86a221e9039e209a95b1dd14468b10c084e)
- [Apply request context to TLS handshakes and close failed sockets](https://github.com/k0t0fey/CycleTLS/commit/b6252424296c338567c4112fb99ee389f257fd82)
- [Bound detached HTTP2 redials by the configured request timeout](https://github.com/k0t0fey/CycleTLS/commit/1cf9636c372db9918567696b113013157e6b20ed)
- [Close all idle connections when connection reuse is disabled](https://github.com/k0t0fey/CycleTLS/commit/7de2e764d7a878bb4a57a7d736986ca0bc6b5249)
- [Cover transport cancellation error envelopes and no-reuse cleanup](https://github.com/k0t0fey/CycleTLS/commit/5fddec306efd19bc0ef2baf532069c45b12c030a)
- [Build and validate the scoped package with seven pinned helpers](https://github.com/k0t0fey/CycleTLS/commit/018764588133f03018667950047bf6fce996c93a)

No release has been published yet. Publication follows the gated workflow below.

## Validation

`cd cycletls && go test ./... -count=1` runs local Go tests. All upstream Go
integration tests are consistently behind the `integration` build tag. Upstream
applied this tag only to some files; the remaining integration files received
the same tag to keep the mandatory gate independent of external services.
External Jest tests are not part of the release gate.

`npm ci && npx tsc` builds the unchanged SDK. `node scripts/build-native.mjs`
builds all seven platforms with Go 1.24.13, CGO disabled, `-trimpath` and
`-buildvcs=false`. The release workflow builds twice and compares checksums.

`node scripts/smoke-local.cjs` performs ten HTTPS requests through one helper
and checks that no server connections remain after each response. Its existing
upstream test certificate and disabled certificate validation are restricted
to the loopback fixture.

## Release procedure

1. Keep `main` as the upstream mirror. Develop on `ic/2.0.5` from the pinned
   commit; require a reviewed pull request and the validation workflow.
2. Disable upstream publishing/testing workflows in the repository Actions
   settings before pushing branches or syncing main. Keep `ic-release.yml`
   enabled. This repository setting cannot be enforced by source files.
3. Confirm the intended npm scope, repository ownership and public access.
   Configure a short-lived granular `NPM_TOKEN` restricted to this package.
   After the first release, configure npm trusted publishing and remove it.
4. Set matching versions in package.json and package-lock.json. Review the diff,
   local tests, seven binary checksums, package contents and Linux/Windows smoke.
5. Merge without squash to preserve the individual fixes. Create a new immutable
   tag matching the package version, for example `v2.0.5-ic.1`.
6. `ic-release.yml` publishes the packed artifact with provenance after all gates
   pass, then creates a GitHub Release containing the tarball and checksums.
   Publication and tags must never be overwritten.
7. Consumers use an exact npm alias and verify with `npm audit signatures`.

Consumers do not need Go or a package lifecycle build script. FORK.md is explicitly
included alongside `/dist` in the npm files list because npm does not include
arbitrary Markdown automatically; LICENSE is also verified in the tarball.

## Updating upstream

Sync the mirror branch, review upstream releases and dependency changes, then
create a new `ic/<upstream-version>` branch. Rebase the separate transport fixes,
resolve conflicts, rerun all gates and examine ClientHello equality before
releasing a new version. Submit independent fixes upstream after acceptance.
Use a GitHub noreply author address and keep private infrastructure and
credentials out of commits and fixtures.
