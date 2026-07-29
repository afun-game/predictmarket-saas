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

// Package backstage provides Backstage plugin configuration generation
// for Twill integration. It generates app-config.yaml entries, plugin
// registration code, and catalog-info.yaml component descriptors that
// allow Backstage to discover and display Twill services.
package backstage

import (
	"fmt"
	"sort"
	"strings"
)

// PluginConfig describes a Backstage plugin configuration for Twill.
type PluginConfig struct {
	PluginID      string                `json:"plugin_id"`
	BackendURL    string                `json:"backend_url"`
	Components    []ComponentDescriptor `json:"components"`
	AppConfigYAML string                `json:"app_config_yaml"`
	CatalogInfo   []string              `json:"catalog_info"`
	Limitations   []string              `json:"limitations"`
}

// ComponentDescriptor describes a Backstage catalog component.
type ComponentDescriptor struct {
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Metadata   ComponentMetadata `json:"metadata"`
	Spec       ComponentSpec     `json:"spec"`
}

// ComponentMetadata describes Backstage component metadata.
type ComponentMetadata struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ComponentSpec describes Backstage component spec.
type ComponentSpec struct {
	Type         string   `json:"type"`
	Lifecycle    string   `json:"lifecycle"`
	Owner        string   `json:"owner"`
	Dependencies []string `json:"dependsOn,omitempty"`
}

// PluginInput configures Backstage plugin generation.
type PluginInput struct {
	Application string
	BackendURL  string
	Components  []BackstageComponent
}

// BackstageComponent describes a component to register in Backstage.
type BackstageComponent struct {
	Name         string
	Type         string
	Owner        string
	Description  string
	Dependencies []string
}

// GeneratePlugin creates a Backstage plugin configuration from the input.
func GeneratePlugin(input PluginInput) PluginConfig {
	pluginID := "twill"
	if input.Application != "" {
		pluginID = "twill-" + sanitizeID(input.Application)
	}

	backendURL := input.BackendURL
	if backendURL == "" {
		backendURL = "http://localhost:8080"
	}

	config := PluginConfig{
		PluginID:   pluginID,
		BackendURL: backendURL,
		Limitations: []string{
			"Backstage plugin configuration is a template; adjust backend URL and auth for your Backstage instance.",
			"Catalog descriptors must be registered in Backstage via catalog-import or declarative integration.",
			"Plugin frontend code must be built and deployed separately.",
		},
	}

	for _, comp := range input.Components {
		descriptor := ComponentDescriptor{
			APIVersion: "backstage.io/v1alpha1",
			Kind:       "Component",
			Metadata: ComponentMetadata{
				Name:        sanitizeID(comp.Name),
				Description: comp.Description,
				Tags:        []string{"twill"},
				Annotations: map[string]string{
					"twill.dev/component": comp.Name,
				},
			},
			Spec: ComponentSpec{
				Type:         comp.Type,
				Lifecycle:    "production",
				Owner:        comp.Owner,
				Dependencies: sanitizeDependencies(comp.Dependencies),
			},
		}
		config.Components = append(config.Components, descriptor)
	}

	sort.Slice(config.Components, func(i, j int) bool {
		return config.Components[i].Metadata.Name < config.Components[j].Metadata.Name
	})

	config.AppConfigYAML = renderAppConfig(pluginID, backendURL)
	config.CatalogInfo = renderCatalogInfo(config.Components)

	return config
}

func renderAppConfig(pluginID, backendURL string) string {
	return fmt.Sprintf(`# Backstage app-config.yaml entry for Twill plugin
proxy:
  endpoints:
    %s:
      target: %s
      changeOrigin: true

twill:
  backendUrl: %s
`, pluginID, backendURL, backendURL)
}

func renderCatalogInfo(components []ComponentDescriptor) []string {
	var result []string
	for _, comp := range components {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("apiVersion: %s\n", comp.APIVersion))
		sb.WriteString(fmt.Sprintf("kind: %s\n", comp.Kind))
		sb.WriteString("metadata:\n")
		sb.WriteString(fmt.Sprintf("  name: %s\n", comp.Metadata.Name))
		if comp.Metadata.Description != "" {
			sb.WriteString(fmt.Sprintf("  description: %s\n", comp.Metadata.Description))
		}
		if len(comp.Metadata.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("  tags:\n"))
			for _, tag := range comp.Metadata.Tags {
				sb.WriteString(fmt.Sprintf("    - %s\n", tag))
			}
		}
		if len(comp.Metadata.Annotations) > 0 {
			sb.WriteString("  annotations:\n")
			keys := make([]string, 0, len(comp.Metadata.Annotations))
			for k := range comp.Metadata.Annotations {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				sb.WriteString(fmt.Sprintf("    %s: %s\n", k, comp.Metadata.Annotations[k]))
			}
		}
		sb.WriteString("spec:\n")
		sb.WriteString(fmt.Sprintf("  type: %s\n", comp.Spec.Type))
		sb.WriteString(fmt.Sprintf("  lifecycle: %s\n", comp.Spec.Lifecycle))
		sb.WriteString(fmt.Sprintf("  owner: %s\n", comp.Spec.Owner))
		if len(comp.Spec.Dependencies) > 0 {
			sb.WriteString("  dependsOn:\n")
			for _, dep := range comp.Spec.Dependencies {
				sb.WriteString(fmt.Sprintf("    - %s\n", dep))
			}
		}
		result = append(result, sb.String())
	}
	return result
}

func sanitizeID(name string) string {
	parts := strings.Split(name, "/")
	result := parts[len(parts)-1]
	result = strings.ToLower(result)
	result = strings.ReplaceAll(result, ".", "-")
	result = strings.ReplaceAll(result, "_", "-")
	return result
}

func sanitizeDependencies(deps []string) []string {
	var result []string
	for _, dep := range deps {
		result = append(result, "component:"+sanitizeID(dep))
	}
	return result
}
