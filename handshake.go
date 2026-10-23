// Package pluginapi defines the constants and handshake line format of the Kryspal Media plugin handshake, shared by the
// host and the SDK.
//
// See PROTOCOL.md for the full specification. The gRPC services are defined in the subpackage v1 (pluginv1); Go plugin
// authors use the subpackage sdk. This package depends only on the standard library.
package pluginapi

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Environment variables for the handshake. The host starts a plugin with a clean environment, passing only these three
// variables plus PATH, TMPDIR and HOME, and SSL_CERT_FILE and SSL_CERT_DIR when the host has them set.
const (
	// EnvCookie must equal CookieValue; otherwise the plugin should refuse to start, because it was not started by the host.
	EnvCookie = "KRYSPAL_PLUGIN_COOKIE"
	// EnvProtocols holds the protocol major versions the host supports, as comma-separated decimal numbers, e.g. "1" or "1,2".
	EnvProtocols = "KRYSPAL_PLUGIN_PROTOCOLS"
	// EnvSocketDir is the 0700 directory the host created for this plugin; the plugin creates its Unix socket in it.
	EnvSocketDir = "KRYSPAL_PLUGIN_SOCKET_DIR"
	// EnvDataDir is the plugin's own data directory (the same path as HOME): keep caches and other files here. It is
	// kept across upgrades and removed on uninstall. Hosts before SDK v0.2.0 do not set it; use DataDir, which falls
	// back to HOME.
	EnvDataDir = "KRYSPAL_PLUGIN_DATA_DIR"
	// EnvHostFeatures lists, comma-separated, the compatible protocol additions the host stores and uses (the
	// HostFeature* values). Older hosts do not set it; see HostFeatures.
	EnvHostFeatures = "KRYSPAL_HOST_FEATURES"
	// EnvHostSocket and EnvHostToken are the Unix socket path and one-time token of the service the host offers to plugins
	// (Host). They differ on every process start; older hosts do not set them, in which case the SDK's Host()
	// returns ErrHostUnavailable and the plugin should degrade gracefully.
	EnvHostSocket = "KRYSPAL_HOST_SOCKET"
	EnvHostToken  = "KRYSPAL_HOST_TOKEN"
)

// CookieValue is the fixed magic value of EnvCookie. It is not a secret; it only keeps users from running a plugin directly.
const CookieValue = "7f3a9c52-kryspal-plugin"

// Network types in the handshake line.
const (
	NetworkUnix = "unix"
	// NetworkTCP is reserved for future plugins that run over the network; the first host version does not accept it.
	NetworkTCP = "tcp"
)

// MaxHandshakeLine is the maximum length in bytes of the handshake line (excluding the newline). The host treats a longer
// line as a failed handshake.
const MaxHandshakeLine = 1024

// SocketName is the socket file name the SDK creates in EnvSocketDir. Plugins in other languages may choose their own
// name, but the full path must not exceed the Unix socket limit (107 bytes on Linux).
const SocketName = "plugin.sock"

// ErrHandshake reports a malformed handshake line or handshake environment variable.
var ErrHandshake = errors.New("plugin handshake")

// Handshake is the handshake line a plugin prints on stdout: <major version>|<network>|<address>.
type Handshake struct {
	Protocol int    // protocol major version chosen by the plugin
	Network  string // NetworkUnix or NetworkTCP
	Address  string // absolute socket path for unix; host:port for tcp
}

// String returns the handshake line (without a newline).
func (h Handshake) String() string {
	return strconv.Itoa(h.Protocol) + "|" + h.Network + "|" + h.Address
}

