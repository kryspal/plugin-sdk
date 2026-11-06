package plugintest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/kryspal/plugin-sdk/plugintest"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// The suite passes against the quickstart plugin (examples/minimal), built on the spot.
func TestRunAgainstTheMinimalExample(t *testing.T) {
	plugintest.RunSource(t, "../examples/minimal", plugintest.Options{
		Metadata: &plugintest.MetadataCase{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "Big Buck Bunny", Year: 2008},
	})
}

// libraryCases check the full plugin's tone setting on a metadata request and on a recognition request.
func libraryCases() []plugintest.LibraryCase {
	return []plugintest.LibraryCase{
		{
			Name: "tone in the item", Key: "tone", A: "loud", B: "quiet",
			Call: func(ctx context.Context, conn *grpc.ClientConn, lib *pluginv1.LibraryContext) (string, string, error) {
				item, err := pluginv1.NewMetadataProviderClient(conn).GetItem(ctx, &pluginv1.GetItemRequest{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Id: "m1", Library: lib})
				return item.GetAttributes()["tone"], "", err
			},
		},
		{
			Name: "tone in the parser version", Key: "tone", A: "loud", B: "quiet",
			Call: func(ctx context.Context, conn *grpc.ClientConn, lib *pluginv1.LibraryContext) (string, string, error) {
				resp, err := pluginv1.NewRecognizerClient(conn).Recognize(ctx, &pluginv1.RecognizeRequest{Path: []string{"SMP-001 Sample.mkv"}, Library: lib})
				return resp.GetCandidates()[0].GetIdentifiers()["product_code"], resp.GetParserVersion(), err
			},
		},
	}
}

func fullOptions() plugintest.Options {
	return plugintest.Options{
		LibraryCases:  libraryCases(),
		Metadata:      &plugintest.MetadataCase{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "Sample Movie", Year: 2020},
		PersonSearch:  &plugintest.PersonSearchCase{Name: "Sample Person"},
		Danmaku:       &plugintest.DanmakuCase{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Title: "Sample Movie", OriginalTitle: "Sample Movie Original", ProductCode: "SMP-001", Year: 2020, Match: "sample"},
		Subtitles:     &plugintest.SubtitleCase{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Title: "Sample Movie", Year: 2020},
		Recognize:     &plugintest.RecognizeCase{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Path: []string{"SMP-001 Sample.mkv"}},
		Ratings:       &plugintest.RatingsCase{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, ProviderIDs: map[string]string{"tmdb": "1"}},
		RunWorkAction: "ask",
		// GetItem fails unless the variable from Setup reaches the plugin.
		Setup: func(t *testing.T) map[string]string {
			srv := httptest.NewTLSServer(http.NotFoundHandler())
			t.Cleanup(srv.Close)
			return map[string]string{"FULL_UPSTREAM": srv.URL}
		},
		Env:      map[string]string{"FULL_BREAK": os.Getenv("PLUGINTEST_FULL_BREAK")},
		RunTasks: []string{"refresh", "clear_cache"},
	}
}

// The suite passes against a plugin with people, images and recognition, covering the checks for those capabilities.
func TestRunAgainstAFullPlugin(t *testing.T) {
	plugintest.RunSource(t, "./testdata/full", fullOptions())
}

// TestBrokenFullPlugin is run by TestTheSuiteCatchesBrokenAnswers in a child process, where it is expected to fail.
func TestBrokenFullPlugin(t *testing.T) {
	if os.Getenv("PLUGINTEST_FULL_BREAK") == "" {
		t.Skip("run by TestTheSuiteCatchesBrokenAnswers")
	}
	plugintest.RunSource(t, "./testdata/full", fullOptions())
}

