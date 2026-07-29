# Twill Roadmap

Twill is the next-generation platform derived from the Twill codebase.
The project repository is `github.com/nxsky/twill`. The Go module path, import
paths, generated code, and CLI name still need a dedicated migration.

## Mission

Build the most advanced enterprise-grade Go microservice platform: Go-first,
Kubernetes-first, infrastructure-aware, observable by default, and native to
AI-assisted software delivery.

The platform should let Go teams develop with a near-monolith experience, run
with cloud-native microservice properties, and use AI agents safely across
design, coding, testing, migration, deployment, and operations.

## Positioning

Twill is not a conventional HTTP/gRPC framework. It combines:

- Twill's component graph, single-image development model, component
  routing, colocation, and deployment runtime.
- go-zero style productivity: code generation, project templates, API/RPC
  scaffolding, and pragmatic defaults.
- Kratos style extensibility: API-first contracts, transport abstraction,
  middleware, registry, config, encoding, and observability integration.
- Encore style application graph and infrastructure-from-code.
- Dapr style distributed building blocks for state, pub/sub, jobs, secrets,
  workflows, and service invocation.
- Backstage style developer portal and service catalog.
- MCP and AI agent workflows as first-class platform interfaces.

## Strategic Constraints and Focus

The positioning above is the long-term vision. It is roughly the combined scope
of Encore, Dapr, Kratos, and Backstage — each of which was built by a funded
team over multiple years. The roadmap must therefore be explicit about
constraints and about the single wedge that justifies the project's existence.

Constraints:

- Upstream Twill was archived on 2025-06-06. This fork carries the
  full maintenance burden alone: Go version compatibility, security patches,
  and dependency updates. There is no upstream to pull from anymore.
- Working assumption: 1-3 core engineers with heavy AI-agent leverage. Every
  P0 list below must be achievable under that assumption or be cut. If the
  actual team is larger, promote P1 items; never the reverse.
- The codebase is Apache-2.0 from Google. Copyright headers and NOTICE
  obligations must be preserved through any rename. "Twill" itself may be
  too close to the upstream brand to keep long-term.

The wedge — the one sentence that differentiates this platform:

> The best Go microservice platform for AI-agent-driven delivery: agents get
> structured context and safe tools, humans get a near-monolith developer
> experience on Kubernetes.

Feature parity with go-zero, Kratos, Encore, or Dapr is explicitly NOT a
near-term goal. Build only the slices that the pilot application and the AI
wedge require. Everything else is P2 or later.

## Pilot Application: Dogfooding First

The strongest asset this project has is a real production system already built
on Twill: the gaming platform backend (`back_server` in the same
workspace) runs on upstream `twill v0.24.6` with multiple services, GORM,
Redis, cron jobs, and EKS deployment.

Every phase below must be validated against this pilot:

- Phase 0: switch `back_server` from the upstream module to this fork and keep
  it green. This proves compatibility and creates the feedback loop.
- Phase 1: the P0 primitive and API scope is defined by what `back_server`
  actually needs, not by framework comparison tables.
- Phase 2: AI tools are validated by using them to develop `back_server`
  features, measured against the success metrics in `AI_NATIVE_PLAN.md`.
- Phase 3+: governance features are prioritized by what operating
  `back_server` in production actually demands.

A platform feature that the pilot does not exercise within one phase of being
built should be questioned.

Current execution note: the pilot migration is temporarily deferred. Near-term
work should continue inside this Twill repository first, without modifying
`back_server`, while preserving the pilot as the later validation target.

## Product Principles

1. Go-first: use Go types, interfaces, context, slog, OpenTelemetry, protobuf,
   and OpenAPI naturally.
2. Explicit distributed semantics: local calls, remote calls, external API
   calls, events, and workflows must be distinguishable in tooling and docs.
3. Kubernetes-first: Kubernetes is the default production substrate, with cloud
   provider integrations layered on top.
4. Progressive adoption: existing net/http, gRPC, Gin, Echo, go-zero, and Kratos
   services must be able to join the platform incrementally.
5. Infrastructure-aware: databases, caches, queues, pub/sub, object storage,
   cron jobs, secrets, and policies should be modeled, validated, and deployed.
6. Observable by default: traces, metrics, logs, profiles, dependency graphs,
   and service-level objectives should exist without bespoke setup.
7. AI-native: the platform must expose structured context and safe tools to AI
   agents instead of relying on generic repository scraping.
8. Open core: keep local development, core runtime, Kubernetes deployment, and
   AI context interfaces open. Enterprise control plane features may be layered
   separately later.

## Priority Levels

