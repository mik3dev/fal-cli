package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mik3dev/fal-cli/internal/api"
	"github.com/mik3dev/fal-cli/internal/output"
)

func newClient(t *testing.T, url string) (*api.Client, *[]time.Duration) {
	t.Helper()
	c := api.New("k-secret", func(string) string { return "" })
	c.PlatformURL, c.QueueURL, c.StorageURL, c.SchemaURL = url, url, url, url
	var sleeps []time.Duration
	c.Sleep = func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }
	return c, &sleeps
}

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func apiErr(t *testing.T, err error) *api.Error {
	t.Helper()
	var ae *api.Error
	if !errors.As(err, &ae) {
		t.Fatalf("err = %T %v", err, err)
	}
	return ae
}

func TestAuthHeaderIsKeyPrefix(t *testing.T) {
	var auths []string
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"ok":1}`))
	})
	c, _ := newClient(t, srv.URL)
	if _, err := c.Do(context.Background(), api.Request{Method: "GET", Path: "/a", Host: api.HostPlatform}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(context.Background(), api.Request{Method: "GET", Path: "/b", Host: api.HostSchema}, nil); err != nil {
		t.Fatal(err)
	}
	if auths[0] != "Key k-secret" || auths[1] != "" {
		t.Errorf("auths = %q", auths)
	}
}

func TestDoDecodesBody(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"request_id":"r1"}`))
	})
	c, _ := newClient(t, srv.URL)
	var out struct {
		RequestID string `json:"request_id"`
	}
	resp, err := c.Do(context.Background(), api.Request{Method: "POST", Path: "/x", Host: api.HostQueue, Body: map[string]any{"prompt": "a"}}, &out)
	if err != nil || out.RequestID != "r1" || resp.Status != 200 {
		t.Fatalf("out=%+v resp=%+v err=%v", out, resp, err)
	}
}

func TestPlatformErrorDecodes(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"type":"authorization_error","message":"API key authentication required"}}`))
	})
	c, _ := newClient(t, srv.URL)
	_, err := c.Do(context.Background(), api.Request{Method: "GET", Path: "/x", Host: api.HostPlatform}, nil)
	ae := apiErr(t, err)
	if ae.Status != 401 || ae.Type != "authorization_error" || ae.Message != "API key authentication required" {
		t.Fatalf("ae = %+v", ae)
	}
	oe := ae.AsOutput()
	if oe.Code != output.CodeAuth || oe.ExitCode() != 3 || oe.Details["type"] != "authorization_error" {
		t.Fatalf("oe = %+v", oe)
	}
}

func TestForbiddenIsAuth(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"detail":"This API key is not permitted to perform this action."}`))
	})
	c, _ := newClient(t, srv.URL)
	_, err := c.Do(context.Background(), api.Request{Method: "GET", Path: "/x", Host: api.HostPlatform}, nil)
	ae := apiErr(t, err)
	if ae.AsOutput().Code != output.CodeAuth || !strings.Contains(ae.Message, "not permitted") {
		t.Fatalf("ae = %+v", ae)
	}
}

func TestValidation422Problems(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"detail":[{"loc":["body","image_urls",0],"msg":"m","type":"missing","ctx":{"x":1}},{"loc":["body","prompt"],"msg":"p","type":"string_type"}]}`))
	})
	c, _ := newClient(t, srv.URL)
	_, err := c.Do(context.Background(), api.Request{Method: "POST", Path: "/x", Host: api.HostQueue, Body: map[string]any{}}, nil)
	ae := apiErr(t, err)
	if len(ae.Problems) != 2 || ae.Problems[0].Field != "image_urls[0]" || ae.Problems[0].Type != "missing" || ae.Problems[0].Ctx["x"] != float64(1) || ae.Problems[1].Field != "prompt" {
		t.Fatalf("problems = %+v", ae.Problems)
	}
	oe := ae.AsOutput()
	if oe.Code != output.CodeValidation || oe.ExitCode() != 2 {
		t.Fatalf("oe = %+v", oe)
	}
	if _, ok := oe.Details["problems"]; !ok {
		t.Fatalf("details = %v", oe.Details)
	}
}

func TestRunnerErrorType(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Fal-Error-Type", "runner_scheduling_failure")
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"detail":"x","error_type":"runner_scheduling_failure"}`))
	})
	c, _ := newClient(t, srv.URL)
	_, err := c.Do(context.Background(), api.Request{Method: "POST", Path: "/x", Host: api.HostQueue, Body: map[string]any{}}, nil)
	ae := apiErr(t, err)
	if ae.ErrorType != "runner_scheduling_failure" || ae.Message != "x" || ae.Status != 503 {
		t.Fatalf("ae = %+v", ae)
	}
	if oe := ae.AsOutput(); oe.Code != output.CodeAPI || oe.Details["errorType"] != "runner_scheduling_failure" {
		t.Fatalf("oe = %+v", oe)
	}
}

