package plugintest_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/kryspal/plugin-sdk/plugintest"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// The suite passes against the quickstart plugin (examples/minimal), built on the spot.
func TestRunAgainstTheMinimalExample(t *testing.T) {
	plugintest.RunSource(t, "../examples/minimal", plugintest.Options{
		Metadata: &plugintest.MetadataCase{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "Big Buck Bunny", Year: 2008},
	})
}

func fullOptions() plugintest.Options {
	return plugintest.Options{
		Metadata:  &plugintest.MetadataCase{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Name: "Sample Movie", Year: 2020},
		Recognize: &plugintest.RecognizeCase{Type: pluginv1.ItemType_ITEM_TYPE_MOVIE, Path: []string{"SMP-001 Sample.mkv"}},
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
		"person":    {"want the person's own ID", `key "Height"`, "at most 256 are kept"},
		"images":    {"is not an absolute HTTPS URL", "IMAGE_TYPE_BACKDROP is not allowed here"},
		"recognize": {"parser_version is empty"},
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
