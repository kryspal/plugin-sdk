// Command full is a test plugin for plugintest: a movie provider with people, item and person images and name
// recognition. Its upstream is the address in FULL_UPSTREAM, which the test sets with Options.Setup; GetItem fails
// without it, so the test also proves that extra environment variables reach the plugin. FULL_BREAK makes one
// answer break the contract, so the test can see the suite catch it.
package main

import (
	"context"
	"os"
	"strings"
	"sync/atomic"

	"github.com/kryspal/plugin-sdk"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
	"github.com/kryspal/plugin-sdk/sdk"
)

type provider struct {
	pluginv1.UnimplementedPluginServer
	pluginv1.UnimplementedMetadataProviderServer
	pluginv1.UnimplementedImageProviderServer
	pluginv1.UnimplementedRecognizerServer
	pluginv1.UnimplementedTaskRunnerServer
	upstream string
	broken   string
	features map[string]bool
	settings *sdk.Settings
	// stuck is what the plugin keeps when FULL_BREAK=library: the last tone any request carried, which later requests then use.
	stuck atomic.Pointer[string]
}

// tone is the setting a library may override: it shows in the item's attributes and in the recognizer's parser_version.
func configFields() []*pluginv1.ConfigField {
	return []*pluginv1.ConfigField{{Key: "tone", Label: "Tone", Type: pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_SELECT, DefaultValue: "plain",
		LibraryOverride: true, Options: []*pluginv1.ConfigOption{{Value: "plain", Label: "Plain"}, {Value: "loud", Label: "Loud"}, {Value: "quiet", Label: "Quiet"}}}}
}

var tones = []string{"plain", "loud", "quiet"}

// toneOf is the tone for a request. With FULL_BREAK=library it is wrong in the way the suite must catch: a request's tone is kept for later requests.
func (p *provider) toneOf(lib *pluginv1.LibraryContext) string {
	t := p.settings.For(lib).Select("tone", tones, "plain")
	if p.broken == "library" {
		if lib.GetOverrides()["tone"] != "" {
			p.stuck.Store(&t)
		} else if k := p.stuck.Load(); k != nil {
			return *k
		}
	}
	return t
}

func main() {
	p := &provider{upstream: os.Getenv("FULL_UPSTREAM"), broken: os.Getenv("FULL_BREAK"), features: pluginapi.HostFeatures(os.Getenv),
		settings: sdk.NewSettings(configFields())}
	sdk.Main(sdk.Config{Plugin: p, MetadataProvider: p, ImageProvider: p, Recognizer: p, TaskRunner: p, SubtitleProvider: &subtitles{broken: p.broken}})
}

func (p *provider) Describe(context.Context, *pluginv1.DescribeRequest) (*pluginv1.Manifest, error) {
	m := &pluginv1.Manifest{
		Id: "full", Name: "Full", Version: "0.1.0", ProtocolVersion: pluginv1.ProtocolVersion,
		Capabilities: []string{
			pluginv1.CapabilityMetadataMovie, pluginv1.CapabilityMetadataPerson,
			pluginv1.CapabilityImagesMovie, pluginv1.CapabilityImagesPerson, pluginv1.CapabilityMediaRecognize,
			pluginv1.CapabilityPluginTasks, pluginv1.CapabilityMetadataPersonSearch, pluginv1.CapabilitySubtitlesSearch,
		},
		ConfigFields: append(configFields(), []*pluginv1.ConfigField{
			{Key: "use_db", Label: "Use a database", Type: pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_BOOL, DefaultValue: "false"},
			{Key: "dsn", Label: "Database address", Type: pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_STRING,
				VisibleWhen: &pluginv1.ConfigCondition{Key: "use_db", Values: []string{"true"}}},
			{Key: "sources", Label: "Sources", Type: pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_ORDERED_MULTISELECT,
				DefaultValue: `["b","a"]`, Options: []*pluginv1.ConfigOption{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}}},
			{Key: "names", Label: "Name map", Type: pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_TEXT},
			{Key: "mirrors", Label: "Mirrors", Type: pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_STRING_LIST, DefaultValue: "[]"},
			{Key: "src_a_proxy", Label: "Proxy", Type: pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_STRING, Group: "per_source", Item: "a"},
			{Key: "src_b_proxy", Label: "Proxy", Type: pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_STRING, Group: "per_source", Item: "b"},
			// Only gives the host's daily limit for automatic subtitle downloads a default: this source has no quota.
			{Key: "autofill_daily_limit", Label: "Limit", Type: pluginv1.ConfigFieldType_CONFIG_FIELD_TYPE_INT, DefaultValue: "0"},
		}...),
		ConfigGroups: []*pluginv1.ConfigGroup{{Key: "per_source", Title: "Each source", Items: []*pluginv1.ConfigItem{
			{Id: "a", Label: "Source A"}, {Id: "b", Label: "Source B"},
		}}},
		ImageFetchRules: []*pluginv1.ImageFetchRule{{Hosts: []string{"images.example.com"}, Referer: "https://www.example.com/"}},
		Tasks: []*pluginv1.TaskDeclaration{
			{Name: "refresh", Title: "Refresh the index", DefaultSchedule: &pluginv1.TaskSchedule{IntervalMinutes: 720}},
			{Name: "clear_cache", Title: "Clear the cache"},
		},
		AttributeLabels: []*pluginv1.AttributeLabel{{Key: "shoe_size", Label: "Shoe size"}},
	}
	if p.broken == "manifest" {
		m.ConfigFields[2].VisibleWhen.Key = "names"
		m.ConfigFields[3].DefaultValue = `["c"]`
		m.ImageFetchRules[0].Hosts = []string{"10.0.0.1", "127.0.0.1"}
		m.Tasks[0].DefaultSchedule = &pluginv1.TaskSchedule{IntervalMinutes: 1}
		m.AttributeLabels[0].Key = "Shoe-Size"
		m.ConfigFields[7].Item = "c"
	}
	if p.broken == "autofill" {
		m.ConfigFields[8].DefaultValue = "5000"
	}
	return m, nil
}

