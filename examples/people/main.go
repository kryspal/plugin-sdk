// Command people is an example of the person and recognition capabilities: GetPerson with a gallery (IMAGE_TYPE_PHOTO)
// and attributes, SearchPerson confirmed by works, GetImages for people, and a Recognizer that reads catalog codes such
// as "ABC-123" from file names. The data is a fixed table; a real plugin asks its upstream instead.
//
// It also shows two runtime helpers, pluginapi.DataDir for the plugin's own directory and pluginapi.HostFeatures to
// keep facts visible on hosts that do not store attributes yet, and three declarations: labels for its own attribute
// keys, a Referer for its image host, and a scheduled task.
package main

import (
	"context"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/kryspal/plugin-sdk"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
	"github.com/kryspal/plugin-sdk/sdk"
)

// person is one entry of the example's catalog.
type person struct {
	id, name   string
	aliases    []string
	works      []string // catalog codes of the person's titles
	attributes map[string]string
}

var catalog = []person{{
	id: "p1", name: "Sample Person", aliases: []string{"S. Person"}, works: []string{"ABC-123", "ABC-124"},
	attributes: map[string]string{"height_cm": "165", "debut_date": "2015-04-01", "agency": "Sample Agency", "favorite_food": "Noodles"},
}}

type plugin struct {
	pluginv1.UnimplementedPluginServer
	pluginv1.UnimplementedMetadataProviderServer
	pluginv1.UnimplementedImageProviderServer
	pluginv1.UnimplementedRecognizerServer
	pluginv1.UnimplementedTaskRunnerServer
	features map[string]bool
}

func main() {
	// Caches and downloaded files belong in the data directory; it survives upgrades.
	slog.Info("starting", slog.String("data_dir", pluginapi.DataDir(os.Getenv)))
	p := &plugin{features: pluginapi.HostFeatures(os.Getenv)}
	sdk.Main(sdk.Config{Plugin: p, MetadataProvider: p, ImageProvider: p, Recognizer: p, TaskRunner: p})
}

func (*plugin) Describe(_ context.Context, req *pluginv1.DescribeRequest) (*pluginv1.Manifest, error) {
	// Labels follow the requested language; this plugin translates one key into Simplified Chinese only.
	foodLabel := "Favorite food"
	if req.GetLanguage() == "zh-CN" {
		foodLabel = "喜欢的食物"
	}
	return &pluginv1.Manifest{
		Id: "people", Name: "People example", Description: "Person data, photos and catalog codes from a fixed table.",
		Version: "0.1.0", ProtocolVersion: pluginv1.ProtocolVersion,
		// Shown in the plugin center when the plugin is not installed from a catalog (for example in development mode).
		Author: "Example Author", Homepage: "https://example.com/people-plugin",
		Capabilities: []string{
			pluginv1.CapabilityMetadataPerson, pluginv1.CapabilityMetadataPersonSearch,
			pluginv1.CapabilityImagesPerson, pluginv1.CapabilityMediaRecognize, pluginv1.CapabilityPluginTasks,
		},
		// Suggested keys (height_cm, agency, ...) are translated by the host; label only your own keys.
		AttributeLabels: []*pluginv1.AttributeLabel{{Key: "favorite_food", Label: foodLabel}},
		// The image host refuses requests without a Referer from its own site.
		ImageFetchRules: []*pluginv1.ImageFetchRule{{Hosts: []string{"images.example.com"}, Referer: "https://www.example.com/"}},
		Tasks: []*pluginv1.TaskDeclaration{{
			Name: "refresh_catalog", Title: "Refresh the catalog", Description: "Downloads the latest person table.",
			DefaultSchedule: &pluginv1.TaskSchedule{IntervalMinutes: 12 * 60},
		}},
	}, nil
}

// RunTask runs a declared task. The host calls it on schedule or when an administrator starts it.
func (*plugin) RunTask(_ context.Context, req *pluginv1.RunTaskRequest) (*pluginv1.RunTaskResponse, error) {
	if req.GetName() != "refresh_catalog" {
		return nil, sdk.InvalidArgument("unknown task")
	}
	// A real plugin downloads into pluginapi.DataDir(os.Getenv) here and reports nothing_to_do when nothing changed.
	return &pluginv1.RunTaskResponse{Message: "The catalog has 1 person"}, nil
}

func (*plugin) Configure(context.Context, *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	return &pluginv1.ConfigureResponse{}, nil
}

