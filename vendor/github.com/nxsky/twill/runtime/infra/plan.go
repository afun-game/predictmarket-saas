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

// Package infra provides infrastructure plan generation from the application
// graph. It analyzes resource declarations and generates a structured
// infrastructure plan covering local provisioning, Kubernetes provisioning,
// and cloud resource requirements.
package infra

import (
	"fmt"
	"sort"
	"strings"
)

// ResourceKind classifies the type of infrastructure resource.
type ResourceKind string

const (
	KindDatabase      ResourceKind = "database"
	KindCache         ResourceKind = "cache"
	KindPubSub        ResourceKind = "pubsub"
	KindObjectStorage ResourceKind = "object_storage"
	KindCron          ResourceKind = "cron"
	KindSecret        ResourceKind = "secret"
	KindQueue         ResourceKind = "queue"
	KindTopic         ResourceKind = "topic"
	KindSubscription  ResourceKind = "subscription"
	KindListener      ResourceKind = "listener"
)

// ProvisioningTarget describes where a resource should be provisioned.
type ProvisioningTarget string

const (
	TargetLocal      ProvisioningTarget = "local"
	TargetKubernetes ProvisioningTarget = "kubernetes"
	TargetCloud      ProvisioningTarget = "cloud"
)

// ResourcePlan describes one infrastructure resource requirement derived
// from the application graph.
type ResourcePlan struct {
	Name      string                  `json:"name"`
	Kind      ResourceKind            `json:"kind"`
	Component string                  `json:"component,omitempty"`
	Type      string                  `json:"type,omitempty"`
	Provider  string                  `json:"provider,omitempty"`
	Targets   []ProvisioningTarget    `json:"targets"`
	LocalSpec *LocalResourceSpec      `json:"local_spec,omitempty"`
	K8sSpec   *KubernetesResourceSpec `json:"kubernetes_spec,omitempty"`
	CloudSpec *CloudResourceSpec      `json:"cloud_spec,omitempty"`
}

