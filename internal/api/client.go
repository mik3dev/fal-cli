package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mik3dev/fal-cli/internal/config"
)

const (
	defaultPlatformURL = "https://api.fal.ai"
	defaultQueueURL    = "https://queue.fal.run"
	defaultStorageURL  = "https://rest.alpha.fal.ai"
	defaultSchemaURL   = "https://fal.ai"
	maxRetries         = 3
	maxBody            = 32 << 20
	platformGap        = 500 * time.Millisecond
)

// Host selects which fal service a request goes to.
type Host int

const (
	HostPlatform Host = iota // api.fal.ai (catalog, pricing, billing, history)
	HostQueue                // queue.fal.run (submit, status, result, cancel)
	HostStorage              // rest.alpha.fal.ai (upload initiate)
	HostSchema               // fal.ai (public OpenAPI per endpoint, no auth)
)

// Request describes one API call.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	Body   any
	// BodyFactory, when set, replaces Body: it is called once per attempt to
	// open a fresh body, so retries can re-read the source.
	BodyFactory func() (contentType string, body io.ReadCloser, err error)
	Host        Host
	// Retry forces retries for a non-GET request (e.g. cancel, which is idempotent).
	Retry bool
	// NoAuth drops the Authorization header (HostSchema never sends it).
	NoAuth bool
}

// Response is the raw successful (2xx) reply.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Client talks to fal.ai. Safe for concurrent use.
type Client struct {
	PlatformURL, QueueURL, StorageURL, SchemaURL, Key string
	HTTP, UploadHTTP                                  *http.Client
	Debug                                             io.Writer
	Sleep                                             func(context.Context, time.Duration) error
	Now                                               func() time.Time

	mu       sync.Mutex
	nextSlot time.Time // platform/schema limiter
}

// New builds a client; env resolves FAL_*_URL overrides.
func New(key string, env func(string) string) *Client {
	pick := func(name, def string) string {
		if env != nil {
			if v := env(name); v != "" {
				return strings.TrimRight(v, "/")
			}
		}
		return def
	}
	return &Client{
		PlatformURL: pick("FAL_PLATFORM_URL", defaultPlatformURL),
		QueueURL:    pick("FAL_QUEUE_URL", defaultQueueURL),
		StorageURL:  pick("FAL_STORAGE_URL", defaultStorageURL),
		SchemaURL:   pick("FAL_SCHEMA_URL", defaultSchemaURL),
		Key:         key,
		HTTP:        &http.Client{Timeout: 60 * time.Second},
		UploadHTTP:  &http.Client{Timeout: 10 * time.Minute},
		Sleep:       sleepCtx,
		Now:         time.Now,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) nowFn() func() time.Time {
	if c.Now != nil {
		return c.Now
	}
	return time.Now
}

func (c *Client) sleepFn() func(context.Context, time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep
	}
	return sleepCtx
}

