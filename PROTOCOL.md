# Kryspal Media Plugin Protocol v1

This document is the public contract between plugin authors and the Kryspal Media server (the host).

- A plugin is a standalone executable that the server starts as a child process; the two sides talk over gRPC. A plugin can be written in any language that can serve gRPC.
- The protocol does not use hashicorp/go-plugin; the handshake is defined by this document, and authors using languages other than Go can implement it by following this document.
- Service definitions: [`pluginv1/plugin.proto`](pluginv1/plugin.proto), [`pluginv1/metadata.proto`](pluginv1/metadata.proto), [`pluginv1/image.proto`](pluginv1/image.proto), [`pluginv1/subtitle.proto`](pluginv1/subtitle.proto), [`pluginv1/usersync.proto`](pluginv1/usersync.proto), [`pluginv1/recognize.proto`](pluginv1/recognize.proto), [`pluginv1/segments.proto`](pluginv1/segments.proto), [`pluginv1/danmaku.proto`](pluginv1/danmaku.proto), [`pluginv1/peoplealias.proto`](pluginv1/peoplealias.proto), [`pluginv1/cast.proto`](pluginv1/cast.proto); the host-side service the host serves to plugins is [`pluginv1/host.proto`](pluginv1/host.proto) (§7.3). Go authors can use the [`sdk`](sdk) package to skip the details of the handshake and the server; see [`examples/static`](examples/static) for an example.
- What a running plugin can expect from the host (timeouts, its data directory, network, sandboxing, which capabilities need a paid plan) is collected in §11 Runtime.

## 1. Versions

- The protocol has a major version number. This document is version `1`, and the proto package is `kryspal.plugin.v1` (full gRPC service names look like `/kryspal.plugin.v1.Plugin/Describe`).
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
| `HOME` | Points to the plugin's own data directory. When the plugin needs to keep data (for example a cache), it writes under `HOME`. See §11.2. |
| `KRYSPAL_PLUGIN_DATA_DIR` | The same directory as `HOME`, under a name that states the intent. Go plugins can call `pluginapi.DataDir(os.Getenv)`, which falls back to `HOME` on older hosts. |
| `KRYSPAL_HOST_FEATURES` | Comma-separated compatible additions the host stores and uses, for example `attributes,photo`. Missing on older hosts. See §11.6. |
| `KRYSPAL_HOST_LAN_ALLOWED` | `1` when the administrator allowed this plugin to use the local network (`network.lan`), otherwise `0`; missing on older hosts. The host restarts the plugin when the permission changes. Use it to tell the user why images on private addresses do not load. |
| `SSL_CERT_FILE`, `SSL_CERT_DIR` | Passed only when the server itself has these two variables set, so the plugin connects using the same set of CA certificates. |

The working directory is the plugin's data directory (the same as `HOME`). The executable lives in the installation directory (`DATA_DIR/plugins/<id>/<version>/`), which the plugin must not write to.

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
   - `kryspal.plugin.v1.MetadataProvider` when `metadata.*` capabilities are declared; when `images.*`, `subtitles.search`, `usersync.*`, `media.recognize`, `segments.*`, `danmaku.provider`, `people.aliasmap`, `cast.target`, `plugin.tasks` or `plugin.checks` are declared, register `kryspal.plugin.v1.ImageProvider`, `kryspal.plugin.v1.SubtitleProvider`, `kryspal.plugin.v1.UserSync`, `kryspal.plugin.v1.Recognizer`, `kryspal.plugin.v1.SegmentProvider`, `kryspal.plugin.v1.DanmakuProvider`, `kryspal.plugin.v1.PersonAliasProvider`, `kryspal.plugin.v1.CastTarget`, `kryspal.plugin.v1.TaskRunner` and `kryspal.plugin.v1.Checker` respectively.
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
| `author`, `homepage` | Optional. The plugin's author and project page (an `https://` URL), shown in the plugin center when the plugin has no catalog entry, for example in development mode. A catalog entry's `owner` and `homepage` take precedence (§9.1). |

- `features`: no longer used; the host ignores it. A plugin is never locked by the server's plan; only some capabilities are, whoever provides them (§11.5).

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
| `text` | As is; lines separated by `\n` | Multi-line text box. At most 64 KiB |
| `string_list` | A JSON string array, for example `["a","b"]` | A list the user adds to, removes from and reorders |
| `ordered_multiselect` | A JSON string array of the chosen option `value`s, highest priority first | Checkboxes plus ordering, for example data sources by priority |

- **Limits**: at most 1000 fields, 50 groups, 200 options per field and 100 items per group. The host ignores what is over a limit and logs a warning.
- **Order**: the form first lists the fields that belong to no group, then each group in `config_groups` order; within a group, fields follow their declaration order.
- **Groups**: a `ConfigGroup` has a key, a label and a description, and two optional hints:
  - `advanced`: the group is collapsed by default and listed after the others. Use it for settings most users never touch.
  - `icon`: one of `general`, `source`, `person`, `image`, `translate`, `network`, `schedule`, `advanced`, shown before the group's label. Other values are ignored.
  - With one or two groups the admin console shows them as cards on one page; with three or more it shows a list of groups next to the selected group's fields (one group at a time on phones). Either way there is a single Save for all fields.
