# AI-Native Development Plan

This document defines how Twill will support AI-assisted development,
migration, deployment, and operations.

## Goal

Make AI agents reliable contributors to Go microservice development by giving
them structured platform context, safe tools, repeatable skills, and explicit
permission boundaries.

The platform should help agents answer questions and perform actions such as:

- What services, components, APIs, and resources exist?
- What will change if this endpoint is added?
- Which tests should be updated?
- Why did this deployment fail?
- Why is this trace slow?
- Is this retry policy safe?
- How can an existing go-zero or Kratos service migrate incrementally?

## Non-Goals

- Do not require a specific AI vendor.
- Do not send secrets to AI models.
- Do not let agents mutate production state without explicit approval.
- Do not rely only on natural-language prompts for correctness.
- Do not replace code review, security review, or release approval.

## AI Integration Surfaces

### MCP Server

The CLI should provide:

```text
twill mcp serve
twill mcp audit-events
twill mcp config
twill mcp diagnose-deploy
twill mcp diagnose-test
twill mcp estimate-cost
twill mcp explain-trace
twill mcp generate-component
twill mcp generate-cron-job
twill mcp generate-db-migration
twill mcp generate-endpoint
twill mcp generate-observability
twill mcp generate-pubsub-worker
twill mcp generate-slo
twill mcp generate-test
twill mcp plan-migration
twill mcp plan-resource-change
twill mcp pr-summary
twill mcp review-api
twill mcp review-performance
twill mcp review-security
twill mcp suggest-retry
```

The MCP server exposes resources, tools, and prompts for local IDEs, coding
agents, CI bots, and SRE agents.

`twill mcp config` prints dry-run MCP stdio configuration snippets for VS Code
and Cursor so local IDE agents can connect to `twill mcp serve` without
hand-written JSON.

`twill mcp audit-events` prints the same read-only audit context exposed by
the `audit.events` MCP resource so CI jobs can validate persisted MCP audit
JSONL files without starting an MCP client session.

`twill mcp pr-summary` prints the read-only PR summary structure used by the
`prepare_pr_summary` MCP tool so CI bots can create review artifacts without
repository write permissions.

`twill mcp diagnose-deploy` and `twill mcp explain-trace` print read-only SRE
diagnostic reports from local evidence files so incident workflows can reuse
the same structured MCP tool outputs outside an IDE session.

`twill mcp diagnose-test` prints the read-only test diagnosis structure used by
the `diagnose_test_failure` MCP tool so CI and local runs can publish failed
test context and safe runtime metric signal names as JSON without source
writes, live metric queries, or secret-bearing raw output.

`twill mcp plan-migration` prints the read-only migration inventory and staged
plan used by the `plan_migration` MCP tool so migration work can begin from a
local or CI artifact with safe runtime metric signal names before source edits.

`twill mcp review-api`, `twill mcp suggest-retry`, and
`twill mcp plan-resource-change` print the read-only planning structures used
by the `review_api_contract`, `suggest_retry_policy`, and
`plan_resource_change` MCP tools so API compatibility, retry behavior, and
resource changes can be reviewed as local or CI JSON artifacts before source or
environment changes.

`twill mcp generate-component`, `twill mcp generate-test`,
`twill mcp generate-endpoint`, `twill mcp generate-cron-job`,
`twill mcp generate-pubsub-worker`, and `twill mcp generate-db-migration` print
the dry-run generation structures used by the matching MCP tools so agents and
CI jobs can review proposed code, tests, contracts, SQL drafts, runbooks, and
operational checklists before any file writes or environment changes.

`twill mcp estimate-cost`, `twill mcp generate-slo`, and
`twill mcp generate-observability` print the read-only operations planning
structures used by the `estimate_cost`, `generate_slo`, and
`generate_observability` MCP tools so cost, reliability, and observability
drafts can be reviewed with safe runtime metric signal names as local or CI
JSON artifacts without billing, alerting, live metric queries, or environment
write permissions.

`twill mcp review-security` and `twill mcp review-performance` print the
read-only review structures used by the `review_security` and
`review_performance` MCP tools so security and performance findings can be
published as local or CI JSON artifacts without secret access, load tests, or
environment write permissions.

Implementation note: build on the official MCP Go SDK
(`github.com/modelcontextprotocol/go-sdk`) rather than hand-rolling the
protocol. Support stdio transport first (covers Claude Code, Cursor, and most
IDE agents); HTTP transport can follow for CI bots and remote agents.

