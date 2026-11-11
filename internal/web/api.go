package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/config"
	"github.com/meistro57/beechief/internal/hosts"
	"github.com/meistro57/beechief/internal/jobs"
	"github.com/meistro57/beechief/internal/openclaw"
	"github.com/meistro57/beechief/internal/services"
)

type hiveView struct {
	Name       string       `json:"name"`
	OK         bool         `json:"ok"`
	Error      string       `json:"error,omitempty"`
	DurationMs int64        `json:"durationMs"`
	Bees       []agents.Bee `json:"bees"`
}

type inventoryResponse struct {
	Hives  []hiveView `json:"hives"`
	Total  int        `json:"total"`
	Failed int        `json:"failed"`
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":    s.version,
		"configPath": s.backend.ConfigPath(),
		"readOnly":   s.readOnly,
		"jobsPath":   s.jobsPath(),
	})
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	if !s.scanMu.TryLock() {
		writeError(w, http.StatusConflict, errors.New("a scan is already running"))
		return
	}
	defer s.scanMu.Unlock()
	inv := s.backend.Scan(r.Context())
	s.mu.Lock()
	s.inventory = inv
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, buildInventory(inv))
}

func (s *Server) handleInventory(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	inv := s.inventory
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, buildInventory(inv))
}

func buildInventory(inv *agents.Inventory) inventoryResponse {
	resp := inventoryResponse{Hives: []hiveView{}}
	if inv == nil {
		return resp
	}
	for _, hive := range inv.Hives {
		st := inv.Status[hive]
		view := hiveView{
			Name:       hive,
			OK:         st.OK(),
			DurationMs: st.Duration.Milliseconds(),
			Bees:       append([]agents.Bee{}, inv.ByHive[hive]...),
		}
		if st.Err != nil {
			view.Error = st.Err.Error()
			resp.Failed++
		}
		resp.Total += len(inv.ByHive[hive])
		resp.Hives = append(resp.Hives, view)
	}
	return resp
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	bee, err := queryBee(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rep, err := s.backend.ListSkills(r.Context(), bee)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleSkillCheck(w http.ResponseWriter, r *http.Request) {
	bee, err := queryBee(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rep, err := s.backend.CheckSkills(r.Context(), bee)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleSelfReview(w http.ResponseWriter, r *http.Request) {
	if s.readOnly {
		writeError(w, http.StatusForbidden, errors.New("web UI is read-only"))
		return
	}
	bee, err := queryBee(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rep, err := s.backend.SelfReview(r.Context(), bee)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleWorkshop(w http.ResponseWriter, r *http.Request) {
	bee, err := queryBee(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rep, err := s.backend.ListWorkshop(r.Context(), bee)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleWorkshopProposal(w http.ResponseWriter, r *http.Request) {
	bee, err := queryBee(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("name query parameter is required"))
		return
	}
	prop, err := s.backend.ShowWorkshopProposal(r.Context(), bee, name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, prop)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	bee, err := queryBee(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rep, err := s.backend.ModelsStatus(r.Context(), bee)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

type instructionRequest struct {
	Hive    string `json:"hive"`
	ID      string `json:"id"`
	Message string `json:"message"`
}

func (s *Server) handleInstruction(w http.ResponseWriter, r *http.Request) {
	if s.readOnly {
		writeError(w, http.StatusForbidden, errors.New("web UI is read-only"))
		return
	}
	var req instructionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	bee := agents.Bee{Hive: strings.TrimSpace(req.Hive), ID: strings.TrimSpace(req.ID)}
	if bee.Hive == "" || bee.ID == "" || strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest, errors.New("hive, id, and message are required"))
		return
	}
	reply, err := s.backend.SendInstruction(r.Context(), bee, req.Message)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"reply": reply})
}

func (s *Server) handleHiveHealth(w http.ResponseWriter, r *http.Request) {
	hive, err := queryHive(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rep, err := s.backend.RunHiveHealth(r.Context(), hive)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	hive, err := queryHive(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rep, err := s.backend.InspectServices(r.Context(), hive)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleUnit(w http.ResponseWriter, r *http.Request) {
	hive, err := queryHive(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	unit := strings.TrimSpace(r.URL.Query().Get("unit"))
	if unit == "" {
		writeError(w, http.StatusBadRequest, errors.New("unit query parameter is required"))
		return
	}
	detail, err := s.backend.InspectSystemdUnit(r.Context(), hive, unit)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

type unitControlRequest struct {
	Hive      string `json:"hive"`
	Unit      string `json:"unit"`
	Action    string `json:"action"`
	Confirmed bool   `json:"confirmed"`
}

func (s *Server) handleUnitControl(w http.ResponseWriter, r *http.Request) {
	if s.readOnly {
		writeError(w, http.StatusForbidden, errors.New("web UI is read-only"))
		return
	}
	var req unitControlRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	action := services.ServiceAction(strings.TrimSpace(req.Action))
	err := s.backend.ControlSystemdService(r.Context(), req.Hive, req.Unit, action, req.Confirmed)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDockerLogs(w http.ResponseWriter, r *http.Request) {
	hive, err := queryHive(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	container := strings.TrimSpace(r.URL.Query().Get("container"))
	if container == "" {
		writeError(w, http.StatusBadRequest, errors.New("container query parameter is required"))
		return
	}
	out, err := s.backend.FetchDockerLogs(r.Context(), hive, container, queryTail(r))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": out})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	hive, err := queryHive(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	source, err := querySource(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	target := strings.TrimSpace(r.URL.Query().Get("target"))
	res, err := s.backend.FetchLogs(r.Context(), hive, source, target, queryTail(r))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type hiveTestResult struct {
	Hive            string `json:"hive"`
	Reachable       bool   `json:"reachable"`
	OpenClawPresent bool   `json:"openClawPresent"`
	OpenClawVersion string `json:"openClawVersion,omitempty"`
	Err             string `json:"err,omitempty"`
}

func (s *Server) handleHiveTest(w http.ResponseWriter, r *http.Request) {
	var h config.Hive
	if err := decodeJSON(w, r, &h); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, testHive(r.Context(), h))
}

func testHive(ctx context.Context, h config.Hive) hiveTestResult {
	res := hiveTestResult{Hive: h.Name}
	if err := h.Validate(); err != nil {
		res.Err = err.Error()
		return res
	}
	exec, err := hosts.ForHive(h)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	pr := hosts.Probe(ctx, exec)
	if !pr.Reachable {
		if pr.Err != nil {
			res.Err = pr.Err.Error()
		} else {
			res.Err = "host unreachable"
		}
		return res
	}
	res.Reachable = true
	ver, err := openclaw.NewClient(exec).Version(ctx)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.OpenClawPresent = true
	res.OpenClawVersion = ver
	return res
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"path":   s.backend.ConfigPath(),
		"config": s.backend.GetConfig(),
	})
}

func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	if s.readOnly {
		writeError(w, http.StatusForbidden, errors.New("web UI is read-only"))
		return
	}
	var cfg config.Config
	if err := decodeJSON(w, r, &cfg); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.backend.SaveConfig(cfg); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "saved", "config": cfg})
}

type reportRequest struct {
	Path string `json:"path"`
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	var req reportRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	s.mu.Lock()
	inv := s.inventory
	s.mu.Unlock()
	if inv == nil {
		s.scanMu.Lock()
		s.mu.Lock()
		inv = s.inventory
		s.mu.Unlock()
		if inv == nil {
			inv = s.backend.Scan(r.Context())
			s.mu.Lock()
			s.inventory = inv
			s.mu.Unlock()
		}
		s.scanMu.Unlock()
	}
	path, err := s.backend.ExportHoneyReport(r.Context(), inv, strings.TrimSpace(req.Path))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("jobs are not configured"))
		return
	}
	writeJSON(w, http.StatusOK, s.jobs.List())
}

func (s *Server) handleJobUpsert(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("jobs are not configured"))
		return
	}
	if s.readOnly {
		writeError(w, http.StatusForbidden, errors.New("web UI is read-only"))
		return
	}
	var job jobs.Job
	if err := decodeJSON(w, r, &job); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.jobs.Upsert(job); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.jobs.List())
}

func (s *Server) handleJobDelete(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("jobs are not configured"))
		return
	}
	if s.readOnly {
		writeError(w, http.StatusForbidden, errors.New("web UI is read-only"))
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("name query parameter is required"))
		return
	}
	if err := s.jobs.Delete(name); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.jobs.List())
}

func (s *Server) handleJobRun(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("jobs are not configured"))
		return
	}
	if s.readOnly {
		writeError(w, http.StatusForbidden, errors.New("web UI is read-only"))
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("name query parameter is required"))
		return
	}
	if err := s.jobs.Trigger(r.Context(), name); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) jobsPath() string {
	if s.jobs == nil {
		return ""
	}
	return s.jobs.Path()
}
