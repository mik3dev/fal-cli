// Package task drives the fal queue: submit, status, result and cancel.
package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/mik3dev/fal-cli/internal/api"
	"github.com/mik3dev/fal-cli/internal/ledger"
	"github.com/mik3dev/fal-cli/internal/output"
)

// Queue states.
const (
	StateInQueue    = "IN_QUEUE"
	StateInProgress = "IN_PROGRESS"
	StateCompleted  = "COMPLETED"
)

// Ledger states.
const (
	LedgerSubmitted = "submitted"
	LedgerUnknown   = "unknown"
	LedgerSuccess   = "success"
	LedgerFail      = "fail"
	LedgerCancelled = "cancelled"
)

const (
	historyLimit = 5
	// skewTolerance absorbs a local clock running ahead of the platform.
	skewTolerance = 30 * time.Second
)

// Status is one poll of a queued request.
type Status struct {
	RequestID     string   `json:"requestId"`
	State         string   `json:"state"`
	QueuePosition int      `json:"queuePosition,omitempty"`
	Error         string   `json:"error,omitempty"`
	ErrorType     string   `json:"errorType,omitempty"`
	InferenceTime float64  `json:"inferenceTime,omitempty"`
	Logs          []string `json:"logs,omitempty"`
}

func reqPath(endpoint, id, suffix string) string {
	return "/" + endpoint + "/requests/" + id + suffix
}

// Submit POSTs input to the queue and records the request in the ledger.
// Rejections (4xx) leave no ledger entry. A failure after the request was
// sent (network, 5xx) is resolved through the request history; when that
// fails the result is submit_uncertain and a ledger line with State unknown.
func Submit(ctx context.Context, c *api.Client, ledgerPath, endpoint string, input map[string]any, outDir string, now time.Time, warn io.Writer) (string, error) {
	var res struct {
		RequestID string `json:"request_id"`
	}
	_, err := c.Do(ctx, api.Request{Method: http.MethodPost, Path: "/" + endpoint, Body: input, Host: api.HostQueue}, &res)
	switch {
	case err == nil && res.RequestID != "":
		record(ledgerPath, ledger.Entry{RequestID: res.RequestID, Endpoint: endpoint, State: LedgerSubmitted, OutDir: outDir, CreatedAt: now}, warn)
		return res.RequestID, nil
	case err == nil:
		err = &api.Error{Status: 200, Message: "submit response lacks request_id"}
	case !sentOrServerError(err):
		return "", err
	}
	if id, ok := recoverUncertain(ctx, c, endpoint, now); ok {
		record(ledgerPath, ledger.Entry{RequestID: id, Endpoint: endpoint, State: LedgerSubmitted, OutDir: outDir, CreatedAt: now, Note: "recovered from request history after: " + err.Error()}, warn)
		return id, nil
	}
	record(ledgerPath, ledger.Entry{Endpoint: endpoint, State: LedgerUnknown, OutDir: outDir, CreatedAt: now, Note: err.Error()}, warn)
	return "", &output.Error{
		Code:    output.CodeSubmitUncertain,
		Message: "request may have been created; check https://fal.ai/dashboard/requests before resubmitting",
		Details: map[string]any{"endpoint": endpoint, "cause": err.Error()},
	}
}

// sentOrServerError is true when the request may have reached the queue.
func sentOrServerError(err error) bool {
	var ne *api.NetError
	if errors.As(err, &ne) {
		return ne.Sent
	}
	var ae *api.Error
	return errors.As(err, &ae) && ae.Status >= 500
}

// record appends a ledger line; a write failure only warns (the request is already in flight).
func record(path string, e ledger.Entry, warn io.Writer) {
	if err := ledger.Append(path, e); err != nil && warn != nil {
		_, _ = fmt.Fprintf(warn, "warning: could not record request %s in the ledger: %v\n", e.RequestID, err)
	}
}

