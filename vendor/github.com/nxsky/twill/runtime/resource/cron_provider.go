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
	"time"
)

// CronProvider creates a production Cron handle (for example a Redis-locked
// scheduler). Users register a provider via RegisterCronProvider to replace
// the default in-memory cron.
//
// The provider is called once per resource name and the result is
// singleton-managed by the Manager.
type CronProvider interface {
	// Open creates a Cron handle for the given resource config.
	Open(cfg Config) (Cron, error)
}

var cronProvider CronProvider

// RegisterCronProvider registers a production cron provider. Call once at
// program startup, before twill.Run. If no provider is registered, the default
// in-memory cron is used (which runs on every replica).
//
// Example with a Redis lock:
//
//	type redisCronProvider struct{ lock resource.LockClient }
//	func (p redisCronProvider) Open(cfg resource.Config) (resource.Cron, error) {
//	    return resource.NewLockedCron(resource.NewMemoryCron(), p.lock, "twill:cron:"), nil
//	}
//	func init() {
//	    resource.RegisterCronProvider(redisCronProvider{lock: myLock})
//	}
func RegisterCronProvider(p CronProvider) {
	cronProvider = p
}

// LockClient is the minimal interface for a distributed lock backend (typically
// Redis SET NX EX). Users implement this to bridge their preferred Redis
// library without adding a direct dependency to the runtime.
//
// LockedCron only requires SetNX. CompareAndDelete remains available for
// callers that need explicit release in other flows, but LockedCron does not
// release locks early: short jobs leave the key until TTL expiry so phase-
// offset replica tickers cannot re-run the same interval.
type LockClient interface {
	// SetNX sets key to value with TTL only if the key does not already exist.
	// Returns true when the lock was acquired.
	SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error)
	// CompareAndDelete deletes key only if its current value equals value.
	// Returns true when the key was deleted.
	CompareAndDelete(ctx context.Context, key, value string) (bool, error)
}

// LockedCron wraps an underlying Cron and runs each job only when a distributed
// lock is acquired. This prevents multi-replica duplicate execution when every
// process schedules the same jobs.
//
// The lock key is prefix + job name. On each tick the winner calls SetNX with
// a TTL of roughly one schedule interval and does not release the lock when
// the job finishes. Other replicas (including those whose tickers fire later
// in the same interval) fail SetNX until the TTL expires near the next period.
// Jobs must complete within the lock TTL; long jobs should use a longer
// schedule interval or a custom provider.
type LockedCron struct {
	inner  Cron
	lock   LockClient
	prefix string
	// holder is written into the lock value for diagnostics / ownership.
	holder string
}

// DefaultLockTTL is used when a schedule cannot be parsed as @every.
const DefaultLockTTL = 30 * time.Second

// NewLockedCron returns a Cron that coordinates job execution with lock.
// prefix is prepended to job names when forming lock keys (for example
// "twill:cron:"). holder identifies this process in the lock value; if empty,
// a fixed default is used.
func NewLockedCron(inner Cron, lock LockClient, prefix string, holder string) *LockedCron {
	if holder == "" {
		holder = "twill"
	}
	return &LockedCron{inner: inner, lock: lock, prefix: prefix, holder: holder}
}

// Add implements Cron. The job function is wrapped so it only runs when the
// distributed lock is acquired. The lock is held until TTL expiry (not
// released when the job returns).
func (c *LockedCron) Add(ctx context.Context, name, schedule string, fn func(context.Context)) error {
	if c == nil || c.inner == nil {
		return fmt.Errorf("locked cron: nil inner")
	}
	if c.lock == nil {
		return fmt.Errorf("locked cron: nil lock client")
	}
	ttl := lockTTLForSchedule(schedule)
	key := c.prefix + name
	holder := c.holder
	wrapped := func(jobCtx context.Context) {
		ok, err := c.lock.SetNX(jobCtx, key, holder, ttl)
		if err != nil || !ok {
			return
		}
		// Do not CompareAndDelete here. Early release lets phase-offset
		// replicas acquire the lock and re-run within the same interval.
		// TTL expiry is the fencing window for multi-replica tickers.
		fn(jobCtx)
	}
	return c.inner.Add(ctx, name, schedule, wrapped)
}

// Remove implements Cron.
func (c *LockedCron) Remove(ctx context.Context, name string) error {
	if c == nil || c.inner == nil {
		return nil
	}
	return c.inner.Remove(ctx, name)
}

// Close implements Cron.
func (c *LockedCron) Close() error {
	if c == nil || c.inner == nil {
		return nil
	}
	return c.inner.Close()
}

func lockTTLForSchedule(schedule string) time.Duration {
	d, err := parseSimpleCron(schedule)
	if err != nil || d <= 0 {
		return DefaultLockTTL
	}
	// Hold for the full interval so any replica that ticks later in the same
	// period still sees the lock. The next legitimate run happens after TTL
	// expiry, aligned with the schedule interval.
	if d < time.Second {
		// Sub-second schedules still need a positive TTL; use the interval
		// itself (tests) or fall back if somehow non-positive.
		if d <= 0 {
			return DefaultLockTTL
		}
		return d
	}
	return d
}