// LocalResourceSpec describes how to provision the resource locally
// (e.g., via Docker Compose).
type LocalResourceSpec struct {
	Image       string            `json:"image"`
	Ports       []string          `json:"ports,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Volume      string            `json:"volume,omitempty"`
	Healthcheck string            `json:"healthcheck,omitempty"`
}

// KubernetesResourceSpec describes how to provision the resource in
// Kubernetes (e.g., via a Helm chart or operator).
type KubernetesResourceSpec struct {
	Chart     string            `json:"chart,omitempty"`
	Namespace string            `json:"namespace,omitempty"`
	Values    map[string]string `json:"values,omitempty"`
	Manifests []string          `json:"manifests,omitempty"`
}

// CloudResourceSpec describes how to provision the resource in a cloud
// provider (e.g., via Terraform or Crossplane).
type CloudResourceSpec struct {
	Provider          string            `json:"provider"`
	ResourceType      string            `json:"resource_type"`
	Region            string            `json:"region,omitempty"`
	Properties        map[string]string `json:"properties,omitempty"`
	TerraformResource string            `json:"terraform_resource,omitempty"`
}

// InfraPlan is the complete infrastructure plan generated from the
// application graph.
type InfraPlan struct {
	SchemaVersion  string         `json:"schema_version"`
	Application    string         `json:"application"`
	Resources      []ResourcePlan `json:"resources"`
	Summary        InfraSummary   `json:"summary"`
	Limitations    []string       `json:"limitations"`
	VerifyCommands []string       `json:"verify_commands"`
}

// InfraSummary summarizes the infrastructure plan.
type InfraSummary struct {
	TotalResources      int            `json:"total_resources"`
	LocalResources      int            `json:"local_resources"`
	KubernetesResources int            `json:"kubernetes_resources"`
	CloudResources      int            `json:"cloud_resources"`
	ByKind              map[string]int `json:"by_kind"`
}

// GraphInput is a minimal interface to the application graph data needed
// for infrastructure plan generation. It avoids a direct dependency on
// the app package.
type GraphInput struct {
	ApplicationName string
	Components      []ComponentInput
}

// ComponentInput describes a component and its resources for infra planning.
type ComponentInput struct {
	Name      string
	Resources []ResourceInput
}

// ResourceInput describes a resource declaration found in a component.
type ResourceInput struct {
	Name      string
	Kind      string
	Type      string
	Component string
}

// GeneratePlan analyzes the application graph input and produces a
// structured infrastructure plan.
func GeneratePlan(input GraphInput) InfraPlan {
	plan := InfraPlan{
		SchemaVersion: "twill.infra.plan.v1",
		Application:   input.ApplicationName,
	}

	seen := map[string]bool{}
	for _, comp := range input.Components {
		for _, res := range comp.Resources {
			key := res.Component + "/" + res.Name + "/" + res.Kind
			if seen[key] {
				continue
			}
			seen[key] = true

			rp := buildResourcePlan(res)
			plan.Resources = append(plan.Resources, rp)
		}
	}

	sort.Slice(plan.Resources, func(i, j int) bool {
		if plan.Resources[i].Kind != plan.Resources[j].Kind {
			return string(plan.Resources[i].Kind) < string(plan.Resources[j].Kind)
		}
		return plan.Resources[i].Name < plan.Resources[j].Name
	})

	plan.Summary = computeSummary(plan.Resources)
	plan.Limitations = defaultLimitations()
	plan.VerifyCommands = defaultVerifyCommands(input.ApplicationName)

	return plan
}

func buildResourcePlan(res ResourceInput) ResourcePlan {
	kind := ResourceKind(res.Kind)
	rp := ResourcePlan{
		Name:      res.Name,
		Kind:      kind,
		Component: res.Component,
		Type:      res.Type,
	}

	switch kind {
	case KindDatabase:
		rp.Targets = []ProvisioningTarget{TargetLocal, TargetKubernetes, TargetCloud}
		rp.LocalSpec = &LocalResourceSpec{
			Image:       "postgres:17-alpine",
			Ports:       []string{"5432"},
			Environment: map[string]string{"POSTGRES_DB": "twill", "POSTGRES_PASSWORD": "*********"},
			Volume:      "postgres-data",
			Healthcheck: "pg_isready -U postgres",
		}
		rp.K8sSpec = &KubernetesResourceSpec{
			Chart:     "bitnami/postgresql",
			Namespace: "default",
			Values:    map[string]string{"auth.postgresPassword": "*********", "primary.persistence.size": "10Gi"},
		}
		rp.CloudSpec = &CloudResourceSpec{
			Provider:          "aws",
			ResourceType:      "aws_db_instance",
			TerraformResource: "aws_db_instance",
			Properties:        map[string]string{"engine": "postgres", "instance_class": "db.t3.micro"},
		}
	case KindCache:
		rp.Targets = []ProvisioningTarget{TargetLocal, TargetKubernetes, TargetCloud}
		rp.LocalSpec = &LocalResourceSpec{
			Image:       "redis:7-alpine",
			Ports:       []string{"6379"},
			Volume:      "redis-data",
			Healthcheck: "redis-cli ping",
		}
		rp.K8sSpec = &KubernetesResourceSpec{
			Chart:     "bitnami/redis",
			Namespace: "default",
			Values:    map[string]string{"architecture": "standalone", "auth.password": "*********"},
		}
		rp.CloudSpec = &CloudResourceSpec{
			Provider:          "aws",
			ResourceType:      "aws_elasticache_cluster",
			TerraformResource: "aws_elasticache_cluster",
			Properties:        map[string]string{"engine": "redis", "node_type": "cache.t3.micro"},
		}
	case KindPubSub, KindTopic, KindSubscription, KindQueue:
		rp.Targets = []ProvisioningTarget{TargetLocal, TargetKubernetes, TargetCloud}
		rp.LocalSpec = &LocalResourceSpec{
			Image:       "nats:2-alpine",
			Ports:       []string{"4222"},
			Healthcheck: "wget --spider -q http://localhost:8222/healthz",
		}
		rp.K8sSpec = &KubernetesResourceSpec{
			Chart:     "bitnami/nats",
			Namespace: "default",
		}
		rp.CloudSpec = &CloudResourceSpec{
			Provider:          "aws",
			ResourceType:      "aws_sns_topic",
			TerraformResource: "aws_sns_topic",
		}
	case KindObjectStorage:
		rp.Targets = []ProvisioningTarget{TargetLocal, TargetKubernetes, TargetCloud}
		rp.LocalSpec = &LocalResourceSpec{
			Image:       "minio/minio:RELEASE.2025-09-07T16-13-09Z",
			Ports:       []string{"9000"},
			Environment: map[string]string{"MINIO_ROOT_PASSWORD": "change-me"},
			Volume:      "minio-data",
			Healthcheck: "curl -sf http://localhost:9000/minio/health/live",
		}
		rp.K8sSpec = &KubernetesResourceSpec{
			Chart:     "minio/minio",
			Namespace: "default",
		}
		rp.CloudSpec = &CloudResourceSpec{
			Provider:          "aws",
			ResourceType:      "aws_s3_bucket",
			TerraformResource: "aws_s3_bucket",
		}
	case KindCron:
		rp.Targets = []ProvisioningTarget{TargetKubernetes}
		rp.K8sSpec = &KubernetesResourceSpec{
			Manifests: []string{"CronJob"},
			Namespace: "default",
		}
	case KindSecret:
		rp.Targets = []ProvisioningTarget{TargetKubernetes, TargetCloud}
		rp.K8sSpec = &KubernetesResourceSpec{
			Manifests: []string{"Secret"},
			Namespace: "default",
		}
		rp.CloudSpec = &CloudResourceSpec{
			Provider:          "aws",
			ResourceType:      "aws_secretsmanager_secret",
			TerraformResource: "aws_secretsmanager_secret",
		}
	case KindListener:
		rp.Targets = []ProvisioningTarget{TargetKubernetes}
		rp.K8sSpec = &KubernetesResourceSpec{
			Manifests: []string{"Service", "Ingress"},
			Namespace: "default",
		}
	default:
		rp.Targets = []ProvisioningTarget{TargetCloud}
	}

	return rp
}

func computeSummary(resources []ResourcePlan) InfraSummary {
	summary := InfraSummary{
		ByKind: map[string]int{},
	}
	for _, res := range resources {
		summary.TotalResources++
		summary.ByKind[string(res.Kind)]++
		for _, target := range res.Targets {
			switch target {
			case TargetLocal:
				summary.LocalResources++
			case TargetKubernetes:
				summary.KubernetesResources++
			case TargetCloud:
				summary.CloudResources++
			}
		}
	}
	return summary
}

func defaultLimitations() []string {
	return []string{
		"Infrastructure plan is generated from local source metadata, not from live infrastructure.",
		"Cloud resource specs are templates; region, sizing, and pricing must be configured before provisioning.",
		"Helm chart references are community charts; verify compatibility before production use.",
		"Secret values are never exposed; only secret resource structure is planned.",
	}
}

func defaultVerifyCommands(appName string) []string {
	return []string{
		fmt.Sprintf("twill deploy compose %s", appName),
		fmt.Sprintf("twill deploy k8s --app %s --image <image> %s", appName, appName),
		fmt.Sprintf("twill app resources %s", appName),
	}
}

// GenerateTerraform generates Terraform configuration strings from an
// infrastructure plan. Each cloud resource becomes a Terraform resource
// block.
func GenerateTerraform(plan InfraPlan) []TerraformBlock {
	var blocks []TerraformBlock
	for _, res := range plan.Resources {
		if res.CloudSpec == nil {
			continue
		}
		block := TerraformBlock{
			ResourceType: res.CloudSpec.TerraformResource,
			Name:         sanitizeTerraformName(res.Name, res.Component),
			Provider:     res.CloudSpec.Provider,
			Properties:   map[string]string{},
		}
		for k, v := range res.CloudSpec.Properties {
			block.Properties[k] = v
		}
		if res.CloudSpec.Region != "" {
			block.Properties["region"] = res.CloudSpec.Region
		}
		blocks = append(blocks, block)
	}
	return blocks
}

// TerraformBlock describes one Terraform resource block.
type TerraformBlock struct {
	ResourceType string            `json:"resource_type"`
	Name         string            `json:"name"`
	Provider     string            `json:"provider"`
	Properties   map[string]string `json:"properties"`
}

// RenderTerraform renders Terraform blocks as HCL configuration text.
func RenderTerraform(blocks []TerraformBlock) string {
	var sb strings.Builder
	for _, block := range blocks {
		sb.WriteString(fmt.Sprintf("resource \"%s\" \"%s\" {\n", block.ResourceType, block.Name))
		keys := make([]string, 0, len(block.Properties))
		for k := range block.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sb.WriteString(fmt.Sprintf("  %s = \"%s\"\n", k, block.Properties[k]))
		}
		sb.WriteString("}\n\n")
	}
	return sb.String()
}

func sanitizeTerraformName(name, component string) string {
	base := name
	if component != "" {
		parts := strings.Split(component, "/")
		if len(parts) > 0 {
			base = parts[len(parts)-1] + "_" + name
		}
	}
	base = strings.ToLower(base)
	base = strings.ReplaceAll(base, "/", "_")
	base = strings.ReplaceAll(base, "-", "_")
	base = strings.ReplaceAll(base, ".", "_")
	return base
}
