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
// WITHOUT WARRANTIES OR CONDITIONS of ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package ci provides GitHub and GitLab CI integration for Twill
// deployments. It generates CI pipeline configurations, PR status
// checks, and preview environment triggers from the application graph
// and deployment plans.
package ci

import (
	"fmt"
	"strings"
)

// Platform identifies a CI platform.
type Platform string

const (
	PlatformGitHub Platform = "github"
	PlatformGitLab Platform = "gitlab"
)

// PipelineConfig describes a CI pipeline configuration for Twill.
type PipelineConfig struct {
	Platform    Platform `json:"platform"`
	Name        string   `json:"name"`
	Jobs        []CIJob  `json:"jobs"`
	Triggers    []string `json:"triggers"`
	Limitations []string `json:"limitations"`
}

// CIJob describes one job in a CI pipeline.
type CIJob struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	RunsOn    string   `json:"runs_on,omitempty"`
	Steps     []CIStep `json:"steps"`
	DependsOn []string `json:"depends_on,omitempty"`
	Condition string   `json:"condition,omitempty"`
}

// CIStep describes one step in a CI job.
type CIStep struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

// PipelineInput configures CI pipeline generation.
type PipelineInput struct {
	Platform      Platform
	Application   string
	Image         string
	EnablePreview bool
	EnableDeploy  bool
	EnableTests   bool
	EnableLint    bool
}

// GeneratePipeline creates a CI pipeline configuration from the input.
func GeneratePipeline(input PipelineInput) PipelineConfig {
	config := PipelineConfig{
		Platform: input.Platform,
		Name:     fmt.Sprintf("twill-%s", input.Application),
		Triggers: defaultTriggers(input.Platform),
		Limitations: []string{
			"CI pipeline is a template; customize runner labels, secrets, and environment references before use.",
			"Image build and push require registry credentials configured as CI secrets.",
			"Preview environments require cluster access from CI runners.",
		},
	}

	if input.EnableTests {
		config.Jobs = append(config.Jobs, testJob(input.Platform))
	}
	if input.EnableLint {
		config.Jobs = append(config.Jobs, lintJob(input.Platform))
	}
	config.Jobs = append(config.Jobs, buildJob(input))
	if input.EnablePreview {
		config.Jobs = append(config.Jobs, previewJob(input))
	}
	if input.EnableDeploy {
		config.Jobs = append(config.Jobs, deployJob(input))
	}

	return config
}

