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
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	imetrics "github.com/nxsky/twill/internal/metrics"
	"github.com/nxsky/twill/internal/tool/deployplan"
	"github.com/nxsky/twill/runtime/codegen"
	"github.com/nxsky/twill/runtime/deployers"
	"golang.org/x/tools/go/packages"
)

const (
	contextSchemaVersion     = "twill.app.context.v1"
	policyRulesSchemaVersion = "twill.policy.rules.v1"
	projectPolicyRulesFile   = ".twill/policy/rules.json"
	maxPolicyRulesBytes      = 256 * 1024
	endpointContractsDir     = "docs/endpoints"
	maxEndpointContractBytes = 128 * 1024
	resourceDeclarationsDir  = "docs/resources"
	maxResourceDeclBytes     = 64 * 1024
	configBindingsDir        = "docs/config"
	maxConfigBindingBytes    = 64 * 1024
	middlewareImportPath     = "github.com/nxsky/twill/runtime/middleware"
	observabilityImportPath  = "github.com/nxsky/twill/runtime/observability"
)

// ContextPack is the local AI-readable context bundle for a Twill application.
type ContextPack struct {
	SchemaVersion     string               `json:"schema_version"`
	Graph             *Graph               `json:"graph"`
	Components        ComponentsContext    `json:"components"`
	Endpoints         EndpointsContext     `json:"endpoints"`
	Protobuf          ProtobufContext      `json:"protobuf"`
	Middleware        MiddlewareContext    `json:"middleware"`
	OpenAPI           *OpenAPIDocument     `json:"openapi"`
	ClientSDK         ClientSDKContext     `json:"client_sdk"`
	ContractTests     ContractTestsContext `json:"contract_tests"`
	LocalCompose      LocalComposeContext  `json:"local_compose"`
	Resources         ResourcesContext     `json:"resources"`
	Config            ConfigContext        `json:"config"`
	Observability     ObservabilityContext `json:"observability"`
	Deployment        DeploymentContext    `json:"deployment"`
	PolicyRules       PolicyRulesContext   `json:"policy_rules"`
	Generated         GeneratedContext     `json:"generated"`
	Tests             *Tests               `json:"tests"`
	SafetyNotes       []string             `json:"safety_notes"`
	VerifyCommands    []string             `json:"verify_commands"`
	PerformedWrites   bool                 `json:"performed_writes"`
	PerformedEnvWrite bool                 `json:"performed_environment_write"`
}

// ComponentsContext describes components and dependency edges.
type ComponentsContext struct {
	SchemaVersion string          `json:"schema_version"`
	Components    []Component     `json:"components"`
	Edges         []Edge          `json:"edges"`
	Routers       []RouterBinding `json:"routers"`
	Files         []string        `json:"files,omitempty"`
	Limitations   []string        `json:"limitations"`
}

// RouterBinding describes a component routing binding without exposing routing logic.
type RouterBinding struct {
	Component  string `json:"component"`
	RouterType string `json:"router_type"`
	Source     string `json:"source"`
}

// EndpointSurface describes endpoint-adjacent listener metadata.
type EndpointSurface struct {
	Component string   `json:"component"`
	Listeners []string `json:"listeners"`
}

// EndpointsContext describes endpoint-adjacent metadata.
type EndpointsContext struct {
	SchemaVersion string                `json:"schema_version"`
	Endpoints     []EndpointSurface     `json:"endpoints"`
	Declarations  []EndpointDeclaration `json:"declarations"`
	Contracts     []EndpointContract    `json:"contracts"`
	Files         []string              `json:"files,omitempty"`
	Limitations   []string              `json:"limitations"`
}

// EndpointContract describes safe endpoint contract metadata from generated docs.
type EndpointContract struct {
	Component string `json:"component"`
	Listener  string `json:"listener"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Source    string `json:"source"`
}

// EndpointDeclaration describes first-class safe endpoint metadata.
type EndpointDeclaration struct {
	Kind              string `json:"kind,omitempty"`
	Protocol          string `json:"protocol,omitempty"`
	Component         string `json:"component"`
	Listener          string `json:"listener"`
	Service           string `json:"service,omitempty"`
	Method            string `json:"method"`
	Path              string `json:"path"`
	RequestType       string `json:"request_type,omitempty"`
	ResponseType      string `json:"response_type,omitempty"`
	RequestStreaming  bool   `json:"request_streaming,omitempty"`
	ResponseStreaming bool   `json:"response_streaming,omitempty"`
	Auth              string `json:"auth,omitempty"`
	Middleware        string `json:"middleware,omitempty"`
	Compatibility     string `json:"compatibility,omitempty"`
	Source            string `json:"source"`
}

// MiddlewareContext describes standard middleware evidence that is safe for AI agents.
type MiddlewareContext struct {
	SchemaVersion string              `json:"schema_version"`
	Components    []string            `json:"components"`
	Middleware    []MiddlewareBinding `json:"middleware"`
	Files         []string            `json:"files,omitempty"`
	Limitations   []string            `json:"limitations"`
}

// MiddlewareBinding describes one standard middleware reference without exposing handler logic.
type MiddlewareBinding struct {
	Component string `json:"component,omitempty"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	Source    string `json:"source"`
}

// ResourceSurface describes application resources visible to AI agents.
type ResourceSurface struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Component string `json:"component,omitempty"`
	Type      string `json:"type,omitempty"`
	Lifecycle string `json:"lifecycle,omitempty"`
	Binding   string `json:"binding,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Required  bool   `json:"required,omitempty"`
	Source    string `json:"source,omitempty"`
}

// ResourceGraphNode describes one node in a resource dependency graph.
type ResourceGraphNode struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Component string `json:"component,omitempty"`
	Resource  string `json:"resource,omitempty"`
}

// ResourceGraphEdge describes one dependency edge between resource graph nodes.
type ResourceGraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

// ResourceGraph describes the dependency graph between components and resources.
type ResourceGraph struct {
	SchemaVersion string              `json:"schema_version"`
	Nodes         []ResourceGraphNode `json:"nodes"`
	Edges         []ResourceGraphEdge `json:"edges"`
}

// ResourcesContext describes resource-related application context.
type ResourcesContext struct {
	SchemaVersion string            `json:"schema_version"`
	Resources     []ResourceSurface `json:"resources"`
	Graph         ResourceGraph     `json:"graph"`
	Files         []string          `json:"files,omitempty"`
	Limitations   []string          `json:"limitations"`
}

// ConfigContext describes config-related context that is safe for AI agents.
type ConfigContext struct {
	SchemaVersion string          `json:"schema_version"`
	Components    []string        `json:"components"`
	Schemas       []ConfigSchema  `json:"schemas"`
	Bindings      []ConfigBinding `json:"bindings"`
	Files         []string        `json:"files,omitempty"`
	Limitations   []string        `json:"limitations"`
}

// ConfigSchema describes a component config binding without exposing config fields or values.
type ConfigSchema struct {
	Component  string `json:"component"`
	ConfigType string `json:"config_type"`
	Source     string `json:"source"`
}

// ConfigBinding describes a safe config binding without exposing keys or values.
type ConfigBinding struct {
	Component  string `json:"component"`
	ConfigType string `json:"config_type,omitempty"`
	Kind       string `json:"kind"`
	Provider   string `json:"provider,omitempty"`
	Lifecycle  string `json:"lifecycle,omitempty"`
	Required   bool   `json:"required,omitempty"`
	Source     string `json:"source"`
}

// ObservabilityContext describes local observability context for AI agents.
type ObservabilityContext struct {
	SchemaVersion string                 `json:"schema_version"`
	Defaults      []ObservabilityDefault `json:"defaults"`
	Traces        TracesContext          `json:"traces"`
	Logs          LogsContext            `json:"logs"`
	Metrics       MetricsContext         `json:"metrics"`
	Files         []string               `json:"files,omitempty"`
	Limitations   []string               `json:"limitations"`
}

// ObservabilityDefault describes standard observability default evidence.
type ObservabilityDefault struct {
	Component string `json:"component,omitempty"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
}

// TraceSurface describes trace metadata visible to AI agents.
type TraceSurface struct {
	TraceID   string `json:"trace_id"`
	Component string `json:"component,omitempty"`
	Source    string `json:"source,omitempty"`
}

// TracesContext describes trace summaries that are safe for AI agents.
type TracesContext struct {
	SchemaVersion string         `json:"schema_version"`
	Components    []string       `json:"components"`
	Traces        []TraceSurface `json:"traces"`
	Limitations   []string       `json:"limitations"`
}

// LogSource describes a log source visible to AI agents.
type LogSource struct {
	Name      string `json:"name"`
	Component string `json:"component,omitempty"`
	Kind      string `json:"kind,omitempty"`
}

// LogsContext describes log sources and redaction limits.
type LogsContext struct {
	SchemaVersion string      `json:"schema_version"`
	Components    []string    `json:"components"`
	Sources       []LogSource `json:"sources"`
	Limitations   []string    `json:"limitations"`
}

// MetricSignal describes a metric signal visible to AI agents.
type MetricSignal struct {
	Name        string `json:"name"`
	Component   string `json:"component,omitempty"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

// MetricsContext describes metric summaries and SLO signals.
type MetricsContext struct {
	SchemaVersion string         `json:"schema_version"`
	Components    []string       `json:"components"`
	Signals       []MetricSignal `json:"signals"`
	Limitations   []string       `json:"limitations"`
}

// DeploymentContext describes local deployment context for AI agents.
type DeploymentContext struct {
	SchemaVersion string              `json:"schema_version"`
	Status        DeployStatusContext `json:"status"`
	Kubernetes    KubernetesContext   `json:"kubernetes"`
	AWS           AWSContext          `json:"aws"`
	Limitations   []string            `json:"limitations"`
}

// DeploymentStatus describes one application or component deployment status.
type DeploymentStatus struct {
	Name        string `json:"name"`
	Component   string `json:"component,omitempty"`
	Environment string `json:"environment,omitempty"`
	State       string `json:"state"`
	Source      string `json:"source"`
}

// DeployStatusContext describes deployment status visible to AI agents.
type DeployStatusContext struct {
	SchemaVersion string             `json:"schema_version"`
	Components    []string           `json:"components"`
	Statuses      []DeploymentStatus `json:"statuses"`
	Limitations   []string           `json:"limitations"`
}

// KubernetesResource describes a generated or observed Kubernetes resource.
type KubernetesResource struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Component string `json:"component,omitempty"`
	Source    string `json:"source"`
}

// KubernetesResourceRequirements describes reviewed container resource settings.
type KubernetesResourceRequirements struct {
	CPURequest    string `json:"cpu_request,omitempty"`
	MemoryRequest string `json:"memory_request,omitempty"`
	CPULimit      string `json:"cpu_limit,omitempty"`
	MemoryLimit   string `json:"memory_limit,omitempty"`
}

// KubernetesRollout describes static rollout metadata emitted by the dry-run planner.
type KubernetesRollout struct {
	Name                 string                         `json:"name"`
	Namespace            string                         `json:"namespace"`
	Strategy             string                         `json:"strategy"`
	Replicas             int                            `json:"replicas"`
	MaxReplicas          int                            `json:"max_replicas,omitempty"`
	HealthPath           string                         `json:"health_path"`
	ResourceRequirements KubernetesResourceRequirements `json:"resource_requirements,omitempty"`
	VerifyCommands       []string                       `json:"verify_commands"`
	RollbackCommands     []string                       `json:"rollback_commands,omitempty"`
	Source               string                         `json:"source"`
}

// KubernetesContext describes Kubernetes resources visible to AI agents.
type KubernetesContext struct {
	SchemaVersion          string               `json:"schema_version"`
	Components             []string             `json:"components"`
	DryRun                 bool                 `json:"dry_run"`
	Resources              []KubernetesResource `json:"resources"`
	Rollout                KubernetesRollout    `json:"rollout"`
	PreApplyValidated      bool                 `json:"pre_apply_validated,omitempty"`
	PreApplyValidationMode string               `json:"pre_apply_validation_mode,omitempty"`
	Environment            string               `json:"environment,omitempty"`
	PolicyGates            []PolicyGateResult   `json:"policy_gates,omitempty"`
	RolloutHealthCheck     *RolloutHealthCheck  `json:"rollout_health_check,omitempty"`
	Limitations            []string             `json:"limitations"`
	VerifyCommands         []string             `json:"verify_commands"`
	PerformedWrites        bool                 `json:"performed_writes"`
	PerformedEnvWrite      bool                 `json:"performed_environment_write"`
}

// PolicyGateResult describes a deployment policy gate evaluation result
// for AI agent visibility.
type PolicyGateResult struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Passed   bool   `json:"passed"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// RolloutHealthCheck describes post-apply rollout health monitoring
// results for AI agent visibility.
type RolloutHealthCheck struct {
	Enabled                bool    `json:"enabled"`
	SLOName                string  `json:"slo_name,omitempty"`
	RollbackBurnRate       float64 `json:"rollback_burn_rate,omitempty"`
	RollbackWindow         string  `json:"rollback_window,omitempty"`
	AllowAutomaticRollback bool    `json:"allow_automatic_rollback"`
	StatusCommand          string  `json:"status_command,omitempty"`
	RollbackCommand        string  `json:"rollback_command,omitempty"`
	HealthEvaluated        bool    `json:"health_evaluated,omitempty"`
	HealthStatus           string  `json:"health_status,omitempty"`
	RollbackTriggered      bool    `json:"rollback_triggered,omitempty"`
	HealthReason           string  `json:"health_reason,omitempty"`
}

// AWSResource describes AWS/EKS dry-run deployment metadata.
type AWSResource struct {
	Kind                      string `json:"kind"`
	Name                      string `json:"name"`
	Region                    string `json:"region,omitempty"`
	Source                    string `json:"source"`
	Layer                     string `json:"layer,omitempty"`
	Target                    string `json:"target,omitempty"`
	ManifestType              string `json:"manifest_type,omitempty"`
	EmbeddedFromSchemaVersion string `json:"embedded_from_schema_version,omitempty"`
}

// AWSContext describes AWS EKS deployment metadata visible to AI agents.
type AWSContext struct {
	SchemaVersion          string              `json:"schema_version"`
	Components             []string            `json:"components"`
	DryRun                 bool                `json:"dry_run"`
	Resources              []AWSResource       `json:"resources"`
	Rollout                KubernetesRollout   `json:"rollout"`
	PreApplyValidated      bool                `json:"pre_apply_validated,omitempty"`
	PreApplyValidationMode string              `json:"pre_apply_validation_mode,omitempty"`
	Environment            string              `json:"environment,omitempty"`
	PolicyGates            []PolicyGateResult  `json:"policy_gates,omitempty"`
	RolloutHealthCheck     *RolloutHealthCheck `json:"rollout_health_check,omitempty"`
	Limitations            []string            `json:"limitations"`
	VerifyCommands         []string            `json:"verify_commands"`
	PerformedWrites        bool                `json:"performed_writes"`
	PerformedEnvWrite      bool                `json:"performed_environment_write"`
}

// PolicyRule describes one AI/tool safety policy.
type PolicyRule struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	AppliesTo   []string `json:"applies_to"`
	Requirement string   `json:"requirement"`
	Enforcement string   `json:"enforcement"`
}

