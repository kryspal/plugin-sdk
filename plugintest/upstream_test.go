package plugintest

import (
	"net/http"
	"strings"
	"testing"
)

func TestUpstreamRecordsAndScripts(t *testing.T) {
	up := NewUpstream(t)
	up.Handle("GET /a", JSON(200, map[string]int{"n": 1}))
	up.Handle("POST /b", Sequence(Status(400), RateLimited("9"), Status(204)))

	get := func(method, path, body string) *http.Response {
		req, _ := http.NewRequest(method, up.URL+path, strings.NewReader(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp
	}
	if r := get("GET", "/a?x=1", ""); r.StatusCode != http.StatusOK || r.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("GET /a: %d %v", r.StatusCode, r.Header)
	}
	for i, want := range []int{400, 429, 204, 204} {
		if r := get("POST", "/b", `{"k":`+string(rune('0'+i))+`}`); r.StatusCode != want {
			t.Errorf("POST /b #%d: %d, want %d", i, r.StatusCode, want)
		}
	}
	reqs := up.Requests()
	if len(reqs) != 5 || reqs[0].Path != "/a" || reqs[0].Query["x"][0] != "1" || string(reqs[2].Body) != `{"k":1}` {
		t.Fatalf("requests = %+v", reqs)
	}
}