- **Lists of similar things**: when a plugin has many things that each take the same settings, for example one proxy, interval and login per data source, declare a group with `items` (one `ConfigItem` per source: an `id` and a `label`) and set each of those fields' `item` to the id it belongs to. The admin console then shows the group as a list: users search it, see which entries they changed, open one entry to edit its settings and reset one entry to its defaults. Fields of the group without an `item` are shown above the list; put settings that apply to every entry there, and give the per-entry fields a default that means "same as above" (for example an empty string) so untouched entries stay unmarked. Field keys are still the plugin's own and must stay stable; derive them from the item id, for example `src_tvdb_proxy`. At most 100 items per group. `visible_when` works inside an entry as usual. Do not build this with a "which source to edit" select and `visible_when`: that select is stored as a setting, counts as a change and hides which entries differ from their defaults. Hosts without the `config_items` feature (§11.6) show an entry's fields as ordinary fields of the group.
- **One plugin or several**: put features that belong together in one plugin and organize them with groups. Features that are useful on their own (a user might want one without the other) belong in separate plugins, so users install only what they need.
- **Keys**: lowercase letters, digits and underscores, starting with a letter. They should not be renamed after release; renaming amounts to deleting the old field and adding a required field, and users must reconfigure after upgrading.
- **Conditional fields**: `visible_when` shows a field only while another field has one of the listed values, for example a database address that only matters when "use a database" is on. For a `bool`, `select`, `string` or `int` field the value must match; for a `multiselect` or `ordered_multiselect` field it is enough that one of the listed options is chosen (for example, show an API key when the sources include `tmdb`). A hidden field keeps its value and is still sent in `Configure`; a hidden required field does not count as missing. Conditions on other types or on unknown keys are ignored.
- **Changing a field's type**: `string` and `text` share an encoding, and so do `multiselect` and `ordered_multiselect`; switching between them keeps the stored value. For any other change, a stored value that does not fit the new type is not sent in `Configure` (as if it were unset) and stays stored in case the plugin is rolled back; a required field then needs configuration again. To migrate values yourself, declare the new type under a new key and read the old key's value in `Configure` while it is still there.
- **Newer types on older hosts**: a host without `config_types` in `KRYSPAL_HOST_FEATURES` does not know `text`, `string_list`, `ordered_multiselect` or `visible_when`. A plugin that must also run there can declare `string` and `multiselect` fields instead when the feature is missing.
- **Required**: when a required field has no value, the plugin **needs configuration**. When a new version adds a required field, it likewise needs configuration after the upgrade. While any required field is empty the host does not call `Configure` at all: the process is started once so the host can read `Describe`, then stopped; it is started again, and `Configure` is sent, when the administrator saves a complete configuration. A plugin therefore never has to validate an empty required field itself.

### 5.3 Configure

- `ConfigureRequest.values` is the **complete** configuration, not a diff. Fields without a value do not appear in the map; the host does not fill in default values for the plugin, which handles defaults itself. Secrets are in plaintext.
- `ConfigureRequest.language` is the language for error messages; the rules are the same as for localization in 5.1.
- When the response's `error` (an overall error) and `field_errors` (keyed by field key) are both empty, the configuration has passed and taken effect.
- When either is non-empty, the plugin **must not apply** this configuration and keeps using the previous one; if it has never received a valid configuration, it remains in need of configuration.
- Answer `Configure` quickly. The host calls it on enable (twice: once to validate the stored settings and once in the running process), on every save and on every start, and the administrator waits for it. Validate and apply the values, then reply; load large data (indexes, name tables, databases) in the background or on first use, and do not reload it when the values that affect it did not change. For a slow or networked test of the settings, declare a check (§7.9) instead of doing it in `Configure`.
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
| `plugin.tasks` | `TaskRunner.RunTask`: maintenance tasks listed in `Manifest.tasks`, run on a schedule or on demand (§7.8) | (does not change the category) |
| `plugin.checks` | `Checker.RunCheck`: settings checks listed in `Manifest.checks`, run when an administrator presses the button (§7.9) | (does not change the category) |

- For a capability that is not declared, the corresponding calls return `UNIMPLEMENTED`. The host does not forward such calls either.
- The host ignores capabilities it does not recognize, so capabilities added later do not affect existing plugins.
- **Plugin center category**: the admin console derives each plugin's category from the capabilities it declares, using the column above; when a plugin declares capabilities of several categories, the first one in this order wins: name recognition, metadata, images, people (actors), subtitles, media sources, playback, library care, account sync, casting. A plugin cannot choose its category. Plugins with none of these capabilities appear under Other.

## 7. Metadata providers

