# Twill

Twill is an AI-native, Kubernetes-first, Go microservice platform.

The current codebase is based on the upstream v0.24.6 codebase. The module path
and CLI have been migrated to `github.com/nxsky/twill` and `twill`.


# Changelog
v0.24.7 (2025-06-13)

Fix: Compatibility issue with the latest version of Go.
feat: Upgrade dependency to the latest version.

## Project Evolution

This repository is now the foundation for **Twill**, a new enterprise-grade Go
microservice platform. The project name, module path, import path, CLI,
generated code, and documentation have been migrated to the Twill identity.

Planning documents:

- [ROADMAP.md](./ROADMAP.md): product goal, priorities, phases, and milestones.
- [ARCHITECTURE.md](./ARCHITECTURE.md): target architecture and system model.
- [AI_NATIVE_PLAN.md](./AI_NATIVE_PLAN.md): MCP, agent tools, and skill plan.
- [docs/internal_api_audit.md](./docs/internal_api_audit.md): internal package
  audit and public extension API candidates.
- [docs/phase1_mvp_backlog.md](./docs/phase1_mvp_backlog.md): issue-sized
  Phase 1 MVP work items and acceptance evidence.
- [docs/http_adapters.md](./docs/http_adapters.md): metadata adapter mode for
  existing `net/http` routes.
- [docs/grpc_adapters.md](./docs/grpc_adapters.md): metadata adapter mode for
  existing gRPC unary methods.
- [docs/config.md](./docs/config.md): safe config schema and binding metadata.
- [docs/openapi.md](./docs/openapi.md): deterministic OpenAPI export from
  safe HTTP endpoint metadata.
- [docs/ide_integration.md](./docs/ide_integration.md): VS Code and Cursor MCP
  setup for Twill local context.
- [docs/pr_bot.md](./docs/pr_bot.md): read-only PR summary bot workflow.
- [docs/resources.md](./docs/resources.md): safe resource declaration metadata
  for SQL, Redis/cache, Pub/Sub, queues, cron, and object storage.
- [docs/sre_diagnostics.md](./docs/sre_diagnostics.md): read-only deployment
  and trace diagnostic workflows for SRE agents.
- [docs/aws_deploy.md](./docs/aws_deploy.md): dry-run AWS EKS deployment
  planning on top of the native Kubernetes planner.
- [docs/migration_gozero.md](./docs/migration_gozero.md): incremental go-zero
  migration guide.
- [docs/migration_kratos.md](./docs/migration_kratos.md): incremental Kratos
  migration guide.
- [docs/migration_agent.md](./docs/migration_agent.md): read-only migration
  planning agent workflow.
- [docs/rename_and_private_repo.md](./docs/rename_and_private_repo.md): rename
  and private repository migration plan.
- [docs/release_checklist.md](./docs/release_checklist.md): release gates and
  verification checklist.

Twill is a programming framework for writing, deploying, and managing
distributed applications. You can run, test, and debug a Twill
application locally on your machine, and then deploy it to the
cloud with a single command.

```bash
$ go run .                                            # Run locally.
$ twill deploy k8s --image example.com/app:latest .  # Plan Kubernetes deployment.
```

Run the checked-in hello example:

```bash
go run ./cmd/twill generate ./examples/hello
go run ./examples/hello
go run ./cmd/twill app context ./examples/hello
go run ./cmd/twill app resources ./examples/hello
go run ./cmd/twill app client ./examples/hello
go run ./cmd/twill app contract-tests ./examples/hello
go run ./cmd/twill deploy compose ./examples/hello
go run ./cmd/twill deploy k8s --image twill-hello:latest ./examples/hello
go run ./cmd/twill deploy aws \
  --region us-east-1 \
  --account 123456789012 \
  --repository twill/hello \
  ./examples/hello
./dev/verify_hello_smoke ./examples/hello
./dev/verify_mainline_light ./examples/hello
./dev/verify_ai_context ./examples/hello
./dev/verify_ai_context_selftest
./dev/verify_release ./examples/hello
```

The hello example includes safe endpoint, config, middleware, observability,
resource, Docker Compose, Kubernetes, AWS, OpenAPI, client SDK, contract-test,
protobuf, and test metadata so local verification exercises both the non-MCP
local developer flow and the same Phase 1 context surfaces used by MCP and
dashboard views.

