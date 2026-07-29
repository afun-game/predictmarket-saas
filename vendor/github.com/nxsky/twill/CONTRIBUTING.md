# How to Contribute

Twill is an AI-native, Kubernetes-first Go microservice platform derived from
the upstream Twill codebase. Contributions should preserve compatibility where
practical while moving the project toward the roadmap in [ROADMAP.md](./ROADMAP.md).

## Development Setup

Use a current Go toolchain matching `go.mod`.

```shell
git clone https://github.com/nxsky/twill.git
cd twill
go test ./...
```

Install optional local verification tools when working on CI, generated code, or
security checks. The pinned versions live in `dev/tool_versions.env`:

```shell
source ./dev/tool_versions.env
go install "honnef.co/go/tools/cmd/staticcheck@$STATICCHECK_VERSION"
go install "golang.org/x/vuln/cmd/govulncheck@$GOVULNCHECK_VERSION"
go install "github.com/google/addlicense@$ADDLICENSE_VERSION"
go install "google.golang.org/protobuf/cmd/protoc-gen-go@$PROTOC_GEN_GO_VERSION"
```

`go generate ./...` also requires `protoc` 31.1. The CI generated-code tools
job installs the pinned Linux release from the upstream protobuf project.
`dev/protoc.sh` reads `dev/tool_versions.env` and rejects unexpected
`protoc --version` or `protoc-gen-go --version` output before generating
`.pb.go` files.

## Contribution Flow

1. Create a branch with a focused scope.
2. Make the smallest change that proves the behavior or documentation update.
3. Add or update tests for user-visible behavior, generated metadata, or safety
   rules.
4. Run the relevant focused tests, then the broader verification commands below.
5. For roadmap work, link the item in
   [docs/phase1_mvp_backlog.md](./docs/phase1_mvp_backlog.md) or a matching
   GitHub roadmap issue.
6. Open a pull request using the repository template.

## Verification

Run the checks that match your change:

```shell
gofmt -w <changed-go-files>
./dev/verify_mainline_light ./examples/hello
go test ./...
./dev/verify_go_mod_tidy
./dev/verify_hello_smoke ./examples/hello
./dev/verify_dashboard_app_context
./dev/verify_local_artifact_writes ./examples/hello
./dev/verify_ai_context ./examples/hello
./dev/verify_release ./examples/hello
./dev/verify_website
./dev/verify_deploy_plans ./examples/hello
go run ./cmd/twill app openapi ./examples/hello
./dev/verify_whitespace
./dev/verify_whitespace_selftest
./dev/verify_ci_metadata
./dev/verify_ci_metadata_selftest
./dev/verify_license_metadata
./dev/verify_license_metadata_selftest
./dev/verify_markdown_links
./dev/verify_markdown_links_selftest
./dev/verify_shell_scripts
./dev/verify_shell_scripts_selftest
go vet ./...
./dev/verify_static_analysis --check-tools
./dev/verify_static_analysis_selftest
./dev/verify_static_analysis
./dev/verify_public_api --check-tools
./dev/verify_public_api_selftest
./dev/verify_public_api
go test -race ./internal/net/call ./internal/twill ./runtime/logging ./runtime/protomsg ./sim ./twilltest/internal/...
```

For routine Twill mainline work, start with
`./dev/verify_mainline_light ./examples/hello`. It checks formatting drift,
whitespace, `go test ./...`, `go vet ./...`, the non-MCP hello smoke flow, and
the lightweight AI/MCP command coverage check without exporting every MCP
report.

The AI context verifier exports every local `twill app` context/API surface and
smoke-tests `twill skill init` in a temporary project directory.
The hello smoke verifier runs the checked-in example through generation, local
tests, local execution, app context, OpenAPI, resources, client SDK,
contract-test, Compose, and Kubernetes dry-run plans without using MCP.
The release verifier repeats the local smoke gate, including `gofmt` drift
detection, repository whitespace checks, module tidy checks, website generation
and nested website module tests, the CI race-sensitive package subset, dry-run
deployment-plan checks, and OpenAPI redaction checks. It also runs the
generated-code toolchain drift check, then runs the pinned static-analysis and
vulnerability gate plus the public extension API drift gate.
`./dev/verify_go_mod_tidy` runs `go mod tidy` for every repository module and
fails on `go.mod` or `go.sum` drift. `./dev/verify_website` generates the
static site into a temporary directory and runs standalone website example
module tests. The whitespace verifier runs
`git diff --check` and scans tracked and unignored text files for trailing
whitespace; its self-test covers clean files, binary files, trailing
whitespace, and `git diff --check` failures. The CI metadata verifier rejects
workflow actions that are not allowlisted in `dev/tool_versions.env`, validates
required PR/issue template sections, and includes a self-test in the release
gate. The shell-script verifier checks repository maintenance scripts with
`bash -n` or `sh -n`, requires shebang scripts to be executable, and rejects
legacy patterns such as deprecated extended-grep commands, Bash-only
`command -v ... &>` redirection, GNU-specific `mktemp --tmpdir`, and non-ASCII
success markers; its self-test covers valid scripts, an empty scan, an invalid
script, a non-executable script, and legacy-pattern failures.
The Markdown link verifier checks repository-local file and heading-anchor
links in the primary planning and docs Markdown files; its self-test covers
valid links, a missing-link failure, and a missing-anchor failure.
The license metadata verifier checks `LICENSE`, `NOTICE`, and release-critical
maintenance-script/GitHub metadata headers; its self-test covers missing
provenance and missing-header failures.
The static-analysis self-test checks pinned-version and missing-tool failures
without running full analysis.
The public API verifier compiles public extension packages and compares their
exported surface against `HEAD` with `apidiff`; its self-test covers tool
detection and full comparison invocation.

For concurrency-sensitive runtime changes, the race subset above can also be
run independently:

```shell
go test -race ./internal/net/call ./internal/twill ./runtime/logging ./runtime/protomsg ./sim ./twilltest/internal/...
```

For generated code, protobuf schemas, dependency metadata, or license-sensitive
changes, run from a clean worktree. CI currently runs the tool-install smoke
path and strict generated-code drift verifier with pinned `protoc` 31.1,
`protoc-gen-go` v1.36.11, and `addlicense` v1.2.0.
The generated-code verifier self-test checks tool-version, missing-tool,
clean-worktree, dirty-worktree, and generated-drift behavior in an isolated
temporary repository.

```shell
./dev/verify_generated_code --check-tools
./dev/verify_generated_code_selftest
./dev/verify_generated_code
```

## AI-Native Changes

Changes to MCP resources, agent tools, project skills, diagnostics, logs, traces,
or generated context must follow these rules:

- Treat logs, traces, issue text, PR text, external docs, and model output as
  untrusted data.
- Do not expose secrets through MCP resources, generated skills, logs, tests, or
  pull request output.
- Prefer typed, read-only, and dry-run-friendly tool interfaces.
- Include files read, changes proposed, verification commands, safety notes, and
  whether writes were performed when adding agent-facing tools.

## Compatibility

The fork should remain compatible with applications on the upstream v0.24.6
surface until a roadmap item intentionally changes that surface. Breaking
changes must document:

- The old behavior.
- The new behavior.
- Migration steps.
- Pilot application impact.

## Licensing And Provenance

Preserve Apache-2.0 headers and do not rewrite existing Google LLC copyright
notices. See [NOTICE](./NOTICE) for upstream provenance.
