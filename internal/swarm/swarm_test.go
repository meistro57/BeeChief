package swarm

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/openclaw"
)

func TestSelectBeesAcrossModes(t *testing.T) {
	inv := agents.NewInventory()
	inv.Record(agents.HiveStatus{Hive: "h1"}, []agents.Bee{{ID: "a"}, {ID: "b"}})
	inv.Record(agents.HiveStatus{Hive: "h2"}, []agents.Bee{{ID: "c"}})
	inv.Record(agents.HiveStatus{Hive: "h3", Err: errors.New("down")}, []agents.Bee{{ID: "d"}})

	selected := &agents.Bee{ID: "b", Hive: "h1"}
	if got := SelectBees(inv, ModeSelectedBee, selected, ""); len(got) != 1 || got[0].ID != "b" {
		t.Errorf("ModeSelectedBee: %+v", got)
	}
	if got := SelectBees(inv, ModeSelectedHive, nil, "h1"); len(got) != 2 {
		t.Errorf("ModeSelectedHive: %+v", got)
	}
	if got := SelectBees(inv, ModeAllReachable, nil, ""); len(got) != 3 {
		t.Errorf("ModeAllReachable: got %d bees, want 3 (h1+h2)", len(got))
	}
}

func TestCoordinatorConcurrencyAndPartialFailureIsolation(t *testing.T) {
	bees := []agents.Bee{
		{ID: "alpha", Hive: "h1"},
		{ID: "broken", Hive: "h1"},
		{ID: "gamma", Hive: "h2"},
	}

	var active, maxActive int32
	var progressSnapshots int32

	reviewFn := func(_ context.Context, b agents.Bee) (*openclaw.SelfReviewReport, error) {
		cur := atomic.AddInt32(&active, 1)
		for {
			prev := atomic.LoadInt32(&maxActive)
			if cur <= prev || atomic.CompareAndSwapInt32(&maxActive, prev, cur) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		atomic.AddInt32(&active, -1)

		if b.ID == "broken" {
			return nil, errors.New("gateway timeout")
		}
		return &openclaw.SelfReviewReport{
			AgentID: b.ID,
			Summary: "reviewed " + b.Key(),
			Proposals: []openclaw.WorkshopProposal{
				{Name: "skill-" + b.ID},
			},
		}, nil
	}

	coord := NewCoordinator(ModeAllReachable, bees, reviewFn, 2, func(Snapshot) {
		atomic.AddInt32(&progressSnapshots, 1)
	})
	snap := coord.Run(context.Background())

	if maxActive > 2 {
		t.Errorf("concurrency exceeded limit 2: maxActive=%d", maxActive)
	}
	if !snap.Done || snap.Summary.Total != 3 || snap.Summary.Completed != 2 || snap.Summary.Failed != 1 || snap.Summary.TotalProposals != 2 {
		t.Fatalf("unexpected final summary: %+v", snap.Summary)
	}
	if snap.Items[0].Output != "reviewed h1/alpha" || snap.Items[1].State != StateFailed || snap.Items[2].Output != "reviewed h2/gamma" {
		t.Errorf("unexpected item results: %+v", snap.Items)
	}
	if atomic.LoadInt32(&progressSnapshots) == 0 {
		t.Error("expected progress callbacks")
	}
}

func TestCoordinatorCancellationPreservesCompletedAndSkipsQueued(t *testing.T) {
	bees := []agents.Bee{
		{ID: "first", Hive: "h1"},
		{ID: "second", Hive: "h1"},
		{ID: "third", Hive: "h2"},
	}

	var coord *Coordinator
	reviewFn := func(ctx context.Context, b agents.Bee) (*openclaw.SelfReviewReport, error) {
		if b.ID == "first" {
			return &openclaw.SelfReviewReport{
				AgentID:   "first",
				Summary:   "first completed",
				Proposals: []openclaw.WorkshopProposal{{Name: "p1"}},
			}, nil
		}
		if b.ID == "second" {
			// Cancel while second is running so third (still queued with concurrency=1) is skipped.
			coord.Cancel()
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &openclaw.SelfReviewReport{AgentID: b.ID, Summary: "should not run"}, nil
	}

	coord = NewCoordinator(ModeAllReachable, bees, reviewFn, 1, nil)
	snap := coord.Run(context.Background())

	if !snap.Canceled || !snap.Done {
		t.Fatalf("expected Canceled and Done snapshot: %+v", snap)
	}
	if snap.Summary.Completed != 1 || snap.Summary.Skipped != 2 || snap.Summary.TotalProposals != 1 {
		t.Fatalf("unexpected summary after cancel: %+v (items: %+v)", snap.Summary, snap.Items)
	}
	if snap.Items[0].Output != "first completed" {
		t.Errorf("completed output was not preserved: %+v", snap.Items[0])
	}
}
