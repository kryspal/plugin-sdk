# Kryspal Media Plugin SDK

Kryspal Media Plugin protocol v1: a plugin is a standalone executable that the server starts as a child process and talks to with gRPC over a Unix socket.

| Path | Contents |
| --- | --- |
| [`PROTOCOL.md`](PROTOCOL.md) | Protocol specification: handshake, call order, health checks, configuration, capabilities, errors, plugin catalog and packages |
| `handshake.go` (package `pluginapi`) | Handshake environment variables, cookie, handshake line format and parsing, protocol version negotiation; shared by the host and the SDK |
| `v1/*.proto` | Package `kryspal.plugin.v1`. Plugin side: `Plugin` (Describe, Configure), `MetadataProvider` (Search, GetItem, GetPerson, SearchPerson, GetCollection), `ImageProvider`, `SubtitleProvider`, `UserSync`, `Recognizer`, `SegmentProvider`, `DanmakuProvider`, `PersonAliasProvider`, `CastTarget`. Host side: `Host`, served by the host to plugins |
| `v1/*.pb.go` (package `pluginv1`) | Generated with `gen.sh` |
| `v1/protocol.go` | Protocol constants: capabilities, health check service names, reading and writing the `retry-after-seconds` trailer |
| `i18n/` | Plugin localization helpers: string tables, language fallback chain (zh-CN → zh-TW → en) |
| `sdk/` | SDK for Go plugin authors: `sdk.Main(sdk.Config{Plugin: p, MetadataProvider: p})` |
| `plugintest/` | Conformance suite: runs a plugin binary the way the host does and checks the protocol contract |
| `examples/minimal/` | The quickstart plugin: one capability, no configuration |
| `examples/static/` | Example plugin that returns fixed data; the tests compile it on the spot and start it the way the host does |
| `gen.sh` | Regenerates `v1/*.pb.go` with pinned generator versions |

## Quickstart

The SDK is published as the Go module `github.com/kryspal/plugin-sdk` (Apache-2.0). Releases are `v0.x` until the API is
frozen. Outside this repository, the import paths are `github.com/kryspal/plugin-sdk/sdk` and
`github.com/kryspal/plugin-sdk/pluginv1`.

1. Create a module and add the SDK:

   ```sh
   mkdir minimal && cd minimal
   go mod init example.com/minimal
   go get github.com/kryspal/plugin-sdk@latest
   ```

2. Copy [`examples/minimal/main.go`](examples/minimal/main.go): a movie metadata provider that declares one capability
   (`metadata.movie`), needs no configuration and knows one title.
3. Build a static binary: `CGO_ENABLED=0 go build -trimpath -o minimal .`
4. Check it with the conformance suite (below), then package it with a catalog entry (`PROTOCOL.md` §9) and add the
   catalog to the server.

### Conformance suite

`plugintest.Run` starts a built plugin binary the way the server does and checks the protocol contract: refusing to run
without the cookie or a shared protocol version, the handshake line and socket location, the health check, `Describe`
(required fields, only text changes with the language), `Configure` with your test configuration, an optional metadata
round trip (`Search`, then `GetItem` on the first candidate) and shutdown on `SIGTERM`.

```go
func TestConformance(t *testing.T) {
	plugintest.Run(t, "./minimal", plugintest.Options{
		Metadata: &plugintest.MetadataCase{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "Big Buck Bunny", Year: 2008},
	})
}
```

Not covered yet: images, subtitles, user sync, recognition, segments, danmaku, alias maps, cast targets and the `Host`
service.

The plugin cannot be run by hand: it exits with "this is a Kryspal Media plugin" unless the server starts it (§2).

## For Go plugin authors

```go
type provider struct {
	pluginv1.UnimplementedPluginServer
	pluginv1.UnimplementedMetadataProviderServer
}

// Implement Describe, Configure, and the MetadataProvider methods for the capabilities you declare.

func main() {
	p := &provider{}
	sdk.Main(sdk.Config{Plugin: p, MetadataProvider: p})
}
```