// PolicyRulesContext describes policy rules for local AI tooling.
type PolicyRulesContext struct {
	SchemaVersion string       `json:"schema_version"`
	Rules         []PolicyRule `json:"rules"`
	Files         []string     `json:"files,omitempty"`
	Limitations   []string     `json:"limitations"`
}

// GeneratedContext describes generated metadata files used for inspection.
type GeneratedContext struct {
	SchemaVersion string   `json:"schema_version"`
	Files         []string `json:"files"`
}

// InspectContextPack inspects packages and returns a local AI context bundle.
func InspectContextPack(ctx context.Context, opts GraphOptions) (*ContextPack, error) {
	graph, err := InspectGraph(ctx, opts)
	if err != nil {
		return nil, err
	}
	tests, err := InspectTests(ctx, opts)
	if err != nil {
		return nil, err
	}
	policyRules, err := InspectPolicyRules(PolicyOptions{Dir: opts.Dir})
	if err != nil {
		return nil, err
	}
	components, err := inspectComponentsContext(ctx, opts, graph)
	if err != nil {
		return nil, err
	}
	config, err := inspectConfigContext(ctx, opts, graph)
	if err != nil {
		return nil, err
	}
	endpoints, err := inspectEndpointsContext(opts, graph)
	if err != nil {
		return nil, err
	}
	protobuf, err := InspectProtobufContext(ctx, opts)
	if err != nil {
		return nil, err
	}
	endpoints = endpointsContextWithProtobuf(endpoints, protobuf)
	middleware, err := inspectMiddlewareContext(ctx, opts, graph)
	if err != nil {
		return nil, err
	}
	resources, err := inspectResourcesContext(ctx, opts, graph)
	if err != nil {
		return nil, err
	}
	observability, err := inspectObservabilityContext(ctx, opts, graph)
	if err != nil {
		return nil, err
	}
	pack := NewContextPack(graph, tests)
	pack.PolicyRules = policyRules
	pack.Components = components
	pack.Config = config
	pack.Endpoints = endpoints
	pack.Protobuf = protobuf
	pack.Middleware = middleware
	pack.OpenAPI = OpenAPIForEndpoints(endpoints)
	pack.ClientSDK = clientSDKContextForEndpoints(endpoints, opts.Patterns)
	pack.ContractTests = contractTestsContextForEndpoints(endpoints, opts.Patterns)
	pack.Resources = resources
	pack.LocalCompose = LocalComposeContextForResources(resources, LocalComposeOptions{
		Patterns: opts.Patterns,
	})
	pack.Observability = observability
	pack.Deployment = deploymentContextForGraph(ctx, graph, endpoints, opts.Patterns)
	pack.VerifyCommands = contextVerifyCommands(opts.Patterns)
	return pack, nil
}

// InspectComponentsContext inspects packages and returns component context.
func InspectComponentsContext(ctx context.Context, opts GraphOptions) (ComponentsContext, error) {
	graph, err := InspectGraph(ctx, opts)
	if err != nil {
		return ComponentsContext{}, err
	}
	return inspectComponentsContext(ctx, opts, graph)
}

// InspectEndpointsContext inspects packages and returns endpoint-adjacent context.
func InspectEndpointsContext(ctx context.Context, opts GraphOptions) (EndpointsContext, error) {
	graph, err := InspectGraph(ctx, opts)
	if err != nil {
		return EndpointsContext{}, err
	}
	endpoints, err := inspectEndpointsContext(opts, graph)
	if err != nil {
		return EndpointsContext{}, err
	}
	protobuf, err := InspectProtobufContext(ctx, opts)
	if err != nil {
		return EndpointsContext{}, err
	}
	return endpointsContextWithProtobuf(endpoints, protobuf), nil
}

// InspectResourcesContext inspects packages and returns safe resource context.
func InspectResourcesContext(ctx context.Context, opts GraphOptions) (ResourcesContext, error) {
	graph, err := InspectGraph(ctx, opts)
	if err != nil {
		return ResourcesContext{}, err
	}
	return inspectResourcesContext(ctx, opts, graph)
}

// InspectConfigContext inspects packages and returns safe config context.
func InspectConfigContext(ctx context.Context, opts GraphOptions) (ConfigContext, error) {
	graph, err := InspectGraph(ctx, opts)
	if err != nil {
		return ConfigContext{}, err
	}
	return inspectConfigContext(ctx, opts, graph)
}

// InspectMiddlewareContext inspects packages and returns safe middleware context.
func InspectMiddlewareContext(ctx context.Context, opts GraphOptions) (MiddlewareContext, error) {
	graph, err := InspectGraph(ctx, opts)
	if err != nil {
		return MiddlewareContext{}, err
	}
	return inspectMiddlewareContext(ctx, opts, graph)
}

// InspectObservabilityContext inspects packages and returns observability context.
func InspectObservabilityContext(ctx context.Context, opts GraphOptions) (ObservabilityContext, error) {
	graph, err := InspectGraph(ctx, opts)
	if err != nil {
		return ObservabilityContext{}, err
	}
	return inspectObservabilityContext(ctx, opts, graph)
}

// InspectDeploymentContext inspects packages and returns deployment context.
func InspectDeploymentContext(ctx context.Context, opts GraphOptions) (DeploymentContext, error) {
	graph, err := InspectGraph(ctx, opts)
	if err != nil {
		return DeploymentContext{}, err
	}
	endpoints, err := inspectEndpointsContext(opts, graph)
	if err != nil {
		return DeploymentContext{}, err
	}
	return deploymentContextForGraph(ctx, graph, endpoints, opts.Patterns), nil
}

// InspectGeneratedContext inspects packages and returns generated metadata context.
func InspectGeneratedContext(ctx context.Context, opts GraphOptions) (GeneratedContext, error) {
	graph, err := InspectGraph(ctx, opts)
	if err != nil {
		return GeneratedContext{}, err
	}
	return GeneratedContextForGraph(graph), nil
}

// PolicyOptions configures policy rule inspection.
type PolicyOptions struct {
	Dir string
}

// InspectPolicyRules returns baseline policy rules plus optional project rules.
func InspectPolicyRules(opts PolicyOptions) (PolicyRulesContext, error) {
	policyRules := PolicyRules()
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	path := filepath.Join(dir, projectPolicyRulesFile)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			policyRules.Limitations = append(
				policyRules.Limitations,
				"No project-specific policy file was found at "+projectPolicyRulesFile+".",
			)
			return policyRules, nil
		}
		return PolicyRulesContext{}, fmt.Errorf("stat project policy rules %s: %w", path, err)
	}
	if info.Size() > maxPolicyRulesBytes {
		return PolicyRulesContext{}, fmt.Errorf(
			"project policy rules %s is %d bytes, maximum is %d bytes",
			path,
			info.Size(),
			maxPolicyRulesBytes,
		)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return PolicyRulesContext{}, fmt.Errorf("read project policy rules %s: %w", path, err)
	}

	var projectRules PolicyRulesContext
	if err := json.Unmarshal(data, &projectRules); err != nil {
		return PolicyRulesContext{}, fmt.Errorf("parse project policy rules %s: %w", path, err)
	}
	if projectRules.SchemaVersion != "" && projectRules.SchemaVersion != policyRulesSchemaVersion {
		return PolicyRulesContext{}, fmt.Errorf(
			"project policy rules %s schema_version = %q, want %q",
			path,
			projectRules.SchemaVersion,
			policyRulesSchemaVersion,
		)
	}
	if err := validateProjectPolicyRules(policyRules.Rules, projectRules.Rules); err != nil {
		return PolicyRulesContext{}, fmt.Errorf("validate project policy rules %s: %w", path, err)
	}

	policyRules.Rules = append(policyRules.Rules, projectRules.Rules...)
	policyRules.Files = []string{projectPolicyRulesFile}
	policyRules.Limitations = []string{
		"Baseline policy rules are static defaults.",
		"Project policy rules are loaded from " + projectPolicyRulesFile + " when present.",
		fmt.Sprintf("Project policy files are limited to %d bytes.", maxPolicyRulesBytes),
		"Only structured policy metadata is read; config values, secret names, and secret values are not inferred.",
	}
	return policyRules, nil
}

// NewContextPack builds a context bundle from inspected graph and test data.
func NewContextPack(graph *Graph, tests *Tests) *ContextPack {
	return &ContextPack{
		SchemaVersion: contextSchemaVersion,
		Graph:         graph,
		Components:    ComponentsContextForGraph(graph),
		Endpoints:     EndpointsContextForGraph(graph),
		Protobuf: ProtobufContext{
			SchemaVersion: protobufSchemaVersion,
			Packages:      []ProtobufPackage{},
			Services:      []ProtobufService{},
			Messages:      []ProtobufMessage{},
			RuntimeHints:  protobufRuntimeHints(),
			Files:         []string{},
			Limitations:   protobufLimitations(),
		},
		Middleware:        MiddlewareContextForGraph(graph),
		OpenAPI:           OpenAPIForEndpoints(EndpointsContextForGraph(graph)),
		ClientSDK:         ClientSDKContextForEndpoints(EndpointsContextForGraph(graph)),
		ContractTests:     ContractTestsContextForEndpoints(EndpointsContextForGraph(graph)),
		Resources:         ResourcesContextForGraph(graph),
		LocalCompose:      LocalComposeContextForResources(ResourcesContextForGraph(graph), LocalComposeOptions{}),
		Config:            ConfigContextForGraph(graph),
		Observability:     ObservabilityContextForGraph(graph),
		Deployment:        DeploymentContextForGraph(graph),
		PolicyRules:       PolicyRules(),
		Generated:         GeneratedContextForGraph(graph),
		Tests:             tests,
		SafetyNotes:       []string{"Read-only inspection; no files or external resources were modified."},
		VerifyCommands:    contextVerifyCommands(nil),
		PerformedWrites:   false,
		PerformedEnvWrite: false,
	}
}

func contextVerifyCommands(patterns []string) []string {
	patternArgs := verifyPatternArgs(patterns)
	return []string{
		"twill app context " + patternArgs,
		"twill app openapi " + patternArgs,
		"go test " + patternArgs,
	}
}

func verifyPatternArgs(patterns []string) string {
	if len(patterns) == 0 {
		return "./..."
	}
	args := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		args = append(args, verifyShellArg(pattern))
	}
	return strings.Join(args, " ")
}

func verifyShellArg(value string) string {
	if value == "" {
		return "''"
	}
	if strings.ContainsAny(value, " \t\n'\"\\$`") {
		return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
	}
	return value
}

// ComponentsContextForGraph returns component context from a graph.
func ComponentsContextForGraph(graph *Graph) ComponentsContext {
	return ComponentsContext{
		SchemaVersion: "twill.app.components.v1",
		Components:    append([]Component{}, graph.Components...),
		Edges:         append([]Edge{}, graph.Edges...),
		Routers:       []RouterBinding{},
		Files:         []string{},
		Limitations: []string{
			"Component context combines generated metadata with safe source-level router bindings when present.",
			"Router bindings report only twill.WithRouter type names and source files; routing methods and keys are not exposed.",
		},
	}
}

func inspectComponentsContext(ctx context.Context, opts GraphOptions, graph *Graph) (ComponentsContext, error) {
	components := ComponentsContextForGraph(graph)
	routers, files, err := inspectRouterBindings(ctx, opts, graph)
	if err != nil {
		return ComponentsContext{}, err
	}
	components.Routers = routers
	components.Files = files
	return components, nil
}

func inspectRouterBindings(
	ctx context.Context,
	opts GraphOptions,
	graph *Graph,
) ([]RouterBinding, []string, error) {
	dir := packageLoadDir(opts)
	rootDir, err := inspectionRootDir(opts)
	if err != nil {
		return nil, nil, err
	}
	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = []string{"."}
	}

	cfg := &packages.Config{
		Context: ctx,
		Dir:     dir,
		Mode:    packages.NeedName | packages.NeedFiles,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, nil, err
	}

	componentSet := make(map[string]struct{}, len(graph.Components))
	for _, component := range graph.Components {
		componentSet[component.Name] = struct{}{}
	}

	routers := []RouterBinding{}
	filesRead := map[string]struct{}{}
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			return nil, nil, packageErrors(pkg)
		}
		for _, filename := range pkg.GoFiles {
			if filepath.Base(filename) == "twill_gen.go" {
				continue
			}
			cleanFilename := cleanPath(rootDir, filename)
			filesRead[cleanFilename] = struct{}{}
			fileRouters, err := inspectRouterBindingsInFile(pkg.PkgPath, componentSet, filename, cleanFilename)
			if err != nil {
				return nil, nil, err
			}
			routers = append(routers, fileRouters...)
		}
	}

	sort.Slice(routers, func(i, j int) bool {
		if routers[i].Component != routers[j].Component {
			return routers[i].Component < routers[j].Component
		}
		if routers[i].RouterType != routers[j].RouterType {
			return routers[i].RouterType < routers[j].RouterType
		}
		return routers[i].Source < routers[j].Source
	})

	files := make([]string, 0, len(filesRead))
	for file := range filesRead {
		files = append(files, file)
	}
	sort.Strings(files)
	return routers, files, nil
}

