// Package swarm coordinates multi-Bee skill self-reviews with bounded
// concurrency, live progress snapshots, failure isolation, and cancellation
// (issue #10).
//
// Workshop proposals discovered during self-review are counted and preserved
// for inspection; they are never auto-applied.
package swarm

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/hosts"
	"github.com/meistro57/beechief/internal/openclaw"
)

// DefaultConcurrency bounds simultaneous Bee self-reviews across the fleet.
const DefaultConcurrency = 2

// Mode selects which Bees participate in a Swarm Review run.
type Mode string

const (
	ModeSelectedBee  Mode = "selected_bee"
	ModeSelectedHive Mode = "selected_hive"
	ModeAllReachable Mode = "all_reachable"
)

// BeeState tracks one Bee's progress in a Swarm Review run.
type BeeState string

const (
	StateQueued   BeeState = "queued"
	StateRunning  BeeState = "running"
	StateComplete BeeState = "complete"
	StateFailed   BeeState = "failed"
	StateSkipped  BeeState = "skipped"
)

// Item holds the state and preserved output for a single Bee in a Swarm Review.
type Item struct {
	Bee           agents.Bee                 `json:"bee"`
	State         BeeState                   `json:"state"`
	Output        string                     `json:"output,omitempty"`
	RawOutput     string                     `json:"rawOutput,omitempty"`
	Proposals     []openclaw.WorkshopProposal `json:"proposals,omitempty"`
	ProposalCount int                        `json:"proposalCount"`
	Err           string                     `json:"err,omitempty"`
	Duration      time.Duration              `json:"duration"`
}

// Summary tallies the outcomes of a Swarm Review snapshot.
type Summary struct {
	Total          int `json:"total"`
	Queued         int `json:"queued"`
	Running        int `json:"running"`
	Completed      int `json:"completed"`
	Failed         int `json:"failed"`
	Skipped        int `json:"skipped"`
	TotalProposals int `json:"totalProposals"`
}

// Snapshot is an immutable view of a Swarm Review's current progress.
type Snapshot struct {
	Mode      Mode      `json:"mode"`
	StartedAt time.Time `json:"startedAt"`
	Done      bool      `json:"done"`
	Canceled  bool      `json:"canceled"`
	Items     []Item    `json:"items"`
	Summary   Summary   `json:"summary"`
}

// ReviewFunc executes a single Bee's self-review.
type ReviewFunc func(ctx context.Context, bee agents.Bee) (*openclaw.SelfReviewReport, error)

// NewOpenClawReviewer builds a ReviewFunc that reuses openclaw.Client.SelfReview
// for each Hive's execution layer.
func NewOpenClawReviewer(execByHive map[string]hosts.Execution) ReviewFunc {
	return func(ctx context.Context, bee agents.Bee) (*openclaw.SelfReviewReport, error) {
		exec := execByHive[bee.Hive]
		if exec == nil {
			return nil, errors.New("no execution available for Hive " + bee.Hive)
		}
		return openclaw.NewClient(exec).SelfReview(ctx, bee.ID)
	}
}

// SelectBees resolves the target Bee list from inv for the requested Mode.
func SelectBees(inv *agents.Inventory, mode Mode, selectedBee *agents.Bee, selectedHive string) []agents.Bee {
	if inv == nil {
		return nil
	}
	switch mode {
	case ModeSelectedBee:
		if selectedBee != nil && selectedBee.ID != "" {
			return []agents.Bee{*selectedBee}
		}
		all := inv.AllBees()
		if len(all) > 0 {
			return all[:1]
		}
		return nil
	case ModeSelectedHive:
		if selectedHive == "" && selectedBee != nil {
			selectedHive = selectedBee.Hive
		}
		if selectedHive == "" && len(inv.Hives) > 0 {
			selectedHive = inv.Hives[0]
		}
		return append([]agents.Bee(nil), inv.ByHive[selectedHive]...)
	case ModeAllReachable:
		var out []agents.Bee
		for _, h := range inv.Hives {
			if st, ok := inv.Status[h]; ok && st.OK() {
				out = append(out, inv.ByHive[h]...)
			}
		}
		return out
	default:
		return inv.AllBees()
	}
}

// Coordinator manages a single Swarm Review run with bounded concurrency and
// safe cancellation.
type Coordinator struct {
	mode        Mode
	review      ReviewFunc
	concurrency int
	onProgress  func(Snapshot)

	mu        sync.Mutex
	startedAt time.Time
	items     []Item
	done      bool
	canceled  bool
	cancelFn  context.CancelFunc
}

// NewCoordinator prepares a Swarm Review across bees with the given concurrency limit.
func NewCoordinator(mode Mode, bees []agents.Bee, review ReviewFunc, concurrency int, onProgress func(Snapshot)) *Coordinator {
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}
	items := make([]Item, len(bees))
	for i, b := range bees {
		items[i] = Item{
			Bee:   b,
			State: StateQueued,
		}
	}
	return &Coordinator{
		mode:        mode,
		review:      review,
		concurrency: concurrency,
		onProgress:  onProgress,
		items:       items,
	}
}

