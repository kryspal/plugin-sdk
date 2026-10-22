package httpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/kryspal/plugin-sdk/sdk"
)

func newServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func TestSendsUserAgentAndHeaders(t *testing.T) {
	var got http.Header
	s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{"a":1}`))
	})
	c := New(Config{Name: "x", UserAgent: "Kryspal Media v1", Header: http.Header{"X-Api-Key": {"k"}}})
	var out struct{ A int }
	if err := c.JSON(t.Context(), Request{URL: s.URL, Header: http.Header{"X-Extra": {"e"}}}, &out); err != nil || out.A != 1 {
		t.Fatalf("%v %+v", err, out)
	}
	if got.Get("User-Agent") != "Kryspal Media v1" || got.Get("X-Api-Key") != "k" || got.Get("X-Extra") != "e" || got.Get("Accept") != "application/json" {
		t.Fatalf("headers = %v", got)
	}
}

func TestQueryIsAppendedToAnExistingQuery(t *testing.T) {
	var q string
	s := newServer(t, func(w http.ResponseWriter, r *http.Request) { q = r.URL.RawQuery; _, _ = w.Write([]byte(`{}`)) })
	c := New(Config{Name: "x", UserAgent: "ua"})
	if err := c.JSON(t.Context(), Request{URL: s.URL + "?a=1", Query: map[string][]string{"b": {"2"}}}, nil); err != nil || q != "a=1&b=2" {
		t.Fatalf("%v %q", err, q)
	}
}

func TestPostsJSON(t *testing.T) {
	var ct, body string
	s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		ct = r.Header.Get("Content-Type")
		b := make([]byte, 64)
		n, _ := r.Body.Read(b)
		body = string(b[:n])
		_, _ = w.Write([]byte(`{}`))
	})
	c := New(Config{Name: "x", UserAgent: "ua"})
	if err := c.JSON(t.Context(), Request{Method: http.MethodPost, URL: s.URL, Body: map[string]int{"n": 1}}, nil); err != nil || ct != "application/json" || body != `{"n":1}` {
		t.Fatalf("%v %q %q", err, ct, body)
	}
}

func TestStatusMapping(t *testing.T) {
	cases := []struct {
		status int
		want   codes.Code
	}{{401, codes.Unauthenticated}, {403, codes.FailedPrecondition}, {404, codes.NotFound}, {418, codes.Unavailable}, {503, codes.Unavailable}}
	for _, c := range cases {
		s := newServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(c.status) })
		err := New(Config{Name: "x", UserAgent: "ua"}).JSON(t.Context(), Request{URL: s.URL}, nil)
		if status.Code(err) != c.want {
			t.Errorf("status %d: %v, want %v", c.status, status.Code(err), c.want)
		}
	}
}

func TestRateLimitCarriesRetryAfter(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/with" {
			w.Header().Set("Retry-After", "42")
		}
		w.WriteHeader(http.StatusTooManyRequests)
	})
	c := New(Config{Name: "x", UserAgent: "ua", RetryAfter: 7 * time.Second})
	for path, want := range map[string]time.Duration{"/with": 42 * time.Second, "/without": 7 * time.Second} {
		var rl *sdk.RateLimitError
		if err := c.JSON(t.Context(), Request{URL: s.URL + path}, nil); !errors.As(err, &rl) || rl.RetryAfter != want || rl.Credential {
			t.Errorf("%s: %#v", path, err)
		}
	}
	resp, err := c.Do(t.Context(), Request{URL: s.URL + "/with"})
	var rl *sdk.RateLimitError
	if err != nil || resp.Status != 429 || !errors.As(resp.CredentialRateLimit("x"), &rl) || !rl.Credential || rl.RetryAfter != 42*time.Second {
		t.Fatalf("Do: %v %+v", err, resp)
	}
}

func TestOversizedAnswerIsUnavailable(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("a", 100))) })
	err := New(Config{Name: "x", UserAgent: "ua", MaxBodyBytes: 50}).JSON(t.Context(), Request{URL: s.URL}, nil)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectionErrorsDoNotLeakTheURL(t *testing.T) {
	err := New(Config{Name: "x", UserAgent: "ua"}).JSON(t.Context(), Request{URL: "http://127.0.0.1:1/?api_key=SECRET"}, nil)
	if status.Code(err) != codes.Unavailable || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err = %v", err)
	}
}

func TestOneRequestAtATimeByDefaultAndSpaced(t *testing.T) {
	peak, starts := runParallel(t, Config{Name: "x", UserAgent: "ua", MinInterval: 40 * time.Millisecond}, 3)
	if peak != 1 {
		t.Fatalf("peak concurrency = %d, want 1", peak)
	}
	for i := 1; i < len(starts); i++ {
		if d := starts[i].Sub(starts[i-1]); d < 35*time.Millisecond {
			t.Fatalf("requests %d and %d were %v apart", i-1, i, d)
		}
	}
}

func TestMaxConcurrentAllowsOverlapAndOnlySpacesTheStarts(t *testing.T) {
	peak, _ := runParallel(t, Config{Name: "x", UserAgent: "ua", MaxConcurrent: 4, MinInterval: 10 * time.Millisecond}, 4)
	// Four requests of 60 ms each, started 10 ms apart, overlap. If the interval held back running requests,
	// at most one would be in flight. Wall-clock bounds and server-side arrival gaps are not checked: on a
	// loaded machine requests sent 10 ms apart can arrive together. TestOneRequestAtATimeByDefaultAndSpaced covers the spacing.
	if peak < 2 || peak > 4 {
		t.Fatalf("peak concurrency = %d, want 2 to 4: the interval held back running requests", peak)
	}
}

// runParallel sends n requests at once to a server that takes 60 ms each, and returns the highest number it saw in flight and the start times in order.
func runParallel(t *testing.T, cfg Config, n int) (int, []time.Time) {
	t.Helper()
	var running, peak atomic.Int32
	var mu sync.Mutex
	var starts []time.Time
	s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		cur := running.Add(1)
		for p := peak.Load(); cur > p && !peak.CompareAndSwap(p, cur); p = peak.Load() {
		}
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		time.Sleep(60 * time.Millisecond)
		running.Add(-1)
		_, _ = w.Write([]byte(`{}`))
	})
	c := New(cfg)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() { defer wg.Done(); _ = c.JSON(t.Context(), Request{URL: s.URL}, nil) }()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	return int(peak.Load()), starts
}

func TestRetryAfterAcceptsAnHTTPDate(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", time.Now().Add(90*time.Second).UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusTooManyRequests)
	})
	var rl *sdk.RateLimitError
	err := New(Config{Name: "x", UserAgent: "ua", RetryAfter: 7 * time.Second}).JSON(t.Context(), Request{URL: s.URL}, nil)
	if !errors.As(err, &rl) || rl.RetryAfter < 80*time.Second || rl.RetryAfter > 91*time.Second {
		t.Fatalf("err = %#v", err)
	}
}

func TestCancelledContextReturnsItsOwnError(t *testing.T) {
	s := newServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	c := New(Config{Name: "x", UserAgent: "ua", MinInterval: time.Hour})
	if err := c.JSON(t.Context(), Request{URL: s.URL}, nil); err != nil { // the first request starts at once
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := c.JSON(ctx, Request{URL: s.URL}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}