// RunTask answers every declared task; refresh reports what it did.
func (*provider) RunTask(_ context.Context, req *pluginv1.RunTaskRequest) (*pluginv1.RunTaskResponse, error) {
	switch req.GetName() {
	case "refresh":
		return &pluginv1.RunTaskResponse{Message: "Refreshed 3 entries"}, nil
	case "clear_cache":
		return &pluginv1.RunTaskResponse{NothingToDo: true}, nil
	}
	return nil, sdk.InvalidArgument("unknown task")
}

// Configure stores the global values; a dry run only checks and stores nothing.
func (p *provider) Configure(_ context.Context, req *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	if !req.GetDryRun() {
		p.settings.Store(req.GetValues())
	}
	return &pluginv1.ConfigureResponse{}, nil
}

func (p *provider) Search(_ context.Context, req *pluginv1.SearchRequest) (*pluginv1.SearchResponse, error) {
	if req.GetName() != "Sample Movie" {
		return &pluginv1.SearchResponse{}, nil
	}
	c := &pluginv1.SearchCandidate{Id: "m1", Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "Sample Movie", Year: 2020}
	if p.broken == "keys" {
		c.ProviderIds = map[string]string{"short": "m1"}
	}
	return &pluginv1.SearchResponse{Candidates: []*pluginv1.SearchCandidate{c}}, nil
}

func (p *provider) GetItem(_ context.Context, req *pluginv1.GetItemRequest) (*pluginv1.ItemMetadata, error) {
	if p.upstream == "" {
		return nil, sdk.NeedsConfiguration("FULL_UPSTREAM is not set")
	}
	if req.GetId() != "m1" {
		return nil, sdk.NotFound("unknown id")
	}
	item := &pluginv1.ItemMetadata{
		Id: "m1", Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "Sample Movie", ProductionYear: 2020,
		ProviderIds: map[string]string{"full": "m1"},
		Images:      []*pluginv1.Image{{Type: pluginv1.ImageType_IMAGE_TYPE_PRIMARY, Url: p.upstream + "/m1.jpg"}},
		Credits: []*pluginv1.Credit{{
			PersonId: "p1", Name: "Sample Person", Kind: pluginv1.PersonKind_PERSON_KIND_ACTOR,
			ProviderIds: map[string]string{"full": "p1"},
		}},
		Attributes: map[string]string{"label": "Sample Label", "series": "Sample Series", "tone": p.toneOf(req.GetLibrary())},
	}
	// A host that drops attributes still gets the facts, in the overview.
	if !p.features[pluginapi.HostFeatureAttributes] {
		item.Overview = "Label: Sample Label. Series: Sample Series."
	}
	if p.broken == "keys" {
		// The right IDs under a key other than the plugin ID: the host would not find them.
		item.ProviderIds = map[string]string{"short": "m1"}
		item.Credits[0].ProviderIds = map[string]string{"short": "p1"}
	}
	return item, nil
}

// SearchPerson finds the one person the plugin knows.
func (p *provider) SearchPerson(_ context.Context, req *pluginv1.SearchPersonRequest) (*pluginv1.SearchPersonResponse, error) {
	if req.GetName() != "Sample Person" {
		return &pluginv1.SearchPersonResponse{}, nil
	}
	ids := map[string]string{"full": "p1"}
	if p.broken == "keys" {
		ids = map[string]string{"short": "p1"}
	}
	return &pluginv1.SearchPersonResponse{Candidates: []*pluginv1.PersonCandidate{
		{Person: &pluginv1.PersonMetadata{Id: "p1", Name: "Sample Person", ProviderIds: ids}},
	}}, nil
}

