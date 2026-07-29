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

// Package migration defines a database migration workflow model with
// validation, rollback planning, and environment promotion. It formalizes
// the safety checks that gate schema changes across environments.
package migration

import (
	"fmt"
	"strings"
	"time"

	"github.com/nxsky/twill/runtime/environment"
)

// RiskLevel classifies the risk of a migration.
type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

// Step represents one phase of an expand-contract migration.
type Step struct {
	Name        string   `json:"name"`
	Phase       string   `json:"phase"`
	SQLOrder    []string `json:"sql_order,omitempty"`
	Description string   `json:"description,omitempty"`
}

// Migration describes a database schema change with forward and rollback
// SQL, validation status, and promotion tracking.
type Migration struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Database      string    `json:"database"`
	Component     string    `json:"component,omitempty"`
	RiskLevel     RiskLevel `json:"risk_level"`
	UpSQL         []string  `json:"up_sql"`
	DownSQL       []string  `json:"down_sql,omitempty"`
	Steps         []Step    `json:"steps,omitempty"`
	Compatibility string    `json:"compatibility,omitempty"`
	Backfill      string    `json:"backfill,omitempty"`
	CreatedAt     time.Time `json:"created_at"`

	// ValidationStatus tracks whether the migration has been validated.
	ValidationStatus ValidationStatus `json:"validation_status"`

	// Promotion tracks the migration's progress through environments.
	Promotion PromotionTracker `json:"promotion"`
}

// ValidationStatus describes the validation state of a migration.
type ValidationStatus struct {
	Validated          bool     `json:"validated"`
	ValidationErrors   []string `json:"validation_errors,omitempty"`
	ValidationWarnings []string `json:"validation_warnings,omitempty"`
	Checklist          []string `json:"checklist,omitempty"`
}

// PromotionTracker tracks which environments have received the migration.
type PromotionTracker struct {
	AppliedEnvironments []string `json:"applied_environments"`
	CurrentEnvironment  string   `json:"current_environment,omitempty"`
	PendingEnvironments []string `json:"pending_environments,omitempty"`
}

// PromotionPath returns the ordered environments for promotion based on
// the standard path: dev -> staging -> production. Local and preview
// environments are not part of the formal promotion path.
func PromotionPath() []environment.Type {
	return []environment.Type{
		environment.TypeDev,
		environment.TypeStaging,
		environment.TypeProduction,
	}
}

// Validate checks the migration for safety issues and returns updated
// validation status. It checks for:
// - Non-empty up SQL
// - Rollback SQL for medium and higher risk migrations
// - Expand-contract compatibility notes for high and critical risk
// - Lock impact considerations for critical risk
func (m Migration) Validate() ValidationStatus {
	status := ValidationStatus{
		Checklist: defaultValidationChecklist(),
	}

	if len(m.UpSQL) == 0 {
		status.ValidationErrors = append(status.ValidationErrors, "up SQL must not be empty")
	}

	if m.RiskLevel == RiskMedium || m.RiskLevel == RiskHigh || m.RiskLevel == RiskCritical {
		if len(m.DownSQL) == 0 {
			status.ValidationErrors = append(status.ValidationErrors, "rollback (down) SQL is required for medium risk and above")
		}
	}

	if m.RiskLevel == RiskHigh || m.RiskLevel == RiskCritical {
		if strings.TrimSpace(m.Compatibility) == "" {
			status.ValidationWarnings = append(status.ValidationWarnings, "compatibility notes (expand-contract strategy) are recommended for high risk and above")
		}
	}

	if m.RiskLevel == RiskCritical {
		hasLockNote := false
		for _, item := range status.Checklist {
			if strings.Contains(strings.ToLower(item), "lock") {
				hasLockNote = true
				break
			}
		}
		if !hasLockNote {
			status.ValidationWarnings = append(status.ValidationWarnings, "lock impact analysis is required for critical risk migrations")
		}
	}

	status.Validated = len(status.ValidationErrors) == 0
	return status
}

// CanPromote checks whether the migration can be promoted to the next
// environment in the promotion path. The migration must be validated and
// already applied to the current environment.
func (m Migration) CanPromote() (environment.Type, error) {
	if !m.ValidationStatus.Validated {
		return "", fmt.Errorf("migration must be validated before promotion")
	}

	path := PromotionPath()
	appliedSet := make(map[string]bool, len(m.Promotion.AppliedEnvironments))
	for _, env := range m.Promotion.AppliedEnvironments {
		appliedSet[env] = true
	}

	for _, envType := range path {
		envName := environment.Default(envType).Name
		if !appliedSet[envName] {
			return envType, nil
		}
	}

	return "", fmt.Errorf("migration has been applied to all environments in the promotion path")
}

// PromoteTo marks the migration as applied to the given environment and
// updates the promotion tracker. It validates that the environment is part
// of the promotion path and that all preceding environments have been
// applied.
func (m *Migration) PromoteTo(env environment.Environment) error {
	if !m.ValidationStatus.Validated {
		return fmt.Errorf("migration must be validated before promotion to %s", env.Name)
	}

	path := PromotionPath()
	envIndex := -1
	for i, envType := range path {
		if envType == env.Type {
			envIndex = i
			break
		}
	}
	if envIndex == -1 {
		return fmt.Errorf("environment %s (%s) is not in the promotion path", env.Name, env.Type)
	}

	appliedSet := make(map[string]bool, len(m.Promotion.AppliedEnvironments))
	for _, applied := range m.Promotion.AppliedEnvironments {
		appliedSet[applied] = true
	}

	for i := 0; i < envIndex; i++ {
		prevEnvName := environment.Default(path[i]).Name
		if !appliedSet[prevEnvName] {
			return fmt.Errorf("cannot promote to %s before applying to %s", env.Name, prevEnvName)
		}
	}

	if appliedSet[env.Name] {
		return fmt.Errorf("migration has already been applied to %s", env.Name)
	}

	m.Promotion.AppliedEnvironments = append(m.Promotion.AppliedEnvironments, env.Name)
	m.Promotion.CurrentEnvironment = env.Name

	pending := []string{}
	for i := envIndex + 1; i < len(path); i++ {
		pendingName := environment.Default(path[i]).Name
		if !appliedSet[pendingName] {
			pending = append(pending, pendingName)
		}
	}
	m.Promotion.PendingEnvironments = pending

	return nil
}

// RollbackPlan returns the rollback steps for the migration. If down SQL
// is present, it is included. For expand-contract migrations, the rollback
// plan also includes the contract step.
func (m Migration) RollbackPlan() []string {
	steps := []string{}

	if len(m.DownSQL) > 0 {
		steps = append(steps, "Apply down SQL to reverse schema changes:")
		for _, sql := range m.DownSQL {
			steps = append(steps, "  "+sql)
		}
	} else {
		steps = append(steps, "No down SQL provided; manual rollback required.")
	}

	if strings.Contains(strings.ToLower(m.Compatibility), "expand-contract") {
		steps = append(steps, "Rollback the contract phase (revert application code to pre-expansion version).")
		steps = append(steps, "Verify backward compatibility after rollback.")
	}

	steps = append(steps, "Run validation queries to confirm schema state after rollback.")

	return steps
}

func defaultValidationChecklist() []string {
	return []string{
		"Verify forward migration SQL is syntactically correct.",
		"Verify rollback migration SQL is syntactically correct.",
		"Check lock impact on large tables (USE INDEX, batch sizing).",
		"Verify data consistency before and after migration.",
		"Test migration on a copy of production data.",
		"Verify application code compatibility with new schema.",
		"Confirm backup or snapshot is available before applying.",
	}
}