// recoverUncertain adopts the single request of endpoint sent at or after
// since minus skewTolerance, per the platform history. Query errors mean "not recovered".
func recoverUncertain(ctx context.Context, c *api.Client, endpoint string, since time.Time) (string, bool) {
	since = since.Add(-skewTolerance)
	var res struct {
		Items []struct {
			RequestID string `json:"request_id"`
			SentAt    string `json:"sent_at"`
		} `json:"items"`
	}
	q := url.Values{"endpoint_id": {endpoint}, "start": {since.UTC().Format(time.RFC3339)}, "limit": {strconv.Itoa(historyLimit)}}
	if _, err := c.Do(ctx, api.Request{Method: http.MethodGet, Path: "/v1/models/requests/by-endpoint", Query: q, Host: api.HostPlatform}, &res); err != nil {
		return "", false
	}
	var found []string
	for _, it := range res.Items {
		sent, err := time.Parse(time.RFC3339Nano, it.SentAt)
		if err != nil || sent.Before(since) {
			continue
		}
		found = append(found, it.RequestID)
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

type rawStatus struct {
	Status        string `json:"status"`
	QueuePosition int    `json:"queue_position"`
	Error         any    `json:"error"`
	ErrorType     string `json:"error_type"`
	Metrics       struct {
		InferenceTime float64 `json:"inference_time"`
	} `json:"metrics"`
	Logs []struct {
		Message string `json:"message"`
	} `json:"logs"`
}

// GetStatus polls one request (with logs).
func GetStatus(ctx context.Context, c *api.Client, endpoint, id string) (Status, error) {
	var raw rawStatus
	req := api.Request{Method: http.MethodGet, Path: reqPath(endpoint, id, "/status"), Query: url.Values{"logs": {"1"}}, Host: api.HostQueue}
	if _, err := c.Do(ctx, req, &raw); err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && ae.IsNotFound() {
			return Status{}, &output.Error{Code: output.CodeTaskFailed, Message: fmt.Sprintf("request %s not found or expired", id), Details: map[string]any{"requestId": id}}
		}
		return Status{}, err
	}
	s := Status{RequestID: id, State: raw.Status, QueuePosition: raw.QueuePosition, ErrorType: raw.ErrorType, InferenceTime: raw.Metrics.InferenceTime}
	s.Error = errorText(raw.Error)
	for _, l := range raw.Logs {
		s.Logs = append(s.Logs, l.Message)
	}
	return s, nil
}

// errorText renders the status error, which is a string or an object with a message.
func errorText(v any) string {
	switch e := v.(type) {
	case nil:
		return ""
	case string:
		return e
	case map[string]any:
		if m, ok := e["message"].(string); ok {
			return m
		}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// GetResult fetches the final payload. Gone results and model failures are task_failed.
// A 2xx whose body does not decode stays an api error (retryable).
func GetResult(ctx context.Context, c *api.Client, endpoint, id string) (json.RawMessage, error) {
	var raw json.RawMessage
	if _, err := c.Do(ctx, api.Request{Method: http.MethodGet, Path: reqPath(endpoint, id, ""), Host: api.HostQueue}, &raw); err != nil {
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Status < 400 || ae.Status == 401 || ae.Status == 403 || ae.Status == 429 {
			return nil, err
		}
		details := map[string]any{"requestId": id, "status": ae.Status}
		if ae.ErrorType != "" {
			details["errorType"] = ae.ErrorType
		}
		if len(ae.Problems) > 0 {
			details["problems"] = ae.Problems
		}
		if ae.Status == http.StatusNotFound || ae.Status == http.StatusGone {
			return nil, &output.Error{Code: output.CodeTaskFailed, Message: fmt.Sprintf("request %s: result expired or not found", id), Details: details}
		}
		return nil, &output.Error{Code: output.CodeTaskFailed, Message: fmt.Sprintf("request %s failed: %s", id, ae.Message), Details: details}
	}
	return raw, nil
}

// Cancel asks the queue to drop a request. Returns CANCELLATION_REQUESTED or ALREADY_COMPLETED.
func Cancel(ctx context.Context, c *api.Client, endpoint, id string) (string, error) {
	_, err := c.Do(ctx, api.Request{Method: http.MethodPut, Path: reqPath(endpoint, id, "/cancel"), Host: api.HostQueue, Retry: true}, nil)
	var ae *api.Error
	switch {
	case err == nil:
		return "CANCELLATION_REQUESTED", nil
	case errors.As(err, &ae) && ae.Status == http.StatusBadRequest:
		return "ALREADY_COMPLETED", nil
	case errors.As(err, &ae) && ae.IsNotFound():
		return "", &output.Error{Code: output.CodeTaskFailed, Message: fmt.Sprintf("request %s not found or expired", id), Details: map[string]any{"requestId": id}}
	}
	return "", err
}
