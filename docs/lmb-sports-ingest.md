# LMB sports ingest worker

`sports-ingest` is a separate, browser-free process that reads the official
Liga Mexicana de Béisbol (LMB) calendar and upserts candidate sports events into
the same PostgreSQL database used by PredictMarket. It is packaged in the
normal application image, but runs independently from the public API.

## Scope

- Uses `https://lmb.com.mx/juegos/api/calendar` through direct HTTP with a
  bounded request timeout.
- Stores LMB games under `source_type=lmb` and `source_id=<gameId>`, so they do
  not collide with existing Polymarket events.
- Creates or updates candidate events and sports metadata only.
- Changes a final LMB fixture to `closed`; it does not resolve an outcome,
  settle a market, void an event, or issue refunds.
- Does not ingest LMP. LMP remains outside this worker because it currently
  presents a Cloudflare challenge.

## Timezones

All stored timestamps are UTC instants. Prediction-market business and display
time for LMB is `America/Mexico_City`. The LMB calendar endpoint has a separate
verified query-day contract, defaulting to `Asia/Shanghai`; do not change that
value merely to change event display time.

## Configuration

The worker requires `DATABASE_URL`, supplied from the existing
`predictmarket-secrets` Kubernetes Secret. The non-secret settings are in
`predictmarket-config` and `.env.example`:

| Variable | Default | Purpose |
| --- | --- | --- |
| `LMB_BASE_URL` | `https://lmb.com.mx` | Official LMB site base URL. |
| `LMB_CALENDAR_TIMEZONE` | `Asia/Shanghai` | Day used to build LMB calendar query bounds. |
| `LMB_MARKET_TIMEZONE` | `America/Mexico_City` | LMB market/business display convention. |
| `LMB_REQUEST_TIMEOUT` | `15s` | Per-request HTTP timeout. |
| `SPORTS_INGEST_POLL_INTERVAL` | `15m` | Delay between normal synchronization runs. |
| `SPORTS_INGEST_LOOKAHEAD_DAYS` | `7` | Inclusive number of future calendar days to query (0–30). |
| `SPORTS_INGEST_RUN_ONCE` | `false` | Exit after the initial synchronization when `true`. |

The worker synchronizes once immediately on startup, then repeats at the poll
interval. Invalid URLs, durations, booleans, or IANA timezones make it exit
before fetching data.

## Local operation

Build or run the worker with the vendored Go dependencies:

```bash
make build-sports-ingest
make run-sports-ingest
```

For a one-time backfill, first build the binary and then run:

```bash
SPORTS_INGEST_RUN_ONCE=true ./bin/sports-ingest
```

Set `DATABASE_URL` to a non-production database for local runs. The command
uses the configured LMB endpoint, so a one-time run requires outbound access
to `lmb.com.mx`.

## Kubernetes deployment

`k8s/sports-ingest-deployment.yaml` creates exactly one worker replica and has
no Service, Ingress, or exposed container port. It uses the same image,
`predictmarket-config`, and `predictmarket-secrets` as the API; `DATABASE_URL`
is explicitly read from the Secret. The pod disables service-account token
mounting, runs non-root with the RuntimeDefault seccomp profile, drops all Linux
capabilities, disallows privilege escalation, and uses a read-only root
filesystem.

Before applying the manifest, make sure `predictmarket-secrets` contains a
`DATABASE_URL` key for the intended database. Apply the normal Kustomize bundle;
the worker is included in `k8s/kustomization.yaml`. Inspect its logs with:

```bash
kubectl -n predictmarket logs deployment/predictmarket-sports-ingest -f
```

The `dev` branch deployment workflow uses `k8s/overlays/dev`, which includes
the same single worker (with the immutable API image substituted for
`IMAGE_URI`) and waits for its rollout. It also prints the worker logs when a
development deployment fails.

Validate the worker's deployment shape with:

```bash
make validate-deployment
```
