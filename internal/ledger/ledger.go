// Package ledger keeps the local JSONL log of submitted requests.
package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const filePerm = 0o600

// Entry is one line of the request ledger. State is submitted|unknown|success|fail|cancelled.
type Entry struct {
	RequestID string    `json:"requestId,omitempty"`
	Endpoint  string    `json:"endpoint"`
	State     string    `json:"state"`
	OutDir    string    `json:"outDir,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	Note      string    `json:"note,omitempty"`
}

// Append adds e as a new line. ponytail: append-only; state updates are new lines, List keeps the last per requestId.
func Append(path string, e Entry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode ledger entry: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerm)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

// List returns entries newest first. Only the last line per RequestID is kept;
// entries without a RequestID are all kept. limit <= 0 means no limit. Corrupt
// lines are skipped and reported on warn when warn is not nil. A missing file
// yields an empty list.
func List(path string, limit int, warn io.Writer) ([]Entry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	parsed := parseLines(data, warn)
	seen := make(map[string]bool, len(parsed))
	out := make([]Entry, 0, len(parsed))
	for i := len(parsed) - 1; i >= 0; i-- {
		e := parsed[i]
		if e.RequestID != "" {
			if seen[e.RequestID] {
				continue
			}
			seen[e.RequestID] = true
		}
		out = append(out, e)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

// Find returns the latest entry of requestID. Corrupt lines are ignored silently.
func Find(path, requestID string) (Entry, bool) {
	all, err := List(path, 0, nil)
	if err != nil {
		return Entry{}, false
	}
	for _, e := range all {
		if e.RequestID == requestID {
			return e, true
		}
	}
	return Entry{}, false
}

// parseLines decodes every non-blank line in file order, skipping corrupt ones.
func parseLines(data []byte, warn io.Writer) []Entry {
	var entries []Entry
	for i, raw := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			if warn != nil {
				_, _ = fmt.Fprintf(warn, "warning: skipping corrupt ledger line %d: %v\n", i+1, err)
			}
			continue
		}
		entries = append(entries, e)
	}
	return entries
}