The `twill app` commands can also export individual read-only context surfaces:
`client`, `compose`, `components`, `config`, `context`, `contract-tests`,
`deployment`, `endpoints`, `generated`, `graph`, `middleware`,
`observability`, `openapi`, `policy`, `protobuf`, `resources`, and `tests`.
The full `context` bundle, dry-run plans, and MCP tool reports expose
`performed_writes: false` and `performed_environment_write: false` so agents
can verify that no files, deployments, cloud APIs, containers, or other
environments were mutated.
`twill app openapi` emits a
minimal deterministic OpenAPI JSON document from safe endpoint contract
metadata. `twill app client` emits a Go or TypeScript client SDK plan from the
same safe HTTP endpoint metadata, and `twill app contract-tests` emits a Go
HTTP contract-test plan with runtime guards for auth and unsafe methods. Both
commands are dry-run by default and write files only when `--write-dir` is
provided. `twill app compose` and
`twill deploy compose` emit dry-run Docker Compose plans for local dependent
infrastructure without running Docker. They write `docker-compose.twill.yaml`
and optional `docker-compose.twill.env.example` only when `--write-dir` is
provided. Pass `--dir PATH` to inspect an application directory from another
working directory.

`twill mcp serve` exposes the same local context over MCP for coding agents.
`twill mcp config --client vscode` and `twill mcp config --client cursor`
print dry-run IDE MCP configuration snippets for VS Code and Cursor without
writing files. `twill mcp pr-summary` prints a read-only PR summary draft from
local git metadata and safe Twill app context for CI bots and agent workflows.
`twill mcp review-security` and `twill mcp review-performance` print read-only
review reports from local Twill context, policy metadata, and test hints
without reading secrets, running load tests, writing files, or mutating
environments.
`twill mcp diagnose-test` runs scoped `go test -json` and prints a read-only
test diagnosis report with redacted output excerpts and static `app.tests`
context, including static unit, fuzz/property, benchmark, simulation, and chaos
strategy hints; failing tests are represented in JSON rather than treated as
command execution errors.
`twill mcp review-api`, `twill mcp suggest-retry`, and
`twill mcp plan-resource-change` print read-only planning reports for endpoint
contracts, retry policy, and resource changes without writing files or mutating
environments.
`twill mcp generate-component`, `twill mcp generate-test`,
`twill mcp generate-endpoint`, `twill mcp generate-cron-job`,
`twill mcp generate-pubsub-worker`, and `twill mcp generate-db-migration`
print dry-run scaffold reports with proposed file contents, SQL drafts, review
context, test plans, operational checklists, and audit evidence without writing
files or mutating environments.
`twill mcp diagnose-deploy` and `twill mcp explain-trace` print read-only SRE
diagnostic reports from local evidence JSON without querying live backends.
`twill mcp plan-migration` prints a read-only adapter-first migration plan from
local source inventory for go-zero, Kratos, `net/http`, gRPC, Gin, and Echo
services. `twill mcp estimate-cost`, `twill mcp generate-slo`, and
`twill mcp generate-observability` print read-only operations planning reports
from local Twill context and caller assumptions without reading billing
systems, writing files, creating alerts, or mutating environments.
Pass `--audit-log .twill/audit/events.jsonl` to append structured MCP tool and
resource-read audit events to a scoped JSONL file and read them through the
`audit.events` resource or `twill mcp audit-events --audit-log`. When no audit
log path is configured, `audit.events` reports that persistence is disabled and
returns no entries. The MCP resource set includes `app.openapi` for the same
minimal OpenAPI export available from
`twill app openapi`, `app.clients` for dry-run client SDK context,
`app.contract_tests` for dry-run endpoint contract-test context,
`app.protobuf` for safe protobuf package/service/RPC metadata, and
`app.middleware` for standard middleware evidence. Deployment resources include
`deploy.compose` for local Docker Compose dependency context. See
[docs/ide_integration.md](./docs/ide_integration.md) and
[docs/pr_bot.md](./docs/pr_bot.md).
Cost and SLO report commands are documented in
[docs/ai_operations.md](./docs/ai_operations.md).
Security and performance review commands are documented in
[docs/ai_reviews.md](./docs/ai_reviews.md).
Test failure diagnostics are documented in
[docs/test_diagnostics.md](./docs/test_diagnostics.md).
API, retry, and resource planning reports are documented in
[docs/ai_planning.md](./docs/ai_planning.md).
Dry-run generation reports are documented in
[docs/ai_generation.md](./docs/ai_generation.md).

