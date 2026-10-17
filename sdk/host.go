package sdk

import (
	"context"
	"errors"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/kryspal/plugin-sdk"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// The service the host offers to plugins (Host): plugins call back into the host.

// ErrHostUnavailable reports that the host does not offer the Host service (the environment variables KRYSPAL_HOST_SOCKET
// and KRYSPAL_HOST_TOKEN are absent): the plugin is running on an older host that does not know the Host service. The
// plugin should degrade gracefully (not use that capability); this is not an error.
var ErrHostUnavailable = errors.New("sdk: the host does not offer the Host service")

// Fixed errors that calls to the Host service can return (compare with errors.Is).
var (
	// ErrAIAssistDisabled: the administrator has not enabled "AI assist" (or has not configured its address and key yet).
	ErrAIAssistDisabled = errors.New("ai_assist_disabled")
	// ErrAIAssistNotAllowed: the plugin manifest does not declare permissions: ["llm"], or the administrator has not allowed
	// this plugin to use AI assist.
	ErrAIAssistNotAllowed = errors.New("ai_assist_not_allowed")
)

// HostClient is the client a plugin uses to call the host's Host service.
type HostClient struct {
	conn *grpc.ClientConn
	c    pluginv1.HostClient
}

// Host connects to the host's Host service. It returns ErrHostUnavailable when the service is not offered. The caller must
// Close the client when done. A nil getenv means os.Getenv (tests can inject one).
func Host(getenv func(string) string) (*HostClient, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	sock, token := getenv(pluginapi.EnvHostSocket), getenv(pluginapi.EnvHostToken)
	if sock == "" || token == "" {
		return nil, ErrHostUnavailable
	}
	conn, err := grpc.NewClient("unix:"+sock,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			return invoker(metadata.AppendToOutgoingContext(ctx, pluginv1.MetadataHostToken, token), method, req, reply, cc, opts...)
		}))
	if err != nil {
		return nil, err
	}
	return &HostClient{conn: conn, c: pluginv1.NewHostClient(conn)}, nil
}

// Close closes the connection.
func (h *HostClient) Close() error { return h.conn.Close() }

// LLMMessage is one message of a conversation; Role is user or assistant.
type LLMMessage struct{ Role, Content string }

// LLMRequest is the request of LLMComplete.
type LLMRequest struct {
	System   string
	Messages []LLMMessage
	// JSONSchema, when non-empty, requests structured output; the response Text is JSON already validated against the schema.
	JSONSchema string
	MaxTokens  uint32
	// Temperature defaults to 0 (reproducible); the host may ignore values above 0.
	Temperature float32
}

// LLMResponse is the response of LLMComplete.
type LLMResponse struct {
	Text  string
	Model string
	// UsageKnown is false when the service provider did not report usage (the host does not estimate it).
	UsageKnown                     bool
	PromptTokens, CompletionTokens uint64
}

// LLMComplete calls "AI assist" through the host. Errors: ErrAIAssistDisabled and ErrAIAssistNotAllowed (errors.Is); the
// rest are gRPC status errors (DEADLINE_EXCEEDED, RESOURCE_EXHAUSTED, UNAVAILABLE…, see host.proto). The caller must set
// its own timeout (ctx).
func (h *HostClient) LLMComplete(ctx context.Context, req LLMRequest) (LLMResponse, error) {
	pr := &pluginv1.LLMCompleteRequest{System: req.System, JsonSchema: req.JSONSchema, MaxTokens: req.MaxTokens, Temperature: req.Temperature}
	for _, m := range req.Messages {
		pr.Messages = append(pr.Messages, &pluginv1.LLMMessage{Role: m.Role, Content: m.Content})
	}
	resp, err := h.c.LLMComplete(ctx, pr)
	if err != nil {
		return LLMResponse{}, hostError(err)
	}
	u := resp.GetUsage()
	return LLMResponse{Text: resp.GetText(), Model: resp.GetModel(), UsageKnown: u.GetKnown(),
		PromptTokens: u.GetPromptTokens(), CompletionTokens: u.GetCompletionTokens()}, nil
}

// hostError turns the Host service's fixed codes into comparable errors and returns anything else unchanged.
func hostError(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch strings.TrimSpace(st.Message()) {
	case pluginv1.CodeAIAssistDisabled:
		if st.Code() == codes.FailedPrecondition {
			return ErrAIAssistDisabled
		}
	case pluginv1.CodeAIAssistNotAllowed:
		if st.Code() == codes.PermissionDenied {
			return ErrAIAssistNotAllowed
		}
	}
	return err
}