- P0: Required for a credible production-oriented MVP.
- P1: Required for broad team adoption.
- P2: Strategic differentiators after the core loop is reliable.

## Phase 0: Stabilize the Fork

Timeline: 2-4 weeks.

Goal: Turn the fork into a sustainable engineering project with clear ownership,
compatibility, and direction.

P0 deliverables:

- Finalize the module path, import paths, generated code strategy, CLI name, and
  private repository settings for `github.com/nxsky/twill`.
- Establish CI for tests, race-sensitive packages, formatting, static analysis,
  vulnerability scanning, and generated-code checks.
- Document the fork status, upstream history, compatibility policy, and support
  window.
- Keep Twill-compatible examples running.
- Migrate the pilot application (`back_server`) from upstream
  `github.com/Twill/twill v0.24.6` to this fork (via a `replace`
  directive first, then the renamed module path) and keep its test suite green.
- Audit all packages under `internal/` and mark candidates for public extension
  APIs.
- Create architecture, roadmap, and AI-native planning documents.
- Verify Apache-2.0 obligations survive the rename: preserve copyright
  headers, carry a NOTICE file, and document provenance from the upstream
  repository.
- Add a minimal release checklist.

P1 deliverables:

- Add an issue and PR template.
- Add dependency update automation.
- Add a small compatibility test suite for generated component code.
- Define the first stable public API boundary.

Exit criteria:

- A new contributor can clone the private repo, run tests, run a hello app, and
  understand the target direction in less than 30 minutes.
- The pilot application builds and passes its tests against this fork.
- The project no longer presents itself as only a Twill maintenance
  branch.

## Phase 1: Platform Core MVP

Timeline: 1-3 months.

Goal: Ship a usable next-generation runtime and developer loop.

Deployer decision (made 2026-06): do NOT fork `twill-kube`. Rewrite
deployment natively inside this repository, behind a public `Deployer`
extension API, replacing the external-binary dispatch mechanism
(`twill <deployer> ...` → `twill-<deployer>` binary) with built-in targets:

```text
twill deploy k8s  [config]   # any Kubernetes cluster via kubeconfig (P0)
twill deploy aws  [config]   # AWS sugar: ECR push, EKS context, IAM/ALB (P1)
twill deploy gke  [config]   # GKE compatibility target (P2, on demand)
```

The verb-first `twill deploy <target>` form replaces the upstream
deployer-first form (`twill gke deploy`, `twill kube deploy`). `twill
deploy k8s` against an EKS kubeconfig must be sufficient to deploy the pilot
application; `twill deploy aws` only adds cloud-specific convenience on top.
The same `Deployer` interface remains open so third parties can still ship
external deployers later.

P0 deliverables:

- Stabilize component registration, code generation, local runtime, multiprocess
  runtime, and Kubernetes deployment paths.
- Add first-class API endpoint declarations with OpenAPI generation.
- Add standard middleware for timeout, retry, rate limit, circuit breaker,
  auth hooks, request IDs, and structured errors.
- Add a unified config system with file, env, Kubernetes ConfigMap, Kubernetes
  Secret, and remote provider interfaces.
- Make Kubernetes deployment first-class: a native `twill deploy k8s` that
  generates and applies Deployment, Service, HPA, Gateway or Ingress,
  ServiceAccount, probes, resource requests, and rollout metadata — no
  separate deployer binary required. Current implementation starts with a
  read-only JSON dry-run plan for Deployment, Service, HPA, Ingress,
  ServiceAccount, probes, resource requests, and rollout metadata; apply support
  and live cluster integration remain future work. The first public extension
  boundary is the dry-run `runtime/deployers.Planner` interface and generic
  deployment `Plan` schema.
- Enable OpenTelemetry traces, metrics, and logs by default (the runtime
  already carries OTel; this means defaults and exporters, not new plumbing).
- Provide a local dashboard with service graph, component graph, API list,
  traces, metrics, logs, and deployment status (extend the existing status
  dashboard rather than rebuilding).

P1 deliverables:

- `twill deploy aws`: image build and ECR push, EKS kubeconfig resolution,
  IAM/IRSA service account annotation, and ALB/Gateway integration on top of
  the native k8s deployer. The pilot application's current EKS deploy script
  (`apply-config.sh` + `kubectl rollout restart`) is the workflow this
  replaces and the acceptance test for it.
- First-class gRPC/protobuf integration for public and internal APIs. (HTTP +
  OpenAPI ships in P0; gRPC follows once the endpoint metadata model is
  proven, since both map to the same model.) Current implementation starts with
  safe `.proto` package/service/RPC/message metadata via `twill app protobuf`;
  full protoc-compatible schema use, generated protobuf clients, and
  grpc.Server mounting remain future work.