// subtitles is a subtitle source; it has its own type because both services have a method called Search.
type subtitles struct {
	pluginv1.UnimplementedSubtitleProviderServer
	broken string
}

func (s *subtitles) Search(_ context.Context, req *pluginv1.SearchSubtitlesRequest) (*pluginv1.SearchSubtitlesResponse, error) {
	if req.GetTarget().GetTitle() != "Sample Movie" {
		return &pluginv1.SearchSubtitlesResponse{}, nil
	}
	c := &pluginv1.SubtitleCandidate{
		Id: "s1", Language: "en", ReleaseName: "Sample.Movie.2020.1080p", Format: "srt",
		MatchedBy: pluginv1.SubtitleCandidate_MATCHED_BY_NAME, Score: 0.8, Rating: 7,
	}
	if s.broken == "subtitles" {
		c.MatchedBy, c.Format, c.Score = pluginv1.SubtitleCandidate_MATCHED_BY_UNSPECIFIED, ".SRT", 1.5
	}
	return &pluginv1.SearchSubtitlesResponse{Candidates: []*pluginv1.SubtitleCandidate{c}}, nil
}

func (p *provider) GetPerson(_ context.Context, req *pluginv1.GetPersonRequest) (*pluginv1.PersonMetadata, error) {
	if req.GetId() != "p1" {
		return nil, sdk.NotFound("unknown id")
	}
	person := &pluginv1.PersonMetadata{
		Id: "p1", Name: "Sample Person", ProviderIds: map[string]string{"full": "p1"},
		Images: []*pluginv1.Image{
			{Type: pluginv1.ImageType_IMAGE_TYPE_PRIMARY, Url: "https://images.example.com/p1.jpg"},
			{Type: pluginv1.ImageType_IMAGE_TYPE_PHOTO, Url: "https://images.example.com/p1-a.jpg"},
		},
		Attributes: map[string]string{"height_cm": "170"},
	}
	// A host that keeps person backdrops gets one; older hosts would take it for a photo.
	if p.features[pluginapi.HostFeaturePersonBackdrop] {
		person.Images = append(person.Images, &pluginv1.Image{Type: pluginv1.ImageType_IMAGE_TYPE_BACKDROP, Url: "https://images.example.com/p1-wide.jpg"})
	}
	if p.broken == "person" {
		// The right ID under a key other than the plugin ID: the host would not find it.
		person.ProviderIds = map[string]string{"short": "p1"}
		person.Attributes["Height"] = strings.Repeat("x", 300)
	}
	return person, nil
}

func (p *provider) GetImages(_ context.Context, req *pluginv1.GetImagesRequest) (*pluginv1.GetImagesResponse, error) {
	if req.GetType() == pluginv1.ItemType_ITEM_TYPE_PERSON {
		img := &pluginv1.Image{Type: pluginv1.ImageType_IMAGE_TYPE_PHOTO, Url: "https://images.example.com/p1-b.jpg"}
		if p.broken == "images" {
			img = &pluginv1.Image{Type: pluginv1.ImageType_IMAGE_TYPE_LOGO, Url: "http://images.example.com/p1-b.jpg"}
		}
		return &pluginv1.GetImagesResponse{Images: []*pluginv1.Image{img}}, nil
	}
	return &pluginv1.GetImagesResponse{Images: []*pluginv1.Image{
		{Type: pluginv1.ImageType_IMAGE_TYPE_BACKDROP, Url: "https://images.example.com/m1-backdrop.jpg"},
	}}, nil
}

// Recognize reads a code like "SMP-001" from the last path segment.
func (p *provider) Recognize(_ context.Context, req *pluginv1.RecognizeRequest) (*pluginv1.RecognizeResponse, error) {
	resp := &pluginv1.RecognizeResponse{ParserVersion: "1"}
	if tone := p.toneOf(req.GetLibrary()); tone != "plain" {
		resp.ParserVersion += "+tone=" + tone
	}
	if p.broken == "recognize" {
		resp.ParserVersion = ""
	}
	path := req.GetPath()
	if len(path) == 0 {
		return resp, nil
	}
	name := path[len(path)-1]
	if code, _, ok := strings.Cut(name, " "); ok && strings.HasPrefix(code, "SMP-") {
		resp.Candidates = []*pluginv1.Recognition{{
			Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Identifiers: map[string]string{"product_code": code}, Confidence: 0.9,
		}}
	}
	return resp, nil
}