Deployment and trace diagnostic CLI wrappers are documented in
[docs/sre_diagnostics.md](./docs/sre_diagnostics.md).

Migration-agent usage is documented in
[docs/migration_agent.md](./docs/migration_agent.md).

`./dev/verify_ai_context ./examples/hello` exports and validates every local
`twill app` AI context/API surface, first checking that the verified surface
list matches `twill app --help` and that verified MCP commands match
`twill mcp --help`. It also validates dry-run deployment targets,
read-only/dry-run MCP report commands, and the generated project-local skill
metadata, including dry-run/read-only markers and representative secret
redaction boundaries. It uses per-run temporary output and skill directories so
parallel verifier runs do not share artifacts.
`./dev/verify_mainline_light ./examples/hello` is the routine mainline
developer gate. It checks gofmt drift, repository whitespace, `go test ./...`,
`go vet ./...`, the non-MCP hello smoke flow, and
`TWILL_AI_CONTEXT_SURFACE_CHECK_ONLY=1 ./dev/verify_ai_context` so CLI surface
coverage can drift-detect without running full MCP report generation.
`./dev/verify_deploy_plans ./examples/hello` validates the Compose,
Kubernetes, and AWS deployment dry-run JSON plans without running the broader
AI/MCP context gate.
`./dev/verify_dashboard_app_context` validates the dashboard `/app` and
`/app/data` local app context projection plus dashboard CLI app-context flags
without starting a dashboard server.
`./dev/verify_local_artifact_writes ./examples/hello` validates explicit
`--write-dir` writes for client SDK, contract tests, Compose, Kubernetes, and
AWS review artifacts without running Docker, Kubernetes, AWS, or MCP flows.
`./dev/verify_ai_context_selftest` exercises the app surface and MCP command
coverage verifier's positive and negative drift paths and is included in the
release smoke gate.
`./dev/verify_release ./examples/hello` runs the local release smoke gate,
including `gofmt` drift detection for git-known Go files, repository whitespace
checks, module tidy checks, website generation and nested website module tests,
and the CI race-sensitive package subset. It uses a per-run temporary output
directory, asserts Compose, Kubernetes, and AWS deployment plans remain dry-run,
read-only JSON without live deploy or cloud API verification commands, runs
generated-code drift verification with the pinned generator toolchain used by
CI, and runs the pinned static-analysis and vulnerability gate.
`./dev/verify_go_mod_tidy` runs `go mod tidy` for every repository module,
including standalone website example modules, and fails on `go.mod` or `go.sum`
drift.
`./dev/verify_website` generates the static website into a temporary output
directory and runs `go test ./...` for standalone Go modules under `website/`.
`./dev/verify_whitespace` runs `git diff --check` and scans tracked and
unignored text files for trailing whitespace, CRLF line endings, and missing
final newlines.
`./dev/verify_whitespace_selftest` exercises the whitespace verifier's clean
repository, binary-file, trailing-whitespace, CRLF, missing-final-newline, and
`git diff --check` failure paths and is included in the release smoke gate.
`./dev/verify_ci_metadata` validates GitHub metadata YAML, allowlisted GitHub
Action references recorded in `dev/tool_versions.env`, required PR/issue
template sections for roadmap, safety, and verification evidence, and the
release checklist's full-gate command list. It accepts `TWILL_CI_WORKFLOW`,
`TWILL_CI_DEPENDABOT`, `TWILL_CI_ISSUE_CONFIG`, `TWILL_CI_PR_TEMPLATE`,
`TWILL_CI_BUG_TEMPLATE`, `TWILL_CI_FEATURE_TEMPLATE`,
`TWILL_CI_ROADMAP_TEMPLATE`, `TWILL_CI_README`,
`TWILL_CI_RELEASE_CHECKLIST`, and
`TWILL_CI_RELEASE_SCRIPT`, `TWILL_CI_COVERAGE_SCRIPT`,
`TWILL_CI_GO_MOD_TIDY_SCRIPT`, `TWILL_CI_WEBSITE_SCRIPT`,
`TWILL_CI_OPENAPI_DOCS`, `TWILL_CI_CLIENT_DOCS`, and
`TWILL_CI_CONTRACT_DOCS` for checking alternate metadata, documentation, and
maintenance-script files during local maintenance.
`./dev/verify_ci_metadata_selftest` exercises the CI metadata verifier's
positive and negative paths, including commented, quoted, and unpinned GitHub
Actions, release script, and issue-template drift, and is included in the
release smoke gate.
`./dev/verify_shell_scripts` runs shell syntax and executable-bit checks for
repository maintenance scripts, and rejects legacy patterns such as deprecated
extended-grep commands, Bash-only `command -v ... &>` redirection,
GNU-specific `mktemp --tmpdir`, and non-ASCII success markers. It is included
in the release smoke gate.
`./dev/verify_shell_scripts_selftest` exercises the shell verifier's positive,
space-containing script path, empty-directory, invalid-script, and
non-executable-script and legacy-pattern failure paths and is included in the
release smoke gate.
`./dev/verify_markdown_links` verifies repository-local file, image, and
heading-anchor references from inline links, inline images, reference
definitions, and explicit reference-style links and images in the primary
planning and docs Markdown files. Override
`TWILL_MARKDOWN_LINK_PATHS` with shell-style quoting when checking paths that
contain spaces. Fenced code blocks and inline code are ignored.
`./dev/verify_markdown_links_selftest` exercises its positive, missing-link,
missing-image, missing-anchor, duplicate-heading anchor, percent-encoded path,
angle-bracket path, quoted-path, parenthesized-path, reference-definition, and
missing reference-label and image reference-label paths plus code-span ignoring
and is included in the release smoke gate.
`./dev/verify_license_metadata` verifies `LICENSE`, `NOTICE`, and release
critical maintenance-script/GitHub metadata license headers. Override
`TWILL_LICENSE_HEADER_FILES` with shell-style quoting when checking header files
whose paths contain spaces.
`./dev/verify_license_metadata_selftest` exercises missing-provenance,
missing-header, and quoted-header-path paths and is included in the release
smoke gate.
`./dev/verify_static_analysis` runs `staticcheck` and `govulncheck` when those
tools are installed at the pinned CI versions: `staticcheck` v0.7.0 and
`govulncheck` v1.3.0. Set `TWILL_STATIC_ANALYSIS_ROOT` to run full analysis
from an alternate repository root.
Use `./dev/verify_static_analysis --check-tools` to validate the pinned tool
versions without running full analysis.
`./dev/verify_static_analysis_selftest` exercises the static-analysis verifier's
full-analysis invocation, alternate-root, tool-version, and missing-tool
failure paths and is included in the release smoke gate.
`./dev/verify_public_api` compiles public extension packages and runs
`dev/apidiff` against `HEAD` so extension-facing API drift is explicit. It
defaults to `./runtime/adapters ./runtime/deployers`; set
`TWILL_PUBLIC_API_PACKAGES` to check an alternate package list. Use
`./dev/verify_public_api --check-tools` to validate that `apidiff` is
installed, and `./dev/verify_public_api_selftest` to exercise tool detection
and comparison invocation.
`./dev/verify_generated_code` runs generated-code drift verification from a
clean worktree when `protoc`, `protoc-gen-go`, and `addlicense` are installed.
CI runs the full drift gate with `protoc` 31.1, `protoc-gen-go` v1.36.11, and
`addlicense` v1.2.0. Tool versions are centralized in
`dev/tool_versions.env`. `dev/protoc.sh` reads the same pins and rejects
unexpected `protoc --version` or `protoc-gen-go --version` output before
generating `.pb.go` files.
`dev/generate_coverage.sh` writes Twill-named coverage reports under
`/tmp/$USER-files`.
`./dev/verify_generated_code_selftest` exercises the generated-code verifier's
tool-version, missing-tool, clean-worktree, space-containing root and command
working directory, scoped root drift, dirty-worktree, and generated-drift paths
and is included in the release smoke gate.

