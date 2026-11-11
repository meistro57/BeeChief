package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/config"
	"github.com/meistro57/beechief/internal/diagnostics"
	"github.com/meistro57/beechief/internal/jobs"
	"github.com/meistro57/beechief/internal/logs"
	"github.com/meistro57/beechief/internal/openclaw"
	"github.com/meistro57/beechief/internal/services"
	"github.com/meistro57/beechief/internal/swarm"
)

type fakeBackend struct {
	inv *agents.Inventory
	cfg config.Config
}

func (f *fakeBackend) Scan(context.Context) *agents.Inventory {
	if f.inv == nil {
		return agents.NewInventory()
	}
	return f.inv
}
func (f *fakeBackend) GetConfig() config.Config         { return f.cfg }
func (f *fakeBackend) ConfigPath() string               { return "/tmp/hosts.yaml" }
func (f *fakeBackend) SaveConfig(c config.Config) error { f.cfg = c; return nil }
func (f *fakeBackend) ListSkills(_ context.Context, bee agents.Bee) (*openclaw.SkillListReport, error) {
	return &openclaw.SkillListReport{AgentID: bee.ID, Skills: []openclaw.Skill{{Name: "demo", Eligible: true}}}, nil
}
func (f *fakeBackend) CheckSkills(_ context.Context, bee agents.Bee) (*openclaw.SkillCheckReport, error) {
	return &openclaw.SkillCheckReport{AgentID: bee.ID, Summary: openclaw.SkillCheckSummary{Total: 1, Eligible: 1}}, nil
}
func (f *fakeBackend) SelfReview(_ context.Context, bee agents.Bee) (*openclaw.SelfReviewReport, error) {
	return &openclaw.SelfReviewReport{AgentID: bee.ID, Summary: "reviewed"}, nil
}
func (f *fakeBackend) ListWorkshop(_ context.Context, bee agents.Bee) (*openclaw.WorkshopListReport, error) {
	return &openclaw.WorkshopListReport{AgentID: bee.ID, Proposals: []openclaw.WorkshopProposal{{Name: "auto"}}}, nil
}
func (f *fakeBackend) ShowWorkshopProposal(_ context.Context, _ agents.Bee, name string) (*openclaw.WorkshopProposal, error) {
	return &openclaw.WorkshopProposal{Name: name, Content: "# " + name}, nil
}
func (f *fakeBackend) ModelsStatus(_ context.Context, bee agents.Bee) (*openclaw.ModelStatusReport, error) {
	return &openclaw.ModelStatusReport{DefaultModel: "test/model"}, nil
}
func (f *fakeBackend) SendInstruction(_ context.Context, _ agents.Bee, _ string) (string, error) {
	return "ok", nil
}
func (f *fakeBackend) RunHiveHealth(_ context.Context, hive string) (*diagnostics.HiveHealthReport, error) {
	return &diagnostics.HiveHealthReport{Hive: hive, Checks: []diagnostics.CheckResult{{Name: "Reachability", State: diagnostics.StatePass, Summary: "ok"}}}, nil
}
func (f *fakeBackend) InspectServices(_ context.Context, hive string) (*services.HiveServicesReport, error) {
	return &services.HiveServicesReport{Hive: hive}, nil
}
func (f *fakeBackend) InspectSystemdUnit(_ context.Context, _, unit string) (services.SystemdUnit, error) {
	return services.SystemdUnit{Name: unit}, nil
}
func (f *fakeBackend) ControlSystemdService(context.Context, string, string, services.ServiceAction, bool) error {
	return nil
}
func (f *fakeBackend) FetchDockerLogs(context.Context, string, string, int) (string, error) {
	return "docker logs", nil
}
func (f *fakeBackend) FetchLogs(context.Context, string, logs.SourceKind, string, int) (*logs.Result, error) {
	return &logs.Result{Entries: []logs.Entry{{Line: "hello", Level: logs.LevelInfo}}}, nil
}
func (f *fakeBackend) StartSwarmReview(mode swarm.Mode, bees []agents.Bee, onProgress func(swarm.Snapshot)) *swarm.Coordinator {
	review := func(context.Context, agents.Bee) (*openclaw.SelfReviewReport, error) {
		return &openclaw.SelfReviewReport{Summary: "reviewed"}, nil
	}
	return swarm.NewCoordinator(mode, bees, review, 1, onProgress)
}
func (f *fakeBackend) ExportHoneyReport(context.Context, *agents.Inventory, string) (string, error) {
	return "/tmp/honey.md", nil
}

func testInventory() *agents.Inventory {
	inv := agents.NewInventory()
	inv.Record(agents.HiveStatus{Hive: "alpha"}, []agents.Bee{{ID: "main", Name: "Main"}})
	return inv
}

