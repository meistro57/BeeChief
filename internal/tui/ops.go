package tui

import (
	"context"

	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/config"
	"github.com/meistro57/beechief/internal/diagnostics"
	"github.com/meistro57/beechief/internal/logs"
	"github.com/meistro57/beechief/internal/openclaw"
	"github.com/meistro57/beechief/internal/services"
	"github.com/meistro57/beechief/internal/swarm"
)

// HiveTestResult reports the outcome of testing a Hive's connectivity and
// OpenClaw installation from the configuration wizard (issue #12).
type HiveTestResult struct {
	Hive            string
	Reachable       bool
	OpenClawPresent bool
	OpenClawVersion string
	Err             string
}

// Ops bundles all backend operations the TUI can trigger asynchronously.
// Keeping these behind an interface ensures Bubble Tea views never construct
// shell commands directly and allows deterministic unit testing of every
// screen and state transition.
type Ops interface {
	// Comb / Skills (issue #5)
	ListSkills(ctx context.Context, bee agents.Bee) (*openclaw.SkillListReport, error)
	CheckSkills(ctx context.Context, bee agents.Bee) (*openclaw.SkillCheckReport, error)

	// Self-Review, Workshop, & Custom Instructions (issues #4, #6)
	SelfReview(ctx context.Context, bee agents.Bee) (*openclaw.SelfReviewReport, error)
	ListWorkshop(ctx context.Context, bee agents.Bee) (*openclaw.WorkshopListReport, error)
	ShowWorkshopProposal(ctx context.Context, bee agents.Bee, name string) (*openclaw.WorkshopProposal, error)
	ModelsStatus(ctx context.Context, bee agents.Bee) (*openclaw.ModelStatusReport, error)
	SendInstruction(ctx context.Context, bee agents.Bee, message string) (string, error)

	// Hive Health quick diagnostics (issue #7)
	RunHiveHealth(ctx context.Context, hive string) (*diagnostics.HiveHealthReport, error)

	// Services & Model Troubleshooting (issue #8)
	InspectServices(ctx context.Context, hive string) (*services.HiveServicesReport, error)
	InspectSystemdUnit(ctx context.Context, hive, unit string) (services.SystemdUnit, error)
	ControlSystemdService(ctx context.Context, hive, unit string, action services.ServiceAction, confirmed bool) error
	FetchDockerLogs(ctx context.Context, hive, container string, tail int) (string, error)

	// Unified Log Browser (issue #9)
	FetchLogs(ctx context.Context, hive string, source logs.SourceKind, target string, tail int) (*logs.Result, error)

	// Swarm Review across selected or all Bees (issue #10)
	StartSwarmReview(mode swarm.Mode, bees []agents.Bee, onProgress func(swarm.Snapshot)) *swarm.Coordinator

	// Honey Report export (issue #11)
	ExportHoneyReport(ctx context.Context, inv *agents.Inventory, path string) (string, error)

	// Hive Configuration Wizard (issue #12)
	GetConfig() config.Config
	ConfigPath() string
	TestHive(ctx context.Context, h config.Hive) HiveTestResult
	SaveConfig(cfg config.Config) error
}

// WithOps injects the operational backend into the TUI Model.
func WithOps(ops Ops) Option {
	return func(m *Model) { m.ops = ops }
}