// Snapshot returns a thread-safe copy of the current progress and summary.
func (c *Coordinator) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshotLocked()
}

func (c *Coordinator) snapshotLocked() Snapshot {
	cp := make([]Item, len(c.items))
	var sum Summary
	sum.Total = len(c.items)
	for i, it := range c.items {
		itCopy := it
		if len(it.Proposals) > 0 {
			itCopy.Proposals = append([]openclaw.WorkshopProposal(nil), it.Proposals...)
		}
		cp[i] = itCopy
		switch it.State {
		case StateQueued:
			sum.Queued++
		case StateRunning:
			sum.Running++
		case StateComplete:
			sum.Completed++
			sum.TotalProposals += it.ProposalCount
		case StateFailed:
			sum.Failed++
		case StateSkipped:
			sum.Skipped++
		}
	}
	return Snapshot{
		Mode:      c.mode,
		StartedAt: c.startedAt,
		Done:      c.done,
		Canceled:  c.canceled,
		Items:     cp,
		Summary:   sum,
	}
}

// Cancel marks any queued Bees as StateSkipped, cancels the context for work
// not yet completed, and preserves all completed results.
func (c *Coordinator) Cancel() {
	c.mu.Lock()
	c.canceled = true
	for i := range c.items {
		if c.items[i].State == StateQueued {
			c.items[i].State = StateSkipped
		}
	}
	cancel := c.cancelFn
	snap := c.snapshotLocked()
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if c.onProgress != nil {
		c.onProgress(snap)
	}
}

// Run executes the Swarm Review until all Bees finish or Cancel is called,
// returning the final Snapshot.
func (c *Coordinator) Run(ctx context.Context) Snapshot {
	runCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.startedAt = time.Now()
	c.cancelFn = cancel
	if len(c.items) == 0 {
		c.done = true
		snap := c.snapshotLocked()
		c.mu.Unlock()
		cancel()
		return snap
	}
	c.mu.Unlock()
	defer cancel()

	c.notify()

	sem := make(chan struct{}, c.concurrency)
	var wg sync.WaitGroup

	loop:
	for idx := range c.items {
		// Claim the slot before deciding whether the item is still queued.
		select {
		case sem <- struct{}{}:
		case <-runCtx.Done():
			c.markRemainingSkipped()
			break loop
		}

		c.mu.Lock()
		if c.canceled || runCtx.Err() != nil {
			if c.items[idx].State == StateQueued {
				c.items[idx].State = StateSkipped
			}
			c.mu.Unlock()
			<-sem
			continue
		}
		if c.items[idx].State != StateQueued {
			c.mu.Unlock()
			<-sem
			continue
		}
		c.items[idx].State = StateRunning
		bee := c.items[idx].Bee
		c.mu.Unlock()
		c.notify()

		wg.Add(1)
		go func(i int, b agents.Bee) {
			defer wg.Done()
			defer func() { <-sem }()

			start := time.Now()
			var rep *openclaw.SelfReviewReport
			var err error
			if c.review == nil {
				err = errors.New("no review function configured")
			} else {
				rep, err = c.review(runCtx, b)
			}
			dur := time.Since(start)

			c.mu.Lock()
			c.items[i].Duration = dur
			switch {
			case err != nil && (errors.Is(err, context.Canceled) || c.canceled):
				c.items[i].State = StateSkipped
				c.items[i].Err = "canceled"
			case err != nil:
				c.items[i].State = StateFailed
				c.items[i].Err = err.Error()
			default:
				c.items[i].State = StateComplete
				if rep != nil {
					c.items[i].Output = rep.Summary
					c.items[i].RawOutput = rep.RawOutput
					c.items[i].Proposals = rep.Proposals
					c.items[i].ProposalCount = len(rep.Proposals)
				}
			}
			c.mu.Unlock()
			c.notify()
		}(idx, bee)
	}

	wg.Wait()
	c.mu.Lock()
	for i := range c.items {
		if c.items[i].State == StateQueued {
			c.items[i].State = StateSkipped
		}
	}
	c.done = true
	final := c.snapshotLocked()
	c.mu.Unlock()
	if c.onProgress != nil {
		c.onProgress(final)
	}
	return final
}

func (c *Coordinator) markRemainingSkipped() {
	c.mu.Lock()
	for i := range c.items {
		if c.items[i].State == StateQueued {
			c.items[i].State = StateSkipped
		}
	}
	c.mu.Unlock()
	c.notify()
}

func (c *Coordinator) notify() {
	if c.onProgress == nil {
		return
	}
	c.onProgress(c.Snapshot())
}
