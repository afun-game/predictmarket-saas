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

// Package template provides an enterprise template catalog for scaffolding
// new Twill services from predefined patterns. Templates include resource
// declarations, component structure, tests, and deployment configuration.
package template

import (
	"fmt"
	"sort"
)

// Category classifies a template by use case.
type Category string

const (
	CategoryService   Category = "service"
	CategoryWorker    Category = "worker"
	CategoryCronJob   Category = "cron_job"
	CategoryMigration Category = "migration"
	CategoryAPI       Category = "api"
)

// Template describes a reusable project template.
type Template struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Category    Category           `json:"category"`
	Description string             `json:"description"`
	Resources   []TemplateResource `json:"resources,omitempty"`
	Files       []TemplateFile     `json:"files"`
	VerifySteps []string           `json:"verify_steps,omitempty"`
}

// TemplateResource describes a resource declaration included in the template.
type TemplateResource struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// TemplateFile describes a file to be generated from the template.
type TemplateFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Catalog holds the collection of available templates.
type Catalog struct {
	templates map[string]Template
}

// NewCatalog returns a catalog populated with the baseline templates.
func NewCatalog() *Catalog {
	c := &Catalog{templates: map[string]Template{}}
	for _, tmpl := range baselineTemplates() {
		c.templates[tmpl.ID] = tmpl
	}
	return c
}