func find(id string) (person, bool) {
	for _, p := range catalog {
		if p.id == id {
			return p, true
		}
	}
	return person{}, false
}

// GetPerson returns the person with the main photo (PRIMARY), a gallery (PHOTO) and attributes. provider_ids must
// contain the person's own ID: the host matches people across titles by it.
func (pl *plugin) GetPerson(_ context.Context, req *pluginv1.GetPersonRequest) (*pluginv1.PersonMetadata, error) {
	p, ok := find(req.GetId())
	if !ok {
		return nil, sdk.NotFound("unknown person")
	}
	out := &pluginv1.PersonMetadata{
		Id: p.id, Name: p.name, Aliases: p.aliases,
		ProviderIds: map[string]string{"people": p.id},
		Images:      personImages(p.id),
		Attributes:  p.attributes,
	}
	// A host without attributes drops them; put the facts where it still shows them.
	if !pl.features[pluginapi.HostFeatureAttributes] {
		out.Overview = "Height: " + p.attributes["height_cm"] + " cm. Agency: " + p.attributes["agency"] + "."
	}
	return out, nil
}

func personImages(id string) []*pluginv1.Image {
	base := "https://images.example.com/people/" + id
	return []*pluginv1.Image{
		{Type: pluginv1.ImageType_IMAGE_TYPE_PRIMARY, Url: base + "/main.jpg"},
		{Type: pluginv1.ImageType_IMAGE_TYPE_PHOTO, Url: base + "/1.jpg"},
		{Type: pluginv1.ImageType_IMAGE_TYPE_PHOTO, Url: base + "/2.jpg"},
	}
}

// SearchPerson matches by name or alias and sets confirmed only when one of the local works is the person's: a name
// alone is never enough.
func (*plugin) SearchPerson(_ context.Context, req *pluginv1.SearchPersonRequest) (*pluginv1.SearchPersonResponse, error) {
	resp := &pluginv1.SearchPersonResponse{}
	for _, p := range catalog {
		if !strings.EqualFold(p.name, req.GetName()) && !containsFold(p.aliases, req.GetName()) {
			continue
		}
		confirmed := false
		for _, w := range req.GetWorks() {
			if code := codeIn(w.GetName()); code != "" && containsFold(p.works, code) {
				confirmed = true
			}
		}
		resp.Candidates = append(resp.Candidates, &pluginv1.PersonCandidate{
			Person: &pluginv1.PersonMetadata{Id: p.id, Name: p.name}, Confirmed: confirmed,
		})
	}
	return resp, nil
}

// GetImages for a person finds them by provider ID, or by name when the server knows no ID for this provider.
func (*plugin) GetImages(_ context.Context, req *pluginv1.GetImagesRequest) (*pluginv1.GetImagesResponse, error) {
	if req.GetType() != pluginv1.ItemType_ITEM_TYPE_PERSON {
		return &pluginv1.GetImagesResponse{}, nil
	}
	for _, p := range catalog {
		if req.GetProviderIds()["people"] == p.id || strings.EqualFold(req.GetName(), p.name) {
			return &pluginv1.GetImagesResponse{Images: personImages(p.id)}, nil
		}
	}
	return &pluginv1.GetImagesResponse{}, nil
}

var codePattern = regexp.MustCompile(`(?i)\b([a-z]{2,6})-?(\d{2,5})\b`)

// codeIn returns the first catalog code in s, normalized to "ABC-123", or "".
func codeIn(s string) string {
	m := codePattern.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return strings.ToUpper(m[1]) + "-" + m[2]
}

// Recognize reads only the path: no file contents, no network, and the same input always gives the same answer.
func (*plugin) Recognize(_ context.Context, req *pluginv1.RecognizeRequest) (*pluginv1.RecognizeResponse, error) {
	resp := &pluginv1.RecognizeResponse{ParserVersion: "1"} // change it when the rules change
	path := req.GetPath()
	if len(path) == 0 {
		return resp, nil
	}
	if code := codeIn(path[len(path)-1]); code != "" {
		resp.Candidates = []*pluginv1.Recognition{{
			Type:        pluginv1.ItemType_ITEM_TYPE_MOVIE,
			Titles:      []*pluginv1.TitleHint{{Text: code, Language: "und", Weight: 1}},
			Identifiers: map[string]string{"product_code": code},
			Confidence:  0.9,
			Evidence:    []string{"code.filename"},
		}}
	}
	return resp, nil
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