// ParseHandshake parses a handshake line (a trailing \n or \r\n is allowed). It checks only the format, not whether
// the host accepts the network and version.
func ParseHandshake(line string) (Handshake, error) {
	line = strings.TrimRight(line, "\r\n")
	if len(line) > MaxHandshakeLine {
		return Handshake{}, fmt.Errorf("%w: line longer than %d bytes", ErrHandshake, MaxHandshakeLine)
	}
	parts := strings.SplitN(line, "|", 3)
	if len(parts) != 3 {
		return Handshake{}, fmt.Errorf("%w: want <protocol>|<network>|<address>, got %q", ErrHandshake, line)
	}
	proto, err := strconv.Atoi(parts[0])
	if err != nil || proto <= 0 {
		return Handshake{}, fmt.Errorf("%w: bad protocol version %q", ErrHandshake, parts[0])
	}
	h := Handshake{Protocol: proto, Network: parts[1], Address: parts[2]}
	switch h.Network {
	case NetworkUnix:
		if !strings.HasPrefix(h.Address, "/") {
			return Handshake{}, fmt.Errorf("%w: unix address must be absolute, got %q", ErrHandshake, h.Address)
		}
	case NetworkTCP:
		if h.Address == "" {
			return Handshake{}, fmt.Errorf("%w: empty tcp address", ErrHandshake)
		}
	default:
		return Handshake{}, fmt.Errorf("%w: unknown network %q", ErrHandshake, h.Network)
	}
	return h, nil
}

