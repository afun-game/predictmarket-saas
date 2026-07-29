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

// Package approval provides a production change approval workflow that
// integrates with the environment model and policy gates. It tracks
// approval requests, approvers, decisions, and evidence for audit and
// compliance purposes.
package approval

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/nxsky/twill/runtime/environment"
)

// Decision describes the outcome of an approval request.
type Decision string

const (
	DecisionPending  Decision = "pending"
	DecisionApproved Decision = "approved"
	DecisionRejected Decision = "rejected"
	DecisionExpired  Decision = "expired"
)

// Request describes a production change approval request.
type Request struct {
	ID                string    `json:"id"`
	Application       string    `json:"application"`
	Environment       string    `json:"environment"`
	Version           string    `json:"version"`
	ChangeType        string    `json:"change_type"`
	Summary           string    `json:"summary"`
	RiskAssessment    string    `json:"risk_assessment,omitempty"`
	PolicyGateResults string    `json:"policy_gate_results,omitempty"`
	RequestedBy       string    `json:"requested_by"`
	RequestedAt       time.Time `json:"requested_at"`

	Decision  Decision   `json:"decision"`
	DecidedBy string     `json:"decided_by,omitempty"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
	Reason    string     `json:"reason,omitempty"`

	// ExpiresAt is when the request auto-expires if not decided.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// IsApproved returns true if the request has been approved.
func (r Request) IsApproved() bool {
	return r.Decision == DecisionApproved
}

// IsDecided returns true if the request has a final decision (approved,
// rejected, or expired).
func (r Request) IsDecided() bool {
	return r.Decision != DecisionPending
}

// IsExpired returns true if the request has expired without a decision.
func (r Request) IsExpired() bool {
	return r.Decision == DecisionExpired
}

// Workflow manages approval requests for production changes. It is safe
// for concurrent use.
type Workflow struct {
	mu       sync.RWMutex
	requests map[string]*Request
	seq      int
}

// NewWorkflow returns an empty approval workflow.
func NewWorkflow() *Workflow {
	return &Workflow{
		requests: map[string]*Request{},
	}
}

// CreateRequest creates a new approval request for a production deployment.
// If the environment does not require explicit approval, the request is
// auto-approved.
func (w *Workflow) CreateRequest(req Request, env environment.Environment) (Request, error) {
	if req.Application == "" {
		return req, fmt.Errorf("application must not be empty")
	}
	if req.Environment == "" {
		return req, fmt.Errorf("environment must not be empty")
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	w.seq++
	req.ID = fmt.Sprintf("approval-%d", w.seq)
	req.RequestedAt = time.Now()

	if !env.RequireExplicitApproval {
		req.Decision = DecisionApproved
		req.DecidedBy = "auto"
		now := time.Now()
		req.DecidedAt = &now
		req.Reason = "environment does not require explicit approval"
	} else {
		req.Decision = DecisionPending
	}

	w.requests[req.ID] = &req
	return req, nil
}

// Approve marks a pending request as approved.
func (w *Workflow) Approve(requestID, approver, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	req, ok := w.requests[requestID]
	if !ok {
		return fmt.Errorf("approval request %q not found", requestID)
	}
	if req.Decision != DecisionPending {
		return fmt.Errorf("approval request %q has already been decided: %s", requestID, req.Decision)
	}
	req.Decision = DecisionApproved
	req.DecidedBy = approver
	now := time.Now()
	req.DecidedAt = &now
	req.Reason = reason
	return nil
}

// Reject marks a pending request as rejected.
func (w *Workflow) Reject(requestID, rejecter, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	req, ok := w.requests[requestID]
	if !ok {
		return fmt.Errorf("approval request %q not found", requestID)
	}
	if req.Decision != DecisionPending {
		return fmt.Errorf("approval request %q has already been decided: %s", requestID, req.Decision)
	}
	req.Decision = DecisionRejected
	req.DecidedBy = rejecter
	now := time.Now()
	req.DecidedAt = &now
	req.Reason = reason
	return nil
}

// ExpireExpired marks all pending requests past their expiry time as expired.
func (w *Workflow) ExpireExpired() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	count := 0
	now := time.Now()
	for _, req := range w.requests {
		if req.Decision == DecisionPending && req.ExpiresAt != nil && now.After(*req.ExpiresAt) {
			req.Decision = DecisionExpired
			count++
		}
	}
	return count
}

// GetRequest returns the approval request with the given ID.
func (w *Workflow) GetRequest(requestID string) (*Request, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	req, ok := w.requests[requestID]
	if !ok {
		return nil, fmt.Errorf("approval request %q not found", requestID)
	}
	return req, nil
}

// ListRequests returns all approval requests, optionally filtered by
// application and/or decision status.
func (w *Workflow) ListRequests(appFilter string, decisionFilter Decision) []Request {
	w.mu.RLock()
	defer w.mu.RUnlock()
	result := make([]Request, 0, len(w.requests))
	for _, req := range w.requests {
		if appFilter != "" && req.Application != appFilter {
			continue
		}
		if decisionFilter != "" && req.Decision != decisionFilter {
			continue
		}
		result = append(result, *req)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].RequestedAt.Before(result[j].RequestedAt)
	})
	return result
}

// ListPending returns all pending approval requests.
func (w *Workflow) ListPending() []Request {
	return w.ListRequests("", DecisionPending)
}

// CheckApproved returns nil if the request is approved, or an error
// explaining why the deployment cannot proceed.
func (w *Workflow) CheckApproved(requestID string) error {
	req, err := w.GetRequest(requestID)
	if err != nil {
		return err
	}
	switch req.Decision {
	case DecisionApproved:
		return nil
	case DecisionPending:
		return fmt.Errorf("approval request %q is still pending", requestID)
	case DecisionRejected:
		return fmt.Errorf("approval request %q was rejected: %s", requestID, req.Reason)
	case DecisionExpired:
		return fmt.Errorf("approval request %q has expired", requestID)
	default:
		return fmt.Errorf("approval request %q has unknown decision: %s", requestID, req.Decision)
	}
}