See [`pluginv1/metadata.proto`](pluginv1/metadata.proto) for the detailed fields. Common conventions:

- Every request carries a language (BCP 47) and a list of fallback languages. When the preferred language is unavailable, try the fallback languages in order; when none is available, return the original language.
- Provider ID keys are always lowercase, for example `tmdb`, `imdb`, `tvdb`.
- **The key for your own IDs must equal your plugin ID** (lowercased), for example a plugin with ID `my-scraper` writes `{"my-scraper": "abc123"}` on items and people. The host finds "the ID this plugin can look up" by the plugin ID: items, people and the person-details fill all look up `provider_ids[plugin_id]`. A different key (for example a short name) makes every person look like it has no ID, so the host falls back to name search (`SearchPerson`) and manual refresh reports `no_provider_id`. IDs from other sites (`imdb`, `tmdb` and so on) keep their own keys.
- `Credit.image_url` (a cast member's photo in an item's cast list) is stored as a placeholder avatar: when `GetPerson` or a person-image source later returns a primary image, the host replaces the placeholder with it. Images from NFO files or set by an administrator are never replaced. Keep returning cast photos even when they are small; they show until the person's own data arrives.
- Dates are `YYYY-MM-DD` strings; an empty string means unknown. A numeric 0 means unknown. Fields without data are left empty; the host never treats an empty value as a clear.
- Images are returned as URLs (`https` preferred, `http` accepted); the host downloads and caches them. Plugins do not write files. Hosts on private addresses need a declaration, see §7.2.
- Seasons and episodes are located by the series ID plus the season number and episode number (`GetItemRequest.series_id`, `season_number`, `episode_number`); if the provider has separate IDs for seasons and episodes, they may also be put in `id`.
- IDs of different kinds may overlap (for example, TMDB movie 550 and series 550 are different items), so `GetItem` always carries the kind.
- `ItemMetadata.status` (a series' airing status) and `language` (the language this result actually uses, BCP 47; when falling back to a fallback language or the original language, fill in the language fallen back to) are fields added within v1. When not given they hold the default value (`SERIES_STATUS_UNSPECIFIED`, an empty string), and the host treats them as not provided by the provider. Old plugins are not affected.
- `Credit.gender` (a cast or crew member's gender) is a field added within v1; when not given it is `GENDER_UNSPECIFIED`. The host uses it only when the person's gender is unknown and the field has not been set by an NFO or an administrator; a known gender is not changed. If the provider's cast and crew list carries gender, please fill it in; it saves calling `GetPerson` one by one. Old plugins are not affected.
- `SearchPerson` is for filling in data for people without provider IDs and requires declaring `metadata.person.search`. The request carries the name, aliases and some of this person's local titles (at most 10, each with a name, year, kind and the title's provider IDs). Each candidate in the response has `confirmed`:
  - **Set it to `true` only when the titles confirm it is the same person**, for example the TMDB plugin finds the TMDB ID of one of the titles in the person's filmography, or a code-based video plugin matches the code. A match on the name alone, or on the name plus guesses such as age or gender, is always `false`. Many people share a name, and setting it wrongly writes someone else's data onto this person.
  - The host uses a candidate only when it is the "one and only `confirmed`" candidate; with none or several, it uses none, and does not report an error for it.
  - A candidate needs at least `id` and `name`; once the host adopts one, it fetches the full data with `GetPerson`, so there is no need to make extra requests just to fill in candidates. Plugins should limit the number of upstream requests per search.
  - When there are no results, return an empty list, not `NOT_FOUND`.
- **Cropping and focus**: an `Image` may carry `crop` (the part to use, in fractions of the width and height) and `focus` (the point to keep in view, for example the center of a face). The host downloads the image, crops it before making any size and centers square and round thumbnails on the focus, so the web client, apps and Emby- or Jellyfin-compatible players all see the same result. Use it when the source's images do not fit, for example a catalog with only wide covers whose front is the right half. Administrators can override the crop by hand; the host then keeps theirs. Coordinates: the origin is the top-left corner of the original image; `x + width` and `y + height` are at most 1; `focus` is also relative to the original image, not to the crop, and should lie inside the crop. A focus of exactly (0, 0) means "no focus". When the plugin does not know the image's size, it can send `crop_hint` instead: an aspect ratio (width divided by height) and a position `x`, `y` from 0 to 1 within the room left over, like CSS `object-position` (x 0 flush left, 0.5 centered, 1 flush right; unset means 0.5). The host takes the largest part with that ratio once it knows the actual size; `crop` wins when both are set. Hosts without the `image_crop` feature (§11.6) use the whole image.
- **Several images of a type**: list them in order of preference. An item has one main backdrop and any number of stills. Mark scene images and screenshots as `IMAGE_TYPE_STILL`: they are always stills, never the main backdrop. Without `IMAGE_TYPE_STILL`, the first `IMAGE_TYPE_BACKDROP` is the main backdrop and the other `IMAGE_TYPE_BACKDROP` images are stills. The host keeps at most the number of stills set by the administrator (30 by default). For a person, `IMAGE_TYPE_PRIMARY` is the main photo and `IMAGE_TYPE_PHOTO` images are the person's gallery (at most 30 are kept).
  - Apart from stills and person photos, the host uses only the first image of each type (after its own ranking and any image sources the library adds behind yours); the rest are ignored, not kept as alternatives. So put the best poster, logo or thumb first.
  - The host never makes a thumb out of a backdrop. When you return no `IMAGE_TYPE_THUMB`, the item has no thumb: Emby- and Jellyfin-compatible players get none (they usually fall back to the backdrop themselves), while the host's own web client builds a landscape card at request time from other images. If your source has a real landscape image, return it as `IMAGE_TYPE_THUMB`.
- **Attributes**: `ItemMetadata.attributes` and `PersonMetadata.attributes` hold public facts that no dedicated field covers, for example a person's height or a title's label. They are shown on detail pages and are not used for search or filters.
  - Keys: lowercase letters, digits and underscores, starting with a letter, at most 64 characters. Values: plain text, at most 256 characters; line breaks become spaces and control characters are removed. At most 32 entries; beyond that the host keeps the first 32 keys in sorted order. Entries with an invalid key or an empty value are dropped.
  - Suggested keys are shown with a translated label; other keys are shown as they are (underscores become spaces), so prefer a suggested key when one fits. Values are free text in the response's language unless a format is given.

    | Key | For | Meaning | Example |
    | --- | --- | --- | --- |
    | `height_cm` | People | Height in centimeters, digits only | `165` |
    | `blood_type` | People | ABO blood type, optionally with Rh | `A`, `AB Rh-` |
    | `debut_date` | People | Date of the first work or appearance, `YYYY-MM-DD`; leave it out when only the year or the title is known | `2015-04-01` |
    | `debut_work` | People | Name or code of the first work | `ABC-001` |
    | `birthplace_detail` | People | A more precise birthplace than `birth_place` (city, district); leave it out when it adds nothing | `Shibuya, Tokyo` |
    | `agency` | People | Agency or management the person belongs to | `Sample Agency` |
    | `hobbies` | People | Hobbies or skills, comma-separated | `Cooking, swimming` |
    | `label` | Titles | The imprint or label the title was released under (the producing company goes in `studios`) | `Studio One` |
    | `series` | Titles | The series the title belongs to at the source; it does not create a collection | `Summer Series` |
    | `maker_code` | Titles | The maker's prefix in the catalog code, when the source lists it | `ABC` |
  - The whole map is one field: a new non-empty map from the provider replaces the stored one, an empty map changes nothing, and an administrator's edits take precedence over the provider.
  - **Labels for your own keys**: list them in `Manifest.attribute_labels` (key and label, in `DescribeRequest.language`). The admin console and the viewer show your label in the user's language, falling back to the key. Translate as many languages as your users need, or none; suggested keys always use the host's own translations. At most 64 labels, each at most 64 characters.
- When `Search` has no results, return an empty list, not `NOT_FOUND`. When a provider ID hint is given (for example imdb) and the plugin can locate the item directly, it should return that result first.

### 7.1 Call limits

- Timeouts: `Search` 15 seconds; `GetItem`, `GetPerson`, `GetCollection` 30 seconds (all timeouts: §11.1).
- Concurrency: each plugin gets at most 4 concurrent calls by default; the Manifest can suggest a value, and administrators can override it. Plugins should also rate-limit themselves toward the upstream (for example, the TMDB plugin limits itself to 20 requests per second).

### 7.2 Images, subtitles, user sync

See [`pluginv1/image.proto`](pluginv1/image.proto), [`pluginv1/subtitle.proto`](pluginv1/subtitle.proto) and [`pluginv1/usersync.proto`](pluginv1/usersync.proto) for the detailed fields. Common conventions:

- **Stateless**: plugins do not keep credentials, do not queue, and do not write files into libraries. A member's credentials (`UserSync`'s `Credentials`: opaque bytes defined by the plugin and not parsed by the host) are stored encrypted by the host and passed in on every call; when they need updating (for example on refresh token rotation), the plugin returns `updated_credentials` with the response, and the host **writes them before using them** and guarantees that the same credentials are never used concurrently. Plugins must not write credentials to logs, and they never see member IDs.
- **Images**: `GetImages` picks the keys it recognizes from `provider_ids`; URLs (`https` preferred, `http` accepted), downloaded and cached by the host; no results means an empty list.
- **Images on private addresses**: the host refuses to download from loopback, link-local and its own addresses, always. For other private addresses (RFC 1918, ULA, CGNAT, for example a NAS or a gateway on the user's LAN) list the hosts in `image_fetch_rules.hosts` (names, `*.` wildcards, IP addresses or CIDR ranges) and declare the permission `network.lan`; the host enforces that permission: the administrator switches it on for the plugin, and only then are images from the declared hosts downloaded. `KRYSPAL_HOST_LAN_ALLOWED` (§2.1) tells the plugin the current state. Without the declaration the host drops such images with a warning.
- **Image hosts that need a Referer**: list them in `Manifest.image_fetch_rules` (hosts, exact or `*.example.com`, and the Referer to send). The host applies the rule whenever it downloads an image from a matching host, including images it loads much later, as long as the plugin is enabled. Only the Referer can be set: no cookies or other headers. Redirects are followed without the Referer. Rules of an enabled plugin apply to all images from those hosts; when two plugins disagree, the first plugin ID in sort order wins. Hosts that support it list `image_referer` in `KRYSPAL_HOST_FEATURES`.
- **Images the host cannot reach directly**: a rule may name a setting of the plugin in `proxy_from` (a `string` or `secret` field whose value is an `http`, `https` or `socks5` proxy URL); the host downloads images from the rule's hosts through that proxy, never logs or echoes the value, and uses no proxy when the setting is empty. Administrators can also set an image download proxy for the whole server (with a host list); a plugin rule wins when both match. The host still never sends cookies, and plugins never deliver image bytes.
- **What happened at each source**: `ItemMetadata`, `PersonMetadata` and `GetImagesResponse` carry `notices`, at most 32, each a source name, a `NoticeStatus` and one plain sentence (optionally the settings list entry it belongs to). The host shows them in the item's refresh history and in the identify dialog and keeps only the latest set per plugin and stage. Use them to say why a source gave nothing: sign-in needed, blocked, challenge, timeout. Hosts that show them list `scrape_notices` in `KRYSPAL_HOST_FEATURES`.
- **Results that ran out of time**: set `incomplete` when a slow source did not finish and `retry_after_seconds` (60 to 86400; 0 means 600). A host with `scrape_notices` saves what you sent, calls again after that delay and fills only fields and images that are still empty, at most three times, never touching values the user edited. On older hosts the result is final, so still return as much as you have.
- **Declarations that follow the configuration**: `image_fetch_rules`, `attribute_labels` and `tasks` may depend on the plugin's settings, for example rules for mirror sites the user entered. The host calls `Describe` again after every accepted `Configure` and applies these three from the new answer. Every other part of the manifest must stay the same for a given version.
- **Person photos** (`images.person`): the same `GetImages` call with `type = ITEM_TYPE_PERSON`. `provider_ids` are the person's IDs at the providers (for example `tmdb`, `imdb`) and may be empty; `name` and `aliases` are the names the server knows, because many photo libraries are keyed by name. The reply has `IMAGE_TYPE_PRIMARY` images (candidates for the main photo) and may add `IMAGE_TYPE_PHOTO` images (the person's gallery), best first within each type. The capability is free. The host merges the photos of the data source with these plugins in the order of the administrator's global "person image sources" list (empty by default: only the data source's own photos), and only fills a missing photo; a photo that is already set (imported, chosen by hand) is never replaced automatically. Plugins that do not declare `images.person` are never called with `ITEM_TYPE_PERSON`.
- **Where person images come from**: the person's data source (the plugin whose `GetPerson` filled the person) supplies `PersonMetadata.images`; the plugins in the "person image sources" list supply `GetImages(ITEM_TYPE_PERSON)`. The two paths are merged, not ranked against each other:
  - Main photo: the first `IMAGE_TYPE_PRIMARY` from the data source, then from the image sources in their order, fills a missing main photo. A main photo that is set is kept.
  - Gallery: the `IMAGE_TYPE_PHOTO` images of the image sources in their order, then those of the data source, then the `IMAGE_TYPE_PRIMARY` images not used as the main photo. Duplicate URLs are dropped (the first one stays) and at most 30 are kept. The gallery is fetched when the person's page is opened and refreshed from time to time.
  - A plugin that is both the data source and an image source may return the same images on both paths; the host removes the duplicates.