### Project Skills

The CLI should provide:

```text
twill skill init
```

This creates project-local skills under a directory such as `.twill/skills/`.
Skills encode platform conventions, local architecture rules, code generation
patterns, testing expectations, and migration workflows.

Format note: do not invent a new skill format. Emit skills in the formats
agents already read — Claude Code skills (`.claude/skills/` with SKILL.md
frontmatter) and an `AGENTS.md` entry point — generated from one canonical
source under `.twill/skills/`. Canonical skills also include
`agents/openai.yaml` UI metadata for OpenAI/Codex skill surfaces and a
`.twill/skills/team-pack.json` manifest so teams and CI jobs can discover the
whole pack deterministically. A skill nobody's agent loads is documentation.

### Agent Tooling

Agent tools should be narrow, typed, auditable, and dry-run friendly. Tools
should return structured diffs and diagnostics instead of free-form output where
possible.

Initial MCP prompts are available for component design, API design, resource
changes, migration planning, DB migrations, cron jobs, pub/sub workers,
performance review, cost estimates, SLO design, observability, PR summaries,
test diagnosis, deployment diagnosis, trace explanation, and security review.
They steer agents toward the read-only context resources and dry-run tools
before proposing source changes.

### Dashboard AI Context

The dashboard should expose AI-readable context packs for a selected app,
deployment, trace, component, endpoint, or incident.

Current implementation extends the existing status dashboard with `/app` and
`/app/data`, a safe local app context view backed by the same
`internal/tool/app.InspectContextPack` provider used by `twill app` and MCP.
It renders service graph, component graph, API, resource, observability,
protobuf, client SDK, contract-test, local Compose, deployment, and test
summaries, including gRPC client SDK RPC operations and RPC contract-test cases,
without exposing raw source, config values, logs, trace payloads, metric values,
request bodies, protobuf fields, or secret-bearing details.
Live telemetry-backed dashboard panels remain future work.

## MCP Resources

Initial resources:

- `app.context`: aggregate local AI context pack with graph, components,
  endpoint-adjacent metadata, standard middleware evidence, minimal OpenAPI
  export, config-safe context, generated files, tests, safety notes, and
  verification commands.
- `app.graph`: components, services, endpoints, resources, dependencies.
- `app.components`: component interfaces, implementations, references, routes.
  Current implementation reports components, dependency edges, and conservative
  `twill.WithRouter` bindings from source files, including the router type name
  and source file only; routing methods, keys, and business logic are not
  exposed.
- `app.endpoints`: HTTP/gRPC endpoints, schemas, auth, middleware, SLOs.
  Current implementation combines Twill listener metadata with safe summaries
  from generated `docs/endpoints/*.md` endpoint contracts when present and safe
  adapter declarations from generated metadata, plus conservative source-level
  `net/http` method-aware route declarations. It exposes only component,
  listener, protocol when known, method, path, gRPC service when known, and
  source file, plus declared request/response type references when present.
  For gRPC adapters with matching local `.proto` files, protobuf RPC
  `RequestType`/`ResponseType` references are associated by service/method and
  included in endpoint metadata; request/response bodies, fields, examples,
  handler names, and free-form contract text are not exposed.
- `app.openapi`: minimal deterministic OpenAPI document generated from safe
  HTTP endpoint metadata. Current implementation is also available as
  `twill app openapi`; it exports HTTP method, path, component, listener, and
  source file metadata only, and filters gRPC adapter declarations instead of
  treating RPC paths as HTTP operations.
- `app.protobuf`: safe protobuf contract metadata. Current implementation is
  also available as `twill app protobuf`; it scans local `.proto` files for
  package, service, RPC, message type, and source-file metadata without
  exposing message fields, enum values, options, comments, examples, payloads,
  or custom annotations. Matching local protobuf RPCs enrich gRPC adapter
  endpoint declarations by service/method with `RequestType` and
  `ResponseType` metadata.
- `app.clients`: dry-run client SDK generation context from safe endpoint
  metadata. Current implementation is also available as `twill app client`;
  it proposes Go or TypeScript HTTP client files and gRPC adapter stub metadata
  with declared or protobuf-inferred request/response type references when
  present, without writing them, and does not read raw request examples,
  response bodies, credentials, protobuf payloads, or headers.