func inspectRouterBindingsInFile(
	pkgPath string,
	components map[string]struct{},
	filename string,
	cleanFilename string,
) ([]RouterBinding, error) {
	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse router binding source %s: %w", filename, err)
	}

	routers := []RouterBinding{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			router, ok := routerBindingForStruct(pkgPath, components, cleanFilename, structType)
			if ok {
				routers = append(routers, router)
			}
		}
	}
	return routers, nil
}

func routerBindingForStruct(
	pkgPath string,
	components map[string]struct{},
	source string,
	structType *ast.StructType,
) (RouterBinding, bool) {
	component := ""
	routerType := ""
	for _, field := range structType.Fields.List {
		if len(field.Names) != 0 {
			continue
		}
		base, arg := genericEmbedding(field.Type)
		switch base {
		case "Implements":
			component = componentNameForConfig(pkgPath, arg)
		case "WithRouter":
			routerType = astTypeName(arg)
		}
	}
	if component == "" || routerType == "" {
		return RouterBinding{}, false
	}
	if _, ok := components[component]; !ok {
		return RouterBinding{}, false
	}
	return RouterBinding{
		Component:  component,
		RouterType: routerType,
		Source:     source,
	}, true
}

// EndpointsContextForGraph returns endpoint-adjacent context from a graph.
func EndpointsContextForGraph(graph *Graph) EndpointsContext {
	return EndpointsContext{
		SchemaVersion: "twill.app.endpoints.v1",
		Endpoints:     EndpointSurfaces(graph),
		Declarations:  []EndpointDeclaration{},
		Contracts:     []EndpointContract{},
		Files:         []string{},
		Limitations: []string{
			"Endpoint context combines Twill listener names with safe endpoint contract summaries when present.",
			"Endpoint declaration discovery reads only whitelisted fields from " + endpointContractsDir + "/*.md.",
			"Source-level declaration discovery only accepts static net/http method-aware route patterns and Twill gRPC adapter marker tags.",
			"Auth, middleware, request, response, and compatibility metadata are normalized to safe summaries.",
			"Free-form contract text, examples, credentials, headers, query values, and response bodies are not exposed.",
			"Generated endpoint declarations remain a compatibility bridge for adapter metadata and gRPC declarations.",
			"Matching local protobuf RPCs can supply gRPC adapter request and response type references without exposing payload fields.",
		},
	}
}

func inspectEndpointsContext(opts GraphOptions, graph *Graph) (EndpointsContext, error) {
	endpoints := EndpointsContextForGraph(graph)
	contracts, declarations, files, err := inspectEndpointContracts(opts, graph)
	if err != nil {
		return EndpointsContext{}, err
	}
	adapterDeclarations, adapterFiles, err := inspectEndpointAdapters(opts, graph)
	if err != nil {
		return EndpointsContext{}, err
	}
	sourceDeclarations, sourceFiles, err := inspectEndpointSources(opts, graph)
	if err != nil {
		return EndpointsContext{}, err
	}
	endpoints.Contracts = contracts
	endpoints.Declarations = dedupeEndpointDeclarations(append(
		append(declarations, adapterDeclarations...),
		sourceDeclarations...,
	))
	sortEndpointDeclarations(endpoints.Declarations)
	endpoints.Files = mergeStringSlices(mergeStringSlices(files, adapterFiles), sourceFiles)
	return endpoints, nil
}

func endpointsContextWithProtobuf(endpoints EndpointsContext, protobuf ProtobufContext) EndpointsContext {
	if len(endpoints.Declarations) == 0 || len(protobuf.Services) == 0 {
		return endpoints
	}
	enriched, matched := enrichEndpointDeclarationsWithProtobuf(endpoints.Declarations, protobuf)
	endpoints.Declarations = dedupeEndpointDeclarations(enriched)
	sortEndpointDeclarations(endpoints.Declarations)
	if matched {
		endpoints.Files = mergeStringSlices(endpoints.Files, protobuf.Files)
	}
	return endpoints
}

func enrichEndpointDeclarationsWithProtobuf(
	declarations []EndpointDeclaration,
	protobuf ProtobufContext,
) ([]EndpointDeclaration, bool) {
	rpcs := protobufRPCsByServiceAndMethod(protobuf)
	if len(rpcs) == 0 {
		return declarations, false
	}
	matched := false
	enriched := make([]EndpointDeclaration, 0, len(declarations))
	for _, declaration := range declarations {
		if declaration.Protocol != "grpc" || declaration.Service == "" || declaration.Method == "" {
			enriched = append(enriched, declaration)
			continue
		}
		rpc, ok := rpcs[protobufRPCKey{
			Service: declaration.Service,
			Method:  declaration.Method,
		}]
		if !ok {
			enriched = append(enriched, declaration)
			continue
		}
		matched = true
		if declaration.RequestType == "" {
			declaration.RequestType = rpc.RequestType
		}
		if declaration.ResponseType == "" {
			declaration.ResponseType = rpc.ResponseType
		}
		if rpc.RequestStreaming {
			declaration.RequestStreaming = true
		}
		if rpc.ResponseStreaming {
			declaration.ResponseStreaming = true
		}
		enriched = append(enriched, declaration)
	}
	return enriched, matched
}

type protobufRPCKey struct {
	Service string
	Method  string
}

func protobufRPCsByServiceAndMethod(protobuf ProtobufContext) map[protobufRPCKey]ProtobufRPC {
	rpcs := map[protobufRPCKey]ProtobufRPC{}
	for _, service := range protobuf.Services {
		serviceNames := protobufServiceMatchNames(service)
		for _, rpc := range service.RPCs {
			for _, serviceName := range serviceNames {
				rpcs[protobufRPCKey{
					Service: serviceName,
					Method:  rpc.Name,
				}] = rpc
			}
		}
	}
	return rpcs
}

func protobufServiceMatchNames(service ProtobufService) []string {
	names := []string{service.Name}
	if service.Package == "" {
		return names
	}
	fullName := service.Package + "." + service.Name
	if fullName != service.Name {
		names = append(names, fullName)
	}
	return names
}

func inspectEndpointContracts(
	opts GraphOptions,
	graph *Graph,
) ([]EndpointContract, []EndpointDeclaration, []string, error) {
	rootDir, err := inspectionRootDir(opts)
	if err != nil {
		return nil, nil, nil, err
	}
	contractDir := filepath.Join(rootDir, endpointContractsDir)
	info, err := os.Stat(contractDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []EndpointContract{}, []EndpointDeclaration{}, []string{}, nil
		}
		return nil, nil, nil, fmt.Errorf("stat endpoint contracts %s: %w", contractDir, err)
	}
	if !info.IsDir() {
		return []EndpointContract{}, []EndpointDeclaration{}, []string{}, nil
	}

	contracts := []EndpointContract{}
	declarations := []EndpointDeclaration{}
	files := []string{}
	err = filepath.WalkDir(contractDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".md" || strings.HasSuffix(path, "_runbook.md") {
			return nil
		}
		contract, declaration, ok, err := inspectEndpointContractFile(rootDir, path, graph)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		contracts = append(contracts, contract)
		declarations = append(declarations, declaration)
		files = append(files, contract.Source)
		return nil
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("walk endpoint contracts %s: %w", contractDir, err)
	}

	sort.Slice(contracts, func(i, j int) bool {
		if contracts[i].Component != contracts[j].Component {
			return contracts[i].Component < contracts[j].Component
		}
		if contracts[i].Listener != contracts[j].Listener {
			return contracts[i].Listener < contracts[j].Listener
		}
		if contracts[i].Method != contracts[j].Method {
			return contracts[i].Method < contracts[j].Method
		}
		if contracts[i].Path != contracts[j].Path {
			return contracts[i].Path < contracts[j].Path
		}
		return contracts[i].Source < contracts[j].Source
	})
	sort.Slice(declarations, func(i, j int) bool {
		return endpointDeclarationLess(declarations[i], declarations[j])
	})
	sort.Strings(files)
	return contracts, declarations, files, nil
}

func inspectEndpointAdapters(
	opts GraphOptions,
	graph *Graph,
) ([]EndpointDeclaration, []string, error) {
	rootDir, err := inspectionRootDir(opts)
	if err != nil {
		return nil, nil, err
	}
	listenersByComponent := map[string]map[string]struct{}{}
	for _, component := range graph.Components {
		listeners := map[string]struct{}{}
		for _, listener := range component.Listeners {
			listeners[listener] = struct{}{}
		}
		listenersByComponent[component.Name] = listeners
	}
	declarations := []EndpointDeclaration{}
	filesRead := map[string]struct{}{}
	for _, generated := range graph.GeneratedFiles {
		filename := filepath.Join(rootDir, generated)
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, nil, fmt.Errorf("read endpoint adapter metadata %s: %w", filename, err)
		}
		source := cleanPath(rootDir, filename)
		fileDeclarations := endpointAdaptersFromGenerated(source, data, listenersByComponent)
		if len(fileDeclarations) == 0 {
			continue
		}
		declarations = append(declarations, fileDeclarations...)
		filesRead[source] = struct{}{}
	}
	sortEndpointDeclarations(declarations)
	files := make([]string, 0, len(filesRead))
	for file := range filesRead {
		files = append(files, file)
	}
	sort.Strings(files)
	return declarations, files, nil
}

func endpointAdaptersFromGenerated(
	source string,
	data []byte,
	listenersByComponent map[string]map[string]struct{},
) []EndpointDeclaration {
	declarations := []EndpointDeclaration{}
	for _, componentAdapters := range codegen.ExtractHTTPAdapters(data) {
		listeners := listenersByComponent[componentAdapters.Component]
		if len(listeners) == 0 {
			continue
		}
		for _, adapter := range componentAdapters.Adapters {
			if _, ok := listeners[adapter.Listener]; !ok {
				continue
			}
			if !validEndpointHTTPAdapter(adapter) {
				continue
			}
			declarations = append(declarations, EndpointDeclaration{
				Kind:      "adapter",
				Protocol:  "http",
				Component: componentAdapters.Component,
				Listener:  adapter.Listener,
				Method:    strings.ToUpper(adapter.Method),
				Path:      adapter.Path,
				Source:    source,
			})
		}
	}
	for _, componentAdapters := range codegen.ExtractGRPCAdapters(data) {
		listeners := listenersByComponent[componentAdapters.Component]
		if len(listeners) == 0 {
			continue
		}
		for _, adapter := range componentAdapters.Adapters {
			if _, ok := listeners[adapter.Listener]; !ok {
				continue
			}
			if !validEndpointGRPCAdapter(adapter) {
				continue
			}
			declarations = append(declarations, EndpointDeclaration{
				Kind:      "adapter",
				Protocol:  "grpc",
				Component: componentAdapters.Component,
				Listener:  adapter.Listener,
				Service:   adapter.Service,
				Method:    adapter.Method,
				Path:      "/" + adapter.Service + "/" + adapter.Method,
				Source:    source,
			})
		}
	}
	return declarations
}

type endpointSourceTarget struct {
	Component string
	Listener  string
}

func inspectEndpointSources(
	opts GraphOptions,
	graph *Graph,
) ([]EndpointDeclaration, []string, error) {
	rootDir, err := inspectionRootDir(opts)
	if err != nil {
		return nil, nil, err
	}
	targets := endpointSourceTargets(graph)
	declarations := []EndpointDeclaration{}
	filesRead := map[string]struct{}{}
	for _, pkg := range graph.Packages {
		target, ok := targets[pkg.Path]
		if !ok {
			continue
		}
		pkgDir := filepath.Join(rootDir, filepath.FromSlash(pkg.Dir))
		entries, err := os.ReadDir(pkgDir)
		if err != nil {
			return nil, nil, fmt.Errorf("read endpoint source package %s: %w", pkgDir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
				continue
			}
			if entry.Name() == "twill_gen.go" || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			filename := filepath.Join(pkgDir, entry.Name())
			source := cleanPath(rootDir, filename)
			fileDeclarations, err := inspectEndpointSourceFile(filename, source, target)
			if err != nil {
				return nil, nil, err
			}
			if len(fileDeclarations) == 0 {
				continue
			}
			declarations = append(declarations, fileDeclarations...)
			filesRead[source] = struct{}{}
		}
	}
	sortEndpointDeclarations(declarations)
	files := make([]string, 0, len(filesRead))
	for file := range filesRead {
		files = append(files, file)
	}
	sort.Strings(files)
	return declarations, files, nil
}

func endpointSourceTargets(graph *Graph) map[string]endpointSourceTarget {
	componentsByPackage := map[string][]Component{}
	for _, component := range graph.Components {
		if len(component.Listeners) == 0 {
			continue
		}
		componentsByPackage[component.Package] = append(componentsByPackage[component.Package], component)
	}
	targets := map[string]endpointSourceTarget{}
	for pkgPath, components := range componentsByPackage {
		if len(components) != 1 || len(components[0].Listeners) != 1 {
			continue
		}
		targets[pkgPath] = endpointSourceTarget{
			Component: components[0].Name,
			Listener:  components[0].Listeners[0],
		}
	}
	return targets
}

func inspectEndpointSourceFile(
	filename string,
	source string,
	target endpointSourceTarget,
) ([]EndpointDeclaration, error) {
	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint source %s: %w", filename, err)
	}
	httpAliases := sourceHTTPAliases(file)
	twillAliases := sourceTwillAliases(file)
	if len(httpAliases) == 0 && len(twillAliases) == 0 {
		return []EndpointDeclaration{}, nil
	}
	declarations := []EndpointDeclaration{}
	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.CallExpr:
			if len(typed.Args) == 0 || !sourceHTTPRouteCall(typed.Fun, httpAliases) {
				return true
			}
			pattern, ok := sourceStringExpr(typed.Args[0], httpAliases)
			if !ok {
				return true
			}
			method, path, ok := sourceHTTPMethodPath(pattern)
			if !ok {
				return true
			}
			declarations = append(declarations, EndpointDeclaration{
				Kind:      "source",
				Protocol:  "http",
				Component: target.Component,
				Listener:  target.Listener,
				Method:    method,
				Path:      path,
				Source:    source,
			})
		case *ast.Field:
			declaration, ok := sourceGRPCAdapterDeclaration(typed, target, source, twillAliases)
			if ok {
				declarations = append(declarations, declaration)
			}
		}
		return true
	})
	return declarations, nil
}