// List returns all templates, sorted by ID.
func (c *Catalog) List() []Template {
	result := make([]Template, 0, len(c.templates))
	for _, t := range c.templates {
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

// Get returns the template with the given ID.
func (c *Catalog) Get(id string) (Template, error) {
	t, ok := c.templates[id]
	if !ok {
		return Template{}, fmt.Errorf("template %q not found", id)
	}
	return t, nil
}

// Add registers a custom template in the catalog.
func (c *Catalog) Add(tmpl Template) error {
	if tmpl.ID == "" {
		return fmt.Errorf("template ID must not be empty")
	}
	c.templates[tmpl.ID] = tmpl
	return nil
}

// ListByCategory returns templates filtered by category.
func (c *Catalog) ListByCategory(cat Category) []Template {
	var result []Template
	for _, t := range c.templates {
		if t.Category == cat {
			result = append(result, t)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

func baselineTemplates() []Template {
	return []Template{
		{
			ID:          "service-database",
			Name:        "Service with Database",
			Category:    CategoryService,
			Description: "A Twill service component with a PostgreSQL database resource, CRUD endpoints, and tests.",
			Resources: []TemplateResource{
				{Kind: "database", Name: "db", Type: "github.com/nxsky/twill.Database"},
			},
			Files: []TemplateFile{
				{
					Path:    "main.go",
					Content: "package app\n\nimport (\n\t\"github.com/nxsky/twill\"\n)\n\ntype Service struct {\n\tDB twill.Database\n}\n",
				},
				{
					Path:    "service_test.go",
					Content: "package app\n\nimport \"testing\"\n\nfunc TestService(t *testing.T) {\n\t// Test service with database\n}\n",
				},
			},
			VerifySteps: []string{
				"go build ./...",
				"go test ./...",
				"twill app graph ./...",
				"twill deploy compose ./...",
			},
		},
		{
			ID:          "service-cache",
			Name:        "Service with Cache",
			Category:    CategoryService,
			Description: "A Twill service component with a Redis cache resource and read-through caching pattern.",
			Resources: []TemplateResource{
				{Kind: "cache", Name: "cache", Type: "github.com/nxsky/twill.Cache"},
			},
			Files: []TemplateFile{
				{
					Path:    "main.go",
					Content: "package app\n\nimport (\n\t\"github.com/nxsky/twill\"\n)\n\ntype CachedService struct {\n\tCache twill.Cache\n}\n",
				},
			},
			VerifySteps: []string{"go build ./...", "go test ./...", "twill deploy compose ./..."},
		},
		{
			ID:          "service-pubsub",
			Name:        "Service with Pub/Sub",
			Category:    CategoryService,
			Description: "A Twill service component with a Pub/Sub resource for event publishing and subscription.",
			Resources: []TemplateResource{
				{Kind: "pubsub", Name: "events", Type: "github.com/nxsky/twill.PubSub"},
			},
			Files: []TemplateFile{
				{
					Path:    "main.go",
					Content: "package app\n\nimport (\n\t\"github.com/nxsky/twill\"\n)\n\ntype EventService struct {\n\tPubSub twill.PubSub\n}\n",
				},
			},
			VerifySteps: []string{"go build ./...", "go test ./...", "twill deploy compose ./..."},
		},
		{
			ID:          "service-fullstack",
			Name:        "Full-stack Service",
			Category:    CategoryService,
			Description: "A Twill service with database, cache, and pub/sub resources for a complete application backend.",
			Resources: []TemplateResource{
				{Kind: "database", Name: "db", Type: "github.com/nxsky/twill.Database"},
				{Kind: "cache", Name: "cache", Type: "github.com/nxsky/twill.Cache"},
				{Kind: "pubsub", Name: "events", Type: "github.com/nxsky/twill.PubSub"},
			},
			Files: []TemplateFile{
				{
					Path:    "main.go",
					Content: "package app\n\nimport (\n\t\"github.com/nxsky/twill\"\n)\n\ntype FullService struct {\n\tDB     twill.Database\n\tCache  twill.Cache\n\tPubSub twill.PubSub\n}\n",
				},
				{
					Path:    "service_test.go",
					Content: "package app\n\nimport \"testing\"\n\nfunc TestFullService(t *testing.T) {\n\t// Test full-stack service\n}\n",
				},
			},
			VerifySteps: []string{
				"go build ./...",
				"go test ./...",
				"twill app graph ./...",
				"twill deploy compose ./...",
				"twill deploy k8s --image <image> ./...",
			},
		},
		{
			ID:          "worker-queue",
			Name:        "Queue Worker",
			Category:    CategoryWorker,
			Description: "A Twill worker component that processes messages from a queue with retry and dead-letter handling.",
			Resources: []TemplateResource{
				{Kind: "queue", Name: "jobs", Type: "github.com/nxsky/twill.PubSub"},
			},
			Files: []TemplateFile{
				{
					Path:    "main.go",
					Content: "package app\n\nimport (\n\t\"github.com/nxsky/twill\"\n)\n\ntype Worker struct {\n\tQueue twill.PubSub\n}\n",
				},
			},
			VerifySteps: []string{"go build ./...", "go test ./..."},
		},
		{
			ID:          "cron-cleanup",
			Name:        "Cron Cleanup Job",
			Category:    CategoryCronJob,
			Description: "A Twill cron job component that runs scheduled cleanup tasks with database access.",
			Resources: []TemplateResource{
				{Kind: "cron", Name: "cleanup", Type: "github.com/nxsky/twill.Cron"},
				{Kind: "database", Name: "db", Type: "github.com/nxsky/twill.Database"},
			},
			Files: []TemplateFile{
				{
					Path:    "main.go",
					Content: "package app\n\nimport (\n\t\"github.com/nxsky/twill\"\n)\n\ntype CleanupJob struct {\n\tCron twill.Cron\n\tDB   twill.Database\n}\n",
				},
			},
			VerifySteps: []string{"go build ./...", "go test ./..."},
		},
		{
			ID:          "api-crud",
			Name:        "CRUD API Service",
			Category:    CategoryAPI,
			Description: "A Twill API service with CRUD endpoints, OpenAPI generation, and contract tests.",
			Resources: []TemplateResource{
				{Kind: "database", Name: "db", Type: "github.com/nxsky/twill.Database"},
			},
			Files: []TemplateFile{
				{
					Path:    "main.go",
					Content: "package app\n\nimport (\n\t\"github.com/nxsky/twill\"\n)\n\ntype CRUDService struct {\n\tDB twill.Database\n}\n",
				},
				{
					Path:    "docs/endpoints/api.md",
					Content: "# CRUD API Endpoints\n\n## POST /api/items\nCreate a new item.\n\n## GET /api/items\nList all items.\n\n## GET /api/items/{id}\nGet an item by ID.\n\n## PUT /api/items/{id}\nUpdate an item.\n\n## DELETE /api/items/{id}\nDelete an item.\n",
				},
			},
			VerifySteps: []string{
				"go build ./...",
				"go test ./...",
				"twill app openapi ./...",
				"twill app contract-tests ./...",
			},
		},
		{
			ID:          "migration-expand-contract",
			Name:        "Expand-Contract Migration",
			Category:    CategoryMigration,
			Description: "A database migration template using the expand-contract pattern with forward and rollback SQL.",
			Files: []TemplateFile{
				{
					Path:    "migrations/001_expand.up.sql",
					Content: "-- Expand phase: add new column\nALTER TABLE items ADD COLUMN status VARCHAR(20) DEFAULT 'active';\n",
				},
				{
					Path:    "migrations/001_expand.down.sql",
					Content: "-- Rollback expand phase\nALTER TABLE items DROP COLUMN status;\n",
				},
				{
					Path:    "migrations/002_contract.up.sql",
					Content: "-- Contract phase: remove old column after application switch\nALTER TABLE items DROP COLUMN old_status;\n",
				},
				{
					Path:    "migrations/002_contract.down.sql",
					Content: "-- Rollback contract phase\nALTER TABLE items ADD COLUMN old_status VARCHAR(20);\n",
				},
			},
			VerifySteps: []string{
				"twill mcp generate-db-migration --name expand-contract --database postgres",
				"twill mcp plan-migration --dir .",
			},
		},
		// Reference applications for Phase 5.
		{
			ID:          "ref-saas",
			Name:        "SaaS Multi-tenant Application",
			Category:    CategoryService,
			Description: "Reference SaaS application with multi-tenant database, cache, pub/sub for tenant events, and per-tenant secret scoping.",
			Resources: []TemplateResource{
				{Kind: "database", Name: "tenant-db", Type: "github.com/nxsky/twill.Database"},
				{Kind: "cache", Name: "tenant-cache", Type: "github.com/nxsky/twill.Cache"},
				{Kind: "pubsub", Name: "tenant-events", Type: "github.com/nxsky/twill.PubSub"},
				{Kind: "secret", Name: "tenant-secrets", Type: "github.com/nxsky/twill.Secret"},
			},
			Files: []TemplateFile{
				{Path: "main.go", Content: "package app\n\nimport (\n\t\"github.com/nxsky/twill\"\n)\n\ntype SaaSService struct {\n\tDB     twill.Database\n\tCache  twill.Cache\n\tPubSub twill.PubSub\n\tSecret twill.Secret\n}\n"},
				{Path: "docs/endpoints/api.md", Content: "# SaaS API\n\n## POST /api/tenants\nCreate a new tenant.\n\n## GET /api/tenants/{id}\nGet tenant details.\n\n## POST /api/tenants/{id}/events\nPublish a tenant event.\n"},
			},
			VerifySteps: []string{"go build ./...", "go test ./...", "twill app graph ./...", "twill deploy compose ./...", "twill deploy k8s --image <image> ./..."},
		},
		{
			ID:          "ref-ecommerce",
			Name:        "Ecommerce Platform",
			Category:    CategoryService,
			Description: "Reference ecommerce platform with product catalog, order processing, inventory cache, event-driven notifications, and object storage for product images.",
			Resources: []TemplateResource{
				{Kind: "database", Name: "catalog-db", Type: "github.com/nxsky/twill.Database"},
				{Kind: "cache", Name: "inventory-cache", Type: "github.com/nxsky/twill.Cache"},
				{Kind: "pubsub", Name: "order-events", Type: "github.com/nxsky/twill.PubSub"},
				{Kind: "object_storage", Name: "product-images", Type: "cloud.google.com/go/storage.BucketHandle"},
			},
			Files: []TemplateFile{
				{Path: "main.go", Content: "package app\n\nimport (\n\t\"github.com/nxsky/twill\"\n)\n\ntype EcommerceService struct {\n\tDB           twill.Database\n\tCache        twill.Cache\n\tPubSub       twill.PubSub\n\tObjectStorage interface{}\n}\n"},
				{Path: "docs/endpoints/api.md", Content: "# Ecommerce API\n\n## GET /api/products\nList products.\n\n## POST /api/orders\nCreate an order.\n\n## GET /api/orders/{id}\nGet order status.\n"},
			},
			VerifySteps: []string{"go build ./...", "go test ./...", "twill app graph ./...", "twill deploy compose ./..."},
		},
		{
			ID:          "ref-ai-agent",
			Name:        "AI Agent Backend",
			Category:    CategoryService,
			Description: "Reference AI agent backend with task queue, pub/sub for agent events, cache for conversation state, and cron for scheduled agent runs.",
			Resources: []TemplateResource{
				{Kind: "pubsub", Name: "agent-events", Type: "github.com/nxsky/twill.PubSub"},
				{Kind: "cache", Name: "conversation-state", Type: "github.com/nxsky/twill.Cache"},
				{Kind: "cron", Name: "scheduled-runs", Type: "github.com/nxsky/twill.Cron"},
				{Kind: "secret", Name: "api-keys", Type: "github.com/nxsky/twill.Secret"},
			},
			Files: []TemplateFile{
				{Path: "main.go", Content: "package app\n\nimport (\n\t\"github.com/nxsky/twill\"\n)\n\ntype AgentBackend struct {\n\tPubSub  twill.PubSub\n\tCache   twill.Cache\n\tCron    twill.Cron\n\tSecret  twill.Secret\n}\n"},
				{Path: "docs/endpoints/api.md", Content: "# AI Agent API\n\n## POST /api/agents/{id}/run\nTrigger an agent run.\n\n## GET /api/agents/{id}/status\nGet agent run status.\n\n## POST /api/agents/{id}/events\nSubscribe to agent events.\n"},
			},
			VerifySteps: []string{"go build ./...", "go test ./...", "twill app graph ./...", "twill deploy compose ./..."},
		},
		{
			ID:          "ref-iot",
			Name:        "IoT Data Ingestion",
			Category:    CategoryService,
			Description: "Reference IoT backend with high-throughput pub/sub for device telemetry, time-series database, cache for device state, and cron for data aggregation.",
			Resources: []TemplateResource{
				{Kind: "pubsub", Name: "telemetry", Type: "github.com/nxsky/twill.PubSub"},
				{Kind: "database", Name: "timeseries-db", Type: "github.com/nxsky/twill.Database"},
				{Kind: "cache", Name: "device-state", Type: "github.com/nxsky/twill.Cache"},
				{Kind: "cron", Name: "aggregator", Type: "github.com/nxsky/twill.Cron"},
			},
			Files: []TemplateFile{
				{Path: "main.go", Content: "package app\n\nimport (\n\t\"github.com/nxsky/twill\"\n)\n\ntype IoTBackend struct {\n\tPubSub twill.PubSub\n\tDB     twill.Database\n\tCache  twill.Cache\n\tCron   twill.Cron\n}\n"},
				{Path: "docs/endpoints/api.md", Content: "# IoT API\n\n## POST /api/devices/{id}/telemetry\nIngest device telemetry.\n\n## GET /api/devices/{id}/state\nGet current device state.\n"},
			},
			VerifySteps: []string{"go build ./...", "go test ./...", "twill app graph ./...", "twill deploy compose ./..."},
		},
		{
			ID:          "ref-financial-ledger",
			Name:        "Financial Ledger Service",
			Category:    CategoryService,
			Description: "Reference financial ledger with append-only database, event-driven audit log via pub/sub, secret-scoped encryption keys, and cache for balance queries.",
			Resources: []TemplateResource{
				{Kind: "database", Name: "ledger-db", Type: "github.com/nxsky/twill.Database"},
				{Kind: "pubsub", Name: "audit-log", Type: "github.com/nxsky/twill.PubSub"},
				{Kind: "secret", Name: "encryption-keys", Type: "github.com/nxsky/twill.Secret"},
				{Kind: "cache", Name: "balance-cache", Type: "github.com/nxsky/twill.Cache"},
			},
			Files: []TemplateFile{
				{Path: "main.go", Content: "package app\n\nimport (\n\t\"github.com/nxsky/twill\"\n)\n\ntype LedgerService struct {\n\tDB     twill.Database\n\tPubSub twill.PubSub\n\tSecret twill.Secret\n\tCache  twill.Cache\n}\n"},
				{Path: "docs/endpoints/api.md", Content: "# Financial Ledger API\n\n## POST /api/entries\nCreate a ledger entry (append-only).\n\n## GET /api/accounts/{id}/balance\nGet account balance.\n\n## GET /api/accounts/{id}/history\nGet account transaction history.\n"},
			},
			VerifySteps: []string{"go build ./...", "go test ./...", "twill app graph ./...", "twill deploy compose ./...", "twill mcp review-security ./..."},
		},
	}
}