// RenderGitHubActions renders a PipelineConfig as a GitHub Actions
// workflow YAML file.
func RenderGitHubActions(config PipelineConfig) string {
	var sb strings.Builder
	sb.WriteString("name: " + config.Name + "\n\n")
	sb.WriteString("on:\n")
	for _, trigger := range config.Triggers {
		sb.WriteString(fmt.Sprintf("  %s\n", trigger))
	}
	sb.WriteString("\njobs:\n")
	for _, job := range config.Jobs {
		sb.WriteString(fmt.Sprintf("  %s:\n", job.ID))
		if job.RunsOn != "" {
			sb.WriteString(fmt.Sprintf("    runs-on: %s\n", job.RunsOn))
		}
		if job.Condition != "" {
			sb.WriteString(fmt.Sprintf("    if: %s\n", job.Condition))
		}
		if len(job.DependsOn) > 0 {
			sb.WriteString(fmt.Sprintf("    needs: [%s]\n", strings.Join(job.DependsOn, ", ")))
		}
		sb.WriteString("    steps:\n")
		for _, step := range job.Steps {
			sb.WriteString(fmt.Sprintf("      - name: %s\n", step.Name))
			sb.WriteString(fmt.Sprintf("        run: %s\n", step.Command))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// RenderGitLabCI renders a PipelineConfig as a GitLab CI YAML file.
func RenderGitLabCI(config PipelineConfig) string {
	var sb strings.Builder
	for _, job := range config.Jobs {
		sb.WriteString(fmt.Sprintf("%s:\n", job.ID))
		if job.RunsOn != "" {
			sb.WriteString(fmt.Sprintf("  image: %s\n", job.RunsOn))
		}
		if job.Condition != "" {
			sb.WriteString(fmt.Sprintf("  rules:\n    - if: %s\n", job.Condition))
		}
		if len(job.DependsOn) > 0 {
			sb.WriteString(fmt.Sprintf("  needs: [%s]\n", strings.Join(job.DependsOn, ", ")))
		}
		sb.WriteString("  script:\n")
		for _, step := range job.Steps {
			sb.WriteString(fmt.Sprintf("    - %s\n", step.Command))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func defaultTriggers(platform Platform) []string {
	switch platform {
	case PlatformGitHub:
		return []string{
			"push:\n    branches: [main]",
			"pull_request:",
		}
	case PlatformGitLab:
		return []string{
			"push",
			"merge_request",
		}
	default:
		return []string{"push"}
	}
}

func testJob(platform Platform) CIJob {
	return CIJob{
		ID:     "test",
		Name:   "Run tests",
		RunsOn: runnerLabel(platform),
		Steps: []CIStep{
			{Name: "Checkout", Command: "actions/checkout@v4"},
			{Name: "Setup Go", Command: "actions/setup-go@v5"},
			{Name: "Run tests", Command: "go test ./... -count=1"},
			{Name: "Twill context", Command: "twill app context ./..."},
		},
	}
}

func lintJob(platform Platform) CIJob {
	return CIJob{
		ID:     "lint",
		Name:   "Lint and vet",
		RunsOn: runnerLabel(platform),
		Steps: []CIStep{
			{Name: "Checkout", Command: "actions/checkout@v4"},
			{Name: "Setup Go", Command: "actions/setup-go@v5"},
			{Name: "Go vet", Command: "go vet ./..."},
			{Name: "Gofmt check", Command: "gofmt -l ."},
		},
	}
}

func buildJob(input PipelineInput) CIJob {
	image := input.Image
	if image == "" {
		image = input.Application + ":latest"
	}
	return CIJob{
		ID:        "build",
		Name:      "Build image",
		RunsOn:    runnerLabel(input.Platform),
		DependsOn: []string{"test"},
		Steps: []CIStep{
			{Name: "Checkout", Command: "actions/checkout@v4"},
			{Name: "Build image", Command: fmt.Sprintf("docker build -t %s .", image)},
			{Name: "Twill deploy plan", Command: fmt.Sprintf("twill deploy k8s --image %s ./...", image)},
		},
	}
}

func previewJob(input PipelineInput) CIJob {
	return CIJob{
		ID:        "preview",
		Name:      "Preview environment",
		RunsOn:    runnerLabel(input.Platform),
		DependsOn: []string{"build"},
		Condition: prCondition(input.Platform),
		Steps: []CIStep{
			{Name: "Checkout", Command: "actions/checkout@v4"},
			{Name: "Generate preview", Command: fmt.Sprintf("twill deploy preview --app %s --pr $PR_NUMBER --image $IMAGE", input.Application)},
			{Name: "Apply preview", Command: "kubectl apply -f preview-manifests.yaml"},
		},
	}
}

func deployJob(input PipelineInput) CIJob {
	return CIJob{
		ID:        "deploy",
		Name:      "Deploy to staging",
		RunsOn:    runnerLabel(input.Platform),
		DependsOn: []string{"build"},
		Condition: mainBranchCondition(input.Platform),
		Steps: []CIStep{
			{Name: "Checkout", Command: "actions/checkout@v4"},
			{Name: "Deploy", Command: fmt.Sprintf("twill deploy k8s --apply --environment staging --image $IMAGE ./...")},
		},
	}
}

func runnerLabel(platform Platform) string {
	switch platform {
	case PlatformGitHub:
		return "ubuntu-latest"
	case PlatformGitLab:
		return "golang:1.24"
	default:
		return "ubuntu-latest"
	}
}

func prCondition(platform Platform) string {
	switch platform {
	case PlatformGitHub:
		return "github.event_name == 'pull_request'"
	case PlatformGitLab:
		return "$CI_PIPELINE_SOURCE == 'merge_request_event'"
	default:
		return ""
	}
}

func mainBranchCondition(platform Platform) string {
	switch platform {
	case PlatformGitHub:
		return "github.ref == 'refs/heads/main'"
	case PlatformGitLab:
		return "$CI_COMMIT_BRANCH == 'main'"
	default:
		return ""
	}
}