func sourceHTTPAliases(file *ast.File) map[string]struct{} {
	aliases := map[string]struct{}{}
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil || path != "net/http" {
			continue
		}
		if imported.Name == nil {
			aliases["http"] = struct{}{}
			continue
		}
		if imported.Name.Name == "." || imported.Name.Name == "_" {
			continue
		}
		aliases[imported.Name.Name] = struct{}{}
	}
	return aliases
}

func sourceTwillAliases(file *ast.File) map[string]struct{} {
	aliases := map[string]struct{}{}
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil || path != "github.com/nxsky/twill" {
			continue
		}
		if imported.Name == nil {
			aliases["twill"] = struct{}{}
			continue
		}
		if imported.Name.Name == "." || imported.Name.Name == "_" {
			continue
		}
		aliases[imported.Name.Name] = struct{}{}
	}
	return aliases
}

func sourceHTTPRouteCall(fun ast.Expr, httpAliases map[string]struct{}) bool {
	selector, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if selector.Sel.Name != "Handle" && selector.Sel.Name != "HandleFunc" {
		return false
	}
	if ident, ok := selector.X.(*ast.Ident); ok {
		if _, ok := httpAliases[ident.Name]; ok {
			return true
		}
	}
	return len(httpAliases) > 0
}

func sourceStringExpr(expr ast.Expr, httpAliases map[string]struct{}) (string, bool) {
	switch typed := expr.(type) {
	case *ast.BasicLit:
		if typed.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(typed.Value)
		return value, err == nil
	case *ast.BinaryExpr:
		if typed.Op != token.ADD {
			return "", false
		}
		left, ok := sourceStringExpr(typed.X, httpAliases)
		if !ok {
			return "", false
		}
		right, ok := sourceStringExpr(typed.Y, httpAliases)
		if !ok {
			return "", false
		}
		return left + right, true
	case *ast.ParenExpr:
		return sourceStringExpr(typed.X, httpAliases)
	case *ast.SelectorExpr:
		ident, ok := typed.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		if _, ok := httpAliases[ident.Name]; !ok {
			return "", false
		}
		method, ok := sourceHTTPMethodConstant(typed.Sel.Name)
		return method, ok
	default:
		return "", false
	}
}

func sourceHTTPMethodConstant(name string) (string, bool) {
	switch name {
	case "MethodGet":
		return "GET", true
	case "MethodHead":
		return "HEAD", true
	case "MethodPost":
		return "POST", true
	case "MethodPut":
		return "PUT", true
	case "MethodPatch":
		return "PATCH", true
	case "MethodDelete":
		return "DELETE", true
	case "MethodConnect":
		return "CONNECT", true
	case "MethodOptions":
		return "OPTIONS", true
	case "MethodTrace":
		return "TRACE", true
	default:
		return "", false
	}
}

func sourceHTTPMethodPath(pattern string) (string, string, bool) {
	fields := strings.Fields(pattern)
	if len(fields) != 2 {
		return "", "", false
	}
	method := strings.ToUpper(fields[0])
	path := fields[1]
	if !validEndpointHTTPMethod(method) || !validEndpointHTTPPath(path) {
		return "", "", false
	}
	return method, path, true
}

func sourceGRPCAdapterDeclaration(
	field *ast.Field,
	target endpointSourceTarget,
	source string,
	twillAliases map[string]struct{},
) (EndpointDeclaration, bool) {
	if !sourceTwillSelector(field.Type, twillAliases, "GRPCAdapter") {
		return EndpointDeclaration{}, false
	}
	if field.Tag == nil {
		return EndpointDeclaration{}, false
	}
	tag, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return EndpointDeclaration{}, false
	}
	values := sourceAdapterTagValues(reflect.StructTag(tag).Get("twill"))
	adapter := codegen.GRPCAdapter{
		Listener: values["listener"],
		Service:  values["service"],
		Method:   values["method"],
	}
	if adapter.Listener != target.Listener || !validEndpointGRPCAdapter(adapter) {
		return EndpointDeclaration{}, false
	}
	return EndpointDeclaration{
		Kind:      "source",
		Protocol:  "grpc",
		Component: target.Component,
		Listener:  adapter.Listener,
		Service:   adapter.Service,
		Method:    adapter.Method,
		Path:      "/" + adapter.Service + "/" + adapter.Method,
		Source:    source,
	}, true
}

func sourceTwillSelector(expr ast.Expr, twillAliases map[string]struct{}, name string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	_, ok = twillAliases[ident.Name]
	return ok
}

func sourceAdapterTagValues(value string) map[string]string {
	values := map[string]string{}
	for _, part := range strings.Split(value, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return values
}

func validEndpointHTTPMethod(method string) bool {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "CONNECT", "OPTIONS", "TRACE":
		return true
	default:
		return false
	}
}

func validEndpointHTTPPath(path string) bool {
	if path == "" || !strings.HasPrefix(path, "/") {
		return false
	}
	return !strings.ContainsAny(path, "\x00\r\n\t ?#")
}

func validEndpointHTTPAdapter(adapter codegen.HTTPAdapter) bool {
	if adapter.Listener == "" || adapter.Method == "" || adapter.Path == "" {
		return false
	}
	if strings.ContainsAny(adapter.Listener+adapter.Method+adapter.Path, "\x00\r\n\t") {
		return false
	}
	return validEndpointHTTPMethod(strings.ToUpper(adapter.Method)) && validEndpointHTTPPath(adapter.Path)
}

func validEndpointGRPCAdapter(adapter codegen.GRPCAdapter) bool {
	if adapter.Listener == "" || adapter.Service == "" || adapter.Method == "" {
		return false
	}
	if strings.ContainsAny(adapter.Listener+adapter.Service+adapter.Method, "/ \x00\r\n\t") {
		return false
	}
	if !token.IsIdentifier(adapter.Listener) || !token.IsIdentifier(adapter.Method) {
		return false
	}
	for _, part := range strings.Split(adapter.Service, ".") {
		if !token.IsIdentifier(part) {
			return false
		}
	}
	return true
}

func sortEndpointDeclarations(declarations []EndpointDeclaration) {
	sort.Slice(declarations, func(i, j int) bool {
		return endpointDeclarationLess(declarations[i], declarations[j])
	})
}

func dedupeEndpointDeclarations(declarations []EndpointDeclaration) []EndpointDeclaration {
	dedupedByKey := map[string]EndpointDeclaration{}
	for _, declaration := range declarations {
		key := endpointDeclarationKey(declaration)
		existing, ok := dedupedByKey[key]
		if !ok || endpointDeclarationPrecedence(declaration) > endpointDeclarationPrecedence(existing) {
			dedupedByKey[key] = declaration
		}
	}
	deduped := make([]EndpointDeclaration, 0, len(dedupedByKey))
	for _, declaration := range dedupedByKey {
		deduped = append(deduped, declaration)
	}
	sortEndpointDeclarations(deduped)
	return deduped
}

func endpointDeclarationKey(declaration EndpointDeclaration) string {
	protocol := declaration.Protocol
	if protocol == "" {
		protocol = "http"
	}
	return strings.Join([]string{
		declaration.Component,
		declaration.Listener,
		protocol,
		declaration.Service,
		strings.ToUpper(declaration.Method),
		declaration.Path,
	}, "\x00")
}

func endpointDeclarationPrecedence(declaration EndpointDeclaration) int {
	switch declaration.Kind {
	case "":
		return 3
	case "adapter":
		return 2
	case "source":
		return 1
	default:
		return 0
	}
}

func endpointDeclarationLess(a, b EndpointDeclaration) bool {
	if a.Component != b.Component {
		return a.Component < b.Component
	}
	if a.Listener != b.Listener {
		return a.Listener < b.Listener
	}
	if a.Method != b.Method {
		return a.Method < b.Method
	}
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.Protocol != b.Protocol {
		return a.Protocol < b.Protocol
	}
	if a.Service != b.Service {
		return a.Service < b.Service
	}
	return a.Source < b.Source
}

func inspectEndpointContractFile(
	rootDir string,
	path string,
	graph *Graph,
) (EndpointContract, EndpointDeclaration, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return EndpointContract{}, EndpointDeclaration{}, false, fmt.Errorf("stat endpoint contract %s: %w", path, err)
	}
	if info.Size() > maxEndpointContractBytes {
		return EndpointContract{}, EndpointDeclaration{}, false, fmt.Errorf(
			"endpoint contract %s is %d bytes, maximum is %d bytes",
			path,
			info.Size(),
			maxEndpointContractBytes,
		)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return EndpointContract{}, EndpointDeclaration{}, false, fmt.Errorf("read endpoint contract %s: %w", path, err)
	}
	values := endpointContractFields(string(data))
	component := resolveEndpointContractComponent(values["Component"], graph)
	if component == "" {
		return EndpointContract{}, EndpointDeclaration{}, false, nil
	}
	contract := EndpointContract{
		Component: component,
		Listener:  values["Listener"],
		Method:    strings.ToUpper(values["Method"]),
		Path:      values["Path"],
		Source:    cleanPath(rootDir, path),
	}
	if !validEndpointContract(contract, graph) {
		return EndpointContract{}, EndpointDeclaration{}, false, nil
	}
	declaration := endpointDeclarationFromContract(contract, values)
	return contract, declaration, true, nil
}

func endpointContractFields(contents string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "- "), ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		switch key {
		case "Component", "Listener", "Method", "Path":
			values[key] = strings.TrimSpace(value)
		case "RequestType", "Request Type":
			values["RequestType"] = strings.TrimSpace(value)
		case "ResponseType", "Response Type":
			values["ResponseType"] = strings.TrimSpace(value)
		case "Auth", "Middleware", "Compatibility":
			values[key] = strings.TrimSpace(value)
		}
	}
	return values
}

func endpointDeclarationFromContract(
	contract EndpointContract,
	values map[string]string,
) EndpointDeclaration {
	return EndpointDeclaration{
		Component:     contract.Component,
		Listener:      contract.Listener,
		Method:        contract.Method,
		Path:          contract.Path,
		RequestType:   safeEndpointTypeRef(values["RequestType"]),
		ResponseType:  safeEndpointTypeRef(values["ResponseType"]),
		Auth:          safeEndpointMarker(values["Auth"]),
		Middleware:    safeEndpointMarker(values["Middleware"]),
		Compatibility: safeEndpointMarker(values["Compatibility"]),
		Source:        contract.Source,
	}
}

func safeEndpointMarker(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "declared"
}

func safeEndpointTypeRef(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, r := range value {
		valid := r >= 'A' && r <= 'Z' ||
			r >= 'a' && r <= 'z' ||
			r >= '0' && r <= '9' ||
			r == '_' || r == '.' || r == '/' || r == '*' || r == '[' || r == ']'
		if !valid {
			return "declared"
		}
	}
	return value
}

func resolveEndpointContractComponent(component string, graph *Graph) string {
	component = strings.TrimSpace(component)
	if component == "" {
		return ""
	}
	for _, candidate := range graph.Components {
		if candidate.Name == component {
			return candidate.Name
		}
	}
	matches := []string{}
	for _, candidate := range graph.Components {
		if strings.HasSuffix(candidate.Name, "/"+component) {
			matches = append(matches, candidate.Name)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

func validEndpointContract(contract EndpointContract, graph *Graph) bool {
	if contract.Listener == "" || contract.Method == "" || contract.Path == "" {
		return false
	}
	if strings.ContainsAny(contract.Listener+contract.Method+contract.Path, "\x00\r\n\t") {
		return false
	}
	if !strings.HasPrefix(contract.Path, "/") {
		return false
	}
	for _, component := range graph.Components {
		if component.Name != contract.Component {
			continue
		}
		for _, listener := range component.Listeners {
			if listener == contract.Listener {
				return true
			}
		}
	}
	return false
}

// MiddlewareContextForGraph returns middleware-safe context from a graph.
func MiddlewareContextForGraph(graph *Graph) MiddlewareContext {
	return MiddlewareContext{
		SchemaVersion: "twill.app.middleware.v1",
		Components:    ComponentNames(graph),
		Middleware:    []MiddlewareBinding{},
		Files:         []string{},
		Limitations: []string{
			"Middleware context reports only references to the standard runtime/middleware package.",
			"Handler logic, auth rules, header values, request bodies, response bodies, and error details are not exposed.",
			"Component matching is inferred from component structs in the same source file and can miss shared middleware packages.",
		},
	}
}

func inspectMiddlewareContext(ctx context.Context, opts GraphOptions, graph *Graph) (MiddlewareContext, error) {
	middleware := MiddlewareContextForGraph(graph)
	bindings, files, err := inspectMiddlewareBindings(ctx, opts, graph)
	if err != nil {
		return MiddlewareContext{}, err
	}
	middleware.Middleware = bindings
	middleware.Files = files
	return middleware, nil
}

func inspectMiddlewareBindings(
	ctx context.Context,
	opts GraphOptions,
	graph *Graph,
) ([]MiddlewareBinding, []string, error) {
	dir := packageLoadDir(opts)
	rootDir, err := inspectionRootDir(opts)
	if err != nil {
		return nil, nil, err
	}
	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = []string{"."}
	}

	cfg := &packages.Config{
		Context: ctx,
		Dir:     dir,
		Mode:    packages.NeedName | packages.NeedFiles,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, nil, err
	}

	componentSet := make(map[string]struct{}, len(graph.Components))
	for _, component := range graph.Components {
		componentSet[component.Name] = struct{}{}
	}

	bindings := []MiddlewareBinding{}
	filesRead := map[string]struct{}{}
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			return nil, nil, packageErrors(pkg)
		}
		for _, filename := range pkg.GoFiles {
			if filepath.Base(filename) == "twill_gen.go" {
				continue
			}
			cleanFilename := cleanPath(rootDir, filename)
			fileBindings, err := inspectMiddlewareBindingsInFile(pkg.PkgPath, componentSet, filename, cleanFilename)
			if err != nil {
				return nil, nil, err
			}
			if len(fileBindings) == 0 {
				continue
			}
			filesRead[cleanFilename] = struct{}{}
			bindings = append(bindings, fileBindings...)
		}
	}

	bindings = dedupeMiddlewareBindings(bindings)
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].Component != bindings[j].Component {
			return bindings[i].Component < bindings[j].Component
		}
		if bindings[i].Category != bindings[j].Category {
			return bindings[i].Category < bindings[j].Category
		}
		if bindings[i].Name != bindings[j].Name {
			return bindings[i].Name < bindings[j].Name
		}
		return bindings[i].Source < bindings[j].Source
	})

	files := make([]string, 0, len(filesRead))
	for file := range filesRead {
		files = append(files, file)
	}
	sort.Strings(files)
	return bindings, files, nil
}

