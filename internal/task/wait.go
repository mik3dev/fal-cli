package task

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mik3dev/fal-cli/internal/api"
	"github.com/mik3dev/fal-cli/internal/output"
)

const (
	pollStart  = 2 * time.Second
	pollMax    = 5 * time.Second
	pollFactor = 1.5
)

// Waiter polls a request until it completes. Zero fields get real-time defaults.
type Waiter struct {
	Sleep    func(context.Context, time.Duration) error
	Now      func() time.Time
	Progress io.Writer
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

// Wait polls with 2s..5s backoff (x1.5). A completed request carrying an
// error is task_failed; timeout or cancellation returns the last Status and
// a timeout error naming the request.
func (w Waiter) Wait(ctx context.Context, c *api.Client, endpoint, id string, timeout time.Duration) (Status, error) {
	sleep, now := w.Sleep, w.Now
	if sleep == nil {
		sleep = sleepCtx
	}
	if now == nil {
		now = time.Now
	}
	deadline := now().Add(timeout)
	delay := pollStart
	last := Status{RequestID: id}
	prev := ""
	for {
		s, err := GetStatus(ctx, c, endpoint, id)
		if err != nil {
			if ctx.Err() != nil {
				return last, timeoutErr(id)
			}
			return last, err
		}
		last = s
		if s.State != prev {
			prev = s.State
			w.report(s)
		}
		if s.State == StateCompleted {
			if s.Error != "" {
				return s, &output.Error{
					Code:    output.CodeTaskFailed,
					Message: fmt.Sprintf("request %s failed: %s", id, s.Error),
					Details: map[string]any{"requestId": id, "errorType": s.ErrorType},
				}
			}
			return s, nil
		}
		remaining := deadline.Sub(now())
		if remaining <= 0 {
			return s, timeoutErr(id)
		}
		if err := sleep(ctx, min(delay, remaining)); err != nil {
			return s, timeoutErr(id)
		}
		delay = min(time.Duration(float64(delay)*pollFactor), pollMax)
	}
}

func (w Waiter) report(s Status) {
	if w.Progress == nil {
		return
	}
	if s.State == StateInQueue && s.QueuePosition > 0 {
		_, _ = fmt.Fprintf(w.Progress, "state: %s (position %d)\n", s.State, s.QueuePosition)
		return
	}
	_, _ = fmt.Fprintf(w.Progress, "state: %s\n", s.State)
}

func timeoutErr(id string) error {
	return &output.Error{
		Code:    output.CodeTimeout,
		Message: fmt.Sprintf("timed out waiting for request %s; resume with: fal-cli task wait %s", id, id),
		Details: map[string]any{"requestId": id},
	}
}