- **Subtitles**: `Search` returns candidates (`matched_by`: hash > ids > name). Fill in what the source tells you about each candidate (`size_bytes`, `duration_seconds` of the last cue, `frame_rate`, `uploaded_at`, `machine_translated`, `trusted`, `file_count`, `comment`): users pick a subtitle that fits by these, and the host will use them to rank candidates. `Download` returns the file bytes (archives are unpacked by the plugin; the total response size limit is 5 MB) and reports the remaining quota in `quota_remaining` (-1 if unknown). The subtitle account (for example the OpenSubtitles username and password) is a server-level setting configured by an administrator. Writing the subtitle next to the video is the host's job.
  - `SubtitleTarget.provider_ids` carries all of the item's provider IDs, lowercased and unfiltered (your own IDs appear under your plugin ID, see §7). For an episode it carries the series' IDs.
  - Automatic download happens only in the scheduled subtitle task (for libraries with automatic subtitle languages), never on scan. It takes the first acceptable candidate after ranking: `matched_by` hash or ids, or name with `score` of at least 0.8. Hearing-impaired versions are not picked automatically. Manual search shows every candidate.
  - `matched_by` ids is trusted without looking at the score and is downloaded automatically, so mark a candidate as ids only when you are sure it belongs to this title (for example the duration also fits); otherwise use name with an honest score.
  - When `quota_remaining` is at or below the reserve the administrator set, automatic download skips that provider. A missing value or -1 means unknown and never causes a skip.
  - The host writes the file next to the video (or the `.strm` file) as `<video name>.<language>.<ext>` and never overwrites an existing file; automatic download skips a video that already has a subtitle in that language.
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
- **Tags**: a candidate may carry `tags` read from the name (for example a release or subtitle mark). The host stores the tags of the candidate it goes with on the item, apart from scraped and manual tags, and replaces them each time it runs recognition for that item; users can filter by them and badge rules can match them. Do not send what the host measures from the media itself (resolution, HDR, subtitle tracks). Tags are kept as written: the host merges only tags that differ in letter case, not synonyms ("Uncensored" and a translated word stay two tags, each filterable). A badge rule matches one tag value; administrators map synonyms by giving several rules the same badge text, which the host then shows once. Keep your tag words stable and, where useful, let users rename them in your settings.
- Timeout 5 seconds; milliseconds are expected.

