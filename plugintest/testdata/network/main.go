// Command network is a test network access provider (PROTOCOL.md §7.14) for the suite's own tests. On start it reports
// its state to the Host service and forwards one request of its own to the ingress, as if a device on its network had
// sent it. Its "network" is a plain HTTP listener on loopback, whose address it writes to the file in NETWORK_ADDR_FILE;
// every request to it is forwarded to the ingress as coming from one fixed device.
//
// NETWORK_BREAK picks what the suite must catch: network (bad reports and a bad request of its own), manifest
// (network.identity without network.access), proxy (the listener passes the client's headers on, as a Director-based
// proxy does), silent (the listener answers without forwarding) and identity (CheckIdentities leaves a subject out,
// answers one twice, one it was not asked and PRESENT for a subject it never sent).
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"github.com/kryspal/plugin-sdk"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
	"github.com/kryspal/plugin-sdk/sdk"
)

type provider struct {
	pluginv1.UnimplementedPluginServer
	pluginv1.UnimplementedNetworkIdentityServer
	broken string
}

func main() {
	p := &provider{broken: os.Getenv("NETWORK_BREAK")}
	if in, err := sdk.IngressFromEnv(nil); err == nil {
		go p.serve(in)
	}
	sdk.Main(sdk.Config{Plugin: p, NetworkIdentity: p})
}

func (p *provider) Describe(context.Context, *pluginv1.DescribeRequest) (*pluginv1.Manifest, error) {
	m := &pluginv1.Manifest{Id: "network", Name: "Network", Version: "0.1.0", ProtocolVersion: pluginv1.ProtocolVersion,
		Capabilities: []string{pluginv1.CapabilityNetworkAccess, pluginv1.CapabilityNetworkIdentity}}
	switch p.broken {
	case "network":
		m.Capabilities = []string{pluginv1.CapabilityNetworkAccess}
	case "manifest":
		m.Capabilities = []string{pluginv1.CapabilityNetworkIdentity}
	}
	return m, nil
}

func (*provider) Configure(context.Context, *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	return &pluginv1.ConfigureResponse{}, nil
}

// CheckIdentities answers from the fake network: its one user is present, nobody else is.
func (p *provider) CheckIdentities(_ context.Context, in *pluginv1.CheckIdentitiesRequest) (*pluginv1.CheckIdentitiesResponse, error) {
	out := &pluginv1.CheckIdentitiesResponse{}
	for _, s := range in.GetSubjects() {
		presence := pluginv1.Presence_PRESENCE_ABSENT
		if s == identity.Subject {
			presence = pluginv1.Presence_PRESENCE_PRESENT
		}
		if p.broken == "identity" {
			switch {
			case s == identity.Subject:
				continue // left out
			default:
				presence = pluginv1.Presence_PRESENCE_PRESENT // a subject it never sent
				out.Results = append(out.Results, &pluginv1.IdentityPresence{Subject: s, Presence: presence})
			}
		}
		out.Results = append(out.Results, &pluginv1.IdentityPresence{Subject: s, Presence: presence})
	}
	if p.broken == "identity" {
		out.Results = append(out.Results, &pluginv1.IdentityPresence{Subject: "someone-else", Presence: pluginv1.Presence_PRESENCE_ABSENT})
	}
	return out, nil
}

const origin = "https://media.example.test"

// target is where requests to the origin go: the ingress speaks plain HTTP, and the Host stays the origin's.
const target = "http://media.example.test/ping"

