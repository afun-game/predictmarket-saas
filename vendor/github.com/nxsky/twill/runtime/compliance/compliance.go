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

// Package compliance provides compliance evidence export from audit
// events, policy gate results, approval decisions, and deployment
// records. It produces structured evidence bundles suitable for
// regulatory or internal audit review.
package compliance

import (
	"time"

	"github.com/nxsky/twill/runtime/approval"
	"github.com/nxsky/twill/runtime/controlplane"
	"github.com/nxsky/twill/runtime/policy"
)

// EvidenceBundle is a structured collection of compliance evidence
// exported from the platform's audit trail.
type EvidenceBundle struct {
	ExportedAt        time.Time                       `json:"exported_at"`
	Application       string                          `json:"application,omitempty"`
	Environment       string                          `json:"environment,omitempty"`
	TimeRangeStart    *time.Time                      `json:"time_range_start,omitempty"`
	TimeRangeEnd      *time.Time                      `json:"time_range_end,omitempty"`
	DeploymentRecords []controlplane.DeploymentRecord `json:"deployment_records,omitempty"`
	PolicyGateResults []policy.GateResult             `json:"policy_gate_results,omitempty"`
	ApprovalRecords   []approval.Request              `json:"approval_records,omitempty"`
	Summary           EvidenceSummary                 `json:"summary"`
}

// EvidenceSummary summarizes the evidence bundle for quick review.
type EvidenceSummary struct {
	TotalDeployments      int `json:"total_deployments"`
	HealthyDeployments    int `json:"healthy_deployments"`
	UnhealthyDeployments  int `json:"unhealthy_deployments"`
	RolledBackDeployments int `json:"rolled_back_deployments"`
	TotalApprovals        int `json:"total_approvals"`
	ApprovedApprovals     int `json:"approved_approvals"`
	RejectedApprovals     int `json:"rejected_approvals"`
	PendingApprovals      int `json:"pending_approvals"`
	TotalPolicyGates      int `json:"total_policy_gates"`
	PassedPolicyGates     int `json:"passed_policy_gates"`
	FailedPolicyGates     int `json:"failed_policy_gates"`
}

// ExportOptions configures which evidence to include in the bundle.
type ExportOptions struct {
	Application    string
	Environment    string
	TimeRangeStart *time.Time
	TimeRangeEnd   *time.Time
}

// Export creates an evidence bundle from the given control plane, policy
// gate results, and approval workflow.
func Export(
	opts ExportOptions,
	cp *controlplane.LocalControlPlane,
	gateResults []policy.GateResult,
	approvalWorkflow *approval.Workflow,
) EvidenceBundle {
	bundle := EvidenceBundle{
		ExportedAt:        time.Now(),
		Application:       opts.Application,
		Environment:       opts.Environment,
		TimeRangeStart:    opts.TimeRangeStart,
		TimeRangeEnd:      opts.TimeRangeEnd,
		PolicyGateResults: gateResults,
	}

	if cp != nil && opts.Application != "" {
		records, err := cp.ListDeployments(opts.Application, opts.Environment)
		if err == nil {
			bundle.DeploymentRecords = filterDeploymentsByTime(records, opts.TimeRangeStart, opts.TimeRangeEnd)
		}
	}

	if approvalWorkflow != nil {
		bundle.ApprovalRecords = approvalWorkflow.ListRequests(opts.Application, "")
	}

	bundle.Summary = computeSummary(bundle)
	return bundle
}

func filterDeploymentsByTime(records []controlplane.DeploymentRecord, start, end *time.Time) []controlplane.DeploymentRecord {
	if start == nil && end == nil {
		return records
	}
	filtered := make([]controlplane.DeploymentRecord, 0, len(records))
	for _, rec := range records {
		if start != nil && rec.AppliedAt.Before(*start) {
			continue
		}
		if end != nil && rec.AppliedAt.After(*end) {
			continue
		}
		filtered = append(filtered, rec)
	}
	return filtered
}

func computeSummary(bundle EvidenceBundle) EvidenceSummary {
	summary := EvidenceSummary{}

	for _, rec := range bundle.DeploymentRecords {
		summary.TotalDeployments++
		switch rec.Status {
		case controlplane.DeploymentStatusHealthy:
			summary.HealthyDeployments++
		case controlplane.DeploymentStatusUnhealthy:
			summary.UnhealthyDeployments++
		case controlplane.DeploymentStatusRolledBack:
			summary.RolledBackDeployments++
		}
	}

	for _, ap := range bundle.ApprovalRecords {
		summary.TotalApprovals++
		switch ap.Decision {
		case approval.DecisionApproved:
			summary.ApprovedApprovals++
		case approval.DecisionRejected:
			summary.RejectedApprovals++
		case approval.DecisionPending:
			summary.PendingApprovals++
		}
	}

	for _, gate := range bundle.PolicyGateResults {
		summary.TotalPolicyGates++
		if gate.Passed {
			summary.PassedPolicyGates++
		} else {
			summary.FailedPolicyGates++
		}
	}

	return summary
}
