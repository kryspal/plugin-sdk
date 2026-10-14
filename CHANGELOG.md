# Changelog

All notable changes to the Kryspal Media Plugin SDK. The protocol major version is `v1` (proto package
`kryspal.plugin.v1`); within v1 only compatible additions are made (new fields, RPCs, services, capabilities).

## v0.1.0 (2026-10-15)

First public release.

Releases are tagged `v0.x` until the API is frozen; `v1.0.0` follows the freeze and the review of the license terms.
The module path is `github.com/kryspal/plugin-sdk`.

- 2026-10-11 `ItemMetadata.original_language`: the original language of a title.
- 2026-10-12 `ItemMetadata.product_code`: a product or catalog code.
- 2026-10-13 `SearchSubtitlesResponse.quota_remaining`, so the host can show the remaining
  quota before the first download.
- 2026-10-13 New service `PersonAliasProvider` (`peoplealias.proto`, capability `people.aliasmap`):
  a server-streaming `GetAliasMap` of person names; the SDK adds a stream interceptor.
- 2026-10-14 Conformance suite `plugintest`: handshake, health check, Describe, Configure,
  a metadata round trip and shutdown against a plugin binary. Tests for the `sdk.Host` client.
- 2026-10-13 New service `CastTarget` (`cast.proto`, capability `cast.target`): DLNA and Chromecast
  plugins; `Manifest.cast_protocol` and the permission `network.lan`.

- First public version of the protocol and the SDK.
- Protocol renamed from `mediaserver.plugin.v1` to `kryspal.plugin.v1`; handshake variables are `KRYSPAL_PLUGIN_*`.
  Plugins built for the old names are not compatible and must be rebuilt.
- New services: `ImageProvider`, `SubtitleProvider`, `UserSync` (capabilities `images.*`, `subtitles.search`, `usersync.*`).
- New service `Recognizer` (`recognize.proto`, capability `media.recognize`): infer what a title is
  from its path and name only (no file reads, no network); the host calls it while scraping.
- Search hints: `SearchRequest` fields 7–13, `SearchCandidate.matched_title`/`reasons`,
  `GetItemRequest.absolute_episode_number`; capabilities `metadata.search.hints`, `metadata.series.absolute`.
- New services `SegmentProvider` (`segments.proto`, capabilities `segments.provider`, `segments.submit`) and
  `DanmakuProvider` (`danmaku.proto`, capability `danmaku.provider`).
- `ItemMetadata.ratings`: per-source ratings.
- New error `UNAUTHENTICATED` (a member's credential is no longer valid) and the optional `retry-after-scope` trailer.
- New host-side service `Host` (`host.proto`): the host serves it on a per-process Unix socket
  (`KRYSPAL_HOST_SOCKET`, `KRYSPAL_HOST_TOKEN`); `sdk.Host()` returns `ErrHostUnavailable` on older hosts.
  First RPC: `LLMComplete` ("AI Assist"). Plugins declare what they need in the new `Manifest.permissions` (`"llm"`);
  the administrator allows it per plugin.
