// Package pluginv1 holds the gRPC definitions of the Kryspal Media Plugin protocol v1 (generated from plugin.proto,
// metadata.proto, image.proto, subtitle.proto, usersync.proto, recognize.proto, segments.proto, danmaku.proto and
// host.proto) and the protocol constants. See PROTOCOL.md at the module root for the specification.
//
// Apart from this file, every *.pb.go file is generated; regenerate them with pluginapi/gen.sh, do not edit them by hand.
package pluginv1

import (
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/metadata"
)

// ProtocolVersion is the protocol major version this package implements.
const ProtocolVersion = 1

// SDKMarker is the value with which a plugin catalog version entry (CatalogVersion.sdk) declares "this version is built
// for the kryspal.plugin.v1 protocol". A version without this field (or with a different value) was built
// for the protocol before the rename; newer hosts list it as not installable and say why.
const SDKMarker = "kryspal-plugin/1"

// Capabilities (Manifest.capabilities). The host ignores capabilities it does not recognize.
const (
	CapabilityMetadataMovie      = "metadata.movie"      // movies: Search, GetItem
	CapabilityMetadataSeries     = "metadata.series"     // series, including seasons and episodes: Search, GetItem
	CapabilityMetadataPerson     = "metadata.person"     // people: GetPerson
	CapabilityMetadataCollection = "metadata.collection" // collections: GetCollection
	CapabilityMetadataTrailers   = "metadata.trailers"   // trailers: GetItem returns ItemMetadata.trailers
	// CapabilityMetadataPersonSearch: look up people by name and title hints: SearchPerson
	CapabilityMetadataPersonSearch = "metadata.person.search"
	// CapabilityMetadataSearchHints: Search reads the name recognition hints (SearchRequest fields 7–13) and fills in
	// SearchCandidate.matched_title and reasons. Plugins that do not declare it receive only name and year.
	CapabilityMetadataSearchHints = "metadata.search.hints"
	// CapabilityMetadataSeriesAbsolute: GetItem accepts an episode's absolute episode number (GetItemRequest.absolute_episode_number).
	CapabilityMetadataSeriesAbsolute = "metadata.series.absolute"

	// Images: GetImages.
	CapabilityImagesMovie  = "images.movie"
	CapabilityImagesSeries = "images.series"
	// CapabilityImagesPerson: GetImages(ITEM_TYPE_PERSON) returns a person's photos; the request carries provider_ids, name and aliases.
	CapabilityImagesPerson = "images.person"
	// Subtitles: Search, Download.
	CapabilitySubtitlesSearch = "subtitles.search"
	// User sync: member linking, now-playing reports (scrobbling), two-way sync of watched state.
	CapabilityUserSyncLink        = "usersync.link"
	CapabilityUserSyncScrobble    = "usersync.scrobble"
	CapabilityUserSyncWatchedPull = "usersync.watched.pull"
	CapabilityUserSyncWatchedPush = "usersync.watched.push"
	// Name recognition: Recognize.
	CapabilityMediaRecognize = "media.recognize"
	// Segment providers: GetSegments, SubmitSegments.
	CapabilitySegmentsProvider = "segments.provider"
	CapabilitySegmentsSubmit   = "segments.submit"
	// Danmaku providers: GetDanmaku.
	CapabilityDanmakuProvider = "danmaku.provider"
	// Cast targets: Discover, Probe, Prepare, Load, Control, Status.
	CapabilityCastTarget = "cast.target"
	// Person alias maps: GetAliasMap.
	CapabilityPeopleAliasMap = "people.aliasmap"
	// Plugin tasks: RunTask for the tasks in Manifest.tasks.
	CapabilityPluginTasks = "plugin.tasks"
	// Settings checks: RunCheck for the checks in Manifest.checks.
	CapabilityPluginChecks = "plugin.checks"
)

// Permissions (Manifest.permissions). The host ignores permissions it does not recognize.
const (
	// PermissionLLM: use AI assist (Host.LLMComplete).
	PermissionLLM = "llm"
	// PermissionLAN: the plugin sends multicast and connects to devices on the local network. Disclosed to the
	// administrator; enabling the plugin is the consent, unlike llm which is allowed per plugin.
	PermissionLAN = "network.lan"
)

