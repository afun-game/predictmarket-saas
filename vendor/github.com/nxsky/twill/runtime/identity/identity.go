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

// Package identity provides service identity management for mTLS-based
// component authentication and environment-scoped secret access. It
// formalizes the service identity model that the runtime mTLS
// infrastructure uses to establish trusted connections between components.
package identity

import (
	"fmt"
	"sort"
	"sync"

	"github.com/nxsky/twill/runtime/environment"
)

// ServiceIdentity describes a component's identity for mTLS authentication.
type ServiceIdentity struct {
	Component   string `json:"component"`
	Listener    string `json:"listener,omitempty"`
	MTLSEnabled bool   `json:"mtls_enabled"`
	CertRef     string `json:"cert_ref,omitempty"`
	KeyRef      string `json:"key_ref,omitempty"`
	TrustDomain string `json:"trust_domain,omitempty"`
}

// SecretScope defines which environments are allowed to access a secret.
type SecretScope struct {
	SecretKey   string   `json:"secret_key"`
	AllowedEnvs []string `json:"allowed_envs"`
	Component   string   `json:"component,omitempty"`
}

// IdentityRegistry manages service identities and secret scopes. It is
// safe for concurrent use.
type IdentityRegistry struct {
	mu         sync.RWMutex
	identities map[string]ServiceIdentity
	scopes     map[string]SecretScope
}

// NewIdentityRegistry returns an empty identity registry.
func NewIdentityRegistry() *IdentityRegistry {
	return &IdentityRegistry{
		identities: map[string]ServiceIdentity{},
		scopes:     map[string]SecretScope{},
	}
}

// RegisterIdentity registers a service identity. The key is the component
// name, optionally suffixed with the listener.
func (r *IdentityRegistry) RegisterIdentity(id ServiceIdentity) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := identityKey(id.Component, id.Listener)
	r.identities[key] = id
}

// LookupIdentity returns the service identity for the given component and
// listener.
func (r *IdentityRegistry) LookupIdentity(component, listener string) (ServiceIdentity, error) {
	if r == nil {
		return ServiceIdentity{}, fmt.Errorf("identity registry is nil")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := identityKey(component, listener)
	id, ok := r.identities[key]
	if !ok {
		return ServiceIdentity{}, fmt.Errorf("service identity not found for component %q listener %q", component, listener)
	}
	return id, nil
}

// ListIdentities returns all registered service identities, sorted by key.
func (r *IdentityRegistry) ListIdentities() []ServiceIdentity {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.identities))
	for k := range r.identities {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]ServiceIdentity, 0, len(keys))
	for _, k := range keys {
		result = append(result, r.identities[k])
	}
	return result
}

// RegisterSecretScope registers a secret scope that restricts which
// environments can access a secret.
func (r *IdentityRegistry) RegisterSecretScope(scope SecretScope) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scopes[scope.SecretKey] = scope
}

// CheckSecretAccess returns nil if the given environment is allowed to
// access the secret, or an error describing why access is denied.
func (r *IdentityRegistry) CheckSecretAccess(secretKey, envName string) error {
	if r == nil {
		return fmt.Errorf("identity registry is nil")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	scope, ok := r.scopes[secretKey]
	if !ok {
		return nil
	}
	for _, allowed := range scope.AllowedEnvs {
		if allowed == envName {
			return nil
		}
	}
	return fmt.Errorf("secret %q is not scoped for environment %q", secretKey, envName)
}

// ListSecretScopes returns all registered secret scopes, sorted by secret key.
func (r *IdentityRegistry) ListSecretScopes() []SecretScope {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.scopes))
	for k := range r.scopes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]SecretScope, 0, len(keys))
	for _, k := range keys {
		result = append(result, r.scopes[k])
	}
	return result
}

// CheckSecretAccessForEnv checks secret access using an Environment value
// instead of a raw environment name.
func (r *IdentityRegistry) CheckSecretAccessForEnv(secretKey string, env environment.Environment) error {
	return r.CheckSecretAccess(secretKey, env.Name)
}

func identityKey(component, listener string) string {
	if listener == "" {
		return component
	}
	return component + "/" + listener
}
