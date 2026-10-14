package sdk

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
)

// fakeStream records the trailer a streaming handler sets (change 0149).
type fakeStream struct {
	grpc.ServerStream
	trailer metadata.MD
}

func (f *fakeStream) SetTrailer(md metadata.MD) { f.trailer = metadata.Join(f.trailer, md) }
func (f *fakeStream) Context() context.Context  { return context.Background() }

func TestStreamInterceptorConvertsErrors(t *testing.T) {
	info := &grpc.StreamServerInfo{FullMethod: "/kryspal.plugin.v1.PersonAliasProvider/GetAliasMap"}
	ss := &fakeStream{}
	err := streamInterceptor(nil, ss, info, func(any, grpc.ServerStream) error {
		return RateLimited(90*time.Second, "slow down")
	})
	if status.Code(err) != codes.ResourceExhausted || ss.trailer.Get(pluginv1.TrailerRetryAfter)[0] != "90" {
		t.Fatalf("rate limit: err = %v, trailer = %v", err, ss.trailer)
	}
	err = streamInterceptor(nil, &fakeStream{}, info, func(any, grpc.ServerStream) error { panic("boom") })
	if status.Code(err) != codes.Internal {
		t.Fatalf("panic: err = %v, want INTERNAL", err)
	}
	if err := streamInterceptor(nil, &fakeStream{}, info, func(any, grpc.ServerStream) error { return nil }); err != nil {
		t.Fatalf("success: err = %v", err)
	}
}
