package cli_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mik3dev/fal-cli/internal/ledger"
)

func TestTaskListCommand(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	env := map[string]string{"FAL_CLI_HOME": home}
	code, m, _ := run(t, env, "task", "list")
	if code != 0 || len(m["tasks"].([]any)) != 0 {
		t.Fatalf("empty: code=%d m=%v", code, m)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := ledger.Append(filepath.Join(home, "tasks.jsonl"), ledger.Entry{RequestID: id, Endpoint: "fal-ai/x", State: "submitted"}); err != nil {
			t.Fatal(err)
		}
	}
	code, m, _ = run(t, env, "task", "list", "--limit", "2")
	tasks, _ := m["tasks"].([]any)
	if code != 0 || len(tasks) != 2 || tasks[0].(map[string]any)["requestId"] != "c" {
		t.Fatalf("code=%d m=%v", code, m)
	}
	if code, m, _ = run(t, env, "task", "list", "--limit", "0"); code != 2 || m["error"].(map[string]any)["code"] != "usage" {
		t.Fatalf("limit 0: code=%d m=%v", code, m)
	}
}

func TestTaskGetNeedsModelWhenNotInLedger(t *testing.T) {
	var hits atomic.Int32
	code, m, _ := run(t, apiEnv(fakeFal(t, &hits)), "task", "get", "req-404")
	e, _ := m["error"].(map[string]any)
	if code != 2 || e["code"] != "usage" || !strings.Contains(e["message"].(string), "--model") {
		t.Fatalf("code=%d m=%v", code, m)
	}
}

// fakeQueue answers status/result/cancel for any endpoint.
func fakeQueue(t *testing.T, status, result string, cancelStatus int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/status"):
			_, _ = w.Write([]byte(status))
		case strings.HasSuffix(r.URL.Path, "/cancel"):
			w.WriteHeader(cancelStatus)
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(result))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTaskGetCompletedIncludesResult(t *testing.T) {
	srv := fakeQueue(t, `{"status":"COMPLETED"}`, `{"images":[{"url":"https://x/a.png"}]}`, 202)
	code, m, _ := run(t, apiEnv(srv), "task", "get", "req-1", "--model", "fal-ai/flux/schnell")
	res, _ := m["result"].(map[string]any)
	if code != 0 || m["state"] != "COMPLETED" || m["model"] != "fal-ai/flux/schnell" || m["requestId"] != "req-1" || res["images"] == nil {
		t.Fatalf("code=%d m=%v", code, m)
	}
	srv2 := fakeQueue(t, `{"status":"IN_QUEUE","queue_position":2}`, ``, 202)
	code, m, _ = run(t, apiEnv(srv2), "task", "get", "req-1", "--model", "fal-ai/flux/schnell")
	if code != 0 || m["state"] != "IN_QUEUE" || m["queuePosition"] != 2.0 || m["result"] != nil {
		t.Fatalf("queued: code=%d m=%v", code, m)
	}
}

func TestTaskCancelCommandUsesLedgerEndpoint(t *testing.T) {
	srv := fakeQueue(t, ``, ``, 202)
	env := apiEnv(srv)
	home := filepath.Join(t.TempDir(), "home")
	env["FAL_CLI_HOME"] = home
	_ = os.MkdirAll(home, 0o700)
	_ = ledger.Append(filepath.Join(home, "tasks.jsonl"), ledger.Entry{RequestID: "req-7", Endpoint: "fal-ai/x", State: "submitted"})
	code, m, _ := run(t, env, "task", "cancel", "req-7")
	if code != 0 || m["status"] != "CANCELLATION_REQUESTED" || m["model"] != "fal-ai/x" {
		t.Fatalf("code=%d m=%v", code, m)
	}
	if e, _ := ledger.Find(filepath.Join(home, "tasks.jsonl"), "req-7"); e.State != "cancelled" {
		t.Fatalf("ledger = %+v", e)
	}
}
