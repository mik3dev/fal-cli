package ledger_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mik3dev/fal-cli/internal/ledger"
)

var base = time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)

func tempLedger(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "tasks.jsonl")
}

func mustAppend(t *testing.T, path string, e ledger.Entry) {
	t.Helper()
	if err := ledger.Append(path, e); err != nil {
		t.Fatal(err)
	}
}

func TestAppendThenListNewestFirst(t *testing.T) {
	p := tempLedger(t)
	mustAppend(t, p, ledger.Entry{RequestID: "a", Endpoint: "m1", State: "submitted", CreatedAt: base})
	mustAppend(t, p, ledger.Entry{RequestID: "b", Endpoint: "m2", State: "submitted", CreatedAt: base.Add(time.Minute)})
	mustAppend(t, p, ledger.Entry{RequestID: "c", Endpoint: "m3", State: "submitted", CreatedAt: base.Add(2 * time.Minute)})
	got, err := ledger.List(p, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].RequestID != "c" || got[2].RequestID != "a" {
		t.Fatalf("got %+v", got)
	}
	if !got[0].CreatedAt.Equal(base.Add(2 * time.Minute)) {
		t.Errorf("CreatedAt not round-tripped: %v", got[0].CreatedAt)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"requestId":"a"`) || !strings.Contains(string(b), `"endpoint":"m1"`) {
		t.Fatalf("json keys wrong: %s", b)
	}
}

func TestListKeepsLastStatePerRequest(t *testing.T) {
	p := tempLedger(t)
	mustAppend(t, p, ledger.Entry{RequestID: "t1", Endpoint: "m", State: "submitted", CreatedAt: base})
	mustAppend(t, p, ledger.Entry{RequestID: "t1", Endpoint: "m", State: "success", OutDir: "/out", CreatedAt: base})
	mustAppend(t, p, ledger.Entry{Endpoint: "m", State: "unknown", Note: "first"})
	mustAppend(t, p, ledger.Entry{Endpoint: "m", State: "unknown", Note: "second"})
	got, err := ledger.List(p, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[2].State != "success" || got[2].OutDir != "/out" || got[0].Note != "second" {
		t.Fatalf("got %+v", got)
	}
	if two, _ := ledger.List(p, 2, nil); len(two) != 2 {
		t.Fatalf("limit ignored: %+v", two)
	}
}

func TestListSkipsCorruptLinesWithWarning(t *testing.T) {
	p := tempLedger(t)
	mustAppend(t, p, ledger.Entry{RequestID: "ok1", Endpoint: "m", State: "submitted"})
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("{not json\n")
	_ = f.Close()
	mustAppend(t, p, ledger.Entry{RequestID: "ok2", Endpoint: "m", State: "success"})
	var warn bytes.Buffer
	got, err := ledger.List(p, 0, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].RequestID != "ok2" || !strings.Contains(warn.String(), "line 2") {
		t.Fatalf("got %+v warn=%q", got, warn.String())
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("perm = %04o", fi.Mode().Perm())
	}
}

func TestFindMissing(t *testing.T) {
	p := tempLedger(t)
	if _, ok := ledger.Find(p, "x"); ok {
		t.Fatal("found in missing file")
	}
	mustAppend(t, p, ledger.Entry{RequestID: "x", Endpoint: "m", State: "submitted"})
	mustAppend(t, p, ledger.Entry{RequestID: "x", Endpoint: "m", State: "success"})
	e, ok := ledger.Find(p, "x")
	if !ok || e.State != "success" {
		t.Fatalf("e=%+v ok=%v", e, ok)
	}
	if _, ok := ledger.Find(p, "y"); ok {
		t.Fatal("found y")
	}
	if got, _ := ledger.List(filepath.Join(t.TempDir(), "none.jsonl"), 10, nil); got == nil || len(got) != 0 {
		t.Fatalf("missing file: %#v", got)
	}
}
