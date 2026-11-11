// Package jobs defines BeeChief's scheduled work and cron expression support.
package jobs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// EnvJobsPath overrides the default jobs file location.
const EnvJobsPath = "BEECHIEF_JOBS"

// CurrentSchemaVersion is the active jobs file schema version.
const CurrentSchemaVersion = 1

// Kind identifies what a scheduled job does.
type Kind string

const (
	KindScan   Kind = "scan"
	KindHealth Kind = "health"
	KindReport Kind = "report"
)

// Job is one scheduled BeeChief operation.
type Job struct {
	Name     string `yaml:"name" json:"name"`
	Kind     Kind   `yaml:"kind" json:"kind"`
	Schedule string `yaml:"schedule" json:"schedule"`
	Hive     string `yaml:"hive,omitempty" json:"hive,omitempty"`
	Output   string `yaml:"output,omitempty" json:"output,omitempty"`
	Disabled bool   `yaml:"disabled,omitempty" json:"disabled,omitempty"`
}

// File is the on-disk jobs document.
type File struct {
	Version int   `yaml:"version,omitempty" json:"version,omitempty"`
	Jobs    []Job `yaml:"jobs" json:"jobs"`
}

// Result records the outcome of one job run.
type Result struct {
	Job        string    `json:"job"`
	Kind       Kind      `json:"kind"`
	StartedAt  time.Time `json:"startedAt"`
	DurationMs int64     `json:"durationMs"`
	Summary    string    `json:"summary,omitempty"`
	Err        string    `json:"err,omitempty"`
}

// Status is the scheduler view of a job.
type Status struct {
	Job      Job       `json:"job"`
	NextRun  time.Time `json:"nextRun,omitempty"`
	LastRun  time.Time `json:"lastRun,omitempty"`
	Running  bool      `json:"running"`
	Last     Result    `json:"last,omitempty"`
	Schedule string    `json:"scheduleExpression,omitempty"`
}

// Validate checks a single job definition.
func (j Job) Validate() error {
	j.Name = strings.TrimSpace(j.Name)
	if j.Name == "" {
		return errors.New("job name is required")
	}
	switch j.Kind {
	case KindScan, KindHealth, KindReport:
	default:
		return fmt.Errorf("job %q: unknown kind %q (want scan, health, or report)", j.Name, j.Kind)
	}
	if _, err := ParseSchedule(j.Schedule); err != nil {
		return fmt.Errorf("job %q: %w", j.Name, err)
	}
	return nil
}

// DefaultPath returns $BEECHIEF_JOBS if set, otherwise
// <user config dir>/beechief/jobs.yaml.
func DefaultPath() string {
	if p := strings.TrimSpace(os.Getenv(EnvJobsPath)); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "jobs.yaml"
	}
	return filepath.Join(dir, "beechief", "jobs.yaml")
}

// Load reads and validates a jobs file. A missing file yields an empty
// document so a fresh install can start cleanly.
func Load(path string) (File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return File{Version: CurrentSchemaVersion, Jobs: []Job{}}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("read jobs: %w", err)
	}
	return Parse(data)
}

// Parse decodes and validates a jobs document.
func Parse(data []byte) (File, error) {
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return File{}, fmt.Errorf("parse jobs: %w", err)
	}
	if f.Version == 0 {
		f.Version = CurrentSchemaVersion
	}
	if f.Version != CurrentSchemaVersion {
		return File{}, fmt.Errorf("unsupported jobs schema version %d", f.Version)
	}
	if f.Jobs == nil {
		f.Jobs = []Job{}
	}
	seen := map[string]bool{}
	for i, j := range f.Jobs {
		if err := j.Validate(); err != nil {
			return File{}, fmt.Errorf("jobs[%d]: %w", i, err)
		}
		if seen[j.Name] {
			return File{}, fmt.Errorf("jobs[%d]: duplicate job name %q", i, j.Name)
		}
		seen[j.Name] = true
	}
	return f, nil
}

// Marshal encodes a validated jobs document.
func Marshal(f File) ([]byte, error) {
	if f.Version == 0 {
		f.Version = CurrentSchemaVersion
	}
	if f.Jobs == nil {
		f.Jobs = []Job{}
	}
	seen := map[string]bool{}
	for i, j := range f.Jobs {
		if err := j.Validate(); err != nil {
			return nil, fmt.Errorf("jobs[%d]: %w", i, err)
		}
		if seen[j.Name] {
			return nil, fmt.Errorf("jobs[%d]: duplicate job name %q", i, j.Name)
		}
		seen[j.Name] = true
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return nil, fmt.Errorf("encode jobs: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode jobs: %w", err)
	}
	return buf.Bytes(), nil
}

// Save writes f atomically with 0600 permissions.
func Save(path string, f File) error {
	data, err := Marshal(f)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create jobs directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".jobs-*.tmp")
	if err != nil {
		return fmt.Errorf("write jobs: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write jobs: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write jobs: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write jobs: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write jobs: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("write jobs: %w", err)
	}
	return nil
}
