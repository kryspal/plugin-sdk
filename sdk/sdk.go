// Package sdk is the Kryspal Media Plugin SDK for Go plugin authors: it performs the handshake, creates the Unix socket,
// starts the gRPC server (including the standard health check), and shuts down gracefully on SIGTERM. See
// pluginapi/PROTOCOL.md for the protocol specification.
//
// Minimal usage:
//
//	type provider struct {
//		pluginv1.UnimplementedPluginServer
//		pluginv1.UnimplementedMetadataProviderServer
//	}
//
//	func main() {
//		p := &provider{}
//		sdk.Main(sdk.Config{Plugin: p, MetadataProvider: p})
//	}
//
// A plugin always writes its logs to stderr (one entry per line; the host forwards them line by line into the server
// log); nothing may be written to stdout after the handshake. This package depends only on the standard library, grpc
// and protobuf.
package sdk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/kryspal/plugin-sdk"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// ErrNotLaunchedByHost reports that the plugin was not started by the host (cookie mismatch).
var ErrNotLaunchedByHost = errors.New("this is a Kryspal Media plugin and must be started by the server, not run directly")

// ErrHostTooOld reports that the plugin was started by an older host: older hosts pass MEDIASERVER_PLUGIN_* environment
// variables (before the protocol rename, change 0098).
var ErrHostTooOld = errors.New("this Kryspal Media plugin needs a newer server: the host speaks the pre-0098 plugin protocol (MEDIASERVER_PLUGIN_*); update the server")

// legacyEnvCookie is the cookie environment variable hosts passed before the rename; it is used only to tell "host too
// old" apart from "run directly".
const legacyEnvCookie = "MEDIASERVER_PLUGIN_COOKIE"

// DefaultGracePeriod is how long to wait for in-flight calls to finish after a termination signal. The host sends SIGKILL
// 5 seconds after SIGTERM, so the default is slightly shorter than 5 seconds.
const DefaultGracePeriod = 4 * time.Second

// Config is the configuration of Serve.
type Config struct {
	// Plugin is required.
	Plugin pluginv1.PluginServer
	// MetadataProvider is required when the plugin declares metadata.* capabilities; when nil, the service is not registered.
	MetadataProvider pluginv1.MetadataProviderServer
	// ImageProvider, SubtitleProvider and UserSync are required when the plugin declares the matching capabilities
	// (images.*, subtitles.search, usersync.*); when nil, the service is not registered (change 0098).
	ImageProvider    pluginv1.ImageProviderServer
	SubtitleProvider pluginv1.SubtitleProviderServer
	UserSync         pluginv1.UserSyncServer
	// Recognizer is required when the plugin declares media.recognize; when nil, the service is not registered (change 0121).
	Recognizer pluginv1.RecognizerServer
	// SegmentProvider and DanmakuProvider are required when the plugin declares segments.* and danmaku.provider; when nil,
	// they are not registered (change 0122).
	SegmentProvider pluginv1.SegmentProviderServer
	DanmakuProvider pluginv1.DanmakuProviderServer
	// CastTarget is required when the plugin declares cast.target; when nil, it is not registered (change 0109).
	CastTarget pluginv1.CastTargetServer
	// PersonAliasProvider is required when the plugin declares people.aliasmap; when nil, it is not registered (change 0149).
	PersonAliasProvider pluginv1.PersonAliasProviderServer

	// GracePeriod of 0 means DefaultGracePeriod.
	GracePeriod time.Duration
	// ServerOptions are appended to grpc.NewServer. The SDK already adds its own unary interceptor (error conversion and
	// panic recovery); interceptors added here run after it.
	ServerOptions []grpc.ServerOption

	// The following can be replaced by tests; when nil, os.Getenv and os.Stdout are used.
	Getenv func(string) string
	Stdout io.Writer
}

// Main is a convenience entry point for a plugin's main: it stops on SIGTERM or SIGINT, and on error writes it to stderr
// and exits with status 1. After the handshake line is printed, os.Stdout is pointed at stderr, so a stray write to stdout
// by the plugin or a package it depends on cannot break the protocol.
func Main(cfg Config) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if cfg.Stdout == nil {
		cfg.Stdout = &redirectAfterHandshake{}
	}
	if err := Serve(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		stop()
		os.Exit(1)
	}
}

// redirectAfterHandshake writes the handshake line to the real stdout, then replaces os.Stdout with os.Stderr.
type redirectAfterHandshake struct{}

func (redirectAfterHandshake) Write(p []byte) (int, error) {
	out := os.Stdout
	n, err := out.Write(p)
	os.Stdout = os.Stderr // stdout must not be used after the handshake (PROTOCOL.md)
	return n, err
}