- Adapter mode for existing net/http services.
- Adapter mode for existing gRPC services.
- Initial integration guides for go-zero and Kratos projects.
- Minimal PostgreSQL, Redis, Pub/Sub, and Cron primitives, scoped to what the
  pilot application uses (it currently runs MySQL/GORM, Redis, and cron — a
  generic SQL primitive beats a PostgreSQL-only one here).
- Static application graph extraction from source and generated metadata.
  (This is also the Phase 2 `app.graph` MCP resource — build it once.)

P2 deliverables:

- Client SDK generation. Current implementation starts with `twill app client`
  as a read-only dry-run plan for Go and TypeScript HTTP clients from safe
  endpoint metadata; schema-rich clients and protobuf/gRPC clients remain
  future work.
- Contract testing for API endpoints. Current implementation starts with
  `twill app contract-tests` as a read-only dry-run plan for guarded Go HTTP
  contract tests from safe endpoint metadata; schema assertions and
  protobuf/gRPC contract tests remain future work.
- Local Docker Compose runner for dependent infrastructure. Current
  implementation starts with `twill deploy compose` as a read-only dry-run
  plan for local database, cache, pub/sub/queue, and object-storage services
  from safe resource metadata; live Docker startup remains explicit future
  work.

Exit criteria:

- A realistic sample application with APIs, database, cache, pub/sub, and cron
  can run locally and deploy to Kubernetes with one command path.
- The pilot application runs on the Phase 1 runtime in at least one
  non-production environment.

## Phase 2: AI-Native Developer Experience

Timeline: 2-5 months, parallel with Phase 1 where possible.

Goal: Make AI agents effective because the platform gives them structured
context, safe tools, and repeatable workflows.

Sequencing note: Phase 2 is the differentiator, but its value scales with
Phase 1 — `generate_endpoint` needs the endpoint model, `diagnose_deploy_failure`
needs deployment metadata for live backend integration. Start Phase 2 with what
already exists today (component graph, generated metadata, traces, logs, test
results, and caller-supplied operational evidence) so the AI surface ships
early and grows as Phase 1 lands. The application graph export is the shared
foundation for both phases; build it first.

P0 deliverables:

- Add `twill mcp serve` for exposing platform context through MCP.
- Expose read-only MCP resources first: local context pack, application graph,
  components, APIs, config, policy rules, generated code, test results, static
  deployment context, static Kubernetes context, and static observability
  context for logs, traces, and metrics, then live deployment status, logs,
  traces, and metrics as Phase 1 makes them available.
- Expose a small set of MCP tools and grow it tool by tool: start with
  `inspect_app_graph`, `inspect_app_context`, `generate_component`,
  `generate_test`, `generate_endpoint`, `generate_cron_job`,
  `generate_pubsub_worker`, `generate_db_migration`, `review_api_contract`,
  `suggest_retry_policy`, `plan_resource_change`, `plan_migration`,
  `review_security`, `review_performance`, `estimate_cost`, `generate_slo`,
  `generate_observability`, `prepare_pr_summary`, `diagnose_test_failure`,
  `diagnose_deploy_failure`, and `explain_trace`;
  add more tools as their underlying platform features land (see
  `AI_NATIVE_PLAN.md` Phases A-C).
  `generate_component`, `generate_test`, and `generate_endpoint` start as
  dry-run scaffold tools before write-capable edits are enabled;
  `generate_cron_job`,
  `generate_pubsub_worker`, `generate_db_migration`, `review_api_contract`,
  `suggest_retry_policy`, `plan_resource_change`, `plan_migration`,
  `review_security`, `review_performance`, `estimate_cost`, `generate_slo`,
  `generate_observability`, `prepare_pr_summary`, and
  `diagnose_test_failure` start as read-only diagnostic and planning tools.
  `diagnose_deploy_failure` starts as a read-only local rollout evidence
  analyzer before live deployment backends are wired into MCP resources.
  `explain_trace` starts as a read-only local span analyzer before live
  telemetry backends are wired into MCP resources.
- Add a `twill skill init` workflow that creates project-local agent skills.
- Ship the baseline skill pack: component-design, api-design, testing,
  observability, security-review, db-migration, pubsub-worker, performance,
  go-zero migration, and Kratos migration. Skills that depend on future live
  deployment or infrastructure features stay workflow-oriented until those
  backends exist.
- Add safety controls: dry-run mode, write approvals, audit logs, secret
  redaction, and tool permission scopes. MCP tool outputs start with
  structured `audit_event` evidence before durable audit persistence is added,
  and directory-taking MCP tools are restricted to the configured `--dir`
  scope root.

