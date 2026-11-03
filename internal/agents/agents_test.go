package agents

import (
	"errors"
	"testing"
	"time"
)

func TestRecordStampsHiveAndSorts(t *testing.T) {
	inv := NewInventory()
	inv.Record(HiveStatus{Hive: "h1"}, []Bee{{ID: "b"}, {ID: "a", Hive: "wrong"}})
	bees := inv.ByHive["h1"]
	if bees[0].ID != "a" || bees[0].Hive != "h1" || bees[1].Hive != "h1" {
		t.Errorf("unexpected bees: %+v", bees)
	}
}

func TestRecordReplacesAndKeepsOrder(t *testing.T) {
	inv := NewInventory()
	inv.Record(HiveStatus{Hive: "x"}, []Bee{{ID: "1"}})
	inv.Record(HiveStatus{Hive: "y", Err: errors.New("down")}, nil)
	inv.Record(HiveStatus{Hive: "x"}, []Bee{{ID: "1"}, {ID: "2"}})
	if len(inv.Hives) != 2 || inv.Hives[0] != "x" {
		t.Errorf("hive order: %v", inv.Hives)
	}
	if inv.Total() != 2 {
		t.Errorf("total: %d", inv.Total())
	}
	if f := inv.Failed(); len(f) != 1 || f[0].Hive != "y" {
		t.Errorf("failed: %+v", f)
	}
}

func TestCloneIsIndependent(t *testing.T) {
	inv := NewInventory()
	inv.Record(HiveStatus{Hive: "x"}, []Bee{{ID: "1"}})
	c := inv.Clone()
	c.Record(HiveStatus{Hive: "z"}, []Bee{{ID: "2"}})
	if inv.Total() != 1 || len(inv.Hives) != 1 {
		t.Errorf("clone mutated original")
	}
}

func TestDisplayName(t *testing.T) {
	if (Bee{ID: "id"}).DisplayName() != "id" || (Bee{ID: "id", Name: "N"}).DisplayName() != "N" {
		t.Error("DisplayName fallback wrong")
	}
}

func TestFilterBeesAndLastScanTime(t *testing.T) {
	inv := NewInventory()
	t1 := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(5 * time.Minute)
	inv.Record(HiveStatus{Hive: "alpha", ScannedAt: t1}, []Bee{
		{ID: "scout", Name: "Scout Bee", Runtime: "openai/gpt-5"},
		{ID: "worker", Name: "Worker Bee", Runtime: "ollama/llama3"},
	})
	inv.Record(HiveStatus{Hive: "beta", ScannedAt: t2}, []Bee{
		{ID: "queen", Name: "Queen", Runtime: "anthropic/claude"},
	})
	if got := inv.LastScanTime(); !got.Equal(t2) {
		t.Errorf("LastScanTime: got %v, want %v", got, t2)
	}
	if all := inv.AllBees(); len(all) != 3 {
		t.Fatalf("AllBees: got %d, want 3", len(all))
	}
	if filtered := inv.FilterBees("SCOUT"); len(filtered) != 1 || filtered[0].ID != "scout" {
		t.Errorf("FilterBees(SCOUT): got %+v", filtered)
	}
	if byHive := inv.FilterBees("beta"); len(byHive) != 1 || byHive[0].ID != "queen" {
		t.Errorf("FilterBees(beta): got %+v", byHive)
	}
}

func TestFilterBeesStructuredAndLargeSwarmQueries(t *testing.T) {
	inv := NewInventory()
	inv.Record(HiveStatus{Hive: "alpha"}, []Bee{
		{ID: "scout", Name: "Scout Bee", Runtime: "openai/gpt-5", Status: "ready", Default: true, Workspace: "/srv/scout"},
		{ID: "worker", Name: "Worker Bee", Runtime: "ollama/llama3", Status: "busy", Default: false, Workspace: "/srv/worker"},
	})
	inv.Record(HiveStatus{Hive: "beta", Err: errors.New("unreachable")}, []Bee{
		{ID: "queen", Name: "Queen", Runtime: "anthropic/claude", Status: "idle", Default: true},
	})

	if got := inv.FilterBees("hive:alpha model:ollama"); len(got) != 1 || got[0].ID != "worker" {
		t.Errorf("FilterBees(hive:alpha model:ollama): got %+v", got)
	}
	if got := inv.FilterBees("default:true !hive:beta"); len(got) != 1 || got[0].ID != "scout" {
		t.Errorf("FilterBees(default:true !hive:beta): got %+v", got)
	}
	if got := inv.FilterBees("status:online -llama3"); len(got) != 1 || got[0].ID != "scout" {
		t.Errorf("FilterBees(status:online -llama3): got %+v", got)
	}
	if got := inv.FilterBees("status:offline"); len(got) != 1 || got[0].ID != "queen" {
		t.Errorf("FilterBees(status:offline): got %+v", got)
	}
}