- `app.contract_tests`: dry-run endpoint contract-test context from safe
  endpoint metadata. Current implementation is also available as
  `twill app contract-tests`; it proposes guarded Go HTTP contract tests
  plus guarded gRPC adapter contract-test stubs with declared or
  protobuf-inferred request/response type references when present, without
  writing files, and does not read raw examples, response bodies, credentials,
  headers, protobuf payloads, or free-form contract text.
- `app.local_compose`: dry-run Docker Compose context for local dependent
  infrastructure inferred from safe resource metadata. Current implementation
  is also available as `twill app compose` and backs `twill deploy compose`;
  it proposes local services for database, cache, pub/sub/queue, and object
  storage resources without running Docker, writing files, or exposing
  connection strings or secret values.
- `app.middleware`: timeout, retry, rate limit, circuit breaker, auth hook,
  request ID, and structured error middleware evidence. Current implementation
  reports safe references to the standard `runtime/middleware` package by
  name, category, inferred component, and source file only; handler logic,
  header values, auth rules, request bodies, response bodies, and error details
  are not exposed.
- `app.resources`: databases, caches, topics, queues, cron jobs, secrets.
  Current implementation exposes Twill listener surfaces, optional
  `docs/resources/*.md` declarations, and conservative source-level backing
  resource type hints for known database, cache, topic, queue, object-storage,
  pub/sub, and cron client types. Declarations expose only kind, component,
  type, lifecycle, binding kind, provider marker, required marker, and source
  file; field names, config keys, provider values, secret names, secret values,
  connection strings, schedules, and free-form notes are not exposed.
- `app.config`: config schemas and environment-specific bindings.
  Current implementation reports components and conservative
  `twill.WithConfig` bindings from source files, including the config type name
  and source file only; field names, TOML keys, secret names, and config values
  are not exposed.
- `app.generated`: generated files and generator metadata.
- `app.tests`: test packages, coverage summaries, failing tests.
  Current implementation exposes static package-local and external test files,
  parsed test/fuzz/benchmark function names, test-name and filename strategy
  hints, existing local Go coverage profile summaries, and package-level
  component test hints with explicit limitations; runtime failures are not
  computed yet and coverage profiles may be stale.
- `deploy.status`: deployments, versions, rollout state, health.
  Current implementation exposes a read-only static deployment-status context
  with component names and explicit limitations; live deployment backends are
  not queried yet.
- `deploy.kubernetes`: generated or observed Kubernetes resources.
  Current implementation exposes a read-only static Kubernetes context with
  component names and explicit limitations; kubeconfig, clusters, manifests,
  pods, events, ConfigMaps, and secrets are not queried yet.
- `deploy.aws`: generated or observed AWS EKS resources.
  Current implementation exposes read-only static ECR, EKS cluster context,
  IAM/IRSA, and ALB/Gateway dry-run metadata with explicit limitations; AWS
  APIs, AWS config, kubeconfig, manifests, IAM policies, subnets, security
  groups, DNS, certificates, and live rollout data are not queried.
- `deploy.compose`: generated or observed local Docker Compose resources.
  Current implementation exposes read-only Docker Compose dry-run metadata for
  local dependent infrastructure; Docker commands, file writes, volume
  creation, container startup, connection strings, and secret values are not
  performed or exposed.
- `obs.traces`: trace summaries and selected spans.
  Current implementation exposes a read-only trace context with component
  names, explicit limitations, and safe `runtime/observability` trace-default
  references when present; live trace backends and raw spans are not queried.
- `obs.logs`: filtered logs with secret redaction.
  Current implementation exposes a read-only log context with component names,
  redaction guidance, explicit limitations, and safe redacting-log default
  references when present; live log backends and raw log entries are not
  queried.
- `obs.metrics`: metric summaries and SLO signals.
  Current implementation exposes a read-only metric context with component
  names, explicit limitations, safe runtime metric signal names when
  `SnapshotMetrics` evidence is present, and runtime metric-snapshot references;
  live metric backends and metric values are not queried.
- `policy.rules`: applicable security and deployment policies.
  Current implementation exposes baseline read-only AI/tool safety rules and
  optional project rules from `.twill/policy/rules.json`, with explicit
  enforcement limitations.
- `audit.events`: persisted MCP tool and resource-read audit events.
  Current implementation reads the optional `--audit-log` JSONL file, exposing
  structured audit evidence only. Resource-read audit entries include
  verification commands scoped to the MCP server package patterns. Full tool
  outputs, proposed file contents, centralized retention, and remote audit
  backends are not included.