### 7.5 Segment providers and danmaku

See [`pluginv1/segments.proto`](pluginv1/segments.proto) and [`pluginv1/danmaku.proto`](pluginv1/danmaku.proto) for the detailed fields.

- **Segments**: what comes back are candidates. The host adopts one only when the local runtime is within 2 seconds of the candidate's `duration_ms`, or when an audio fingerprint spot check matches; everything else is discarded. Candidates without `duration_ms` always require an audio fingerprint spot check.
- **Upload**: the host calls `SubmitSegments` only after an administrator turns it on, and sends only provider IDs, season and episode, runtime and times, never file names or paths. The plugin must use the administrator's own account or key (a configuration field) and must not embed a shared key.
- **Danmaku**: the host handles caching and uses at most 20000 comments. The plugin only returns data and is not responsible for display.
- **Ratings**: `ItemMetadata.ratings` carries the scores from each source (source code, score, maximum score, vote count); `community_rating` stays as before.
- **Product code**: `ItemMetadata.product_code` is the item's catalog number (for example `ABC-123`), for providers whose catalog is keyed by such codes. Only movies and episodes carry one; the host ignores it for other types. Surrounding whitespace is ignored, it is cut to 64 characters, and a value with control characters is dropped. Leave it empty when there is none (an empty value never clears an existing code). The provider's own internal content ID belongs in `provider_ids`, not here. The host stores it from the provider like any other field (NFO and manual edits take precedence) and uses it for product-code search, sorting, filtering and the NFO `<num>` element.
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

