package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/meistro57/beechief/internal/events"
	"github.com/meistro57/beechief/internal/jobs"
)

// RunJob executes one scheduled operation against the configured fleet.
func (b *backend) RunJob(ctx context.Context, job jobs.Job) (jobs.Result, error) {
	res := jobs.Result{Job: job.Name, Kind: job.Kind, StartedAt: time.Now()}
	var hardErr error

	switch job.Kind {
	case jobs.KindScan:
		inv := b.Scan(ctx)
		failed := inv.Failed()
		res.Summary = fmt.Sprintf("Scanned %d Hive(s): %d Bee(s) discovered, %d failed",
			len(inv.Hives), inv.Total(), len(failed))
		if len(failed) > 0 {
			res.Err = fmt.Sprintf("%d Hive(s) failed", len(failed))
		}

	case jobs.KindHealth:
		hives := b.jobHives(job.Hive)
		if len(hives) == 0 {
			if job.Hive != "" {
				res.Err = fmt.Sprintf("Hive %q is not configured", job.Hive)
			} else {
				res.Err = "no Hives configured"
			}
			hardErr = errors.New(res.Err)
			break
		}
		var pass, warn, fail, unavail int
		var failures []string
		for _, hive := range hives {
			rep, err := b.RunHiveHealth(ctx, hive)
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", hive, err))
				continue
			}
			p, w, f, u := rep.Counts()
			pass += p
			warn += w
			fail += f
			unavail += u
			if f > 0 {
				failures = append(failures, fmt.Sprintf("%s: %d failing check(s)", hive, f))
			}
		}
		res.Summary = fmt.Sprintf("Hive Health across %d Hive(s): pass=%d warn=%d fail=%d unavailable=%d",
			len(hives), pass, warn, fail, unavail)
		if len(failures) > 0 {
			res.Err = strings.Join(failures, "; ")
		}

	case jobs.KindReport:
		inv := b.Scan(ctx)
		path, err := b.ExportHoneyReport(ctx, inv, job.Output)
		if err != nil {
			res.Err = err.Error()
			hardErr = err
			break
		}
		res.Summary = "Honey Report exported to " + path

	default:
		res.Err = fmt.Sprintf("unknown job kind %q", job.Kind)
		hardErr = errors.New(res.Err)
	}

	res.DurationMs = time.Since(res.StartedAt).Milliseconds()
	lvl := events.LevelInfo
	if hardErr != nil {
		lvl = events.LevelError
	} else if res.Err != "" {
		lvl = events.LevelWarn
	}
	if res.Summary == "" {
		res.Summary = "job completed"
	}
	_ = b.events.Record(events.Event{
		Kind:       events.KindJobRun,
		Level:      lvl,
		Hive:       job.Hive,
		Summary:    fmt.Sprintf("Job %s (%s): %s", job.Name, job.Kind, res.Summary),
		Detail:     res.Err,
		DurationMs: res.DurationMs,
	})
	return res, hardErr
}

func (b *backend) jobHives(target string) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if target != "" {
		for _, h := range b.cfg.Hives {
			if h.Name == target {
				return []string{target}
			}
		}
		return nil
	}
	out := make([]string, 0, len(b.cfg.Hives))
	for _, h := range b.cfg.Hives {
		out = append(out, h.Name)
	}
	return out
}
