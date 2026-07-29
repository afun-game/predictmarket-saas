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

// Package cloud provides cloud provider adapters for AWS, GCP, and Azure.
// Each adapter generates provider-specific infrastructure specs and
// Terraform resources from the application graph's resource declarations.
package cloud

import (
	"fmt"
	"sort"
	"strings"
)

// Provider identifies a cloud provider.
type Provider string

const (
	ProviderAWS   Provider = "aws"
	ProviderGCP   Provider = "gcp"
	ProviderAzure Provider = "azure"
)

// Adapter generates cloud-specific infrastructure specs for a given
// resource kind.
type Adapter interface {
	Provider() Provider
	GenerateCloudSpec(kind string, name string, component string) *CloudSpec
	GenerateTerraform(spec *CloudSpec) TerraformResource
}

// CloudSpec describes a cloud resource for a specific provider.
type CloudSpec struct {
	Provider          string            `json:"provider"`
	ResourceType      string            `json:"resource_type"`
	Region            string            `json:"region,omitempty"`
	Properties        map[string]string `json:"properties,omitempty"`
	TerraformResource string            `json:"terraform_resource"`
	TerraformProvider string            `json:"terraform_provider,omitempty"`
}

// TerraformResource describes a Terraform resource block for a specific
// cloud provider.
type TerraformResource struct {
	Type       string            `json:"type"`
	Name       string            `json:"name"`
	Provider   string            `json:"provider"`
	Properties map[string]string `json:"properties"`
}

// AWSAdapter generates AWS-specific infrastructure specs.
type AWSAdapter struct {
	Region string
}

func (a AWSAdapter) Provider() Provider { return ProviderAWS }

func (a AWSAdapter) GenerateCloudSpec(kind, name, component string) *CloudSpec {
	region := a.Region
	if region == "" {
		region = "us-east-1"
	}
	spec := &CloudSpec{
		Provider:          string(ProviderAWS),
		Region:            region,
		TerraformProvider: "aws",
		Properties:        map[string]string{},
	}
	switch kind {
	case "database":
		spec.ResourceType = "aws_db_instance"
		spec.TerraformResource = "aws_db_instance"
		spec.Properties["engine"] = "postgres"
		spec.Properties["instance_class"] = "db.t3.micro"
		spec.Properties["allocated_storage"] = "20"
	case "cache":
		spec.ResourceType = "aws_elasticache_cluster"
		spec.TerraformResource = "aws_elasticache_cluster"
		spec.Properties["engine"] = "redis"
		spec.Properties["node_type"] = "cache.t3.micro"
		spec.Properties["num_cache_nodes"] = "1"
	case "pubsub", "topic":
		spec.ResourceType = "aws_sns_topic"
		spec.TerraformResource = "aws_sns_topic"
	case "queue", "subscription":
		spec.ResourceType = "aws_sqs_queue"
		spec.TerraformResource = "aws_sqs_queue"
	case "object_storage":
		spec.ResourceType = "aws_s3_bucket"
		spec.TerraformResource = "aws_s3_bucket"
	case "secret":
		spec.ResourceType = "aws_secretsmanager_secret"
		spec.TerraformResource = "aws_secretsmanager_secret"
	case "cron":
		spec.ResourceType = "aws_cloudwatch_event_rule"
		spec.TerraformResource = "aws_cloudwatch_event_rule"
		spec.Properties["schedule_expression"] = "rate(1 hour)"
	default:
		return nil
	}
	return spec
}

func (a AWSAdapter) GenerateTerraform(spec *CloudSpec) TerraformResource {
	props := map[string]string{}
	for k, v := range spec.Properties {
		props[k] = v
	}
	if spec.Region != "" {
		props["region"] = spec.Region
	}
	return TerraformResource{
		Type:       spec.TerraformResource,
		Name:       sanitizeName(spec.ResourceType, "aws"),
		Provider:   "aws",
		Properties: props,
	}
}

// GCPAdapter generates GCP-specific infrastructure specs.
type GCPAdapter struct {
	Region string
}

func (a GCPAdapter) Provider() Provider { return ProviderGCP }

func (a GCPAdapter) GenerateCloudSpec(kind, name, component string) *CloudSpec {
	region := a.Region
	if region == "" {
		region = "us-central1"
	}
	spec := &CloudSpec{
		Provider:          string(ProviderGCP),
		Region:            region,
		TerraformProvider: "google",
		Properties:        map[string]string{},
	}
	switch kind {
	case "database":
		spec.ResourceType = "google_sql_database_instance"
		spec.TerraformResource = "google_sql_database_instance"
		spec.Properties["database_version"] = "POSTGRES_15"
		spec.Properties["tier"] = "db-f1-micro"
	case "cache":
		spec.ResourceType = "google_redis_instance"
		spec.TerraformResource = "google_redis_instance"
		spec.Properties["tier"] = "BASIC"
		spec.Properties["memory_size_gb"] = "1"
	case "pubsub", "topic":
		spec.ResourceType = "google_pubsub_topic"
		spec.TerraformResource = "google_pubsub_topic"
	case "queue", "subscription":
		spec.ResourceType = "google_pubsub_subscription"
		spec.TerraformResource = "google_pubsub_subscription"
	case "object_storage":
		spec.ResourceType = "google_storage_bucket"
		spec.TerraformResource = "google_storage_bucket"
	case "secret":
		spec.ResourceType = "google_secret_manager_secret"
		spec.TerraformResource = "google_secret_manager_secret"
	case "cron":
		spec.ResourceType = "google_cloud_scheduler_job"
		spec.TerraformResource = "google_cloud_scheduler_job"
		spec.Properties["schedule"] = "0 * * * *"
	default:
		return nil
	}
	return spec
}