func TestBalanceMessageIsCredits(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"detail":"Insufficient balance"}`))
	})
	c, _ := newClient(t, srv.URL)
	_, err := c.Do(context.Background(), api.Request{Method: "POST", Path: "/x", Host: api.HostQueue, Body: map[string]any{}}, nil)
	if oe := apiErr(t, err).AsOutput(); oe.Code != output.CodeCredits || oe.ExitCode() != 4 {
		t.Fatalf("oe = %+v", oe)
	}
}

func TestNonJSONErrorUsesStatusText(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte(`<html>bad gateway</html>`))
	})
	c, _ := newClient(t, srv.URL)
	_, err := c.Do(context.Background(), api.Request{Method: "POST", Path: "/x", Host: api.HostQueue, Body: map[string]any{}}, nil)
	if ae := apiErr(t, err); ae.Status != 502 || ae.Message != "Bad Gateway" {
		t.Fatalf("ae = %+v", ae)
	}
}

func TestRateLimited(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":{"type":"rate_limited","message":"slow down"}}`))
	})
	c, _ := newClient(t, srv.URL)
	_, err := c.Do(context.Background(), api.Request{Method: "POST", Path: "/x", Host: api.HostQueue, Body: map[string]any{}}, nil)
	if oe := apiErr(t, err).AsOutput(); oe.Code != output.CodeRateLimit || oe.ExitCode() != 7 {
		t.Fatalf("oe = %+v", oe)
	}
}

func TestGetRetries429ThenOK(t *testing.T) {
	var n atomic.Int32
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"type":"rate_limited","message":"x"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":1}`))
	})
	c, sleeps := newClient(t, srv.URL)
	if _, err := c.Do(context.Background(), api.Request{Method: "GET", Path: "/x", Host: api.HostQueue}, nil); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 3 || len(*sleeps) != 2 || (*sleeps)[0] != time.Second || (*sleeps)[1] != 2*time.Second {
		t.Fatalf("n=%d sleeps=%v", n.Load(), *sleeps)
	}
}

func TestGetRetries5xx(t *testing.T) {
	var n atomic.Int32
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	c, _ := newClient(t, srv.URL)
	if _, err := c.Do(context.Background(), api.Request{Method: "GET", Path: "/x", Host: api.HostQueue}, nil); err != nil || n.Load() != 2 {
		t.Fatalf("n=%d err=%v", n.Load(), err)
	}
}

func TestPostNoRetryOn500(t *testing.T) {
	var n atomic.Int32
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"detail":"boom","error_type":"internal_error"}`))
	})
	c, _ := newClient(t, srv.URL)
	_, err := c.Do(context.Background(), api.Request{Method: "POST", Path: "/x", Host: api.HostQueue, Body: map[string]any{}}, nil)
	if ae := apiErr(t, err); ae.Status != 500 || n.Load() != 1 {
		t.Fatalf("n=%d ae=%+v", n.Load(), ae)
	}
}

func TestPostRetries429(t *testing.T) {
	var n atomic.Int32
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(429)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	c, _ := newClient(t, srv.URL)
	if _, err := c.Do(context.Background(), api.Request{Method: "POST", Path: "/x", Host: api.HostQueue, Body: map[string]any{}}, nil); err != nil || n.Load() != 2 {
		t.Fatalf("n=%d err=%v", n.Load(), err)
	}
}

func TestPostRetryFlag(t *testing.T) {
	var n atomic.Int32
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"status":"CANCELLATION_REQUESTED"}`))
	})
	c, _ := newClient(t, srv.URL)
	resp, err := c.Do(context.Background(), api.Request{Method: "PUT", Path: "/x", Host: api.HostQueue, Retry: true}, nil)
	if err != nil || n.Load() != 2 || resp.Status != 202 {
		t.Fatalf("n=%d resp=%+v err=%v", n.Load(), resp, err)
	}
}

func TestRetriesCapAtThree(t *testing.T) {
	var n atomic.Int32
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(429)
	})
	c, sleeps := newClient(t, srv.URL)
	_, err := c.Do(context.Background(), api.Request{Method: "GET", Path: "/x", Host: api.HostQueue}, nil)
	if err == nil || n.Load() != 4 || len(*sleeps) != 3 || (*sleeps)[2] != 4*time.Second {
		t.Fatalf("n=%d sleeps=%v err=%v", n.Load(), *sleeps, err)
	}
}