// serve reports the provider's state, forwards one request and starts the fake network.
func (p *provider) serve(in *sdk.Ingress) {
	defer p.listen(in)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host, err := sdk.Host(nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "host:", err)
		return
	}
	defer func() { _ = host.Close() }()
	reports := []sdk.NetworkAccessReport{
		{State: pluginv1.NetworkAccessStatus_STATE_CONNECTING, Message: "Connecting to the network.", Language: "en"},
		{State: pluginv1.NetworkAccessStatus_STATE_READY, Origin: origin, NetworkName: "Example network",
			ClientSteps:  []string{"Install the network's app.", "Sign in with your network account."},
			ClientAppURL: "https://example.com/download", Language: "en"},
	}
	if p.broken == "network" {
		reports = []sdk.NetworkAccessReport{
			{State: pluginv1.NetworkAccessStatus_STATE_UNSPECIFIED, Language: "en"},
			{State: pluginv1.NetworkAccessStatus_STATE_CONNECTING, Origin: "https://early.example.test", Language: "en"},
			{State: pluginv1.NetworkAccessStatus_STATE_READY, Origin: "https://100.64.0.1/media", Language: "en"},
			{State: pluginv1.NetworkAccessStatus_STATE_NEEDS_LOGIN, ActionURL: "javascript:alert(1)", Message: "Sign\x07in",
				ClientSteps: []string{"1", "2", "3", "4", "5", "6"}},
			{State: pluginv1.NetworkAccessStatus_STATE_READY, Origin: origin, Language: "en"},
		}
		// One call with the wrong host token.
		if bad, err := sdk.Host(func(k string) string {
			if k == pluginapi.EnvHostToken {
				return "wrong"
			}
			return os.Getenv(k)
		}); err == nil {
			_, _ = bad.ReportNetworkAccess(ctx, reports[len(reports)-1])
			_ = bad.Close()
		}
	}
	for _, r := range reports {
		if _, err := host.ReportNetworkAccess(ctx, r); err != nil && p.broken != "network" {
			fmt.Fprintln(os.Stderr, "report:", err)
		}
	}

	if p.broken == "silent" {
		return // nothing reaches the ingress, not even a request of its own
	}
	client := &http.Client{Transport: in.Transport()}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	sdk.SetPeer(req, peer)
	sdk.SetIdentity(req, identity)
	if p.broken == "network" {
		// What a provider that skipped SetPeer and passed the client's headers on would send.
		req.Header = http.Header{}
		req.Header.Set(pluginv1.HeaderPeer, "127.0.0.1:5000")
		req.Header.Set(pluginv1.HeaderIdentitySubject, "u1")
		req.Header.Set("X-Forwarded-For", "203.0.113.9")
		req.Header.Set("X-Kryspal-User", "admin")
		sendRaw(ctx, os.Getenv(pluginapi.EnvIngressSocket))
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ingress:", err)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if p.broken != "network" && resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "ingress answered", resp.Status)
	}
}

// sendRaw sends one request to the ingress with the wrong token.
func sendRaw(ctx context.Context, sock string) {
	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	req.Header.Set(pluginv1.HeaderIngressToken, "wrong")
	req.Header.Set(pluginv1.HeaderPeer, "100.64.0.7:41641")
	if resp, err := c.Do(req); err == nil {
		_ = resp.Body.Close()
	}
}

// The fake network's one device and its user.
const peer = "100.64.0.7:41641"

var identity = sdk.NetworkIdentity{Subject: "u1", Login: "alex@example.com", DeviceName: "Alex's phone"}

// listen starts the fake network when NETWORK_ADDR_FILE is set and writes its address there.
func (p *provider) listen(in *sdk.Ingress) {
	file := os.Getenv("NETWORK_ADDR_FILE")
	if file == "" {
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		return
	}
	to, _ := url.Parse(target)
	var h http.Handler = &httputil.ReverseProxy{Transport: in.Transport(), FlushInterval: -1,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(&url.URL{Scheme: to.Scheme, Host: to.Host})
			sdk.SetPeer(r.Out, peer)
			sdk.SetIdentity(r.Out, identity)
		}}
	switch p.broken {
	case "proxy":
		h = &httputil.ReverseProxy{Transport: in.Transport(), Director: func(r *http.Request) {
			r.URL.Scheme, r.URL.Host, r.Host = to.Scheme, to.Host, to.Host
		}}
	case "silent":
		h = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	}
	go func() { _ = (&http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}).Serve(ln) }()
	if err := os.WriteFile(file, []byte(ln.Addr().String()), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "address file:", err)
	}
}