func newTestServer(t *testing.T, backend *fakeBackend, readOnly bool, manager *jobs.Manager) *Server {
	t.Helper()
	s, err := New(backend, Options{Version: "test", ReadOnly: readOnly, Jobs: manager})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func doRequest(t *testing.T, s *Server, method, path string, body any, token bool) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if token {
		req.Header.Set("X-BeeChief-Token", s.token)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestAPIRequiresTokenAndSameOrigin(t *testing.T) {
	s := newTestServer(t, &fakeBackend{}, false, nil)
	if rec := doRequest(t, s, http.MethodGet, "/api/meta", nil, false); rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token: got %d", rec.Code)
	}
	if rec := doRequest(t, s, http.MethodGet, "/api/meta", nil, true); rec.Code != http.StatusOK {
		t.Errorf("valid token: got %d: %s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/meta", nil)
	req.Header.Set("X-BeeChief-Token", s.token)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin request: got %d", rec.Code)
	}
}

func TestIndexServesTokenAndCSP(t *testing.T) {
	s := newTestServer(t, &fakeBackend{}, false, nil)
	rec := doRequest(t, s, http.MethodGet, "/", nil, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("index: got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), s.token) {
		t.Error("index does not contain the session token")
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("missing CSP header")
	}
	rec = doRequest(t, s, http.MethodGet, "/static/app.js", nil, false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "beechief-token") {
		t.Errorf("static app.js: got %d", rec.Code)
	}
}

func TestScanAndBeeEndpoints(t *testing.T) {
	backend := &fakeBackend{inv: testInventory()}
	s := newTestServer(t, backend, false, nil)

	rec := doRequest(t, s, http.MethodPost, "/api/scan", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("scan: got %d: %s", rec.Code, rec.Body.String())
	}
	var scan inventoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &scan); err != nil {
		t.Fatal(err)
	}
	if scan.Total != 1 || len(scan.Hives) != 1 {
		t.Errorf("unexpected scan response: %+v", scan)
	}

	rec = doRequest(t, s, http.MethodGet, "/api/bee/skills?hive=alpha&id=main", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("skills: got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "demo") {
		t.Errorf("unexpected skills response: %s", rec.Body.String())
	}
}

func TestReadOnlyBlocksMutations(t *testing.T) {
	s := newTestServer(t, &fakeBackend{}, true, nil)
	rec := doRequest(t, s, http.MethodPost, "/api/bee/self-review?hive=alpha&id=main", nil, true)
	if rec.Code != http.StatusForbidden {
		t.Errorf("self-review in read-only: got %d", rec.Code)
	}
}

func TestJobsAPI(t *testing.T) {
	manager, err := jobs.NewManager(filepath.Join(t.TempDir(), "jobs.yaml"), func(context.Context, jobs.Job) (jobs.Result, error) {
		return jobs.Result{Summary: "done"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, &fakeBackend{}, false, manager)

	rec := doRequest(t, s, http.MethodPost, "/api/jobs", jobs.Job{
		Name: "nightly", Kind: jobs.KindScan, Schedule: "0 3 * * *",
	}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert job: got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, s, http.MethodGet, "/api/jobs", nil, true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "nightly") {
		t.Fatalf("list jobs: got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, s, http.MethodPost, "/api/jobs/run?name=nightly", nil, true)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("run job: got %d: %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for {
		rec = doRequest(t, s, http.MethodGet, "/api/jobs", nil, true)
		if !strings.Contains(rec.Body.String(), `"running":true`) || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec = doRequest(t, s, http.MethodDelete, "/api/jobs?name=nightly", nil, true)
	if rec.Code != http.StatusOK {
		t.Errorf("delete job: got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSwarmRequiresScanAndReportsProgress(t *testing.T) {
	backend := &fakeBackend{inv: testInventory()}
	s := newTestServer(t, backend, false, nil)

	rec := doRequest(t, s, http.MethodPost, "/api/swarm", map[string]any{"mode": "all_reachable"}, true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("swarm before scan: got %d", rec.Code)
	}
	if rec = doRequest(t, s, http.MethodPost, "/api/scan", nil, true); rec.Code != http.StatusOK {
		t.Fatalf("scan: got %d", rec.Code)
	}
	rec = doRequest(t, s, http.MethodPost, "/api/swarm", map[string]any{"mode": "all_reachable"}, true)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("swarm start: got %d: %s", rec.Code, rec.Body.String())
	}
	var start struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &start); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		rec = doRequest(t, s, http.MethodGet, "/api/swarm?id="+start.ID, nil, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("swarm status: got %d: %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), `"done":true`) || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLogsAndReportEndpoints(t *testing.T) {
	backend := &fakeBackend{inv: testInventory()}
	s := newTestServer(t, backend, false, nil)
	if rec := doRequest(t, s, http.MethodPost, "/api/scan", nil, true); rec.Code != http.StatusOK {
		t.Fatalf("scan: got %d", rec.Code)
	}
	rec := doRequest(t, s, http.MethodGet, "/api/hive/logs?hive=alpha&source=events&target=", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("logs: got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"entries"`) {
		t.Errorf("logs response missing entries: %s", rec.Body.String())
	}
	rec = doRequest(t, s, http.MethodPost, "/api/report", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("report: got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "/tmp/honey.md") {
		t.Errorf("unexpected report response: %s", rec.Body.String())
	}
}
