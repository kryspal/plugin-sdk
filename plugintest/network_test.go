package plugintest_test

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kryspal/plugin-sdk/plugintest"
)

// networkOptions drive the test provider's fake network: a loopback listener whose address the plugin writes to a file.
func networkOptions() plugintest.Options {
	var addrFile string
	return plugintest.Options{
		Env: map[string]string{"NETWORK_BREAK": os.Getenv("PLUGINTEST_NETWORK_BREAK")},
		Setup: func(t *testing.T) map[string]string {
			addrFile = filepath.Join(t.TempDir(), "addr")
			return map[string]string{"NETWORK_ADDR_FILE": addrFile}
		},
		IngressTraffic: func(t testing.TB) {
			var addr []byte
			for deadline := time.Now().Add(5 * time.Second); len(addr) == 0 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
				addr, _ = os.ReadFile(addrFile)
			}
			if len(addr) == 0 {
				t.Fatal("the plugin did not start its fake network")
			}
			plain := get(t, string(addr), nil)
			if plain != http.StatusOK {
				t.Errorf("a plain request through the fake network got %d, want 200", plain)
			}
			get(t, string(addr), plugintest.ForgedClientHeaders())
		},
	}
}

func get(t testing.TB, addr string, h http.Header) int {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, vs := range h {
		req.Header[k] = vs
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// The suite passes against a network access provider that reports its state and forwards a request properly.
func TestRunAgainstANetworkProvider(t *testing.T) {
	plugintest.RunSource(t, "./testdata/network", networkOptions())
}

// TestBrokenNetworkProvider is run by TestTheSuiteCatchesBrokenNetworkProviders in a child process, where it is expected
// to fail.
func TestBrokenNetworkProvider(t *testing.T) {
	if os.Getenv("PLUGINTEST_NETWORK_BREAK") == "" {
		t.Skip("run by TestTheSuiteCatchesBrokenNetworkProviders")
	}
	plugintest.RunSource(t, "./testdata/network", networkOptions())
}

func TestTheSuiteCatchesBrokenNetworkProviders(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the test plugin once per case")
	}
	cases := map[string][]string{
		"network": {
			"Host calls carried no or the wrong host token",
			"state is STATE_UNSPECIFIED",
			`origin "https://early.example.test" is set while the state is STATE_CONNECTING`,
			`origin "https://100.64.0.1/media" is not https with a DNS name`,
			`action_url "javascript:alert(1)" is not an absolute http or https URL`,
			"message \"Sign\ain\" has control characters",
			"6 client_steps; at most 5",
			"the report has texts but no language",
			"no or the wrong X-Kryspal-Ingress-Token",
			`X-Kryspal-Peer "127.0.0.1:5000" is not the address and port of a device`,
			"forwards X-Forwarded-For",
			"forwards X-Kryspal-User",
			"sends identity headers without declaring network.identity",
		},
		"manifest": {"declares network.identity without network.access"},
		"proxy": {
			`carries the client's X-Kryspal-Peer "192.0.2.66:6666" on`, `carries the client's X-Kryspal-Identity-Subject "plugintest-forged" on`,
			`carries the client's X-Real-Ip "192.0.2.66" on`, "forwards X-Forwarded-For",
		},
		"silent": {"Options.IngressTraffic returned and no request reached the ingress"},
		"identity": {
			`CheckIdentities left out "u1"`, `CheckIdentities answered "plugintest-nobody" more than once`,
			`CheckIdentities answered PRESENCE_PRESENT for "plugintest-nobody"`, `CheckIdentities answered "someone-else", which was not asked`,
			"CheckIdentities with no subjects answered 1 results, want none",
		},
	}
	// A working provider passes, and its request reached the ingress, so the request checks ran.
	t.Run("none", func(t *testing.T) {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestBrokenNetworkProvider$", "-test.v")
		cmd.Env = append(os.Environ(), "PLUGINTEST_NETWORK_BREAK=none")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("the suite failed a working provider: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "--- PASS: TestBrokenNetworkProvider/NetworkAccess/CheckIdentities") {
			t.Errorf("CheckIdentities did not run:\n%s", out)
		}
		for _, bad := range []string{"no request reached the ingress", "ingress:", "report:", "ingress answered"} {
			if strings.Contains(string(out), bad) {
				t.Errorf("output mentions %q:\n%s", bad, out)
			}
		}
	})
	for mode, wants := range cases {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestBrokenNetworkProvider$", "-test.v")
			cmd.Env = append(os.Environ(), "PLUGINTEST_NETWORK_BREAK="+mode)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("the suite passed a provider that breaks %s:\n%s", mode, out)
			}
			for _, want := range wants {
				if !strings.Contains(string(out), want) {
					t.Errorf("output does not mention %q:\n%s", want, out)
				}
			}
		})
	}
}
