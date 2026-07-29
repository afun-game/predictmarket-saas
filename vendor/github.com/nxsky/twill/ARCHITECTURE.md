# Twill Architecture

This document describes the target architecture for Twill, the enterprise Go
microservice platform derived from Twill.

## Architectural Goal

Provide a Go-first platform where developers can write modular applications with
ordinary Go code, run locally with low friction, and deploy to Kubernetes as
observable, governed, scalable microservices.

The platform should preserve Twill's strongest idea: component
boundaries are both programming boundaries and runtime placement boundaries. It
must also make distributed behavior explicit enough for production teams.

## Current Foundation

The existing codebase provides:

- Component model based on Go interfaces and generated stubs.
- Local single-process runtime.
- Multiprocess runtime.
- SSH deployer prototype.
- External deployer dispatch for Kubernetes and GKE-style deployers.
- Metrics, tracing, logging, profiling, status dashboard, routing, and testing
  utilities.
- Code generation for component registration, references, routing, and
  serialization.

The current foundation is strong for componentized distributed applications, but
it lacks several enterprise platform layers: stable extension APIs, API contract
management, infrastructure primitives, governance, a production control plane,
and AI-native developer workflows.

## Target System Overview

```text
Developer Tools
  CLI, codegen, local runner, dashboard, IDE integration, MCP server, skills

Application Model
  Components, services, endpoints, jobs, subscribers, workflows, resources

Runtime
  Component host, RPC transport, routing, load reporting, lifecycle, telemetry

Control Plane
  Apps, environments, deployments, versions, config, policy, rollout, audit

Infrastructure Layer
  Kubernetes, Helm/Operator/Terraform, databases, caches, queues, secrets

Observability Layer
  OpenTelemetry, Prometheus, logs, profiles, traces, service graph, SLOs

AI Layer
  MCP resources, MCP tools, project skills, review agents, SRE agents
```

## Core Domain Model

### Application

An application is the top-level deployable unit. It owns components, services,
resources, configuration, environments, policies, and deployments.

### Component

A component is an internal Go interface plus an implementation. Components may
be colocated in one process or split across processes, pods, nodes, clusters, or
regions.

Component calls may be:

- Local in-process calls.
- Internal remote calls through the platform RPC layer.
- Routed calls based on component routing keys.

Distributed semantics must be visible in generated metadata, dashboards, traces,
and AI context.

### Service

A service is an externally or internally addressable API surface. A service can
be backed by one or more components. Services may expose HTTP, gRPC, or both.

### Endpoint

An endpoint is a typed API method with request, response, auth, rate limit,
timeout, retry, idempotency, and documentation metadata.

Endpoints should produce:

- OpenAPI for HTTP APIs.
- Protobuf descriptors for gRPC APIs.
- Client SDK metadata.
- Contract test metadata.

### Resource

A resource is infrastructure used by the application:

- SQL database.
- Cache.
- Queue.
- Pub/Sub topic.
- Object store.
- Secret.
- Config.
- Cron job.
- Workflow.
- Lock.
- Feature flag.

Resources should be represented in the application graph and mapped to local,
Kubernetes, and cloud implementations.

### Environment

An environment is an isolated runtime target such as local, dev, staging,
preview, or production. Each environment owns config, secrets, resource
bindings, deployment policy, and access rules.

### Deployment

A deployment is a specific version of an application running in an environment.
Deployments include application graph metadata, binary/image identity, resource
bindings, rollout state, and observability links.

### Policy

Policies govern deployment, security, traffic, resource limits, public API
exposure, secret access, and data migration behavior.

## Runtime Architecture

### Data Plane

The data plane runs application code.

Responsibilities:

- Host component implementations.
- Serve public and internal endpoints.
- Execute internal RPC calls.
- Report health, load, metrics, logs, traces, and profiles.
- Enforce local middleware and call policies.
- Receive routing and config updates from the control plane.

### Control Plane

The control plane manages desired and observed state.

Responsibilities:

- Track apps, environments, components, deployments, versions, and routes.
- Manage rollout strategies and rollback decisions.
- Store application graph metadata.
- Coordinate config and secret references.
- Enforce policies.
- Provide APIs for the CLI, dashboard, and AI tools.

The open-source core should support a local or in-cluster lightweight control
plane. An enterprise edition may add multi-tenant management, audit, approvals,
and hosted operations.

### Component Placement

Placement determines where components run and which components are colocated.

Inputs:

- Static dependency graph.
- Runtime load and latency.
- Resource requirements.
- Colocation hints.
- Isolation requirements.
- Rollout state.
- Cost policy.

Initial implementation should be deterministic and simple. Advanced scheduling
can arrive later after telemetry and policy data are reliable.

### Internal RPC

Internal RPC must support:

- Context propagation.
- Deadlines and cancellation.
- Structured errors.
- Retry policy.
- Idempotency metadata.
- Load balancing.
- Circuit breaking.
- OpenTelemetry tracing.
- Metrics per caller, component, method, and remote/local mode.

The platform should avoid hiding remote-call risk. Generated code and docs must
make partial execution, duplicate execution, and retry safety clear.

## API Layer

The API layer should support two styles:

1. Code-first Go endpoints for fast development.
2. API-first protobuf/OpenAPI contracts for enterprise teams.

Both styles should map to the same endpoint metadata model.

Required capabilities:

