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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nxsky/twill/runtime/ci"
	"github.com/nxsky/twill/runtime/compliance"
	"github.com/nxsky/twill/runtime/template"
	"github.com/nxsky/twill/runtime/tool"
)

func ciCommand() *tool.Command {
	flags, compact, _ := inspectFlagSet("ci")
	platform := flags.String("platform", "github", "CI platform: github or gitlab")
	appName := flags.String("app", "twill-app", "Application name for the CI pipeline")
	image := flags.String("image", "twill-app:latest", "Container image for CI build and push")
	enablePreview := flags.Bool("preview", false, "Enable preview environment job in CI pipeline")
	enableDeploy := flags.Bool("deploy", false, "Enable production deploy job in CI pipeline")
	enableTests := flags.Bool("tests", true, "Enable test job in CI pipeline")
	enableLint := flags.Bool("lint", true, "Enable lint job in CI pipeline")
	renderYAML := flags.Bool("yaml", false, "Render as CI platform YAML instead of JSON")
	output := flags.String("output", "", "Write generated CI pipeline file under this directory")

	return &tool.Command{
		Name:        "ci",
		Flags:       flags,
		Description: "Generate a CI pipeline configuration for GitHub Actions or GitLab CI",
		Help: `Usage:
  twill app ci [--platform PLATFORM] [--app NAME] [--image IMAGE]
               [--preview] [--deploy] [--tests] [--lint]
               [--yaml] [--output DIR] [packages...]

Description:
  "twill app ci" generates a CI pipeline configuration for the specified
  platform (github for GitHub Actions, gitlab for GitLab CI). The pipeline
  includes test, lint, build, optional preview environment, and optional
  production deploy jobs. By default the output is JSON metadata. Pass --yaml
  to render the pipeline as a platform-native YAML file. Pass --output to
  write the YAML file to disk.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app ci --platform github --app my-service ./...
    twill app ci --platform gitlab --yaml --output ./ci ./...
    twill app ci --preview --deploy --yaml ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			plat := ci.Platform(*platform)
			if plat != ci.PlatformGitHub && plat != ci.PlatformGitLab {
				return errInvalidCIPlatform(*platform)
			}

			config := ci.GeneratePipeline(ci.PipelineInput{
				Platform:      plat,
				Application:   *appName,
				Image:         *image,
				EnablePreview: *enablePreview,
				EnableDeploy:  *enableDeploy,
				EnableTests:   *enableTests,
				EnableLint:    *enableLint,
			})

			if *renderYAML {
				var yamlContent string
				switch plat {
				case ci.PlatformGitHub:
					yamlContent = ci.RenderGitHubActions(config)
				case ci.PlatformGitLab:
					yamlContent = ci.RenderGitLabCI(config)
				}
				if *output != "" {
					filename := ciYAMLFilename(plat)
					if err := os.MkdirAll(*output, 0o755); err != nil {
						return err
					}
					if err := os.WriteFile(filepath.Join(*output, filename), []byte(yamlContent), 0o644); err != nil {
						return err
					}
				}
				fmt.Fprintln(os.Stdout, yamlContent)
				return nil
			}

			return encodeJSON(os.Stdout, config, !*compact, "ci")
		},
	}
}

func templateCommand() *tool.Command {
	flags, compact, _ := inspectFlagSet("template")
	getID := flags.String("get", "", "Get a specific template by ID; omit to list all templates")
	category := flags.String("category", "", "Filter templates by category (service, worker, cron_job, migration, api)")
	output := flags.String("output", "", "Write template files under this directory when using --get")

	return &tool.Command{
		Name:        "template",
		Flags:       flags,
		Description: "List templates from the enterprise catalog or get a specific template",
		Help: `Usage:
  twill app template [--category CATEGORY]
  twill app template --get ID [--output DIR]

Description:
  "twill app template" lists templates from the enterprise catalog. Pass
  --get with a template ID to retrieve a specific template and its file
  contents. Pass --category to filter by category. Pass --output with --get
  to write the template files to disk.

Categories:
  service, worker, cron_job, migration, api

Examples:
  twill app template
  twill app template --category service
  twill app template --get basic-service
  twill app template --get basic-service --output ./my-service

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			catalog := template.NewCatalog()

			if *getID != "" {
				tmpl, err := catalog.Get(*getID)
				if err != nil {
					return err
				}
				if *output != "" {
					if err := writeTemplateFiles(tmpl, *output); err != nil {
						return err
					}
				}
				return encodeJSON(os.Stdout, tmpl, !*compact, "template")
			}

			var templates []template.Template
			if *category != "" {
				templates = catalog.ListByCategory(template.Category(*category))
			} else {
				templates = catalog.List()
			}

			type templateListItem struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				Category    string `json:"category"`
				Description string `json:"description"`
			}
			items := make([]templateListItem, len(templates))
			for i, t := range templates {
				items[i] = templateListItem{
					ID:          t.ID,
					Name:        t.Name,
					Category:    string(t.Category),
					Description: t.Description,
				}
			}
			return encodeJSON(os.Stdout, items, !*compact, "template-list")
		},
	}
}

func complianceCommand() *tool.Command {
	flags, compact, _ := inspectFlagSet("compliance")
	appName := flags.String("app", "", "Filter evidence by application name")
	environment := flags.String("environment", "", "Filter evidence by environment type")
	output := flags.String("output", "", "Write evidence bundle JSON under this directory")

	return &tool.Command{
		Name:        "compliance",
		Flags:       flags,
		Description: "Export compliance evidence bundle from deployment, policy, and approval records",
		Help: `Usage:
  twill app compliance [--app NAME] [--environment TYPE] [--output DIR]

Description:
  "twill app compliance" exports a structured compliance evidence bundle
  containing deployment records, policy gate results, and approval
  decisions. The bundle includes a summary with counts of healthy/unhealthy
  deployments, approved/rejected approvals, and passed/failed policy gates.
  Pass --output to write the evidence bundle JSON to disk.

  If no packages are provided, the current package is inspected. Package
  patterns follow the same syntax as go list, for example:

    twill app compliance --app my-service --environment production ./...
    twill app compliance --output ./audit ./...

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			bundle := compliance.Export(compliance.ExportOptions{
				Application: *appName,
				Environment: *environment,
			}, nil, nil, nil)

			if *output != "" {
				if err := os.MkdirAll(*output, 0o755); err != nil {
					return err
				}
				data, err := jsonMarshalIndent(bundle)
				if err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(*output, "evidence-bundle.json"), data, 0o644); err != nil {
					return err
				}
			}

			return encodeJSON(os.Stdout, bundle, !*compact, "compliance")
		},
	}
}

func writeTemplateFiles(tmpl template.Template, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	sort.Slice(tmpl.Files, func(i, j int) bool {
		return tmpl.Files[i].Path < tmpl.Files[j].Path
	})
	for _, f := range tmpl.Files {
		path := filepath.Join(outDir, f.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(f.Content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func ciYAMLFilename(plat ci.Platform) string {
	switch plat {
	case ci.PlatformGitHub:
		return "twill-ci.yml"
	case ci.PlatformGitLab:
		return ".gitlab-ci.yml"
	default:
		return "twill-ci.yml"
	}
}

func errInvalidCIPlatform(platform string) error {
	return fmt.Errorf("invalid CI platform %q: must be 'github' or 'gitlab'", platform)
}

func jsonMarshalIndent(v any) ([]byte, error) {
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return []byte(sb.String()), nil
}
