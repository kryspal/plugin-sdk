// Command static is the minimal example plugin: a metadata provider that returns fixed data (plus three demo services for
// images, subtitles and user sync, see services.go). It demonstrates how to use the SDK and is also used by the host's
// tests. It makes no network connections.
//
// Configuration: api_key (secret, required, only "static-key" is accepted) and language_mode (select).
// Until it is configured, metadata calls return FAILED_PRECONDITION; a search for the name "rate-limit" returns
// RESOURCE_EXHAUSTED (retry-after 30 seconds).
package main

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kryspal/plugin-sdk/i18n"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
	"github.com/kryspal/plugin-sdk/sdk"
)

// Fixed data.
const (
	movieID      = "1001"
	personID     = "2001"
	collectionID = "3001"
	validKey     = "static-key"
	version      = "1.0.0"
)

type provider struct {
	pluginv1.UnimplementedPluginServer
	pluginv1.UnimplementedMetadataProviderServer

	mu         sync.Mutex
	configured bool
}

func main() {
	p := &provider{}
	sdk.Main(sdk.Config{Plugin: p, MetadataProvider: p, ImageProvider: images{}, SubtitleProvider: subtitles{}, UserSync: usersync{}})
}

// String tables: the host calls Describe and Configure with a BCP 47 language tag. Languages without a
// string table get their strings along the i18n fallback chain, ending in English; every key needs at least English.
var messages = i18n.New(map[string]map[string]string{
	"en": {
		"desc":          "Example provider returning fixed data.",
		"advanced":      "Advanced",
		"apiKeyLabel":   "API key",
		"apiKeyHelp":    `Use "static-key".`,
		"modeLabel":     "Language mode",
		"modeAuto":      "Automatic",
		"modeOriginal":  "Original",
		"attribution":   "Data from the static example.",
		"required":      "Required.",
		"invalidKey":    "Invalid API key.",
		"unknownOption": "Unknown option.",
		"movieName":     "Static Movie",
		"movieOverview": "A movie that never changes.",
	},
	"zh-TW": {
		"desc":          "回傳固定資料的範例提供者。",
		"advanced":      "進階",
		"apiKeyLabel":   "API 金鑰",
		"apiKeyHelp":    `請填 "static-key"。`,
		"modeLabel":     "語言模式",
		"modeAuto":      "自動",
		"modeOriginal":  "原文",
		"attribution":   "資料來自靜態範例。",
		"required":      "必填。",
		"invalidKey":    "API 金鑰無效。",
		"unknownOption": "未知的選項。",
		"movieName":     "靜態電影",
		"movieOverview": "一部永遠不變的電影。",
	},
	"zh-CN": {
		"desc":          "返回固定数据的示例提供者。",
		"advanced":      "高级",
		"apiKeyLabel":   "API 密钥",
		"apiKeyHelp":    `请填 "static-key"。`,
		"modeLabel":     "语言模式",
		"modeAuto":      "自动",
		"modeOriginal":  "原文",
		"attribution":   "数据来自静态示例。",
		"required":      "必填。",
		"invalidKey":    "API 密钥无效。",
		"unknownOption": "未知的选项。",
		"movieName":     "静态电影",
		"movieOverview": "一部永远不变的电影。",
	},
})

