package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/config"
	"github.com/meistro57/beechief/internal/diagnostics"
	"github.com/meistro57/beechief/internal/discovery"
	"github.com/meistro57/beechief/internal/jobs"
)

type scanHiveView struct {
	Name       string       `json:"name"`
	OK         bool         `json:"ok"`
	Error      string       `json:"error,omitempty"`
	Bees       []agents.Bee `json:"bees"`
	DurationMs int64        `json:"durationMs"`
}

type scanResponse struct {
	Hives  []scanHiveView `json:"hives"`
	Total  int            `json:"total"`
	Failed int            `json:"failed"`
}

func runScanCLI(cfg config.Config, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "print machine-readable JSON")
	allowFailures := fs.Bool("allow-failures", false, "exit zero even when a Hive fails")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("scan flags: %w", err)
	}

	inv := discovery.FromConfig(cfg).Scan(context.Background())
	resp := scanResponse{Hives: make([]scanHiveView, 0, len(inv.Hives))}
	for _, hive := range inv.Hives {
		st := inv.Status[hive]
		view := scanHiveView{
			Name:       hive,
			OK:         st.OK(),
			Bees:       inv.ByHive[hive],
			DurationMs: st.Duration.Milliseconds(),
		}
		if st.Err != nil {
			view.Error = st.Err.Error()
			resp.Failed++
		}
		resp.Total += len(inv.ByHive[hive])
		resp.Hives = append(resp.Hives, view)
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("encode scan: %w", err)
		}
	} else {
		for _, hive := range resp.Hives {
			if hive.OK {
				fmt.Fprintf(stdout, "Hive %s: ok (%d Bees, %dms)\n", hive.Name, len(hive.Bees), hive.DurationMs)
			} else {
				fmt.Fprintf(stdout, "Hive %s: FAILED (%s)\n", hive.Name, hive.Error)
			}
		}
		fmt.Fprintf(stdout, "Total: %d Bee(s) across %d Hive(s), %d failed\n", resp.Total, len(resp.Hives), resp.Failed)
	}

	if resp.Failed > 0 && !*allowFailures {
		return fmt.Errorf("%d Hive(s) failed", resp.Failed)
	}
	return nil
}

func runHealthCLI(cfg config.Config, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	hiveName := fs.String("hive", "", "only check the named Hive")
	asJSON := fs.Bool("json", false, "print machine-readable JSON")
	failOn := fs.String("fail-on", "fail", "exit non-zero on: none, warning, or fail")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("health flags: %w", err)
	}
	threshold := strings.ToLower(strings.TrimSpace(*failOn))
	switch threshold {
	case "none", "warning", "fail":
	default:
		return fmt.Errorf("health: --fail-on must be none, warning, or fail")
	}

	hives := make([]string, 0, len(cfg.Hives))
	for _, h := range cfg.Hives {
		hives = append(hives, h.Name)
	}
	if *hiveName != "" {
		found := false
		for _, h := range hives {
			if h == *hiveName {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("health: Hive %q is not configured", *hiveName)
		}
		hives = []string{*hiveName}
	}
	if len(hives) == 0 {
		if *asJSON {
			_, _ = io.WriteString(stdout, "[]\n")
		} else {
			fmt.Fprintln(stdout, "No Hives configured.")
		}
		return nil
	}

	execs := buildExecMap(cfg)
	reports := make([]*diagnostics.HiveHealthReport, 0, len(hives))
	var totalPass, totalWarn, totalFail, totalUnavailable int
	for _, hive := range hives {
		rep := diagnostics.Run(context.Background(), hive, execs[hive])
		reports = append(reports, rep)
		p, w, f, u := rep.Counts()
		totalPass += p
		totalWarn += w
		totalFail += f
		totalUnavailable += u
	}

	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(reports); err != nil {
			return fmt.Errorf("encode health: %w", err)
		}
	} else {
		for _, rep := range reports {
			p, w, f, u := rep.Counts()
			fmt.Fprintf(stdout, "Hive %s: %d pass, %d warn, %d fail, %d unavailable (%s)\n",
				rep.Hive, p, w, f, u, rep.Duration.Round(time.Millisecond))
			for _, check := range rep.SortedBySeverity() {
				if check.State == diagnostics.StatePass {
					continue
				}
				fmt.Fprintf(stdout, "  [%s] %s: %s\n", strings.ToUpper(string(check.State)), check.Name, check.Summary)
			}
		}
	}

	failed := false
	switch threshold {
	case "fail":
		failed = totalFail > 0
	case "warning":
		failed = totalFail > 0 || totalWarn > 0
	}
	if failed {
		return fmt.Errorf("Hive Health: pass=%d warn=%d fail=%d unavailable=%d",
			totalPass, totalWarn, totalFail, totalUnavailable)
	}
	return nil
}

func runCronCLI(cfgPath string, cfg config.Config, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("cron", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	file := fs.String("file", jobs.DefaultPath(), "jobs YAML file")
	once := fs.Bool("once", false, "run every enabled job once and exit")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("cron flags: %w", err)
	}

	backend := newBackend(cfgPath, cfg)
	defer backend.Close()

	manager, err := jobs.NewManager(*file, backend.RunJob)
	if err != nil {
		return err
	}

	if *once {
		results, err := manager.RunAllOnce(context.Background())
		if len(results) == 0 {
			fmt.Fprintln(stdout, "No enabled jobs.")
		}
		for _, res := range results {
			status := res.Summary
			if res.Err != "" {
				status = res.Err
			}
			fmt.Fprintf(stdout, "Job %s (%s): %s (%dms)\n", res.Job, res.Kind, status, res.DurationMs)
		}
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := manager.Start(ctx); err != nil {
		return err
	}
	defer manager.Stop()
	fmt.Fprintf(stdout, "Job scheduler started with %d job(s) from %s; press Ctrl+C to stop\n",
		len(manager.List()), *file)
	<-ctx.Done()
	return nil
}
