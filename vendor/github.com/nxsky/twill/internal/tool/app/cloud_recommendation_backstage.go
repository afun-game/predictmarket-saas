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

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nxsky/twill/runtime/backstage"
	"github.com/nxsky/twill/runtime/cloud"
	"github.com/nxsky/twill/runtime/recommendation"
	"github.com/nxsky/twill/runtime/tool"
)

func cloudCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("cloud")
	provider := flags.String("provider", "", "Filter to a single provider: aws, gcp, or azure; omit for all providers")
	terraform := flags.Bool("terraform", false, "Render as Terraform HCL instead of JSON")
	output := flags.String("output", "", "Write generated Terraform files under this directory")

	return &tool.Command{
		Name:        "cloud",
		Flags:       flags,
		Description: "Generate cloud infrastructure specs and Terraform for AWS, GCP, and Azure",
		Help: `Usage:
  twill app cloud [--dir DIR] [--provider PROVIDER] [--terraform] [--output DIR] [packages...]

Description:
  "twill app cloud" scans safe resource metadata and generates cloud-specific
  infrastructure specs for AWS, GCP, and Azure. By default, specs for all
  providers are emitted as JSON. Pass --provider to filter to a single
  provider. Pass --terraform to render as Terraform HCL. Pass --output to
  write Terraform files to disk.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app cloud ./...
    twill app cloud --provider aws --terraform ./...
    twill app cloud --terraform --output ./terraform ./...

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

			kinds := extractResourceKinds(resources)
			if len(kinds) == 0 {
				return encodeJSON(os.Stdout, map[string]any{
					"schema_version": "twill.cloud.spec.v1",
					"providers":      map[string][]any{},
					"limitations": []string{
						"No resource declarations found. Add resource imports (e.g., database, cache) to generate cloud specs.",
					},
				}, !*compact, "cloud")
			}

			specs := cloud.GenerateMultiCloud(kinds)

			if *terraform {
				hcl := renderCloudTerraform(specs, *provider)
				if *output != "" {
					if err := writeCloudTerraform(specs, *provider, *output); err != nil {
						return err
					}
				}
				fmt.Fprint(os.Stdout, hcl)
				return nil
			}

			if *provider != "" {
				p := cloud.Provider(*provider)
				if _, ok := specs[p]; !ok {
					return errInvalidCloudProvider(*provider)
				}
				return encodeJSON(os.Stdout, specs[p], !*compact, "cloud")
			}

			return encodeJSON(os.Stdout, specs, !*compact, "cloud")
		},
	}
}

func recommendationCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("recommendation")
	cpuRequest := flags.String("cpu-request", "", "Current CPU request (e.g., 100m)")
	memoryRequest := flags.String("memory-request", "", "Current memory request (e.g., 128Mi)")
	cpuLimit := flags.String("cpu-limit", "", "Current CPU limit (e.g., 500m)")
	memoryLimit := flags.String("memory-limit", "", "Current memory limit (e.g., 512Mi)")
	replicas := flags.Int("replicas", 0, "Current desired replica count")
	maxReplicas := flags.Int("max-replicas", 0, "Current HPA maximum replica count")
	sloTarget := flags.Float64("slo-target", 0.999, "SLO availability target (e.g., 0.999 for 99.9%)")
	avgCPU := flags.Float64("avg-cpu-utilization", 0, "Average CPU utilization percentage (0-100)")
	avgMemory := flags.Float64("avg-memory-utilization", 0, "Average memory utilization percentage (0-100)")
	p99Latency := flags.Float64("p99-latency-ms", 0, "Current p99 latency in milliseconds")
	sloLatencyTarget := flags.Float64("slo-latency-target-ms", 0, "SLO latency target in milliseconds")
	errorRate := flags.Float64("error-rate", 0, "Current error rate as a fraction (0-1)")
	sloErrorTarget := flags.Float64("slo-error-rate-target", 0, "SLO error rate target as a fraction")
	useDeployCtx := flags.Bool("from-deployment", false, "Auto-populate resource settings from the deployment dry-run plan")

	return &tool.Command{
		Name:        "recommendation",
		Flags:       flags,
		Description: "Generate resource right-sizing recommendations from utilization metrics and SLO targets",
		Help: `Usage:
  twill app recommendation [--cpu-request QTY] [--memory-request QTY]
                           [--cpu-limit QTY] [--memory-limit QTY]
                           [--replicas N] [--max-replicas N]
                           [--slo-target TARGET] [--avg-cpu-utilization PCT]
                           [--avg-memory-utilization PCT]
                           [--p99-latency-ms MS] [--slo-latency-target-ms MS]
                           [--error-rate RATE] [--slo-error-rate-target RATE]
                           [--from-deployment] [packages...]

Description:
  "twill app recommendation" analyzes current resource settings and
  utilization metrics against SLO targets to produce right-sizing
  recommendations for CPU, memory, and replica counts. Pass
  --from-deployment to auto-populate resource settings from the dry-run
  deployment plan instead of specifying them manually.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app recommendation --from-deployment --avg-cpu-utilization 45 ./...
    twill app recommendation --replicas 3 --slo-target 0.999 --avg-cpu-utilization 80 ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			input := recommendation.Input{
				CPURequest:           *cpuRequest,
				MemoryRequest:        *memoryRequest,
				CPULimit:             *cpuLimit,
				MemoryLimit:          *memoryLimit,
				Replicas:             *replicas,
				MaxReplicas:          *maxReplicas,
				SLOTarget:            *sloTarget,
				AvgCPUUtilization:    *avgCPU,
				AvgMemoryUtilization: *avgMemory,
				P99LatencyMs:         *p99Latency,
				SLOLatencyTargetMs:   *sloLatencyTarget,
				ErrorRate:            *errorRate,
				SLOErrorRateTarget:   *sloErrorTarget,
			}

			if *useDeployCtx {
				deployCtx, err := InspectDeploymentContext(ctx, GraphOptions{
					Dir:      *dir,
					Patterns: args,
				})
				if err != nil {
					return err
				}
				input.CPURequest = deployCtx.Kubernetes.Rollout.ResourceRequirements.CPURequest
				input.MemoryRequest = deployCtx.Kubernetes.Rollout.ResourceRequirements.MemoryRequest
				input.CPULimit = deployCtx.Kubernetes.Rollout.ResourceRequirements.CPULimit
				input.MemoryLimit = deployCtx.Kubernetes.Rollout.ResourceRequirements.MemoryLimit
				input.Replicas = deployCtx.Kubernetes.Rollout.Replicas
				input.MaxReplicas = deployCtx.Kubernetes.Rollout.MaxReplicas
			}

			engine := recommendation.NewEngine()
			recs := engine.Recommend(input)

			return encodeJSON(os.Stdout, recs, !*compact, "recommendation")
		},
	}
}