P1 deliverables:

- VS Code and Cursor integration guidance. Current implementation adds
  `twill mcp config` as a dry-run configuration exporter for VS Code and
  Cursor MCP stdio setup, with docs in `docs/ide_integration.md`.
- Pull request bot for architecture diff, risk review, and test suggestions.
  Current implementation exposes `prepare_pr_summary` over MCP and
  `twill mcp pr-summary` as a read-only JSON draft for local CI/artifact
  workflows; comment posting and repository writes remain future work.
- Security and performance review reports. Current implementation exposes
  `review_security` and `review_performance` over MCP plus
  `twill mcp review-security` and `twill mcp review-performance` as read-only
  JSON reports over local context, policy metadata, resources, middleware,
  endpoint contract summaries, and test hints.
- Dry-run generation reports. Current implementation exposes
  `generate_component`, `generate_test`, `generate_endpoint`,
  `generate_cron_job`, `generate_pubsub_worker`, and
  `generate_db_migration` over MCP plus matching `twill mcp generate-*`
  commands as dry-run JSON reports with proposed file contents, SQL drafts,
  review evidence, test plans, operational checklists, safety notes, and audit
  evidence.
- API, retry, and resource planning reports. Current implementation exposes
  `review_api_contract`, `suggest_retry_policy`, and `plan_resource_change`
  over MCP plus `twill mcp review-api`, `twill mcp suggest-retry`, and
  `twill mcp plan-resource-change` as read-only JSON reports over local
  endpoint, middleware, resource, config, and policy evidence.
- Test failure diagnosis reports. Current implementation exposes
  `diagnose_test_failure` over MCP plus `twill mcp diagnose-test` as a scoped
  read-only `go test -json` wrapper with static test context and redacted
  output excerpts for local and CI artifacts.
- SRE diagnostic agent for traces, logs, metrics, and deployment failures.
  Current implementation exposes `diagnose_deploy_failure` and `explain_trace`
  over MCP plus `twill mcp diagnose-deploy` and `twill mcp explain-trace` as
  read-only JSON report commands for local evidence files; live backend queries
  remain future work.
- Migration agent for go-zero, Kratos, and conventional net/http services.
  Current implementation exposes `plan_migration` over MCP plus
  `twill mcp plan-migration` as a read-only JSON report for adapter-first
  migration inventory and staged planning.

P2 deliverables:

- AI-assisted cost recommendations. Current implementation exposes
  `estimate_cost` over MCP plus `twill mcp estimate-cost` as a read-only JSON
  report over local context, policy rules, endpoint contract summaries,
  resource evidence, and caller traffic assumptions.
- AI-assisted SLO and alert generation. Current implementation exposes
  `generate_slo` over MCP plus `twill mcp generate-slo` as a dry-run JSON SLO
  draft over local endpoint, resource, and test metadata. It also exposes
  `generate_observability` over MCP plus `twill mcp generate-observability` as
  a dry-run JSON observability documentation draft; alert/dashboard creation
  remains future work.
- Team-specific skill packs.

Exit criteria:

- An agent can generate a platform-native endpoint, component, and test from
  existing project context.
- An agent can explain a failed deployment or slow trace using platform data
  without direct database or secret access.
- The AI tools have been used to deliver at least one real feature in the
  pilot application, with the time savings measured.

## Phase 3: Enterprise Runtime

Timeline: 4-9 months.

Goal: Support production teams with governance, reliability, and operational
control.

Scoping rule: Phase 3 as written is a multi-year product for a funded team.
Until there are external production adopters beyond the pilot, treat this
phase as "operate the pilot in production credibly" — the P0 list below is
the floor for that, and several items (multi-tenancy, full control plane)
should not start without adoption evidence.

P0 deliverables:

- A minimal control plane for applications, environments, deployments,
  versions, components, instances, routes, and config — local or in-cluster,
  single-tenant, backed by the application graph store.
- Environment model: local, dev, staging, production, and preview.
- Rollout strategies: start with safe rollout + automatic rollback on health
  regression; blue-green, canary, and traffic split build on that.
- Service identity, mTLS, secret scoping, and audit logs. (Full RBAC/OIDC can
  trail until there is a multi-user control plane to protect.)
- Policy gates for deployment, resource limits, public APIs, secret usage, and
  risky migrations.
- Database migration workflow with validation, rollback plan, and environment
  promotion.
- SLO model tied to deployment health and rollback decisions.

P1 deliverables:

- RBAC and OIDC for the control plane.
- Multi-tenant isolation.
- Cost attribution by app, component, environment, and team.
- Resource recommendation engine.
- Approval workflow for production changes.
- Compliance and evidence export.

P2 deliverables:

- Multi-cluster placement.
- Cross-region routing.
- Chaos and resilience testing.

Exit criteria:

- A 5-20 person Go team can operate real production services with deployment,
  observability, rollback, audit, and access controls.

## Phase 4: Infrastructure-from-Code Platform

Timeline: 9-18 months.

Goal: Approach the full Encore-style experience while keeping an open,
Kubernetes-first foundation.

Gate: do not start Phase 4 P0 work until Phase 3's exit criteria are met and
there is at least one production team besides the pilot. Phase 4 and 5 are
direction, not commitments — revisit their scope at each gate.

P0 deliverables:

- Generate a complete application graph from code, config, and generated
  metadata.
- Generate an infrastructure plan from the graph.
- Local provisioning for PostgreSQL, Redis, Pub/Sub, object storage, cron, and
  secrets.
- Kubernetes provisioning through Helm, Operator, or Terraform.
- Preview environments for pull requests.
- Web console with service catalog, API docs, architecture graph, deployment
  status, logs, traces, metrics, costs, and permissions.
- Terraform or Crossplane provider for cloud resources.

P1 deliverables:

- Backstage plugin.
- GitHub and GitLab integration.
- Enterprise template catalog.
- Plugin marketplace.
- AWS, GCP, and Azure adapters.

P2 deliverables:

- Managed cloud control plane.
- Cross-cloud resource policy engine.
- Enterprise marketplace for internal skills and templates.

Exit criteria:

- A developer can add a service with a database and message queue by writing
  code and minimal declarations, then get local runtime, preview environment,
  production deployment, docs, and observability automatically.

## Phase 5: Ecosystem and Commercial Readiness

Timeline: 18-24 months.

Goal: Make the platform trustworthy for external adoption and enterprise use.

P0 deliverables:

- Stable public extension API.
- Compatibility policy and release train.
- Security response process.
- Enterprise console packaging.
- Reference applications for SaaS, ecommerce, AI agent backend, IoT, and
  financial ledger use cases.
- English and Chinese documentation.

P1 deliverables:

- Hosted control plane.
- Certification program.
- Migration services.
- Partner integrations.

Exit criteria:

- The project has a clear open-source core, a credible enterprise path, and a
  repeatable adoption story for existing Go teams.

## First-Year Objective

Within one year, Twill should let a 5-20 person Go team build, test,
deploy, observe, and operate a real Kubernetes microservice system. AI agents
should be able to handle 30-50% of routine scaffolding, testing, migration, and
diagnostic work under explicit safety controls.

Concretely, by month 12: Phase 0 and Phase 1 complete, Phase 2 P0 complete,
the pilot application in production on the platform, and Phase 3 started.

## Top Risks

1. Scope. The vision spans four mature products. Mitigation: the pilot
   application defines P0 scope; phase gates block speculative work.
2. Solo-maintainer burnout on fork upkeep. Every Go release and CVE is now
   this project's job. Mitigation: strong CI, dependency automation, and
   keeping the diff from upstream small until the rename.
3. Building AI tools before the platform features they wrap. Mitigation: the
   Phase 2 sequencing note — read-only context first, tools follow features.
4. Twill's original adoption failure (rewrite-required) repeating.
   Mitigation: adapter modes are P1 in Phase 1, and the pilot migration in
   Phase 0 proves the compatible path before anything else.
5. Rename/legal friction. Apache-2.0 obligations, pkg.go.dev module identity,
   and brand distance from "Twill". Mitigation: Phase 0 rename
   checklist in `docs/rename_and_private_repo.md`, executed once, early.

## Immediate Next Steps

1. Continue Twill core work in this repository first; keep the `back_server`
   migration deferred until the execution plan explicitly resumes it.
2. Add `https://github.com/nxsky/twill` as the private remote and mirror the
   current branch there before code-level rename work.
3. Create CI: `go test ./...`, race tests, `go vet`, staticcheck, govulncheck,
   and a generated-code drift check.
4. Convert the current fork identity from maintenance branch to Twill.
5. Design and ship the application graph export — it is the foundation for
   Phase 1 metadata, the dashboard, and every Phase 2 MCP resource.
6. Build the Phase 1 platform core MVP plan as tracked issues. Until GitHub
   issue tracking is active, use `docs/phase1_mvp_backlog.md` as the
   issue-sized backlog and copy items into the roadmap issue template.
7. Start `twill mcp serve` design and project-local skill templates.
