package plugintest

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// Limits on attributes (PROTOCOL.md §7): the host drops what breaks them, the suite reports it.
const (
	maxAttributes          = 32
	maxAttributeValueRunes = 256
	maxAttributeKeyLen     = 64
	maxRecognitions        = 3
	maxFieldSources        = 64 // PROTOCOL.md §7.10
	maxSourceNameRunes     = 48
)

func declares(m *pluginv1.Manifest, capability string) bool {
	return slices.Contains(m.GetCapabilities(), capability)
}

var imageCapability = map[pluginv1.ItemType]string{
	pluginv1.ItemType_ITEM_TYPE_MOVIE:  pluginv1.CapabilityImagesMovie,
	pluginv1.ItemType_ITEM_TYPE_SERIES: pluginv1.CapabilityImagesSeries,
}

// itemImages runs GetImages for the item; the plugin declares the matching images.* capability.
func itemImages(ctx context.Context, t *testing.T, conn *grpc.ClientConn, item *pluginv1.ItemMetadata) {
	if len(item.GetProviderIds()) == 0 {
		t.Fatal("GetItem returned no provider_ids, so GetImages has nothing to look up")
	}
	resp, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.GetImagesResponse, error) {
		return pluginv1.NewImageProviderClient(conn).GetImages(cctx, &pluginv1.GetImagesRequest{
			Type: item.GetType(), ProviderIds: item.GetProviderIds(),
		})
	})
	if err != nil {
		t.Fatalf("GetImages(%v): %v", item.GetProviderIds(), err)
	}
	checkImages(t, "GetImages", resp.GetImages(), nil)
}

// personToCheck returns the person ID to run GetPerson with, or "" when the person check does not apply.
func personToCheck(t *testing.T, m *pluginv1.Manifest, pc *PersonCase, item *pluginv1.ItemMetadata) string {
	t.Helper()
	if pc != nil {
		if pc.ID == "" {
			t.Fatal("PersonCase.ID is empty")
		}
		return pc.ID
	}
	if m == nil || !declares(m, pluginv1.CapabilityMetadataPerson) {
		return ""
	}
	for _, c := range item.GetCredits() {
		if c.GetPersonId() != "" {
			return c.GetPersonId()
		}
	}
	return ""
}

// personRoundTrip runs GetPerson, checks the person's provider IDs, images and attributes, and runs GetImages for the
// person when the plugin declares images.person.
func personRoundTrip(ctx context.Context, t *testing.T, conn *grpc.ClientConn, m *pluginv1.Manifest, id string) {
	if !declares(m, pluginv1.CapabilityMetadataPerson) {
		t.Fatalf("the manifest does not declare %s", pluginv1.CapabilityMetadataPerson)
	}
	p, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.PersonMetadata, error) {
		return pluginv1.NewMetadataProviderClient(conn).GetPerson(cctx, &pluginv1.GetPersonRequest{Id: id})
	})
	if err != nil {
		t.Fatalf("GetPerson(%q): %v", id, err)
	}
	if p.GetId() != id || p.GetName() == "" {
		t.Errorf("GetPerson(%q) = id %q name %q; want the same id and a name", id, p.GetId(), p.GetName())
	}
	// The host matches people across items and providers by provider_ids; the plugin's own ID must be among them.
	if !slices.Contains(mapValues(p.GetProviderIds()), id) {
		t.Errorf("GetPerson(%q).provider_ids = %v; want the person's own ID among the values", id, p.GetProviderIds())
	}
	personTypes := []pluginv1.ImageType{pluginv1.ImageType_IMAGE_TYPE_PRIMARY, pluginv1.ImageType_IMAGE_TYPE_PHOTO}
	checkImages(t, "PersonMetadata.images", p.GetImages(), personTypes)
	checkAttributes(t, "PersonMetadata.attributes", p.GetAttributes())
	checkFieldSources(t, "PersonMetadata.field_sources", p, p.GetFieldSources())
	checkNotices(t, "PersonMetadata.notices", p.GetNotices(), p.GetIncomplete(), p.GetRetryAfterSeconds())

	if !declares(m, pluginv1.CapabilityImagesPerson) {
		return
	}
	resp, err := call(ctx, lookupTimeout, func(cctx context.Context) (*pluginv1.GetImagesResponse, error) {
		return pluginv1.NewImageProviderClient(conn).GetImages(cctx, &pluginv1.GetImagesRequest{
			Type: pluginv1.ItemType_ITEM_TYPE_PERSON, ProviderIds: p.GetProviderIds(), Name: p.GetName(), Aliases: p.GetAliases(),
		})
	})
	if err != nil {
		t.Fatalf("GetImages(person %q): %v", id, err)
	}
	checkImages(t, "GetImages(person)", resp.GetImages(), personTypes)
}

