package plugintest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/kryspal/plugin-sdk"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// Network access providers (PROTOCOL.md §7.14). The host gives a plugin that declares network.access an ingress socket
// and a Host service it reports its state to. The suite starts the plugin once more with both, after the main run has
// shut the first process down (the host never runs two copies of a plugin), and checks what the plugin sends.

const (
	// NetworkReportTimeout is how long the suite waits for the first ReportNetworkAccess after Configure.
	NetworkReportTimeout = 15 * time.Second
	// networkSettle is how long the suite keeps listening after the first report, for the reports and requests that
	// follow it.
	networkSettle = time.Second
	// networkLanguage is the server language the fake Host service answers with.
	networkLanguage = "en"
)

// networkRun records what a network access provider sent. The gRPC and HTTP handlers only record; the test goroutine
// checks the records once both servers are stopped.
type networkRun struct {
	hostToken, ingressToken string

	mu        sync.Mutex
	reports   []*pluginv1.NetworkAccessStatus
	badTokens int // Host calls with a missing or wrong host token
	requests  []ingressRequest
	readyHost string // host[:port] of the last valid READY origin
	first     chan struct{}
	firstOnce sync.Once
	ready     chan struct{} // closed on the first STATE_READY report with a valid origin
	readyOnce sync.Once
}

type ingressRequest struct {
	host    string
	header  http.Header
	tokenOK bool
}

func newNetworkRun(t *testing.T) *networkRun {
	return &networkRun{hostToken: randomToken(t), ingressToken: randomToken(t), first: make(chan struct{}), ready: make(chan struct{})}
}

func randomToken(t *testing.T) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// networkHost is the fake Host service: it checks the host token and records reports. It applies the host's field
// limits so the plugin sees the same INVALID_ARGUMENT a real host returns.
type networkHost struct {
	pluginv1.UnimplementedHostServer
	run *networkRun
}

func (h *networkHost) ReportNetworkAccess(ctx context.Context, s *pluginv1.NetworkAccessStatus) (*pluginv1.ReportNetworkAccessResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if v := md.Get(pluginv1.MetadataHostToken); len(v) != 1 || v[0] != h.run.hostToken {
		h.run.mu.Lock()
		h.run.badTokens++
		h.run.mu.Unlock()
		return nil, status.Error(codes.PermissionDenied, "host_token_invalid")
	}
	h.run.mu.Lock()
	h.run.reports = append(h.run.reports, proto.Clone(s).(*pluginv1.NetworkAccessStatus))
	ready := false
	if hp, ok := originHost(s.GetOrigin()); ok && s.GetState() == pluginv1.NetworkAccessStatus_STATE_READY {
		h.run.readyHost, ready = hp, true
	}
	h.run.mu.Unlock()
	h.run.firstOnce.Do(func() { close(h.run.first) })
	if ready {
		h.run.readyOnce.Do(func() { close(h.run.ready) })
	}
	if len(networkStatusProblems(s)) > 0 {
		return nil, status.Error(codes.InvalidArgument, "a field exceeds its limit")
	}
	return &pluginv1.ReportNetworkAccessResponse{Language: networkLanguage}, nil
}

// ServeHTTP is the fake ingress. It answers like the host (403, 503, 421, 400, otherwise 200) and records each request.
func (r *networkRun) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	tokenOK := req.Header.Get(pluginv1.HeaderIngressToken) == r.ingressToken
	r.mu.Lock()
	r.requests = append(r.requests, ingressRequest{host: req.Host, header: req.Header.Clone(), tokenOK: tokenOK})
	ready := r.readyHost
	r.mu.Unlock()
	switch {
	case !tokenOK:
		w.WriteHeader(http.StatusForbidden)
	case ready == "":
		w.WriteHeader(http.StatusServiceUnavailable)
	case normalizeHostPort(req.Host) != ready:
		w.WriteHeader(http.StatusMisdirectedRequest)
	case !validPeer(req.Header.Get(pluginv1.HeaderPeer)):
		w.WriteHeader(http.StatusBadRequest)
	default:
		_, _ = w.Write([]byte("ok"))
	}
}

