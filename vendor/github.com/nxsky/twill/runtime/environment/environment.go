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

// Package environment defines a formal environment model for deployment
// governance. Environments classify deployment targets (local, dev, staging,
// production, preview) with validation rules and deployment constraints that
// policy gates and rollout strategies rely on.
package environment

import (
	"fmt"
	"strings"
)

// Type classifies a deployment environment.
type Type string

const (
	// TypeLocal is the local development environment running on a developer
	// machine. It has no deployment gates and allows unrestricted rollbacks.
	TypeLocal Type = "local"

	// TypeDev is a shared development environment. It allows automatic
	// rollbacks and has relaxed resource constraints.
	TypeDev Type = "dev"

	// TypeStaging is a pre-production environment that mirrors production
	// configuration. It enforces policy gates but allows automatic rollback.
	TypeStaging Type = "staging"

	// TypeProduction is the production environment. It requires explicit
	// approval for deployments, enforces strict policy gates, and requires
	// manual rollback decisions.
	TypeProduction Type = "production"

	// TypePreview is an ephemeral preview environment created for pull
	// request validation. It has relaxed constraints and automatic cleanup.
	TypePreview Type = "preview"
)

// Environment describes a deployment target with its type, namespace, and
// governance constraints.
type Environment struct {
	Name      string `json:"name"`
	Type      Type   `json:"type"`
	Namespace string `json:"namespace"`

	// AllowAutomaticRollback controls whether the rollout strategy can
	// trigger rollback without human approval when health regresses.
	AllowAutomaticRollback bool `json:"allow_automatic_rollback"`

	// RequireExplicitApproval controls whether deployments require an
	// explicit approval signal before proceeding.
	RequireExplicitApproval bool `json:"require_explicit_approval"`

	// EnforcePolicyGates controls whether deployment policy gates are
	// evaluated and must pass before apply.
	EnforcePolicyGates bool `json:"enforce_policy_gates"`

	// EnforceResourceLimits controls whether resource limit policy gates
	// are evaluated.
	EnforceResourceLimits bool `json:"enforce_resource_limits"`

	// EnforceSecretScoping controls whether secret access is scoped to
	// the environment.
	EnforceSecretScoping bool `json:"enforce_secret_scoping"`

	// AllowIngressHost controls whether custom ingress hosts are allowed.
	// Production environments typically require explicit ingress hosts.
	AllowIngressHost bool `json:"allow_ingress_host"`
}

// Default returns the default environment configuration for the given type.
// The name is set to the type string unless overridden.
func Default(envType Type) Environment {
	switch envType {
	case TypeLocal:
		return Environment{
			Name:                    "local",
			Type:                    TypeLocal,
			Namespace:               "default",
			AllowAutomaticRollback:  true,
			RequireExplicitApproval: false,
			EnforcePolicyGates:      false,
			EnforceResourceLimits:   false,
			EnforceSecretScoping:    false,
			AllowIngressHost:        true,
		}
	case TypeDev:
		return Environment{
			Name:                    "dev",
			Type:                    TypeDev,
			Namespace:               "dev",
			AllowAutomaticRollback:  true,
			RequireExplicitApproval: false,
			EnforcePolicyGates:      true,
			EnforceResourceLimits:   false,
			EnforceSecretScoping:    false,
			AllowIngressHost:        true,
		}
	case TypeStaging:
		return Environment{
			Name:                    "staging",
			Type:                    TypeStaging,
			Namespace:               "staging",
			AllowAutomaticRollback:  true,
			RequireExplicitApproval: false,
			EnforcePolicyGates:      true,
			EnforceResourceLimits:   true,
			EnforceSecretScoping:    true,
			AllowIngressHost:        true,
		}
	case TypeProduction:
		return Environment{
			Name:                    "production",
			Type:                    TypeProduction,
			Namespace:               "production",
			AllowAutomaticRollback:  false,
			RequireExplicitApproval: true,
			EnforcePolicyGates:      true,
			EnforceResourceLimits:   true,
			EnforceSecretScoping:    true,
			AllowIngressHost:        false,
		}
	case TypePreview:
		return Environment{
			Name:                    "preview",
			Type:                    TypePreview,
			Namespace:               "preview",
			AllowAutomaticRollback:  true,
			RequireExplicitApproval: false,
			EnforcePolicyGates:      false,
			EnforceResourceLimits:   false,
			EnforceSecretScoping:    false,
			AllowIngressHost:        true,
		}
	default:
		return Environment{
			Name:                    string(envType),
			Type:                    envType,
			Namespace:               "default",
			AllowAutomaticRollback:  false,
			RequireExplicitApproval: true,
			EnforcePolicyGates:      true,
			EnforceResourceLimits:   true,
			EnforceSecretScoping:    true,
			AllowIngressHost:        false,
		}
	}
}

// ParseType parses an environment type from a string. It is case-insensitive
// and accepts common aliases.
func ParseType(value string) (Type, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "local", "localhost":
		return TypeLocal, nil
	case "dev", "development":
		return TypeDev, nil
	case "staging", "stage":
		return TypeStaging, nil
	case "production", "prod":
		return TypeProduction, nil
	case "preview", "pr":
		return TypePreview, nil
	default:
		return "", fmt.Errorf("unknown environment type %q; valid types are local, dev, staging, production, preview", value)
	}
}

// Validate checks that the environment is internally consistent.
func (e Environment) Validate() error {
	if strings.TrimSpace(e.Name) == "" {
		return fmt.Errorf("environment name must not be empty")
	}
	if strings.TrimSpace(e.Namespace) == "" {
		return fmt.Errorf("environment namespace must not be empty")
	}
	if _, err := ParseType(string(e.Type)); err != nil {
		return err
	}
	if e.Type == TypeProduction && !e.RequireExplicitApproval {
		return fmt.Errorf("production environment must require explicit approval")
	}
	if e.Type == TypeProduction && e.AllowAutomaticRollback {
		return fmt.Errorf("production environment must not allow automatic rollback")
	}
	return nil
}

// IsProduction returns true if the environment type is production.
func (e Environment) IsProduction() bool {
	return e.Type == TypeProduction
}

// IsLocal returns true if the environment type is local.
func (e Environment) IsLocal() bool {
	return e.Type == TypeLocal
}

// String returns a human-readable description of the environment.
func (e Environment) String() string {
	return fmt.Sprintf("%s/%s (namespace=%s)", e.Type, e.Name, e.Namespace)
}