func (a GCPAdapter) GenerateTerraform(spec *CloudSpec) TerraformResource {
	props := map[string]string{}
	for k, v := range spec.Properties {
		props[k] = v
	}
	if spec.Region != "" {
		props["region"] = spec.Region
	}
	return TerraformResource{
		Type:       spec.TerraformResource,
		Name:       sanitizeName(spec.ResourceType, "gcp"),
		Provider:   "google",
		Properties: props,
	}
}

// AzureAdapter generates Azure-specific infrastructure specs.
type AzureAdapter struct {
	Region string
}

func (a AzureAdapter) Provider() Provider { return ProviderAzure }

func (a AzureAdapter) GenerateCloudSpec(kind, name, component string) *CloudSpec {
	region := a.Region
	if region == "" {
		region = "eastus"
	}
	spec := &CloudSpec{
		Provider:          string(ProviderAzure),
		Region:            region,
		TerraformProvider: "azurerm",
		Properties:        map[string]string{},
	}
	switch kind {
	case "database":
		spec.ResourceType = "azurerm_postgresql_server"
		spec.TerraformResource = "azurerm_postgresql_server"
		spec.Properties["sku_name"] = "B_Gen5_1"
		spec.Properties["version"] = "15"
		spec.Properties["storage_mb"] = "5120"
	case "cache":
		spec.ResourceType = "azurerm_redis_cache"
		spec.TerraformResource = "azurerm_redis_cache"
		spec.Properties["sku_name"] = "Basic"
		spec.Properties["capacity"] = "0"
	case "pubsub", "topic":
		spec.ResourceType = "azurerm_servicebus_topic"
		spec.TerraformResource = "azurerm_servicebus_topic"
	case "queue", "subscription":
		spec.ResourceType = "azurerm_servicebus_queue"
		spec.TerraformResource = "azurerm_servicebus_queue"
	case "object_storage":
		spec.ResourceType = "azurerm_storage_account"
		spec.TerraformResource = "azurerm_storage_account"
		spec.Properties["account_tier"] = "Standard"
		spec.Properties["account_replication_type"] = "LRS"
	case "secret":
		spec.ResourceType = "azurerm_key_vault_secret"
		spec.TerraformResource = "azurerm_key_vault_secret"
	case "cron":
		spec.ResourceType = "azurerm_logic_app_trigger_recurrence"
		spec.TerraformResource = "azurerm_logic_app_trigger_recurrence"
		spec.Properties["frequency"] = "Hour"
		spec.Properties["interval"] = "1"
	default:
		return nil
	}
	return spec
}

func (a AzureAdapter) GenerateTerraform(spec *CloudSpec) TerraformResource {
	props := map[string]string{}
	for k, v := range spec.Properties {
		props[k] = v
	}
	if spec.Region != "" {
		props["location"] = spec.Region
	}
	return TerraformResource{
		Type:       spec.TerraformResource,
		Name:       sanitizeName(spec.ResourceType, "azure"),
		Provider:   "azurerm",
		Properties: props,
	}
}

// GetAdapter returns the adapter for the given provider.
func GetAdapter(provider Provider) Adapter {
	switch provider {
	case ProviderAWS:
		return AWSAdapter{}
	case ProviderGCP:
		return GCPAdapter{}
	case ProviderAzure:
		return AzureAdapter{}
	default:
		return nil
	}
}

// AllAdapters returns all registered cloud adapters.
func AllAdapters() []Adapter {
	return []Adapter{AWSAdapter{}, GCPAdapter{}, AzureAdapter{}}
}

// GenerateMultiCloud generates cloud specs for all providers from a
// list of resource kinds.
func GenerateMultiCloud(kinds []string) map[Provider][]CloudSpec {
	result := map[Provider][]CloudSpec{}
	for _, adapter := range AllAdapters() {
		var specs []CloudSpec
		for _, kind := range kinds {
			spec := adapter.GenerateCloudSpec(kind, kind, "")
			if spec != nil {
				specs = append(specs, *spec)
			}
		}
		sort.Slice(specs, func(i, j int) bool {
			return specs[i].ResourceType < specs[j].ResourceType
		})
		result[adapter.Provider()] = specs
	}
	return result
}

// RenderTerraformBlock renders a Terraform resource as HCL text.
func RenderTerraformBlock(res TerraformResource) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("resource \"%s\" \"%s\" {\n", res.Type, res.Name))
	keys := make([]string, 0, len(res.Properties))
	for k := range res.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sb.WriteString(fmt.Sprintf("  %s = \"%s\"\n", k, res.Properties[k]))
	}
	sb.WriteString("}\n")
	return sb.String()
}

func sanitizeName(resourceType, providerPrefix string) string {
	name := strings.ReplaceAll(resourceType, "_", "-")
	name = strings.TrimPrefix(name, providerPrefix+"-")
	name = strings.TrimPrefix(name, "aws-")
	name = strings.TrimPrefix(name, "google-")
	name = strings.TrimPrefix(name, "azurerm-")
	if name == "" {
		name = "resource"
	}
	return name
}
