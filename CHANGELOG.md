# Changelog

All notable changes to the Kryspal Media Plugin SDK. The protocol major version is `v1` (proto package
`kryspal.plugin.v1`); within v1 only compatible additions are made (new fields, RPCs, services, capabilities).

## v0.2.0 (unreleased)

Compatible additions to protocol v1; plugins built with v0.1.0 keep working.

- `IMAGE_TYPE_PHOTO`: a person's gallery photos, from `GetPerson` and from `GetImages` for people.
- `ItemMetadata.attributes`, `PersonMetadata.attributes`: public facts without a dedicated field, with suggested keys.
- `ConfigGroup.advanced` and `ConfigGroup.icon`: collapse rarely used setting groups and label groups with an icon.
- `KRYSPAL_PLUGIN_DATA_DIR` and `pluginapi.DataDir`: the plugin's data directory (the same as `HOME`).
- `plugintest`: `RunSource` and `Build` build the plugin from its main package; `Start` returns a connection to the
  running plugin for your own tests; `Options.Env` and `Options.Setup` point it at fake upstreams. New checks for item
  and person images, person provider IDs, attributes and name recognition.
- `PROTOCOL.md` §11 Runtime: all timeouts, the data directory, network and proxies, the trust model, and which
  capabilities need a paid plan on the server.
- `KRYSPAL_HOST_FEATURES` and `pluginapi.HostFeatures`: which compatible additions the host keeps, so a plugin can
  fall back on older hosts (`PROTOCOL.md` §11.6).
- Suggested attribute keys `agency`, `hobbies`, `debut_work`, and a definition with an example for every suggested key.
- `plugintest` gives each call the host's own deadline (15 s for `Search`, 30 s for `GetItem`, `GetPerson` and
  `GetImages`, 5 s for `Recognize`) instead of a flat 10 s.
- New example `examples/people`: person data with a gallery and attributes, person search, person images and a
  recognizer.
- Setting types `text`, `string_list` and `ordered_multiselect`, and `ConfigField.visible_when` to show a field only
  while another field has certain values (`PROTOCOL.md` §5.2; host feature `config_types`).
- `Manifest.attribute_labels`: display names for your own attribute keys, in each language you choose to support.
- `Manifest.image_fetch_rules`: the Referer the host sends when it downloads images from given hosts (host feature
  `image_referer`).
- `PersonWork.product_code`: the product code of a local title, when it has one, to confirm a person.
- More about each subtitle candidate: `size_bytes`, `duration_seconds`, `frame_rate`, `uploaded_at`,
  `machine_translated`, `trusted`, `file_count`, `comment`.
- New capability `plugin.tasks` and service `TaskRunner` (`tasks.proto`): maintenance tasks declared in
  `Manifest.tasks`, shown in the server's task center and run on a schedule or on demand (§7.8). `sdk.Config.TaskRunner`.
- `Manifest.author` and `Manifest.homepage`: shown when the plugin has no catalog entry (development mode, manual
  installs); a catalog entry's owner and homepage take precedence.
- `visible_when` also works on `multiselect` and `ordered_multiselect` fields: the field shows when any listed option
  is chosen.
- Host feature `tasks`; documented which setting type changes keep stored values, and that `image_fetch_rules`,
  `attribute_labels` and `tasks` may follow the configuration (the host calls `Describe` again after `Configure`).
- `plugintest` checks the new declarations (setting defaults, conditions, image rules, tasks, attribute labels) and
  runs the tasks named in `Options.RunTasks`.
- `Image.crop` and `Image.focus` (`ImageCrop`, `ImagePoint`, fractions of the original image): the host crops before
  resizing and centers square thumbnails on the focus. `Image.crop_hint` (`ImageCropHint`): an aspect ratio and a
  position, worked out by the host from the actual size. Host feature `image_crop`.
- `Recognition.tags`: tags read from the name, stored on the item apart from scraped and manual tags.
- New capability `plugin.checks` and service `Checker` (`checks.proto`): settings checks listed in `Manifest.checks`,
  shown as buttons on the settings page and run only when an administrator presses one (§7.9). `sdk.Config.Checker`.
- `ConfigGroup.items` (`ConfigItem`) and `ConfigField.item`: a group that is a list of similar entries with the same
  settings, for example one per data source, shown as a searchable list with per-entry changes and reset (§5.2).
  Host feature `config_items`. `plugintest` checks item ids, labels, the 100-item limit and each field's item.
- Host feature `item_checks`: the host offers `per_item` checks per entry and sends `RunCheckRequest.item`; a plugin on a host without it receives an empty `item` and should test every configured entry.
- `ImageFetchRule.proxy_from`: download images of a rule's hosts through a proxy named by one of the plugin's settings.
- `Notice` with `NoticeStatus`, and `notices`, `incomplete`, `retry_after_seconds` on `ItemMetadata`, `PersonMetadata` and `GetImagesResponse`: tell the user what happened at each source, and ask the host to fetch again later for a result a slow source left incomplete. Host feature `scrape_notices`. `plugintest` checks the limits.
- `CheckResult.item`: which entry a per_item check result belongs to.
- `SearchResponse.notices`: why a search found little or nothing, per source.
- Image URLs may be `http`; images on private addresses need `image_fetch_rules.hosts` (now also IP addresses and CIDR ranges) plus the enforced `network.lan` permission.
- Startup environment `KRYSPAL_HOST_LAN_ALLOWED`: whether the administrator allowed this plugin to use the local network.
- `CheckDeclaration.per_item` and `RunCheckRequest.item`: a check about one entry of such a list, shown inside each
  entry, for example "Test this source" (§7.9).
- Documentation: testing a plugin on your own server with `MEDIASERVER_DEV_PLUGINS` (§9.5); setting groups and when
  to split a plugin; several images per type; where person images come from and how they are merged; the data
  directory's `cache/` convention; which provider IDs `PersonWork` carries; product codes.

## v0.1.0 (2026-10-14)

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
