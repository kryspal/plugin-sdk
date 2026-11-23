// Package pluginv1 holds the gRPC definitions of the Kryspal Media Plugin protocol v1 (generated from plugin.proto,
// metadata.proto, image.proto, rating.proto, subtitle.proto, usersync.proto, recognize.proto, segments.proto, danmaku.proto,
// discover.proto and host.proto) and the protocol constants. See PROTOCOL.md at the module root for the specification.
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
	// Ratings: GetRatings adds ratings of the sources in Manifest.rating_sources to items matched by another plugin.
	CapabilityRatingsMovie  = "ratings.movie"
	CapabilityRatingsSeries = "ratings.series"
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
	// Danmaku match picking: SearchDanmakuCandidates and GetDanmakuRequest.match_override (with danmaku.provider).
	CapabilityDanmakuMatch = "danmaku.match"
	// Cast targets: Discover, Probe, Prepare, Load, Control, Status.
	CapabilityCastTarget = "cast.target"
	// Person alias maps: GetAliasMap.
	CapabilityPeopleAliasMap = "people.aliasmap"
	// Plugin tasks: RunTask for the tasks in Manifest.tasks.
	CapabilityPluginTasks = "plugin.tasks"
	// Settings checks: RunCheck for the checks in Manifest.checks.
	CapabilityPluginChecks = "plugin.checks"
	// Browsing works outside the library: GetDiscoverPage, SearchWorks, GetWork.
	CapabilityDiscoverSource = "discover.source"
	// Acting on works: GetWorkStates, RunWorkAction for the actions in Manifest.work_actions. Receives member information.
	CapabilityWorkAction = "work.action"
	// Acting on the plugin's own discover pages: GetPageState, RunPageAction for the actions in Manifest.page_actions.
	// Receives member information. Needs discover.source.
	CapabilityPageAction = "page.action"
	// A member's subscriptions: ListMemberSubscriptions, RunSubscriptionAction for the actions in
	// Manifest.subscription_actions. Receives member information.
	CapabilityDiscoverSubscriptions = "discover.subscriptions"
	// A member's calendar: ListMemberCalendar. Receives member information.
	CapabilityDiscoverCalendar = "discover.calendar"
	// A network access provider: the plugin joins the server to another network and forwards that network's requests
	// through the ingress socket (KRYSPAL_INGRESS_SOCKET), reporting its state with Host.ReportNetworkAccess. Ingress
	// traffic always counts as outside the local network. Free on all plans.
	CapabilityNetworkAccess = "network.access"
	// A network identity provider: on the ingress, the plugin also sends the identity its network proved for the
	// connection (the X-Kryspal-Identity-* headers), so an administrator can bind a network user to members for sign-in
	// without a password. Requires network.access. Free on all plans.
	CapabilityNetworkIdentity = "network.identity"
)

// Permissions (Manifest.permissions). The host ignores permissions it does not recognize.
const (
	// PermissionLLM: use AI assist (Host.LLMComplete).
	PermissionLLM = "llm"
	// PermissionLAN: the plugin sends multicast and connects to devices on the local network. Disclosed to the
	// administrator; enabling the plugin is the consent, unlike llm which is allowed per plugin.
	PermissionLAN = "network.lan"
	// PermissionDiscoverSeeds: the plugin receives GetDiscoverPageRequest.seeds. The administrator allows each ID of
	// Manifest.discover_seed_ids for each plugin; it is off by default.
	PermissionDiscoverSeeds = "discover.seeds"
	// DiscoverSeedIDProductCode is the entry of Manifest.discover_seed_ids that asks for DiscoverSeed.product_code.
	DiscoverSeedIDProductCode = "product_code"
	// PermissionDiscoverLibraryFiles: GetWorkStatesRequest carries WorkRef.library_files, a summary of the library's files
	// of each work in the library that the member can see (size, height, running time, whether it is a link, the version's
	// name; never a path). The administrator allows it for each plugin; it is off by default.
	PermissionDiscoverLibraryFiles = "discover.library_files"
)

// SubscriptionFormNew is the value of SuggestChoicesRequest.form for the new-subscription form (GetSubscriptionForm).
const SubscriptionFormNew = "subscription.new"

// Fixed codes of the Host service (gRPC status messages; they do not vary by language).
const (
	CodeAIAssistDisabled   = "ai_assist_disabled"
	CodeAIAssistNotAllowed = "ai_assist_not_allowed"
	CodeHostTokenInvalid   = "host_token_invalid"
)

// MetadataHostToken is the gRPC metadata key under which a plugin sends the token when calling the Host service.
const MetadataHostToken = "x-kryspal-host-token"