func inspectMiddlewareBindingsInFile(
	pkgPath string,
	components map[string]struct{},
	filename string,
	cleanFilename string,
) ([]MiddlewareBinding, error) {
	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse middleware source %s: %w", filename, err)
	}
	aliases := middlewareImportAliases(file)
	if len(aliases) == 0 {
		return []MiddlewareBinding{}, nil
	}
	fileComponents := middlewareComponentsInFile(pkgPath, components, file)
	bindings := []MiddlewareBinding{}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		if _, ok := aliases[ident.Name]; !ok && !observabilityMethodSelector(selector.Sel.Name) {
			return true
		}
		category := middlewareCategory(selector.Sel.Name)
		if category == "" {
			return true
		}
		if len(fileComponents) == 0 {
			bindings = append(bindings, MiddlewareBinding{
				Name:     selector.Sel.Name,
				Category: category,
				Source:   cleanFilename,
			})
			return true
		}
		for _, component := range fileComponents {
			bindings = append(bindings, MiddlewareBinding{
				Component: component,
				Name:      selector.Sel.Name,
				Category:  category,
				Source:    cleanFilename,
			})
		}
		return true
	})
	return bindings, nil
}

func middlewareImportAliases(file *ast.File) map[string]struct{} {
	return importAliases(file, middlewareImportPath, "middleware")
}

func middlewareComponentsInFile(
	pkgPath string,
	components map[string]struct{},
	file *ast.File,
) []string {
	names := []string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range structType.Fields.List {
				if len(field.Names) != 0 {
					continue
				}
				base, arg := genericEmbedding(field.Type)
				if base != "Implements" {
					continue
				}
				component := componentNameForConfig(pkgPath, arg)
				if _, ok := components[component]; ok {
					names = appendUniqueString(names, component)
				}
			}
		}
	}
	sort.Strings(names)
	return names
}

func middlewareCategory(name string) string {
	switch name {
	case "Timeout":
		return "timeout"
	case "RequestID", "RequestIDWithGenerator", "RequestIDFromContext":
		return "request_id"
	case "HandleErrors", "WriteError", "StatusError":
		return "structured_error"
	case "RetryAllowed", "RequireIdempotencyKey":
		return "retry"
	case "RateLimit":
		return "rate_limit"
	case "CircuitBreaker":
		return "circuit_breaker"
	case "AuthHook":
		return "auth"
	default:
		return ""
	}
}

func dedupeMiddlewareBindings(bindings []MiddlewareBinding) []MiddlewareBinding {
	deduped := []MiddlewareBinding{}
	seen := map[MiddlewareBinding]struct{}{}
	for _, binding := range bindings {
		if _, ok := seen[binding]; ok {
			continue
		}
		seen[binding] = struct{}{}
		deduped = append(deduped, binding)
	}
	return deduped
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// ResourcesContextForGraph returns safe resource context from a graph.
func ResourcesContextForGraph(graph *Graph) ResourcesContext {
	resources := ResourceSurfaces(graph)
	return ResourcesContext{
		SchemaVersion: "twill.app.resources.v1",
		Resources:     resources,
		Graph:         buildResourceGraph(resources, ComponentNames(graph)),
		Files:         []string{},
		Limitations: []string{
			"Twill listeners are modeled as static resource surfaces from generated metadata.",
			"Explicit resource declarations are read from " + resourceDeclarationsDir + "/*.md using whitelisted metadata fields only.",
			"Known backing resource types in component implementations are modeled by kind, type, component, and source file only.",
			"Field names, config keys, secret names, and secret values are not inferred from configuration or source code.",
			"Free-form resource text, provider resource names, connection strings, credentials, and binding values are not exposed.",
			"Unknown local wrappers and dynamic resource construction may be missed unless declared in " + resourceDeclarationsDir + ".",
		},
	}
}

func buildResourceGraph(resources []ResourceSurface, components []string) ResourceGraph {
	const schemaVersion = "twill.app.resources.graph.v1"
	nodeSet := map[string]ResourceGraphNode{}
	edges := []ResourceGraphEdge{}

	for _, component := range components {
		nodeSet[component] = ResourceGraphNode{
			ID:   component,
			Kind: "component",
			Name: component,
		}
	}

	for _, resource := range resources {
		resourceID := resource.Component + "/" + resource.Name
		nodeSet[resourceID] = ResourceGraphNode{
			ID:        resourceID,
			Kind:      "resource",
			Name:      resource.Name,
			Component: resource.Component,
			Resource:  resource.Kind,
		}
		if resource.Component != "" {
			edges = append(edges, ResourceGraphEdge{
				Source: resource.Component,
				Target: resourceID,
				Kind:   "component_uses_resource",
			})
		}
	}

	nodes := make([]ResourceGraphNode, 0, len(nodeSet))
	for _, node := range nodeSet {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Kind != nodes[j].Kind {
			return nodes[i].Kind < nodes[j].Kind
		}
		return nodes[i].ID < nodes[j].ID
	})
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Source != edges[j].Source {
			return edges[i].Source < edges[j].Source
		}
		if edges[i].Target != edges[j].Target {
			return edges[i].Target < edges[j].Target
		}
		return edges[i].Kind < edges[j].Kind
	})
	return ResourceGraph{
		SchemaVersion: schemaVersion,
		Nodes:         nodes,
		Edges:         edges,
	}
}

func inspectResourcesContext(ctx context.Context, opts GraphOptions, graph *Graph) (ResourcesContext, error) {
	resources := ResourcesContextForGraph(graph)
	sourceResources, files, err := inspectSourceResources(ctx, opts, graph)
	if err != nil {
		return ResourcesContext{}, err
	}
	declaredResources, declarationFiles, err := inspectResourceDeclarations(opts, graph)
	if err != nil {
		return ResourcesContext{}, err
	}
	resources.Resources = append(resources.Resources, sourceResources...)
	resources.Resources = append(resources.Resources, declaredResources...)
	resources.Resources = uniqueResourceSurfaces(resources.Resources)
	sortResourceSurfaces(resources.Resources)
	resources.Graph = buildResourceGraph(resources.Resources, ComponentNames(graph))
	resources.Files = mergeStringSlices(files, declarationFiles)
	return resources, nil
}

func inspectResourceDeclarations(
	opts GraphOptions,
	graph *Graph,
) ([]ResourceSurface, []string, error) {
	rootDir, err := inspectionRootDir(opts)
	if err != nil {
		return nil, nil, err
	}
	declarationDir := filepath.Join(rootDir, resourceDeclarationsDir)
	info, err := os.Stat(declarationDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []ResourceSurface{}, []string{}, nil
		}
		return nil, nil, fmt.Errorf("stat resource declarations %s: %w", declarationDir, err)
	}
	if !info.IsDir() {
		return []ResourceSurface{}, []string{}, nil
	}

	resources := []ResourceSurface{}
	files := []string{}
	err = filepath.WalkDir(declarationDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		resource, ok, err := inspectResourceDeclarationFile(rootDir, path, graph)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		resources = append(resources, resource)
		files = append(files, resource.Source)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("walk resource declarations %s: %w", declarationDir, err)
	}
	resources = uniqueResourceSurfaces(resources)
	sortResourceSurfaces(resources)
	sort.Strings(files)
	return resources, files, nil
}

func inspectResourceDeclarationFile(
	rootDir string,
	path string,
	graph *Graph,
) (ResourceSurface, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return ResourceSurface{}, false, fmt.Errorf("stat resource declaration %s: %w", path, err)
	}
	if info.Size() > maxResourceDeclBytes {
		return ResourceSurface{}, false, fmt.Errorf(
			"resource declaration %s is %d bytes, maximum is %d bytes",
			path,
			info.Size(),
			maxResourceDeclBytes,
		)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ResourceSurface{}, false, fmt.Errorf("read resource declaration %s: %w", path, err)
	}
	values := resourceDeclarationFields(string(data))
	component := resolveEndpointContractComponent(values["Component"], graph)
	if component == "" {
		return ResourceSurface{}, false, nil
	}
	kind := canonicalResourceKind(values["Kind"])
	if kind == "" {
		return ResourceSurface{}, false, nil
	}
	name := safeResourceName(values["Name"])
	if name == "" {
		name = kind
	}
	resource := ResourceSurface{
		Name:      name,
		Kind:      kind,
		Component: component,
		Type:      safeResourceTypeRef(values["Type"]),
		Lifecycle: safeResourceLifecycle(values["Lifecycle"]),
		Binding:   safeResourceBinding(values["Binding"]),
		Provider:  safeConfigMarker(values["Provider"]),
		Required:  parseConfigRequired(values["Required"]),
		Source:    cleanPath(rootDir, path),
	}
	return resource, true, nil
}

func resourceDeclarationFields(contents string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "- "), ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		switch key {
		case "Name", "Kind", "Component", "Lifecycle", "Binding", "Provider", "Required":
			values[key] = strings.TrimSpace(value)
		case "Type", "ResourceType", "Resource Type":
			values["Type"] = strings.TrimSpace(value)
		}
	}
	return values
}

func canonicalResourceKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "sql", "db", "database", "postgres", "postgresql", "mysql":
		return "database"
	case "redis", "cache", "memcache", "memcached":
		return "cache"
	case "pubsub", "pub/sub", "pub-sub":
		return "pubsub"
	case "topic":
		return "topic"
	case "subscription", "subscriber":
		return "subscription"
	case "queue", "stream":
		return "queue"
	case "cron", "scheduler", "schedule", "job":
		return "cron"
	case "object-storage", "object_storage", "bucket", "s3", "gcs":
		return "object_storage"
	case "secret", "secrets":
		return "secret"
	default:
		return ""
	}
}

func safeResourceName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, r := range value {
		valid := r >= 'A' && r <= 'Z' ||
			r >= 'a' && r <= 'z' ||
			r >= '0' && r <= '9' ||
			r == '_' || r == '-' || r == '.' || r == '/'
		if !valid {
			return "declared"
		}
	}
	return value
}

func safeResourceTypeRef(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, r := range value {
		valid := r >= 'A' && r <= 'Z' ||
			r >= 'a' && r <= 'z' ||
			r >= '0' && r <= '9' ||
			r == '_' || r == '-' || r == '.' || r == '/' || r == '*' || r == '[' || r == ']'
		if !valid {
			return "declared"
		}
	}
	return value
}

func safeResourceLifecycle(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "owned", "shared", "external", "ephemeral", "imported":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func safeResourceBinding(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "managed", "external", "config", "env", "secret", "kubernetes", "terraform", "helm", "manual":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func inspectSourceResources(
	ctx context.Context,
	opts GraphOptions,
	graph *Graph,
) ([]ResourceSurface, []string, error) {
	dir := packageLoadDir(opts)
	rootDir, err := inspectionRootDir(opts)
	if err != nil {
		return nil, nil, err
	}
	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = []string{"."}
	}

	cfg := &packages.Config{
		Context: ctx,
		Dir:     dir,
		Mode:    packages.NeedName | packages.NeedFiles,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, nil, err
	}

	componentSet := make(map[string]struct{}, len(graph.Components))
	for _, component := range graph.Components {
		componentSet[component.Name] = struct{}{}
	}

	resources := []ResourceSurface{}
	filesRead := map[string]struct{}{}
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			return nil, nil, packageErrors(pkg)
		}
		for _, filename := range pkg.GoFiles {
			if filepath.Base(filename) == "twill_gen.go" {
				continue
			}
			cleanFilename := cleanPath(rootDir, filename)
			filesRead[cleanFilename] = struct{}{}
			fileResources, err := inspectSourceResourcesInFile(pkg.PkgPath, componentSet, filename, cleanFilename)
			if err != nil {
				return nil, nil, err
			}
			resources = append(resources, fileResources...)
		}
	}

	resources = uniqueResourceSurfaces(resources)
	sortResourceSurfaces(resources)

	files := make([]string, 0, len(filesRead))
	for file := range filesRead {
		files = append(files, file)
	}
	sort.Strings(files)
	return resources, files, nil
}

func inspectSourceResourcesInFile(
	pkgPath string,
	components map[string]struct{},
	filename string,
	cleanFilename string,
) ([]ResourceSurface, error) {
	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse resource source %s: %w", filename, err)
	}
	imports := importPathsByName(file)

	resources := []ResourceSurface{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			resources = append(
				resources,
				resourceSurfacesForStruct(pkgPath, components, imports, cleanFilename, structType)...,
			)
		}
	}
	return resources, nil
}

func resourceSurfacesForStruct(
	pkgPath string,
	components map[string]struct{},
	imports map[string]string,
	source string,
	structType *ast.StructType,
) []ResourceSurface {
	component := ""
	for _, field := range structType.Fields.List {
		if len(field.Names) != 0 {
			continue
		}
		base, arg := genericEmbedding(field.Type)
		if base == "Implements" {
			component = componentNameForConfig(pkgPath, arg)
			break
		}
	}
	if component == "" {
		return []ResourceSurface{}
	}
	if _, ok := components[component]; !ok {
		return []ResourceSurface{}
	}

	resources := []ResourceSurface{}
	for _, field := range structType.Fields.List {
		if base, _ := genericEmbedding(field.Type); base != "" {
			continue
		}
		kind, resourceType, ok := resourceKindForType(field.Type, imports)
		if !ok {
			continue
		}
		resources = append(resources, ResourceSurface{
			Name:      resourceType,
			Kind:      kind,
			Component: component,
			Type:      resourceType,
			Source:    source,
		})
	}
	return resources
}

func importPathsByName(file *ast.File) map[string]string {
	imports := map[string]string{}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || importPath == "" {
			continue
		}
		if spec.Name != nil {
			if spec.Name.Name == "_" || spec.Name.Name == "." {
				continue
			}
			imports[spec.Name.Name] = importPath
			continue
		}
		imports[packageNameFromImportPath(importPath)] = importPath
	}
	return imports
}