- Logs go to stderr; nothing may be written to stdout after the handshake (`sdk.Main` points `os.Stdout` at stderr).
- Errors: `sdk.NotFound`, `sdk.InvalidArgument`, `sdk.NeedsConfiguration`, `sdk.Unavailable`, `sdk.RateLimited(retryAfter, msg)`. A panic in a handler becomes `INTERNAL` and does not crash the plugin.
- **Localization**: `Describe` and `Configure` requests carry a language, and all display text must be returned in that language (rules in `PROTOCOL.md` §5.1). Use `pluginapi/i18n`:

  ```go
  var messages = i18n.New(map[string]map[string]string{
  	"en":    {"desc": "Example provider.", "required": "Required."},
  	"zh-TW": {"desc": "…", "required": "…"}, // Traditional Chinese text
  	"zh-CN": {"desc": "…", "required": "…"}, // Simplified Chinese text
  })

  func (p *provider) Describe(_ context.Context, req *pluginv1.DescribeRequest) (*pluginv1.Manifest, error) {
  	t := messages.For(req.GetLanguage()) // zh-CN → zh-TW → en; ja, the empty string, etc. → en
  	return &pluginv1.Manifest{Description: t.T("desc") /* … */}, nil
  }
  ```

  When `T` cannot find a key it returns the key itself, so a missing translation is obvious at a glance; every key needs at least `en`. See `examples/static` for a complete example and `examples/static/i18n_test.go` for a test example (English contains no Han characters; Simplified and Traditional Chinese are not mixed).
- Versions: releases are `v0.x` until the API is frozen; `v1.0.0` follows the freeze.
- **Images, subtitles, user sync**: the `ImageProvider`, `SubtitleProvider` and `UserSync` fields of `sdk.Config` (required when the matching capability is declared). Error helpers: `sdk.Unauthenticated` (the credential is no longer valid), `sdk.RateLimitedCredential` (rate-limits only this credential); `examples/static/services.go` demonstrates all three services. Plugins are stateless: the host passes the credential in on every call, and updates come back with the response.

## Licensing for plugin authors

The SDK is licensed under the Apache License 2.0 (see `LICENSE`; trademark rules in `TRADEMARKS.md`). You may use it in
open-source and closed-source plugins. A plugin binary built with the SDK links these components, and distributing the
binary carries their obligations:

| Component | License | When you distribute a binary |
| --- | --- | --- |
| This SDK (including the OpenCC data in `hanconv/`, if you import it) | Apache-2.0 | Include the license text and reproduce `NOTICE` (and `hanconv/NOTICE` if you use `hanconv`) |
| `google.golang.org/grpc` | Apache-2.0, with a `NOTICE.txt` | Include the license text and reproduce its `NOTICE.txt` |
| `google.golang.org/genproto/googleapis/rpc` | Apache-2.0 | Include the license text |
| `google.golang.org/protobuf` | BSD-3-Clause (plus a patent grant) | Reproduce the copyright notice and license terms in your documentation |
| `golang.org/x/net`, `golang.org/x/sys`, `golang.org/x/text` | BSD-3-Clause (plus a patent grant) | Same as above |

None of the SDK's dependencies is under a copyleft license. The exact list depends on what your plugin imports; a tool
such as [go-licenses](https://github.com/google/go-licenses) can produce it for your binary:

```sh
go run github.com/google/go-licenses@v1.6.0 report ./... > THIRD_PARTY_LICENSES.csv
go run github.com/google/go-licenses@v1.6.0 save ./... --save_path=third_party_licenses
```

More things to keep in mind:

- To be listed in the official plugin catalog, a plugin must be open source under an OSI-approved license. Plugins that
  are not listed may use any license and are installed by users from catalogs they add themselves.
- If you choose a GPL license, prefer GPL-3.0 or "GPL-2.0-or-later": the Apache License 2.0 is not compatible with
  GPL-2.0-only.
- Complying with the terms of the services your plugin calls (attribution, caching limits, commercial use) is your
  responsibility. Use the `Attribution` field of the manifest for the credits a service requires.
- An application key compiled into a binary (for example with `-ldflags -X`) is not secret: anyone can extract it.
  Check whether the service's terms allow that, and offer a user-supplied key as a `secret` configuration field.
- Never write keys or tokens to logs or error messages.

## Tests

```sh
go test ./...   # from this directory
```

The `examples/static` tests `go build` the example plugin into `t.TempDir()`, start it in a clean environment, perform the handshake, and call Describe, the health check, Configure, Search, GetItem, GetPerson and GetCollection over the Unix socket; they verify FAILED_PRECONDITION and the retry-after trailer of RESOURCE_EXHAUSTED, and finally send SIGTERM to confirm a graceful shutdown that removes the socket.

## Regenerating

```sh
PROTOC=/path/to/protoc ./gen.sh   # from this directory; protoc-gen-go and protoc-gen-go-grpc must be on PATH (protoc 36.2, protoc-gen-go v1.36.12, protoc-gen-go-grpc 1.6.2)
```