// recognize checks the Recognizer contract: an empty path is not an error, results are deterministic, at most three
// candidates, a parser version, and a known path is recognized.
func recognize(ctx context.Context, t *testing.T, conn *grpc.ClientConn, m *pluginv1.Manifest, rc *RecognizeCase) {
	if !declares(m, pluginv1.CapabilityMediaRecognize) {
		t.Fatalf("the manifest does not declare %s", pluginv1.CapabilityMediaRecognize)
	}
	c := pluginv1.NewRecognizerClient(conn)
	call := func(req *pluginv1.RecognizeRequest) *pluginv1.RecognizeResponse {
		t.Helper()
		resp, err := call(ctx, recognizeTimeout, func(cctx context.Context) (*pluginv1.RecognizeResponse, error) {
			return c.Recognize(cctx, req)
		})
		if err != nil {
			t.Fatalf("Recognize(%q): %v", req.GetPath(), err)
		}
		if resp.GetParserVersion() == "" {
			t.Errorf("Recognize(%q).parser_version is empty", req.GetPath())
		}
		if n := len(resp.GetCandidates()); n > maxRecognitions {
			t.Errorf("Recognize(%q) returned %d candidates; at most %d", req.GetPath(), n, maxRecognitions)
		}
		for i, r := range resp.GetCandidates() {
			if conf := r.GetConfidence(); conf < 0 || conf > 1 {
				t.Errorf("Recognize(%q) candidate %d: confidence %v is outside 0–1", req.GetPath(), i, conf)
			}
		}
		return resp
	}

	t.Run("EmptyPath", func(t *testing.T) {
		if resp := call(&pluginv1.RecognizeRequest{}); len(resp.GetCandidates()) > 0 {
			t.Errorf("Recognize with no path returned %d candidates; want none", len(resp.GetCandidates()))
		}
	})
	if rc == nil {
		return
	}
	t.Run("KnownPath", func(t *testing.T) {
		req := &pluginv1.RecognizeRequest{LibraryKind: rc.LibraryKind, Type: rc.Type, Path: rc.Path, Siblings: rc.Siblings}
		first := call(req)
		if len(first.GetCandidates()) == 0 {
			t.Fatalf("Recognize(%q) recognized nothing", rc.Path)
		}
		if len(first.GetCandidates()[0].GetTitles()) == 0 && len(first.GetCandidates()[0].GetIdentifiers()) == 0 {
			t.Errorf("Recognize(%q): the first candidate has neither titles nor identifiers", rc.Path)
		}
		if again := call(req); !proto.Equal(first, again) {
			t.Errorf("Recognize(%q) is not deterministic:\n first %v\nsecond %v", rc.Path, first, again)
		}
	})
}

// checkImages reports images that are not HTTPS or whose type is unknown, or not in allowed when it is non-empty.
func checkImages(t *testing.T, what string, images []*pluginv1.Image, allowed []pluginv1.ImageType) {
	t.Helper()
	for i, img := range images {
		if u, err := url.Parse(img.GetUrl()); err != nil || u.Scheme != "https" || u.Host == "" {
			t.Errorf("%s[%d]: url %q is not an absolute HTTPS URL", what, i, img.GetUrl())
		}
		typ := img.GetType()
		if _, known := pluginv1.ImageType_name[int32(typ)]; !known || typ == pluginv1.ImageType_IMAGE_TYPE_UNSPECIFIED {
			t.Errorf("%s[%d]: type %v is not a known image type", what, i, typ)
		} else if len(allowed) > 0 && !slices.Contains(allowed, typ) {
			t.Errorf("%s[%d]: type %v is not allowed here; want one of %v", what, i, typ, allowed)
		}
		if n := utf8.RuneCountInString(img.GetSource()); n > maxSourceNameRunes {
			t.Errorf("%s[%d]: source has %d characters; at most %d are kept", what, i, n, maxSourceNameRunes)
		}
	}
}

