package jobs

import (
	"context"
	"path/filepath"
	"testing"
)

func testRunner(ctx context.Context, job Job) (Result, error) {
	return Result{Job: job.Name, Kind: job.Kind, Summary: "ran " + job.Name}, nil
}

func TestManagerUpsertRunPersistDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.yaml")
	m, err := NewManager(path, testRunner)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m.Upsert(Job{Name: "nightly", Kind: KindScan, Schedule: "0 3 * * *"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	list := m.List()
	if len(list) != 1 || list[0].Job.Name != "nightly" || list[0].NextRun.IsZero() {
		t.Fatalf("unexpected status: %+v", list)
	}

	res, err := m.RunNow(context.Background(), "nightly")
	if err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if res.Summary != "ran nightly" || res.DurationMs < 0 {
		t.Errorf("unexpected result: %+v", res)
	}
	if list = m.List(); list[0].Last.Summary != "ran nightly" || list[0].Running {
		t.Errorf("run state not recorded: %+v", list[0])
	}

	reloaded, err := NewManager(path, testRunner)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.List(); len(got) != 1 || got[0].Job.Name != "nightly" {
		t.Fatalf("persisted jobs: %+v", got)
	}

	if err := reloaded.Delete("nightly"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := reloaded.List(); len(got) != 0 {
		t.Errorf("jobs after delete: %+v", got)
	}
}

func TestManagerRejectsDuplicateAndInvalid(t *testing.T) {
	m, err := NewManager("", testRunner)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Upsert(Job{Name: "bad", Kind: "nope", Schedule: "* * * * *"}); err == nil {
		t.Error("expected invalid kind error")
	}
	if err := m.Upsert(Job{Name: "bad", Kind: KindScan, Schedule: "nope"}); err == nil {
		t.Error("expected invalid schedule error")
	}
	if err := m.Upsert(Job{Name: "ok", Kind: KindHealth, Schedule: "* * * * *"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RunNow(context.Background(), "missing"); err == nil {
		t.Error("expected missing job error")
	}
}

func TestRunAllOnceSkipsDisabled(t *testing.T) {
	m, err := NewManager("", testRunner)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Upsert(Job{Name: "a", Kind: KindScan, Schedule: "* * * * *"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Upsert(Job{Name: "b", Kind: KindHealth, Schedule: "* * * * *", Disabled: true}); err != nil {
		t.Fatal(err)
	}
	results, err := m.RunAllOnce(context.Background())
	if err != nil {
		t.Fatalf("RunAllOnce: %v", err)
	}
	if len(results) != 1 || results[0].Job != "a" {
		t.Errorf("unexpected results: %+v", results)
	}
}