// Serve performs the handshake and serves gRPC until ctx is done; it then stops accepting new calls, waits up to
// GracePeriod for in-flight calls, removes the socket and returns nil. Errors before the handshake (cookie mismatch, no
// common protocol version, socket creation failure) are returned directly.
func Serve(ctx context.Context, cfg Config) error {
	if cfg.Plugin == nil {
		return errors.New("sdk: Config.Plugin is required")
	}
	getenv := cfg.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	stdout := cfg.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	grace := cfg.GracePeriod
	if grace <= 0 {
		grace = DefaultGracePeriod
	}

	if getenv(pluginapi.EnvCookie) != pluginapi.CookieValue {
		if getenv(legacyEnvCookie) != "" {
			return ErrHostTooOld
		}
		return ErrNotLaunchedByHost
	}
	hostVersions, err := pluginapi.ParseProtocols(getenv(pluginapi.EnvProtocols))
	if err != nil {
		return fmt.Errorf("sdk: %w", err)
	}
	proto, err := pluginapi.NegotiateProtocol(hostVersions, []int{pluginv1.ProtocolVersion})
	if err != nil {
		return fmt.Errorf("sdk: %w", err)
	}
	dir := getenv(pluginapi.EnvSocketDir)
	if dir == "" || !filepath.IsAbs(dir) {
		return fmt.Errorf("sdk: %w: %s must be an absolute directory, got %q", pluginapi.ErrHandshake, pluginapi.EnvSocketDir, dir)
	}
	sock := filepath.Join(dir, pluginapi.SocketName)
	if err := os.Remove(sock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("sdk: remove stale socket: %w", err)
	}
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, pluginapi.NetworkUnix, sock)
	if err != nil {
		return fmt.Errorf("sdk: listen: %w", err)
	}
	defer func() { _ = os.Remove(sock) }()
	if err := os.Chmod(sock, 0o600); err != nil {
		_ = lis.Close()
		return fmt.Errorf("sdk: chmod socket: %w", err)
	}

	opts := append([]grpc.ServerOption{grpc.ChainUnaryInterceptor(unaryInterceptor), grpc.ChainStreamInterceptor(streamInterceptor)}, cfg.ServerOptions...)
	srv := grpc.NewServer(opts...)
	hs := health.NewServer()
	pluginv1.RegisterPluginServer(srv, cfg.Plugin)
	hs.SetServingStatus(pluginv1.HealthServicePlugin, healthpb.HealthCheckResponse_SERVING)
	if cfg.MetadataProvider != nil {
		pluginv1.RegisterMetadataProviderServer(srv, cfg.MetadataProvider)
		hs.SetServingStatus(pluginv1.HealthServiceMetadataProvider, healthpb.HealthCheckResponse_SERVING)
	}
	if cfg.ImageProvider != nil {
		pluginv1.RegisterImageProviderServer(srv, cfg.ImageProvider)
		hs.SetServingStatus(pluginv1.HealthServiceImageProvider, healthpb.HealthCheckResponse_SERVING)
	}
	if cfg.SubtitleProvider != nil {
		pluginv1.RegisterSubtitleProviderServer(srv, cfg.SubtitleProvider)
		hs.SetServingStatus(pluginv1.HealthServiceSubtitleProvider, healthpb.HealthCheckResponse_SERVING)
	}
	if cfg.UserSync != nil {
		pluginv1.RegisterUserSyncServer(srv, cfg.UserSync)
		hs.SetServingStatus(pluginv1.HealthServiceUserSync, healthpb.HealthCheckResponse_SERVING)
	}
	if cfg.Recognizer != nil {
		pluginv1.RegisterRecognizerServer(srv, cfg.Recognizer)
		hs.SetServingStatus(pluginv1.HealthServiceRecognizer, healthpb.HealthCheckResponse_SERVING)
	}
	if cfg.SegmentProvider != nil {
		pluginv1.RegisterSegmentProviderServer(srv, cfg.SegmentProvider)
		hs.SetServingStatus(pluginv1.HealthServiceSegmentProvider, healthpb.HealthCheckResponse_SERVING)
	}
	if cfg.DanmakuProvider != nil {
		pluginv1.RegisterDanmakuProviderServer(srv, cfg.DanmakuProvider)
		hs.SetServingStatus(pluginv1.HealthServiceDanmakuProvider, healthpb.HealthCheckResponse_SERVING)
	}
	if cfg.CastTarget != nil {
		pluginv1.RegisterCastTargetServer(srv, cfg.CastTarget)
		hs.SetServingStatus(pluginv1.HealthServiceCastTarget, healthpb.HealthCheckResponse_SERVING)
	}
	if cfg.PersonAliasProvider != nil {
		pluginv1.RegisterPersonAliasProviderServer(srv, cfg.PersonAliasProvider)
		hs.SetServingStatus(pluginv1.HealthServicePersonAliasProvider, healthpb.HealthCheckResponse_SERVING)
	}
	healthpb.RegisterHealthServer(srv, hs)

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(lis) }()

	line := pluginapi.Handshake{Protocol: proto, Network: pluginapi.NetworkUnix, Address: sock}.String() + "\n"
	if _, err := io.WriteString(stdout, line); err != nil {
		srv.Stop()
		return fmt.Errorf("sdk: write handshake: %w", err)
	}

	select {
	case err := <-serveErr:
		hs.Shutdown()
		return fmt.Errorf("sdk: grpc serve: %w", err)
	case <-ctx.Done():
	}

	hs.Shutdown() // health check goes back to NOT_SERVING
	done := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(done)
	}()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		srv.Stop()
		<-done
	}
	return nil
}