// Fixed codes of the Host service (gRPC status messages; they do not vary by language).
const (
	CodeAIAssistDisabled   = "ai_assist_disabled"
	CodeAIAssistNotAllowed = "ai_assist_not_allowed"
	CodeHostTokenInvalid   = "host_token_invalid"
)

// MetadataHostToken is the gRPC metadata key under which a plugin sends the token when calling the Host service.
const MetadataHostToken = "x-kryspal-host-token"

// Service names for the standard health check (grpc.health.v1). The empty string stands for the whole plugin.
const (
	HealthServicePlugin              = "kryspal.plugin.v1.Plugin"
	HealthServiceMetadataProvider    = "kryspal.plugin.v1.MetadataProvider"
	HealthServiceImageProvider       = "kryspal.plugin.v1.ImageProvider"
	HealthServiceSubtitleProvider    = "kryspal.plugin.v1.SubtitleProvider"
	HealthServiceUserSync            = "kryspal.plugin.v1.UserSync"
	HealthServiceRecognizer          = "kryspal.plugin.v1.Recognizer"
	HealthServiceSegmentProvider     = "kryspal.plugin.v1.SegmentProvider"
	HealthServiceDanmakuProvider     = "kryspal.plugin.v1.DanmakuProvider"
	HealthServicePersonAliasProvider = "kryspal.plugin.v1.PersonAliasProvider"
	HealthServiceCastTarget          = "kryspal.plugin.v1.CastTarget"
	HealthServiceTaskRunner          = "kryspal.plugin.v1.TaskRunner"
	HealthServiceChecker             = "kryspal.plugin.v1.Checker"
)

// TrailerRetryAfter is the trailer key attached to a RESOURCE_EXHAUSTED error; its value is a positive decimal number of
// seconds. The host pauses the whole plugin until then; when it is missing or unparsable, the host uses DefaultRetryAfter.
const TrailerRetryAfter = "retry-after-seconds"

// TrailerRetryAfterScope is an optional trailer key attached to a RESOURCE_EXHAUSTED error: the scope of
// the rate limit. Its value is RetryScopePlugin (the default, pauses the whole plugin) or RetryScopeCredential (pauses only
// this one credential, e.g. Trakt's per-user quota).
const TrailerRetryAfterScope = "retry-after-scope"

// Rate limit scopes (values of TrailerRetryAfterScope).
const (
	RetryScopePlugin     = "plugin"
	RetryScopeCredential = "credential"
)

// RetryScope reads the rate limit scope from the trailer; when it is missing or unrecognized, it returns RetryScopePlugin
// (the existing behavior).
func RetryScope(trailer metadata.MD) string {
	if vals := trailer.Get(TrailerRetryAfterScope); len(vals) > 0 && strings.TrimSpace(vals[0]) == RetryScopeCredential {
		return RetryScopeCredential
	}
	return RetryScopePlugin
}

// DefaultRetryAfter is the pause the host applies when a RESOURCE_EXHAUSTED error carries no TrailerRetryAfter.
const DefaultRetryAfter = 60 * time.Second

// MaxRetryAfter is the longest pause the host accepts; longer values are capped to it, so a plugin cannot pause itself
// indefinitely.
const MaxRetryAfter = time.Hour

// RetryAfter reads TrailerRetryAfter from the trailer. When it is missing or malformed, it returns DefaultRetryAfter and
// false; values above MaxRetryAfter return MaxRetryAfter.
func RetryAfter(trailer metadata.MD) (time.Duration, bool) {
	vals := trailer.Get(TrailerRetryAfter)
	if len(vals) == 0 {
		return DefaultRetryAfter, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(vals[0]), 10, 64)
	if err != nil || n <= 0 {
		return DefaultRetryAfter, false
	}
	if n >= int64(MaxRetryAfter/time.Second) {
		return MaxRetryAfter, true
	}
	return time.Duration(n) * time.Second, true
}

// RetryAfterTrailer builds a trailer carrying TrailerRetryAfter; a d below one second is rounded up to one second.
func RetryAfterTrailer(d time.Duration) metadata.MD {
	secs := int64((d + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return metadata.Pairs(TrailerRetryAfter, strconv.FormatInt(secs, 10))
}