### 7.8 Plugin tasks

A plugin that declares `plugin.tasks` lists its maintenance tasks in `Manifest.tasks`, for example refreshing a downloaded index, clearing a cache or checking a connection. The host shows them in its task center and on the plugin's page.

- Each task has a `name` (fixed after release), a `title` and a `description` in `DescribeRequest.language`, and an optional `default_schedule` (`interval_minutes`, 5 to 44640, or a five-field `cron` in the server's time zone).
- With a default schedule, administrators can change the schedule, pause it or set the task to run only on demand. Without one, the task runs only when an administrator starts it.
- The host calls `RunTask` when a schedule is due or an administrator starts the task, never because the plugin was enabled, configured or restarted. A stopped plugin is started first.
- `RunTask` returns when the work is done, with an optional one-sentence `message` for the task center (at most 500 characters are kept) and `nothing_to_do` when there was nothing to do. Errors follow §8: `FAILED_PRECONDITION` when the plugin needs configuration, `RESOURCE_EXHAUSTED` to try later.
- The deadline is 30 minutes. The call is canceled when the server shuts down or an administrator cancels the task; stop soon after. The same task never runs twice at once.
- Tasks are free on every plan. Do not use tasks for work the host already schedules for a capability (scraping, fetching the alias map). Maintaining the plugin's own data is a good task, even when that data later feeds a capability: refreshing a downloaded name table that `GetAliasMap` serves, for example.
- Hosts that schedule tasks list `tasks` in `KRYSPAL_HOST_FEATURES`. On an older host nothing runs them, so a plugin that needs periodic work keeps its own timer when the feature is missing.
- To let administrators test a key, an account or a connection, use a check (§7.9) rather than a task.

### 7.9 Settings checks

A plugin that declares `plugin.checks` lists checks in `Manifest.checks`, for example "test the API key", "sign in" or "show the remaining quota". The host shows each as a button on the plugin's settings page, in its `group` or at the top. See [`pluginv1/checks.proto`](pluginv1/checks.proto).

- Each check has an `id` (same format as task names), a `label` and a `description` in `DescribeRequest.language`. At most 16.
- The host calls `RunCheck` only when an administrator presses the button: never on a schedule, on enable, on save or on restart. Results never decide whether settings can be saved.
- `values` are the settings on the administrator's form, encoded as in `ConfigureRequest`, possibly not saved yet; secret fields the administrator did not change carry the stored value. Test with them and do not apply or keep them.
- Return one or more results (`ok`, `warning`, `failed`, one line each, at most 500 characters kept). The host only shows them: it stores nothing, changes nothing and records no task.
- Checks confirm settings. They must not download catalog data in bulk, write files, change the plugin's state or start other work. The Host service offers plugins nothing that starts server work, so a check cannot trigger scans, scrapes or tasks.
- **Per-entry checks**: in a group with `items` (§5.2), set `per_item` for a check about one entry, for example "Test this source". The button then appears inside each entry and `RunCheckRequest.item` is that entry's id; check only that entry and keep it quick. Without `per_item`, a check covers the whole plugin or group.
- The host enforces a 30-second deadline, one running check per plugin and at most one run of the same check every 10 seconds (for a per-entry check, of the same check on the same entry). A timeout or an error is shown as a failed result.
- `CheckResult.item`: for a `per_item` check, the entry the result belongs to; the host links the result to that entry. Hosts with `item_checks` also offer "run this check for every entry": each entry gets its own run, timeout and cooldown, and the results form one table.

### 7.10 Field sources

A plugin that combines several data sources into one result can say where each field came from, so administrators see "Overview: TVDB" next to the field and know which source to look at when a value is wrong.

- `ItemMetadata.field_sources` and `PersonMetadata.field_sources` map a field name to a source name. Keys are the proto field names of that message, for example `name`, `original_title`, `overview`, `genres`, `tags`, `studios`, `credits`, `premiere_date`, `community_rating`, `ratings`, `product_code`; `attributes.<key>` names a single attribute. Values are the source as the user knows it, at most 48 characters, the same name the plugin uses in `Notice.source`.
- `Image.source` names the source of one image, with the same rules.
- Everything is optional. Leave the map empty when the whole result comes from one source (a single-source plugin such as TMDB needs neither); the host already records which plugin wrote each field. Fill in only the fields you actually took from a source.
- At most 64 entries. The host drops entries whose key is not a field of the message (or `attributes.` followed by a valid attribute key), whose value is empty, and values longer than 48 characters are cut.
- Display only: the host stores the name next to the field's provenance (plugin and time) and does not use it to merge, rank or filter. When an administrator edits a field, the field's source becomes the administrator and the plugin's source name is cleared.
- Hosts without the `field_sources` feature (§11.6) ignore both; sending them is harmless.

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
    "homepage": "https://github.com/kryspal/plugin-tmdb",
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
| `plugins[].homepage` | Optional. The plugin's project page, for example its source repository, shown as a link in the admin console. Must be an absolute `https://` URL of at most 512 characters; any other value is ignored with a warning. Hosts that predate this field ignore it. |
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

### 9.5 Testing on your own server

A plugin under development can be loaded straight from a directory, without a catalog or a signature:

1. Lay out the directory like a package, one level per ID and version: `<dir>/<id>/<version>/<id>` (the executable). A `SHA256SUMS` file next to it is optional; when present it is checked, and a version that does not match is skipped.
2. Start the server with `MEDIASERVER_DEV_PLUGINS=<dir>`. With Docker, mount the directory into the container read-only and set the variable to the path inside the container.
3. The plugin appears in the admin console's plugin center as installed and disabled, marked as in development. Enable it and configure it as usual.
4. After rebuilding, use "Reload development plugins" in the plugin center (or restart the server).

Rules:

- When an ID has several versions, the highest one is loaded. A development version takes precedence over the same ID installed from a catalog; remove it from the directory to go back to the catalog version.
- IDs of official plugins are skipped: development mode cannot replace an official plugin.
- Development plugins are never updated from a catalog, and uninstalling one removes only its settings, not your files.
- Capabilities that need a paid plan (§11.5) are limited exactly as for installed plugins.
- There is no sandbox (§10). Use development mode only for plugins you wrote or trust, and remove the variable when you are done.

## 10. Security model

- Installing a plugin means trusting it. Plugins run as the same user as the server; the host only isolates environment variables and does not sandbox.
- The only secrets a plugin can get are its own configuration values, passed in through `Configure`.

## 11. Runtime

What a running plugin can expect from the host, in one place.

### 11.1 Timeouts

The host sets each call's timeout as the gRPC deadline. When it passes, the host cancels the call and does not retry the same call at once; the work it belonged to (a scrape, a subtitle search) reports a temporary failure and is retried later under its own schedule. A timed-out call does not count against the plugin's health, which only the health check decides (§4). Plugins should honor the deadline and stop upstream work when the context is canceled.

| Call | Timeout |
| --- | --- |
| Handshake (start to handshake line) | 10 seconds |
| `Describe` | 10 seconds |
| `Configure` | 30 seconds |
| `Search`, `SearchPerson` | 15 seconds |
| `GetItem`, `GetPerson`, `GetCollection` | 30 seconds |
| `GetImages` | 30 seconds |
| `Recognize` | 5 seconds; 30 seconds in the second round (`previous_unmatched`) |
| Subtitles `Search` / `Download` | 15 / 60 seconds |
| `BeginLink`, `PollLink`, `Unlink` | 15 seconds |
| `Scrobble` | 10 seconds |
| `PullWatched`, `PushWatched` | 60 seconds |
| `GetSegments` / `SubmitSegments` | 15 / 30 seconds |
| `GetDanmaku` | 30 seconds |
| `GetAliasMap` (whole stream) | 5 minutes |
| Cast `Discover`, `Probe` / `Load` / `Prepare`, `Control`, `Status` | 15 / 30 / 10 seconds |
| `RunTask` | 30 minutes |
| Shutdown (`SIGTERM` to exit) | 5 seconds, then `SIGKILL` |

A plugin cannot ask for longer timeouts. When an upstream is slow, cache what you can (§11.2) and return what you have before the deadline.

### 11.2 Data directory

- `HOME` and `KRYSPAL_PLUGIN_DATA_DIR` point to the plugin's own data directory. Use it for caches, downloaded databases and other files the plugin keeps.
- It is kept across upgrades and rollbacks and deleted when the plugin is uninstalled.
- It is also the working directory. The installation directory holding the executable is separate; the plugin must not write there, and it is replaced on upgrade.
- There is no quota and the host never deletes files in it on its own; keep it bounded yourself (expire caches, cap their size). Put files that can be rebuilt under `cache/` inside it: administrators may delete that folder to reclaim space, and the plugin must cope with it disappearing at any time.
- `TMPDIR` is shared with the server; clean up what you put there.

### 11.3 Network

- Plugins may connect to the internet. A plugin that talks to devices on the local network declares the permission `network.lan`, so the admin console can disclose it.
- The host does not set up a proxy for plugins: proxy variables such as `HTTP_PROXY` in the server's environment are not passed on (§2.1). A plugin that needs a proxy offers its own setting for it.
- Rate-limit yourself toward the upstream and report the upstream's limits with `RESOURCE_EXHAUSTED` (§8).

### 11.4 No sandbox

Plugins run as the server's user with no sandbox and no resource limits; the host only cleans the environment. Installing a plugin means trusting its author (§2.1, §10).

### 11.5 Capabilities that need a paid plan

A plugin is never locked by the server's plan: any plugin can be installed, configured and enabled. Some capabilities, though, are used only when the server's plan includes the matching feature, **whoever provides them, official or third-party**. Without it the host behaves as if no plugin declared the capability; it starts using them as soon as the plan allows, without restarting the plugin.

| Capability | Plan |
| --- | --- |
| `media.recognize` | PRO and up |
| `subtitles.search` | PRO and up |
| `segments.provider`, `segments.submit` | PRO and up |
| All other capabilities | Free |

The admin console tells administrators when an installed plugin declares a capability their plan does not include.

### 11.6 Host features

Additions to v1 are compatible: an older host ignores fields it does not know, so a plugin's data can be dropped without an error. `KRYSPAL_HOST_FEATURES` tells the plugin what the host keeps:

| Feature | Meaning |
| --- | --- |
| `attributes` | `ItemMetadata.attributes` and `PersonMetadata.attributes` are stored and shown |
| `photo` | `IMAGE_TYPE_PHOTO` images of people are kept as the person's gallery |
| `config_types` | The setting types `text`, `string_list` and `ordered_multiselect`, and `visible_when` (§5.2) |
| `image_referer` | `Manifest.image_fetch_rules` (§7.2) |
| `tasks` | The host schedules and runs `Manifest.tasks` (§7.8) |
| `image_crop` | `Image.crop`, `Image.focus` and `Image.crop_hint` |
| `config_items` | `ConfigGroup.items` and `ConfigField.item` (§5.2) |
| `item_checks` | `CheckDeclaration.per_item` and `RunCheckRequest.item` (§7.9): the host offers the check per entry; without it the host calls the check once with an empty `item` |
| `scrape_notices` | `notices`, `incomplete` and `retry_after_seconds` on `ItemMetadata`, `PersonMetadata` and `GetImagesResponse` (§7.2) |
| `field_sources` | `ItemMetadata.field_sources`, `PersonMetadata.field_sources` and `Image.source` are stored and shown next to each field (§7.10) |

The variable is missing on hosts older than SDK v0.2.0, which support none of them. A plugin may use this to put the same facts where an older host still shows them (for example, in the overview), but it should still send the new fields. Go plugins call `pluginapi.HostFeatures(os.Getenv)`. In `plugintest`, every feature is announced; set `KRYSPAL_HOST_FEATURES` to an empty string in `Options.Env` to test against an older host.
