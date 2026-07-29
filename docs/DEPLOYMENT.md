# Deployment Guide

## Production topology

The Kubernetes manifests deploy only the PredictMarket API. PostgreSQL,
Redis, and NATS JetStream remain external managed dependencies. Kafka,
Prometheus, and Grafana are not required by this MVP deployment.

Use PostgreSQL 15+, Redis 7+, and a persistent NATS JetStream cluster. Require
TLS and authentication for all three services in production.

## Build and publish

Replace the example image in `k8s/deployment.yaml`, then build and push:

```bash
docker build -t ghcr.io/your-org/predictmarket-saas:0.1.0 .
docker push ghcr.io/your-org/predictmarket-saas:0.1.0
```

The image runs as a non-root user with a read-only root filesystem. Kubernetes
mounts the production Twill configuration from `predictmarket-config` and a
size-limited `emptyDir` at `/tmp` for Twill runtime metadata. Go dependencies
are vendored because the pinned Twill build is private/local, so image builds
do not need its host path or private Git credentials.

## Configure secrets

Do not commit a populated Secret manifest. Create it from your secret manager
or directly from protected environment variables:

```bash
kubectl apply -f k8s/namespace.yaml
kubectl -n predictmarket create secret generic predictmarket-secrets \
  --from-literal=DATABASE_URL="$DATABASE_URL" \
  --from-literal=REDIS_URL="$REDIS_URL" \
  --from-literal=NATS_URL="$NATS_URL" \
  --from-literal=ADMIN_API_KEY="$ADMIN_API_KEY"
```

`k8s/secret.example.yaml` documents the required keys. Use an administrator
key with at least 32 random bytes and rotate it through the secret manager.

## Run database migrations

Migrations use Goose and its `goose_db_version` table. The migration Job uses
the same versioned application image as the API, so update its image tag with
each release, then run it before deploying that release:

```bash
kubectl -n predictmarket delete job predictmarket-migrate --ignore-not-found
kubectl apply -f k8s/migration-job.yaml
kubectl -n predictmarket wait --for=condition=complete \
  job/predictmarket-migrate --timeout=180s
kubectl -n predictmarket logs job/predictmarket-migrate
```

For local status and the latest reversible rollback, use `make
db-migrate-status` and `make db-migrate-down`. Credential-hashing migration
`007` is intentionally irreversible; do not downgrade an application below
its bcrypt-backed credential support.

## Deploy the API

Update the Ingress hostname and TLS secret, then deploy:

```bash
kubectl apply -k k8s
kubectl -n predictmarket rollout status deployment/predictmarket-api
kubectl -n predictmarket get pods,service,ingress
curl -fsS https://api.predictmarket.example.com/readyz
```

The Deployment starts with three replicas. Every scheduled task acquires a
Redis lease, so Polymarket synchronization, reconciliation, and settlement
alerts run once per interval across the replica set. Keep job runtime below its
schedule interval and monitor Redis availability before increasing replicas.

`/healthz` is process liveness; `/readyz` verifies PostgreSQL, Redis, and
NATS and is used for traffic readiness. `/metrics` exposes Prometheus text
metrics. Traces export through OTLP when `OTEL_EXPORTER_OTLP_ENDPOINT` is set.

## Rollback

Application rollback does not reverse schema migrations:

```bash
kubectl -n predictmarket rollout history deployment/predictmarket-api
kubectl -n predictmarket rollout undo deployment/predictmarket-api
kubectl -n predictmarket rollout status deployment/predictmarket-api
```

Use backward-compatible migrations and take a PostgreSQL backup before schema
changes. See `docs/RUNBOOK.md` for dependency and settlement recovery steps.
