# PredictMarket SaaS

A multi-tenant SaaS prediction market platform built with Twill framework, providing Polymarket-compatible API services for merchants.

## Features

- 🏢 **Multi-Tenant Architecture**: Each merchant has isolated data and API access
- 🎯 **Event Aggregation**: Sync events from Polymarket and other sources
- 💰 **Virtual Wallet**: Play money system without real currency integration
- 🌍 **Global Ready**: Multi-currency display and timezone support
- 📊 **Real-time Markets**: Binary prediction markets (exactly two options)
- ⚡ **High Performance**: Built on Twill microservice framework
- 🚀 **Kubernetes Native**: Easy deployment to K8s clusters

## Architecture

The system is built using Twill's component model with core domain services and background workers:

- **Merchant Service**: Tenant management and API authentication
- **Event Service**: Event aggregation from Polymarket
- **Market Service**: Prediction market creation and management
- **Order Service**: Order matching and execution
- **Wallet Service**: Virtual credit management
- **Settlement Service**: Atomic, idempotent payout processing
- **Settlement Worker**: PostgreSQL outbox and NATS JetStream delivery
- **Currency Service**: Exchange rate and multi-currency support
- **Sports Service**: Sports event integration
- **Analytics Service**: Reporting and statistics

## Quick Start

### Prerequisites

- Go 1.26+
- PostgreSQL 15+
- Redis 7+
- Twill CLI

### Installation

```bash
# Clone the repository
git clone https://github.com/afun-game/predictmarket-saas.git
cd predictmarket-saas

# Install dependencies
go mod download

# Generate Twill code
go run /mnt/c/works/solgame/twill/cmd/twill generate .

# Run versioned database migrations
make db-migrate

# Run locally
go run ./cmd/api
```

### Configuration

Create a `.env` file:

```env
DATABASE_URL=postgres://user:pass@localhost:5432/predictmarket?sslmode=disable
REDIS_URL=redis://localhost:6379/0
NATS_URL=nats://localhost:4222
POLYMARKET_API_URL=https://gamma-api.polymarket.com
PORT=8080
```

## API Documentation

### Authentication

All API requests require authentication using API Key:

```bash
curl -H "Authorization: Bearer YOUR_API_KEY" \
     https://api.example.com/api/v1/events
```

### Core Endpoints

#### Merchants

- `POST /api/v1/merchants/register` - Register new merchant
- `GET /api/v1/merchants/{id}/config` - Get merchant configuration
- `PATCH /api/v1/merchants/{id}/config` - Update merchant configuration
- `GET /api/v1/merchants` - List merchants (admin only)

#### Events

- `GET /api/v1/events` - List events
- `GET /api/v1/events/{id}` - Get event details
- `POST /api/v1/events` - Create a custom event (admin only)
- `PATCH /api/v1/events/{id}/status` - Advance event status (admin only)
- `POST /api/v1/events/{id}/resolve` - Resolve a closed event (admin only)

#### Markets

- `POST /api/v1/markets` - Create market (admin only)
- `GET /api/v1/markets` - List markets
- `GET /api/v1/markets/{id}` - Get market details
- `GET /api/v1/markets/{id}/orderbook` - Get order book

#### Orders

- `POST /api/v1/orders` - Create order
- `GET /api/v1/orders/{id}` - Get order status
- `DELETE /api/v1/orders/{id}` - Cancel order

#### Wallets

- `GET /api/v1/wallets/{user_id}` - Get wallet balance
- `POST /api/v1/wallets/{user_id}/credit` - Add credits

See [API Documentation](docs/API.md) for full API reference.

## Development

### Generate Twill Code

```bash
go run /mnt/c/works/solgame/twill/cmd/twill generate .
```

### Run Tests

```bash
go test -race ./...
make test-integration
make test-e2e
```

See [docs/TESTING.md](docs/TESTING.md) for the full HTTP settlement flow and
k6 load-test instructions.

The machine-readable API contract is [openapi.yaml](openapi.yaml). Production
deployment and operations are covered by [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)
and [docs/RUNBOOK.md](docs/RUNBOOK.md).

### Local Dashboard

```bash
go run /mnt/c/works/solgame/twill/cmd/twill single dashboard
# Open http://localhost:9000/app
```

### View Application Context

```bash
go run /mnt/c/works/solgame/twill/cmd/twill app context .
go run /mnt/c/works/solgame/twill/cmd/twill app endpoints .
go run /mnt/c/works/solgame/twill/cmd/twill app resources .
```

## Deployment

### Docker Compose (Local)

```bash
go run /mnt/c/works/solgame/twill/cmd/twill deploy compose . --write-dir .
docker-compose -f docker-compose.twill.yaml up
```

### Kubernetes

```bash
# Build image
docker build -t predictmarket:v1.0 .

# Deploy to Kubernetes
go run /mnt/c/works/solgame/twill/cmd/twill deploy k8s \
  --image predictmarket:v1.0 \
  --write-dir ./k8s
  
kubectl apply -f k8s/reviewed-manifests.yaml
```

### AWS EKS

```bash
go run /mnt/c/works/solgame/twill/cmd/twill deploy aws \
  --region us-east-1 \
  --account 123456789012 \
  --repository predictmarket/api \
  --write-dir ./aws
```

## Project Structure

```
predictmarket-saas/
├── cmd/
│   └── api/              # Main application entry
├── internal/
│   ├── merchant/         # Merchant management
│   ├── event/            # Event service
│   ├── market/           # Market service
│   ├── order/            # Order service
│   ├── wallet/           # Wallet service
│   ├── currency/         # Currency service
│   ├── sports/           # Sports service
│   └── analytics/        # Analytics service
├── pkg/
│   ├── polymarket/       # Polymarket API client
│   └── types/            # Shared types
├── migrations/           # Database migrations
├── docs/                 # Documentation
│   ├── endpoints/        # API endpoints
│   ├── resources/        # Resource declarations
│   └── config/           # Configuration docs
├── go.mod
└── twill.toml
```

## Configuration

### Twill Resources

Resources are declared in `docs/resources/`:

- `database.md` - PostgreSQL configuration
- `cache.md` - Redis configuration
- `events.md` - Pub/Sub configuration

### Background Jobs

- `PolymarketSync`: Syncs events every 5 minutes
- `ExchangeRateUpdate`: Updates rates hourly
- `MarketSettlement`: Checks settlement every minute

## Monitoring

The application exposes metrics and traces via OpenTelemetry:

- Metrics: Prometheus format on `/metrics`
- Traces: OTLP export to configured backend
- Logs: Structured JSON logs

## Contributing

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

MIT License - see LICENSE file for details

## Support

- Documentation: [docs/](docs/)
- Issues: [GitHub Issues](https://github.com/afun-game/predictmarket-saas/issues)
- Twill Framework: [https://github.com/nxsky/twill](https://github.com/nxsky/twill)