func (p *provider) Describe(_ context.Context, req *pluginv1.DescribeRequest) (*pluginv1.Manifest, error) {
	t := messages.For(req.GetLanguage())
	return &pluginv1.Manifest{
		Id:              "static",
		Name:            "Static",
		Description:     t.T("desc"),
		Version:         version,
		ProtocolVersion: pluginv1.ProtocolVersion,
		Capabilities: []string{
			pluginv1.CapabilityMetadataMovie,
			pluginv1.CapabilityMetadataPerson,
			pluginv1.CapabilityMetadataCollection,
			pluginv1.CapabilityMetadataPersonSearch,
			// Images, subtitles and user sync (services.go).
			pluginv1.CapabilityImagesMovie,
			pluginv1.CapabilitySubtitlesSearch,
			pluginv1.CapabilityUserSyncLink,
			pluginv1.CapabilityUserSyncScrobble,
			pluginv1.CapabilityUserSyncWatchedPull,
			pluginv1.CapabilityUserSyncWatchedPush,
		},
		ConfigGroups: []*pluginv1.ConfigGroup{
			{Key: "advanced", Title: t.T("advanced")},
		},
		ConfigFields: []*pluginv1.ConfigField{
			{
				Key: "api_key", Type: pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_SECRET, Required: true,
				Label:       t.T("apiKeyLabel"),
				Description: t.T("apiKeyHelp"),
				Link:        "https://example.com/static/keys",
			},
			{
				Key: "language_mode", Type: pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_SELECT, Group: "advanced",
				Label: t.T("modeLabel"), DefaultValue: "auto",
				Options: []*pluginv1.ConfigOption{
					{Value: "auto", Label: t.T("modeAuto")},
					{Value: "original", Label: t.T("modeOriginal")},
				},
			},
		},
		Attribution: &pluginv1.Attribution{
			Text: t.T("attribution"),
			Link: "https://example.com/static",
		},
		SuggestedConcurrency: 2,
	}, nil
}

func (p *provider) Configure(_ context.Context, req *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	resp := &pluginv1.ConfigureResponse{FieldErrors: map[string]string{}}
	t := messages.For(req.GetLanguage())
	switch key := req.GetValues()["api_key"]; key {
	case "":
		resp.FieldErrors["api_key"] = t.T("required")
	case validKey:
	default:
		resp.FieldErrors["api_key"] = t.T("invalidKey")
	}
	if m, ok := req.GetValues()["language_mode"]; ok && m != "auto" && m != "original" {
		resp.FieldErrors["language_mode"] = t.T("unknownOption")
	}
	if len(resp.FieldErrors) > 0 {
		return resp, nil
	}
	p.mu.Lock()
	p.configured = true
	p.mu.Unlock()
	return resp, nil
}

func (p *provider) ready() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.configured {
		return sdk.NeedsConfiguration("api_key is not configured")
	}
	return nil
}

func movieCandidate() *pluginv1.SearchCandidate {
	return &pluginv1.SearchCandidate{
		Id: movieID, Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "Static Movie", Year: 2001,
		ImageUrl: "https://example.com/static/poster.jpg", ProviderIds: map[string]string{"static": movieID}, Score: 1,
	}
}

func (p *provider) Search(_ context.Context, req *pluginv1.SearchRequest) (*pluginv1.SearchResponse, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	if req.GetName() == "rate-limit" {
		return nil, sdk.RateLimited(30*time.Second, "static: simulated rate limit")
	}
	if req.GetType() != pluginv1.ItemType_ITEM_TYPE_MOVIE {
		return &pluginv1.SearchResponse{}, nil
	}
	if req.GetProviderIds()["static"] == movieID || strings.Contains(strings.ToLower(req.GetName()), "static") {
		return &pluginv1.SearchResponse{Candidates: []*pluginv1.SearchCandidate{movieCandidate()}}, nil
	}
	return &pluginv1.SearchResponse{}, nil
}

