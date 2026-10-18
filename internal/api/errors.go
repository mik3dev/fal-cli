// Package api is the fal.ai HTTP client: three hosts, typed errors, safe retries and a platform limiter.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mik3dev/fal-cli/internal/output"
)

// Problem is one entry of a 422 "detail" array.
type Problem struct {
	Field   string         `json:"field"`
	Message string         `json:"message"`
	Type    string         `json:"type,omitempty"`
	Ctx     map[string]any `json:"ctx,omitempty"`
}

// Error is a non-2xx response from any fal host.
type Error struct {
	Status    int
	Type      string // platform error type (authorization_error, not_found, rate_limited...)
	Message   string
	Problems  []Problem // model-level 422 details
	ErrorType string    // request-level error_type (runner_..., request_timeout...)
	Raw       json.RawMessage
}

func (e *Error) Error() string {
	msg := e.Message
	if msg == "" && len(e.Problems) > 0 {
		msg = e.Problems[0].Field + ": " + e.Problems[0].Message
	}
	return fmt.Sprintf("fal.ai error %d: %s", e.Status, msg)
}

// IsNotFound reports a 404 from any host.
func (e *Error) IsNotFound() bool { return e.Status == http.StatusNotFound }

// AsOutput maps the HTTP status and body to the CLI output contract.
func (e *Error) AsOutput() *output.Error {
	code := output.CodeAPI
	lower := strings.ToLower(e.Message)
	switch {
	case e.Status == 401 || e.Status == 403 || e.Type == "authorization_error":
		code = output.CodeAuth
		if strings.Contains(lower, "balance") || strings.Contains(lower, "credit") || strings.Contains(lower, "locked") {
			code = output.CodeCredits
		}
	case e.Status == 422:
		code = output.CodeValidation
	case e.Status == 429 || e.Type == "rate_limited":
		code = output.CodeRateLimit
	}
	details := map[string]any{"status": e.Status}
	if e.Type != "" {
		details["type"] = e.Type
	}
	if e.ErrorType != "" {
		details["errorType"] = e.ErrorType
	}
	if len(e.Problems) > 0 {
		details["problems"] = e.Problems
	}
	return &output.Error{Code: code, Message: e.Error(), Details: details}
}

// NetError is a transport failure. Sent=false means the request provably never
// reached the server (safe to retry); when in doubt Sent is true.
type NetError struct {
	Sent bool
	Err  error
}

func (e *NetError) Error() string { return "network error: " + e.Err.Error() }
func (e *NetError) Unwrap() error { return e.Err }

// decodeError builds an *Error from a non-2xx body. It understands the three
// fal shapes: {"error":{type,message}}, {"detail":[...]} and {"detail":"...","error_type":"..."}.
func decodeError(status int, header http.Header, raw []byte) *Error {
	e := &Error{Status: status, Raw: json.RawMessage(raw), ErrorType: header.Get("X-Fal-Error-Type")}
	var body struct {
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Detail    json.RawMessage `json:"detail"`
		ErrorType string          `json:"error_type"`
	}
	if json.Unmarshal(raw, &body) != nil {
		e.Message = http.StatusText(status)
		return e
	}
	if body.ErrorType != "" {
		e.ErrorType = body.ErrorType
	}
	if body.Error != nil {
		e.Type, e.Message = body.Error.Type, body.Error.Message
	}
	if len(body.Detail) > 0 {
		var s string
		if json.Unmarshal(body.Detail, &s) == nil {
			e.Message = s
		} else {
			e.Problems = parseProblems(body.Detail)
		}
	}
	if e.Message == "" && len(e.Problems) == 0 {
		e.Message = http.StatusText(status)
	}
	return e
}

func parseProblems(raw json.RawMessage) []Problem {
	var items []struct {
		Loc  []any          `json:"loc"`
		Msg  string         `json:"msg"`
		Type string         `json:"type"`
		Ctx  map[string]any `json:"ctx"`
	}
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	out := make([]Problem, 0, len(items))
	for _, it := range items {
		out = append(out, Problem{Field: formatLoc(it.Loc), Message: it.Msg, Type: it.Type, Ctx: it.Ctx})
	}
	return out
}

// formatLoc turns ["body","image_urls",0] into "image_urls[0]".
func formatLoc(loc []any) string {
	if len(loc) > 0 && loc[0] == "body" {
		loc = loc[1:]
	}
	var b strings.Builder
	for i, part := range loc {
		switch p := part.(type) {
		case float64:
			fmt.Fprintf(&b, "[%d]", int(p))
		default:
			if i > 0 {
				b.WriteByte('.')
			}
			fmt.Fprint(&b, p)
		}
	}
	if b.Len() == 0 {
		return "body"
	}
	return b.String()
}