## MCP Tools

P0 tools:

- `inspect_app_graph` / `inspect_app_context`: return the current application
  graph or local AI context with top-level `performed_writes=false` and
  `performed_environment_write=false` evidence plus matching audit entries.
- `generate_component`: create or update a component skeleton. Current
  implementation is dry-run only and returns proposed file contents, local
  component/edge/test context evidence, safe runtime metric signal names,
  design notes, test plan items, and verification commands. It is also
  available as `twill mcp generate-component` for local and CI JSON artifacts.
- `generate_endpoint`: create or update an endpoint and contract metadata.
  Current implementation is dry-run only and returns a listener-backed
  `net/http` scaffold plus a proposed safe HTTP endpoint declaration, endpoint
  contract, runbook, test plan, rollout checklist, existing
  component/resource/test evidence, safe runtime metric signal names, and
  existing safe contract/declaration evidence when local context is available.
  It is also available as
  `twill mcp generate-endpoint`.
- `generate_test`: create focused tests for a component or endpoint. Current
  implementation is dry-run only and returns a Twill component test scaffold,
  structured local endpoint contract/declaration and component test evidence,
  safe runtime metric signal names, context-aware success/error/endpoint
  contract/declaration test plan items, and coverage notes when local context
  is available. It is also available as `twill mcp generate-test`.
- `review_api_contract`: inspect compatibility, auth, errors, and docs.
  Current implementation is read-only and returns endpoint-adjacent listener
  findings plus safe generated endpoint contract summaries, backing-resource
  evidence, safe runtime metric signal names, and config schema references when
  present, with explicit metadata limitations.
- `suggest_retry_policy`: recommend timeout, retry, and idempotency settings.
  Current implementation is read-only and returns conservative policy
  suggestions from local app context, safe endpoint contract and declaration
  method/path summaries, safe runtime metric signal names, standard middleware
  evidence, dependency edge evidence, and caller-supplied idempotency.
- `plan_resource_change`: explain database, cache, or pub/sub changes.
  Current implementation is read-only and returns inferred resource kind,
  matched resource context evidence, safe runtime metric signal names, impact
  level, approvals, risks, test plan items, operational checklist, design notes,
  and policy evidence from local context.
- `plan_migration`: produce an incremental migration plan from existing code.
  Current implementation is read-only and scans scoped local source files for
  go-zero, Kratos, net/http, gRPC, config, data-access, middleware, and test
  surfaces, returning staged adapter-first migration guidance and guide
  references for go-zero, Kratos, HTTP, and gRPC plus safe runtime metric
  signal names when local Twill observability evidence is available. It is also
  available as `twill mcp plan-migration` and does not modify files, generate
  adapters, query live metrics, or inspect external projects.
- `diagnose_test_failure`: summarize failing tests and likely root causes.
  Current implementation runs `go test -json` and returns structured failure
  signals, static test package inventory, component test hints, existing local
  Go coverage profile summaries, safe runtime metric signal names when local
  observability evidence is available, and context notes without modifying
  source files. Coverage profile paths are included in files-read audit
  evidence when parsed. Captured output excerpts are passed through baseline
  secret redaction before being returned.
- `diagnose_deploy_failure`: inspect rollout, pods, events, config, and logs.
  Current implementation is read-only and accepts supplied rollout status,
  replica counts, conditions, events, logs, and config changes, combines them
  with safe local Twill component/resource/config/test context and safe runtime
  metric signal names when available, is also available as `twill mcp
  diagnose-deploy` for local evidence files, records the scoped evidence file
  in `files_read` and audit evidence, and returns redacted deployment findings
  without querying or modifying clusters, deployments, metrics, or environments.
- `explain_trace`: explain a slow or failed trace using spans and logs.
  Current implementation is read-only and accepts local spans, inline trace
  JSON, or a scoped trace JSON file, returning slow spans, error spans, a
  visible critical path, safe local Twill component/endpoint/resource context
  matches, safe endpoint contract/declaration method/path summaries, safe
  runtime metric signal names, redacted findings, and verification guidance. It
  is also available as `twill mcp explain-trace` for local trace and evidence
  files, records scoped evidence files in `files_read` and audit evidence, and
  does not query or modify telemetry backends.

P1 tools:

- `generate_db_migration`: draft a SQL migration and runbook. Current
  implementation is dry-run only and returns proposed SQL and runbook files,
  existing component/resource/test context evidence, safe runtime metric signal
  names, test plan items, validation checklist, and design notes without
  creating files, changing database schemas or data, or modifying environments.
  It is also available as `twill mcp generate-db-migration`.
- `generate_pubsub_worker`: draft a Twill pub/sub worker component and runbook.
  Current implementation is dry-run only and returns proposed Go and runbook
  files, existing component/resource context evidence, safe runtime metric
  signal names, test plan items, operational checklist, and design notes
  without creating files, broker resources, subscriptions, or deployments. It is
  also available as
  `twill mcp generate-pubsub-worker`.
- `generate_cron_job`: draft a Twill cron component and runbook. Current
  implementation is dry-run only and returns proposed Go and runbook files,
  existing component/resource context evidence, safe runtime metric signal
  names, test plan items, operational checklist, and design notes without
  creating files, scheduler resources, or deployments. It is also available as
  `twill mcp generate-cron-job`.
- `generate_observability`: draft metrics, logs, traces, alerts, and runbook
  guidance from local Twill context. Current implementation is dry-run only and
  returns proposed observability documentation with safe endpoint
  contract/declaration method/path summaries, resource evidence, runtime
  observability-default evidence, and component test hints when present,
  without creating files, dashboards, alerts, or external resources.
- `review_security`: review auth, secrets, config, endpoint, middleware,
  resource, and AI tool safety risks. Current implementation is read-only and
  returns policy-backed metadata findings, structured local
  component/endpoint contract and declaration/middleware/resource/config
  evidence, safe runtime metric signal names, and explicit limitations.
- `review_performance`: review benchmark coverage, dependency fan-out,
  endpoint-adjacent latency risks, and profiling gaps. Current implementation
  is read-only and returns static metadata findings, component-level test
  hints, safe endpoint contract and declaration method/path summaries,
  standard middleware evidence, backing-resource evidence, safe runtime metric
  signal names, and benchmark verification commands when benchmarks are
  present.
- `estimate_cost`: estimate qualitative compute, traffic, network, resource,
  and environment cost drivers. Current implementation is read-only and avoids
  cloud-provider price claims, while returning direct modeled backing-resource
  evidence, safe endpoint contract/declaration method/path summaries, and safe
  runtime metric signal names for route-level traffic assumptions until
  pricing, region, topology, and usage inputs are supplied.
- `generate_slo`: draft availability, latency, indicator, alert, and missing-
  input SLO metadata from endpoint-adjacent Twill context. Current
  implementation is dry-run only, includes safe generated endpoint
  contract/declaration summaries, safe runtime metric signal names,
  backing-resource evidence, component test hints, and existing local Go
  coverage profile summaries when present, and does not create files,
  dashboards, telemetry queries, refresh coverage, or alert rules.
- `prepare_pr_summary`: draft PR title, summary bullets, risks, checklist, and
  verification commands from local git status and diff metadata. Current
  implementation is read-only, adds safe local Twill component, endpoint,
  contract/declaration, resource, runtime metric signal, test, and policy
  context when available, redacts common secret patterns from git metadata
  before output, reports original changed-file and diffstat counts alongside
  bounded output lists and truncation markers, is also available as
  `twill mcp pr-summary` for CI/artifact workflows, and does not create
  commits, branches, remotes, comments, or pull requests.

Tool output should include:

- Inputs used.
- Files or resources read.
- Proposed changes.
- Safety notes.
- Commands to verify.
- Whether the tool performed writes.
- `audit_event` evidence with tool name, scope, read resources, proposed
  changes, action evidence, verification commands, safety notes, and write
  flags.

## Project Skill Pack

Baseline skills:

### component-design

Guides agents to design component boundaries, dependencies, colocation hints,
and remote-call semantics.

### api-design

Guides endpoint naming, request/response shape, error mapping, auth, versioning,
OpenAPI/protobuf generation, and compatibility tests.

### db-migration

Guides schema changes, migration ordering, rollback planning, data safety, and
environment promotion.

### pubsub-worker

Guides topic naming, subscriber design, retries, dead-letter queues,
idempotency, and observability.

### observability

Guides trace spans, metrics, logs, SLOs, alert rules, and dashboard panels.

### testing

Guides unit, component, integration, simulation, contract, and deployment tests.

### performance

Guides profiling, hot path review, allocation review, batching, caching, and
load-test design.

### security-review