`twill app components` reports generated component metadata and conservative
`twill.WithRouter` bindings from source files. It does not expose routing
methods, routing keys, or business logic.

`twill app config` reports components, conservative `twill.WithConfig` schemas
from source files, and safe binding metadata from `docs/config/*.md` when
present. Binding metadata exposes only component, config type, binding kind,
required flag, source file, and normalized provider/lifecycle markers. It does
not expose config field names, TOML keys, environment variable names, ConfigMap
names, Secret names, or config values. See
[docs/config.md](./docs/config.md).

`twill app endpoints` reports Twill listener surfaces, safe endpoint
declarations, and safe summaries from generated `docs/endpoints/*.md`
contracts. Endpoint declarations expose component, listener, method, path,
source file, protocol when known, gRPC service when known, safe request/response
type references, and normalized markers for auth, middleware, and compatibility
metadata. They do not expose raw auth
details, headers, query values, examples, request bodies, response bodies, or
free-form contract text.

`twill app protobuf` reports safe protobuf package, service, RPC, message type,
and source-file metadata from local `.proto` files. It does not expose message
fields, enum values, options, comments, examples, payloads, or custom
annotations. See [docs/protobuf_context.md](./docs/protobuf_context.md).

`runtime/middleware` provides opt-in HTTP middleware primitives for request IDs,
auth hooks, rate limiting, circuit breaking, retry idempotency guards, timeouts,
and structured JSON errors. `twill app middleware` reports safe references to
these primitives by name, category, inferred component, and source file without
exposing handler logic, header values, auth rules, or error details. Ordering
and failure semantics are documented in [docs/middleware.md](./docs/middleware.md).

