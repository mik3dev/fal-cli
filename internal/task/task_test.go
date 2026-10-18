package task_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mik3dev/fal-cli/internal/api"
	"github.com/mik3dev/fal-cli/internal/ledger"
	"github.com/mik3dev/fal-cli/internal/output"
	"github.com/mik3dev/fal-cli/internal/task"
)

const ep = "fal-ai/flux/schnell"

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// queue is a scripted fal queue + platform history server.
type queue struct {
	mu       sync.Mutex
	submit   []func(w http.ResponseWriter) // consumed in order; last one repeats
	statuses []string                      // JSON bodies for GET status, consumed in order; last repeats
	result   func(w http.ResponseWriter)
	cancel   func(w http.ResponseWriter)
	history  string
	calls    []string
}

func (q *queue) pop(list *[]string) string {
	if len(*list) > 1 {
		s := (*list)[0]
		*list = (*list)[1:]
		return s
	}
	return (*list)[0]
}

func (q *queue) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.calls = append(q.calls, r.Method+" "+r.URL.RequestURI())
	switch {
	case r.URL.Path == "/v1/models/requests/by-endpoint":
		_, _ = w.Write([]byte(q.history))
	case r.Method == "POST" && r.URL.Path == "/"+ep:
		f := q.submit[0]
		if len(q.submit) > 1 {
			q.submit = q.submit[1:]
		}
		f(w)
	case strings.HasSuffix(r.URL.Path, "/status"):
		_, _ = w.Write([]byte(q.pop(&q.statuses)))
	case strings.HasSuffix(r.URL.Path, "/cancel"):
		q.cancel(w)
	case strings.HasPrefix(r.URL.Path, "/"+ep+"/requests/"):
		q.result(w)
	default:
		w.WriteHeader(404)
	}
}

func ok(body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { _, _ = w.Write([]byte(body)) }
}

func code(status int, body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) { w.WriteHeader(status); _, _ = w.Write([]byte(body)) }
}

func setup(t *testing.T, q *queue) (*api.Client, string) {
	t.Helper()
	srv := httptest.NewServer(q)
	t.Cleanup(srv.Close)
	c := api.New("k", func(string) string { return "" })
	c.QueueURL, c.PlatformURL = srv.URL, srv.URL
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return c, filepath.Join(t.TempDir(), "tasks.jsonl")
}

const submitted = `{"request_id":"req-1","status_url":"x","response_url":"y","cancel_url":"z","queue_position":0}`

func asOut(t *testing.T, err error) *output.Error {
	t.Helper()
	var oe *output.Error
	if errors.As(err, &oe) {
		return oe
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae.AsOutput()
	}
	t.Fatalf("not an output/api error: %v", err)
	return nil
}

func TestSubmitReturnsRequestIDAndLedgers(t *testing.T) {
	q := &queue{submit: []func(http.ResponseWriter){ok(submitted)}}
	c, lp := setup(t, q)
	id, err := task.Submit(context.Background(), c, lp, ep, map[string]any{"prompt": "cat"}, "/out", t0, nil)
	if err != nil || id != "req-1" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	e, found := ledger.Find(lp, "req-1")
	if !found || e.State != "submitted" || e.Endpoint != ep || e.OutDir != "/out" || !e.CreatedAt.Equal(t0) {
		t.Fatalf("ledger = %+v found=%v", e, found)
	}
}

func TestSubmit422IsValidationNoLedger(t *testing.T) {
	q := &queue{submit: []func(http.ResponseWriter){code(422, `{"detail":[{"loc":["body","prompt"],"msg":"field required","type":"missing"}]}`)}}
	c, lp := setup(t, q)
	_, err := task.Submit(context.Background(), c, lp, ep, map[string]any{}, "", t0, nil)
	oe := asOut(t, err)
	if oe.Code != output.CodeValidation {
		t.Fatalf("err = %+v", oe)
	}
	if l, _ := ledger.List(lp, 0, nil); len(l) != 0 {
		t.Fatalf("ledger not empty: %+v", l)
	}
}

func TestSubmit5xxAfterSendIsUncertainWhenHistoryEmpty(t *testing.T) {
	q := &queue{submit: []func(http.ResponseWriter){code(502, "bad gateway")}, history: `{"items":[],"next_cursor":null,"has_more":false}`}
	c, lp := setup(t, q)
	_, err := task.Submit(context.Background(), c, lp, ep, map[string]any{"prompt": "cat"}, "", t0, nil)
	oe := asOut(t, err)
	if oe.Code != output.CodeSubmitUncertain || oe.ExitCode() != 8 || !strings.Contains(oe.Message, "fal.ai/dashboard/requests") || oe.Details["endpoint"] != ep {
		t.Fatalf("err = %+v", oe)
	}
	l, _ := ledger.List(lp, 0, nil)
	if len(l) != 1 || l[0].State != "unknown" || l[0].RequestID != "" {
		t.Fatalf("ledger = %+v", l)
	}
	joined := strings.Join(q.calls, "\n")
	if !strings.Contains(joined, "GET /v1/models/requests/by-endpoint?endpoint_id=fal-ai%2Fflux%2Fschnell&limit=5&start=2026-10-18T12%3A00%3A00Z") {
		t.Fatalf("history not queried: %s", joined)
	}
}

