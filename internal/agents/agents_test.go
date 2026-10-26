package agents

import (
	"errors"
	"testing"
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