func TestConnectionRefusedIsNetErrorNotSent(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, sleeps := newClient(t, url)
	_, err := c.Do(context.Background(), api.Request{Method: "POST", Path: "/x", Host: api.HostQueue, Body: map[string]any{}}, nil)
	var ne *api.NetError
	if !errors.As(err, &ne) || ne.Sent {
		t.Fatalf("err = %T %v", err, err)
	}
	if len(*sleeps) != 3 {
		t.Errorf("unsent POST should retry: sleeps=%v", *sleeps)
	}
}

func TestNoKeyLeak(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"detail":"bad key k-secret"}`))
	})
	c, _ := newClient(t, srv.URL)
	var dbg bytes.Buffer
	c.Debug = &dbg
	_, err := c.Do(context.Background(), api.Request{Method: "GET", Path: "/x", Host: api.HostPlatform}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "k-secret") || strings.Contains(dbg.String(), "k-secret") {
		t.Fatalf("leak: err=%q dbg=%q", err.Error(), dbg.String())
	}
	if !strings.Contains(dbg.String(), "GET ") || !strings.Contains(dbg.String(), "401") {
		t.Errorf("debug = %q", dbg.String())
	}
}

func TestPlatformLimiterSpacing(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	c, sleeps := newClient(t, srv.URL)
	now := time.Unix(1000, 0)
	c.Now = func() time.Time { return now }
	for i := 0; i < 2; i++ {
		if _, err := c.Do(context.Background(), api.Request{Method: "GET", Path: "/p", Host: api.HostPlatform}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(*sleeps) != 1 || (*sleeps)[0] < 500*time.Millisecond {
		t.Fatalf("platform sleeps = %v", *sleeps)
	}
	*sleeps = nil
	for i := 0; i < 2; i++ {
		if _, err := c.Do(context.Background(), api.Request{Method: "GET", Path: "/q", Host: api.HostQueue}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(*sleeps) != 0 {
		t.Fatalf("queue sleeps = %v", *sleeps)
	}
}

func TestQueryAndSlashInPath(t *testing.T) {
	var got string
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.String()
		_, _ = w.Write([]byte(`{}`))
	})
	c, _ := newClient(t, srv.URL)
	q := map[string][]string{"endpoint_id": {"fal-ai/flux/schnell"}}
	if _, err := c.Do(context.Background(), api.Request{Method: "GET", Path: "/fal-ai/flux/schnell/requests/r1", Host: api.HostQueue, Query: q}, nil); err != nil {
		t.Fatal(err)
	}
	if got != "/fal-ai/flux/schnell/requests/r1?endpoint_id=fal-ai%2Fflux%2Fschnell" {
		t.Errorf("url = %q", got)
	}
}

func TestEnvOverridesHosts(t *testing.T) {
	env := map[string]string{"FAL_PLATFORM_URL": "http://p/", "FAL_QUEUE_URL": "http://q", "FAL_STORAGE_URL": "http://s", "FAL_SCHEMA_URL": "http://sc"}
	c := api.New("k", func(k string) string { return env[k] })
	if c.PlatformURL != "http://p" || c.QueueURL != "http://q" || c.StorageURL != "http://s" || c.SchemaURL != "http://sc" {
		t.Fatalf("c = %+v", c)
	}
	d := api.New("k", func(string) string { return "" })
	if d.PlatformURL != "https://api.fal.ai" || d.QueueURL != "https://queue.fal.run" || d.StorageURL != "https://rest.alpha.fal.ai" || d.SchemaURL != "https://fal.ai" {
		t.Fatalf("defaults = %+v", d)
	}
}

func TestNoKeyLeakInProblemsAndErrorType(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"detail":[{"loc":["body","k-secret"],"msg":"bad k-secret","type":"k-secret","ctx":{"v":"k-secret"}}],"error_type":"k-secret"}`))
	})
	c, _ := newClient(t, srv.URL)
	_, err := c.Do(context.Background(), api.Request{Method: "POST", Path: "/x", Host: api.HostQueue, Body: map[string]any{}}, nil)
	ae := apiErr(t, err)
	b, _ := json.Marshal(ae.AsOutput())
	if strings.Contains(string(b), "k-secret") || strings.Contains(string(ae.Raw), "k-secret") {
		t.Fatalf("leak: %s", b)
	}
}