// CodeNetworkAccessNotDeclared is the PERMISSION_DENIED message of Host.ReportNetworkAccess when the manifest does not
// declare network.access.
const CodeNetworkAccessNotDeclared = "network_access_not_declared"

// Headers a network access provider writes on requests it forwards through the ingress socket. The host reads them only
// on the ingress, and only after checking HeaderIngressToken; it removes them before any handler sees the request. On the
// server's ordinary ports they have no effect. Values other than the token are percent-encoded UTF-8.
const (
	// HeaderIngressToken carries the value of KRYSPAL_INGRESS_TOKEN. A missing or wrong token gets 403.
	HeaderIngressToken = "X-Kryspal-Ingress-Token"
	// HeaderPeer is the peer address of the connection on the plugin's network, as ip:port. Loopback, unspecified and
	// multicast addresses get 400.
	HeaderPeer = "X-Kryspal-Peer"
	// The identity the network proved for the connection (network.identity only). The host takes all three or none: when
	// one exceeds its limit or does not decode, the request is handled as having no identity. Bindings use the subject
	// only; the login and the device name are for display.
	HeaderIdentitySubject    = "X-Kryspal-Identity-Subject"     // stable, opaque user ID on that network; at most 128 bytes
	HeaderIdentityLogin      = "X-Kryspal-Identity-Login"       // display login, e.g. an email address; at most 256 bytes
	HeaderIdentityDeviceName = "X-Kryspal-Identity-Device-Name" // display device name; at most 128 bytes
)

// Limits of the identity headers, in bytes after decoding.
const (
	MaxIdentitySubject    = 128
	MaxIdentityLogin      = 256
	MaxIdentityDeviceName = 128
)

// Limits of the NetworkAccessStatus fields (Host.ReportNetworkAccess), in characters (Unicode code points). The host
// rejects a report that exceeds one of them.
const (
	MaxNetworkMessage  = 500  // message
	MaxNetworkLabel    = 128  // action_label and network_name
	MaxNetworkURL      = 2048 // action_url and client_app_url
	MaxNetworkSteps    = 5    // number of client_steps
	MaxNetworkStep     = 500  // each client step
	MaxNetworkLanguage = 35   // language (a BCP 47 tag)
)

// Limits of NetworkIdentity.CheckIdentities (capability network.identity, PROTOCOL.md §7.14).
const (
	// MaxCheckIdentitiesSubjects is the most subjects the host sends in one CheckIdentities call.
	MaxCheckIdentitiesSubjects = 1000
	// CheckIdentitiesTimeout is the deadline the host gives one CheckIdentities call.
	CheckIdentitiesTimeout = 30 * time.Second
)

// Service names for the standard health check (grpc.health.v1). The empty string stands for the whole plugin.
const (
	HealthServicePlugin              = "kryspal.plugin.v1.Plugin"
	HealthServiceMetadataProvider    = "kryspal.plugin.v1.MetadataProvider"
	HealthServiceImageProvider       = "kryspal.plugin.v1.ImageProvider"
	HealthServiceRatingProvider      = "kryspal.plugin.v1.RatingProvider"
	HealthServiceSubtitleProvider    = "kryspal.plugin.v1.SubtitleProvider"
	HealthServiceUserSync            = "kryspal.plugin.v1.UserSync"
	HealthServiceRecognizer          = "kryspal.plugin.v1.Recognizer"
	HealthServiceSegmentProvider     = "kryspal.plugin.v1.SegmentProvider"
	HealthServiceDanmakuProvider     = "kryspal.plugin.v1.DanmakuProvider"
	HealthServicePersonAliasProvider = "kryspal.plugin.v1.PersonAliasProvider"
	HealthServiceCastTarget          = "kryspal.plugin.v1.CastTarget"
	HealthServiceTaskRunner          = "kryspal.plugin.v1.TaskRunner"
	HealthServiceChecker             = "kryspal.plugin.v1.Checker"
	HealthServiceNetworkIdentity     = "kryspal.plugin.v1.NetworkIdentity"
	HealthServiceDiscoverSource      = "kryspal.plugin.v1.DiscoverSource"
	HealthServiceWorkActionProvider  = "kryspal.plugin.v1.WorkActionProvider"
	HealthServicePageActionProvider  = "kryspal.plugin.v1.PageActionProvider"
	HealthServiceMemberSubscriptions = "kryspal.plugin.v1.MemberSubscriptionProvider"
	HealthServiceMemberCalendar      = "kryspal.plugin.v1.MemberCalendarProvider"
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