// ParseProtocols parses the value of EnvProtocols and returns the major versions sorted ascending without duplicates.
func ParseProtocols(s string) ([]int, error) {
	var out []int
	for f := range strings.SplitSeq(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		v, err := strconv.Atoi(f)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("%w: bad protocol version %q in %s", ErrHandshake, f, EnvProtocols)
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %s is empty", ErrHandshake, EnvProtocols)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// FormatProtocols formats a list of major versions as the value of EnvProtocols.
func FormatProtocols(versions []int) string {
	s := make([]string, len(versions))
	for i, v := range versions {
		s[i] = strconv.Itoa(v)
	}
	return strings.Join(s, ",")
}

// NegotiateProtocol returns the highest major version both sides support, or ErrHandshake when there is none.
func NegotiateProtocol(host, plugin []int) (int, error) {
	best := 0
	for _, v := range plugin {
		if v > best && slices.Contains(host, v) {
			best = v
		}
	}
	if best == 0 {
		return 0, fmt.Errorf("%w: no common protocol version (host %v, plugin %v)", ErrHandshake, host, plugin)
	}
	return best, nil
}

// DataDir returns the plugin's own data directory: EnvDataDir when the host sets it, otherwise HOME (older hosts give
// the plugin its own HOME with the same meaning). getenv is usually os.Getenv; an empty result means the plugin was not
// started by a host.
func DataDir(getenv func(string) string) string {
	if d := getenv(EnvDataDir); d != "" {
		return d
	}
	return getenv("HOME")
}

// Values of EnvHostFeatures. Each names a compatible addition to protocol v1 that an older host ignores, so a plugin
// can tell whether the host will keep what it sends (for example, to put the same facts into the overview for hosts
// that drop attributes).
const (
	HostFeatureAttributes = "attributes" // ItemMetadata.attributes and PersonMetadata.attributes
	HostFeaturePhoto      = "photo"      // IMAGE_TYPE_PHOTO for people
	// HostFeatureStill: IMAGE_TYPE_STILL for items. Without it the host skips STILL images, so a plugin sends its stills
	// as BACKDROP images after the main backdrop instead.
	HostFeatureStill = "still"
	// HostFeatureConfigTypes: the setting types text, string_list and ordered_multiselect, and ConfigField.visible_when.
	HostFeatureConfigTypes = "config_types"
	// HostFeatureImageReferer: Manifest.image_fetch_rules.
	HostFeatureImageReferer = "image_referer"
	// HostFeatureTasks: the host schedules and runs Manifest.tasks (capability plugin.tasks). Without it, a plugin that
	// needs periodic work runs its own timer.
	HostFeatureTasks = "tasks"
	// HostFeatureImageCrop: Image.crop, Image.focus and Image.crop_hint. Without it the host uses the whole image, so a
	// plugin that needs a crop (for example a portrait poster cut from a wide cover) should send an uncropped image only
	// where it fits.
	HostFeatureImageCrop = "image_crop"
	// HostFeatureConfigItems: ConfigGroup.items and ConfigField.item. Without it the host shows an item's fields as
	// ordinary fields of the group, so a plugin with many items may fall back to a select plus visible_when.
	HostFeatureConfigItems = "config_items"
	// HostFeatureItemChecks: the host shows a per_item check (§7.9) next to every entry of the group's list and calls
	// RunCheck with RunCheckRequest.item. Without it the host runs the check once for the whole group with an empty
	// item, so a plugin should then test every entry the user configured.
	HostFeatureItemChecks = "item_checks"
	// HostFeatureScrapeNotices: the host shows Notice entries from metadata and image results to the user and fetches
	// again after retry_after_seconds when a result says incomplete. Without it notices are ignored and
	// an incomplete result is final.
	HostFeatureScrapeNotices = "scrape_notices"
	// HostFeatureFieldSources: the host stores ItemMetadata.field_sources, PersonMetadata.field_sources and
	// Image.source next to each field's provenance and shows them (§7.10). Without it they are ignored; sending them is
	// harmless.
	HostFeatureFieldSources = "field_sources"
	// HostFeaturePersonBackdrop: the host stores a person's IMAGE_TYPE_BACKDROP (PersonMetadata.images and
	// GetImages with ITEM_TYPE_PERSON) as the person's backdrop and only fills a missing one. Older hosts take every
	// person image as a photo candidate, so send person backdrops only when the host declares this.
	// Automatic fills use only the backdrops in PersonMetadata.images (what the metadata source thinks fits); the host
	// does not fill person backdrops from GetImages with ITEM_TYPE_PERSON and IMAGE_TYPE_BACKDROP.
	HostFeaturePersonBackdrop = "person_backdrop"
	// HostFeatureAutoFillLimit: the host stores and uses the setting key autofill_daily_limit (a plugin's daily limit
	// for automatic subtitle downloads, 0 means unlimited). Declare the key, only to give it a default, when the host
	// declares this; on older hosts it would be a setting with no effect.
	HostFeatureAutoFillLimit = "autofill_limit"
	// HostFeatureSearchConfirmed: SearchCandidate.confirmed. The host applies a single confirmed candidate
	// automatically whatever its score; several confirmed candidates are ambiguous and left for the user to choose.
	// Without it the host judges candidates by its own scoring only, so a plugin still sends a score.
	HostFeatureSearchConfirmed = "search_confirmed"
	// HostFeatureBadgeSuggestions: Manifest.suggested_badge_rules. The host lists them on its badge settings for the
	// administrator to add; it never enables them by itself. Without it they are ignored.
	HostFeatureBadgeSuggestions = "badge_suggestions"
	// HostFeatureConfirmedNumbering: Recognition.numbering_confirmed. The host takes a confirmed season,
	// episode and episode_end as an episode's numbering in place of its own parse; a season folder still decides the
	// season of the files inside it, and NFO numbers still come first. Without it the field is ignored.
	HostFeatureConfirmedNumbering = "confirmed_numbering"
	// HostFeatureLibraryConfig: ConfigField.library_override, LibraryContext in requests, and Configure with dry_run
	// for per-library settings. Without it requests carry no LibraryContext and plugins use their global settings only.
	HostFeatureLibraryConfig = "library_config"
	// HostFeatureRecognitionHint: SearchRequest.recognition and GetItemRequest.recognition. The host also writes the
	// provider IDs a recognition plugin found into the item's provider IDs (not over manual or NFO ones). Without it
	// recognition results reach metadata plugins only as titles and known IDs.
	HostFeatureRecognitionHint = "recognition_hint"
	// HostFeatureLookupNames: GetDanmakuRequest and SubtitleTarget carry original_title and product_code. Without it
	// both are empty, and a plugin looks up by title, year, provider IDs and season and episode only.
	HostFeatureLookupNames = "lookup_names"
)

// HostFeatures returns the features listed in EnvHostFeatures. The result is empty, not nil, when the variable is
// missing (a host older than SDK v0.2.0): such a host supports none of them. getenv is usually os.Getenv.
func HostFeatures(getenv func(string) string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.Split(getenv(EnvHostFeatures), ",") {
		if f = strings.TrimSpace(f); f != "" {
			out[f] = true
		}
	}
	return out
}