// checkNetworkAccess starts the plugin with a Host service and an ingress, configures it, waits for its reports and
// checks them and every request that reached the ingress.
func checkNetworkAccess(t *testing.T, bin string, extra []string, m *pluginv1.Manifest, opts Options) {
	t.Helper()
	sockDir := socketDir(t)
	run := newNetworkRun(t)

	var lc net.ListenConfig
	hostSock, ingressSock := filepath.Join(sockDir, "host.sock"), filepath.Join(sockDir, "ingress.sock")
	hostLis, err := lc.Listen(t.Context(), pluginapi.NetworkUnix, hostSock)
	if err != nil {
		t.Fatal(err)
	}
	grpcSrv := grpc.NewServer()
	pluginv1.RegisterHostServer(grpcSrv, &networkHost{run: run})
	go func() { _ = grpcSrv.Serve(hostLis) }()
	ingressLis, err := lc.Listen(t.Context(), pluginapi.NetworkUnix, ingressSock)
	if err != nil {
		t.Fatal(err)
	}
	httpSrv := &http.Server{Handler: run, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = httpSrv.Serve(ingressLis) }()
	stopped := false
	stopServers := func() {
		if !stopped {
			stopped = true
			grpcSrv.Stop()
			_ = httpSrv.Close()
		}
	}
	t.Cleanup(stopServers)

	env := append(slices.Clone(extra),
		pluginapi.EnvHostSocket+"="+hostSock, pluginapi.EnvHostToken+"="+run.hostToken,
		pluginapi.EnvIngressSocket+"="+ingressSock, pluginapi.EnvIngressToken+"="+run.ingressToken)
	p := start(t, bin, sockDir, env)

	cctx, cancel := context.WithTimeout(t.Context(), configureTimeout)
	resp, err := pluginv1.NewPluginClient(p.conn).Configure(cctx, &pluginv1.ConfigureRequest{Values: opts.Config})
	cancel()
	if err != nil || resp.GetError() != "" || len(resp.GetFieldErrors()) > 0 {
		t.Errorf("Configure with an ingress: %v %q %v", err, resp.GetError(), resp.GetFieldErrors())
	}

	reported := false
	select {
	case <-run.first:
		reported = true
	case <-time.After(NetworkReportTimeout):
	}
	if opts.IngressTraffic != nil && reported {
		driveIngress(t, run, opts.IngressTraffic)
	}
	if reported {
		time.Sleep(networkSettle)
	}
	if reported && declares(m, pluginv1.CapabilityNetworkIdentity) {
		run.mu.Lock()
		sent := sentSubjects(run.requests)
		run.mu.Unlock()
		t.Run("CheckIdentities", func(t *testing.T) { checkIdentities(t.Context(), t, p.conn, sent) })
	}
	p.stop(t)
	stopServers()

	run.mu.Lock()
	defer run.mu.Unlock()
	if run.badTokens > 0 {
		t.Errorf("%d Host calls carried no or the wrong host token (%s)", run.badTokens, pluginapi.EnvHostToken)
	}
	if len(run.reports) == 0 {
		t.Errorf("no ReportNetworkAccess within %v of starting; report on every state change and every 60 seconds (PROTOCOL.md §7.14)", NetworkReportTimeout)
	}
	for i, s := range run.reports {
		for _, problem := range networkStatusProblems(s) {
			t.Errorf("report %d: %s", i, problem)
		}
		for _, problem := range networkStateProblems(s) {
			t.Errorf("report %d: %s", i, problem)
		}
	}
	if len(run.requests) == 0 && opts.IngressTraffic == nil {
		t.Log("no request reached the ingress, so the token, peer and identity checks did not run; set Options.IngressTraffic to send requests through your fake network")
	}
	identity := declares(m, pluginv1.CapabilityNetworkIdentity)
	for i, r := range run.requests {
		for _, problem := range ingressRequestProblems(r, identity) {
			t.Errorf("ingress request %d (Host %q): %s", i, r.host, problem)
		}
	}
}

// forgedPeer and forgedValue mark the headers ForgedClientHeaders returns. They are valid in form, so only these values
// show that a request carried a client's headers on to the ingress.
const (
	forgedPeer  = "192.0.2.66:6666"
	forgedAddr  = "192.0.2.66"
	forgedValue = "plugintest-forged"
)

// ForgedClientHeaders returns headers a client on the provider's network might send to pass for another address or
// another user. Send them in one request from Options.IngressTraffic: the suite fails if any of these values reaches
// the ingress.
func ForgedClientHeaders() http.Header {
	h := http.Header{}
	h.Set("X-Forwarded-For", forgedAddr)
	h.Set("X-Forwarded-Host", forgedValue+".invalid")
	h.Set("Forwarded", "for="+forgedAddr)
	h.Set("X-Real-Ip", forgedAddr)
	h.Set(pluginv1.HeaderPeer, forgedPeer)
	h.Set(pluginv1.HeaderIdentitySubject, forgedValue)
	h.Set(pluginv1.HeaderIdentityLogin, forgedValue)
	h.Set(pluginv1.HeaderIdentityDeviceName, forgedValue)
	return h
}

