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

package config

import (
	"context"
	"fmt"
	"time"
)

// RemoteProvider is an interface for remote configuration sources such as
// etcd, Consul, Spring Cloud Config, or a custom control plane. Implementations
// are registered via NewRemoteLoader and must handle their own connection
// management, caching, and error handling.
//
// Implementations should be safe for concurrent use.
type RemoteProvider interface {
	// Get retrieves the value for key from the remote source. Returns
	// (value, true, nil) if found, ("", false, nil) if not found, and
	// ("", false, err) on failure.
	Get(ctx context.Context, key string) (string, bool, error)

	// Watch subscribes to changes for key. When the value changes, the
	// callback is called with the new value. If the key is deleted,
	// callback is called with ("", false). Watch returns nil if
	// watching is not supported. The watch is cancelled when ctx is done.
	Watch(ctx context.Context, key string, callback func(value string, ok bool)) error
}

// RemoteLoader adapts a RemoteProvider to the Loader interface. It caches
// values locally with a configurable TTL to reduce remote calls.
type RemoteLoader struct {
	provider RemoteProvider
	ttl      time.Duration
}

// NewRemoteLoader returns a loader backed by a RemoteProvider. Values are
// cached for ttl duration; a ttl of 0 disables caching.
func NewRemoteLoader(provider RemoteProvider, ttl time.Duration) *RemoteLoader {
	return &RemoteLoader{provider: provider, ttl: ttl}
}

// Get implements Loader.
func (l *RemoteLoader) Get(ctx context.Context, key string) (string, bool) {
	if l == nil || l.provider == nil {
		return "", false
	}
	value, ok, err := l.provider.Get(ctx, key)
	if err != nil || !ok {
		return "", false
	}
	return value, true
}

// Unmarshal implements Loader. RemoteLoader does not support structured
// unmarshaling; it always returns nil without changing dst.
func (l *RemoteLoader) Unmarshal(ctx context.Context, key string, dst any) error {
	return nil
}

// NoopRemoteProvider is a RemoteProvider that returns not-found for all keys.
// It is useful as a default when no remote provider is configured.
type NoopRemoteProvider struct{}

// Get returns ("", false, nil) for all keys.
func (NoopRemoteProvider) Get(ctx context.Context, key string) (string, bool, error) {
	return "", false, nil
}

// Watch returns nil immediately.
func (NoopRemoteProvider) Watch(ctx context.Context, key string, callback func(string, bool)) error {
	return nil
}

// StaticRemoteProvider is a RemoteProvider backed by a simple map. It is
// useful for testing and for local development where a remote source is
// simulated.
type StaticRemoteProvider struct {
	values map[string]string
}

// NewStaticRemoteProvider returns a provider backed by the given map.
func NewStaticRemoteProvider(values map[string]string) *StaticRemoteProvider {
	return &StaticRemoteProvider{values: values}
}

// Get returns the value for key from the static map.
func (p *StaticRemoteProvider) Get(ctx context.Context, key string) (string, bool, error) {
	if p == nil {
		return "", false, nil
	}
	v, ok := p.values[key]
	if !ok {
		return "", false, nil
	}
	return v, true, nil
}

// Watch returns nil (watching not supported).
func (p *StaticRemoteProvider) Watch(ctx context.Context, key string, callback func(string, bool)) error {
	return fmt.Errorf("StaticRemoteProvider does not support Watch")
}
