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
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nxsky/twill/runtime/packaging"
	"github.com/nxsky/twill/runtime/security"
	"github.com/nxsky/twill/runtime/tool"
	"github.com/nxsky/twill/runtime/version"
)

func versionCommand() *tool.Command {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	checkVersion := flags.String("check", "", "Check if a version (e.g., 0.24.6) is within supported ranges")
	compact := flags.Bool("compact", false, "Emit compact JSON")

	return &tool.Command{
		Name:        "version",
		Flags:       flags,
		Description: "Show the platform compatibility policy, supported version ranges, and release trains",
		Help: `Usage:
  twill app version [--check VERSION]

Description:
  "twill app version" shows the platform's compatibility policy including
  the current version, supported version ranges, release trains, and
  deprecation policy. Pass --check with a semantic version string to verify
  whether that version is within the platform's supported ranges.

Examples:
  twill app version
  twill app version --check 0.24.6
  twill app version --check 3.0.0

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			policy := version.DefaultCompatibilityPolicy()

			if *checkVersion != "" {
				v, err := version.ParseCompatVer(*checkVersion)
				if err != nil {
					return err
				}
				checkErr := policy.CheckCompatVer(v)
				result := map[string]any{
					"schema_version": "twill.version.check.v1",
					"version":        v.String(),
					"compatible":     checkErr == nil,
					"policy":         policy,
				}
				if checkErr != nil {
					result["reason"] = checkErr.Error()
				}
				return encodeJSON(os.Stdout, result, !*compact, "version-check")
			}

			nextTrain := policy.NextReleaseTrain()
			result := map[string]any{
				"schema_version":     "twill.version.policy.v1",
				"current_version":    policy.CurrentVersion,
				"supported_ranges":   policy.SupportedRanges,
				"release_trains":     policy.ReleaseTrains,
				"deprecation_policy": policy.DeprecationPolicy,
			}
			if nextTrain != nil {
				result["next_release_train"] = nextTrain
			}
			return encodeJSON(os.Stdout, result, !*compact, "version")
		},
	}
}

func securityCommand() *tool.Command {
	flags, compact, _ := inspectFlagSet("security")
	action := flags.String("action", "list", "Action: create, list, list-unfixed, list-published")
	title := flags.String("title", "", "Advisory title (for create)")
	severity := flags.String("severity", "medium", "Severity: critical, high, medium, low")
	summary := flags.String("summary", "", "Advisory summary")
	component := flags.String("component", "", "Affected component")
	reporter := flags.String("reporter", "security-team", "Reporter identity")
	fixedVersion := flags.String("fixed-version", "", "Fixed version (for create)")
	remediation := flags.String("remediation", "", "Remediation guidance")

	return &tool.Command{
		Name:        "security",
		Flags:       flags,
		Description: "Manage security advisories for vulnerability tracking and response",
		Help: `Usage:
  twill app security --action create --title TITLE [--severity LEVEL]
                     [--summary TEXT] [--component COMP] [--reporter USER]
  twill app security --action list
  twill app security --action list-unfixed
  twill app security --action list-published

Description:
  "twill app security" manages security advisories for the Twill platform.
  The create action registers a new advisory in draft state with severity
  classification. The list actions return advisories filtered by state.
  Since advisory state is in-memory, each invocation starts fresh. Use the
  JSON output for compliance records and audit evidence.

Actions:
  create          Create a new security advisory
  list            List all advisories
  list-unfixed    List advisories without a fix
  list-published  List published advisories

Severities:
  critical, high, medium, low

Examples:
  twill app security --action create --title "SQL injection in /v1/reverse" \
    --severity high --summary "User input not sanitized"
  twill app security --action list-unfixed

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			rp := security.NewResponseProcess()

			switch *action {
			case "create":
				adv := security.Advisory{
					Title:        *title,
					Severity:     security.Severity(*severity),
					Summary:      *summary,
					Component:    *component,
					Reporter:     *reporter,
					FixedVersion: *fixedVersion,
					Remediation:  *remediation,
				}
				created, err := rp.CreateAdvisory(adv)
				if err != nil {
					return err
				}
				return encodeJSON(os.Stdout, map[string]any{
					"schema_version": "twill.security.advisory.v1",
					"advisory":       created,
					"limitations": []string{
						"Advisory state is in-memory and resets between invocations.",
						"Integrate with a persistent store for production advisory tracking.",
					},
				}, !*compact, "security")

			case "list":
				advisories := rp.ListAdvisories("", "")
				return encodeJSON(os.Stdout, map[string]any{
					"schema_version": "twill.security.list.v1",
					"advisories":     advisories,
					"count":          len(advisories),
				}, !*compact, "security-list")

			case "list-unfixed":
				advisories := rp.ListUnfixed()
				return encodeJSON(os.Stdout, map[string]any{
					"schema_version": "twill.security.unfixed.v1",
					"advisories":     advisories,
					"count":          len(advisories),
				}, !*compact, "security-unfixed")

			case "list-published":
				advisories := rp.ListPublished()
				return encodeJSON(os.Stdout, map[string]any{
					"schema_version": "twill.security.published.v1",
					"advisories":     advisories,
					"count":          len(advisories),
				}, !*compact, "security-published")

			default:
				return fmt.Errorf("invalid action %q: must be create, list, list-unfixed, or list-published", *action)
			}
		},
	}
}

