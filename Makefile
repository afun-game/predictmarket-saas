MAKEFILE_PATH := $(abspath $(lastword $(MAKEFILE_LIST)))
PROJECT_ROOT := $(dir $(MAKEFILE_PATH))

.PHONY: help
help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: setup
setup: ## Setup development environment
	@echo "Setting up development environment..."
	go mod download
	cp .env.example .env
	@echo "Done! Edit .env file with your configuration"

.PHONY: generate
generate: ## Generate Twill code
	@echo "Generating Twill code..."
	@TWILL_GENERATOR="$$(mktemp)"; \
	trap 'rm -f "$$TWILL_GENERATOR"' EXIT; \
	go build -mod=mod -o "$$TWILL_GENERATOR" github.com/nxsky/twill/cmd/twill; \
	"$$TWILL_GENERATOR" generate ./...

.PHONY: build
build: ## Build the application
	@echo "Building application..."
	go build -buildvcs=false -o bin/predictmarket ./cmd/api

.PHONY: run
run: ## Run the application locally
	@echo "Running application..."
	SERVICETWILL_CONFIG=$(PROJECT_ROOT)twill.toml go run -buildvcs=false ./cmd/api

.PHONY: merchant-sim
merchant-sim: ## Run the V3 merchant callback/webhook simulator
	go run ./cmd/merchant-sim

.PHONY: merchant-portal
merchant-portal: ## Run the local merchant portal against a target API (default localhost:8080)
	go run ./cmd/merchant-portal -api "$${PORTAL_API:-http://localhost:8080}"

.PHONY: test
test: ## Run tests
	@echo "Running tests..."
	go test -v -race -coverprofile=coverage.out ./...

.PHONY: test-coverage
test-coverage: test ## Run tests with coverage report
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

.PHONY: test-integration
test-integration: db-up db-migrate test-integration-ci ## Run PostgreSQL, Redis, and NATS integration tests locally

.PHONY: test-integration-ci
test-integration-ci: ## Run integration tests against already-running dependencies
	INTEGRATION_TEST=1 \
		DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
		REDIS_URL="$${REDIS_URL:-redis://localhost:6379/0}" \
		go test -race -count=1 -run '^TestEventPostgresRedisIntegration$$' ./internal/event
	INTEGRATION_TEST=1 \
		DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
		go test -race -count=1 -run '^TestMarketPostgresIntegration$$' ./internal/market
	INTEGRATION_TEST=1 \
		DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
		go test -race -count=1 -run '^TestWalletPostgresIntegration$$' ./internal/wallet
	INTEGRATION_TEST=1 \
		DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
		go test -race -count=1 -run '^TestPostgresQueryServiceIntegration$$' ./internal/v2query
	INTEGRATION_TEST=1 \
		DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
		go test -race -count=1 -run '^TestPostgresReconciliation' ./internal/reconciliation
	INTEGRATION_TEST=1 \
		DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
		go test -race -count=1 -run '^TestOrderPostgresIntegration$$' ./internal/order
	INTEGRATION_TEST=1 \
		DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
		go test -race -count=1 -run '^TestSettlementPostgres' ./internal/settlement
	INTEGRATION_TEST=1 \
		DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
		go test -race -count=1 -run '^TestSeamlessChaos' ./internal/callback
	INTEGRATION_TEST=1 \
		DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
		NATS_URL="$${NATS_URL:-nats://localhost:4222}" \
		go test -race -count=1 -run '^TestSettlementWorkerPostgresNATSIntegration$$' ./internal/settlementworker
	INTEGRATION_TEST=1 \
		DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
		REDIS_URL="$${REDIS_URL:-redis://localhost:6379/0}" \
		go test -race -count=1 -run '^TestCurrencyPostgresRedisIntegration$$' ./internal/currency
	INTEGRATION_TEST=1 \
		DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
		REDIS_URL="$${REDIS_URL:-redis://localhost:6379/0}" \
		go test -race -count=1 -run '^TestSportsPostgresRedisIntegration$$' ./internal/sports
	INTEGRATION_TEST=1 \
		DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
		REDIS_URL="$${REDIS_URL:-redis://localhost:6379/0}" \
		go test -race -count=1 -run '^TestAnalyticsPostgresRedisIntegration$$' ./internal/analytics

.PHONY: test-e2e
test-e2e: db-up db-migrate test-e2e-ci ## Run the full HTTP, PostgreSQL, Redis, and NATS business flow locally