// driveIngress waits for the plugin to report STATE_READY, then calls IngressTraffic, which must make the plugin forward
// at least one request.
func driveIngress(t *testing.T, run *networkRun, traffic func(testing.TB)) {
	t.Helper()
	select {
	case <-run.ready:
	case <-time.After(NetworkReportTimeout):
		t.Errorf("no STATE_READY report with a valid origin within %v, so Options.IngressTraffic was not called", NetworkReportTimeout)
		return
	}
	count := func() int {
		run.mu.Lock()
		defer run.mu.Unlock()
		return len(run.requests)
	}
	before := count()
	traffic(t)
	if count() == before {
		t.Error("Options.IngressTraffic returned and no request reached the ingress; forward your network's requests to " + pluginapi.EnvIngressSocket)
	}
}

// networkStatusProblems are the field limits the host enforces with INVALID_ARGUMENT.
func networkStatusProblems(s *pluginv1.NetworkAccessStatus) []string {
	var out []string
	text := func(name, v string, limit int) {
		if n := len([]rune(v)); n > limit {
			out = append(out, name+" is longer than "+strconv.Itoa(limit)+" characters")
		}
		if strings.ContainsFunc(strings.ReplaceAll(v, "\n", ""), unicode.IsControl) {
			out = append(out, name+" "+quote(v)+" has control characters")
		}
	}
	link := func(name, v string) {
		if v == "" {
			return
		}
		text(name, v, pluginv1.MaxNetworkURL)
		if !absoluteHTTP(v) {
			out = append(out, name+" "+quote(v)+" is not an absolute http or https URL")
		}
	}
	if st := s.GetState(); st < pluginv1.NetworkAccessStatus_STATE_UNSPECIFIED || st > pluginv1.NetworkAccessStatus_STATE_STOPPED {
		out = append(out, "state "+strconv.Itoa(int(st))+" is not a NetworkAccessStatus.State")
	}
	text("message", s.GetMessage(), pluginv1.MaxNetworkMessage)
	text("action_label", s.GetActionLabel(), pluginv1.MaxNetworkLabel)
	text("network_name", s.GetNetworkName(), pluginv1.MaxNetworkLabel)
	text("language", s.GetLanguage(), pluginv1.MaxNetworkLanguage)
	link("action_url", s.GetActionUrl())
	link("client_app_url", s.GetClientAppUrl())
	if n := len(s.GetClientSteps()); n > pluginv1.MaxNetworkSteps {
		out = append(out, strconv.Itoa(n)+" client_steps; at most "+strconv.Itoa(pluginv1.MaxNetworkSteps))
	}
	for _, step := range s.GetClientSteps() {
		text("client step", step, pluginv1.MaxNetworkStep)
	}
	return out
}

// networkStateProblems are what host.proto and PROTOCOL.md §7.14 ask of the state and the origin. The host does not
// reject these reports, but it cannot use them as meant.
func networkStateProblems(s *pluginv1.NetworkAccessStatus) []string {
	var out []string
	st := s.GetState()
	switch {
	case st == pluginv1.NetworkAccessStatus_STATE_UNSPECIFIED:
		out = append(out, "state is STATE_UNSPECIFIED; report the provider's actual state")
	case st == pluginv1.NetworkAccessStatus_STATE_READY && s.GetOrigin() == "":
		out = append(out, "state is STATE_READY without an origin")
	case st != pluginv1.NetworkAccessStatus_STATE_READY && s.GetOrigin() != "":
		out = append(out, "origin "+quote(s.GetOrigin())+" is set while the state is "+st.String()+"; set it only when STATE_READY (PROTOCOL.md §7.14)")
	}
	if o := s.GetOrigin(); o != "" {
		if _, ok := originHost(o); !ok {
			out = append(out, "origin "+quote(o)+" is not https with a DNS name and no path; the host ignores it (PROTOCOL.md §7.14)")
		}
	}
	hasText := s.GetMessage() != "" || s.GetActionLabel() != "" || len(s.GetClientSteps()) > 0
	if hasText && s.GetLanguage() == "" {
		out = append(out, "the report has texts but no language")
	}
	return out
}