func backstageCommand() *tool.Command {
	flags, compact, dir := inspectFlagSet("backstage")
	backendURL := flags.String("backend-url", "http://localhost:8080", "Twill console backend URL for the Backstage plugin")
	owner := flags.String("owner", "platform-team", "Default owner for Backstage component descriptors")
	output := flags.String("output", "", "Write Backstage plugin files under this directory")

	return &tool.Command{
		Name:        "backstage",
		Flags:       flags,
		Description: "Generate Backstage plugin configuration and catalog descriptors",
		Help: `Usage:
  twill app backstage [--dir DIR] [--backend-url URL] [--owner TEAM]
                      [--output DIR] [packages...]

Description:
  "twill app backstage" generates a Backstage plugin configuration with
  app-config.yaml entries and catalog-info.yaml component descriptors
  for each Twill component in the application graph. The configuration
  allows Backstage to discover and display Twill services. Pass --output
  to write the plugin files to disk.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app backstage --backend-url http://twill-console:8080 ./...
    twill app backstage --output ./backstage-config ./...

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

			var components []backstage.BackstageComponent
			for _, comp := range graph.Components {
				components = append(components, backstage.BackstageComponent{
					Name:        comp.Name,
					Type:        "service",
					Owner:       *owner,
					Description: "Twill component " + comp.Name,
				})
			}
			sort.Slice(components, func(i, j int) bool {
				return components[i].Name < components[j].Name
			})

			appName := "twill-app"
			if len(graph.Components) > 0 {
				appName = graph.Components[0].Name
			}

			config := backstage.GeneratePlugin(backstage.PluginInput{
				Application: appName,
				BackendURL:  *backendURL,
				Components:  components,
			})

			if *output != "" {
				if err := writeBackstageFiles(config, *output); err != nil {
					return err
				}
			}

			return encodeJSON(os.Stdout, config, !*compact, "backstage")
		},
	}
}

func extractResourceKinds(resources ResourcesContext) []string {
	seen := map[string]bool{}
	var kinds []string
	for _, res := range resources.Resources {
		if res.Kind != "" && !seen[res.Kind] {
			seen[res.Kind] = true
			kinds = append(kinds, res.Kind)
		}
	}
	sort.Strings(kinds)
	return kinds
}

func renderCloudTerraform(specs map[cloud.Provider][]cloud.CloudSpec, providerFilter string) string {
	var sb strings.Builder
	providers := make([]cloud.Provider, 0, len(specs))
	for prov := range specs {
		providers = append(providers, prov)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i] < providers[j] })

	for _, prov := range providers {
		if providerFilter != "" && string(prov) != providerFilter {
			continue
		}
		adapter := cloud.GetAdapter(prov)
		if adapter == nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("# Provider: %s\n", prov))
		for _, spec := range specs[prov] {
			res := adapter.GenerateTerraform(&spec)
			sb.WriteString(cloud.RenderTerraformBlock(res))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func writeCloudTerraform(specs map[cloud.Provider][]cloud.CloudSpec, providerFilter, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	providers := make([]cloud.Provider, 0, len(specs))
	for prov := range specs {
		providers = append(providers, prov)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i] < providers[j] })

	for _, prov := range providers {
		if providerFilter != "" && string(prov) != providerFilter {
			continue
		}
		adapter := cloud.GetAdapter(prov)
		if adapter == nil {
			continue
		}
		filename := fmt.Sprintf("%s.tf", prov)
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("# Generated by twill app cloud --provider %s --terraform\n", prov))
		sb.WriteString("# Review and customize before production use.\n\n")
		for _, spec := range specs[prov] {
			res := adapter.GenerateTerraform(&spec)
			sb.WriteString(cloud.RenderTerraformBlock(res))
		}
		if err := os.WriteFile(filepath.Join(outDir, filename), []byte(sb.String()), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func writeBackstageFiles(config backstage.PluginConfig, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if config.AppConfigYAML != "" {
		if err := os.WriteFile(filepath.Join(outDir, "app-config.yaml"), []byte(config.AppConfigYAML), 0o644); err != nil {
			return err
		}
	}
	for i, catalog := range config.CatalogInfo {
		filename := fmt.Sprintf("catalog-info-%d.yaml", i)
		if len(config.CatalogInfo) == 1 {
			filename = "catalog-info.yaml"
		}
		if err := os.WriteFile(filepath.Join(outDir, filename), []byte(catalog), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func errInvalidCloudProvider(provider string) error {
	return fmt.Errorf("invalid cloud provider %q: must be aws, gcp, or azure", provider)
}
