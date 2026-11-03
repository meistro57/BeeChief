package discovery

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meistro57/beechief/internal/config"
	"github.com/meistro57/beechief/internal/hosts"
	"github.com/meistro57/beechief/internal/openclaw"
)

// scripted is a hosts.Execution returning canned output.
type scripted struct {
	stdout string
	exit   int
	err    error
	delay  time.Duration
	calls  atomic.Int32
}

func (s *scripted) Run(ctx context.Context, _ string, _ ...string) (hosts.Result, error) {
	s.calls.Add(1)
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return hosts.Result{}, &hosts.Error{Kind: hosts.KindTimeout, Err: ctx.Err()}
		}
	}
	return hosts.Result{Stdout: s.stdout, ExitCode: s.exit}, s.err
}

func TestScanPartialFailure(t *testing.T) {
	healthy := &scripted{stdout: `[{"id":"main","identityName":"Main"},{"id":"helper"}]`}
	down := &scripted{err: &hosts.Error{Hive: "down", Kind: hosts.KindUnreachable}}
	broken := &scripted{stdout: "garbage"}
	missing := &scripted{exit: 127}

	s := New([]Target{
		{Hive: "healthy", Exec: healthy},
		{Hive: "down", Exec: down},
		{Hive: "broken", Exec: broken},
		{Hive: "missing", Exec: missing},
		{Hive: "misconfigured", Err: errors.New("bad config")},
	})
	inv := s.Scan(context.Background())

	if got := inv.Hives; len(got) != 5 || got[0] != "healthy" || got[4] != "misconfigured" {
		t.Errorf("hive order: %v", got)
	}
	if inv.Total() != 2 {
		t.Errorf("total: got %d, want 2", inv.Total())
	}
	for _, b := range inv.ByHive["healthy"] {
		if b.Hive != "healthy" {
			t.Errorf("bee missing hive: %+v", b)
		}
	}
	if !inv.Status["healthy"].OK() {
		t.Errorf("healthy hive marked failed: %v", inv.Status["healthy"].Err)
	}

	failed := inv.Failed()
	if len(failed) != 4 {
		t.Fatalf("failed hives: got %d, want 4", len(failed))
	}
	checks := map[string]func(error) bool{
		"down":          func(e error) bool { return hosts.IsKind(e, hosts.KindUnreachable) },
		"broken":        func(e error) bool { return errors.Is(e, openclaw.ErrInvalidOutput) },
		"missing":       func(e error) bool { return errors.Is(e, openclaw.ErrNotInstalled) },
		"misconfigured": func(e error) bool { return e != nil },
	}
	for _, f := range failed {
		if !checks[f.Hive](f.Err) {
			t.Errorf("hive %s: unexpected error %v", f.Hive, f.Err)
		}
		if len(inv.ByHive[f.Hive]) != 0 {
			t.Errorf("failed hive %s should have no bees", f.Hive)
		}
	}
}

func TestDuplicateAgentIDsAcrossHivesDoNotCollide(t *testing.T) {
	same := `[{"id":"main"}]`
	s := New([]Target{
		{Hive: "a", Exec: &scripted{stdout: same}},
		{Hive: "b", Exec: &scripted{stdout: same}},
	})
	inv := s.Scan(context.Background())
	if inv.Total() != 2 {
		t.Fatalf("total: got %d, want 2", inv.Total())
	}
	ka, kb := inv.ByHive["a"][0].Key(), inv.ByHive["b"][0].Key()
	if ka == kb || ka != "a/main" || kb != "b/main" {
		t.Errorf("keys collide or wrong: %q %q", ka, kb)
	}
}

func TestSlowHiveTimesOutWithoutBlockingOthers(t *testing.T) {
	s := New([]Target{
		{Hive: "slow", Exec: &scripted{delay: 10 * time.Second}},
		{Hive: "fast", Exec: &scripted{stdout: `[{"id":"x"}]`}},
	})
	s.HiveTimeout = 100 * time.Millisecond
	start := time.Now()
	inv := s.Scan(context.Background())
	if time.Since(start) > 3*time.Second {
		t.Errorf("scan took %v", time.Since(start))
	}
	if !hosts.IsKind(inv.Status["slow"].Err, hosts.KindTimeout) {
		t.Errorf("slow hive: %v", inv.Status["slow"].Err)
	}
	if len(inv.ByHive["fast"]) != 1 {
		t.Errorf("fast hive did not load")
	}
}

func TestRescanSingleHiveMerges(t *testing.T) {
	a := &scripted{stdout: `[{"id":"one"}]`}
	b := &scripted{err: &hosts.Error{Kind: hosts.KindUnreachable}}
	s := New([]Target{{Hive: "a", Exec: a}, {Hive: "b", Exec: b}})

	inv := s.Scan(context.Background())
	if len(inv.Failed()) != 1 {
		t.Fatalf("want b failed, got %v", inv.Failed())
	}

	// b comes back with two agents; rescan only b.
	b.err, b.stdout = nil, `[{"id":"x"},{"id":"y"}]`
	inv = s.Rescan(context.Background(), "b")
	if len(inv.Failed()) != 0 || inv.Total() != 3 {
		t.Errorf("after rescan: failed=%v total=%d", inv.Failed(), inv.Total())
	}
	if a.calls.Load() != 1 {
		t.Errorf("hive a should not be rescanned, calls=%d", a.calls.Load())
	}
	if s.Last().Total() != 3 {
		t.Errorf("Last not updated")
	}

	// Rescan with no names scans everything again.
	s.Rescan(context.Background())
	if a.calls.Load() != 2 || b.calls.Load() != 3 {
		t.Errorf("full rescan calls: a=%d b=%d", a.calls.Load(), b.calls.Load())
	}
}

func TestFromConfigEmpty(t *testing.T) {
	inv := FromConfig(config.Default()).Scan(context.Background())
	if inv.Total() != 0 || len(inv.Hives) != 0 {
		t.Errorf("empty config should yield empty inventory: %+v", inv)
	}
}

func TestFromConfigInvalidHiveMarkedFailed(t *testing.T) {
	cfg := config.Config{Hives: []config.Hive{{Name: "nohost", Type: config.TypeSSH}}}
	inv := FromConfig(cfg).Scan(context.Background())
	if len(inv.Failed()) != 1 {
		t.Errorf("want 1 failed hive, got %+v", inv.Failed())
	}
}