// ingressRequestProblems checks one request the plugin forwarded to the ingress (PROTOCOL.md §7.14).
func ingressRequestProblems(r ingressRequest, identityDeclared bool) []string {
	var out []string
	for k, vs := range r.header {
		for _, v := range vs {
			if v == forgedPeer || strings.Contains(v, forgedAddr) || strings.Contains(v, forgedValue) {
				out = append(out, "carries the client's "+k+" "+quote(v)+" on (from ForgedClientHeaders); the plugin must drop what clients send")
			}
		}
	}
	if !r.tokenOK {
		out = append(out, "no or the wrong "+pluginv1.HeaderIngressToken+" (use the token in "+pluginapi.EnvIngressToken+")")
	}
	peer := r.header.Values(pluginv1.HeaderPeer)
	switch {
	case len(peer) != 1:
		out = append(out, strconv.Itoa(len(peer))+" "+pluginv1.HeaderPeer+" headers; send exactly one (sdk.SetPeer)")
	case !validPeer(peer[0]):
		out = append(out, pluginv1.HeaderPeer+" "+quote(peer[0])+" is not the address and port of a device on the network (loopback, unspecified and multicast addresses get 400)")
	}
	for _, h := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Forwarded", "X-Real-Ip"} {
		if r.header.Get(h) != "" {
			out = append(out, "forwards "+h+"; remove forwarding headers (use ReverseProxy.Rewrite, not Director)")
		}
	}
	identityHeaders := []string{pluginv1.HeaderIdentitySubject, pluginv1.HeaderIdentityLogin, pluginv1.HeaderIdentityDeviceName}
	known := append([]string{pluginv1.HeaderIngressToken, pluginv1.HeaderPeer}, identityHeaders...)
	for k := range r.header {
		if strings.HasPrefix(strings.ToLower(k), "x-kryspal-") && !slices.ContainsFunc(known, func(h string) bool { return strings.EqualFold(h, k) }) {
			out = append(out, "forwards "+k+"; a client's X-Kryspal-* headers must not pass (sdk.SetPeer removes them)")
		}
	}
	sent := 0
	for _, h := range identityHeaders {
		if len(r.header.Values(h)) > 0 {
			sent++
		}
	}
	switch {
	case sent > 0 && !identityDeclared:
		out = append(out, "sends identity headers without declaring network.identity")
	case sent > 0 && sent < len(identityHeaders):
		out = append(out, "sends only some identity headers; the host takes all three or none")
	case sent > 0:
		limits := map[string]int{pluginv1.HeaderIdentitySubject: pluginv1.MaxIdentitySubject,
			pluginv1.HeaderIdentityLogin: pluginv1.MaxIdentityLogin, pluginv1.HeaderIdentityDeviceName: pluginv1.MaxIdentityDeviceName}
		for _, h := range identityHeaders {
			vs := r.header.Values(h)
			v, err := url.PathUnescape(strings.Join(vs, ""))
			switch {
			case len(vs) != 1:
				out = append(out, h+" is repeated; the host ignores the identity")
			case err != nil:
				out = append(out, h+" "+quote(vs[0])+" does not percent-decode; the host ignores the identity")
			case len(v) > limits[h]:
				out = append(out, h+" is longer than "+strconv.Itoa(limits[h])+" bytes; the host ignores the identity")
			case h == pluginv1.HeaderIdentitySubject && v == "":
				out = append(out, h+" is empty; the host ignores the identity")
			}
		}
	}
	return out
}

// originHost returns the host[:port] of a valid origin: https, a DNS name (not an IP literal), no path, query, user
// or fragment. Port 443 is left out.
func originHost(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" ||
		(u.Path != "" && u.Path != "/") {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.Contains(host, ":") || strings.HasSuffix(host, ".") {
		return "", false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return "", false
	}
	for _, label := range strings.Split(host, ".") {
		if !dnsLabel(label) {
			return "", false
		}
	}
	if p := u.Port(); p != "" && p != "443" {
		return net.JoinHostPort(host, p), true
	}
	return host, true
}

func dnsLabel(l string) bool {
	if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}
	return !strings.ContainsFunc(l, func(c rune) bool { return (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' })
}

func normalizeHostPort(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ":443")
}

// validPeer reports whether peer is an address and port the host accepts as the remote address.
func validPeer(peer string) bool {
	ap, err := netip.ParseAddrPort(peer)
	if err != nil {
		return false
	}
	a := ap.Addr().Unmap()
	return !a.IsLoopback() && !a.IsUnspecified() && !a.IsMulticast()
}