func TestSubmit5xxRecoveredFromHistory(t *testing.T) {
	q := &queue{submit: []func(http.ResponseWriter){code(500, "boom")}, history: `{"items":[{"request_id":"hist-1","endpoint_id":"fal-ai/flux/schnell","sent_at":"2026-10-18T12:00:01Z"},{"request_id":"old","endpoint_id":"fal-ai/flux/schnell","sent_at":"2026-10-18T11:59:00Z"}]}`}
	c, lp := setup(t, q)
	id, err := task.Submit(context.Background(), c, lp, ep, map[string]any{"prompt": "cat"}, "", t0, nil)
	if err != nil || id != "hist-1" {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if e, found := ledger.Find(lp, "hist-1"); !found || e.State != "submitted" {
		t.Fatalf("ledger = %+v", e)
	}
}

func TestSubmitHistoryAmbiguousStaysUncertain(t *testing.T) {
	q := &queue{submit: []func(http.ResponseWriter){code(500, "boom")}, history: `{"items":[{"request_id":"a","sent_at":"2026-10-18T12:00:01Z"},{"request_id":"b","sent_at":"2026-10-18T12:00:02Z"}]}`}
	c, lp := setup(t, q)
	_, err := task.Submit(context.Background(), c, lp, ep, map[string]any{"prompt": "cat"}, "", t0, nil)
	if asOut(t, err).Code != output.CodeSubmitUncertain {
		t.Fatalf("err = %v", err)
	}
}

func TestSubmitNetErrorBeforeSendIsPlainAPIError(t *testing.T) {
	c := api.New("k", func(string) string { return "" })
	c.QueueURL = "http://127.0.0.1:1"
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	lp := filepath.Join(t.TempDir(), "tasks.jsonl")
	_, err := task.Submit(context.Background(), c, lp, ep, map[string]any{}, "", t0, nil)
	var ne *api.NetError
	if !errors.As(err, &ne) || ne.Sent {
		t.Fatalf("err = %v", err)
	}
	if l, _ := ledger.List(lp, 0, nil); len(l) != 0 {
		t.Fatalf("ledger = %+v", l)
	}
}

func st(state string, extra string) string {
	if extra != "" {
		extra = "," + extra
	}
	return fmt.Sprintf(`{"status":"%s","response_url":"r","status_url":"s","cancel_url":"c"%s}`, state, extra)
}

type clock struct {
	now    time.Time
	sleeps []time.Duration
}

func (k *clock) waiter() task.Waiter {
	return task.Waiter{
		Now: func() time.Time { return k.now },
		Sleep: func(_ context.Context, d time.Duration) error {
			k.sleeps = append(k.sleeps, d)
			k.now = k.now.Add(d)
			return nil
		},
	}
}

func TestWaitInQueueThenInProgressThenCompleted(t *testing.T) {
	q := &queue{statuses: []string{st("IN_QUEUE", `"queue_position":3`), st("IN_PROGRESS", `"logs":[{"message":"hi"}]`), st("COMPLETED", `"metrics":{"inference_time":1.5}`)}}
	c, _ := setup(t, q)
	k := &clock{now: t0}
	var progress strings.Builder
	w := k.waiter()
	w.Progress = &progress
	s, err := w.Wait(context.Background(), c, ep, "req-1", time.Minute)
	if err != nil || s.State != "COMPLETED" || s.InferenceTime != 1.5 || s.RequestID != "req-1" {
		t.Fatalf("s=%+v err=%v", s, err)
	}
	if len(k.sleeps) != 2 || k.sleeps[0] != 2*time.Second || k.sleeps[1] != 3*time.Second {
		t.Fatalf("sleeps = %v", k.sleeps)
	}
	if p := progress.String(); !strings.Contains(p, "state: IN_QUEUE (position 3)\n") || !strings.Contains(p, "state: IN_PROGRESS\n") || !strings.Contains(p, "state: COMPLETED\n") {
		t.Fatalf("progress = %q", p)
	}
	if !strings.Contains(q.calls[0], "/requests/req-1/status?logs=1") {
		t.Fatalf("calls = %v", q.calls)
	}
}

func TestWaitCompletedWithErrorIsTaskFailed(t *testing.T) {
	q := &queue{statuses: []string{st("COMPLETED", `"error":"content policy violation","error_type":"content_policy_violation"`)}}
	c, _ := setup(t, q)
	k := &clock{now: t0}
	s, err := k.waiter().Wait(context.Background(), c, ep, "req-1", time.Minute)
	oe := asOut(t, err)
	if oe.Code != output.CodeTaskFailed || !strings.Contains(oe.Message, "req-1 failed: content policy violation") || oe.Details["errorType"] != "content_policy_violation" || oe.Details["requestId"] != "req-1" {
		t.Fatalf("s=%+v err=%+v", s, oe)
	}
}

func TestWaitTimeoutKeepsRequestID(t *testing.T) {
	q := &queue{statuses: []string{st("IN_PROGRESS", "")}}
	c, _ := setup(t, q)
	k := &clock{now: t0}
	s, err := k.waiter().Wait(context.Background(), c, ep, "req-1", 7*time.Second)
	oe := asOut(t, err)
	if oe.Code != output.CodeTimeout || oe.Details["requestId"] != "req-1" || !strings.Contains(oe.Message, "fal-cli task wait req-1") || s.State != "IN_PROGRESS" {
		t.Fatalf("s=%+v err=%+v", s, oe)
	}
	// 2s + 3s + min(4.5s, 2s remaining) then deadline.
	if len(k.sleeps) != 3 || k.sleeps[2] != 2*time.Second {
		t.Fatalf("sleeps = %v", k.sleeps)
	}
}

func TestWaitCtxCancelIsTimeout(t *testing.T) {
	q := &queue{statuses: []string{st("IN_QUEUE", "")}}
	c, _ := setup(t, q)
	ctx, cancel := context.WithCancel(context.Background())
	w := task.Waiter{Now: func() time.Time { return t0 }, Sleep: func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }}
	_, err := w.Wait(ctx, c, ep, "req-1", time.Minute)
	if asOut(t, err).Code != output.CodeTimeout {
		t.Fatalf("err = %v", err)
	}
}

func TestGetStatusNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"detail":"Request not found"}`))
	}))
	t.Cleanup(srv.Close)
	c := api.New("k", func(string) string { return "" })
	c.QueueURL = srv.URL
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	_, err := task.GetStatus(context.Background(), c, ep, "req-9")
	oe := asOut(t, err)
	if oe.Code != output.CodeTaskFailed || !strings.Contains(oe.Message, "req-9 not found or expired") {
		t.Fatalf("err = %+v", oe)
	}
}

func TestGetResultExpired(t *testing.T) {
	q := &queue{result: code(404, `{"detail":"Request not found"}`)}
	c, _ := setup(t, q)
	_, err := task.GetResult(context.Background(), c, ep, "req-1")
	oe := asOut(t, err)
	if oe.Code != output.CodeTaskFailed || !strings.Contains(oe.Message, "result expired or not found") {
		t.Fatalf("err = %+v", oe)
	}
}

func TestGetResultFailureCarriesErrorType(t *testing.T) {
	q := &queue{result: func(w http.ResponseWriter) {
		w.Header().Set("X-Fal-Error-Type", "runner_error")
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"detail":"model crashed","error_type":"runner_error"}`))
	}}
	c, _ := setup(t, q)
	_, err := task.GetResult(context.Background(), c, ep, "req-1")
	oe := asOut(t, err)
	if oe.Code != output.CodeTaskFailed || oe.Details["errorType"] != "runner_error" || !strings.Contains(oe.Message, "model crashed") {
		t.Fatalf("err = %+v", oe)
	}
}

func TestGetResultOK(t *testing.T) {
	q := &queue{result: ok(`{"images":[{"url":"https://x/y.png"}]}`)}
	c, _ := setup(t, q)
	raw, err := task.GetResult(context.Background(), c, ep, "req-1")
	if err != nil || !json.Valid(raw) || !strings.Contains(string(raw), "y.png") {
		t.Fatalf("raw=%s err=%v", raw, err)
	}
}

func TestCancelAccepted(t *testing.T) {
	q := &queue{cancel: code(202, `{"status":"CANCELLATION_REQUESTED"}`)}
	c, _ := setup(t, q)
	s, err := task.Cancel(context.Background(), c, ep, "req-1")
	if err != nil || s != "CANCELLATION_REQUESTED" || !strings.HasPrefix(q.calls[0], "PUT /fal-ai/flux/schnell/requests/req-1/cancel") {
		t.Fatalf("s=%q err=%v calls=%v", s, err, q.calls)
	}
}

func TestCancelAlreadyCompleted(t *testing.T) {
	q := &queue{cancel: code(400, `{"status":"ALREADY_COMPLETED"}`)}
	c, _ := setup(t, q)
	s, err := task.Cancel(context.Background(), c, ep, "req-1")
	if err != nil || s != "ALREADY_COMPLETED" {
		t.Fatalf("s=%q err=%v", s, err)
	}
	q2 := &queue{cancel: code(404, `{"detail":"nope"}`)}
	c2, _ := setup(t, q2)
	if _, err := task.Cancel(context.Background(), c2, ep, "req-1"); asOut(t, err).Code != output.CodeTaskFailed {
		t.Fatalf("404 err = %v", err)
	}
}
