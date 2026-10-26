// Package discovery scans every configured Hive for Bees.
//
// Hives are scanned concurrently and independently: one unreachable or
// broken Hive is recorded as failed in the Inventory while every healthy
// Hive still loads (roadmap: "Partial failure is normal").
package discovery

import (
	"context"
	"sync"
	"time"

	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/config"
	"github.com/meistro57/beechief/internal/hosts"
	"github.com/meistro57/beechief/internal/openclaw"
)

// DefaultHiveTimeout bounds the scan of a single Hive.
const DefaultHiveTimeout = 20 * time.Second

// Target is one Hive to scan. Exactly one of Exec or Err is set: Err holds
// a configuration problem that makes the Hive unscannable.
type Target struct {
	Hive string
	Exec hosts.Execution
	Err  error
}

// Scanner discovers Bees on a fixed set of Hives and remembers the most
// recent Inventory so individual Hives can be rescanned.
type Scanner struct {
	targets []Target
	// HiveTimeout bounds each Hive's scan. Zero means DefaultHiveTimeout.
	HiveTimeout time.Duration

	mu   sync.Mutex
	last *agents.Inventory
}

// New returns a Scanner for the given targets.
func New(targets []Target) *Scanner {
	return &Scanner{targets: targets, last: agents.NewInventory()}
}

// FromConfig builds a Scanner with one target per configured Hive.
func FromConfig(cfg config.Config) *Scanner {
	targets := make([]Target, 0, len(cfg.Hives))
	for _, h := range cfg.Hives {
		exec, err := hosts.ForHive(h)
		targets = append(targets, Target{Hive: h.Name, Exec: exec, Err: err})
	}
	return New(targets)
}

// Scan discovers Bees on every Hive and replaces the remembered Inventory.
func (s *Scanner) Scan(ctx context.Context) *agents.Inventory {
	inv := agents.NewInventory()
	s.scanInto(ctx, inv, s.targets)
	s.mu.Lock()
	s.last = inv
	s.mu.Unlock()
	return inv.Clone()
}

// Rescan rescans the named Hives (all Hives when none are named), merging
// the fresh results into the remembered Inventory. Unknown names are ignored.
func (s *Scanner) Rescan(ctx context.Context, hives ...string) *agents.Inventory {
	if len(hives) == 0 {
		return s.Scan(ctx)
	}
	want := make(map[string]bool, len(hives))
	for _, h := range hives {
		want[h] = true
	}
	var subset []Target
	for _, t := range s.targets {
		if want[t.Hive] {
			subset = append(subset, t)
		}
	}

	s.mu.Lock()
	inv := s.last.Clone()
	s.mu.Unlock()
	s.scanInto(ctx, inv, subset)
	s.mu.Lock()
	s.last = inv
	s.mu.Unlock()
	return inv.Clone()
}

// Last returns a copy of the most recent Inventory.
func (s *Scanner) Last() *agents.Inventory {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last.Clone()
}

type outcome struct {
	status agents.HiveStatus
	bees   []agents.Bee
}

func (s *Scanner) scanInto(ctx context.Context, inv *agents.Inventory, targets []Target) {
	results := make([]outcome, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t Target) {
			defer wg.Done()
			results[i] = s.scanOne(ctx, t)
		}(i, t)
	}
	wg.Wait()
	// Record in target order so the Inventory is deterministic.
	for _, r := range results {
		inv.Record(r.status, r.bees)
	}
}

func (s *Scanner) scanOne(ctx context.Context, t Target) outcome {
	start := time.Now()
	status := agents.HiveStatus{Hive: t.Hive, ScannedAt: start}
	if t.Err != nil {
		status.Err = t.Err
		return outcome{status: status}
	}

	timeout := s.HiveTimeout
	if timeout <= 0 {
		timeout = DefaultHiveTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	bees, err := openclaw.NewClient(t.Exec).DiscoverAgents(ctx)
	status.Err = err
	status.Duration = time.Since(start)
	return outcome{status: status, bees: bees}
}