func TestTheSuiteCatchesBrokenAnswers(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the test plugin once per case")
	}
	cases := map[string][]string{
		"person": {`want the person's own ID under the plugin ID, "full": "p1"`, `key "Height"`, "at most 256 are kept"},
		"keys": {
			`SearchCandidate 0: provider_ids = map[short:m1]`, `GetItem(m1): provider_ids = map[short:m1]; want the entry's own ID under the plugin ID, "full": "m1"; the ID is there under the key "short"`,
			`GetItem credit 0 (Sample Person)`, `SearchPerson candidate 0`,
		},
		"danmaku":     {"comments must be in increasing time order", "text is empty", "above 0xFFFFFF"},
		"match":       {"the host stores at most 2048", "title is empty"},
		"subtitles":   {"matched_by is not set", `format ".SRT"`, "score 1.5 is outside 0–1"},
		"autofill":    {`field "autofill_daily_limit": default "5000" is not an integer from 0 to 1000`},
		"images":      {"is not an absolute HTTPS URL", "IMAGE_TYPE_LOGO is not allowed here"},
		"recognize":   {"parser_version is empty"},
		"ratings":     {`rating source "imdb" is not in rating_sources [fullrate]`},
		"library":     {"keeps a library's values", "mixes up the settings"},
		"seedperm":    {"discover_seed_ids without the permission discover.seeds"},
		"seedids":     {"5 discover_seed_ids; the host keeps 4", `discover_seed_ids: "Bad Key" is not lowercase`},
		"seeds":       {"the filters or the sort depend on the seeds"},
		"seededfirst": {`the first tab "Home" is seeded`},
		"noseededtab": {"without a seeded tab after the first"},
		"refresh":     {`refreshable row "picked", batch 4294967295`, `refreshable row "popular", batch 4294967295`},
		"discover": {
			`key "a\nb" is not valid`, `row id "Bad Row" is not valid`, `poster_url "/relative.jpg" is not an absolute`,
			`type ITEM_TYPE_SEASON is not declared here`, `tab with ref "untitled" has no title`, `tab "Bad": ref "a\nb" is not valid`, `search type ITEM_TYPE_SEASON`,
			`GetDiscoverPage(ref "plugintest-no-such-ref") = 0 rows, <nil>`, `the header has no title`, `header image_url "/header.jpg"`, `fact "No value"=""`,
			`hero row "top": work "m1" has no backdrop_url`, `hero row "top" has a next_page_token`, `row "late" is a hero but not the first row`,
			`key "Bad Key" is not valid`, `key "genre" is repeated`, `is required; required does not apply`, `has disabled or note`, `sort "sort" allows several choices`,
			`card No ref: ref "" is not valid`, `card "Future" has image_kind 99`, `caption is longer than 64 characters`, `product_code is longer than 64 characters`, `\n1" has control characters; the host drops it`, `landscape row "wide": no work has a backdrop_url`, `7 badges; the host keeps 4`, `a badge has neither text nor rating`, `badge text "Far too long" is longer than 8 characters`, `badge "Far too long" color "red"`, `badge "O" outline "#12"`, `badge "C" has corner 99`, `more than 3 badges in corner`, `badge rating "Bad Source" 11 of 10`, `a card has no title`, `row "all" has cards and works`,
			`an empty text in genres`, `genres Bad: ref "a\nb" is not valid`, `21 companies`, `credit "No kind" has kind`, `a credit has no name`, `image_url "/relative.jpg"`,
			`external link "ftp://video.example.com/plain" is not an http or https URL`, `external link "https://video.example.com/nolabel" has no label`,
		},
		"actions": {
			`action "ghost" is not in work_actions`, "actions without include_actions", `status 99 is not in the fixed set`,
			`link "javascript:alert(1)" is not an absolute`, "status_text is longer than 32",
			`member work "" has no title`, `progress 150 is outside`, `updated_at "yesterday" is not RFC 3339`, `is newer than the one before it`,
		},
		"declaration": {`work action "bad" lists no required_provider_ids`, `provider ID key "TMDB" must be lowercase`},
		"manifest": {
			`visible_when refers to "names" of type`, `default contains "c", which is not an option`,
			`host "10.0.0.1" is a private network address, so the manifest must declare the permission network.lan`,
			`host "127.0.0.1" is not an acceptable IP address`, "interval_minutes 1 is outside", `attribute label for "Shoe-Size"`,
			`field "src_b_proxy": item "c" is not an item of its group`, `item "b" has no fields`,
		},
	}
	for mode, wants := range cases {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestBrokenFullPlugin$", "-test.v")
			cmd.Env = append(os.Environ(), "PLUGINTEST_FULL_BREAK="+mode)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("the suite passed a plugin that breaks %s:\n%s", mode, out)
			}
			for _, want := range wants {
				if !strings.Contains(string(out), want) {
					t.Errorf("output does not mention %q:\n%s", want, out)
				}
			}
		})
	}
}

// Start hands out a connection for checks the suite does not cover, with the test's variables in the environment.
func TestStart(t *testing.T) {
	opts := fullOptions()
	conn := plugintest.Start(t, plugintest.Build(t, "./testdata/full"), opts)
	item, err := pluginv1.NewMetadataProviderClient(conn).GetItem(t.Context(), &pluginv1.GetItemRequest{
		Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Id: "m1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(item.GetImages()[0].GetUrl(), "https://127.0.0.1:") {
		t.Errorf("image url %q does not come from the Setup upstream", item.GetImages()[0].GetUrl())
	}
	if item.GetOverview() != "" {
		t.Errorf("overview = %q; the host announces attributes by default, so the plugin should not fall back", item.GetOverview())
	}
}

// Setting KRYSPAL_HOST_FEATURES to "" makes the plugin see an older host.
func TestStartAsAnOlderHost(t *testing.T) {
	opts := fullOptions()
	opts.Env["KRYSPAL_HOST_FEATURES"] = ""
	conn := plugintest.Start(t, plugintest.Build(t, "./testdata/full"), opts)
	item, err := pluginv1.NewMetadataProviderClient(conn).GetItem(t.Context(), &pluginv1.GetItemRequest{
		Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Id: "m1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.GetOverview() == "" {
		t.Error("the plugin did not fall back for a host without attributes")
	}
}
