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

// Package policy provides deployment policy gate evaluation. Policy gates
// validate deployment plans against environment-specific rules before apply,
// covering resource limits, namespace restrictions, ingress controls, and
// production approval requirements.
package policy

import (
	"fmt"
	"strings"

	"github.com/nxsky/twill/runtime/environment"
)

// GateResult describes the outcome of a single policy gate evaluation.
type GateResult struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Passed   bool   `json:"passed"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// GateInput is the data a policy gate needs to evaluate. It is intentionally
// generic so that both Kubernetes and AWS plans can be validated.
type GateInput struct {
	Environment environment.Environment

	// Namespace is the target Kubernetes namespace for the deployment.
	Namespace string

	// Replicas is the desired replica count.
	Replicas int

	// CPURequest, MemoryRequest, CPULimit, MemoryLimit are the container
	// resource requests and limits.
	CPURequest    string
	MemoryRequest string
	CPULimit      string
	MemoryLimit   string

	// IngressHost is the custom ingress host, if any.
	IngressHost string

	// HasIngress is true if the plan generates Ingress or Gateway resources.
	HasIngress bool

	// Approved is true if the deployment has received explicit approval.
	// This is required for production environments.
	Approved bool

	// AppliedFromDryRun is true if the deployment is a dry-run (server or
	// client), not a real apply.
	AppliedFromDryRun bool

	// PublicAPIPaths is the list of public API endpoint paths exposed by
	// the deployment. Used for public API policy gate evaluation.
	PublicAPIPaths []string

	// MigrationRiskLevel is the risk level of a database migration
	// associated with the deployment ("low", "medium", "high", "critical").
	// Empty means no migration is associated.
	MigrationRiskLevel string

	// MigrationValidated is true if the associated migration has been
	// validated.
	MigrationValidated bool
}

// EvaluateGates evaluates all applicable policy gates for the given input and
// returns the results. If any gate with severity "block" fails, the
// deployment should be rejected.
func EvaluateGates(input GateInput) []GateResult {
	var results []GateResult

	if input.Environment.EnforcePolicyGates {
		results = append(results, evaluateApprovalGate(input))
		results = append(results, evaluateNamespaceGate(input))
		results = append(results, evaluatePublicAPIGate(input))
		results = append(results, evaluateMigrationRiskGate(input))
	}
	if input.Environment.EnforceResourceLimits {
		results = append(results, evaluateResourceLimitGates(input)...)
	}
	if input.Environment.EnforceSecretScoping {
		results = append(results, evaluateSecretScopingGate(input))
	}
	results = append(results, evaluateIngressGate(input))

	return results
}

// EvaluateGatesForApply runs EvaluateGates and returns an error if any
// blocking gate fails. Non-blocking gates (severity "warn") are returned as
// results but do not cause an error.
func EvaluateGatesForApply(input GateInput) ([]GateResult, error) {
	results := EvaluateGates(input)
	var blockers []string
	for _, r := range results {
		if !r.Passed && r.Severity == "block" {
			blockers = append(blockers, fmt.Sprintf("%s: %s", r.ID, r.Message))
		}
	}
	if len(blockers) > 0 {
		return results, fmt.Errorf("deployment policy gates failed:\n  - %s", strings.Join(blockers, "\n  - "))
	}
	return results, nil
}

func evaluateApprovalGate(input GateInput) GateResult {
	result := GateResult{
		ID:       "approval-required",
		Title:    "Explicit approval required",
		Severity: "block",
	}
	if input.Environment.RequireExplicitApproval && !input.Approved && !input.AppliedFromDryRun {
		result.Passed = false
		result.Message = fmt.Sprintf("environment %s requires explicit approval before apply", input.Environment.Name)
		return result
	}
	result.Passed = true
	result.Message = "approval requirement satisfied"
	return result
}

func evaluateNamespaceGate(input GateInput) GateResult {
	result := GateResult{
		ID:       "namespace-match",
		Title:    "Namespace matches environment",
		Severity: "block",
	}
	if input.Namespace == "" {
		result.Passed = true
		result.Message = "namespace not specified; using environment default"
		return result
	}
	if input.Environment.Namespace != "" && input.Namespace != input.Environment.Namespace {
		result.Passed = false
		result.Message = fmt.Sprintf("namespace %q does not match environment namespace %q", input.Namespace, input.Environment.Namespace)
		return result
	}
	result.Passed = true
	result.Message = "namespace matches environment"
	return result
}

func evaluateResourceLimitGates(input GateInput) []GateResult {
	var results []GateResult

	if input.CPULimit == "" {
		results = append(results, GateResult{
			ID:       "resource-cpu-limit",
			Title:    "CPU limit required",
			Severity: "warn",
			Passed:   false,
			Message:  "CPU limit is not set; resource usage may be unbounded",
		})
	} else {
		results = append(results, GateResult{
			ID:       "resource-cpu-limit",
			Title:    "CPU limit required",
			Severity: "warn",
			Passed:   true,
			Message:  "CPU limit is set",
		})
	}

	if input.MemoryLimit == "" {
		results = append(results, GateResult{
			ID:       "resource-memory-limit",
			Title:    "Memory limit required",
			Severity: "block",
			Passed:   false,
			Message:  "Memory limit is not set; OOM kills may occur without bounds",
		})
	} else {
		results = append(results, GateResult{
			ID:       "resource-memory-limit",
			Title:    "Memory limit required",
			Severity: "block",
			Passed:   true,
			Message:  "Memory limit is set",
		})
	}

	if input.Replicas > 10 {
		results = append(results, GateResult{
			ID:       "resource-replica-cap",
			Title:    "Replica count within bounds",
			Severity: "warn",
			Passed:   false,
			Message:  fmt.Sprintf("replica count %d exceeds soft cap of 10; verify HPA configuration", input.Replicas),
		})
	} else {
		results = append(results, GateResult{
			ID:       "resource-replica-cap",
			Title:    "Replica count within bounds",
			Severity: "warn",
			Passed:   true,
			Message:  "replica count is within bounds",
		})
	}

	return results
}

func evaluateSecretScopingGate(input GateInput) GateResult {
	result := GateResult{
		ID:       "secret-scoping",
		Title:    "Secret access scoped to environment",
		Severity: "warn",
	}
	if input.Environment.IsProduction() {
		result.Message = "production environment requires secret access review before apply"
	} else {
		result.Message = "secret scoping enforced for this environment"
	}
	result.Passed = true
	return result
}

func evaluateIngressGate(input GateInput) GateResult {
	result := GateResult{
		ID:       "ingress-host-control",
		Title:    "Ingress host control",
		Severity: "block",
	}
	if !input.HasIngress {
		result.Passed = true
		result.Message = "no ingress resources generated"
		return result
	}
	if !input.Environment.AllowIngressHost && input.IngressHost == "" && input.Environment.IsProduction() {
		result.Passed = false
		result.Message = "production environment requires an explicit ingress host"
		return result
	}
	result.Passed = true
	result.Message = "ingress host configuration is acceptable"
	return result
}

func evaluatePublicAPIGate(input GateInput) GateResult {
	result := GateResult{
		ID:       "public-api-exposure",
		Title:    "Public API exposure review",
		Severity: "warn",
	}
	if len(input.PublicAPIPaths) == 0 {
		result.Passed = true
		result.Message = "no public API paths declared"
		return result
	}
	if input.Environment.IsProduction() {
		result.Message = fmt.Sprintf("production environment exposes %d public API path(s); review auth, rate limiting, and input validation", len(input.PublicAPIPaths))
	} else {
		result.Message = fmt.Sprintf("%d public API path(s) exposed in %s environment", len(input.PublicAPIPaths), input.Environment.Name)
	}
	result.Passed = true
	return result
}

func evaluateMigrationRiskGate(input GateInput) GateResult {
	result := GateResult{
		ID:       "migration-risk",
		Title:    "Migration risk validation",
		Severity: "block",
	}
	if input.MigrationRiskLevel == "" {
		result.Passed = true
		result.Message = "no migration associated with this deployment"
		return result
	}
	if !input.MigrationValidated {
		result.Passed = false
		result.Message = fmt.Sprintf("migration with risk level %q has not been validated; run migration validation before deployment", input.MigrationRiskLevel)
		return result
	}
	if input.Environment.IsProduction() && (input.MigrationRiskLevel == "high" || input.MigrationRiskLevel == "critical") {
		result.Severity = "warn"
		result.Passed = true
		result.Message = fmt.Sprintf("high-risk migration (%s) in production; ensure expand-contract strategy and rollback plan are reviewed", input.MigrationRiskLevel)
		return result
	}
	result.Passed = true
	result.Message = fmt.Sprintf("migration risk level %q is validated and acceptable for %s", input.MigrationRiskLevel, input.Environment.Name)
	return result
}
