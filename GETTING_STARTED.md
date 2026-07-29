# Getting Started Guide

## Prerequisites

Before you begin, ensure you have the following installed:

- **Go 1.26+**: [Download](https://golang.org/dl/)
- **Docker & Docker Compose**: [Download](https://www.docker.com/get-started)
- **PostgreSQL 15+**: (or use Docker)
- **Redis 7+**: (or use Docker)
- **Make**: For running Makefile commands

## Step 1: Clone and Setup

```bash
# Clone the repository
git clone <repository-url>
cd predictmarket-saas

# Copy environment file
cp .env.example .env

# Edit .env with your configuration
nano .env

# Download dependencies
go mod download
```

## Step 2: Start Dependencies

Using Docker Compose (recommended):

```bash
# Start PostgreSQL and Redis
make docker-up

# Or start only database services
make db-up
```

Or install them locally and configure `.env` accordingly.

## Step 3: Run Database Migrations

```bash
# Run migrations
make db-migrate

# Inspect applied versions
make db-migrate-status
```

## Step 4: Generate Twill Code

```bash
# Generate Twill boilerplate
make generate

# This runs: twill generate .
```

## Step 5: Run the Application

```bash
# Run locally
make run

# Or directly:
go run ./cmd/api
```

The API will be available at `http://localhost:8080`

## Step 6: Test the API

### Register a Merchant

```bash
curl -X POST http://localhost:8080/api/v1/merchants/register \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Test Merchant",
    "email": "test@example.com",
    "currency": "USD",
    "timezone": "America/New_York"
  }'
```

Response:
```json
{
  "merchant_id": "uuid",
  "api_key": "pk_live_xxx",
  "api_secret": "sk_live_xxx"
}
```

### List Events

```bash
curl http://localhost:8080/api/v1/events \
  -H "Authorization: Bearer YOUR_API_KEY"
```

### Create a Market (Admin Only)

```bash
curl -X POST http://localhost:8080/api/v1/markets \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "event_id": "event-uuid",
    "type": "binary",
    "question": "Will it happen?",
    "options": ["Yes", "No"],
    "liquidity_pool": 10000.00
  }'
```

## Step 7: View Twill Dashboard

```bash
# Start Twill dashboard
make twill-dashboard

# Open browser to http://localhost:9000/app
```

The dashboard shows:
- Service graph
- API endpoints
- Resource configuration
- Component dependencies

## Development Workflow

### Running Tests

```bash
# Run all tests
make test

# Run with coverage
make test-coverage

# Start PostgreSQL/Redis/NATS, migrate, and run integration tests
make test-integration

# View coverage report
open coverage.html
```

### Code Formatting

```bash
# Format code
make fmt

# Run linter
make lint
```

### Viewing Application Context

```bash
# Show all endpoints
make twill-endpoints

# Show resources
make twill-resources

# Show full context
make twill-context
```

### Hot Reload Development

Use `air` for hot reload:

```bash
# Install air
go install github.com/cosmtrek/air@latest

# Run with hot reload
air
```

## Docker Development

### Build Docker Image

```bash
make docker-build
```

### Run in Docker

```bash
# Start all services
docker-compose up

# View logs
make docker-logs

# Stop all services
make docker-down
```

## Kubernetes Deployment

### Generate Kubernetes Manifests

```bash
make k8s-deploy-plan

# Manifests will be in ./k8s/reviewed-manifests.yaml
```

### Deploy to Kubernetes

```bash
# Apply manifests
kubectl apply -f k8s/reviewed-manifests.yaml

# Check status
kubectl get pods
kubectl get services
```

### Configure Ingress

Edit `k8s/reviewed-manifests.yaml` and set your domain:

```yaml
spec:
  rules:
  - host: api.yourdomain.com
```

## Monitoring

### Metrics and traces

Metrics are exposed in Prometheus text format at
`http://localhost:8080/metrics`. The local dependency stack intentionally does
not run Prometheus or Grafana; configure an external scraper and optional
dashboard in the deployment environment. OTLP traces export when
`OTEL_EXPORTER_OTLP_ENDPOINT` is set.

### Logs

Structured JSON logs are written to stdout.

View logs in development:
```bash
# Application logs
tail -f logs/app.log

# Docker logs
docker-compose logs -f api
```

## Troubleshooting

### Database Connection Error

```bash
# Check if PostgreSQL is running
docker-compose ps postgres

# Check connection
psql postgres://predictmarket:password@localhost:5432/predictmarket -c "SELECT 1"
```

### Redis Connection Error

```bash
# Check if Redis is running
docker-compose ps redis

# Test connection
redis-cli -h localhost -p 6379 ping
```

### Port Already in Use

Change ports in `.env`:
```env
APP_PORT=8081
```

### Twill Generate Fails

Ensure you have the correct Twill installation:
```bash
cd /mnt/c/works/solgame/twill
go install ./cmd/twill
```

## Next Steps

1. Read the [API Documentation](docs/api.md)
2. Review the [MVP Implementation Plan](MVP_PLAN.md)
3. Check the [Architecture Documentation](../predictmarket-saas-requirements.md)
4. Start implementing components following the plan

## Useful Commands

```bash
# Full development cycle
make all

# Clean and rebuild
make clean && make build

# Database reset (CAUTION: drops all data)
docker-compose down -v
docker-compose up -d postgres
make db-migrate
```

## Getting Help

- Read the documentation in `/docs`
- Check Twill framework docs: https://github.com/nxsky/twill
- Review example Twill projects
- Open an issue if you find bugs
