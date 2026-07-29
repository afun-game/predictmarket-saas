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

package middleware

import (
	"fmt"
	"net/http"
	"sync"
)

// AuthRegistry holds named auth functions that can be referenced from config.
// This allows config-driven auth hooks without hard-coding Go functions in
// TOML.
type AuthRegistry struct {
	mu   sync.RWMutex
	auth map[string]AuthFunc
}

// NewAuthRegistry returns an empty auth registry.
func NewAuthRegistry() *AuthRegistry {
	return &AuthRegistry{auth: map[string]AuthFunc{}}
}

// Register associates name with auth. Calling Register with the same name
// replaces the previous function.
func (r *AuthRegistry) Register(name string, auth AuthFunc) {
	if r == nil || auth == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.auth[name] = auth
}

// Lookup returns the auth function registered under name, or an error if no
// function is registered.
func (r *AuthRegistry) Lookup(name string) (AuthFunc, error) {
	if r == nil {
		return nil, fmt.Errorf("auth function %q not found: registry is nil", name)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	auth, ok := r.auth[name]
	if !ok {
		return nil, fmt.Errorf("auth function %q not found", name)
	}
	return auth, nil
}

// Names returns the names of all registered auth functions.
func (r *AuthRegistry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.auth))
	for name := range r.auth {
		names = append(names, name)
	}
	return names
}

// defaultAuthRegistry is the process-global registry used by RegisterAuth and
// LookupAuth.
var (
	defaultAuthRegistry     = NewAuthRegistry()
	defaultAuthRegistryOnce sync.Once
)

// RegisterAuth registers a named auth function in the process-global registry.
// This should be called from package init() functions or early in main().
func RegisterAuth(name string, auth AuthFunc) {
	defaultAuthRegistry.Register(name, auth)
}

// LookupAuth returns the auth function registered under name in the
// process-global registry.
func LookupAuth(name string) (AuthFunc, error) {
	return defaultAuthRegistry.Lookup(name)
}

// DefaultAuthRegistry returns the process-global auth registry.
func DefaultAuthRegistry() *AuthRegistry {
	return defaultAuthRegistry
}

// ensureRequest is a no-op to prevent unused import warnings in generated code.
var _ = http.MethodGet
