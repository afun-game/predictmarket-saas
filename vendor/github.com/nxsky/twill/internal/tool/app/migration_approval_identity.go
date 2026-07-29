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

package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nxsky/twill/runtime/approval"
	"github.com/nxsky/twill/runtime/environment"
	"github.com/nxsky/twill/runtime/identity"
	"github.com/nxsky/twill/runtime/migration"
	"github.com/nxsky/twill/runtime/tool"
)

func migrationCommand() *tool.Command {
	flags, compact, _ := inspectFlagSet("migration")
	name := flags.String("name", "", "Migration name")
	database := flags.String("database", "primary", "Database name")
	component := flags.String("component", "", "Component that owns the migration")
	riskLevel := flags.String("risk-level", "low", "Risk level: low, medium, high, critical")
	upSQL := flags.String("up-sql", "", "Forward migration SQL (semicolon-separated)")
	downSQL := flags.String("down-sql", "", "Rollback SQL (semicolon-separated)")
	compatibility := flags.String("compatibility", "", "Expand-contract compatibility notes")
	showRollback := flags.Bool("rollback-plan", false, "Show the rollback plan for this migration")
	showPromotionPath := flags.Bool("promotion-path", false, "Show the environment promotion path")

	return &tool.Command{
		Name:        "migration",
		Flags:       flags,
		Description: "Validate a database migration and generate rollback and promotion plans",
		Help: `Usage:
  twill app migration --name NAME [--database DB] [--component COMP]
                      [--risk-level LEVEL] [--up-sql SQL] [--down-sql SQL]
                      [--compatibility NOTES] [--rollback-plan] [--promotion-path]

Description:
  "twill app migration" creates a migration spec from the given parameters,
  validates it for safety issues, and outputs the validation status with
  errors and warnings. Pass --rollback-plan to show the rollback plan.
  Pass --promotion-path to show the environment promotion path
  (dev -> staging -> production).

Risk Levels:
  low, medium, high, critical

Examples:
  twill app migration --name add-users-table --risk-level medium \
    --up-sql "CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL)" \
    --down-sql "DROP TABLE users" --rollback-plan
  twill app migration --promotion-path

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			if *showPromotionPath {
				path := migration.PromotionPath()
				envNames := make([]string, len(path))
				for i, envType := range path {
					envNames[i] = environment.Default(envType).Name
				}
				return encodeJSON(os.Stdout, map[string]any{
					"schema_version":  "twill.migration.promotion_path.v1",
					"promotion_path":  envNames,
					"verify_commands": []string{"twill app migration --promotion-path"},
				}, !*compact, "migration-promotion-path")
			}

			m := migration.Migration{
				ID:            "migration-dry-run",
				Name:          *name,
				Database:      *database,
				Component:     *component,
				RiskLevel:     migration.RiskLevel(*riskLevel),
				UpSQL:         splitSQL(*upSQL),
				DownSQL:       splitSQL(*downSQL),
				Compatibility: *compatibility,
				CreatedAt:     time.Now(),
			}

			m.ValidationStatus = m.Validate()

			result := map[string]any{
				"schema_version":      "twill.migration.plan.v1",
				"migration":           m,
				"validation_passed":   m.ValidationStatus.Validated,
				"validation_errors":   m.ValidationStatus.ValidationErrors,
				"validation_warnings": m.ValidationStatus.ValidationWarnings,
				"checklist":           m.ValidationStatus.Checklist,
				"limitations": []string{
					"Migration plan is a dry-run validation; no database is contacted.",
					"Promotion tracking is in-memory and resets between invocations.",
					"Review SQL and rollback plan with a DBA before applying.",
				},
			}

			if *showRollback {
				result["rollback_plan"] = m.RollbackPlan()
			}

			canPromote, err := m.CanPromote()
			if err == nil {
				result["next_promotion_environment"] = environment.Default(canPromote).Name
			}

			return encodeJSON(os.Stdout, result, !*compact, "migration")
		},
	}
}

func approvalCommand() *tool.Command {
	flags, compact, _ := inspectFlagSet("approval")
	appName := flags.String("app", "", "Application name for the approval request")
	envName := flags.String("environment", "production", "Environment for the approval request")
	version := flags.String("version", "", "Version being deployed")
	changeType := flags.String("change-type", "deployment", "Change type: deployment, migration, config, rollback")
	summary := flags.String("summary", "", "Change summary")
	requestedBy := flags.String("requested-by", "developer", "Requester identity")
	action := flags.String("action", "create", "Action: create, list, approve, reject")
	requestID := flags.String("request-id", "", "Approval request ID (for approve/reject)")
	approver := flags.String("approver", "", "Approver identity (for approve/reject)")
	reason := flags.String("reason", "", "Approval or rejection reason")

	return &tool.Command{
		Name:        "approval",
		Flags:       flags,
		Description: "Manage production change approval requests for deployment governance",
		Help: `Usage:
  twill app approval --action create --app NAME [--environment ENV]
                     [--version VER] [--change-type TYPE] [--summary TEXT]
                     [--requested-by USER]
  twill app approval --action approve --request-id ID --approver USER [--reason TEXT]
  twill app approval --action reject --request-id ID --approver USER [--reason TEXT]
  twill app approval --action list [--app NAME]

Description:
  "twill app approval" manages production change approval requests. The
  create action simulates the approval workflow for a deployment: if the
  environment does not require explicit approval, the request is auto-
  approved; otherwise it enters pending state. Since approval state is
  in-memory, each invocation starts fresh. Use the JSON output to inspect
  the approval decision and evidence for compliance records.

Actions:
  create  Create a new approval request
  approve Approve a pending request (requires --request-id and --approver)
  reject  Reject a pending request (requires --request-id and --approver)
  list    List all requests (optionally filtered by --app)

Examples:
  twill app approval --action create --app my-service --environment production \
    --version v1.2.3 --change-type deployment --summary "Deploy v1.2.3"
  twill app approval --action approve --request-id approval-1 \
    --approver lead --reason "LGTM"

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			wf := approval.NewWorkflow()

			switch *action {
			case "create":
				envType, err := environment.ParseType(*envName)
				if err != nil {
					return err
				}
				env := environment.Default(envType)

				req := approval.Request{
					Application: *appName,
					Environment: *envName,
					Version:     *version,
					ChangeType:  *changeType,
					Summary:     *summary,
					RequestedBy: *requestedBy,
				}
				created, err := wf.CreateRequest(req, env)
				if err != nil {
					return err
				}
				return encodeJSON(os.Stdout, map[string]any{
					"schema_version": "twill.approval.request.v1",
					"request":        created,
					"environment":    env,
					"auto_approved":  created.Decision == approval.DecisionApproved && created.DecidedBy == "auto",
					"limitations": []string{
						"Approval state is in-memory and resets between invocations.",
						"Integrate with a persistent store for production approval tracking.",
					},
				}, !*compact, "approval")

			case "list":
				requests := wf.ListRequests(*appName, "")
				return encodeJSON(os.Stdout, map[string]any{
					"schema_version": "twill.approval.list.v1",
					"requests":       requests,
					"count":          len(requests),
				}, !*compact, "approval-list")

			case "approve":
				if *requestID == "" || *approver == "" {
					return errApprovalMissingArgs("approve", "--request-id", "--approver")
				}
				return encodeJSON(os.Stdout, map[string]any{
					"schema_version": "twill.approval.decision.v1",
					"action":         "approve",
					"request_id":     *requestID,
					"approver":       *approver,
					"reason":         *reason,
					"limitations": []string{
						"Approval state is in-memory; this is a dry-run simulation.",
					},
				}, !*compact, "approval-decision")

			case "reject":
				if *requestID == "" || *approver == "" {
					return errApprovalMissingArgs("reject", "--request-id", "--approver")
				}
				return encodeJSON(os.Stdout, map[string]any{
					"schema_version": "twill.approval.decision.v1",
					"action":         "reject",
					"request_id":     *requestID,
					"approver":       *approver,
					"reason":         *reason,
					"limitations": []string{
						"Approval state is in-memory; this is a dry-run simulation.",
					},
				}, !*compact, "approval-decision")

			default:
				return fmt.Errorf("invalid action %q: must be create, list, approve, or reject", *action)
			}
		},
	}
}

func identityCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("identity")
	checkSecret := flags.String("check-secret", "", "Check if a secret key is accessible from an environment")
	checkEnv := flags.String("check-env", "", "Environment name for secret access check")
	mtls := flags.Bool("mtls", true, "Whether mTLS is enabled for the service identities")

	return &tool.Command{
		Name:        "identity",
		Flags:       flags,
		Description: "List service identity candidates from the app graph and check secret scoping",
		Help: `Usage:
  twill app identity [--dir DIR] [packages...]
  twill app identity --check-secret KEY --check-env ENV

Description:
  "twill app identity" discovers components from the application graph
  and presents them as service identity candidates with mTLS metadata.
  Pass --check-secret with a secret key and --check-env with an
  environment name to verify whether the secret is accessible from that
  environment (returns the scoping decision).

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app identity ./...
    twill app identity --check-secret database-credentials --check-env production ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			if *checkSecret != "" && *checkEnv != "" {
				registry := identity.NewIdentityRegistry()
				err := registry.CheckSecretAccess(*checkSecret, *checkEnv)
				result := map[string]any{
					"schema_version": "twill.identity.access_check.v1",
					"secret_key":     *checkSecret,
					"environment":    *checkEnv,
					"accessible":     err == nil,
				}
				if err != nil {
					result["reason"] = err.Error()
				}
				return encodeJSON(os.Stdout, result, !*compact, "identity-access-check")
			}

			graph, err := InspectGraph(ctx, GraphOptions{
				Dir:      *dir,
				Patterns: args,
			})
			if err != nil {
				return err
			}

			registry := identity.NewIdentityRegistry()
			for _, comp := range graph.Components {
				registry.RegisterIdentity(identity.ServiceIdentity{
					Component:   comp.Name,
					MTLSEnabled: *mtls,
				})
			}

			identities := registry.ListIdentities()
			scopes := registry.ListSecretScopes()

			return encodeJSON(os.Stdout, map[string]any{
				"schema_version": "twill.identity.registry.v1",
				"identities":     identities,
				"secret_scopes":  scopes,
				"identity_count": len(identities),
				"scope_count":    len(scopes),
				"limitations": []string{
					"Identity registry is in-memory and populated from the app graph on each invocation.",
					"Secret scopes must be registered via a persistent store for production use.",
					"mTLS certificate references are templates; configure for your PKI infrastructure.",
				},
			}, !*compact, "identity")
		},
	}
}

func splitSQL(sql string) []string {
	if strings.TrimSpace(sql) == "" {
		return nil
	}
	parts := strings.Split(sql, ";")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func errApprovalMissingArgs(action string, required ...string) error {
	return fmt.Errorf("action %q requires: %s", action, strings.Join(required, ", "))
}
