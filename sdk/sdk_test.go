package sdk_test

import (
	"errors"
	"testing"
	"time"

	"github.com/kryspal/plugin-sdk"
	pluginv1 "github.com/kryspal/plugin-sdk/pluginv1"
	"github.com/kryspal/plugin-sdk/sdk"
)

type nopPlugin struct {
	pluginv1.UnimplementedPluginServer
}

func TestServeRejectsBadEnvironment(t *testing.T) {
	t.Parallel()
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := map[string]struct {
		env  map[string]string
		want error
	}{
		"no cookie":    {map[string]string{}, sdk.ErrNotLaunchedByHost},
		"no protocols": {map[string]string{pluginapi.EnvCookie: pluginapi.CookieValue}, pluginapi.ErrHandshake},
		"relative dir": {map[string]string{
			pluginapi.EnvCookie: pluginapi.CookieValue, pluginapi.EnvProtocols: "1", pluginapi.EnvSocketDir: "rel",
		}, pluginapi.ErrHandshake},
	}
	for name, tc := range cases {
		err := sdk.Serve(t.Context(), sdk.Config{Plugin: nopPlugin{}, Getenv: env(tc.env)})
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

func TestRetryAfterTrailer(t *testing.T) {
	t.Parallel()
	for in, want := range map[time.Duration]time.Duration{
		1500 * time.Millisecond: 2 * time.Second,
		0:                       time.Second,
		30 * time.Second:        30 * time.Second,
		48 * time.Hour:          pluginv1.MaxRetryAfter,
	} {
		got, ok := pluginv1.RetryAfter(pluginv1.RetryAfterTrailer(in))
		if !ok || got != want {
			t.Errorf("RetryAfter(%v) = %v, %v; want %v", in, got, ok, want)
		}
	}
	if d, ok := pluginv1.RetryAfter(nil); ok || d != pluginv1.DefaultRetryAfter {
		t.Errorf("missing trailer = %v, %v", d, ok)
	}
	if !errors.As(sdk.RateLimited(time.Second, "x"), new(*sdk.RateLimitError)) {
		t.Error("RateLimited type")
	}
}
