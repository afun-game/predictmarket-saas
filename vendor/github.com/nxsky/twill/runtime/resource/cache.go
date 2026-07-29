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
)

// Cache is a key-value cache abstraction.
type Cache interface {
	Get(ctx context.Context, key string) (string, error)
	Put(ctx context.Context, key, value string) error
	Delete(ctx context.Context, key string) error
}

// NewMemoryCache returns a process-local in-memory cache useful for tests and
// local development. It is not suitable for production or multi-process use.
func NewMemoryCache() Cache {
	return &memoryCache{data: map[string]string{}}
}

type memoryCache struct {
	mu   sync.RWMutex
	data map[string]string
}

func (c *memoryCache) Get(ctx context.Context, key string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	value, ok := c.data[key]
	if !ok {
		return "", fmt.Errorf("cache key %q not found", key)
	}
	return value, nil
}

func (c *memoryCache) Put(ctx context.Context, key, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = value
	return nil
}

func (c *memoryCache) Delete(ctx context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
	return nil
}