.PHONY: test-e2e-ci
test-e2e-ci: build ## Run the full HTTP E2E flow against already-running dependencies
	@set -eu; \
		E2E_LOG="$$(mktemp)"; \
		SERVICETWILL_CONFIG="$(PROJECT_ROOT)twill.toml" ADMIN_API_KEY=e2e-admin-secret \
			ADMIN_USERNAME=e2e-admin ADMIN_PASSWORD=e2e-admin-password \
			MERCHANT_SECRET_ENCRYPTION_KEY=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY \
			SESSION_JWT_SECRET=MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY \
			HOSTED_UI_URL=https://play.e2e.test/launch \
			GLOBAL_RATE_LIMIT=100000 \
			V3_ALLOW_PRIVATE_CALLBACK_URLS=1 \
			V3_ORDER_RATE_LIMIT=100000 \
			V3_QUERY_RATE_LIMIT=100000 \
			V3_USER_RATE_LIMIT=100000 \
			DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
			REDIS_URL="$${REDIS_URL:-redis://localhost:6379/0}" \
			NATS_URL="$${NATS_URL:-nats://localhost:4222}" \
			./bin/predictmarket >"$$E2E_LOG" 2>&1 & \
		E2E_PID="$$!"; \
		cleanup() { kill "$$E2E_PID" 2>/dev/null || true; wait "$$E2E_PID" 2>/dev/null || true; rm -f "$$E2E_LOG"; }; \
		trap cleanup EXIT; \
		READY=0; \
		for ATTEMPT in $$(seq 1 40); do \
			if curl --noproxy '*' -fsS http://localhost:8080/readyz >/dev/null 2>&1; then READY=1; break; fi; \
			sleep 0.25; \
		done; \
		if [ "$$READY" -ne 1 ]; then cat "$$E2E_LOG"; exit 1; fi; \
		E2E_TEST=1 E2E_BASE_URL=http://localhost:8080 ADMIN_API_KEY=e2e-admin-secret \
			GLOBAL_RATE_LIMIT=100000 \
			V3_ALLOW_PRIVATE_CALLBACK_URLS=1 \
			DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
			go test -race -count=1 ./tests/e2e

.PHONY: test-v2-regression
test-v2-regression: ## Run share-model financial regression tests
	@echo "Running binary share-model collateral, price-improvement, and settlement regressions."
	go test -tags=v2regression ./internal/order ./internal/settlement

.PHONY: load-test
load-test: ## Run the k6 authenticated read-path load test
	@test -n "$${API_KEY:-}" || (echo "API_KEY is required" && exit 1)
	k6 run tests/load/read_api.js

.PHONY: lint
lint: ## Run linter
	@echo "Running linter..."
	golangci-lint run ./...

.PHONY: fmt
fmt: ## Format code
	@echo "Formatting code..."
	go fmt ./...
	gofmt -s -w .

.PHONY: db-up
db-up: ## Start database containers
	docker compose up -d postgres redis nats

.PHONY: sandbox-db-up
sandbox-db-up: ## Start the isolated V3 sandbox PostgreSQL database
	docker compose --profile sandbox up -d postgres-sandbox

.PHONY: sandbox-db-migrate
sandbox-db-migrate: ## Apply migrations to the isolated V3 sandbox database
	DATABASE_URL="$${SANDBOX_DATABASE_URL:-postgres://predictmarket:password@localhost:55432/predictmarket_sandbox?sslmode=disable}" \
		go run -mod=vendor ./cmd/migrate up

.PHONY: db-migrate
db-migrate: ## Run database migrations
	@echo "Applying versioned Goose migrations..."
	DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
		go run -mod=vendor ./cmd/migrate up

.PHONY: db-migrate-status
db-migrate-status: ## Show Goose migration version status
	DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
		go run -mod=vendor ./cmd/migrate status

.PHONY: db-migrate-down
db-migrate-down: ## Roll back the latest reversible Goose migration
	DATABASE_URL="$${DATABASE_URL:-postgres://predictmarket:password@localhost:5432/predictmarket?sslmode=disable}" \
		go run -mod=vendor ./cmd/migrate down

.PHONY: docker-up
docker-up: ## Start all services with Docker Compose
	docker compose up -d --remove-orphans

.PHONY: docker-down
docker-down: ## Stop all services
	docker compose down

.PHONY: docker-logs
docker-logs: ## Show docker logs
	docker compose logs -f

.PHONY: docker-build
docker-build: ## Build Docker image
	docker build -t predictmarket:latest .

.PHONY: clean
clean: ## Clean build artifacts
	@echo "Cleaning..."
	rm -rf bin/
	rm -f coverage.out coverage.html
	go clean

.PHONY: twill-context
twill-context: ## Show Twill application context
	go run github.com/nxsky/twill/cmd/twill app context .

.PHONY: twill-endpoints
twill-endpoints: ## Show Twill endpoints
	go run github.com/nxsky/twill/cmd/twill app endpoints .

.PHONY: twill-resources
twill-resources: ## Show Twill resources
	go run github.com/nxsky/twill/cmd/twill app resources .

.PHONY: twill-dashboard
twill-dashboard: ## Start Twill dashboard
	go run github.com/nxsky/twill/cmd/twill single dashboard

.PHONY: k8s-deploy-plan
k8s-deploy-plan: ## Generate Kubernetes deployment plan
	go run github.com/nxsky/twill/cmd/twill deploy k8s \
		--image predictmarket:v1.0 \
		--write-dir ./k8s

.PHONY: validate-deployment
validate-deployment: ## Validate OpenAPI and Kubernetes YAML rendering
	python3 scripts/validate_openapi.py
	kubectl kustomize k8s >/dev/null
	@for FILE in k8s/*.yaml; do \
		python3 -c 'import sys, yaml; list(yaml.safe_load_all(open(sys.argv[1], encoding="utf-8")))' "$$FILE"; \
	done
	@echo "Kubernetes YAML: OK"

.PHONY: all
all: fmt lint test build ## Run all checks and build