func packageNameFromImportPath(importPath string) string {
	switch importPath {
	case "github.com/go-redis/redis/v8",
		"github.com/redis/go-redis/v9":
		return "redis"
	case "github.com/robfig/cron/v3":
		return "cron"
	}
	if idx := strings.LastIndex(importPath, "/"); idx >= 0 {
		return importPath[idx+1:]
	}
	return importPath
}

func resourceKindForType(expr ast.Expr, imports map[string]string) (string, string, bool) {
	switch typed := expr.(type) {
	case *ast.StarExpr:
		return resourceKindForType(typed.X, imports)
	case *ast.SelectorExpr:
		alias, ok := typed.X.(*ast.Ident)
		if !ok {
			return "", "", false
		}
		importPath := imports[alias.Name]
		if importPath == "" {
			return "", "", false
		}
		resourceType := importPath + "." + typed.Sel.Name
		kind, ok := resourceKindForKnownType(resourceType)
		return kind, resourceType, ok
	default:
		return "", "", false
	}
}

func resourceKindForKnownType(resourceType string) (string, bool) {
	switch resourceType {
	case "database/sql.DB",
		"github.com/jackc/pgx/v4/pgxpool.Pool",
		"github.com/jackc/pgx/v5/pgxpool.Pool",
		"github.com/jmoiron/sqlx.DB",
		"gorm.io/gorm.DB",
		"github.com/nxsky/twill.Database":
		return "database", true
	case "github.com/bradfitz/gomemcache/memcache.Client",
		"github.com/go-redis/redis/v8.Client",
		"github.com/go-redis/redis/v8.ClusterClient",
		"github.com/go-redis/redis/v8.Ring",
		"github.com/go-redis/redis/v8.UniversalClient",
		"github.com/patrickmn/go-cache.Cache",
		"github.com/redis/go-redis/v9.Client",
		"github.com/redis/go-redis/v9.ClusterClient",
		"github.com/redis/go-redis/v9.Ring",
		"github.com/redis/go-redis/v9.UniversalClient",
		"github.com/nxsky/twill.Cache":
		return "cache", true
	case "cloud.google.com/go/pubsub.Client",
		"github.com/nxsky/twill.PubSub":
		return "pubsub", true
	case "cloud.google.com/go/pubsub.Subscription":
		return "subscription", true
	case "cloud.google.com/go/pubsub.Topic",
		"github.com/aws/aws-sdk-go-v2/service/sns.Client":
		return "topic", true
	case "github.com/aws/aws-sdk-go-v2/service/sqs.Client",
		"github.com/rabbitmq/amqp091-go.Channel",
		"github.com/segmentio/kafka-go.Conn",
		"github.com/segmentio/kafka-go.Reader",
		"github.com/segmentio/kafka-go.Writer",
		"github.com/streadway/amqp.Channel":
		return "queue", true
	case "cloud.google.com/go/storage.BucketHandle",
		"cloud.google.com/go/storage.Client",
		"github.com/aws/aws-sdk-go-v2/service/s3.Client":
		return "object_storage", true
	case "github.com/robfig/cron/v3.Cron",
		"github.com/nxsky/twill.Cron":
		return "cron", true
	case "github.com/nxsky/twill.Secret":
		return "secret", true
	default:
		return "", false
	}
}

func uniqueResourceSurfaces(resources []ResourceSurface) []ResourceSurface {
	unique := make([]ResourceSurface, 0, len(resources))
	seen := map[ResourceSurface]struct{}{}
	for _, resource := range resources {
		if _, ok := seen[resource]; ok {
			continue
		}
		seen[resource] = struct{}{}
		unique = append(unique, resource)
	}
	return unique
}

// ConfigContextForGraph returns config-safe context from a graph.
func ConfigContextForGraph(graph *Graph) ConfigContext {
	return ConfigContext{
		SchemaVersion: "twill.app.config.v1",
		Components:    ComponentNames(graph),
		Schemas:       []ConfigSchema{},
		Bindings:      []ConfigBinding{},
		Files:         []string{},
		Limitations: []string{
			"Config values are not read or exposed to avoid leaking secrets.",
			"Component config schemas report only twill.WithConfig type names, binding kinds, and source files.",
			"Config field names, TOML keys, environment variable names, ConfigMap names, Secret names, and config values are not exposed.",
		},
	}
}

func inspectConfigContext(ctx context.Context, opts GraphOptions, graph *Graph) (ConfigContext, error) {
	config := ConfigContextForGraph(graph)
	schemas, sourceFiles, err := inspectConfigSchemas(ctx, opts, graph)
	if err != nil {
		return ConfigContext{}, err
	}
	bindings, bindingFiles, err := inspectConfigBindings(opts, graph, schemas)
	if err != nil {
		return ConfigContext{}, err
	}
	config.Schemas = schemas
	config.Bindings = bindings
	config.Files = mergeStringSlices(sourceFiles, bindingFiles)
	return config, nil
}

func inspectConfigSchemas(
	ctx context.Context,
	opts GraphOptions,
	graph *Graph,
) ([]ConfigSchema, []string, error) {
	dir := packageLoadDir(opts)
	rootDir, err := inspectionRootDir(opts)
	if err != nil {
		return nil, nil, err
	}
	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = []string{"."}
	}

	cfg := &packages.Config{
		Context: ctx,
		Dir:     dir,
		Mode:    packages.NeedName | packages.NeedFiles,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, nil, err
	}

	componentSet := make(map[string]struct{}, len(graph.Components))
	for _, component := range graph.Components {
		componentSet[component.Name] = struct{}{}
	}

	schemas := []ConfigSchema{}
	filesRead := map[string]struct{}{}
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			return nil, nil, packageErrors(pkg)
		}
		for _, filename := range pkg.GoFiles {
			if filepath.Base(filename) == "twill_gen.go" {
				continue
			}
			cleanFilename := cleanPath(rootDir, filename)
			filesRead[cleanFilename] = struct{}{}
			fileSchemas, err := inspectConfigSchemasInFile(pkg.PkgPath, componentSet, filename, cleanFilename)
			if err != nil {
				return nil, nil, err
			}
			schemas = append(schemas, fileSchemas...)
		}
	}

	sort.Slice(schemas, func(i, j int) bool {
		if schemas[i].Component != schemas[j].Component {
			return schemas[i].Component < schemas[j].Component
		}
		if schemas[i].ConfigType != schemas[j].ConfigType {
			return schemas[i].ConfigType < schemas[j].ConfigType
		}
		return schemas[i].Source < schemas[j].Source
	})

	files := make([]string, 0, len(filesRead))
	for file := range filesRead {
		files = append(files, file)
	}
	sort.Strings(files)
	return schemas, files, nil
}

func inspectConfigBindings(
	opts GraphOptions,
	graph *Graph,
	schemas []ConfigSchema,
) ([]ConfigBinding, []string, error) {
	rootDir, err := inspectionRootDir(opts)
	if err != nil {
		return nil, nil, err
	}
	bindingDir := filepath.Join(rootDir, configBindingsDir)
	info, err := os.Stat(bindingDir)
	if err != nil {
		if os.IsNotExist(err) {
			return missingConfigBindings(schemas, nil), []string{}, nil
		}
		return nil, nil, fmt.Errorf("stat config bindings %s: %w", bindingDir, err)
	}
	if !info.IsDir() {
		return missingConfigBindings(schemas, nil), []string{}, nil
	}

	bindings := []ConfigBinding{}
	files := []string{}
	err = filepath.WalkDir(bindingDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		binding, ok, err := inspectConfigBindingFile(rootDir, path, graph)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		bindings = append(bindings, binding)
		files = append(files, binding.Source)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("walk config bindings %s: %w", bindingDir, err)
	}

	bindings = append(bindings, missingConfigBindings(schemas, bindings)...)
	sortConfigBindings(bindings)
	sort.Strings(files)
	return bindings, files, nil
}

func inspectConfigBindingFile(
	rootDir string,
	path string,
	graph *Graph,
) (ConfigBinding, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return ConfigBinding{}, false, fmt.Errorf("stat config binding %s: %w", path, err)
	}
	if info.Size() > maxConfigBindingBytes {
		return ConfigBinding{}, false, fmt.Errorf(
			"config binding %s is %d bytes, maximum is %d bytes",
			path,
			info.Size(),
			maxConfigBindingBytes,
		)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ConfigBinding{}, false, fmt.Errorf("read config binding %s: %w", path, err)
	}
	values := configBindingFields(string(data))
	component := resolveEndpointContractComponent(values["Component"], graph)
	if component == "" {
		return ConfigBinding{}, false, nil
	}
	kind := safeConfigBindingKind(values["Kind"])
	if kind == "" {
		return ConfigBinding{}, false, nil
	}
	return ConfigBinding{
		Component:  component,
		ConfigType: safeEndpointTypeRef(values["ConfigType"]),
		Kind:       kind,
		Provider:   safeConfigMarker(values["Provider"]),
		Lifecycle:  safeConfigMarker(values["Lifecycle"]),
		Required:   parseConfigRequired(values["Required"]),
		Source:     cleanPath(rootDir, path),
	}, true, nil
}

func configBindingFields(contents string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "- "), ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		switch key {
		case "Component", "Kind", "Provider", "Lifecycle", "Required":
			values[key] = strings.TrimSpace(value)
		case "ConfigType", "Config Type":
			values["ConfigType"] = strings.TrimSpace(value)
		}
	}
	return values
}

func missingConfigBindings(schemas []ConfigSchema, bindings []ConfigBinding) []ConfigBinding {
	bound := map[string]struct{}{}
	componentBound := map[string]struct{}{}
	for _, binding := range bindings {
		if binding.ConfigType == "" {
			componentBound[binding.Component] = struct{}{}
			continue
		}
		bound[configBindingKey(binding.Component, binding.ConfigType)] = struct{}{}
	}
	missing := []ConfigBinding{}
	for _, schema := range schemas {
		if _, ok := componentBound[schema.Component]; ok {
			continue
		}
		if _, ok := bound[configBindingKey(schema.Component, schema.ConfigType)]; ok {
			continue
		}
		missing = append(missing, ConfigBinding{
			Component:  schema.Component,
			ConfigType: schema.ConfigType,
			Kind:       "missing",
			Source:     schema.Source,
		})
	}
	return missing
}

func configBindingKey(component, configType string) string {
	return component + "\x00" + configType
}

func safeConfigBindingKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "file", "env", "configmap", "secret", "remote":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func safeConfigMarker(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "declared"
}

func parseConfigRequired(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "required":
		return true
	default:
		return false
	}
}

func sortConfigBindings(bindings []ConfigBinding) {
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].Component != bindings[j].Component {
			return bindings[i].Component < bindings[j].Component
		}
		if bindings[i].ConfigType != bindings[j].ConfigType {
			return bindings[i].ConfigType < bindings[j].ConfigType
		}
		if bindings[i].Kind != bindings[j].Kind {
			return bindings[i].Kind < bindings[j].Kind
		}
		return bindings[i].Source < bindings[j].Source
	})
}

func mergeStringSlices(a, b []string) []string {
	seen := map[string]struct{}{}
	merged := make([]string, 0, len(a)+len(b))
	for _, value := range append(append([]string{}, a...), b...) {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		merged = append(merged, value)
	}
	sort.Strings(merged)
	return merged
}

func inspectConfigSchemasInFile(
	pkgPath string,
	components map[string]struct{},
	filename string,
	cleanFilename string,
) ([]ConfigSchema, error) {
	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse config schema source %s: %w", filename, err)
	}

	schemas := []ConfigSchema{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			schema, ok := configSchemaForStruct(pkgPath, components, cleanFilename, structType)
			if ok {
				schemas = append(schemas, schema)
			}
		}
	}
	return schemas, nil
}

func configSchemaForStruct(
	pkgPath string,
	components map[string]struct{},
	source string,
	structType *ast.StructType,
) (ConfigSchema, bool) {
	component := ""
	configType := ""
	for _, field := range structType.Fields.List {
		if len(field.Names) != 0 {
			continue
		}
		base, arg := genericEmbedding(field.Type)
		switch base {
		case "Implements":
			component = componentNameForConfig(pkgPath, arg)
		case "WithConfig":
			configType = astTypeName(arg)
		}
	}
	if component == "" || configType == "" {
		return ConfigSchema{}, false
	}
	if _, ok := components[component]; !ok {
		return ConfigSchema{}, false
	}
	return ConfigSchema{
		Component:  component,
		ConfigType: configType,
		Source:     source,
	}, true
}

func genericEmbedding(expr ast.Expr) (string, ast.Expr) {
	switch typed := expr.(type) {
	case *ast.IndexExpr:
		return selectorName(typed.X), typed.Index
	case *ast.IndexListExpr:
		if len(typed.Indices) == 0 {
			return selectorName(typed.X), nil
		}
		return selectorName(typed.X), typed.Indices[0]
	default:
		return "", nil
	}
}

func selectorName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typed.Sel.Name
	default:
		return ""
	}
}

func componentNameForConfig(pkgPath string, expr ast.Expr) string {
	name := astTypeName(expr)
	if name == "" || strings.Contains(name, ".") {
		return ""
	}
	return pkgPath + "/" + name
}

func astTypeName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		pkg := astTypeName(typed.X)
		if pkg == "" {
			return typed.Sel.Name
		}
		return pkg + "." + typed.Sel.Name
	case *ast.StarExpr:
		return "*" + astTypeName(typed.X)
	default:
		return ""
	}
}

// GeneratedContextForGraph returns generated metadata context from a graph.
func GeneratedContextForGraph(graph *Graph) GeneratedContext {
	return GeneratedContext{
		SchemaVersion: "twill.app.generated.v1",
		Files:         append([]string{}, graph.GeneratedFiles...),
	}
}

// EndpointSurfaces returns endpoint-adjacent listener metadata from a graph.
func EndpointSurfaces(graph *Graph) []EndpointSurface {
	endpoints := []EndpointSurface{}
	for _, component := range graph.Components {
		if len(component.Listeners) == 0 {
			continue
		}
		endpoints = append(endpoints, EndpointSurface{
			Component: component.Name,
			Listeners: append([]string{}, component.Listeners...),
		})
	}
	return endpoints
}

