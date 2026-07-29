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

package deployers

import (
	"context"
	"fmt"
	"strings"
)

// PlanSchemaVersion is the schema version for generic deployment plans.
const PlanSchemaVersion = "twill.deploy.plan.v1"

// Planner is implemented by deployment targets that can produce a read-only
// deployment plan. Plan implementations must not modify files, local
// environments, registries, or remote deployment backends.
type Planner interface {
	Target() string
	Plan(context.Context, PlanRequest) (*Plan, error)
}

// PlanRequest describes the target-agnostic deployment planning input.
type PlanRequest struct {
	App           string     `json:"app"`
	Target        string     `json:"target"`
	Namespace     string     `json:"namespace,omitempty"`
	Image         string     `json:"image,omitempty"`
	IngressClass  string     `json:"ingress_class,omitempty"`
	IngressHost   string     `json:"ingress_host,omitempty"`
	GatewayClass  string     `json:"gateway_class,omitempty"`
	HealthPath    string     `json:"health_path,omitempty"`
	Replicas      int        `json:"replicas,omitempty"`
	MaxReplicas   int        `json:"max_replicas,omitempty"`
	CPURequest    string     `json:"cpu_request,omitempty"`
	MemoryRequest string     `json:"memory_request,omitempty"`
	CPULimit      string     `json:"cpu_limit,omitempty"`
	MemoryLimit   string     `json:"memory_limit,omitempty"`
	Components    []string   `json:"components"`
	Endpoints     []Endpoint `json:"endpoints"`
}

// Endpoint describes safe endpoint metadata used by deployment planners.
type Endpoint struct {
	Component string `json:"component"`
	Listener  string `json:"listener"`
	Path      string `json:"path"`
}

// Plan is the target-agnostic dry-run deployment plan returned by planners.
// When a plan is applied through a deploy command, ApplyOutput and Applied
// record the result without changing the plan's original dry-run schema.
type Plan struct {
	SchemaVersion            string              `json:"schema_version"`
	Target                   string              `json:"target"`
	App                      string              `json:"app"`
	Namespace                string              `json:"namespace,omitempty"`
	Image                    string              `json:"image,omitempty"`
	DryRun                   bool                `json:"dry_run"`
	Components               []string            `json:"components"`
	Rollout                  Rollout             `json:"rollout"`
	Resources                []Resource          `json:"resources"`
	WrittenFiles             []string            `json:"written_files,omitempty"`
	Limitations              []string            `json:"limitations"`
	VerifyCommands           []string            `json:"verify_commands"`
	PerformedWrites          bool                `json:"performed_writes"`
	PerformedEnvWrite        bool                `json:"performed_environment_write"`
	Applied                  bool                `json:"applied,omitempty"`
	ApplyDryRun              bool                `json:"apply_dry_run,omitempty"`
	ApplyDryRunMode          string              `json:"apply_dry_run_mode,omitempty"`
	ApplyOutput              string              `json:"apply_output,omitempty"`
	PreApplyValidated        bool                `json:"pre_apply_validated,omitempty"`
	PreApplyValidationOutput string              `json:"pre_apply_validation_output,omitempty"`
	PreApplyValidationMode   string              `json:"pre_apply_validation_mode,omitempty"`
	Environment              string              `json:"environment,omitempty"`
	PolicyGates              []GateResult        `json:"policy_gates,omitempty"`
	RolloutHealthCheck       *RolloutHealthCheck `json:"rollout_health_check,omitempty"`
}

// RolloutHealthCheck describes post-apply health monitoring configuration
// that ties SLO-based health evaluation to automatic rollback decisions.
type RolloutHealthCheck struct {
	Enabled                bool    `json:"enabled"`
	SLOName                string  `json:"slo_name,omitempty"`
	RollbackBurnRate       float64 `json:"rollback_burn_rate,omitempty"`
	RollbackWindow         string  `json:"rollback_window,omitempty"`
	AllowAutomaticRollback bool    `json:"allow_automatic_rollback"`
	StatusCommand          string  `json:"status_command,omitempty"`
	RollbackCommand        string  `json:"rollback_command,omitempty"`
	HealthEvaluated        bool    `json:"health_evaluated,omitempty"`
	HealthStatus           string  `json:"health_status,omitempty"`
	RollbackTriggered      bool    `json:"rollback_triggered,omitempty"`
	HealthReason           string  `json:"health_reason,omitempty"`
}

// Rollout describes target-agnostic rollout metadata in a dry-run plan.
type Rollout struct {
	Strategy         string   `json:"strategy"`
	Replicas         int      `json:"replicas"`
	MaxReplicas      int      `json:"max_replicas,omitempty"`
	HealthPath       string   `json:"health_path,omitempty"`
	VerifyCommands   []string `json:"verify_commands"`
	RollbackCommands []string `json:"rollback_commands,omitempty"`
}

// Resource describes one generated or planned deployment resource.
type Resource struct {
	Kind                      string         `json:"kind"`
	Name                      string         `json:"name"`
	Source                    string         `json:"source,omitempty"`
	Layer                     string         `json:"layer,omitempty"`
	Target                    string         `json:"target,omitempty"`
	ManifestType              string         `json:"manifest_type,omitempty"`
	EmbeddedFromSchemaVersion string         `json:"embedded_from_schema_version,omitempty"`
	Manifest                  map[string]any `json:"manifest,omitempty"`
}

// ValidateDryRunPlan checks that a planner result satisfies Twill's public
// dry-run extension boundary. It is intended for third-party planner tests and
// command adapters before a plan is exposed for review.
func ValidateDryRunPlan(plan *Plan, expectedTarget string) error {
	if plan == nil {
		return fmt.Errorf("deployment plan is nil")
	}
	if strings.TrimSpace(plan.SchemaVersion) == "" {
		return fmt.Errorf("deployment plan schema version is empty")
	}
	if strings.TrimSpace(plan.Target) == "" {
		return fmt.Errorf("deployment plan target is empty")
	}
	if expectedTarget = strings.TrimSpace(expectedTarget); expectedTarget != "" && plan.Target != expectedTarget {
		return fmt.Errorf("deployment plan target %q does not match expected target %q", plan.Target, expectedTarget)
	}
	if strings.TrimSpace(plan.App) == "" {
		return fmt.Errorf("deployment plan app is empty")
	}
	if !plan.DryRun {
		return fmt.Errorf("deployment plan must be dry-run")
	}
	if plan.PerformedWrites {
		return fmt.Errorf("deployment plan must not report file writes")
	}
	if plan.PerformedEnvWrite {
		return fmt.Errorf("deployment plan must not report environment writes")
	}
	if len(plan.Limitations) == 0 {
		return fmt.Errorf("deployment plan limitations are empty")
	}
	if len(plan.VerifyCommands) == 0 {
		return fmt.Errorf("deployment plan verify commands are empty")
	}
	for _, resource := range plan.Resources {
		if strings.TrimSpace(resource.Kind) == "" {
			return fmt.Errorf("deployment plan resource kind is empty")
		}
		if strings.TrimSpace(resource.Name) == "" {
			return fmt.Errorf("deployment plan resource name is empty")
		}
	}
	return nil
}

// GateResult describes the outcome of a deployment policy gate evaluation.
// It mirrors runtime/policy.GateResult but is defined here to avoid a
// cross-package dependency in the plan output type.
type GateResult struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Passed   bool   `json:"passed"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}
