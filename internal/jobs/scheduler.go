package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const schedulerTick = 30 * time.Second

// Runner executes one job.
type Runner func(context.Context, Job) (Result, error)

// Manager owns job definitions, the in-process scheduler, and run state.
type Manager struct {
	mu     sync.Mutex
	path   string
	runner Runner

	jobs      []Job
	schedules map[string]Schedule
	next      map[string]time.Time
	last      map[string]Result
	lastRun   map[string]time.Time
	running   map[string]bool

	stop context.CancelFunc
	wg   sync.WaitGroup
	sem  chan struct{}
}

// NewManager loads jobs from path and prepares them for scheduling.
// A missing file produces an empty manager.
func NewManager(path string, runner Runner) (*Manager, error) {
	f, err := Load(path)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		path:      path,
		runner:    runner,
		jobs:      f.Jobs,
		schedules: map[string]Schedule{},
		next:      map[string]time.Time{},
		last:      map[string]Result{},
		lastRun:   map[string]time.Time{},
		running:   map[string]bool{},
		sem:       make(chan struct{}, 2),
	}
	m.mu.Lock()
	m.rebuildLocked()
	m.mu.Unlock()
	return m, nil
}

// Path returns the jobs file path.
func (m *Manager) Path() string {
	if m == nil {
		return ""
	}
	return m.path
}

// Start begins scheduling jobs until ctx is canceled or Stop is called.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.stop != nil {
		m.mu.Unlock()
		return errors.New("job scheduler is already running")
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.stop = cancel
	m.mu.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(schedulerTick)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case now := <-ticker.C:
				m.runDue(runCtx, now)
			}
		}
	}()
	return nil
}

// Stop halts the scheduler and waits for in-flight jobs.
func (m *Manager) Stop() {
	m.mu.Lock()
	cancel := m.stop
	m.stop = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
}

// List returns the scheduler state in file order.
func (m *Manager) List() []Status {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.jobs))
	for _, job := range m.jobs {
		sched := m.schedules[job.Name]
		out = append(out, Status{
			Job:      job,
			NextRun:  m.next[job.Name],
			LastRun:  m.lastRun[job.Name],
			Running:  m.running[job.Name],
			Last:     m.last[job.Name],
			Schedule: sched.String(),
		})
	}
	return out
}

// Upsert validates, persists, and schedules job.
func (m *Manager) Upsert(job Job) error {
	job = normalizeJob(job)
	if err := job.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	next := append([]Job(nil), m.jobs...)
	replaced := false
	for i := range next {
		if next[i].Name == job.Name {
			next[i] = job
			replaced = true
			break
		}
	}
	if !replaced {
		next = append(next, job)
	}
	if err := m.saveLocked(next); err != nil {
		return err
	}
	m.jobs = next
	m.rebuildLocked()
	return nil
}

// Delete removes a job definition and its schedule.
func (m *Manager) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running[name] {
		return fmt.Errorf("job %q is running", name)
	}
	next := make([]Job, 0, len(m.jobs))
	found := false
	for _, job := range m.jobs {
		if job.Name == name {
			found = true
			continue
		}
		next = append(next, job)
	}
	if !found {
		return fmt.Errorf("job %q not found", name)
	}
	if err := m.saveLocked(next); err != nil {
		return err
	}
	m.jobs = next
	delete(m.schedules, name)
	delete(m.next, name)
	delete(m.last, name)
	delete(m.lastRun, name)
	delete(m.running, name)
	return nil
}

// RunNow runs a job synchronously and returns its result.
func (m *Manager) RunNow(ctx context.Context, name string) (Result, error) {
	if m == nil {
		return Result{}, errors.New("jobs are not configured")
	}
	m.mu.Lock()
	job, ok := m.findLocked(name)
	if !ok {
		m.mu.Unlock()
		return Result{}, fmt.Errorf("job %q not found", name)
	}
	if m.running[name] {
		m.mu.Unlock()
		return Result{}, fmt.Errorf("job %q is already running", name)
	}
	m.running[name] = true
	m.mu.Unlock()
	return m.execute(ctx, job), nil
}

// Trigger starts a job in the background and returns immediately.
func (m *Manager) Trigger(ctx context.Context, name string) error {
	if m == nil {
		return errors.New("jobs are not configured")
	}
	m.mu.Lock()
	job, ok := m.findLocked(name)
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("job %q not found", name)
	}
	if m.running[name] {
		m.mu.Unlock()
		return fmt.Errorf("job %q is already running", name)
	}
	m.running[name] = true
	m.mu.Unlock()

	runCtx := context.WithoutCancel(ctx)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.execute(runCtx, job)
	}()
	return nil
}