// checkFieldSources reports field_sources entries the host would drop or cut (PROTOCOL.md §7.10): keys must be field
// names of msg, or "attributes." followed by a valid attribute key; values are non-empty source names of at most 48
// characters.
func checkFieldSources(t *testing.T, what string, msg proto.Message, sources map[string]string) {
	t.Helper()
	if len(sources) > maxFieldSources {
		t.Errorf("%s has %d entries; at most %d are kept", what, len(sources), maxFieldSources)
	}
	fields := msg.ProtoReflect().Descriptor().Fields()
	for k, v := range sources {
		if attr, ok := strings.CutPrefix(k, "attributes."); ok {
			if !validAttributeKey(attr) {
				t.Errorf("%s: key %q names an invalid attribute key", what, k)
			}
		} else if k == "field_sources" || fields.ByName(protoreflect.Name(k)) == nil {
			t.Errorf("%s: key %q is not a field of %s", what, k, fields.Get(0).ContainingMessage().Name())
		}
		if v == "" {
			t.Errorf("%s[%q]: empty source name; leave the entry out instead", what, k)
		} else if n := utf8.RuneCountInString(v); n > maxSourceNameRunes {
			t.Errorf("%s[%q]: source name has %d characters; at most %d are kept", what, k, n, maxSourceNameRunes)
		}
	}
}

// checkAttributes reports attributes the host would drop or cut (PROTOCOL.md §7).
func checkAttributes(t *testing.T, what string, attrs map[string]string) {
	t.Helper()
	if len(attrs) > maxAttributes {
		t.Errorf("%s has %d entries; at most %d are kept", what, len(attrs), maxAttributes)
	}
	for k, v := range attrs {
		if !validAttributeKey(k) {
			t.Errorf("%s: key %q is not lowercase letters, digits and underscores starting with a letter", what, k)
		}
		if n := utf8.RuneCountInString(v); n > maxAttributeValueRunes {
			t.Errorf("%s[%q]: value has %d characters; at most %d are kept", what, k, n, maxAttributeValueRunes)
		}
	}
}

func validAttributeKey(k string) bool {
	if k == "" || len(k) > maxAttributeKeyLen || k[0] < 'a' || k[0] > 'z' {
		return false
	}
	for i := 1; i < len(k); i++ {
		if c := k[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// Limits on notices and retry_after_seconds (PROTOCOL.md §7.2): the host cuts what breaks them.
const (
	maxNotices        = 32
	maxNoticeSource   = 48
	maxNoticeMessage  = 200
	minRetryAfterSecs = 60
	maxRetryAfterSecs = 86400
)

// checkNotices reports notices the host would drop or cut, and a retry_after_seconds the host would clamp.
func checkNotices(t *testing.T, what string, notices []*pluginv1.Notice, incomplete bool, retryAfter uint32) {
	t.Helper()
	if len(notices) > maxNotices {
		t.Errorf("%s: %d notices; the host keeps the first %d", what, len(notices), maxNotices)
	}
	for i, n := range notices {
		if n.GetSource() == "" || len([]rune(n.GetSource())) > maxNoticeSource {
			t.Errorf("%s[%d]: source %q must be 1 to %d characters", what, i, n.GetSource(), maxNoticeSource)
		}
		if n.GetStatus() == pluginv1.NoticeStatus_NOTICE_STATUS_UNSPECIFIED {
			t.Errorf("%s[%d]: status is unspecified", what, i)
		}
		if len([]rune(n.GetMessage())) > maxNoticeMessage {
			t.Errorf("%s[%d]: message is %d characters; the host keeps %d", what, i, len([]rune(n.GetMessage())), maxNoticeMessage)
		}
	}
	if !incomplete && retryAfter != 0 {
		t.Errorf("%s: retry_after_seconds is set but incomplete is false; the host ignores it", what)
	}
	if incomplete && retryAfter != 0 && (retryAfter < minRetryAfterSecs || retryAfter > maxRetryAfterSecs) {
		t.Errorf("%s: retry_after_seconds %d is outside %d..%d; the host clamps it", what, retryAfter, minRetryAfterSecs, maxRetryAfterSecs)
	}
}