Guides auth, authorization, secret usage, tenant isolation, data exposure, and
deployment policy review.

### gozero-migration

Guides incremental migration from go-zero API/RPC/model/service patterns into
Twill adapters or native services.

### kratos-migration

Guides incremental migration from Kratos transport, service, biz, data, config,
registry, and middleware patterns.

## Agent Workflows

### New Endpoint Workflow

1. Inspect application graph.
2. Select target service and component.
3. Generate endpoint contract.
4. Generate handler/component code.
5. Generate tests.
6. Update docs and dashboard metadata.
7. Run verification commands.
8. Produce a PR summary.

### New Resource Workflow

1. Inspect resource graph.
2. Propose resource declaration.
3. Generate local binding.
4. Generate Kubernetes binding.
5. Generate migration or initialization code.
6. Update policy metadata.
7. Generate tests and verification steps.

### Migration Workflow

1. Inspect existing project structure.
2. Classify services, endpoints, configs, data access, and middleware.
3. Produce a staged migration plan.
4. Add adapter mode first.
5. Convert one endpoint or component at a time.
6. Preserve existing tests.
7. Add platform-native observability.
8. Remove adapters only after parity is proven.

### Deployment Diagnosis Workflow

1. Inspect deployment status.
2. Inspect rollout state.
3. Inspect Kubernetes events.
4. Inspect health checks.
5. Inspect config and secret references.
6. Inspect logs, metrics, and traces.
7. Identify likely root cause.
8. Recommend rollback, retry, config fix, or code fix.

### Trace Diagnosis Workflow

1. Load trace summary.
2. Identify critical path.
3. Find slow spans and error spans.
4. Correlate logs and metrics.
5. Identify remote calls, retries, and resource calls.
6. Suggest instrumentation or code changes.
7. Suggest tests or load reproduction.

## Safety Model

Agent operations must be classified:

- Read-only: inspect graph, docs, status, logs, traces, metrics.
- Local write: modify files in a working tree.
- Local execute: run tests, codegen, lint, or dry-run deployment.
- Environment write: change dev/staging/preview resources.
- Production write: change production resources or traffic.

Default rules:

- Read-only is allowed after local user consent.
- Local writes require diff preview.
- Local execute requires command transparency.
- Environment writes require explicit environment selection.
- Production writes require human approval and audit.
- Secret values are never exposed to model context.
- Logs are redacted before entering AI context.
- Agent actions are recorded with inputs, outputs, and tool versions.

## Prompt Injection Defenses

The platform should:

- Treat logs, traces, issue text, PR text, and external docs as untrusted input.
  Current implementation also marks MCP prompt arguments as untrusted data and
  redacts common secret patterns before returning prompt text.
- Separate instructions from data.
- Redact secrets before context assembly.
  Current implementation avoids config/secret value inference and redacts
  common secret patterns from test-diagnosis output excerpts, trace inputs,
  deployment evidence, PR-summary git metadata, analysis/planning tool
  inputs, and migration source-scan file metadata.
- Expose config binding metadata as safe structure only. Current implementation
  reports binding kind, required flag, source, and normalized provider/lifecycle
  markers without exposing env vars, ConfigMap names, Secret names, keys, or
  values.
- Require tool scopes.
- Reject tool calls that exceed the active scope.
  Current implementation treats `twill mcp serve --dir` as the local directory
  scope root and rejects MCP tool/resource requests that override `dir` outside
  it or use absolute/parent-relative local package patterns outside it.
- Provide dry-run mode for generated operational actions.
- Preserve an audit trail for all agent-generated changes.

## Implementation Roadmap

Dependency rule: a tool ships only after the platform feature it wraps exists.
`generate_endpoint` full endpoint-model integration requires the Phase 1
endpoint model; `diagnose_deploy_failure` live backend integration requires
deployment metadata; `plan_migration` adapter generation requires adapter
modes. Phase A deliberately contains only what the current codebase can already
answer or what callers supply as local diagnostic evidence.

### Phase A: Local AI Context

Depends on: nothing beyond the current codebase plus the application graph
export (ROADMAP Phase 0/1).

- Add application graph export.
- Add `twill mcp serve` prototype.
- Expose read-only resources for local context packs, graph, components,
  endpoints, config, tests, observability, policy rules, and generated
  metadata.