`runtime/observability` provides opt-in local defaults for OpenTelemetry HTTP
tracing, runtime metric snapshots, and redacting structured logs. `twill app
observability` reports safe references to standard defaults by name, kind,
inferred component, and source file without exposing service names, handler
names, raw logs, trace payloads, metric values, or secret-bearing attributes.
The `generate_observability` MCP tool includes the same defaults evidence in
its dry-run documentation.

`twill app resources` combines listener surfaces, conservative backing type
hints, and optional `docs/resources/*.md` declarations. Resource declarations
model SQL/database, Redis/cache, Pub/Sub/topic/subscription/queue, cron, and
object-storage ownership with lifecycle and deployment binding metadata while
excluding connection strings, provider resource values, secret names, schedules,
and free-form notes. See [docs/resources.md](./docs/resources.md).

`twill deploy compose` maps safe database, cache, pub/sub/queue, and object
storage resource metadata to dry-run `docker-compose.twill.yaml` and
`docker-compose.twill.env.example` proposals for local development. It emits
environment variable references and placeholder examples for local credentials,
and skips runtime-owned listeners and cron jobs. See
[docs/local_compose.md](./docs/local_compose.md).

The existing `twill single dashboard`, `twill multi dashboard`, and `twill ssh
dashboard` commands include a local app context view at `/app` and a JSON data
endpoint at `/app/data`. The view uses the same typed context providers as
`twill app` and MCP to show safe service graph, component graph, API, config,
middleware, resource, protobuf, client SDK, contract-test, local Compose,
observability, deployment, and test summaries, including gRPC client SDK RPC
operations and RPC contract-test cases. Both `/app` and `/app/data` surface
read-only safety evidence, including file-write and environment-write markers;
observability summaries include safe trace/log component coverage, runtime
metric signal names, and telemetry boundary notes. Use `--app-dir PATH` and
`--app-packages PATTERNS` when serving the dashboard from outside the application
directory.

