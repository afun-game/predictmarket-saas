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

// Package resource provides typed, config-driven infrastructure primitives for
// Twill applications. It exposes database, cache, pub/sub, cron, and secret
// abstractions that are resolved from local config and environment variables,
// without hard-coding provider-specific client dependencies in the core
// runtime.
package resource

import (
	"context"
	"fmt"
	"sync"
)

// Kind identifies a resource primitive kind.
type Kind string

// Resource kinds supported by the runtime resource abstraction.
const (
	KindDatabase    Kind = "database"
	KindCache       Kind = "cache"
	KindQueue       Kind = "queue"
	KindPubSub      Kind = "pubsub"
	KindObjectStore Kind = "object_storage"
	KindCron        Kind = "cron"
	KindSecret      Kind = "secret"
)

// Config describes a resource binding. DSN and secret values are only resolved
// at runtime and are not included in AI context surfaces.
type Config struct {
	Name      string
	Kind      Kind
	Component string
	Type      string
	Lifecycle string
	Binding   string
	Provider  string
	Required  bool
	DSN       string
	Env       map[string]string
}

// Resolver resolves a resource name to its runtime config.
type Resolver interface {
	Resolve(ctx context.Context, name string) (Config, bool, error)
}

// Manager resolves resource configs and provides typed handles.
type Manager struct {
	resolver   Resolver
	mu         sync.Mutex
	singletons map[string]any
}

// NewManager returns a resource manager backed by resolver.
func NewManager(resolver Resolver) *Manager {
	return &Manager{resolver: resolver, singletons: map[string]any{}}
}

// Resolve returns the config for the named resource.
func (m *Manager) Resolve(ctx context.Context, name string) (Config, bool, error) {
	if m == nil || m.resolver == nil {
		return Config{}, false, nil
	}
	return m.resolver.Resolve(ctx, name)
}

// ResolveDatabase returns a Database handle for the named resource. The
// underlying *sql.DB is singleton-managed: repeated calls for the same
// resource name reuse the same connection pool.
func (m *Manager) ResolveDatabase(ctx context.Context, name string) (Database, error) {
	cfg, ok, err := m.Resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("resource %q not found", name)
	}
	if cfg.Kind != KindDatabase {
		return nil, fmt.Errorf("resource %q is not a database", name)
	}
	v := m.getOrCreateSingleton(name, func() any {
		db, err := OpenDatabase(cfg.Type, cfg.DSN)
		if err != nil {
			return err
		}
		return db
	})
	if db, ok := v.(Database); ok {
		return db, nil
	}
	if err, ok := v.(error); ok {
		return nil, fmt.Errorf("open database %q: %w", name, err)
	}
	return nil, fmt.Errorf("unexpected type from database factory for %q", name)
}

// ResolveCache returns a Cache handle for the named resource. If a
// CacheProvider has been registered via RegisterCacheProvider, it is used
// to create a production cache (e.g., Redis). Otherwise, an in-memory cache
// is used. The cache is singleton-managed per resource name.
func (m *Manager) ResolveCache(ctx context.Context, name string) (Cache, error) {
	cfg, ok, err := m.Resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("resource %q not found", name)
	}
	if cfg.Kind != KindCache {
		return nil, fmt.Errorf("resource %q is not a cache", name)
	}
	if cacheProvider != nil {
		v := m.getOrCreateSingleton(name, func() any {
			c, err := cacheProvider.Open(cfg)
			if err != nil {
				return err
			}
			return c
		})
		if c, ok := v.(Cache); ok {
			return c, nil
		}
		if err, ok := v.(error); ok {
			return nil, fmt.Errorf("cache provider open %q: %w", name, err)
		}
	}
	return NewMemoryCache(), nil
}

// ResolvePubSub returns a PubSub handle for the named resource. If a
// PubSubProvider has been registered via RegisterPubSubProvider, it is used
// to create a production pub/sub (for example Redis Streams). Otherwise, an
// in-memory pub/sub is used. The handle is singleton-managed per resource name.
func (m *Manager) ResolvePubSub(ctx context.Context, name string) (PubSub, error) {
	cfg, ok, err := m.Resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("resource %q not found", name)
	}
	if cfg.Kind != KindPubSub && cfg.Kind != KindQueue {
		return nil, fmt.Errorf("resource %q is not a pubsub or queue", name)
	}
	if pubsubProvider != nil {
		v := m.getOrCreateSingleton(name, func() any {
			ps, err := pubsubProvider.Open(cfg)
			if err != nil {
				return err
			}
			return ps
		})
		if ps, ok := v.(PubSub); ok {
			return ps, nil
		}
		if err, ok := v.(error); ok {
			return nil, fmt.Errorf("pubsub provider open %q: %w", name, err)
		}
	}
	return m.getOrCreateSingleton(name, func() any { return NewMemoryPubSub() }).(PubSub), nil
}

// ResolveCron returns a Cron handle for the named resource. If a CronProvider
// has been registered via RegisterCronProvider, it is used (typically to wrap
// scheduling with a distributed lock). Otherwise, an in-memory cron is used.
// The handle is singleton-managed per resource name.
func (m *Manager) ResolveCron(ctx context.Context, name string) (Cron, error) {
	cfg, ok, err := m.Resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("resource %q not found", name)
	}
	if cfg.Kind != KindCron {
		return nil, fmt.Errorf("resource %q is not a cron", name)
	}
	if cronProvider != nil {
		v := m.getOrCreateSingleton(name, func() any {
			c, err := cronProvider.Open(cfg)
			if err != nil {
				return err
			}
			return c
		})
		if c, ok := v.(Cron); ok {
			return c, nil
		}
		if err, ok := v.(error); ok {
			return nil, fmt.Errorf("cron provider open %q: %w", name, err)
		}
	}
	return m.getOrCreateSingleton(name, func() any { return NewMemoryCron() }).(Cron), nil
}

// ResolveSecret returns a Secret handle for the named resource. If a
// SecretProvider has been registered via RegisterSecretProvider, it is used.
// Otherwise DefaultSecret is used (TWILL_SECRET_DIR files, then env vars).
func (m *Manager) ResolveSecret(ctx context.Context, name string) (Secret, error) {
	cfg, ok, err := m.Resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("resource %q not found", name)
	}
	if cfg.Kind != KindSecret {
		return nil, fmt.Errorf("resource %q is not a secret", name)
	}
	if secretProvider != nil {
		v := m.getOrCreateSingleton(name, func() any {
			s, err := secretProvider.Open(cfg)
			if err != nil {
				return err
			}
			return s
		})
		if s, ok := v.(Secret); ok {
			return s, nil
		}
		if err, ok := v.(error); ok {
			return nil, fmt.Errorf("secret provider open %q: %w", name, err)
		}
	}
	return DefaultSecret(), nil
}

// getOrCreateSingleton returns a shared instance for the named resource,
// creating it with factory if it does not already exist.
func (m *Manager) getOrCreateSingleton(name string, factory func() any) any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v, ok := m.singletons[name]; ok {
		return v
	}
	v := factory()
	m.singletons[name] = v
	return v
}