// Do performs the request with the limiter and safe retries. On 2xx it
// decodes the body into out (when out != nil) and returns the raw Response.
func (c *Client) Do(ctx context.Context, req Request, out any) (*Response, error) {
	var payload []byte
	if req.Body != nil {
		b, err := json.Marshal(req.Body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		payload = b
	}
	for attempt := 0; ; attempt++ {
		if err := c.limit(ctx, req.Host); err != nil {
			return nil, err
		}
		resp, err := c.once(ctx, req, payload)
		if err == nil || attempt >= maxRetries || ctx.Err() != nil || !retryable(req, err) {
			if err != nil {
				return nil, err
			}
			if out != nil && len(resp.Body) > 0 {
				if jerr := json.Unmarshal(resp.Body, out); jerr != nil {
					return resp, &Error{Status: resp.Status, Message: "unexpected response shape: " + jerr.Error(), Raw: resp.Body}
				}
			}
			return resp, nil
		}
		if serr := c.sleepFn()(ctx, time.Second<<attempt); serr != nil {
			return nil, err
		}
	}
}

// limit spaces platform/schema calls by platformGap.
// ponytail: in-process only; add a file lock if parallel processes hit 429.
func (c *Client) limit(ctx context.Context, h Host) error {
	if h != HostPlatform && h != HostSchema {
		return nil
	}
	c.mu.Lock()
	now := c.nowFn()()
	at := now
	if c.nextSlot.After(at) {
		at = c.nextSlot
	}
	c.nextSlot = at.Add(platformGap)
	c.mu.Unlock()
	if d := at.Sub(now); d > 0 {
		return c.sleepFn()(ctx, d)
	}
	return nil
}

// retryable: GET and Retry requests retry on 429, 5xx and any network error.
// Other requests (submit, upload) retry only on 429 or when the request
// provably never left (NetError.Sent==false): the request may already exist and cost money.
func retryable(r Request, err error) bool {
	idempotent := r.Method == http.MethodGet || r.Retry
	var ne *NetError
	if errors.As(err, &ne) {
		return idempotent || !ne.Sent
	}
	var ae *Error
	if errors.As(err, &ae) {
		if ae.Status == 429 {
			return true
		}
		return idempotent && ae.Status >= 500
	}
	return false
}

func (c *Client) base(h Host) (string, *http.Client) {
	switch h {
	case HostQueue:
		return c.QueueURL, c.HTTP
	case HostStorage:
		return c.StorageURL, c.UploadHTTP
	case HostSchema:
		return c.SchemaURL, c.HTTP
	default:
		return c.PlatformURL, c.HTTP
	}
}

func (c *Client) once(ctx context.Context, r Request, payload []byte) (*Response, error) {
	base, hc := c.base(r.Host)
	u := base + r.Path
	if len(r.Query) > 0 {
		u += "?" + r.Query.Encode()
	}
	var body io.Reader
	contentType := ""
	if payload != nil {
		body = bytes.NewReader(payload)
		contentType = "application/json"
	}
	if r.BodyFactory != nil {
		ct, rc, ferr := r.BodyFactory()
		if ferr != nil {
			return nil, fmt.Errorf("open request body: %w", ferr)
		}
		defer func() { _ = rc.Close() }()
		body, contentType = rc, ct
	}
	hreq, err := http.NewRequestWithContext(ctx, r.Method, u, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if !r.NoAuth && r.Host != HostSchema && c.Key != "" {
		hreq.Header.Set("Authorization", "Key "+c.Key)
	}
	hreq.Header.Set("Accept", "application/json")
	if contentType != "" {
		hreq.Header.Set("Content-Type", contentType)
	}
	var wroteHeaders atomic.Bool
	hreq = hreq.WithContext(httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteHeaders: func() { wroteHeaders.Store(true) },
	}))

	resp, err := hc.Do(hreq)
	if err != nil {
		c.debugf("%s %s -> error\n", r.Method, u)
		return nil, &NetError{Sent: wroteHeaders.Load(), Err: c.scrubErr(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	c.debugf("%s %s -> %d\n", r.Method, u, resp.StatusCode)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, &NetError{Sent: true, Err: c.scrubErr(err)}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		e := decodeError(resp.StatusCode, resp.Header, raw)
		e.Message = c.scrub(e.Message)
		return nil, e
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: raw}, nil
}

func (c *Client) debugf(format string, args ...any) {
	if c.Debug == nil {
		return
	}
	_, _ = fmt.Fprintf(c.Debug, "Authorization: Key ****\n"+format, args...)
}

func (c *Client) scrub(s string) string {
	if c.Key == "" {
		return s
	}
	return strings.ReplaceAll(s, c.Key, config.Redact(c.Key))
}

func (c *Client) scrubErr(err error) error {
	if c.Key == "" || !strings.Contains(err.Error(), c.Key) {
		return err
	}
	return errors.New(c.scrub(err.Error()))
}
