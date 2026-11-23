package sdk_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/kryspal/plugin-sdk"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
	"github.com/kryspal/plugin-sdk/sdk"
)

func TestIngressUnavailableWithoutEnvironment(t *testing.T) {
	t.Parallel()
	for name, env := range map[string]map[string]string{
		"neither":   {},
		"no token":  {pluginapi.EnvIngressSocket: "/tmp/i.sock"},
		"no socket": {pluginapi.EnvIngressToken: "tok"},
	} {
		if _, err := sdk.IngressFromEnv(func(k string) string { return env[k] }); !errors.Is(err, sdk.ErrIngressUnavailable) {
			t.Errorf("%s: err = %v, want ErrIngressUnavailable", name, err)
		}
	}
}

// TestIngressReverseProxy forwards a client request through a ReverseProxy built the documented way and checks what the
// host's ingress socket receives: the token, the peer, the identity, and none of the client's own forwarding or
// X-Kryspal-* headers.
func TestIngressReverseProxy(t *testing.T) {
	t.Parallel()
	dir, err := os.MkdirTemp("", "kpi") // short path: Unix socket paths are limited to 107 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "ingress.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan http.Header, 1)
	host := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Clone()
		_, _ = io.WriteString(w, "ok")
	})}
	go func() { _ = host.Serve(lis) }()
	t.Cleanup(func() { _ = host.Close() })

	env := map[string]string{pluginapi.EnvIngressSocket: sock, pluginapi.EnvIngressToken: "ingress-secret"}
	in, err := sdk.IngressFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	target, _ := url.Parse("http://kryspal.invalid")
	proxy := &httputil.ReverseProxy{
		Transport:     in.Transport(),
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host
			sdk.SetPeer(pr.Out, "100.64.1.2:41000")
			sdk.SetIdentity(pr.Out, sdk.NetworkIdentity{Subject: "u1", Login: "alice@example.com", DeviceName: "Alice's phone"})
		},
	}
	front := httptest.NewServer(proxy)
	t.Cleanup(front.Close)

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, front.URL+"/web/", nil)
	req.Header.Set("X-Forwarded-For", "192.168.1.5")
	req.Header.Set("X-Real-IP", "192.168.1.5")
	req.Header.Set("X-Kryspal-Ingress-Token", "forged")
	req.Header.Set("X-Kryspal-Identity-Subject", "forged")
	req.Header.Set("X-Kryspal-Other", "forged")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	h := <-got
	if v := h.Get(pluginv1.HeaderIngressToken); v != "ingress-secret" {
		t.Errorf("token = %q, want the KRYSPAL_INGRESS_TOKEN value", v)
	}
	if v := h.Get(pluginv1.HeaderPeer); v != "100.64.1.2:41000" {
		t.Errorf("peer = %q", v)
	}
	if v := h.Get(pluginv1.HeaderIdentitySubject); v != "u1" {
		t.Errorf("subject = %q, want the provider's value, not the client's", v)
	}
	if v, _ := url.PathUnescape(h.Get(pluginv1.HeaderIdentityDeviceName)); v != "Alice's phone" {
		t.Errorf("device name = %q", v)
	}
	if v := h.Get("X-Kryspal-Other"); v != "" {
		t.Errorf("client X-Kryspal-Other reached the ingress: %q", v)
	}
	if v := h.Get("X-Real-IP"); v != "" {
		t.Errorf("client X-Real-IP reached the ingress: %q", v)
	}
	// Rewrite drops the inbound X-Forwarded-For; the proxy adds none unless SetXForwarded is called.
	if v := h.Get("X-Forwarded-For"); v == "192.168.1.5" || v == "192.168.1.5, 127.0.0.1" {
		t.Errorf("client X-Forwarded-For reached the ingress: %q", v)
	}
}

// SetPeer leaves only the peer of all the forwarding and identity headers a client can send, whatever their case.
func TestSetPeerClearsClientHeaders(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "http://kryspal.invalid/", nil)
	sent := []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Forwarded", "X-Real-IP", "x-real-ip",
		pluginv1.HeaderPeer, pluginv1.HeaderIdentitySubject, pluginv1.HeaderIdentityLogin, pluginv1.HeaderIdentityDeviceName, "X-Kryspal-Other"}
	for _, k := range sent {
		req.Header[k] = []string{"forged"} // raw keys: a client's header need not be in canonical form
	}
	req.Header.Set("Accept", "text/html")
	sdk.SetPeer(req, "100.64.1.2:41000")
	want := http.Header{"Accept": {"text/html"}, pluginv1.HeaderPeer: {"100.64.1.2:41000"}}
	if len(req.Header) != len(want) || req.Header.Get("Accept") != "text/html" || req.Header.Get(pluginv1.HeaderPeer) != "100.64.1.2:41000" {
		t.Errorf("headers after SetPeer = %v, want %v", req.Header, want)
	}
}

// fakeAccessHost answers ReportNetworkAccess with lang, or err.
type fakeAccessHost struct {
	fakeHost
	lang   string
	err    error
	status *pluginv1.NetworkAccessStatus
}

func (h *fakeAccessHost) ReportNetworkAccess(ctx context.Context, s *pluginv1.NetworkAccessStatus) (*pluginv1.ReportNetworkAccessResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if v := md.Get(pluginv1.MetadataHostToken); len(v) == 1 {
		h.token = v[0]
	}
	h.status = s
	if h.err != nil {
		return nil, h.err
	}
	return &pluginv1.ReportNetworkAccessResponse{Language: h.lang}, nil
}

func TestReportNetworkAccess(t *testing.T) {
	t.Parallel()
	h := &fakeAccessHost{lang: "zh-Hans"}
	c := serveAccessHost(t, h)
	lang, err := c.ReportNetworkAccess(t.Context(), sdk.NetworkAccessReport{
		State: pluginv1.NetworkAccessStatus_STATE_READY, Origin: "https://media.example.ts.net", Message: "Ready",
		ClientSteps: []string{"Install", "Sign in"}, Language: "en",
	})
	if err != nil {
		t.Fatal(err)
	}
	if lang != "zh-Hans" || h.token != "secret-token" {
		t.Errorf("lang = %q, token = %q", lang, h.token)
	}
	if s := h.status; s.GetState() != pluginv1.NetworkAccessStatus_STATE_READY || s.GetOrigin() != "https://media.example.ts.net" ||
		len(s.GetClientSteps()) != 2 || s.GetLanguage() != "en" {
		t.Errorf("status = %+v", s)
	}

	h2 := &fakeAccessHost{err: status.Error(codes.PermissionDenied, pluginv1.CodeNetworkAccessNotDeclared)}
	if _, err := serveAccessHost(t, h2).ReportNetworkAccess(t.Context(), sdk.NetworkAccessReport{}); !errors.Is(err, sdk.ErrNetworkAccessNotDeclared) {
		t.Errorf("err = %v, want ErrNetworkAccessNotDeclared", err)
	}
}

// serveAccessHost serves h on a Unix socket and returns a connected client.
func serveAccessHost(t *testing.T, h *fakeAccessHost) *sdk.HostClient {
	t.Helper()
	dir, err := os.MkdirTemp("", "kph")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "host.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	pluginv1.RegisterHostServer(srv, h)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	env := map[string]string{pluginapi.EnvHostSocket: sock, pluginapi.EnvHostToken: "secret-token"}
	c, err := sdk.Host(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
