# Kryspal Media Plugin Protocol v1

This document is the public contract between plugin authors and the Kryspal Media server (the host).

- A plugin is a standalone executable that the server starts as a child process; the two sides talk over gRPC. A plugin can be written in any language that can serve gRPC.
- The protocol does not use hashicorp/go-plugin; the handshake is defined by this document, and authors using languages other than Go can implement it by following this document.
- Service definitions: [`pluginv1/plugin.proto`](pluginv1/plugin.proto), [`pluginv1/metadata.proto`](pluginv1/metadata.proto), [`pluginv1/image.proto`](pluginv1/image.proto), [`pluginv1/subtitle.proto`](pluginv1/subtitle.proto), [`pluginv1/usersync.proto`](pluginv1/usersync.proto), [`pluginv1/recognize.proto`](pluginv1/recognize.proto), [`pluginv1/segments.proto`](pluginv1/segments.proto), [`pluginv1/danmaku.proto`](pluginv1/danmaku.proto), [`pluginv1/peoplealias.proto`](pluginv1/peoplealias.proto), [`pluginv1/cast.proto`](pluginv1/cast.proto); the host-side service the host serves to plugins is [`pluginv1/host.proto`](pluginv1/host.proto) (§7.3). Go authors can use the [`sdk`](sdk) package to skip the details of the handshake and the server; see [`examples/static`](examples/static) for an example.

## 1. Versions

