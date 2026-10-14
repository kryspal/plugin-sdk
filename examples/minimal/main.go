// Command minimal is the smallest useful plugin: a movie metadata provider with no configuration that knows one title.
// It is the program the quickstart in README.md walks through; copy it as the starting point of a new plugin.
package main

import (
	"context"

	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
	"github.com/kryspal/plugin-sdk/sdk"
)

type provider struct {
	pluginv1.UnimplementedPluginServer
	pluginv1.UnimplementedMetadataProviderServer
}

func main() {
	p := &provider{}
	sdk.Main(sdk.Config{Plugin: p, MetadataProvider: p})
}

// Describe returns the manifest. The id and version must match the plugin catalog entry.
func (*provider) Describe(context.Context, *pluginv1.DescribeRequest) (*pluginv1.Manifest, error) {
	return &pluginv1.Manifest{
		Id:              "minimal",
		Name:            "Minimal",
		Description:     "Knows exactly one movie.",
		Version:         "0.1.0",
		ProtocolVersion: pluginv1.ProtocolVersion,
		Capabilities:    []string{pluginv1.CapabilityMetadataMovie},
	}, nil
}

// Configure accepts the (empty) configuration.
func (*provider) Configure(context.Context, *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	return &pluginv1.ConfigureResponse{}, nil
}

// Search answers with the one movie when the name matches.
func (*provider) Search(_ context.Context, req *pluginv1.SearchRequest) (*pluginv1.SearchResponse, error) {
	if req.GetName() != "Big Buck Bunny" {
		return &pluginv1.SearchResponse{}, nil
	}
	return &pluginv1.SearchResponse{Candidates: []*pluginv1.SearchCandidate{
		{Id: "bbb", Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "Big Buck Bunny", Year: 2008},
	}}, nil
}

// GetItem returns the details of the movie Search found.
func (*provider) GetItem(_ context.Context, req *pluginv1.GetItemRequest) (*pluginv1.ItemMetadata, error) {
	if req.GetId() != "bbb" {
		return nil, sdk.NotFound("unknown id")
	}
	return &pluginv1.ItemMetadata{Id: "bbb", Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "Big Buck Bunny", ProductionYear: 2008}, nil
}