- HTTP and gRPC transports.
- Request validation.
- Auth hooks.
- Middleware chain.
- Error mapping.
- OpenAPI/protobuf generation.
- Contract tests.
- API versioning.
- Client metadata generation.

## Infrastructure Primitives

The platform should provide typed primitives while allowing escape hatches.

Initial P0 primitives:

- PostgreSQL or generic SQL database.
- Redis cache.
- Pub/Sub topic.
- Cron job.
- Secret.
- Config.

Later primitives:

- Object store.
- Workflow.
- Durable queue.
- Distributed lock.
- Feature flag.
- Search index.
- Vector store.

Each primitive needs:

- Local implementation.
- Kubernetes binding.
- Cloud binding interface.
- Observability labels.
- Policy metadata.
- AI-readable documentation and examples.

## Kubernetes Architecture

Kubernetes is the default production substrate.

Deployment is native to the core CLI. The upstream design shipped Kubernetes
support as a separate `twill-kube` binary invoked through an external-deployer
dispatch; this platform rewrites it in-repo as built-in deploy targets:

- `twill deploy k8s`: works against any kubeconfig (EKS, GKE, AKS,
  self-managed). This is the P0 path and the only thing required to reach
  production.
- `twill deploy aws`: AWS conveniences (ECR push, EKS context, IAM/IRSA,
  ALB) layered on the k8s target.
- `twill deploy gke` and other clouds: same layering pattern, added on
  demand.

Cloud targets must stay thin: everything cluster-shaped belongs in the k8s
target, and a cloud target only resolves credentials, registries, and
provider-specific annotations. The `Deployer` extension API stays public so
external deployers remain possible.

The platform should generate or manage:

- Deployment or StatefulSet.
- Service.
- Gateway API or Ingress.
- HorizontalPodAutoscaler.
- ServiceAccount.
- ConfigMap and Secret references.
- Probes.
- Resource requests and limits.
- NetworkPolicy.
- PodDisruptionBudget.
- Rollout metadata.

Longer term, Kubernetes support should be available through:

- CLI-generated manifests for simple teams.
- Helm charts for platform teams.
- Operator for advanced runtime coordination.
- Terraform or Crossplane provider for cloud resources.

## Observability Architecture

OpenTelemetry is the default instrumentation layer.

Required signals:

- Distributed traces.
- Metrics.
- Structured logs.
- CPU and heap profiles.
- Runtime health.
- Component load.
- Application graph.
- API latency, error rate, and throughput.

The dashboard should start local-first and later become a full web console.

The observability model must be AI-readable. Agents should be able to inspect a
trace, identify related logs and metrics, and suggest concrete next actions.

## AI Architecture

AI agents should not rely on unstructured repository access alone. The platform
must expose:

- Structured application graph.
- Generated metadata.
- API contracts.
- Config schemas.
- Resource graph.
- Deployment state.
- Observability data.
- Safe code generation tools.
- Audit logs for agent actions.

The MCP server is the primary integration surface for external agents and IDEs.
Project-local skills provide repeatable workflows and coding standards.

See `AI_NATIVE_PLAN.md` for the detailed plan.

## Extension Architecture

Target public extension points:

- Transport.
- Middleware.
- Registry.
- Config provider.
- Secret provider.
- Resource provider.
- Deployer.
- Telemetry exporter.
- Policy checker.
- Code generator plugin.
- Dashboard plugin.
- AI tool provider.

Extension APIs must be versioned and tested. Internal packages should remain
internal until the API shape is stable.

## Compatibility and Migration

The platform must support progressive adoption.

Migration modes:

- Twill compatible mode for existing component apps.
- net/http adapter mode.
- gRPC adapter mode.
- go-zero adapter and migration guide.
- Kratos adapter and migration guide.
- Sidecar or gateway mode for services that cannot be rewritten yet.

The migration principle is simple: let teams join the platform before rewriting
their business code.

## Security Architecture

Security requirements:

- OIDC for human identity.
- Service identity for workloads.
- mTLS for internal communication.
- RBAC for platform actions.
- Secret scoping.
- Audit logs.
- Policy gates.
- Supply-chain checks.
- Generated-code integrity checks.
- AI tool permission scopes.
- Secret redaction in AI context.

Security must be part of Phase 1 and Phase 2 design, not a late enterprise
add-on.

## Key Technical Decisions

### Decision 1: Keep the Component Model

The component model is the unique asset of the codebase. It should remain the
core internal programming model.

### Decision 2: Add Explicit API Contracts

The platform needs HTTP/gRPC contracts, OpenAPI, protobuf, and client metadata
to compete with mature enterprise frameworks.

### Decision 3: Make Kubernetes the Default Production Target

Kubernetes gives the broadest enterprise adoption path and avoids early lock-in
to a single cloud provider.

### Decision 4: Treat AI as a Platform Interface

AI support should be implemented through structured metadata, MCP, skills, and
safe tools, not only prompts in documentation.

### Decision 5: Support Incremental Migration

The original Twill adoption problem was the need to rewrite large parts
of existing applications. Twill must avoid repeating that mistake.

## Near-Term Architecture Work

1. Identify current runtime seams that can become public extension APIs.
2. Design endpoint metadata and OpenAPI/protobuf generation.
3. Design the resource primitive schema.
4. Design the local control plane and application graph store.
5. Design the MCP resource and tool model.
6. Design Kubernetes deployment metadata and rollout state.
7. Define compatibility boundaries for existing Twill apps.