// RunAllOnce runs every enabled job once, sequentially.
func (m *Manager) RunAllOnce(ctx context.Context) ([]Result, error) {
	if m == nil {
		return nil, errors.New("jobs are not configured")
	}
	m.mu.Lock()
	names := make([]string, 0, len(m.jobs))
	for _, job := range m.jobs {
		if !job.Disabled {
			names = append(names, job.Name)
		}
	}
	m.mu.Unlock()

	var results []Result
	var errs []error
	for _, name := range names {
		res, err := m.RunNow(ctx, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		results = append(results, res)
		if res.Err != "" {
			errs = append(errs, fmt.Errorf("%s: %s", res.Job, res.Err))
		}
	}
	return results, errors.Join(errs...)
}

func (m *Manager) runDue(ctx context.Context, now time.Time) {
	type due struct {
		job Job
	}
	var pending []due
	m.mu.Lock()
	for _, job := range m.jobs {
		if job.Disabled || m.running[job.Name] {
			continue
		}
		next := m.next[job.Name]
		if next.IsZero() || next.After(now) {
			continue
		}
		sched := m.schedules[job.Name]
		m.running[job.Name] = true
		m.next[job.Name] = sched.Next(now)
		pending = append(pending, due{job: job})
	}
	m.mu.Unlock()

	for _, d := range pending {
		m.wg.Add(1)
		go func(d due) {
			defer m.wg.Done()
			m.execute(ctx, d.job)
		}(d)
	}
}

func (m *Manager) execute(ctx context.Context, job Job) (res Result) {
	defer func() {
		if r := recover(); r != nil {
			res = Result{
				Job:        job.Name,
				Kind:       job.Kind,
				StartedAt:  time.Now(),
				DurationMs: 0,
				Err:        fmt.Sprintf("job panicked: %v", r),
			}
		}
		m.mu.Lock()
		m.running[job.Name] = false
		m.last[job.Name] = res
		m.lastRun[job.Name] = time.Now()
		m.mu.Unlock()
	}()

	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-ctx.Done():
		res = Result{Job: job.Name, Kind: job.Kind, StartedAt: time.Now(), Err: ctx.Err().Error()}
		return res
	}

	res = Result{Job: job.Name, Kind: job.Kind, StartedAt: time.Now()}
	if m.runner == nil {
		res.Err = "no job runner configured"
	} else {
		out, err := m.runner(ctx, job)
		res = out
		if res.Job == "" {
			res.Job = job.Name
		}
		if res.Kind == "" {
			res.Kind = job.Kind
		}
		if res.StartedAt.IsZero() {
			res.StartedAt = time.Now()
		}
		if err != nil {
			res.Err = err.Error()
		}
	}
	if res.DurationMs == 0 {
		res.DurationMs = time.Since(res.StartedAt).Milliseconds()
	}
	return res
}

func (m *Manager) rebuildLocked() {
	schedules := make(map[string]Schedule, len(m.jobs))
	next := make(map[string]time.Time, len(m.jobs))
	now := time.Now()
	for _, job := range m.jobs {
		sched, err := ParseSchedule(job.Schedule)
		if err != nil {
			continue
		}
		schedules[job.Name] = sched
		if !job.Disabled {
			next[job.Name] = sched.Next(now)
		}
	}
	m.schedules = schedules
	m.next = next
}

func (m *Manager) findLocked(name string) (Job, bool) {
	for _, job := range m.jobs {
		if job.Name == name {
			return job, true
		}
	}
	return Job{}, false
}

func (m *Manager) saveLocked(jobs []Job) error {
	if strings.TrimSpace(m.path) == "" {
		return nil
	}
	return Save(m.path, File{Version: CurrentSchemaVersion, Jobs: jobs})
}

func normalizeJob(job Job) Job {
	job.Name = strings.TrimSpace(job.Name)
	job.Kind = Kind(strings.ToLower(strings.TrimSpace(string(job.Kind))))
	job.Schedule = strings.TrimSpace(job.Schedule)
	job.Hive = strings.TrimSpace(job.Hive)
	job.Output = strings.TrimSpace(job.Output)
	return job
}
