// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package app implements application inspection commands.
package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/nxsky/twill/runtime/observability"
	"github.com/nxsky/twill/runtime/tool"
)

// Commands are the app inspection subcommands.
var Commands = map[string]*tool.Command{
	"backstage":      backstageCommand(),
	"client":         clientCommand(),
	"ci":             ciCommand(),
	"cloud":          cloudCommand(),
	"compliance":     complianceCommand(),
	"compose":        composeCommand(),
	"components":     componentsCommand(),
	"config":         configCommand(),
	"context":        contextCommand(),
	"contract-tests": contractTestsCommand(),
	"deployment":     deploymentCommand(),
	"endpoints":      endpointsCommand(),
	"generated":      generatedCommand(),
	"graph":          graphCommand(),
	"identity":       identityCommand(),
	"infra":          infraCommand(),
	"middleware":     middlewareCommand(),
	"migration":      migrationCommand(),
	"approval":       approvalCommand(),
	"observability":  observabilityCommand(),
	"obs-config":     obsConfigCommand(),
	"openapi":        openAPICommand(),
	"packaging":      packagingCommand(),
	"policy":         policyCommand(),
	"protobuf":       protobufCommand(),
	"recommendation": recommendationCommand(),
	"resources":      resourcesCommand(),
	"security":       securityCommand(),
	"template":       templateCommand(),
	"tests":          testsCommand(),
	"version":        versionCommand(),
}

func protobufCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("protobuf")

	return &tool.Command{
		Name:        "protobuf",
		Flags:       flags,
		Description: "Export safe protobuf contract metadata",
		Help: `Usage:
  twill app protobuf [--dir DIR] [packages...]

Description:
  "twill app protobuf" scans local .proto files and emits package, service,
  RPC, message type, and source-file metadata for local AI agents and CI
  checks. It does not expose message fields, comments, examples, payloads, or
  custom options.

  If a single local package directory is provided, protobuf scanning is rooted
  at that application directory. Otherwise scanning is rooted at --dir.

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			protobuf, err := InspectProtobufContext(ctx, GraphOptions{
				Dir:      *dir,
				Patterns: args,
			})
			if err != nil {
				return err
			}
			return encodeJSON(os.Stdout, protobuf, !*compact, "protobuf")
		},
	}
}

func composeCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("compose")
	project := flags.String("project", "twill-local", "Docker Compose project name")
	writeDir := flags.String("write-dir", "", "Write generated Docker Compose files under this directory after conflict checks")

	return &tool.Command{
		Name:        "compose",
		Flags:       flags,
		Description: "Generate a dry-run Docker Compose plan for local dependencies",
		Help: `Usage:
  twill app compose [--dir DIR] [--project NAME] [packages...]

	Description:
  "twill app compose" scans safe resource metadata and emits a deterministic
  dry-run Docker Compose plan for local dependent infrastructure. It proposes
  a docker-compose.twill.yaml file. By default it is dry-run and does not run
  Docker or write files. Pass --write-dir to write proposed files under that
  directory after conflict checks.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app compose ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			plan, err := InspectLocalCompose(ctx, GraphOptions{
				Dir:      *dir,
				Patterns: args,
			}, LocalComposeOptions{
				Project: *project,
			})
			if err != nil {
				return err
			}
			if *writeDir != "" {
				if err := WriteLocalComposePlan(plan, *writeDir); err != nil {
					return err
				}
			}
			return encodeJSON(os.Stdout, plan, !*compact, "compose")
		},
	}
}

func contractTestsCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("contract-tests")
	writeDir := flags.String("write-dir", "", "Write generated contract-test files under this directory after conflict checks")

	return &tool.Command{
		Name:        "contract-tests",
		Flags:       flags,
		Description: "Generate dry-run endpoint contract tests from Twill metadata",
		Help: `Usage:
  twill app contract-tests [--dir DIR] [--write-dir DIR] [packages...]

Description:
  "twill app contract-tests" scans safe endpoint metadata and emits a
  deterministic contract-test plan as JSON. By default it is dry-run and
  proposes Go HTTP contract tests plus guarded gRPC adapter stubs when gRPC
  adapter metadata is present. Pass --write-dir to write proposed files under
  that directory after conflict checks.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app contract-tests ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			plan, err := InspectContractTests(ctx, GraphOptions{
				Dir:      *dir,
				Patterns: args,
			})
			if err != nil {
				return err
			}
			if *writeDir != "" {
				if err := WriteContractTestsPlan(plan, *writeDir); err != nil {
					return err
				}
			}
			return encodeJSON(os.Stdout, plan, !*compact, "contract-tests")
		},
	}
}

func clientCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("client")
	language := flags.String("lang", "go", "Client SDK language: go or typescript")
	packageName := flags.String("package", "client", "Go package name for generated client files")
	writeDir := flags.String("write-dir", "", "Write generated client SDK files under this directory after conflict checks")

	return &tool.Command{
		Name:        "client",
		Flags:       flags,
		Description: "Generate a dry-run client SDK from Twill endpoint metadata",
		Help: `Usage:
  twill app client [--dir DIR] [--lang go|typescript] [--package NAME] [--write-dir DIR] [packages...]

Description:
  "twill app client" scans safe endpoint metadata and emits a deterministic
  client SDK plan as JSON. By default it is dry-run and only proposes generated
  files. Pass --write-dir to write proposed files under that directory after
  conflict checks.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app client --lang go ./...
    twill app client --lang typescript ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			plan, err := InspectClientSDK(ctx, GraphOptions{
				Dir:      *dir,
				Patterns: args,
			}, ClientSDKOptions{
				Language:    *language,
				PackageName: *packageName,
			})
			if err != nil {
				return err
			}
			if *writeDir != "" {
				if err := WriteClientSDKPlan(plan, *writeDir); err != nil {
					return err
				}
			}
			return encodeJSON(os.Stdout, plan, !*compact, "client")
		},
	}
}

func contextCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("context")

	return &tool.Command{
		Name:        "context",
		Flags:       flags,
		Description: "Export the local Twill AI context pack",
		Help: `Usage:
  twill app context [--dir DIR] [packages...]

Description:
  "twill app context" scans generated twill_gen.go files and package test
  metadata, then emits a read-only JSON context pack for local AI agents and
  CI checks. The pack includes graph, components, endpoint-adjacent listener
  metadata, middleware evidence, config context, policy rules, generated files,
  test inventory, safety notes, and verification commands.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app context ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			pack, err := InspectContextPack(ctx, GraphOptions{
				Dir:      *dir,
				Patterns: args,
			})
			if err != nil {
				return err
			}
			return encodeJSON(os.Stdout, pack, !*compact, "context")
		},
	}
}

func componentsCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("components")

	return &tool.Command{
		Name:        "components",
		Flags:       flags,
		Description: "Export static Twill component context",
		Help: `Usage:
  twill app components [--dir DIR] [packages...]

Description:
  "twill app components" scans generated twill_gen.go files and emits static
  component and dependency edge context for local AI agents and CI checks.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app components ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			components, err := InspectComponentsContext(ctx, GraphOptions{
				Dir:      *dir,
				Patterns: args,
			})
			if err != nil {
				return err
			}
			return encodeJSON(os.Stdout, components, !*compact, "components")
		},
	}
}

func endpointsCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("endpoints")

	return &tool.Command{
		Name:        "endpoints",
		Flags:       flags,
		Description: "Export endpoint-adjacent Twill listener context",
		Help: `Usage:
  twill app endpoints [--dir DIR] [packages...]

Description:
  "twill app endpoints" scans generated twill_gen.go files, safe endpoint
  contracts, and conservative source-level net/http route declarations. It
  emits endpoint-adjacent listener context with explicit limitations for
  endpoint declarations, schemas, auth metadata, and middleware.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app endpoints ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			endpoints, err := InspectEndpointsContext(ctx, GraphOptions{
				Dir:      *dir,
				Patterns: args,
			})
			if err != nil {
				return err
			}
			return encodeJSON(os.Stdout, endpoints, !*compact, "endpoints")
		},
	}
}

func openAPICommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("openapi")
	validate := flags.Bool("validate", false, "Validate the generated OpenAPI document against endpoint contracts")

	return &tool.Command{
		Name:        "openapi",
		Flags:       flags,
		Description: "Export a minimal OpenAPI document from Twill endpoint metadata",
		Help: `Usage:
  twill app openapi [--dir DIR] [--validate] [packages...]

Description:
  "twill app openapi" scans safe HTTP endpoint metadata and emits a
  deterministic OpenAPI JSON document. The export includes method, path,
  component, listener, and source file metadata only; request schemas, response
  schemas, auth details, middleware, and examples are not exposed.

  Pass --validate to compare the generated OpenAPI paths and operations against
  endpoint contracts in docs/endpoints/*.md. The result is included under
  x-twill-validation in the exported document. When no contracts are present,
  validation is omitted.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app openapi ./...
    twill app openapi --validate ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			var openapi *OpenAPIDocument
			var err error
			if *validate {
				openapi, err = InspectOpenAPIWithValidation(ctx, GraphOptions{
					Dir:      *dir,
					Patterns: args,
				})
			} else {
				openapi, err = InspectOpenAPI(ctx, GraphOptions{
					Dir:      *dir,
					Patterns: args,
				})
			}
			if err != nil {
				return err
			}
			return encodeJSON(os.Stdout, openapi, !*compact, "openapi")
		},
	}
}

func graphCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("graph")
	writeDir := flags.String("write-dir", "", "Write the exported graph under this directory after conflict checks")

	return &tool.Command{
		Name:        "graph",
		Flags:       flags,
		Description: "Export the static Twill application graph",
		Help: `Usage:
  twill app graph [--dir DIR] [--write-dir DIR] [packages...]

Description:
  "twill app graph" scans generated twill_gen.go files and emits a JSON graph
  containing packages, components, component dependency edges, listeners, and
  generated metadata files. By default it is dry-run and does not write files.
  Pass --write-dir to write app-graph.json under that directory after conflict
  checks.

  The exported graph is the foundation for component context, endpoint
  context, deployment context, and MCP resources. Keep it deterministic and
  free of secret or runtime data.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app graph ./...
    twill app graph --write-dir ./review ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			graph, err := InspectGraph(ctx, GraphOptions{
				Dir:      *dir,
				Patterns: args,
			})
			if err != nil {
				return err
			}
			if *writeDir != "" {
				if err := writeGraph(graph, *writeDir); err != nil {
					return err
				}
			}
			return encodeJSON(os.Stdout, graph, !*compact, "graph")
		},
	}
}

func middlewareCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("middleware")

	return &tool.Command{
		Name:        "middleware",
		Flags:       flags,
		Description: "Export standard Twill middleware context",
		Help: `Usage:
  twill app middleware [--dir DIR] [packages...]

Description:
  "twill app middleware" scans source files for references to the standard
  github.com/nxsky/twill/runtime/middleware package and emits safe middleware
  evidence for local AI agents and CI checks. It reports middleware names,
  categories, source files, and inferred component ownership only.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app middleware ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			middleware, err := InspectMiddlewareContext(ctx, GraphOptions{
				Dir:      *dir,
				Patterns: args,
			})
			if err != nil {
				return err
			}
			return encodeJSON(os.Stdout, middleware, !*compact, "middleware")
		},
	}
}

func configCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("config")

	return &tool.Command{
		Name:        "config",
		Flags:       flags,
		Description: "Export config-safe Twill context",
		Help: `Usage:
  twill app config [--dir DIR] [packages...]

Description:
  "twill app config" scans generated twill_gen.go files and emits config-safe
  component context. It does not read or expose config values.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app config ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			config, err := InspectConfigContext(ctx, GraphOptions{
				Dir:      *dir,
				Patterns: args,
			})
			if err != nil {
				return err
			}
			return encodeJSON(os.Stdout, config, !*compact, "config")
		},
	}
}

func generatedCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("generated")

	return &tool.Command{
		Name:        "generated",
		Flags:       flags,
		Description: "Export generated Twill metadata files",
		Help: `Usage:
  twill app generated [--dir DIR] [packages...]

Description:
  "twill app generated" scans generated twill_gen.go files and emits the
  generated metadata file list used by local app inspection.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app generated ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			generated, err := InspectGeneratedContext(ctx, GraphOptions{
				Dir:      *dir,
				Patterns: args,
			})
			if err != nil {
				return err
			}
			return encodeJSON(os.Stdout, generated, !*compact, "generated")
		},
	}
}

func observabilityCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("observability")

	return &tool.Command{
		Name:        "observability",
		Flags:       flags,
		Description: "Export read-only Twill observability context",
		Help: `Usage:
  twill app observability [--dir DIR] [packages...]

Description:
  "twill app observability" scans generated twill_gen.go files and emits
  read-only traces, logs, and metrics context with explicit live-backend
  limitations.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app observability ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			observability, err := InspectObservabilityContext(ctx, GraphOptions{
				Dir:      *dir,
				Patterns: args,
			})
			if err != nil {
				return err
			}
			return encodeJSON(os.Stdout, observability, !*compact, "observability")
		},
	}
}

func deploymentCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("deployment")

	return &tool.Command{
		Name:        "deployment",
		Flags:       flags,
		Description: "Export read-only Twill deployment context",
		Help: `Usage:
  twill app deployment [--dir DIR] [packages...]

Description:
  "twill app deployment" scans generated twill_gen.go files and emits
  read-only deployment status, Kubernetes context, and AWS EKS dry-run context
  with explicit live-backend limitations.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app deployment ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			deployment, err := InspectDeploymentContext(ctx, GraphOptions{
				Dir:      *dir,
				Patterns: args,
			})
			if err != nil {
				return err
			}
			return encodeJSON(os.Stdout, deployment, !*compact, "deployment")
		},
	}
}

func policyCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("policy")

	return &tool.Command{
		Name:        "policy",
		Flags:       flags,
		Description: "Export baseline Twill AI/tool policy rules",
		Help: `Usage:
  twill app policy [--dir DIR]

Description:
  "twill app policy" emits baseline read-only AI/tool safety policy rules plus
  optional project rules from .twill/policy/rules.json.

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(_ context.Context, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("policy accepts no package patterns")
			}
			policyRules, err := InspectPolicyRules(PolicyOptions{Dir: *dir})
			if err != nil {
				return err
			}
			return encodeJSON(os.Stdout, policyRules, !*compact, "policy")
		},
	}
}

func resourcesCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("resources")

	return &tool.Command{
		Name:        "resources",
		Flags:       flags,
		Description: "Export the safe Twill resource context",
		Help: `Usage:
  twill app resources [--dir DIR] [packages...]

Description:
  "twill app resources" emits a JSON resource context that is safe for local AI
  agents and CI checks. It combines listener surfaces from generated metadata,
  whitelisted declarations from docs/resources/*.md, and conservative
  source-level hints for known database, cache, pub/sub, queue, cron, and object
  storage client types. It does not expose field names, config keys, secret
  names, connection strings, credentials, binding values, or free-form
  declaration text.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app resources ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			resources, err := InspectResourcesContext(ctx, GraphOptions{
				Dir:      *dir,
				Patterns: args,
			})
			if err != nil {
				return err
			}
			return encodeJSON(os.Stdout, resources, !*compact, "resources")
		},
	}
}

func testsCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("tests")

	return &tool.Command{
		Name:        "tests",
		Flags:       flags,
		Description: "Export the static Twill test inventory",
		Help: `Usage:
  twill app tests [--dir DIR] [packages...]

Description:
  "twill app tests" scans package test metadata and emits a JSON inventory of
  package-local and external Go test files, discovered test/fuzz/benchmark
  functions, conservative package-level component test hints, static strategy
  signals, and existing local Go coverage profile summaries when present.

  Coverage summaries are read from common local coverage profile filenames
  such as coverage.out and cover.out. This command does not run tests or write
  coverage files.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app tests ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			tests, err := InspectTests(ctx, GraphOptions{
				Dir:      *dir,
				Patterns: args,
			})
			if err != nil {
				return err
			}
			return encodeJSON(os.Stdout, tests, !*compact, "tests")
		},
	}
}

func inspectFlagSet(name string) (*flag.FlagSet, *bool, *string) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	compact := flags.Bool("compact", false, "Emit compact JSON")
	dir := flags.String("dir", "", "Directory to inspect")
	return flags, compact, dir
}

func encodeJSON(out io.Writer, value any, pretty bool, label string) error {
	enc := json.NewEncoder(out)
	if pretty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(value); err != nil {
		return fmt.Errorf("encode %s: %w", label, err)
	}
	return nil
}

func obsConfigCommand() *tool.Command {
	flags := flag.NewFlagSet("obs-config", flag.ContinueOnError)
	compact := flags.Bool("compact", false, "Emit compact JSON")

	return &tool.Command{
		Name:        "obs-config",
		Flags:       flags,
		Description: "Show runtime observability configuration from environment",
		Help: `Usage:
  twill app obs-config

Description:
  "twill app obs-config" reads environment variables and shows the effective
  observability configuration for traces, metrics, and logs. It documents
  all supported environment variables and their current values.

  This command does not inspect source code; it reports the runtime
  configuration that would be applied when the application starts.

Environment Variables:
  TWILL_TRACE_EXPORTER       Trace exporter: stdout, none, or registered provider
  OTEL_EXPORTER_OTLP_ENDPOINT  OTLP endpoint URL (e.g., http://jaeger:4318)
  OTEL_TRACES_SAMPLER        Trace sampler: always_on, always_off, traceidratio
  OTEL_TRACES_SAMPLER_ARG    Sample rate for ratio sampler (0.0-1.0)
  TWILL_LOG_LEVEL            Log level: debug, info, warn, error
  TWILL_LOG_FORMAT           Log format: json, text
  TWILL_CONFIG_DIR           File-based config directory (K8s ConfigMap)
  TWILL_SECRET_DIR           File-based secrets directory (K8s Secret)

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			report := observability.GenerateConfigReport()
			return encodeJSON(os.Stdout, report, !*compact, "obs-config")
		},
	}
}