- Add dry-run IDE MCP configuration export for VS Code and Cursor.
- Add a read-only PR summary CLI surface for CI bots and local agent workflows.
- Expose initial MCP prompts for component design, API design, resource
  changes, migration planning, DB migrations, cron jobs, pub/sub workers, test
  diagnosis, deployment diagnosis, trace explanation, and security review.
- Add `twill skill init`.
- Add baseline skills: component-design, api-design, testing, observability,
  security-review, db-migration, pubsub-worker, performance, gozero-migration,
  and kratos-migration.

### Phase B: Safe Code Generation

Depends on: Phase 1 endpoint model and codegen stability.

- Add tools for component, endpoint, test, pub/sub worker, cron, and database
  migration generation.
- Add dry-run and patch output.
- Add safe endpoint declaration metadata for method, path, listener, component,
  type references, auth, middleware, and compatibility evidence. Current
  implementation derives normalized declarations from `docs/endpoints/*.md`
  without exposing raw contract text, and also reports `twill.HTTPAdapter`
  routes and `twill.GRPCAdapter` unary methods as `kind: adapter` declarations
  from generated metadata. It also reports conservative source-level `net/http`
  method-aware routes and `twill.GRPCAdapter` marker tags as `kind: source`
  declarations when the owning component and listener are unambiguous.
- Add safe protobuf contract metadata from local `.proto` files. Current
  implementation exposes `twill app protobuf` and `app.protobuf` for package,
  service, RPC request/response type references, message type, source-file
  metadata, and runtime hints. When a local protobuf RPC matches a gRPC adapter
  by service/method, its `RequestType` and `ResponseType` are associated with
  endpoint metadata and flow into client SDK `rpc_operations` and
  contract-test `rpc_cases`. Runtime gRPC adapter helpers can register and
  serve existing gRPC servers through Twill listeners; full protoc-compatible
  parsing and schema-rich clients remain future work.
- Add dry-run client SDK generation from safe endpoint metadata. Current
  implementation exposes `twill app client` and `app.clients` for Go and
  TypeScript HTTP clients plus gRPC adapter stub metadata, carrying declared
  or protobuf-inferred request/response type references when present, without
  writing files; schema-rich protobuf/gRPC clients remain future work.
- Add dry-run endpoint contract-test generation from safe endpoint metadata.
  Current implementation exposes `twill app contract-tests` and
  `app.contract_tests` for guarded Go HTTP contract tests without writing
  files plus guarded gRPC adapter contract-test stubs, carrying declared
  or protobuf-inferred request/response type references when present; schema
  assertions and protobuf payload construction remain future work.
- Add dry-run Docker Compose planning from safe resource metadata. Current
  implementation exposes `twill app compose`, `twill deploy compose`, and
  `deploy.compose` for local database, cache, pub/sub/queue, and object
  storage dependency plans without running Docker or writing files.
- Add read-only planning tools for API contract review and retry policy
  suggestions. Current implementation exposes `review_api_contract` and
  `suggest_retry_policy` over MCP and as `twill mcp review-api` and
  `twill mcp suggest-retry` CLI JSON wrappers.
- Add read-only resource change planning with policy evidence.
  Current implementation exposes `plan_resource_change` over MCP and as
  `twill mcp plan-resource-change`, a read-only CLI JSON wrapper with policy,
  structured approval evidence, risk, test, and operational evidence. The
  matching audit event summarizes action and approval evidence for release
  artifacts.
- Add tool audit log. Current implementation emits a structured `audit_event`
  in MCP tool outputs and can append tool and resource-read events to an
  optional scoped JSONL audit log via `twill mcp serve --audit-log <path>`.
  The `audit.events` MCP resource and `twill mcp audit-events` CLI report read
  that JSONL as structured audit evidence without appending entries. Malformed
  JSONL lines are counted in `invalid_entry_count` and reported as bounded
  `invalid_entries` samples with line numbers and parser errors while valid
  entries remain available; raw malformed line content is not exposed.
- Add verification command suggestions.

### Phase C: Operations Context

Depends on: Phase 1 Kubernetes deployment metadata and observability defaults.

- Expose deployment status, Kubernetes resources, logs, metrics, and traces.
  Current implementation exposes static deployment status limitations plus
  native `twill deploy k8s` dry-run Kubernetes resources, `twill deploy aws`
  EKS/ECR/IRSA/ALB dry-run metadata, rollout metadata, and safe opt-in
  `runtime/observability` default references. Live deployment and telemetry
  backends are still outside the local context surface.
