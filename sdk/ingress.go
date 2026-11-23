package sdk

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/kryspal/plugin-sdk"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// Network access providers (capabilities network.access and network.identity, see PROTOCOL.md): the plugin joins the
// server to another network, forwards that network's requests to the host's ingress socket, and reports its state.

// ErrIngressUnavailable reports that the host did not set up an ingress for this process (KRYSPAL_INGRESS_SOCKET or
// KRYSPAL_INGRESS_TOKEN is absent): the manifest does not declare network.access, or the host is older than the protocol
// addition.
var ErrIngressUnavailable = errors.New("sdk: the host offers no ingress to this plugin")

// ErrNetworkAccessNotDeclared: Host.ReportNetworkAccess was refused because the manifest does not declare network.access.
var ErrNetworkAccessNotDeclared = errors.New(pluginv1.CodeNetworkAccessNotDeclared)

// Ingress is the host's ingress for this plugin process.
type Ingress struct {
	socket string
	token  string
}

// IngressFromEnv reads the ingress socket and token. A nil getenv means os.Getenv (tests can inject one).
func IngressFromEnv(getenv func(string) string) (*Ingress, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	sock, token := getenv(pluginapi.EnvIngressSocket), getenv(pluginapi.EnvIngressToken)
	if sock == "" || token == "" {
		return nil, ErrIngressUnavailable
	}
	return &Ingress{socket: sock, token: token}, nil
}

// Transport returns an http.RoundTripper that dials the ingress socket for every request, whatever the request URL's
// host, and sets the ingress token header. Use it as the Transport of an httputil.ReverseProxy whose Rewrite calls
// SetPeer (and SetIdentity for network.identity); with FlushInterval -1 streams are passed through as they arrive.
//
// Use ReverseProxy.Rewrite, not Director: Go strips X-Forwarded-* and Forwarded before Rewrite, but not before Director,
// and SetPeer clears the rest; a provider that passes the client's headers on lets clients forge their address.
func (in *Ingress) Transport() http.RoundTripper {
	base := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", in.socket)
		},
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          64,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Header.Set(pluginv1.HeaderIngressToken, in.token)
		return base.RoundTrip(r)
	})
}

// IngressTransport is shorthand for IngressFromEnv(nil) followed by Transport.
func IngressTransport() (http.RoundTripper, error) {
	in, err := IngressFromEnv(nil)
	if err != nil {
		return nil, err
	}
	return in.Transport(), nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// NetworkIdentity is the identity a network proved for a connection (network.identity). Subject is the user's stable ID
// on that network and the only part bindings use; Login and DeviceName are for display only and may change.
type NetworkIdentity struct {
	Subject, Login, DeviceName string
}

// SetPeer clears every forwarding and identity header the client brought (X-Forwarded-*, Forwarded, X-Real-IP and every
// X-Kryspal-* header), then sets the peer address. Call it from ReverseProxy.Rewrite on the outgoing request, with the
// network connection's remote address; Rewrite itself drops X-Forwarded-* and Forwarded but not X-Real-IP.
func SetPeer(out *http.Request, peer string) {
	for k := range out.Header {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-kryspal-") || strings.HasPrefix(lk, "x-forwarded-") || lk == "forwarded" || lk == "x-real-ip" {
			delete(out.Header, k)
		}
	}
	out.Header.Set(pluginv1.HeaderPeer, peer)
}

// SetIdentity sets the identity headers, percent-encoded. Send no identity (do not call it) when the network cannot say
// who the peer is, when the peer is a device without a user, when its key has expired, or when the request shows it came
// through another proxy.
func SetIdentity(out *http.Request, id NetworkIdentity) {
	out.Header.Set(pluginv1.HeaderIdentitySubject, url.PathEscape(id.Subject))
	out.Header.Set(pluginv1.HeaderIdentityLogin, url.PathEscape(id.Login))
	out.Header.Set(pluginv1.HeaderIdentityDeviceName, url.PathEscape(id.DeviceName))
}

// NetworkAccessState is the state in a network access report.
type NetworkAccessState = pluginv1.NetworkAccessStatus_State

// NetworkAccessReport is what ReportNetworkAccess sends; see NetworkAccessStatus in host.proto for each field.
type NetworkAccessReport struct {
	State                  NetworkAccessState
	Origin                 string
	ActionURL, ActionLabel string
	Message, NetworkName   string
	ClientSteps            []string
	ClientAppURL, Language string
}

// ReportNetworkAccess reports the provider's state to the host and returns the server's interface language. Report on every
// state change and every 60 seconds. When the returned language differs from r.Language, report again in that language.
func (h *HostClient) ReportNetworkAccess(ctx context.Context, r NetworkAccessReport) (string, error) {
	resp, err := h.c.ReportNetworkAccess(ctx, &pluginv1.NetworkAccessStatus{
		State: r.State, Origin: r.Origin, ActionUrl: r.ActionURL, ActionLabel: r.ActionLabel, Message: r.Message,
		NetworkName: r.NetworkName, ClientSteps: r.ClientSteps, ClientAppUrl: r.ClientAppURL, Language: r.Language,
	})
	if err != nil {
		return "", hostError(err)
	}
	return resp.GetLanguage(), nil
}
