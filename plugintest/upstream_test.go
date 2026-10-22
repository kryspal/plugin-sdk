package plugintest

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"os"
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

func TestTLSUpstreamIsTrustedThroughItsCertFile(t *testing.T) {
	up := NewTLSUpstream(t)
	up.Handle("GET /a", JSON(200, map[string]int{"n": 1}))
	if !strings.HasPrefix(up.URL, "https://") || up.Env()["SSL_CERT_FILE"] != up.CertFile {
		t.Fatalf("URL %q, env %v", up.URL, up.Env())
	}
	// A client that does not know the certificate refuses the service; one that reads the file, like a plugin with
	// SSL_CERT_FILE, accepts it.
	if _, err := http.Get(up.URL + "/a"); err == nil {
		t.Fatal("the self-signed certificate was trusted without the file")
	}
	pemBytes, err := os.ReadFile(up.CertFile)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		t.Fatal("the certificate file holds no certificate")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	resp, err := client.Get(up.URL + "/a")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(up.Requests()) != 1 {
		t.Errorf("status %d, requests %d", resp.StatusCode, len(up.Requests()))
	}
	if NewUpstream(t).Env() != nil {
		t.Error("a plain HTTP upstream has an environment")
	}
}