- The protocol has a major version number. This document is version `1`, and the proto package is `kryspal.plugin.v1` (full gRPC service names look like `/kryspal.plugin.v1.Plugin/Describe`).
- **Rename**: before the SDK was made public, the protocol was renamed from `mediaserver.plugin.v1` to `kryspal.plugin.v1`, the handshake environment variables changed from `MEDIASERVER_PLUGIN_*` to `KRYSPAL_PLUGIN_*`, and the cookie value changed as well. This was a one-time incompatible change: plugins built for the old names (for example TMDB 1.x) are incompatible with the new host and must be rebuilt and re-released (TMDB 2.0.0 and later are built with the new SDK). v1 and v2 do not coexist. When the host detects an old plugin (the process exits before the handshake, and stderr carries the old SDK's "not started by the host" message), it marks the plugin as "incompatible, needs an update" instead of treating it as a crash and restarting it. A version entry in the plugin catalog declares the new protocol with the `sdk` field (value `kryspal-plugin/1`); versions without this field cannot be installed. When a new plugin is started by an old host (it sees only `MEDIASERVER_PLUGIN_*`), it exits with a clear message ("a newer server is required").
- Within v1, only compatible additions are made: new fields, new RPCs, new enum values. Field numbers and types do not change, and deleted numbers are not reused. Plugins should ignore enum values and capabilities they do not recognize.
- Incompatible changes go into `v2` (`kryspal.plugin.v2`); the host will support several major versions at the same time for a while.
- SDK releases (the Go module `github.com/kryspal/plugin-sdk`) are tagged `v0.x` while the API settles; a `v0.x` minor release may still change the Go API of the SDK helpers, but never the wire protocol rules above. `v1.0.0` follows the API freeze and the review of the license terms.

## 2. Handshake

### 2.1 Startup environment

The host starts a plugin with a **clean environment** and does not pass on the server's environment variables (otherwise database connection strings and various keys would reach third-party plugins). The plugin receives only:

| Variable | Description |
| --- | --- |
| `KRYSPAL_PLUGIN_COOKIE` | The fixed magic value `7f3a9c52-kryspal-plugin`. If it does not match, the plugin should explain on stderr "this is a Kryspal Media plugin and cannot be run directly" and exit with a non-zero status. It is not a secret; it only stops users from running the plugin directly. |
| `KRYSPAL_PLUGIN_PROTOCOLS` | The protocol major versions the host supports, as comma-separated decimal numbers, for example `1` or `1,2`. |
| `KRYSPAL_PLUGIN_SOCKET_DIR` | The absolute path of a 0700 directory the host created for this plugin; the plugin creates its Unix socket inside it. The path is deliberately short and is not under DATA_DIR. |
| `PATH` | The system path. |
| `TMPDIR` | The temporary directory. |
| `HOME` | Points to the plugin's own directory. When the plugin needs to keep data (for example a cache), it writes under `HOME`. |
| `SSL_CERT_FILE`, `SSL_CERT_DIR` | Passed only when the server itself has these two variables set, so the plugin connects using the same set of CA certificates. |

The working directory is the plugin's installation directory (`DATA_DIR/plugins/<id>/<version>/`), which the plugin must not write to.

**Trust boundary.** The clean environment only keeps the host's environment (database connection strings, keys) from reaching the plugin; the plugin process still runs as the server's user, with no sandbox and no resource limits (rlimit): it can read DATA_DIR (including `secret.key` and uploaded files), the server process's `/proc/<pid>/environ` and the libraries, and it can also access the network and start other processes (by design). Installing a third-party plugin therefore means trusting its author; official plugins are verified by the official catalog's signature and SHA-256, while for third-party catalogs only the package's SHA-256 is verified. The admin console shows this warning where a third-party catalog is added.

### 2.2 Plugin steps

1. Check `KRYSPAL_PLUGIN_COOKIE`.
2. Pick the highest major version it supports from `KRYSPAL_PLUGIN_PROTOCOLS`; if there is no overlap, explain on stderr and exit with a non-zero status.
3. Create a Unix socket in `KRYSPAL_PLUGIN_SOCKET_DIR`.
   - The file name is the plugin's choice (the Go SDK uses `plugin.sock`), but it must sit directly in that directory; the host verifies that the path is inside that directory and rejects anything outside it.
   - The full path must not exceed the Unix socket limit (107 bytes on Linux).
   - Recommended permissions: 0600.
4. Start a gRPC server on that socket (cleartext HTTP/2, no TLS) and register:
   - `kryspal.plugin.v1.Plugin` (required).
   - `kryspal.plugin.v1.MetadataProvider` when `metadata.*` capabilities are declared; when `images.*`, `subtitles.search`, `usersync.*`, `media.recognize`, `segments.*`, `danmaku.provider`, `people.aliasmap` or `cast.target` are declared, register `kryspal.plugin.v1.ImageProvider`, `kryspal.plugin.v1.SubtitleProvider`, `kryspal.plugin.v1.UserSync`, `kryspal.plugin.v1.Recognizer`, `kryspal.plugin.v1.SegmentProvider`, `kryspal.plugin.v1.DanmakuProvider`, `kryspal.plugin.v1.PersonAliasProvider` and `kryspal.plugin.v1.CastTarget` respectively.
   - The standard health check `grpc.health.v1.Health` (see section 4).
5. **After the server starts accepting connections**, print **exactly one line** of handshake on stdout:

   ```
   <major version>|unix|<absolute socket path>\n
   ```

   For example `1|unix|/run/kryspal/p3f9/plugin.sock`.

6. **Never write to stdout again** after that. All logs go to stderr.

Rules for the handshake line:
- The three fields are separated by `|`; the address field itself may contain `|` (only the first two are split off). The line must not exceed 1024 bytes.
- The network field reserves `tcp` (with a `host:port` address), so that the protocol may run over the network later. A v1 host accepts only `unix` and treats `tcp` as a handshake failure.
- The host waits for the handshake with a timeout; a timeout, a malformed line, a major version not in the host's list, or a socket outside the given directory all count as a startup failure.

### 2.3 Logs

- Each line on stderr is one log entry; the host forwards it line by line into the server log and attaches the plugin id.
- The host limits line length and rate; anything beyond is truncated or dropped. Plugins should not write large amounts of data to stderr.
- Logs must not contain secret configuration values such as API keys.

### 2.4 Shutdown

- To stop a plugin, the host first sends `SIGTERM`. The plugin should stop accepting new calls, finish calls in progress, remove the socket, and then exit with status 0.
- If it has not exited after 5 seconds, the host sends `SIGKILL` to the whole process group. The Go SDK waits 4 seconds by default.
- The plugin forms its own process group; on Linux, Pdeathsig is set so the plugin also exits when the server crashes. Child processes started by the plugin are terminated along with it.
- When a plugin exits on its own or crashes, the host restarts it with backoff (starting at 1 second and doubling, up to 5 minutes); after 5 crashes within 10 minutes it is disabled until an administrator re-enables it.

## 3. Call order

1. After the handshake, the host calls `Plugin.Describe` to get the Manifest and checks that:
   - `id` and `version` match the plugin catalog and the installation record.
   - `protocol_version` equals the major version in the handshake line.
2. The host sends the complete saved configuration with `Plugin.Configure`. Afterwards, every time an administrator changes the configuration, the host first validates it with `Configure`: it is saved only if it passes; otherwise the field errors are shown to the administrator.
3. Only after the configuration passes and every required field has a value does the plugin appear in the server's list of metadata providers. Until then, the host does not forward metadata calls; if the plugin receives one anyway, it should return `FAILED_PRECONDITION`.
4. From then on the host calls `MetadataProvider` methods as needed and runs a health check every 30 seconds.

## 4. Health checks

The standard [`grpc.health.v1`](https://github.com/grpc/grpc/blob/master/doc/health-checking.md) is used; every language has a ready-made implementation, and the protocol does not define a Health RPC of its own.

- The service name `""` (the whole plugin) must return `SERVING`.
- It is recommended to also register `kryspal.plugin.v1.Plugin` and every registered service (`…MetadataProvider`, `…ImageProvider`, `…SubtitleProvider`, `…UserSync`, `…Recognizer`, `…SegmentProvider`, `…DanmakuProvider`, `…PersonAliasProvider`, `…CastTarget`).
- After receiving `SIGTERM`, switch to `NOT_SERVING`.
- The host checks with `Check("")` every 30 seconds; after 3 consecutive failures or non-`SERVING` responses, it restarts the plugin.

## 5. Description and configuration

### 5.1 Manifest

`Describe` carries a language (BCP 47), and the plugin returns all text other than the name (description, field labels, option text, attribution) in that language; if it does not have that language, it returns English. While a plugin is running, the content of the Manifest must stay fixed apart from the language of its text.

**Localization (i18n).** Translations are optional: a plugin that only has English is complete and works everywhere (every fallback chain below ends in English). A plugin that does offer other languages follows these rules. The language always comes from the host in the request; the plugin does not guess it:

- `DescribeRequest.language`: all display text in the Manifest — `name`, `description`, `config_groups[].title`, the field `label`, `description`, `placeholder`, `link_label`, the option `label`, and `attribution.text`.
- `ConfigureRequest.language`: the text of `ConfigureResponse.error` and `field_errors`.
- The language is a BCP 47 tag; it may be `zh-TW`, `zh-CN`, `zh-Hant`, `zh`, `ja-JP`, `en`, `en-US`, or an empty string (treated as English). The host only passes the language; it does not guarantee that every plugin has a translation for it.
- **Fallback chain**: when the plugin does not have text in that language, it falls back in a fixed order that always ends in English; it must never return an empty string or a key name: `zh-CN` (including `zh`, `zh-Hans`, `zh-SG`) → `zh-TW` → `en`; `zh-TW` (including `zh-HK`, `zh-MO`, `zh-Hant`) → `zh-CN` → `en`; other languages: full tag → the language alone (`ja-JP` → `ja`) → `en`.
- **No mixing**: returned text contains only characters of that language (English text contains no Chinese characters; `zh-CN` uses Simplified and `zh-TW` uses Traditional). When an example is needed, English text should not embed Chinese examples.
- Proper nouns, brands and URLs are not translated; if a legally required attribution must appear in the original wording, every language may return the same original text.
- Field `key`s, option `value`s, capability strings and error codes are not display text and do not change with the language.
- When showing a configuration form, the host passes the current interface language of the admin console user; if the plugin is not running, the host starts its process temporarily and then calls `Describe`, so what comes back is the declaration in that language, not the last cached one. The names and descriptions in the installed list are fetched in English when the plugin starts.
- Go plugin authors use [`pluginapi/i18n`](i18n/i18n.go) (see the README). In other languages, implementing the fallback chain above is enough.

| Field | Description |
| --- | --- |
| `id` | Lowercase letters, digits and hyphens, starting with a letter or digit, at most 63 characters. Matches the plugin catalog. Official IDs are reserved for the official catalog. |
| `version` | semver, matching the plugin catalog. |
| `protocol_version` | Equals the major version in the handshake line. |
| `capabilities` | See section 6. |
| `config_fields`, `config_groups` | See 5.2. |
| `attribution` | The attribution a data source requires (text, logo URL, link), shown on the admin console's plugin page and "About" page. For example, the attribution required by the TMDB terms is declared by the TMDB plugin; the host does not hard-code any data source. |
| `suggested_concurrency` | The suggested concurrency limit; 0 means the host default of 4. Administrators can override it. |

- `features`: codes of the value-added features the plugin implements. They only matter for plugins signed by the official catalog; the host will later decide by license whether they can be enabled. Third-party plugins need not declare them and are not affected by licensing.

### 5.2 Configuration fields

The admin console generates the form automatically from the declaration. Field attributes: key, label, description, external link (and link text), type, whether required, default value, placeholder, and group.

| Type | String encoding of the value | Notes |
| --- | --- | --- |
| `string` | As is | |
| `secret` | As is | Stored encrypted by the server; the admin interface only shows whether it is set and never returns the value. Should not have a default value. |
| `bool` | `true` or `false` | |
| `int` | Decimal integer | Optional `min`, `max` (inclusive) |
| `select` | The `value` of one option | Options have a `value` and a `label` |
| `multiselect` | A JSON string array of option `value`s, for example `["en","ja"]` | |

- **Order**: the form first lists the fields that belong to no group, then each group in `config_groups` order; within a group, fields follow their declaration order.
- **Keys**: lowercase letters, digits and underscores, starting with a letter. They should not be renamed after release; renaming amounts to deleting the old field and adding a required field, and users must reconfigure after upgrading.
- **Required**: when a required field has no value, the plugin **needs configuration**. When a new version adds a required field, it likewise needs configuration after the upgrade.

### 5.3 Configure

- `ConfigureRequest.values` is the **complete** configuration, not a diff. Fields without a value do not appear in the map; the host does not fill in default values for the plugin, which handles defaults itself. Secrets are in plaintext.
- `ConfigureRequest.language` is the language for error messages; the rules are the same as for localization in 5.1.
- When the response's `error` (an overall error) and `field_errors` (keyed by field key) are both empty, the configuration has passed and taken effect.
- When either is non-empty, the plugin **must not apply** this configuration and keeps using the previous one; if it has never received a valid configuration, it remains in need of configuration.
- The plugin may verify connectivity in `Configure`, for example by calling the upstream once with the API key. Verification should take no more than a few seconds.
- gRPC errors are only for problems other than the configuration itself (for example an internal plugin error).

## 6. Capabilities

| Capability | Methods | Plugin center category |
| --- | --- | --- |
| `metadata.movie` | `Search(MOVIE)`, `GetItem(MOVIE)` | Metadata |
| `metadata.series` | `Search(SERIES)`, `GetItem(SERIES, SEASON, EPISODE)` | Metadata |
| `metadata.person` | `GetPerson` | People |
| `metadata.collection` | `GetCollection` | Metadata |
| `metadata.person.search` | `SearchPerson`: for people without provider IDs, looks up a person by name plus hints from their local titles | People |
| `metadata.trailers` | `GetItem` returns `ItemMetadata.trailers`; without the declaration, the host ignores this field | Metadata |
| `images.movie`, `images.series` | `ImageProvider.GetImages`: fetches images by the item's provider IDs | Images |
| `images.person` | `ImageProvider.GetImages(ITEM_TYPE_PERSON)`: fetches a person's photos by the person's provider IDs, `name` and `aliases`; free | People |
| `subtitles.search` | `SubtitleProvider.Search`, `Download` | Subtitles |
| `usersync.link` | `UserSync.BeginLink`, `PollLink`, `Unlink`: a member links an account | Account sync |
| `usersync.scrobble` | `UserSync.Scrobble`: real-time reporting during playback | Account sync |
| `usersync.watched.pull`, `usersync.watched.push` | `UserSync.PullWatched`, `PushWatched`: two-way sync of watched history | Account sync |
| `metadata.search.hints` | `Search` reads the name-recognition hints (`titles`, `year_source`, region and language hints, `shape`, `flags`, `duration_seconds`) and fills in `matched_title` and `reasons`; without the declaration it receives only `name` and `year` | Metadata |
| `metadata.series.absolute` | `GetItem` accepts an episode's `absolute_episode_number` | Metadata |
| `segments.provider`, `segments.submit` | `SegmentProvider.GetSegments`, `SubmitSegments`: querying and uploading community intro and credits data | Playback |
| `danmaku.provider` | `DanmakuProvider.GetDanmaku`: danmaku | Playback |
| `media.recognize` | `Recognizer.Recognize`: infers what a title is from its path and name only | Name recognition |
| `people.aliasmap` | `PersonAliasProvider.GetAliasMap`: the plugin's whole name map of people (real names, stage names, former names and other spellings) | People |
| `cast.target` | `CastTarget`: finds playback devices on the local network and plays a media URL on them (declare `cast_protocol`: `dlna` or `chromecast`, and the permission `network.lan`) | Casting |

- For a capability that is not declared, the corresponding calls return `UNIMPLEMENTED`. The host does not forward such calls either.
- The host ignores capabilities it does not recognize, so capabilities added later do not affect existing plugins.
- **Plugin center category**: the admin console derives each plugin's category from the capabilities it declares, using the column above; when a plugin declares capabilities of several categories, the first one in this order wins: name recognition, metadata, images, people (actors), subtitles, media sources, playback, library care, account sync, casting. A plugin cannot choose its category. Plugins with none of these capabilities appear under Other.

## 7. Metadata providers

See [`pluginv1/metadata.proto`](pluginv1/metadata.proto) for the detailed fields. Common conventions:

- Every request carries a language (BCP 47) and a list of fallback languages. When the preferred language is unavailable, try the fallback languages in order; when none is available, return the original language.
- Provider ID keys are always lowercase, for example `tmdb`, `imdb`, `tvdb`.
- Dates are `YYYY-MM-DD` strings; an empty string means unknown. A numeric 0 means unknown. Fields without data are left empty; the host never treats an empty value as a clear.
- Images are always returned as HTTPS URLs; the host downloads and caches them. Plugins do not write files.
- Seasons and episodes are located by the series ID plus the season number and episode number (`GetItemRequest.series_id`, `season_number`, `episode_number`); if the provider has separate IDs for seasons and episodes, they may also be put in `id`.
- IDs of different kinds may overlap (for example, TMDB movie 550 and series 550 are different items), so `GetItem` always carries the kind.
- `ItemMetadata.status` (a series' airing status) and `language` (the language this result actually uses, BCP 47; when falling back to a fallback language or the original language, fill in the language fallen back to) are fields added within v1. When not given they hold the default value (`SERIES_STATUS_UNSPECIFIED`, an empty string), and the host treats them as not provided by the provider. Old plugins are not affected.
- `Credit.gender` (a cast or crew member's gender) is a field added within v1; when not given it is `GENDER_UNSPECIFIED`. The host uses it only when the person's gender is unknown and the field has not been set by an NFO or an administrator; a known gender is not changed. If the provider's cast and crew list carries gender, please fill it in; it saves calling `GetPerson` one by one. Old plugins are not affected.
- `SearchPerson` is for filling in data for people without provider IDs and requires declaring `metadata.person.search`. The request carries the name, aliases and some of this person's local titles (at most 10, each with a name, year, kind and the title's provider IDs). Each candidate in the response has `confirmed`:
  - **Set it to `true` only when the titles confirm it is the same person**, for example the TMDB plugin finds the TMDB ID of one of the titles in the person's filmography, or a code-based video plugin matches the code. A match on the name alone, or on the name plus guesses such as age or gender, is always `false`. Many people share a name, and setting it wrongly writes someone else's data onto this person.
  - The host uses a candidate only when it is the "one and only `confirmed`" candidate; with none or several, it uses none, and does not report an error for it.
  - A candidate needs at least `id` and `name`; once the host adopts one, it fetches the full data with `GetPerson`, so there is no need to make extra requests just to fill in candidates. Plugins should limit the number of upstream requests per search.
  - When there are no results, return an empty list, not `NOT_FOUND`.
- When `Search` has no results, return an empty list, not `NOT_FOUND`. When a provider ID hint is given (for example imdb) and the plugin can locate the item directly, it should return that result first.

### 7.1 Call limits

- Timeouts: `Search` 15 seconds; `GetItem`, `GetPerson`, `GetCollection` 30 seconds. The host passes them as gRPC deadlines, and plugins should respect them.
- Concurrency: each plugin gets at most 4 concurrent calls by default; the Manifest can suggest a value, and administrators can override it. Plugins should also rate-limit themselves toward the upstream (for example, the TMDB plugin limits itself to 20 requests per second).

### 7.2 Images, subtitles, user sync

See [`pluginv1/image.proto`](pluginv1/image.proto), [`pluginv1/subtitle.proto`](pluginv1/subtitle.proto) and [`pluginv1/usersync.proto`](pluginv1/usersync.proto) for the detailed fields. Common conventions:

- **Stateless**: plugins do not keep credentials, do not queue, and do not write files into libraries. A member's credentials (`UserSync`'s `Credentials`: opaque bytes defined by the plugin and not parsed by the host) are stored encrypted by the host and passed in on every call; when they need updating (for example on refresh token rotation), the plugin returns `updated_credentials` with the response, and the host **writes them before using them** and guarantees that the same credentials are never used concurrently. Plugins must not write credentials to logs, and they never see member IDs.
- **Images**: `GetImages` picks the keys it recognizes from `provider_ids`; always HTTPS URLs, downloaded and cached by the host; no results means an empty list.
- **Person photos** (`images.person`): the same `GetImages` call with `type = ITEM_TYPE_PERSON`. `provider_ids` are the person's IDs at the providers (for example `tmdb`, `imdb`) and may be empty; `name` and `aliases` are the names the server knows, because many photo libraries are keyed by name. The reply has only `IMAGE_TYPE_PRIMARY` images, best first. The capability is free. The host merges the photos of the data source with these plugins in the order of the administrator's global "person image sources" list (empty by default: only the data source's own photos), and only fills a missing photo; a photo that is already set (imported, chosen by hand) is never replaced automatically. Plugins that do not declare `images.person` are never called with `ITEM_TYPE_PERSON`.
- **Subtitles**: `Search` returns candidates (`matched_by`: hash > ids > name); `Download` returns the file bytes (archives are unpacked by the plugin; the total response size limit is 5 MB) and reports the remaining quota in `quota_remaining` (-1 if unknown). The subtitle account (for example the OpenSubtitles username and password) is a server-level setting configured by an administrator. Writing the subtitle next to the video is the host's job.
- **User sync**: `BeginLink`/`PollLink` is a device-code flow: `BeginLink` returns the `user_code` and URL to display, and `state` (defined by the plugin) is passed back unchanged by the host in `PollLink`; the host polls `PollLink` according to `interval_seconds`, and the plugin should not wait internally. `Scrobble` carries an `event_id` generated by the host (for deduplication; it may be resent); `PullWatched` is paginated (`cursor`, `since`); `PushWatched` takes at most 100 entries per batch, and those the upstream cannot find are put in `unmatched` by index.
- Timeouts: `GetImages` 30 seconds; subtitle `Search` 15 seconds, `Download` 60 seconds; `BeginLink`, `PollLink`, `Unlink` 15 seconds; `Scrobble` 10 seconds; `PullWatched`, `PushWatched` 60 seconds. Concurrency: credential-related calls are serialized by the host per credential.

## 7.3 Host service and permissions

Everywhere else in the protocol the host calls the plugin; `Host` (`pluginv1/host.proto`) goes the other way: the plugin calls back into the host.

- **Channel**: each time the host starts a plugin process, it opens a separate gRPC service in that process's socket directory (`KRYSPAL_PLUGIN_SOCKET_DIR`, 0700), with the socket file name `host.sock` (0600). The path and a one-time token are passed to the plugin in the environment variables `KRYSPAL_HOST_SOCKET` and `KRYSPAL_HOST_TOKEN`. Each endpoint serves only one plugin process; when the plugin stops while idle, restarts, or is restarted after a crash, the endpoint is recreated with the process and the token is renewed. Old hosts do not have these two variables: the SDK's `sdk.Host(nil)` returns `ErrHostUnavailable`, and the plugin should degrade (not use that capability).
- **Authentication**: every call carries `x-kryspal-host-token` in its metadata; a mismatched token returns `PERMISSION_DENIED` (message `host_token_invalid`). Who the caller is is determined by the endpoint, never by any ID the plugin reports about itself.
- **Permissions**: a plugin declares the permissions it needs in `Manifest.permissions` (currently only `llm`, called "AI enhancement" in the user interface). Check order: declared in the manifest → allowed by an administrator for that particular plugin → enabled globally. Not declared or not allowed returns `PERMISSION_DENIED` (`ai_assist_not_allowed`); not enabled globally returns `FAILED_PRECONDITION` (`ai_assist_disabled`). The host ignores permissions it does not recognize.
- **LLMComplete**: `system`, `messages` (roles are only user and assistant, at most 16 messages, 128 KiB in total), an optional `json_schema` (valid JSON, at most 16 KiB; the host makes a best effort to use the vendor's structured output and validates locally), and `max_tokens` (default 1024, hard limit 4096). Temperature is always 0 (reproducibility is the host's guarantee). The response carries the text, the model name, and the usage reported by the vendor (when the vendor does not report it, `usage.known` is false; the host does not estimate). Each plugin can have at most 4 calls at the same time; beyond that returns `RESOURCE_EXHAUSTED`.
- **Errors**: see the comments in `host.proto`. Vendor error details go only into the host log and are not returned to the plugin (they may contain the vendor's response content). Rate limiting (429) carries the `retry-after-seconds` trailer; a vendor response of insufficient balance or quota (402) is `RESOURCE_EXHAUSTED` with the message `ai_assist_payment_required` and no trailer.
- **Timeouts**: a single call from the host to a plugin times out after 15 seconds; when the plugin calls back into the host, it must set its own ctx timeout. The host's LLM call has its own timeout, and the two are independent of each other.
- Plugins never get the key, the BaseURL, or other plugins' usage.

### 7.4 Name recognition

See [`pluginv1/recognize.proto`](pluginv1/recognize.proto) for the detailed fields.

- The host calls it while **scraping**, not while scanning; it is not called when the item already has provider IDs.
- The input is names only: the segments of the relative path, sibling file names (at most 20), the local season and episode structure, the current name and year, and known IDs. Plugins **must not read file contents or access the network**, and the same input must produce the same result.
- Return at most 3 candidates in descending order of confidence; when nothing is recognized, return an empty list, not `NOT_FOUND`. Each candidate's titles are in descending order of weight; the host takes the few with the highest weight and searches the provider with each.
- `parser_version` is the rule version: when it changes, the host puts the items that are "awaiting identification" or "failed" back into the scrape queue; items already matched are not rerun.
- A library can select several recognizers and order them; the host calls them in order and goes with the first one that has a result, without merging.
- Recognizers and providers do not call each other; the host chains them: the host puts the recognition result into the hint fields of `SearchRequest` (only providers that declare `metadata.search.hints` receive them), and for providers without the declaration, the host calls `Search` separately with each of the few highest-weighted titles. So any recognizer can be paired with any provider.
- `previous_unmatched` (second round): when the previous round's result did not lead to a provider match, the host sends the request once more, at most once per item. In this round the plugin may switch to a more expensive method, for example asking AI to guess the title through `Host.LLMComplete` (requires declaring the `llm` permission and the administrator's approval); the second round's timeout is 30 seconds. The result goes to provider search under the normal rules, and the threshold for automatic application is unchanged.
- Timeout 5 seconds; milliseconds are expected.

### 7.5 Segment providers and danmaku

See [`pluginv1/segments.proto`](pluginv1/segments.proto) and [`pluginv1/danmaku.proto`](pluginv1/danmaku.proto) for the detailed fields.

- **Segments**: what comes back are candidates. The host adopts one only when the local runtime is within 2 seconds of the candidate's `duration_ms`, or when an audio fingerprint spot check matches; everything else is discarded. Candidates without `duration_ms` always require an audio fingerprint spot check.
- **Upload**: the host calls `SubmitSegments` only after an administrator turns it on, and sends only provider IDs, season and episode, runtime and times, never file names or paths. The plugin must use the administrator's own account or key (a configuration field) and must not embed a shared key.
- **Danmaku**: the host handles caching and uses at most 20000 comments. The plugin only returns data and is not responsible for display.
- **Ratings**: `ItemMetadata.ratings` carries the scores from each source (source code, score, maximum score, vote count); `community_rating` stays as before.
- **Product code**: `ItemMetadata.product_code` is the item's catalogue number (for example `ABC-123`) and is meant for providers of adult videos. Only movies and episodes carry one; the host ignores it for other types. Surrounding whitespace is ignored, it is cut to 64 characters, and a value with control characters is dropped. Leave it empty when there is none (an empty value never clears an existing code). The provider's own content ID (for example DMM's cid) belongs in `provider_ids`, not here. It is a compatible addition to v1: older plugins simply do not send it, and an older host ignores it. The host stores it from the provider like any other field (NFO and manual edits take precedence) and uses it for product-code search, sorting, filtering and the NFO `<num>` element.
- Timeouts: `GetSegments` 15 seconds, `SubmitSegments` 30 seconds, `GetDanmaku` 30 seconds.
- Complying with the upstream's terms and rate limits is the plugin author's responsibility.

### 7.6 Person alias maps

See [`pluginv1/peoplealias.proto`](pluginv1/peoplealias.proto) for the detailed fields.

- `GetAliasMap` is a server-streaming call. The host calls it after the plugin starts and then about once a day, sending the version it already has; when nothing changed the plugin answers with a single chunk that has `not_modified` set. Otherwise it streams the whole map in chunks of any size (keep each chunk under 4 MB). The host replaces its copy only when the stream ends without an error, so a failed or partial stream keeps the previous map.
- An entry lists at least two names of one person, the preferred one first, with optional provider IDs and birth date. Entries with fewer than two usable names are ignored.
- How the host uses a map: to recognize people by any of their names when it scans and scrapes, to find people by any name in search, and as evidence when it looks for duplicate people. A map never merges two existing people by itself; an administrator confirms every merge. When the host's data conflicts with the map (a different birth date or a different provider ID), the host does not recognize the person automatically; it lists the two people as a possible duplicate and shows the conflict, because either side may be wrong.
- Administrators can turn off a plugin's map; the host then stops using it and keeps the copy until the plugin is uninstalled.
- Timeout: 5 minutes for the whole stream. Limits: at most 500000 entries and 16 names per entry; the host truncates beyond that and logs it.

### 7.7 Cast targets

See [`pluginv1/cast.proto`](pluginv1/cast.proto) for the detailed fields.

- A plugin declares `cast.target`, the Manifest field `cast_protocol` (`dlna` or `chromecast`) and the permission `network.lan` (it sends multicast and connects to devices on the local network; the admin console discloses it, and enabling the plugin is the consent).
- The host owns the device list, the cast session, the authorization behind the media URL and play tracking; the plugin owns the protocol. A cast has this order: `Prepare` (no URL; the plugin answers with the content type, the response headers the host must add, warnings and reasons it cannot play) → the host creates an authorization and builds the URL → `Load` (the plugin receives the complete URL, which carries its own token) → the host polls `Status` about every 2 seconds and sends `Control` commands.
- The URL belongs to one item and the host can revoke it at any time; the plugin never receives a signing key or a credential. The host accepts only these response headers from `Prepare`: `transferMode.dlna.org`, `contentFeatures.dlna.org`, `CaptionInfo.sec`, `Access-Control-Allow-Origin`, `Access-Control-Allow-Headers`, `Access-Control-Expose-Headers`.
- `Device.handle` and `Device.state` are opaque to the host, which stores them and sends them back unchanged; a response that carries `updated_state` is saved by the host before it is used.
- A device refusing the media is an answer (`LoadResponse.rejected_code`), not an RPC error. `Prepare.reasons` lists why the media cannot be played on the device (the host then answers `cast_not_possible` without calling `Load`); `Prepare.warnings` are codes the host translates.
- Timeouts: `Discover` and `Probe` 15 seconds, `Load` 30 seconds, `Prepare`, `Control` and `Status` 10 seconds. Suggested concurrency 8.

## 8. Errors

Errors are expressed as gRPC status codes:

| Status code | Meaning | Host handling |
| --- | --- | --- |
| `NOT_FOUND` | The provider cannot find the ID | Reports not found |
| `INVALID_ARGUMENT` | The request is invalid, for example a required field is missing or the kind is unsupported | Reports a request error; no retry |
| `FAILED_PRECONDITION` | The plugin needs configuration: not configured yet, invalid configuration, or the upstream rejected the key | Marks the plugin as needing configuration and removes it from the provider list until the next `Configure` passes |
| `RESOURCE_EXHAUSTED` | Upstream rate limit | Pauses the whole plugin until after retry-after; see below |
| `UNAUTHENTICATED` | This member's credentials are no longer valid: revoked, or expired and cannot be refreshed (for example Trakt's `invalid_grant`) | Marks only this member's link as needing to be relinked; the plugin is neither taken offline nor paused |
| `UNAVAILABLE` | The upstream is temporarily unreachable | Retries later |
| `DEADLINE_EXCEEDED`, `CANCELED` | Timed out or canceled by the host | Retries later |
| `UNIMPLEMENTED` | The capability is not supported | Treated as a caller error |
| Other | Internal plugin error | Logs it and reports failure |

**retry-after**: `RESOURCE_EXHAUSTED` attaches `retry-after-seconds` as a gRPC **trailer**; the value is a positive decimal integer number of seconds, for example `retry-after-seconds: 30`.

- Trailer metadata was chosen over the `google.rpc.RetryInfo` error detail because gRPC in every language can set trailers directly, with no extra protobuf dependency.
- When it is missing or cannot be parsed, the host pauses for 60 seconds; anything over 1 hour counts as 1 hour.
- Go SDK: a handler just returns `sdk.RateLimited(d, msg)`, and the SDK converts it to the status code and sets the trailer. The host reads it with `pluginv1.RetryAfter(trailer)`.
- **Rate-limit scope**: there is also an optional trailer `retry-after-scope`: `plugin` (the default; pauses the whole plugin, the existing behavior) or `credential` (pauses only this one credential). Use `credential` when the upstream limits each user separately (for example Trakt); one member being rate-limited must not pause everyone. Go SDK: `sdk.RateLimitedCredential(d, msg)`; the host reads it with `pluginv1.RetryScope(trailer)`.

Error messages are explanations in English or Chinese for administrators and must not contain secrets.

## 9. Plugin catalog

A plugin catalog is the URL of a JSON list, always HTTPS. The server has the official catalog built in, and administrators can add third-party catalogs.

### 9.1 Format

```json
{
  "generated": "2026-10-04T00:00:00Z",
  "expires": "2026-11-04T00:00:00Z",
  "plugins": [{
    "id": "tmdb",
    "name": "TMDB",
    "description": "…",
    "owner": "Kryspal Media",
    "versions": [{
      "version": "1.0.0",
      "protocol": 1,
      "sdk": "kryspal-plugin/1",
      "minServer": "0.3.0",
      "changelog": "…",
      "capabilities": ["metadata.movie", "metadata.series"],
      "targets": [
        { "os": "linux", "arch": "amd64", "url": "https://…/tmdb-1.0.0-linux-amd64.tar.gz", "sha256": "…" }
      ]
    }]
  }]
}
```

| Field | Description |
| --- | --- |
| `generated`, `expires` | RFC 3339 UTC times. The host rejects expired catalogs, and also rejects a catalog whose `generated` is older than the last one it has seen (protection against rollback and freeze). |
| `plugins[].id` | Same as the Manifest's `id`. |
| `plugins[].owner` | The publisher's display name. |
| `versions[].protocol` | The protocol major version. If the host does not support it, the version is not listed as installable and the reason is shown. |
| `versions[].sdk` | The protocol marker this version was built with, `kryspal-plugin/1`. A version without this field or with a different value was built for the protocol before the rename; the host lists it as not installable and shows the reason. |
| `versions[].minServer` | The minimum server version (semver). |
| `versions[].capabilities` | Optional. The capabilities this version declares in its Manifest, so the admin UI can show hints (for example that a capability needs a paid plan) before the plugin is installed. Catalogs without it parse as before; the host then falls back to its own table for official plugins and shows none for others. The Manifest of the running plugin always wins after installation. |
| `targets[].os`, `arch` | Same as Go's `GOOS` and `GOARCH`, for example `linux`/`amd64`, `linux`/`arm64`. |
| `targets[].sha256` | The package's SHA-256, in lowercase hexadecimal. |

Versions whose protocol, minServer or os/arch do not match are not listed as installable, and the reason is shown.

### 9.2 Signatures

- The official catalog must be signed: a detached ed25519 signature over **the raw bytes of the whole catalog JSON** (not reformatted), placed at `<catalog URL>.sig`, whose content is the base64 (standard alphabet, a trailing newline allowed) of the 64-byte signature. For example, the signature of `https://…/catalog.json` is at `https://…/catalog.json.sig`.
- The server has several trusted public keys built in to allow rotation; verification by any one of them is enough. The sha256 of each package in the catalog is thereby protected by the signature.
- Third-party catalogs are not required to be signed. When one is added, the admin console shows the URL and a summary of its content for the administrator to confirm, with a clear warning about the risk.

### 9.3 IDs and update sources

- Official IDs are reserved for the official catalog; third-party catalogs must not shadow them.
- A plugin remembers which catalog it came from and from then on updates only from that same catalog, so that a third-party catalog cannot hijack updates by publishing a higher version of a plugin with the same name.

### 9.4 Packages

- The format is tar.gz. The root directory may contain only:
  - `<id>`: the plugin executable (required), for example `tmdb`.
  - `LICENSE`, `README`, `NOTICE` (optional, may have an extension, for example `README.md`).
- When extracting, the host rejects absolute paths, `..`, subdirectories, symbolic links and hard links, and limits the total size and the number of files.
- Installation flow: download to a temporary directory → verify sha256 → extract to `DATA_DIR/plugins/<id>/<version>/` → start, handshake, `Configure` → only then update the "current version" in the database. The previous version is kept so it can be rolled back to.
- Updates are only triggered manually in the admin console; there are no automatic updates.

## 10. Security model

- Installing a plugin means trusting it. Plugins run as the same user as the server; the host only isolates environment variables and does not sandbox.
- The only secrets a plugin can get are its own configuration values, passed in through `Configure`.