func (p *provider) GetItem(_ context.Context, req *pluginv1.GetItemRequest) (*pluginv1.ItemMetadata, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	if req.GetType() != pluginv1.ItemType_ITEM_TYPE_MOVIE {
		return nil, sdk.InvalidArgument("static: only movies are supported")
	}
	if req.GetId() != movieID {
		return nil, sdk.NotFound("static: no such movie")
	}
	lang := req.GetLanguages().GetLanguage()
	return &pluginv1.ItemMetadata{
		Id: movieID, Type: pluginv1.ItemType_ITEM_TYPE_MOVIE,
		Name:           messages.For(lang).T("movieName"),
		OriginalTitle:  "Static Movie",
		Overview:       messages.For(lang).T("movieOverview"),
		ProductionYear: 2001, PremiereDate: "2001-02-03", RuntimeSeconds: 5400,
		CommunityRating: 7.5, OfficialRating: "PG",
		ProviderIds: map[string]string{"static": movieID, "imdb": "tt0000001"},
		Genres:      []string{"Drama"},
		Studios:     []string{"Static Studio"},
		Images: []*pluginv1.Image{
			{Type: pluginv1.ImageType_IMAGE_TYPE_PRIMARY, Url: "https://example.com/static/poster.jpg", Width: 1000, Height: 1500},
			{Type: pluginv1.ImageType_IMAGE_TYPE_BACKDROP, Url: "https://example.com/static/backdrop.jpg"},
		},
		Credits: []*pluginv1.Credit{
			{PersonId: personID, Name: "Static Actor", Kind: pluginv1.PersonKind_PERSON_KIND_ACTOR, Role: "Hero",
				ImageUrl: "https://example.com/static/actor.jpg", ProviderIds: map[string]string{"static": personID}},
			{Name: "Static Director", Kind: pluginv1.PersonKind_PERSON_KIND_DIRECTOR},
		},
		Collection: &pluginv1.CollectionRef{Id: collectionID, Name: "Static Collection"},
		// Optional: a catalogue number (movies and episodes only). The provider's own ID stays in provider_ids.
		ProductCode: "STA-001",
	}, nil
}

// SearchPerson demonstrates metadata.person.search: there is a candidate only when the name or an alias
// matches; confirmed is set only when the title hints include this provider's movie 1001, never on a name match alone.
func (p *provider) SearchPerson(_ context.Context, req *pluginv1.SearchPersonRequest) (*pluginv1.SearchPersonResponse, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	out := &pluginv1.SearchPersonResponse{}
	names := append([]string{req.GetName()}, req.GetAliases()...)
	if !slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, "Static Actor") || n == "靜態演員" }) {
		return out, nil
	}
	confirmed := slices.ContainsFunc(req.GetWorks(), func(w *pluginv1.PersonWork) bool { return w.GetProviderIds()["static"] == movieID })
	out.Candidates = append(out.Candidates, &pluginv1.PersonCandidate{
		Person:    &pluginv1.PersonMetadata{Id: personID, Name: "Static Actor", ProviderIds: map[string]string{"static": personID}},
		Confirmed: confirmed,
	})
	return out, nil
}

func (p *provider) GetPerson(_ context.Context, req *pluginv1.GetPersonRequest) (*pluginv1.PersonMetadata, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	if req.GetId() != personID {
		return nil, sdk.NotFound("static: no such person")
	}
	return &pluginv1.PersonMetadata{
		Id: personID, Name: "Static Actor", Aliases: []string{"靜態演員"},
		Overview: "Always plays the hero.", BirthDate: "1970-01-01", BirthPlace: "Nowhere",
		Gender:      pluginv1.Gender_GENDER_FEMALE,
		ProviderIds: map[string]string{"static": personID},
		Links:       []*pluginv1.Link{{Kind: "homepage", Url: "https://example.com/static/actor"}},
		Images:      []*pluginv1.Image{{Type: pluginv1.ImageType_IMAGE_TYPE_PRIMARY, Url: "https://example.com/static/actor.jpg"}},
	}, nil
}

func (p *provider) GetCollection(_ context.Context, req *pluginv1.GetCollectionRequest) (*pluginv1.CollectionMetadata, error) {
	if err := p.ready(); err != nil {
		return nil, err
	}
	if req.GetId() != collectionID {
		return nil, sdk.NotFound("static: no such collection")
	}
	return &pluginv1.CollectionMetadata{
		Id: collectionID, Name: "Static Collection", ProviderIds: map[string]string{"static": collectionID},
		Parts: []*pluginv1.SearchCandidate{movieCandidate()},
	}, nil
}
