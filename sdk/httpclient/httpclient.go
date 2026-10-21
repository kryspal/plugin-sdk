// Package httpclient is the HTTP client most plugins need to talk to an upstream service politely: one request at a time, a minimum
// interval between requests, a recognizable User-Agent, a cap on the size of an answer, and the shared mapping of upstream failures to the
// errors the host understands (5xx and connection errors are UNAVAILABLE; a 429 becomes a rate limit with the Retry-After seconds).
//
//	c := httpclient.New(httpclient.Config{Name: "example", UserAgent: "Kryspal Media v1.0.0", MinInterval: 300 * time.Millisecond})
//	var out result
//	err := c.JSON(ctx, httpclient.Request{URL: "https://api.example.com/search", Query: url.Values{"q": {"x"}}}, &out)
package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/kryspal/plugin-sdk/sdk"
)

const (
	defaultTimeout    = 30 * time.Second
	defaultMaxBody    = 4 << 20
	defaultRetryAfter = 30 * time.Second
)

// Config is the fixed part of a client.
type Config struct {
	// Name prefixes the error messages, usually the plugin id.
	Name string
	// UserAgent is sent on every request. Most upstream services ask for one that names the application.
	UserAgent string
	// Timeout of one request; 30 seconds when zero.
	Timeout time.Duration
	// MinInterval is the least time between the start of two requests; zero means no pause. It spaces out when requests are sent and does not
	// hold back a request that is already running.
	MinInterval time.Duration
	// MaxConcurrent is how many requests may be in flight at once; 1 when zero. Requests over the limit wait for a free place.
	MaxConcurrent int
	// MaxBodyBytes caps the size of an answer that is read; 4 MiB when zero. A longer answer is an UNAVAILABLE error.
	MaxBodyBytes int64
	// RetryAfter is used when a 429 carries no usable Retry-After header; 30 seconds when zero.
	RetryAfter time.Duration
	// Header is added to every request (for example an API key header).
	Header http.Header
	// Transport replaces the default transport, for tests.
	Transport http.RoundTripper
}

// Client sends requests, at most MaxConcurrent at once and at least MinInterval apart. It is safe for concurrent use; calls over the limit queue.
type Client struct {
	cfg  Config
	http *http.Client
	sem  chan struct{}
	mu   sync.Mutex // guards next only; it is never held while a request runs
	next time.Time  // the earliest time the next request may start
}

// New returns a client.
func New(cfg Config) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = defaultMaxBody
	}
	if cfg.RetryAfter <= 0 {
		cfg.RetryAfter = defaultRetryAfter
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 1
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout, Transport: cfg.Transport}, sem: make(chan struct{}, cfg.MaxConcurrent)}
}

// Request is one call. Body, when not nil, is sent as JSON.
type Request struct {
	Method string // GET when empty
	URL    string
	Query  url.Values
	Header http.Header
	Body   any
}

// Response is an answer that was read in full.
type Response struct {
	Status int
	Header http.Header
	Body   []byte

	retry time.Duration
}

// RetryAfter is the pause a 429 asked for.
func (r Response) RetryAfter() time.Duration { return r.retry }

// RateLimit is the error to return for a 429: it pauses the whole plugin.
func (r Response) RateLimit(name string) error {
	return sdk.RateLimited(r.retry, name+": rate limit reached")
}

// CredentialRateLimit is the error to return for a 429 that concerns one member's credential only.
func (r Response) CredentialRateLimit(name string) error {
	return sdk.RateLimitedCredential(r.retry, name+": rate limit reached")
}

// Do sends one request and reads the whole answer. Connection errors, a cancelled context and 5xx answers are returned as errors (the
// context's own error when it ended, UNAVAILABLE otherwise); every other status, 429 included, is returned in the Response for the caller.
func (c *Client) Do(ctx context.Context, r Request) (Response, error) {
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return Response{}, ctx.Err()
	}
	if err := c.wait(ctx); err != nil {
		return Response{}, err
	}
	var rd io.Reader
	if r.Body != nil {
		b, err := json.Marshal(r.Body)
		if err != nil {
			return Response{}, sdk.InvalidArgument(c.cfg.Name + ": bad request body")
		}
		rd = bytes.NewReader(b)
	}
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	u := r.URL
	if len(r.Query) > 0 {
		sep := "?"
		if u2, err := url.Parse(u); err == nil && u2.RawQuery != "" {
			sep = "&"
		}
		u += sep + r.Query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return Response{}, sdk.InvalidArgument(c.cfg.Name + ": bad request")
	}
	req.Header.Set("Accept", "application/json")
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range c.cfg.Header {
		req.Header[k] = v
	}
	for k, v := range r.Header {
		req.Header[k] = v
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return Response{}, c.transportError(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxBodyBytes+1))
	if err != nil {
		return Response{}, c.transportError(ctx, err)
	}
	if int64(len(raw)) > c.cfg.MaxBodyBytes {
		return Response{}, sdk.Unavailable(c.cfg.Name + ": the answer is too large")
	}
	if resp.StatusCode >= 500 {
		return Response{}, sdk.Unavailable(c.cfg.Name + ": " + resp.Status)
	}
	return Response{Status: resp.StatusCode, Header: resp.Header, Body: raw, retry: c.retryAfter(resp)}, nil
}

// JSON sends the request and decodes a 200 answer into out. A 429 is a plugin-wide rate limit; 401 is UNAUTHENTICATED (a member's credential is no
// longer valid); 403 is FAILED_PRECONDITION (the plugin's own settings are refused, for example its API key: the plugin needs configuration, no
// member has to link again); 404 is NOT_FOUND; any other non-200 status is UNAVAILABLE. Callers that read statuses differently use Do.
func (c *Client) JSON(ctx context.Context, r Request, out any) error {
	resp, err := c.Do(ctx, r)
	if err != nil {
		return err
	}
	switch {
	case resp.Status == http.StatusTooManyRequests:
		return resp.RateLimit(c.cfg.Name)
	case resp.Status == http.StatusUnauthorized:
		return sdk.Unauthenticated(c.cfg.Name + ": " + strconv.Itoa(resp.Status))
	case resp.Status == http.StatusForbidden:
		return sdk.NeedsConfiguration(c.cfg.Name + ": the upstream refused the plugin's settings (403)")
	case resp.Status == http.StatusNotFound:
		return sdk.NotFound(c.cfg.Name + ": not found")
	case resp.Status != http.StatusOK:
		return sdk.Unavailable(c.cfg.Name + ": status " + strconv.Itoa(resp.Status))
	}
	if out != nil {
		if err := json.Unmarshal(resp.Body, out); err != nil {
			return sdk.Unavailable(c.cfg.Name + ": bad response")
		}
	}
	return nil
}

// wait reserves the next start time and sleeps until it. The lock covers only the reservation, so requests that are already running are not held.
func (c *Client) wait(ctx context.Context) error {
	if c.cfg.MinInterval <= 0 {
		return nil
	}
	c.mu.Lock()
	at := time.Now()
	if c.next.After(at) {
		at = c.next
	}
	c.next = at.Add(c.cfg.MinInterval)
	c.mu.Unlock()
	if d := time.Until(at); d > 0 {
		t := time.NewTimer(d)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		}
	}
	return nil
}

func (c *Client) retryAfter(resp *http.Response) time.Duration {
	v := resp.Header.Get("Retry-After")
	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil { // the HTTP-date form
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return c.cfg.RetryAfter
}

func (c *Client) transportError(ctx context.Context, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err // url.Error carries the full URL, which may hold a key; keep only the cause
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return sdk.Unavailable(c.cfg.Name + ": " + err.Error())
}