// unaryInterceptor converts the SDK's error types into gRPC status codes and turns a handler panic into INTERNAL, so a
// failure in a single request cannot crash the whole plugin.
func unaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic in %s: %v\n%s", info.FullMethod, r, debug.Stack())
			resp, err = nil, status.Errorf(codes.Internal, "plugin panic in %s", info.FullMethod)
		}
	}()
	resp, err = handler(ctx, req)
	if err == nil {
		return resp, nil
	}
	return nil, convertError(err, func(md metadata.MD) { _ = grpc.SetTrailer(ctx, md) })
}

// streamInterceptor does for streaming methods (GetAliasMap, change 0149) what unaryInterceptor does for unary ones.
func streamInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic in %s: %v\n%s", info.FullMethod, r, debug.Stack())
			err = status.Errorf(codes.Internal, "plugin panic in %s", info.FullMethod)
		}
	}()
	if err = handler(srv, ss); err == nil {
		return nil
	}
	return convertError(err, ss.SetTrailer)
}

// convertError turns the SDK's error types into gRPC status errors; setTrailer attaches retry-after-seconds.
func convertError(err error, setTrailer func(metadata.MD)) error {
	if rl, ok := errors.AsType[*RateLimitError](err); ok {
		md := pluginv1.RetryAfterTrailer(rl.RetryAfter)
		if rl.Credential {
			md.Set(pluginv1.TrailerRetryAfterScope, pluginv1.RetryScopeCredential)
		}
		setTrailer(md)
		return status.Error(codes.ResourceExhausted, rl.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, err.Error())
	}
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, err.Error())
	}
	return err
}

// RateLimitError reports an upstream rate limit. It is created by RateLimited; the SDK converts it into
// RESOURCE_EXHAUSTED and sends retry-after-seconds in the trailer.
type RateLimitError struct {
	RetryAfter time.Duration
	Message    string
	// Credential, when true, pauses only this one credential (retry-after-scope: credential, change 0098); otherwise the
	// whole plugin is paused.
	Credential bool
}

func (e *RateLimitError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("rate limited, retry after %s", e.RetryAfter)
	}
	return e.Message
}

// RateLimited returns an upstream rate limit error: the host pauses the whole plugin for retryAfter (rounded up to one
// second if shorter).
func RateLimited(retryAfter time.Duration, msg string) error {
	return &RateLimitError{RetryAfter: retryAfter, Message: msg}
}

// RateLimitedCredential returns an upstream rate limit that affects only this one credential (e.g. an upstream per-user
// quota): the host pauses only this member, while other members and the plugin as a whole carry on (change 0098).
func RateLimitedCredential(retryAfter time.Duration, msg string) error {
	return &RateLimitError{RetryAfter: retryAfter, Message: msg, Credential: true}
}

// Unauthenticated returns UNAUTHENTICATED: this member's credential is no longer valid (revoked, or expired and not
// refreshable). The host only marks this member's link as needing to be re-linked; the plugin stays online (change 0098).
func Unauthenticated(msg string) error {
	return status.Error(codes.Unauthenticated, msg)
}

// NeedsConfiguration returns FAILED_PRECONDITION: the plugin is not configured yet or its configuration is invalid (e.g.
// the upstream rejects the API key).
func NeedsConfiguration(msg string) error {
	return status.Error(codes.FailedPrecondition, msg)
}

// NotFound returns NOT_FOUND: the provider cannot find that ID.
func NotFound(msg string) error {
	return status.Error(codes.NotFound, msg)
}

// InvalidArgument returns INVALID_ARGUMENT: the request itself is invalid, e.g. a required field is missing.
func InvalidArgument(msg string) error {
	return status.Error(codes.InvalidArgument, msg)
}

// Unavailable returns UNAVAILABLE: the upstream is temporarily unreachable; the host retries later.
func Unavailable(msg string) error {
	return status.Error(codes.Unavailable, msg)
}
