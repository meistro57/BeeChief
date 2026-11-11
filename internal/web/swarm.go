package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/swarm"
)

type swarmRun struct {
	id      string
	mode    swarm.Mode
	coord   *swarm.Coordinator
	created time.Time

	mu   sync.Mutex
	snap swarm.Snapshot
}

func newSwarmRun(id string, mode swarm.Mode, bees []agents.Bee, backend Backend) *swarmRun {
	run := &swarmRun{id: id, mode: mode, created: time.Now()}
	run.coord = backend.StartSwarmReview(mode, bees, func(snap swarm.Snapshot) {
		run.mu.Lock()
		run.snap = snap
		run.mu.Unlock()
	})
	run.mu.Lock()
	run.snap = run.coord.Snapshot()
	run.mu.Unlock()
	go func() {
		final := run.coord.Run(context.Background())
		run.mu.Lock()
		run.snap = final
		run.mu.Unlock()
	}()
	return run
}

func (r *swarmRun) status() swarm.Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snap
}

func (r *swarmRun) cancel() {
	r.coord.Cancel()
}

type swarmStartRequest struct {
	Mode swarm.Mode `json:"mode"`
	Hive string     `json:"hive"`
	Bee  agents.Bee `json:"bee"`
}

func (s *Server) handleSwarmStart(w http.ResponseWriter, r *http.Request) {
	if s.readOnly {
		writeError(w, http.StatusForbidden, errors.New("web UI is read-only"))
		return
	}
	var req swarmStartRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	switch req.Mode {
	case swarm.ModeSelectedBee, swarm.ModeSelectedHive, swarm.ModeAllReachable:
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unknown swarm mode %q", req.Mode))
		return
	}

	s.mu.Lock()
	inv := s.inventory
	s.mu.Unlock()
	if inv == nil {
		writeError(w, http.StatusConflict, errors.New("scan the swarm before starting a review"))
		return
	}

	var selected *agents.Bee
	if req.Bee.ID != "" {
		if bee, ok := findBee(inv, req.Bee.Hive, req.Bee.ID); ok {
			selected = &bee
		} else {
			writeError(w, http.StatusBadRequest, errors.New("selected Bee was not found in the current inventory; rescan"))
			return
		}
	}
	bees := swarm.SelectBees(inv, req.Mode, selected, strings.TrimSpace(req.Hive))
	if len(bees) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("no Bees match the selected Swarm Review scope"))
		return
	}

	s.mu.Lock()
	active := 0
	for _, run := range s.swarms {
		if !run.status().Done {
			active++
		}
	}
	if active >= 4 {
		s.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, errors.New("too many Swarm Reviews are already running"))
		return
	}
	s.swarmSeq++
	id := fmt.Sprintf("swarm-%d", s.swarmSeq)
	run := newSwarmRun(id, req.Mode, bees, s.backend)
	s.swarms[id] = run
	s.pruneSwarmsLocked()
	s.mu.Unlock()
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id, "bees": len(bees)})
}

func (s *Server) handleSwarmStatus(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("id query parameter is required"))
		return
	}
	s.mu.Lock()
	run := s.swarms[id]
	s.mu.Unlock()
	if run == nil {
		writeError(w, http.StatusNotFound, errors.New("Swarm Review not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "snapshot": run.status()})
}

func (s *Server) handleSwarmCancel(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("id query parameter is required"))
		return
	}
	s.mu.Lock()
	run := s.swarms[id]
	s.mu.Unlock()
	if run == nil {
		writeError(w, http.StatusNotFound, errors.New("Swarm Review not found"))
		return
	}
	run.cancel()
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "snapshot": run.status()})
}

func (s *Server) pruneSwarmsLocked() {
	if len(s.swarms) <= 20 {
		return
	}
	var oldest *swarmRun
	for _, run := range s.swarms {
		if !run.status().Done {
			continue
		}
		if oldest == nil || run.created.Before(oldest.created) {
			oldest = run
		}
	}
	if oldest != nil {
		delete(s.swarms, oldest.id)
	}
}

func findBee(inv *agents.Inventory, hive, id string) (agents.Bee, bool) {
	for _, bee := range inv.ByHive[hive] {
		if bee.ID == id {
			return bee, true
		}
	}
	return agents.Bee{}, false
}
