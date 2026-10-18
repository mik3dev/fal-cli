package cli_test

import (
	"os"
	"path/filepath"
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