// ResourceSurfaces returns resource-related metadata from a graph.
func ResourceSurfaces(graph *Graph) []ResourceSurface {
	resources := []ResourceSurface{}
	for _, component := range graph.Components {
		for _, listener := range component.Listeners {
			resources = append(resources, ResourceSurface{
				Name:      listener,
				Kind:      "listener",
				Component: component.Name,
			})
		}
	}
	sortResourceSurfaces(resources)
	return resources
}

func sortResourceSurfaces(resources []ResourceSurface) {
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Kind != resources[j].Kind {
			return resources[i].Kind < resources[j].Kind
		}
		if resources[i].Name != resources[j].Name {
			return resources[i].Name < resources[j].Name
		}
		if resources[i].Component != resources[j].Component {
			return resources[i].Component < resources[j].Component
		}
		if resources[i].Type != resources[j].Type {
			return resources[i].Type < resources[j].Type
		}
		if resources[i].Lifecycle != resources[j].Lifecycle {
			return resources[i].Lifecycle < resources[j].Lifecycle
		}
		if resources[i].Binding != resources[j].Binding {
			return resources[i].Binding < resources[j].Binding
		}
		if resources[i].Provider != resources[j].Provider {
			return resources[i].Provider < resources[j].Provider
		}
		if resources[i].Required != resources[j].Required {
			return !resources[i].Required && resources[j].Required
		}
		return resources[i].Source < resources[j].Source
	})
}

// ObservabilityContextForGraph returns read-only local observability context.
func ObservabilityContextForGraph(graph *Graph) ObservabilityContext {
	return ObservabilityContext{
		SchemaVersion: "twill.obs.context.v1",
		Defaults:      []ObservabilityDefault{},
		Traces:        TracesContextForGraph(graph),
		Logs:          LogsContextForGraph(graph),
		Metrics:       MetricsContextForGraph(graph),
		Files:         []string{},
		Limitations: []string{
			"Live telemetry backends are not connected to local MCP resources yet.",
			"Standard runtime/observability references are reported by name, kind, inferred component, and source file only.",
			"Raw logs, trace payloads, metric values, request bodies, and secret-bearing attributes are not exposed.",
		},
	}
}

// TracesContextForGraph returns trace context that is safe for AI agents.
func TracesContextForGraph(graph *Graph) TracesContext {
	return TracesContext{
		SchemaVersion: "twill.obs.traces.v1",
		Components:    ComponentNames(graph),
		Traces:        []TraceSurface{},
		Limitations: []string{
			"Runtime trace databases are not queried by the local MCP resource yet.",
			"Use explain_trace with caller-supplied spans, trace_json, or scoped trace files for local diagnosis.",
		},
	}
}

// LogsContextForGraph returns log context that is safe for AI agents.
func LogsContextForGraph(graph *Graph) LogsContext {
	return LogsContext{
		SchemaVersion: "twill.obs.logs.v1",
		Components:    ComponentNames(graph),
		Sources:       []LogSource{},
		Limitations: []string{
			"Runtime log backends are not queried by the local MCP resource yet.",
			"Diagnostic tools redact baseline secret patterns before returning log excerpts.",
		},
	}
}

// MetricsContextForGraph returns metric context that is safe for AI agents.
func MetricsContextForGraph(graph *Graph) MetricsContext {
	return MetricsContext{
		SchemaVersion: "twill.obs.metrics.v1",
		Components:    ComponentNames(graph),
		Signals:       []MetricSignal{},
		Limitations: []string{
			"Runtime metric backends and SLO rollups are not queried by the local MCP resource yet.",
			"Use generate_slo and generate_observability for dry-run metric and alert design until live metrics are modeled.",
		},
	}
}

func inspectObservabilityContext(
	ctx context.Context,
	opts GraphOptions,
	graph *Graph,
) (ObservabilityContext, error) {
	observability := ObservabilityContextForGraph(graph)
	defaults, files, err := inspectObservabilityDefaults(ctx, opts, graph)
	if err != nil {
		return ObservabilityContext{}, err
	}
	observability.Defaults = defaults
	observability.Metrics.Signals = observabilityMetricSignals(defaults)
	observability.Files = files
	return observability, nil
}

func inspectObservabilityDefaults(
	ctx context.Context,
	opts GraphOptions,
	graph *Graph,
) ([]ObservabilityDefault, []string, error) {
	dir := packageLoadDir(opts)
	rootDir, err := inspectionRootDir(opts)
	if err != nil {
		return nil, nil, err
	}
	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = []string{"."}
	}

	cfg := &packages.Config{
		Context: ctx,
		Dir:     dir,
		Mode:    packages.NeedName | packages.NeedFiles,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, nil, err
	}

	componentSet := make(map[string]struct{}, len(graph.Components))
	for _, component := range graph.Components {
		componentSet[component.Name] = struct{}{}
	}

	defaults := []ObservabilityDefault{}
	filesRead := map[string]struct{}{}
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			return nil, nil, packageErrors(pkg)
		}
		for _, filename := range pkg.GoFiles {
			if filepath.Base(filename) == "twill_gen.go" {
				continue
			}
			cleanFilename := cleanPath(rootDir, filename)
			fileDefaults, err := inspectObservabilityDefaultsInFile(pkg.PkgPath, componentSet, filename, cleanFilename)
			if err != nil {
				return nil, nil, err
			}
			if len(fileDefaults) == 0 {
				continue
			}
			filesRead[cleanFilename] = struct{}{}
			defaults = append(defaults, fileDefaults...)
		}
	}

	defaults = dedupeObservabilityDefaults(defaults)
	sort.Slice(defaults, func(i, j int) bool {
		if defaults[i].Component != defaults[j].Component {
			return defaults[i].Component < defaults[j].Component
		}
		if defaults[i].Kind != defaults[j].Kind {
			return defaults[i].Kind < defaults[j].Kind
		}
		if defaults[i].Name != defaults[j].Name {
			return defaults[i].Name < defaults[j].Name
		}
		return defaults[i].Source < defaults[j].Source
	})

	files := make([]string, 0, len(filesRead))
	for file := range filesRead {
		files = append(files, file)
	}
	sort.Strings(files)
	return defaults, files, nil
}

func inspectObservabilityDefaultsInFile(
	pkgPath string,
	components map[string]struct{},
	filename string,
	cleanFilename string,
) ([]ObservabilityDefault, error) {
	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse observability source %s: %w", filename, err)
	}
	aliases := importAliases(file, observabilityImportPath, "observability")
	if len(aliases) == 0 {
		return []ObservabilityDefault{}, nil
	}
	fileComponents := middlewareComponentsInFile(pkgPath, components, file)
	defaults := []ObservabilityDefault{}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		if _, ok := aliases[ident.Name]; !ok && !observabilityMethodSelector(selector.Sel.Name) {
			return true
		}
		kind := observabilityDefaultKind(selector.Sel.Name)
		if kind == "" {
			return true
		}
		if len(fileComponents) == 0 {
			defaults = append(defaults, ObservabilityDefault{
				Name:   selector.Sel.Name,
				Kind:   kind,
				Source: cleanFilename,
			})
			return true
		}
		for _, component := range fileComponents {
			defaults = append(defaults, ObservabilityDefault{
				Component: component,
				Name:      selector.Sel.Name,
				Kind:      kind,
				Source:    cleanFilename,
			})
		}
		return true
	})
	return defaults, nil
}

func observabilityMethodSelector(name string) bool {
	switch name {
	case "InstrumentHandler", "SnapshotMetrics":
		return true
	default:
		return false
	}
}

func importAliases(file *ast.File, importPath string, defaultAlias string) map[string]struct{} {
	aliases := map[string]struct{}{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		alias := defaultAlias
		if spec.Name != nil {
			if spec.Name.Name == "." || spec.Name.Name == "_" {
				continue
			}
			alias = spec.Name.Name
		}
		aliases[alias] = struct{}{}
	}
	return aliases
}

func observabilityDefaultKind(name string) string {
	switch name {
	case "Start":
		return "defaults"
	case "InstrumentHandler":
		return "trace"
	case "SnapshotMetrics":
		return "metrics"
	case "Options", "Defaults":
		return "configuration"
	default:
		return ""
	}
}

func dedupeObservabilityDefaults(defaults []ObservabilityDefault) []ObservabilityDefault {
	deduped := []ObservabilityDefault{}
	seen := map[ObservabilityDefault]struct{}{}
	for _, item := range defaults {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		deduped = append(deduped, item)
	}
	return deduped
}

func observabilityMetricSignals(defaults []ObservabilityDefault) []MetricSignal {
	signals := []MetricSignal{}
	for _, item := range defaults {
		if item.Kind != "metrics" || item.Name != "SnapshotMetrics" {
			continue
		}
		signals = append(signals, runtimeMetricSignals(item.Component)...)
	}
	return dedupeMetricSignals(signals)
}

func runtimeMetricSignals(component string) []MetricSignal {
	return []MetricSignal{
		{
			Name:        imetrics.MethodCountsName,
			Component:   component,
			Type:        "counter",
			Description: "Count of Twill component method invocations.",
		},
		{
			Name:        imetrics.MethodErrorsName,
			Component:   component,
			Type:        "counter",
			Description: "Count of Twill component method invocations that return an error.",
		},
		{
			Name:        imetrics.MethodLatenciesName,
			Component:   component,
			Type:        "histogram",
			Description: "Duration of Twill component method execution in microseconds.",
		},
		{
			Name:        imetrics.MethodBytesRequestName,
			Component:   component,
			Type:        "histogram",
			Description: "Request bytes for Twill component method calls.",
		},
		{
			Name:        imetrics.MethodBytesReplyName,
			Component:   component,
			Type:        "histogram",
			Description: "Reply bytes for Twill component method calls.",
		},
	}
}

func dedupeMetricSignals(signals []MetricSignal) []MetricSignal {
	deduped := []MetricSignal{}
	seen := map[MetricSignal]struct{}{}
	for _, signal := range signals {
		if _, ok := seen[signal]; ok {
			continue
		}
		seen[signal] = struct{}{}
		deduped = append(deduped, signal)
	}
	sort.Slice(deduped, func(i, j int) bool {
		if deduped[i].Component != deduped[j].Component {
			return deduped[i].Component < deduped[j].Component
		}
		if deduped[i].Name != deduped[j].Name {
			return deduped[i].Name < deduped[j].Name
		}
		return deduped[i].Type < deduped[j].Type
	})
	return deduped
}

// DeploymentContextForGraph returns read-only local deployment context.
func DeploymentContextForGraph(graph *Graph) DeploymentContext {
	return deploymentContextForGraph(context.Background(), graph, EndpointsContext{}, nil)
}

func deploymentContextForGraph(ctx context.Context, graph *Graph, endpoints EndpointsContext, patterns []string) DeploymentContext {
	return DeploymentContext{
		SchemaVersion: "twill.deploy.context.v1",
		Status:        DeployStatusContextForGraph(graph),
		Kubernetes:    kubernetesContextForGraph(ctx, graph, endpoints, patterns),
		AWS:           awsContextForGraph(ctx, graph, endpoints, patterns),
		Limitations: []string{
			"Live deployment backends are not connected to local MCP resources yet.",
			"Kubernetes clusters, kubeconfig, AWS APIs, AWS config, pods, events, rollout state, and live resource specs are not queried.",
		},
	}
}

// DeployStatusContextForGraph returns deployment status context for AI agents.
func DeployStatusContextForGraph(graph *Graph) DeployStatusContext {
	return DeployStatusContext{
		SchemaVersion: "twill.deploy.status.v1",
		Components:    ComponentNames(graph),
		Statuses:      []DeploymentStatus{},
		Limitations: []string{
			"Live deployment status is not queried by the local MCP resource yet.",
			"Use diagnose_deploy_failure with caller-supplied rollout status, events, and logs for local diagnosis.",
		},
	}
}

// KubernetesContextForGraph returns Kubernetes context that is safe for AI agents.
func KubernetesContextForGraph(graph *Graph) KubernetesContext {
	return kubernetesContextForGraph(context.Background(), graph, EndpointsContext{}, nil)
}

func kubernetesContextForGraph(ctx context.Context, graph *Graph, endpoints EndpointsContext, patterns []string) KubernetesContext {
	req := deploymentPlanRequest(
		graph,
		endpoints,
		"k8s",
		deployplan.DefaultKubernetesApp,
		deployplan.DefaultKubernetesNamespace,
		"image",
	)
	plan, err := deployplan.KubernetesPlanner{}.Plan(ctx, req)
	if err != nil {
		return KubernetesContext{
			SchemaVersion: "twill.deploy.kubernetes.v1",
			Components:    ComponentNames(graph),
			Limitations:   []string{"Kubernetes dry-run planner context could not be generated from local metadata."},
		}
	}
	verifyCommands := displayDeploymentVerifyCommands(plan.VerifyCommands, patterns)
	return KubernetesContext{
		SchemaVersion: "twill.deploy.kubernetes.v1",
		Components:    append([]string{}, plan.Components...),
		DryRun:        plan.DryRun,
		Resources:     kubernetesResourcesForPlan(plan.Resources, "twill deploy k8s dry-run plan"),
		Rollout: KubernetesRollout{
			Name:                 plan.App,
			Namespace:            plan.Namespace,
			Strategy:             plan.Rollout.Strategy,
			Replicas:             plan.Rollout.Replicas,
			MaxReplicas:          plan.Rollout.MaxReplicas,
			HealthPath:           plan.Rollout.HealthPath,
			ResourceRequirements: rolloutResourceRequirements(plan.Resources),
			VerifyCommands:       verifyCommands,
			RollbackCommands:     append([]string{}, plan.Rollout.RollbackCommands...),
			Source:               "twill deploy k8s dry-run plan",
		},
		PreApplyValidated:      plan.PreApplyValidated,
		PreApplyValidationMode: plan.PreApplyValidationMode,
		Environment:            plan.Environment,
		PolicyGates:            convertPolicyGates(plan.PolicyGates),
		RolloutHealthCheck:     convertRolloutHealthCheck(plan.RolloutHealthCheck),
		Limitations:            append([]string{}, plan.Limitations...),
		VerifyCommands:         verifyCommands,
		PerformedWrites:        plan.PerformedWrites,
		PerformedEnvWrite:      plan.PerformedEnvWrite,
	}
}