Existing `net/http` services can be adapted incrementally with
`twill.HTTPAdapter` metadata fields and metadata-backed `runtime/adapters`
route helpers. A
component declares adapter metadata with a tag such as
`twill:"listener=public,method=GET,path=/legacy"`, keeps serving its existing
`http.Handler`, and exposes the route through safe app endpoint context as
`kind: adapter` and `protocol: http`; runtime mounting can read the generated
method/path metadata with `adapters.RoutesFor`. Existing gRPC unary methods can
be tracked with `twill.GRPCAdapter` metadata such as
`twill:"listener=grpc,service=profiles.v1.ProfileService,method=GetProfile"`;
they appear in endpoint context as `kind: adapter`, `protocol: grpc`, service,
method, and canonical RPC path, and runtime registration can read the generated
service/method metadata with `adapters.GRPCMethodsFor` without adding a grpc
runtime dependency. See
[docs/http_adapters.md](./docs/http_adapters.md) and
[docs/grpc_adapters.md](./docs/grpc_adapters.md).

`twill app deployment` reports read-only deployment context, including the
native `twill deploy k8s` dry-run Kubernetes resources, rollout strategy,
health check path, AWS EKS dry-run metadata, and verification commands.
`twill deploy k8s --write-dir <dir>` writes `reviewed-manifests.yaml` under the
requested directory after conflict checks, but still does not read kubeconfig,
contact a cluster, apply manifests, or mutate environments.
Pass `--ingress-class <class>` to set `spec.ingressClassName` on the generated
Kubernetes `Ingress` while keeping deployment planning dry-run and offline.
Pass `--ingress-host <host>` to set `spec.rules[].host` on generated dry-run
Ingress rules, including AWS embedded Kubernetes ingress rules, without DNS,
cloud API, kubeconfig, or API-server access.
Pass `--health-path <path>` to review non-default readiness and liveness probe
paths in generated Kubernetes manifests and AWS embedded Kubernetes manifests.
Pass `--replicas <n>` and `--max-replicas <n>` to review non-default
Deployment replica counts and HorizontalPodAutoscaler bounds in Kubernetes and
AWS dry-run plans.
Pass `--cpu-request`, `--memory-request`, `--cpu-limit`, and `--memory-limit`
to review non-default container resource requests and limits in Kubernetes and
AWS embedded Kubernetes manifests without contacting a cluster.
Kubernetes and AWS dry-run ingress paths prefer safe HTTP endpoint metadata
when available, falling back to listener-derived paths for listeners without
declared endpoint paths. OpenAPI-style templated paths such as `/profiles/{id}`
are converted to Kubernetes prefix paths such as `/profiles`.
`twill deploy aws` adds ECR, EKS cluster context, IAM/IRSA, and ALB/Gateway
metadata on top of the Kubernetes planner. `twill deploy aws --write-dir <dir>`
writes `reviewed-aws-eks-manifests.yaml` under the requested directory after
conflict checks; the file is an AWS/EKS review bundle, not an apply step.
These commands do not read AWS config, kubeconfig, cluster state, pods, events,
ConfigMaps, Secrets, or live manifests. Generated verification commands stay
offline: they re-run Twill dry-run/context exports and explicit review-bundle
generation rather than querying AWS or a Kubernetes API server. The
`runtime/adapters` contains the public incremental migration helpers for
HTTP/gRPC adapter metadata and route/service mounting. `runtime/deployers`
contains the initial target-agnostic dry-run planning interface used by the
native Kubernetes and AWS planners.
Planner authors can use `deployers.ValidateDryRunPlan` in tests or command
adapters to keep review plans dry-run, non-writing, and structurally
reviewable. See
[docs/aws_deploy.md](./docs/aws_deploy.md).

Project-specific AI/tool policy rules can be added in `.twill/policy/rules.json`.
`twill skill init` creates the project-local skill pack and an empty policy
template:

```json
{
  "schema_version": "twill.policy.rules.v1",
  "rules": [
    {
      "id": "project.production.review",
      "title": "Project production review",
      "applies_to": ["deployments"],
      "requirement": "Production changes require project owner review.",
      "enforcement": "Planning tools must surface this project policy."
    }
  ]
}
```

The policy file is structured metadata only and is limited to 256 KiB.

Visit the project repository to learn more about Twill.

## Installation and Getting Started

See the documentation in this repository for installation instructions and
information on getting started.

## Contributing

Please read our [contribution guide](./CONTRIBUTING.md) for details on how
to contribute.

[website]: https://github.com/nxsky/twill
[docs]: ./website/docs.md
