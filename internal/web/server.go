// Package web serves BeeChief's browser interface.
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"sync"
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

//go:embed static
var staticFS embed.FS

// Backend is the set of operations the web UI can invoke.
type Backend interface {
	Scan(ctx context.Context) *agents.Inventory
	GetConfig() config.Config
	ConfigPath() string
	SaveConfig(config.Config) error

	ListSkills(ctx context.Context, bee agents.Bee) (*openclaw.SkillListReport, error)
	CheckSkills(ctx context.Context, bee agents.Bee) (*openclaw.SkillCheckReport, error)
	SelfReview(ctx context.Context, bee agents.Bee) (*openclaw.SelfReviewReport, error)
	ListWorkshop(ctx context.Context, bee agents.Bee) (*openclaw.WorkshopListReport, error)
	ShowWorkshopProposal(ctx context.Context, bee agents.Bee, name string) (*openclaw.WorkshopProposal, error)
	ModelsStatus(ctx context.Context, bee agents.Bee) (*openclaw.ModelStatusReport, error)
	SendInstruction(ctx context.Context, bee agents.Bee, message string) (string, error)

	RunHiveHealth(ctx context.Context, hive string) (*diagnostics.HiveHealthReport, error)
	InspectServices(ctx context.Context, hive string) (*services.HiveServicesReport, error)
	InspectSystemdUnit(ctx context.Context, hive, unit string) (services.SystemdUnit, error)
	ControlSystemdService(ctx context.Context, hive, unit string, action services.ServiceAction, confirmed bool) error
	FetchDockerLogs(ctx context.Context, hive, container string, tail int) (string, error)
	FetchLogs(ctx context.Context, hive string, source logs.SourceKind, target string, tail int) (*logs.Result, error)

	StartSwarmReview(mode swarm.Mode, bees []agents.Bee, onProgress func(swarm.Snapshot)) *swarm.Coordinator
	ExportHoneyReport(ctx context.Context, inv *agents.Inventory, path string) (string, error)
}

// Options configures the web server.
type Options struct {
	ReadOnly bool
	Version  string
	Jobs     *jobs.Manager
}

// Server serves the BeeChief web UI and JSON API.
type Server struct {
	backend  Backend
	jobs     *jobs.Manager
	readOnly bool
	version  string
	token    string
	static   fs.FS
	mux      *http.ServeMux

	mu        sync.Mutex
	scanMu    sync.Mutex
	inventory *agents.Inventory
	swarms    map[string]*swarmRun
	swarmSeq  int
}

// New builds a web server. It returns an error if the embedded assets are
// unavailable or a session token cannot be generated.
func New(backend Backend, opts Options) (*Server, error) {
	if backend == nil {
		return nil, errors.New("web backend is required")
	}
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("web assets: %w", err)
	}
	s := &Server{
		backend:  backend,
		jobs:     opts.Jobs,
		readOnly: opts.ReadOnly,
		version:  opts.Version,
		token:    token,
		static:   sub,
		mux:      http.NewServeMux(),
		swarms:   map[string]*swarmRun{},
	}
	s.routes()
	return s, nil
}

func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate web token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// Handler returns the secured HTTP handler.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if !s.authorized(r) {
				writeError(w, http.StatusUnauthorized, errors.New("missing or invalid BeeChief token"))
				return
			}
			if !sameOrigin(r) {
				writeError(w, http.StatusForbidden, errors.New("cross-origin requests are not allowed"))
				return
			}
		}
		s.mux.ServeHTTP(w, r)
	})
}

// ListenAndServe runs the server until ctx is canceled.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		<-errCh
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) authorized(r *http.Request) bool {
	got := r.Header.Get("X-BeeChief-Token")
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(s.static))))

	s.mux.HandleFunc("GET /api/meta", s.handleMeta)
	s.mux.HandleFunc("POST /api/scan", s.handleScan)
	s.mux.HandleFunc("GET /api/inventory", s.handleInventory)

	s.mux.HandleFunc("GET /api/bee/skills", s.handleSkills)
	s.mux.HandleFunc("GET /api/bee/check", s.handleSkillCheck)
	s.mux.HandleFunc("POST /api/bee/self-review", s.handleSelfReview)
	s.mux.HandleFunc("GET /api/bee/workshop", s.handleWorkshop)
	s.mux.HandleFunc("GET /api/bee/workshop/proposal", s.handleWorkshopProposal)
	s.mux.HandleFunc("GET /api/bee/models", s.handleModels)
	s.mux.HandleFunc("POST /api/bee/instruction", s.handleInstruction)

	s.mux.HandleFunc("GET /api/hive/health", s.handleHiveHealth)
	s.mux.HandleFunc("GET /api/hive/services", s.handleServices)
	s.mux.HandleFunc("GET /api/hive/unit", s.handleUnit)
	s.mux.HandleFunc("POST /api/hive/unit/control", s.handleUnitControl)
	s.mux.HandleFunc("GET /api/hive/docker-logs", s.handleDockerLogs)
	s.mux.HandleFunc("GET /api/hive/logs", s.handleLogs)
	s.mux.HandleFunc("POST /api/hive/test", s.handleHiveTest)

	s.mux.HandleFunc("GET /api/config", s.handleGetConfig)
	s.mux.HandleFunc("PUT /api/config", s.handleSaveConfig)
	s.mux.HandleFunc("POST /api/report", s.handleReport)

	s.mux.HandleFunc("POST /api/swarm", s.handleSwarmStart)
	s.mux.HandleFunc("GET /api/swarm", s.handleSwarmStatus)
	s.mux.HandleFunc("POST /api/swarm/cancel", s.handleSwarmCancel)

	s.mux.HandleFunc("GET /api/jobs", s.handleJobs)
	s.mux.HandleFunc("POST /api/jobs", s.handleJobUpsert)
	s.mux.HandleFunc("DELETE /api/jobs", s.handleJobDelete)
	s.mux.HandleFunc("POST /api/jobs/run", s.handleJobRun)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(s.static, "index.html")
	if err != nil {
		http.Error(w, "web UI unavailable", http.StatusInternalServerError)
		return
	}
	page := strings.ReplaceAll(string(data), "{{TOKEN}}", s.token)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(page))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorResponse{Error: err.Error()})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func queryBee(r *http.Request) (agents.Bee, error) {
	hive := strings.TrimSpace(r.URL.Query().Get("hive"))
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if hive == "" || id == "" {
		return agents.Bee{}, errors.New("hive and id query parameters are required")
	}
	return agents.Bee{Hive: hive, ID: id}, nil
}

func queryHive(r *http.Request) (string, error) {
	hive := strings.TrimSpace(r.URL.Query().Get("hive"))
	if hive == "" {
		return "", errors.New("hive query parameter is required")
	}
	return hive, nil
}

func queryTail(r *http.Request) int {
	var tail int
	if _, err := fmt.Sscanf(r.URL.Query().Get("tail"), "%d", &tail); err != nil || tail <= 0 {
		tail = logs.DefaultTail
	}
	if tail > logs.MaxTail {
		tail = logs.MaxTail
	}
	return tail
}

func querySource(r *http.Request) (logs.SourceKind, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("source"))
	if raw == "" {
		return logs.SourceJournalctl, nil
	}
	for _, source := range logs.AllSources {
		if string(source) == raw {
			return source, nil
		}
	}
	return "", fmt.Errorf("unknown log source %q", raw)
}
