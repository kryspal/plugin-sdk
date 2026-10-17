package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
	"github.com/kryspal/plugin-sdk/sdk"
)

// Demo implementations of the image, subtitle and user sync services: no network, fixed data, behavior driven by the
// input, so the host and e2e tests can cover every path of the protocol.

// images implements ImageProvider: when the item's provider IDs include static=1001 it returns two images (filtered by
// types).
type images struct {
	pluginv1.UnimplementedImageProviderServer
}

func (images) GetImages(_ context.Context, req *pluginv1.GetImagesRequest) (*pluginv1.GetImagesResponse, error) {
	if req.GetProviderIds()["static"] != movieID {
		return &pluginv1.GetImagesResponse{}, nil
	}
	all := []*pluginv1.Image{
		{Type: pluginv1.ImageType_IMAGE_TYPE_PRIMARY, Url: "https://example.com/static/poster-en.jpg", Language: "en", Width: 1000, Height: 1500, Likes: 12, SourceId: "p1"},
		{Type: pluginv1.ImageType_IMAGE_TYPE_LOGO, Url: "https://example.com/static/logo.png", Likes: 3, SourceId: "l1"},
	}
	if len(req.GetTypes()) == 0 {
		return &pluginv1.GetImagesResponse{Images: all}, nil
	}
	out := &pluginv1.GetImagesResponse{}
	for _, im := range all {
		for _, t := range req.GetTypes() {
			if im.GetType() == t {
				out.Images = append(out.Images, im)
			}
		}
	}
	return out, nil
}

// subtitles implements SubtitleProvider: when the title contains static it returns two candidates; the name rate-limit
// and the subtitle ID quota trigger rate limiting.
type subtitles struct {
	pluginv1.UnimplementedSubtitleProviderServer
}

func (subtitles) Search(_ context.Context, req *pluginv1.SearchSubtitlesRequest) (*pluginv1.SearchSubtitlesResponse, error) {
	title := strings.ToLower(req.GetTarget().GetTitle())
	if title == "rate-limit" {
		return nil, sdk.RateLimited(30*time.Second, "static: simulated rate limit")
	}
	if !strings.Contains(title, "static") {
		return &pluginv1.SearchSubtitlesResponse{}, nil
	}
	return &pluginv1.SearchSubtitlesResponse{Candidates: []*pluginv1.SubtitleCandidate{
		{Id: "sub-1", Language: "en", ReleaseName: "Static.Movie.2001.1080p", Format: "srt", MatchedBy: pluginv1.SubtitleCandidate_MATCHED_BY_IDS, DownloadCount: 100, Rating: 8, Score: 0.9},
		{Id: "quota", Language: "zh-TW", ReleaseName: "Static.Movie.2001", Format: "srt", MatchedBy: pluginv1.SubtitleCandidate_MATCHED_BY_NAME},
	}}, nil
}