func packagingCommand() *tool.Command {
	flags, compact, _ := inspectFlagSet("packaging")
	name := flags.String("name", "twill-console", "Console package name")
	pkgVersion := flags.String("version", "latest", "Console package version")
	envName := flags.String("environment", "production", "Target environment")
	image := flags.String("image", "", "Container image (defaults to name:version)")
	replicas := flags.Int("replicas", 2, "Desired replica count")
	enableTLS := flags.Bool("tls", false, "Enable TLS for the console")
	enableAuth := flags.Bool("auth", false, "Enable authentication for the console")
	authType := flags.String("auth-type", "", "Auth type (e.g., oidc, saml, basic)")
	output := flags.String("output", "", "Write console manifests and Dockerfile under this directory")

	return &tool.Command{
		Name:        "packaging",
		Flags:       flags,
		Description: "Generate enterprise console deployment package with Kubernetes manifests and Dockerfile",
		Help: `Usage:
  twill app packaging [--name NAME] [--version VER] [--environment ENV]
                      [--image IMAGE] [--replicas N] [--tls] [--auth]
                      [--auth-type TYPE] [--output DIR]

Description:
  "twill app packaging" generates an enterprise console deployment package
  with Kubernetes manifests (Deployment, Service, ConfigMap, Secret, HPA)
  and a multi-stage Dockerfile. The package includes configurable TLS,
  authentication, CORS, resource limits, and replica count. Pass --output
  to write the manifests and Dockerfile to disk.

Examples:
  twill app packaging --tls --auth --auth-type oidc
  twill app packaging --replicas 3 --output ./console-deploy
  twill app packaging --environment staging --image registry/twill-console:v1

Flags:
` + tool.FlagsHelp(flags),
		Fn: func(ctx context.Context, args []string) error {
			pkg := packaging.Package(packaging.PackageInput{
				Name:        *name,
				Version:     *pkgVersion,
				Environment: *envName,
				Image:       *image,
				Replicas:    *replicas,
				EnableTLS:   *enableTLS,
				EnableAuth:  *enableAuth,
				AuthType:    *authType,
			})

			if *output != "" {
				if err := writeConsolePackage(pkg, *output); err != nil {
					return err
				}
			}

			return encodeJSON(os.Stdout, pkg, !*compact, "packaging")
		},
	}
}

func writeConsolePackage(pkg packaging.ConsolePackage, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	manifestsDir := filepath.Join(outDir, "manifests")
	if err := os.MkdirAll(manifestsDir, 0o755); err != nil {
		return err
	}
	sort.Slice(pkg.Manifests, func(i, j int) bool {
		return pkg.Manifests[i].Kind < pkg.Manifests[j].Kind
	})
	for _, m := range pkg.Manifests {
		filename := strings.ToLower(m.Kind) + ".yaml"
		if err := os.WriteFile(filepath.Join(manifestsDir, filename), []byte(m.Content), 0o644); err != nil {
			return err
		}
	}
	if pkg.Dockerfile != "" {
		if err := os.WriteFile(filepath.Join(outDir, "Dockerfile"), []byte(pkg.Dockerfile), 0o644); err != nil {
			return err
		}
	}
	return nil
}