- Add deploy failure and trace diagnosis tools.
  Current implementation exposes the tools over MCP and as read-only CLI JSON
  wrappers for local evidence files.
- Add test failure diagnosis reports. Current implementation exposes
  `diagnose_test_failure` over MCP and as `twill mcp diagnose-test`, a scoped
  `go test -json` wrapper that returns failed tests, static test context,
  existing local Go coverage profile summaries, safe runtime metric signal
  names when available, redacted excerpts, diagnosis notes, and audit evidence
  without source writes or live metric queries.
- Add cost estimate and SLO draft reports.
  Current implementation exposes `estimate_cost`, `generate_slo`, and
  `generate_observability` over MCP and as `twill mcp estimate-cost`,
  `twill mcp generate-slo`, and `twill mcp generate-observability` CLI JSON
  wrappers for local context and caller-supplied assumptions.
- Add redaction and prompt-injection safeguards.

### Phase D: Migration Agents

Depends on: Phase 1 adapter modes.

- Add go-zero and Kratos migration analyzers.
  Current implementation includes read-only source scanning through
  `plan_migration`, the `twill mcp plan-migration` CLI report, project skill
  templates, and integration guides in `docs/migration_gozero.md`,
  `docs/migration_kratos.md`, and `docs/migration_agent.md`, with safe runtime
  metric signal names when local Twill observability evidence is available.
- Add adapter generation. Current runtime support includes explicit
  `twill.HTTPAdapter` metadata plus `runtime/adapters` helpers for routing
  existing `net/http` handlers through Twill listeners while preserving safe
  endpoint context. Current gRPC support includes explicit `twill.GRPCAdapter`
  metadata for ownership, context, dashboard, and MCP visibility plus
  `runtime/adapters` helpers for registering and serving existing gRPC servers
  through Twill listeners without adding a Twill runtime dependency on grpc;
  when local `.proto` files are present, matching RPC `RequestType` and
  `ResponseType` metadata is associated with adapters by service/method.
- Add staged migration reports. Current `plan_migration` includes a
  `staged_report` with per-phase readiness status, evidence, blocking
  compatibility checks, and next steps.
- Add compatibility checks. Current `plan_migration` reports static
  compatibility checks for endpoint/RPC contracts, middleware order, config
  compatibility, data/resource ownership, and parity-test coverage.

### Phase E: Enterprise AI Controls

Depends on: ROADMAP Phase 3 control plane and identity model.

- Add policy-backed tool scopes.
- Add RBAC integration. Current `plan_resource_change` reports structured
  role evidence for source, environment, and production impact levels and
  mirrors RBAC summaries into the tool audit event; live identity backends
  remain future work.
- Add production approval flows. Current `plan_resource_change` reports
  structured approval evidence for source, environment, and production impact
  levels and mirrors approval summaries into the tool audit event; live
  approval backends remain future work.
- Add team skill packs. Current `twill skill init` writes a deterministic
  `.twill/skills/team-pack.json` manifest listing canonical, Claude-compatible,
  and OpenAI metadata paths plus policy and verification entry points.
- Add AI action evidence for audits. Current audit events include tool scope,
  files/resources read, proposed change paths, structured action evidence,
  verification commands, safety notes, write flags, resource-change approval
  evidence summaries, and resource-change RBAC evidence summaries.

## Success Metrics

Baseline rule: every metric below is measured against the pilot application
(the gaming backend in this workspace) — record the current time/coverage
numbers before Phase A ships so "decreases by 50%" has a denominator.

- Time to add a standard endpoint decreases by at least 50%.
- Generated tests cover the common success and failure paths.
- Agents can explain deployment failures using platform data without secret
  access.
- Agents can produce incremental migration plans for existing Go services.
- At least 30% of routine scaffolding and diagnostic tasks are agent-assisted
  within the first production pilot.

## Near-Term Work

1. Define the application graph schema.
2. Define MCP resource names and payload schemas.
3. Define the first tool interface and audit format.
4. Create initial project-local skill templates.
5. Integrate AI context generation into CI and local dashboard.
   Current CI runs `./dev/verify_ai_context ./examples/hello` to export and
   validate every local `twill app` AI context/API surface, dry-run deployment
   plan, MCP report, dry-run/read-only marker, environment-write marker, and
   representative redaction boundary; it also smoke-tests `twill skill init`
   project skill generation. The local status dashboard now exposes the same
   typed app context through `/app` and `/app/data`.
