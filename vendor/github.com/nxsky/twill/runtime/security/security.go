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

// Package security defines the security response process for the Twill
// platform. It provides security advisory tracking, severity
// classification, vulnerability reporting workflow, and CVE coordination
// metadata.
package security

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Severity classifies the severity of a security advisory.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

// AdvisoryState describes the lifecycle state of a security advisory.
type AdvisoryState string

const (
	StateDraft         AdvisoryState = "draft"
	StateReported      AdvisoryState = "reported"
	StateTriaged       AdvisoryState = "triaged"
	StateFixInProgress AdvisoryState = "fix_in_progress"
	StateFixed         AdvisoryState = "fixed"
	StatePublished     AdvisoryState = "published"
	StateWithdrawn     AdvisoryState = "withdrawn"
)

// Advisory describes a security advisory for the Twill platform.
type Advisory struct {
	ID               string        `json:"id"`
	Title            string        `json:"title"`
	Severity         Severity      `json:"severity"`
	State            AdvisoryState `json:"state"`
	Summary          string        `json:"summary"`
	AffectedVersions []string      `json:"affected_versions,omitempty"`
	FixedVersion     string        `json:"fixed_version,omitempty"`
	CVE              string        `json:"cve,omitempty"`
	Component        string        `json:"component,omitempty"`
	Reporter         string        `json:"reporter,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	PublishedAt      *time.Time    `json:"published_at,omitempty"`
	Remediation      string        `json:"remediation,omitempty"`
	References       []string      `json:"references,omitempty"`
}

// IsFixed returns true if the advisory has a fix available.
func (a Advisory) IsFixed() bool {
	return a.State == StateFixed || a.State == StatePublished
}

// IsPublished returns true if the advisory has been publicly disclosed.
func (a Advisory) IsPublished() bool {
	return a.State == StatePublished
}

// ResponseProcess manages security advisories. It is safe for concurrent use.
type ResponseProcess struct {
	mu         sync.RWMutex
	advisories map[string]*Advisory
	seq        int
}

// NewResponseProcess returns an empty security response process.
func NewResponseProcess() *ResponseProcess {
	return &ResponseProcess{
		advisories: map[string]*Advisory{},
	}
}

// CreateAdvisory creates a new security advisory in draft state.
func (rp *ResponseProcess) CreateAdvisory(adv Advisory) (Advisory, error) {
	if adv.Title == "" {
		return adv, fmt.Errorf("advisory title must not be empty")
	}
	if adv.Severity == "" {
		return adv, fmt.Errorf("advisory severity must not be empty")
	}
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.seq++
	adv.ID = fmt.Sprintf("TSA-%04d", rp.seq)
	adv.State = StateDraft
	adv.CreatedAt = time.Now()
	adv.UpdatedAt = adv.CreatedAt
	rp.advisories[adv.ID] = &adv
	return adv, nil
}

// Transition changes the state of an advisory.
func (rp *ResponseProcess) Transition(advisoryID string, newState AdvisoryState) error {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	adv, ok := rp.advisories[advisoryID]
	if !ok {
		return fmt.Errorf("advisory %q not found", advisoryID)
	}
	if !isValidTransition(adv.State, newState) {
		return fmt.Errorf("invalid state transition from %s to %s", adv.State, newState)
	}
	adv.State = newState
	adv.UpdatedAt = time.Now()
	if newState == StatePublished {
		now := time.Now()
		adv.PublishedAt = &now
	}
	return nil
}

// GetAdvisory returns the advisory with the given ID.
func (rp *ResponseProcess) GetAdvisory(id string) (*Advisory, error) {
	rp.mu.RLock()
	defer rp.mu.RUnlock()
	adv, ok := rp.advisories[id]
	if !ok {
		return nil, fmt.Errorf("advisory %q not found", id)
	}
	return adv, nil
}

// ListAdvisories returns all advisories, optionally filtered by state
// and/or severity.
func (rp *ResponseProcess) ListAdvisories(stateFilter AdvisoryState, severityFilter Severity) []Advisory {
	rp.mu.RLock()
	defer rp.mu.RUnlock()
	result := make([]Advisory, 0, len(rp.advisories))
	for _, adv := range rp.advisories {
		if stateFilter != "" && adv.State != stateFilter {
			continue
		}
		if severityFilter != "" && adv.Severity != severityFilter {
			continue
		}
		result = append(result, *adv)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result
}

// ListPublished returns all published advisories.
func (rp *ResponseProcess) ListPublished() []Advisory {
	return rp.ListAdvisories(StatePublished, "")
}

// ListUnfixed returns all advisories that do not have a fix available.
func (rp *ResponseProcess) ListUnfixed() []Advisory {
	rp.mu.RLock()
	defer rp.mu.RUnlock()
	result := make([]Advisory, 0, len(rp.advisories))
	for _, adv := range rp.advisories {
		if !adv.IsFixed() {
			result = append(result, *adv)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result
}

// SetRemediation updates the remediation guidance for an advisory.
func (rp *ResponseProcess) SetRemediation(advisoryID, remediation string) error {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	adv, ok := rp.advisories[advisoryID]
	if !ok {
		return fmt.Errorf("advisory %q not found", advisoryID)
	}
	adv.Remediation = remediation
	adv.UpdatedAt = time.Now()
	return nil
}

// SetFixedVersion updates the fixed version for an advisory.
func (rp *ResponseProcess) SetFixedVersion(advisoryID, version string) error {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	adv, ok := rp.advisories[advisoryID]
	if !ok {
		return fmt.Errorf("advisory %q not found", advisoryID)
	}
	adv.FixedVersion = version
	adv.UpdatedAt = time.Now()
	return nil
}

func isValidTransition(from, to AdvisoryState) bool {
	transitions := map[AdvisoryState][]AdvisoryState{
		StateDraft:         {StateReported, StateWithdrawn},
		StateReported:      {StateTriaged, StateWithdrawn},
		StateTriaged:       {StateFixInProgress, StateWithdrawn},
		StateFixInProgress: {StateFixed, StateTriaged},
		StateFixed:         {StatePublished},
		StatePublished:     {},
		StateWithdrawn:     {},
	}
	allowed, ok := transitions[from]
	if !ok {
		return false
	}
	for _, a := range allowed {
		if a == to {
			return true
		}
	}
	return false
}

// SecurityPolicy describes the platform's security response policy.
type SecurityPolicy struct {
	ResponseTimeSLA  map[Severity]string `json:"response_time_sla"`
	DisclosurePolicy string              `json:"disclosure_policy"`
	ReportingChannel string              `json:"reporting_channel"`
}

// DefaultSecurityPolicy returns the default security response policy.
func DefaultSecurityPolicy() SecurityPolicy {
	return SecurityPolicy{
		ResponseTimeSLA: map[Severity]string{
			SeverityCritical: "24 hours initial response, 7 days fix",
			SeverityHigh:     "48 hours initial response, 14 days fix",
			SeverityMedium:   "5 business days initial response, 30 days fix",
			SeverityLow:      "10 business days initial response, 90 days fix",
		},
		DisclosurePolicy: "Coordinated disclosure: advisories are published after a fix is available or after 90 days from report, whichever comes first.",
		ReportingChannel: "Report security vulnerabilities to security@twill.dev with details, reproduction steps, and affected versions.",
	}
}