func (subtitles) Download(_ context.Context, req *pluginv1.DownloadSubtitleRequest) (*pluginv1.DownloadSubtitleResponse, error) {
	switch req.GetId() {
	case "sub-1":
		return &pluginv1.DownloadSubtitleResponse{
			Files:          []*pluginv1.SubtitleFile{{Name: "Static.Movie.2001.en.srt", Language: "en", Data: []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n")}},
			QuotaRemaining: 7,
		}, nil
	case "quota":
		return nil, sdk.RateLimited(time.Hour, "static: download quota used up")
	}
	return nil, sdk.NotFound("static: no such subtitle")
}

// usersync implements UserSync: the credential is JSON {"access":…,"refresh":…}; access revoked yields UNAUTHENTICATED,
// limited rate-limits only this credential, and refresh old returns a new credential (demonstrating updated_credentials).
// Device code state "device-1" authorizes successfully; "pending" waits forever.
type usersync struct {
	pluginv1.UnimplementedUserSyncServer
}

type staticCreds struct {
	Access  string `json:"access"`
	Refresh string `json:"refresh"`
}

func parseCreds(c *pluginv1.Credentials) (staticCreds, error) {
	var out staticCreds
	if err := json.Unmarshal(c.GetData(), &out); err != nil || out.Access == "" {
		return out, sdk.Unauthenticated("static: no usable credentials")
	}
	if out.Access == "revoked" {
		return out, sdk.Unauthenticated("static: the token was revoked")
	}
	return out, nil
}

// refreshed returns a new credential when refresh is old (single-use refresh token rotation).
func refreshed(c staticCreds) *pluginv1.Credentials {
	if c.Refresh != "old" {
		return nil
	}
	data, _ := json.Marshal(staticCreds{Access: "a2", Refresh: "r2"})
	return &pluginv1.Credentials{Data: data}
}

func (usersync) BeginLink(context.Context, *pluginv1.BeginLinkRequest) (*pluginv1.LinkChallenge, error) {
	return &pluginv1.LinkChallenge{UserCode: "ABCD1234", VerificationUrl: "https://example.com/static/activate", ExpiresInSeconds: 600, IntervalSeconds: 5, State: []byte("device-1")}, nil
}

func (usersync) PollLink(_ context.Context, req *pluginv1.PollLinkRequest) (*pluginv1.LinkResult, error) {
	switch string(req.GetState()) {
	case "device-1":
		data, _ := json.Marshal(staticCreds{Access: "a1", Refresh: "old"})
		return &pluginv1.LinkResult{Status: pluginv1.LinkResult_STATUS_AUTHORIZED, Credentials: &pluginv1.Credentials{Data: data}, AccountLabel: "static-user"}, nil
	case "pending":
		return &pluginv1.LinkResult{Status: pluginv1.LinkResult_STATUS_PENDING}, nil
	case "denied":
		return &pluginv1.LinkResult{Status: pluginv1.LinkResult_STATUS_DENIED}, nil
	}
	return &pluginv1.LinkResult{Status: pluginv1.LinkResult_STATUS_EXPIRED}, nil
}

func (usersync) Unlink(context.Context, *pluginv1.UnlinkRequest) (*pluginv1.UnlinkResponse, error) {
	return &pluginv1.UnlinkResponse{}, nil
}

func (usersync) Scrobble(_ context.Context, req *pluginv1.ScrobbleRequest) (*pluginv1.ScrobbleResponse, error) {
	c, err := parseCreds(req.GetCredentials())
	if err != nil {
		return nil, err
	}
	if c.Access == "limited" {
		return nil, sdk.RateLimitedCredential(20*time.Second, "static: this user is rate limited")
	}
	return &pluginv1.ScrobbleResponse{UpdatedCredentials: refreshed(c)}, nil
}

func (usersync) PullWatched(_ context.Context, req *pluginv1.PullWatchedRequest) (*pluginv1.PullWatchedResponse, error) {
	c, err := parseCreds(req.GetCredentials())
	if err != nil {
		return nil, err
	}
	movie := func(id, at string, watched bool) *pluginv1.WatchedEntry {
		return &pluginv1.WatchedEntry{Media: &pluginv1.MediaRef{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Title: "Static Movie", Year: 2001,
			ProviderIds: map[string]string{"static": id}}, Watched: watched, WatchedAt: at, PlayCount: 1}
	}
	resp := &pluginv1.PullWatchedResponse{UpdatedCredentials: refreshed(c)}
	switch req.GetCursor() {
	case "":
		resp.Entries = []*pluginv1.WatchedEntry{movie("1001", "2026-01-02T03:04:05Z", true), movie("1002", "2026-01-03T03:04:05Z", false)}
		resp.NextCursor = "p2"
	case "p2":
		resp.Entries = []*pluginv1.WatchedEntry{movie("1003", "2026-02-01T00:00:00Z", true)}
	default:
		return nil, sdk.InvalidArgument("static: unknown cursor")
	}
	return resp, nil
}

func (usersync) PushWatched(_ context.Context, req *pluginv1.PushWatchedRequest) (*pluginv1.PushWatchedResponse, error) {
	c, err := parseCreds(req.GetCredentials())
	if err != nil {
		return nil, err
	}
	resp := &pluginv1.PushWatchedResponse{UpdatedCredentials: refreshed(c)}
	for i, e := range req.GetEntries() {
		if e.GetMedia().GetProviderIds()["static"] != movieID {
			resp.Unmatched = append(resp.Unmatched, int32(i))
		}
	}
	return resp, nil
}
