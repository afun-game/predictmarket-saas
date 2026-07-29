// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package resource

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Cron is a cron-style job scheduling abstraction. Jobs are identified by name
// and run on a schedule expressed as a standard 5-field cron expression.
type Cron interface {
	// Add registers a job with the given name and cron schedule. The job runs
	// in the background and calls fn when the schedule fires. Calling Add with
	// the same name replaces the previous job.
	Add(ctx context.Context, name, schedule string, fn func(context.Context)) error
	// Remove stops and removes the named job.
	Remove(ctx context.Context, name string) error
	// Close stops all jobs and releases resources.
	Close() error
}

// NewMemoryCron returns a process-local in-memory cron implementation useful
// for tests and local development. It is not suitable for production use.
func NewMemoryCron() Cron {
	return &memoryCron{
		jobs: map[string]*cronJob{},
	}
}

type memoryCron struct {
	mu   sync.Mutex
	jobs map[string]*cronJob
}

type cronJob struct {
	name     string
	cancel   context.CancelFunc
	interval time.Duration
}

func (c *memoryCron) Add(ctx context.Context, name, schedule string, fn func(context.Context)) error {
	interval, err := parseSimpleCron(schedule)
	if err != nil {
		return fmt.Errorf("cron schedule %q: %w", schedule, err)
	}
	c.Remove(ctx, name)
	jobCtx, cancel := context.WithCancel(ctx)
	job := &cronJob{name: name, cancel: cancel, interval: interval}
	c.mu.Lock()
	c.jobs[name] = job
	c.mu.Unlock()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				fn(jobCtx)
			}
		}
	}()
	return nil
}

func (c *memoryCron) Remove(ctx context.Context, name string) error {
	c.mu.Lock()
	job, ok := c.jobs[name]
	if ok {
		delete(c.jobs, name)
	}
	c.mu.Unlock()
	if ok {
		job.cancel()
	}
	return nil
}

func (c *memoryCron) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, job := range c.jobs {
		job.cancel()
	}
	c.jobs = map[string]*cronJob{}
	return nil
}

// parseSimpleCron supports a minimal subset of cron expressions for local
// development. It accepts interval-based shortcuts like "@every 30s",
// "@every 5m", and "@every 1h". Full 5-field cron parsing is left to
// provider-specific implementations.
func parseSimpleCron(schedule string) (time.Duration, error) {
	const prefix = "@every "
	if len(schedule) <= len(prefix) || schedule[:len(prefix)] != prefix {
		return 0, fmt.Errorf("unsupported cron schedule %q, use @every <duration>", schedule)
	}
	d, err := time.ParseDuration(schedule[len(prefix):])
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("cron interval must be positive")
	}
	return d, nil
}
