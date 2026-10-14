package sdk_test

import (
	"context"
	"errors"
	"net"
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

func TestHostUnavailableWithoutEnvironment(t *testing.T) {
	t.Parallel()
	for name, env := range map[string]map[string]string{
		"neither":    {},
		"no token":   {pluginapi.EnvHostSocket: "/tmp/h.sock"},
		"no socket":  {pluginapi.EnvHostToken: "tok"},
		"empty both": {pluginapi.EnvHostSocket: "", pluginapi.EnvHostToken: ""},
	} {
		if _, err := sdk.Host(func(k string) string { return env[k] }); !errors.Is(err, sdk.ErrHostUnavailable) {
			t.Errorf("%s: err = %v, want ErrHostUnavailable", name, err)
		}
	}
}

// fakeHost is a Host service that records the request and the token, and answers with resp or err.
type fakeHost struct {
	pluginv1.UnimplementedHostServer
	resp  *pluginv1.LLMCompleteResponse
	err   error
	token string
	req   *pluginv1.LLMCompleteRequest
}

func (h *fakeHost) LLMComplete(ctx context.Context, req *pluginv1.LLMCompleteRequest) (*pluginv1.LLMCompleteResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if v := md.Get(pluginv1.MetadataHostToken); len(v) == 1 {
		h.token = v[0]
	}
	h.req = req
	return h.resp, h.err
}

// serveHost serves h on a Unix socket and returns a connected client.
func serveHost(t *testing.T, h *fakeHost) *sdk.HostClient {
	t.Helper()
	dir, err := os.MkdirTemp("", "kph") // short path: Unix socket paths are limited to 107 bytes
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

func TestHostLLMCompleteSendsTokenAndMapsFields(t *testing.T) {
	t.Parallel()
	h := &fakeHost{resp: &pluginv1.LLMCompleteResponse{Text: `{"ok":true}`, Model: "m1",
		Usage: &pluginv1.LLMUsage{Known: true, PromptTokens: 12, CompletionTokens: 3}}}
	c := serveHost(t, h)
	got, err := c.LLMComplete(t.Context(), sdk.LLMRequest{
		System:     "sys",
		Messages:   []sdk.LLMMessage{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}},
		JSONSchema: `{"type":"object"}`, MaxTokens: 64, Temperature: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != (sdk.LLMResponse{Text: `{"ok":true}`, Model: "m1", UsageKnown: true, PromptTokens: 12, CompletionTokens: 3}) {
		t.Errorf("response = %+v", got)
	}
	if h.token != "secret-token" {
		t.Errorf("token = %q, want the KRYSPAL_HOST_TOKEN value", h.token)
	}
	r := h.req
	if r.GetSystem() != "sys" || r.GetJsonSchema() != `{"type":"object"}` || r.GetMaxTokens() != 64 || r.GetTemperature() != 0.5 ||
		len(r.GetMessages()) != 2 || r.GetMessages()[1].GetRole() != "assistant" || r.GetMessages()[1].GetContent() != "hello" {
		t.Errorf("request = %+v", r)
	}
}

func TestHostLLMCompleteErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		err      error
		want     error      // a fixed SDK error, compared with errors.Is
		wantCode codes.Code // otherwise the status code that passes through
	}{
		{"disabled", status.Error(codes.FailedPrecondition, pluginv1.CodeAIAssistDisabled), sdk.ErrAIAssistDisabled, 0},
		{"not allowed", status.Error(codes.PermissionDenied, pluginv1.CodeAIAssistNotAllowed), sdk.ErrAIAssistNotAllowed, 0},
		// The fixed codes count only with their documented status code.
		{"code mismatch", status.Error(codes.Unavailable, pluginv1.CodeAIAssistDisabled), nil, codes.Unavailable},
		{"rate limited", status.Error(codes.ResourceExhausted, "slow down"), nil, codes.ResourceExhausted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := serveHost(t, &fakeHost{err: tc.err})
			_, err := c.LLMComplete(t.Context(), sdk.LLMRequest{Messages: []sdk.LLMMessage{{Role: "user", Content: "x"}}})
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
				return
			}
			if status.Code(err) != tc.wantCode || errors.Is(err, sdk.ErrAIAssistDisabled) {
				t.Fatalf("err = %v, want status %v passed through", err, tc.wantCode)
			}
		})
	}
}