// AWSContextForGraph returns AWS EKS context that is safe for AI agents.
func AWSContextForGraph(graph *Graph) AWSContext {
	return awsContextForGraph(context.Background(), graph, EndpointsContext{}, nil)
}

func awsContextForGraph(ctx context.Context, graph *Graph, endpoints EndpointsContext, patterns []string) AWSContext {
	req := deploymentPlanRequest(
		graph,
		endpoints,
		"aws",
		deployplan.DefaultKubernetesApp,
		deployplan.DefaultKubernetesNamespace,
		"image",
	)
	plan, err := deployplan.AWSPlanner{
		Region:       deployplan.DefaultAWSRegion,
		AccountID:    "123456789012",
		Cluster:      deployplan.DefaultAWSCluster,
		Repository:   deployplan.DefaultKubernetesApp,
		IngressClass: deployplan.DefaultAWSIngressClass,
	}.Plan(ctx, req)
	if err != nil {
		return AWSContext{
			SchemaVersion: "twill.deploy.aws.v1",
			Components:    ComponentNames(graph),
			Limitations:   []string{"AWS dry-run planner context could not be generated from local metadata."},
		}
	}
	verifyCommands := displayDeploymentVerifyCommands(plan.VerifyCommands, patterns)
	return AWSContext{
		SchemaVersion: "twill.deploy.aws.v1",
		Components:    append([]string{}, plan.Components...),
		DryRun:        plan.DryRun,
		Resources:     awsResourcesForPlan(plan.Resources),
		Rollout: KubernetesRollout{
			Name:                 plan.App,
			Namespace:            plan.Namespace,
			Strategy:             plan.Rollout.Strategy,
			Replicas:             plan.Rollout.Replicas,
			MaxReplicas:          plan.Rollout.MaxReplicas,
			HealthPath:           plan.Rollout.HealthPath,
			ResourceRequirements: rolloutResourceRequirements(plan.Resources),
			VerifyCommands:       verifyCommands,
			RollbackCommands:     append([]string{}, plan.Rollout.RollbackCommands...),
			Source:               "twill deploy aws dry-run plan",
		},
		PreApplyValidated:      plan.PreApplyValidated,
		PreApplyValidationMode: plan.PreApplyValidationMode,
		Environment:            plan.Environment,
		PolicyGates:            convertPolicyGates(plan.PolicyGates),
		RolloutHealthCheck:     convertRolloutHealthCheck(plan.RolloutHealthCheck),
		Limitations:            append([]string{}, plan.Limitations...),
		VerifyCommands:         verifyCommands,
		PerformedWrites:        plan.PerformedWrites,
		PerformedEnvWrite:      plan.PerformedEnvWrite,
	}
}

func deploymentPlanRequest(
	graph *Graph,
	endpoints EndpointsContext,
	target string,
	appName string,
	namespace string,
	image string,
) deployers.PlanRequest {
	return deployers.PlanRequest{
		App:        appName,
		Target:     target,
		Namespace:  namespace,
		Image:      image,
		Components: ComponentNames(graph),
		Endpoints:  DeploymentPlanEndpoints(graph, endpoints),
	}
}

func rolloutResourceRequirements(resources []deployers.Resource) KubernetesResourceRequirements {
	for _, resource := range resources {
		if resource.Kind != "Deployment" {
			continue
		}
		container := firstDeploymentContainer(resource.Manifest)
		if len(container) == 0 {
			return KubernetesResourceRequirements{}
		}
		values := containerResourceValues(container)
		return KubernetesResourceRequirements{
			CPURequest:    values["requests.cpu"],
			MemoryRequest: values["requests.memory"],
			CPULimit:      values["limits.cpu"],
			MemoryLimit:   values["limits.memory"],
		}
	}
	return KubernetesResourceRequirements{}
}

func convertPolicyGates(gates []deployers.GateResult) []PolicyGateResult {
	if len(gates) == 0 {
		return nil
	}
	result := make([]PolicyGateResult, len(gates))
	for i, g := range gates {
		result[i] = PolicyGateResult{
			ID:       g.ID,
			Title:    g.Title,
			Passed:   g.Passed,
			Severity: g.Severity,
			Message:  g.Message,
		}
	}
	return result
}

func convertRolloutHealthCheck(hc *deployers.RolloutHealthCheck) *RolloutHealthCheck {
	if hc == nil {
		return nil
	}
	return &RolloutHealthCheck{
		Enabled:                hc.Enabled,
		SLOName:                hc.SLOName,
		RollbackBurnRate:       hc.RollbackBurnRate,
		RollbackWindow:         hc.RollbackWindow,
		AllowAutomaticRollback: hc.AllowAutomaticRollback,
		StatusCommand:          hc.StatusCommand,
		RollbackCommand:        hc.RollbackCommand,
		HealthEvaluated:        hc.HealthEvaluated,
		HealthStatus:           hc.HealthStatus,
		RollbackTriggered:      hc.RollbackTriggered,
		HealthReason:           hc.HealthReason,
	}
}

func firstDeploymentContainer(manifest map[string]any) map[string]any {
	spec, ok := manifest["spec"].(map[string]any)
	if !ok {
		return nil
	}
	template, ok := spec["template"].(map[string]any)
	if !ok {
		return nil
	}
	podSpec, ok := template["spec"].(map[string]any)
	if !ok {
		return nil
	}
	switch containers := podSpec["containers"].(type) {
	case []map[string]any:
		if len(containers) == 0 {
			return nil
		}
		return containers[0]
	case []any:
		if len(containers) == 0 {
			return nil
		}
		container, _ := containers[0].(map[string]any)
		return container
	default:
		return nil
	}
}

func containerResourceValues(container map[string]any) map[string]string {
	values := map[string]string{}
	resources, ok := container["resources"].(map[string]any)
	if !ok {
		return values
	}
	copyResourceValues(values, resources, "requests")
	copyResourceValues(values, resources, "limits")
	return values
}

func copyResourceValues(out map[string]string, resources map[string]any, group string) {
	switch values := resources[group].(type) {
	case map[string]string:
		for name, value := range values {
			out[group+"."+name] = value
		}
	case map[string]any:
		for name, value := range values {
			if text, ok := value.(string); ok {
				out[group+"."+name] = text
			}
		}
	}
}

// DeploymentPlanEndpoints returns deterministic endpoint metadata for dry-run deployment planners.
func DeploymentPlanEndpoints(graph *Graph, endpointContext EndpointsContext) []deployers.Endpoint {
	planned := []deployers.Endpoint{}
	listenersWithPaths := map[string]bool{}
	seen := map[string]bool{}
	addEndpoint := func(component string, listener string, path string) {
		path = deployplan.KubernetesIngressPath(path)
		if !strings.HasPrefix(path, "/") {
			return
		}
		key := component + "\x00" + listener + "\x00" + path
		if seen[key] {
			return
		}
		seen[key] = true
		listenersWithPaths[listener] = true
		planned = append(planned, deployers.Endpoint{
			Component: component,
			Listener:  listener,
			Path:      path,
		})
	}
	for _, declaration := range endpointContext.Declarations {
		if declaration.Protocol != "" && declaration.Protocol != "http" {
			continue
		}
		addEndpoint(declaration.Component, declaration.Listener, declaration.Path)
	}
	for _, contract := range endpointContext.Contracts {
		addEndpoint(contract.Component, contract.Listener, contract.Path)
	}
	for _, endpoint := range EndpointSurfaces(graph) {
		for _, listener := range endpoint.Listeners {
			if listenersWithPaths[listener] {
				continue
			}
			planned = append(planned, deployers.Endpoint{
				Component: endpoint.Component,
				Listener:  listener,
				Path:      "/" + deployplan.KubernetesName(listener),
			})
		}
	}
	sort.Slice(planned, func(i, j int) bool {
		if planned[i].Component != planned[j].Component {
			return planned[i].Component < planned[j].Component
		}
		if planned[i].Listener != planned[j].Listener {
			return planned[i].Listener < planned[j].Listener
		}
		return planned[i].Path < planned[j].Path
	})
	return planned
}

func kubernetesResourcesForPlan(resources []deployers.Resource, source string) []KubernetesResource {
	out := make([]KubernetesResource, 0, len(resources))
	for _, resource := range resources {
		out = append(out, KubernetesResource{
			Kind:   resource.Kind,
			Name:   resource.Name,
			Source: source,
		})
	}
	return out
}

func awsResourcesForPlan(resources []deployers.Resource) []AWSResource {
	out := make([]AWSResource, 0, len(resources))
	for _, resource := range resources {
		out = append(out, AWSResource{
			Kind:                      resource.Kind,
			Name:                      resource.Name,
			Region:                    deploymentResourceRegion(resource),
			Source:                    deploymentResourceSource(resource),
			Layer:                     resource.Layer,
			Target:                    resource.Target,
			ManifestType:              resource.ManifestType,
			EmbeddedFromSchemaVersion: resource.EmbeddedFromSchemaVersion,
		})
	}
	return out
}

func deploymentResourceRegion(resource deployers.Resource) string {
	if region, ok := resource.Manifest["region"].(string); ok {
		return region
	}
	return ""
}

func deploymentResourceSource(resource deployers.Resource) string {
	if resource.Source != "" {
		return resource.Source
	}
	switch resource.Kind {
	case "ServiceAccount",
		"Deployment",
		"Service",
		"HorizontalPodAutoscaler",
		"Ingress":
		return "embedded twill deploy k8s dry-run plan"
	default:
		return "twill deploy aws dry-run plan"
	}
}

func displayDeploymentVerifyCommands(commands []string, patterns []string) []string {
	out := make([]string, 0, len(commands))
	patternArgs := verifyPatternArgs(patterns)
	for _, command := range commands {
		command = strings.ReplaceAll(command, "--image image", "--image <image>")
		command = strings.ReplaceAll(command, "--region "+deployplan.DefaultAWSRegion, "--region <region>")
		command = strings.ReplaceAll(command, "--account 123456789012", "--account <account-id>")
		command = strings.ReplaceAll(command, "--repository "+deployplan.DefaultKubernetesApp, "--repository <repository>")
		if strings.HasSuffix(command, " ./...") {
			command = strings.TrimSuffix(command, " ./...") + " " + patternArgs
		}
		out = append(out, command)
	}
	return out
}

// PolicyRules returns the baseline local AI/tool safety policy rules.
func PolicyRules() PolicyRulesContext {
	return PolicyRulesContext{
		SchemaVersion: policyRulesSchemaVersion,
		Rules: []PolicyRule{
			{
				ID:          "tool.read_only.default",
				Title:       "Read-only context by default",
				AppliesTo:   []string{"mcp.resources", "mcp.tools"},
				Requirement: "Tools and resources that inspect local context must report performed_writes=false and performed_environment_write=false.",
				Enforcement: "Structured output includes performed_writes, performed_environment_write, safety_notes, and audit_event.",
			},
			{
				ID:          "tool.dry_run.generation",
				Title:       "Generated source starts as dry-run",
				AppliesTo:   []string{"generate_component", "generate_test"},
				Requirement: "Generation tools must return proposed file contents before write-capable edits are enabled.",
				Enforcement: "Structured output includes proposed_changes and verification commands.",
			},
			{
				ID:          "tool.environment_write.approval",
				Title:       "Environment writes require approval",
				AppliesTo:   []string{"resource_changes", "deployments"},
				Requirement: "Dev, staging, preview, and production resource changes require explicit approval and audit evidence.",
				Enforcement: "Planning tools identify approval level and do not perform environment writes.",
			},
			{
				ID:          "tool.scope.directory",
				Title:       "MCP tool calls stay inside the active directory scope",
				AppliesTo:   []string{"mcp.tools"},
				Requirement: "Tool calls that accept a directory must stay within the server --dir scope root.",
				Enforcement: "MCP tools reject requested dirs outside the configured scope root before inspecting files.",
			},
			{
				ID:          "tool.scope.package_patterns",
				Title:       "MCP package patterns stay inside the active scope",
				AppliesTo:   []string{"mcp.tools", "mcp.resources"},
				Requirement: "Local package patterns must not escape the server --dir scope root.",
				Enforcement: "MCP tools and resources reject absolute or parent-relative package patterns before package inspection.",
			},
			{
				ID:          "tool.secrets.redaction",
				Title:       "Secrets stay out of AI context",
				AppliesTo:   []string{"app.config", "app.resources", "logs", "traces"},
				Requirement: "Config values, secret names, and secret values must not be inferred or exposed as safe context.",
				Enforcement: "Current context exposes metadata and explicit limitations; diagnostic output excerpts use baseline redaction before being returned.",
			},
		},
		Limitations: []string{
			"Baseline policy rules are static defaults.",
			"Project policy rules are loaded from " + projectPolicyRulesFile + " when present.",
			fmt.Sprintf("Project policy files are limited to %d bytes.", maxPolicyRulesBytes),
			"Runtime enforcement and audit persistence are planned separately from this read-only context.",
		},
	}
}

func validateProjectPolicyRules(baseline, project []PolicyRule) error {
	ids := make(map[string]struct{}, len(baseline)+len(project))
	for _, rule := range baseline {
		ids[rule.ID] = struct{}{}
	}
	for i, rule := range project {
		if rule.ID == "" {
			return fmt.Errorf("rules[%d].id is required", i)
		}
		if rule.Title == "" {
			return fmt.Errorf("rules[%d].title is required", i)
		}
		if len(rule.AppliesTo) == 0 {
			return fmt.Errorf("rules[%d].applies_to must not be empty", i)
		}
		if rule.Requirement == "" {
			return fmt.Errorf("rules[%d].requirement is required", i)
		}
		if rule.Enforcement == "" {
			return fmt.Errorf("rules[%d].enforcement is required", i)
		}
		if _, ok := ids[rule.ID]; ok {
			return fmt.Errorf("rules[%d].id %q duplicates an existing policy rule", i, rule.ID)
		}
		ids[rule.ID] = struct{}{}
	}
	return nil
}

// ComponentNames returns sorted component names from a graph.
func ComponentNames(graph *Graph) []string {
	components := make([]string, 0, len(graph.Components))
	for _, component := range graph.Components {
		components = append(components, component.Name)
	}
	return components
}
