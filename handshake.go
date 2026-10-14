// Package pluginapi defines the constants and handshake line format of the Kryspal Media plugin handshake, shared by the
// host and the SDK.
//
// See PROTOCOL.md for the full specification. The gRPC services are defined in the subpackage v1 (pluginv1); Go plugin
// authors use the subpackage sdk. This package depends only on the standard library.
package pluginapi

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Environment variables for the handshake. The host starts a plugin with a clean environment, passing only these three
// variables plus PATH, TMPDIR and HOME, and SSL_CERT_FILE and SSL_CERT_DIR when the host has them set.
const (
	// EnvCookie must equal CookieValue; otherwise the plugin should refuse to start, because it was not started by the host.
	EnvCookie = "KRYSPAL_PLUGIN_COOKIE"
	// EnvProtocols holds the protocol major versions the host supports, as comma-separated decimal numbers, e.g. "1" or "1,2".
	EnvProtocols = "KRYSPAL_PLUGIN_PROTOCOLS"
	// EnvSocketDir is the 0700 directory the host created for this plugin; the plugin creates its Unix socket in it.
	EnvSocketDir = "KRYSPAL_PLUGIN_SOCKET_DIR"
	// EnvHostSocket and EnvHostToken are the Unix socket path and one-time token of the service the host offers to plugins
	// (Host, change 0103). They differ on every process start; older hosts do not set them, in which case the SDK's Host()
	// returns ErrHostUnavailable and the plugin should degrade gracefully.
	EnvHostSocket = "KRYSPAL_HOST_SOCKET"
	EnvHostToken  = "KRYSPAL_HOST_TOKEN"
)

// CookieValue is the fixed magic value of EnvCookie. It is not a secret; it only keeps users from running a plugin directly.
const CookieValue = "7f3a9c52-kryspal-plugin"

// Network types in the handshake line.
const (
	NetworkUnix = "unix"
	// NetworkTCP is reserved for future plugins that run over the network; the first host version does not accept it.
	NetworkTCP = "tcp"
)

// MaxHandshakeLine is the maximum length in bytes of the handshake line (excluding the newline). The host treats a longer
// line as a failed handshake.
const MaxHandshakeLine = 1024

// SocketName is the socket file name the SDK creates in EnvSocketDir. Plugins in other languages may choose their own
// name, but the full path must not exceed the Unix socket limit (107 bytes on Linux).
const SocketName = "plugin.sock"

// ErrHandshake reports a malformed handshake line or handshake environment variable.
var ErrHandshake = errors.New("plugin handshake")

// Handshake is the handshake line a plugin prints on stdout: <major version>|<network>|<address>.
type Handshake struct {
	Protocol int    // protocol major version chosen by the plugin
	Network  string // NetworkUnix or NetworkTCP
	Address  string // absolute socket path for unix; host:port for tcp
}

// String returns the handshake line (without a newline).
func (h Handshake) String() string {
	return strconv.Itoa(h.Protocol) + "|" + h.Network + "|" + h.Address
}

// ParseHandshake parses a handshake line (a trailing \n or \r\n is allowed). It checks only the format, not whether
// the host accepts the network and version.
func ParseHandshake(line string) (Handshake, error) {
	line = strings.TrimRight(line, "\r\n")
	if len(line) > MaxHandshakeLine {
		return Handshake{}, fmt.Errorf("%w: line longer than %d bytes", ErrHandshake, MaxHandshakeLine)
	}
	parts := strings.SplitN(line, "|", 3)
	if len(parts) != 3 {
		return Handshake{}, fmt.Errorf("%w: want <protocol>|<network>|<address>, got %q", ErrHandshake, line)
	}
	proto, err := strconv.Atoi(parts[0])
	if err != nil || proto <= 0 {
		return Handshake{}, fmt.Errorf("%w: bad protocol version %q", ErrHandshake, parts[0])
	}
	h := Handshake{Protocol: proto, Network: parts[1], Address: parts[2]}
	switch h.Network {
	case NetworkUnix:
		if !strings.HasPrefix(h.Address, "/") {
			return Handshake{}, fmt.Errorf("%w: unix address must be absolute, got %q", ErrHandshake, h.Address)
		}
	case NetworkTCP:
		if h.Address == "" {
			return Handshake{}, fmt.Errorf("%w: empty tcp address", ErrHandshake)
		}
	default:
		return Handshake{}, fmt.Errorf("%w: unknown network %q", ErrHandshake, h.Network)
	}
	return h, nil
}

// ParseProtocols parses the value of EnvProtocols and returns the major versions sorted ascending without duplicates.
func ParseProtocols(s string) ([]int, error) {
	var out []int
	for f := range strings.SplitSeq(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		v, err := strconv.Atoi(f)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("%w: bad protocol version %q in %s", ErrHandshake, f, EnvProtocols)
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %s is empty", ErrHandshake, EnvProtocols)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// FormatProtocols formats a list of major versions as the value of EnvProtocols.
func FormatProtocols(versions []int) string {
	s := make([]string, len(versions))
	for i, v := range versions {
		s[i] = strconv.Itoa(v)
	}
	return strings.Join(s, ",")
}

// NegotiateProtocol returns the highest major version both sides support, or ErrHandshake when there is none.
func NegotiateProtocol(host, plugin []int) (int, error) {
	best := 0
	for _, v := range plugin {
		if v > best && slices.Contains(host, v) {
			best = v
		}
	}
	if best == 0 {
		return 0, fmt.Errorf("%w: no common protocol version (host %v, plugin %v)", ErrHandshake, host, plugin)
	}
	return best, nil
}
