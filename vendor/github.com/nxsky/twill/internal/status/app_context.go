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

package status

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/nxsky/twill/internal/tool/app"
)

const dashboardAppSchemaVersion = "twill.dashboard.app.v1"

// DashboardAppInput describes the local app context inspected for dashboard data.
type DashboardAppInput struct {
	Dir      string   `json:"dir"`
	Patterns []string `json:"patterns"`
}

// DashboardAppData is a safe, UI-oriented projection of the local app context.
type DashboardAppData struct {
	SchemaVersion         string                        `json:"schema_version"`
	Inputs                DashboardAppInput             `json:"inputs"`
	State                 string                        `json:"state"`
	Services              []DashboardService            `json:"services"`
	Components            []DashboardComponent          `json:"components"`
	Edges                 []DashboardEdge               `json:"edges"`
	APIs                  []DashboardAPI                `json:"apis"`
	Protobuf              DashboardProtobuf             `json:"protobuf"`
	ClientSDK             DashboardClientSDK            `json:"client_sdk"`
	ContractTests         DashboardContractTests        `json:"contract_tests"`
	Config                DashboardConfigContext        `json:"config"`
	Middleware            DashboardMiddlewareContext    `json:"middleware"`
	Resources             []DashboardResource           `json:"resources"`
	LocalCompose          DashboardLocalCompose         `json:"local_compose"`
	ObservabilityDefaults []DashboardObservability      `json:"observability_defaults"`
	Observability         DashboardObservabilityContext `json:"observability"`
	Deployment            DashboardDeployment           `json:"deployment"`
	Tests                 []DashboardTestHint           `json:"tests"`
	Limitations           []string                      `json:"limitations"`
	SafetyNotes           []string                      `json:"safety_notes"`
	PerformedWrites       bool                          `json:"performed_writes"`
	PerformedEnvWrite     bool                          `json:"performed_environment_write"`
}

// DashboardService describes one Go package in the app graph.
type DashboardService struct {
	Path string `json:"path"`
	Dir  string `json:"dir"`
}

// DashboardComponent describes one component in the app graph.
type DashboardComponent struct {
	Name      string   `json:"name"`
	Package   string   `json:"package"`
	Listeners []string `json:"listeners,omitempty"`
}

// DashboardEdge describes one component dependency.
type DashboardEdge struct {
	Caller string `json:"caller"`
	Callee string `json:"callee"`
}

// DashboardAPI describes one safe endpoint summary.
type DashboardAPI struct {
	Kind         string `json:"kind,omitempty"`
	Protocol     string `json:"protocol,omitempty"`
	Component    string `json:"component"`
	Listener     string `json:"listener"`
	Service      string `json:"service,omitempty"`
	Method       string `json:"method,omitempty"`
	Path         string `json:"path,omitempty"`
	RequestType  string `json:"request_type,omitempty"`
	ResponseType string `json:"response_type,omitempty"`
	Source       string `json:"source,omitempty"`
}

// DashboardProtobuf describes safe protobuf contract summaries.
type DashboardProtobuf struct {
	Packages     []app.ProtobufPackage     `json:"packages"`
	Services     []DashboardProtoService   `json:"services"`
	Messages     []app.ProtobufMessage     `json:"messages"`
	RuntimeHints []app.ProtobufRuntimeHint `json:"runtime_hints"`
	Files        []string                  `json:"files,omitempty"`
}

// DashboardProtoService describes one protobuf service without payload fields.
type DashboardProtoService struct {
	Name    string            `json:"name"`
	Package string            `json:"package,omitempty"`
	RPCs    []app.ProtobufRPC `json:"rpcs"`
	Source  string            `json:"source"`
}

// DashboardClientSDK describes dry-run client SDK coverage.
type DashboardClientSDK struct {
	Targets       []app.ClientSDKTarget       `json:"targets"`
	Operations    []app.ClientSDKOperation    `json:"operations"`
	RPCOperations []app.ClientSDKRPCOperation `json:"rpc_operations"`
}

// DashboardContractTests describes dry-run endpoint contract-test coverage.
type DashboardContractTests struct {
	Cases    []app.ContractTestCase    `json:"cases"`
	RPCCases []app.RPCContractTestCase `json:"rpc_cases"`
	Targets  []string                  `json:"targets"`
}

// DashboardConfigContext describes safe config schema and binding summaries.
type DashboardConfigContext struct {
	Schemas     []app.ConfigSchema  `json:"schemas"`
	Bindings    []app.ConfigBinding `json:"bindings"`
	Limitations []string            `json:"limitations"`
}

// DashboardMiddlewareContext describes safe standard middleware summaries.
type DashboardMiddlewareContext struct {
	Components  []string                `json:"components"`
	Middleware  []app.MiddlewareBinding `json:"middleware"`
	Limitations []string                `json:"limitations"`
}

// DashboardResource describes one backing resource summary.
type DashboardResource struct {
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

// DashboardLocalCompose describes dry-run local dependency infrastructure.
type DashboardLocalCompose struct {
	Project  string                            `json:"project"`
	Services []DashboardLocalComposeService    `json:"services"`
	Volumes  []app.LocalComposeVolume          `json:"volumes"`
	Skipped  []app.LocalComposeSkippedResource `json:"skipped"`
}

// DashboardLocalComposeService describes one safe local Compose service summary.
type DashboardLocalComposeService struct {
	Name         string                                 `json:"name"`
	Image        string                                 `json:"image"`
	ResourceName string                                 `json:"resource_name"`
	ResourceKind string                                 `json:"resource_kind"`
	Component    string                                 `json:"component,omitempty"`
	Resources    []DashboardLocalComposeServiceResource `json:"resources,omitempty"`
	Ports        []string                               `json:"ports,omitempty"`
}

// DashboardLocalComposeServiceResource describes a Twill resource on a Compose service.
type DashboardLocalComposeServiceResource struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Component string `json:"component,omitempty"`
}

// DashboardObservability describes one standard observability default reference.
type DashboardObservability struct {
	Component string `json:"component,omitempty"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
}

// DashboardObservabilityContext describes safe trace, log, and metric context.
type DashboardObservabilityContext struct {
	Defaults    []DashboardObservability `json:"defaults"`
	Traces      DashboardTracesContext   `json:"traces"`
	Logs        DashboardLogsContext     `json:"logs"`
	Metrics     DashboardMetricsContext  `json:"metrics"`
	Limitations []string                 `json:"limitations"`
}

// DashboardTracesContext describes safe trace evidence for the dashboard.
type DashboardTracesContext struct {
	Components  []string           `json:"components"`
	Traces      []app.TraceSurface `json:"traces"`
	Limitations []string           `json:"limitations"`
}

// DashboardLogsContext describes safe log-source evidence for the dashboard.
type DashboardLogsContext struct {
	Components  []string        `json:"components"`
	Sources     []app.LogSource `json:"sources"`
	Limitations []string        `json:"limitations"`
}

// DashboardMetricsContext describes safe metric-signal evidence for the dashboard.
type DashboardMetricsContext struct {
	Components  []string           `json:"components"`
	Signals     []app.MetricSignal `json:"signals"`
	Limitations []string           `json:"limitations"`
}

// DashboardDeployment describes local deployment context visible in the dashboard.
type DashboardDeployment struct {
	StatusComponents []string                 `json:"status_components"`
	Statuses         []app.DeploymentStatus   `json:"statuses"`
	Kubernetes       []app.KubernetesResource `json:"kubernetes"`
	AWS              []app.AWSResource        `json:"aws"`
	Rollout          app.KubernetesRollout    `json:"rollout"`
	Limitations      []string                 `json:"limitations"`
}

// DashboardTestHint describes package-level test evidence for a component.
type DashboardTestHint struct {
	Component string `json:"component"`
	Package   string `json:"package"`
	Status    string `json:"status"`
}

func (d *dashboard) handleApp(w http.ResponseWriter, r *http.Request) {
	data := d.dashboardAppData(r.Context())
	content := struct {
		Tool string
		Data DashboardAppData
	}{
		Tool: d.spec.Tool,
		Data: data,
	}
	if err := appTemplate.Execute(w, content); err != nil {
		http.Error(w, fmt.Sprintf("cannot display app context: %v", err), http.StatusInternalServerError)
	}
}

func (d *dashboard) handleAppData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	data := d.dashboardAppData(r.Context())
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, fmt.Sprintf("cannot encode app context: %v", err), http.StatusInternalServerError)
	}
}

func (d *dashboard) dashboardAppData(ctx context.Context) DashboardAppData {
	input := dashboardAppInput()
	pack, err := app.InspectContextPack(ctx, app.GraphOptions{
		Dir:      input.Dir,
		Patterns: input.Patterns,
	})
	if err != nil {
		return emptyDashboardAppData(input)
	}
	return dashboardAppDataFromPack(input, pack)
}

func dashboardAppInput() DashboardAppInput {
	return DashboardAppInput{
		Dir:      strings.TrimSpace(*dashboardAppDir),
		Patterns: dashboardAppPatterns(),
	}
}

func dashboardAppPatterns() []string {
	value := strings.TrimSpace(*dashboardAppPackages)
	if value == "" {
		return []string{"."}
	}
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	patterns := []string{}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		patterns = append(patterns, field)
	}
	if len(patterns) == 0 {
		return []string{"."}
	}
	return patterns
}

func emptyDashboardAppData(input DashboardAppInput) DashboardAppData {
	return DashboardAppData{
		SchemaVersion: dashboardAppSchemaVersion,
		Inputs:        input,
		State:         "empty",
		Services:      []DashboardService{},
		Components:    []DashboardComponent{},
		Edges:         []DashboardEdge{},
		APIs:          []DashboardAPI{},
		Protobuf: DashboardProtobuf{
			Packages:     []app.ProtobufPackage{},
			Services:     []DashboardProtoService{},
			Messages:     []app.ProtobufMessage{},
			RuntimeHints: []app.ProtobufRuntimeHint{},
			Files:        []string{},
		},
		ClientSDK: DashboardClientSDK{
			Targets:       []app.ClientSDKTarget{},
			Operations:    []app.ClientSDKOperation{},
			RPCOperations: []app.ClientSDKRPCOperation{},
		},
		ContractTests: DashboardContractTests{
			Cases:    []app.ContractTestCase{},
			RPCCases: []app.RPCContractTestCase{},
			Targets:  []string{},
		},
		Config: DashboardConfigContext{
			Schemas:     []app.ConfigSchema{},
			Bindings:    []app.ConfigBinding{},
			Limitations: []string{},
		},
		Middleware: DashboardMiddlewareContext{
			Components:  []string{},
			Middleware:  []app.MiddlewareBinding{},
			Limitations: []string{},
		},
		Resources: []DashboardResource{},
		LocalCompose: DashboardLocalCompose{
			Services: []DashboardLocalComposeService{},
			Volumes:  []app.LocalComposeVolume{},
			Skipped:  []app.LocalComposeSkippedResource{},
		},
		ObservabilityDefaults: []DashboardObservability{},
		Observability: DashboardObservabilityContext{
			Defaults: []DashboardObservability{},
			Traces: DashboardTracesContext{
				Components:  []string{},
				Traces:      []app.TraceSurface{},
				Limitations: []string{},
			},
			Logs: DashboardLogsContext{
				Components:  []string{},
				Sources:     []app.LogSource{},
				Limitations: []string{},
			},
			Metrics: DashboardMetricsContext{
				Components:  []string{},
				Signals:     []app.MetricSignal{},
				Limitations: []string{},
			},
			Limitations: []string{},
		},
		Deployment: DashboardDeployment{
			StatusComponents: []string{},
			Statuses:         []app.DeploymentStatus{},
			Kubernetes:       []app.KubernetesResource{},
			AWS:              []app.AWSResource{},
			Limitations:      []string{},
		},
		Tests: []DashboardTestHint{},
		Limitations: []string{
			"Local app context is unavailable for the selected dashboard directory and package patterns.",
			"Run the dashboard from a Twill app directory or pass --app-dir and --app-packages.",
			"No source, logs, trace payloads, metric values, config values, or secret-bearing details are exposed.",
		},
		SafetyNotes:       []string{"Dashboard app context is read-only; no files or external resources were modified."},
		PerformedWrites:   false,
		PerformedEnvWrite: false,
	}
}

func dashboardAppDataFromPack(input DashboardAppInput, pack *app.ContextPack) DashboardAppData {
	if pack == nil || pack.Graph == nil {
		return emptyDashboardAppData(input)
	}
	data := DashboardAppData{
		SchemaVersion:         dashboardAppSchemaVersion,
		Inputs:                input,
		State:                 "available",
		Services:              dashboardServices(pack.Graph.Packages),
		Components:            dashboardComponents(pack.Graph.Components),
		Edges:                 dashboardEdges(pack.Graph.Edges),
		APIs:                  dashboardAPIs(pack.Endpoints),
		Protobuf:              dashboardProtobuf(pack.Protobuf),
		ClientSDK:             dashboardClientSDK(pack.ClientSDK),
		ContractTests:         dashboardContractTests(pack.ContractTests),
		Config:                dashboardConfig(pack.Config),
		Middleware:            dashboardMiddleware(pack.Middleware),
		Resources:             dashboardResources(pack.Resources.Resources),
		LocalCompose:          dashboardLocalCompose(pack.LocalCompose),
		ObservabilityDefaults: dashboardObservability(pack.Observability.Defaults),
		Observability:         dashboardObservabilityContext(pack.Observability),
		Deployment:            dashboardDeployment(pack.Deployment),
		Tests:                 dashboardTests(pack.Tests),
		Limitations:           dashboardLimitations(pack),
		SafetyNotes:           append([]string{}, pack.SafetyNotes...),
		PerformedWrites:       pack.PerformedWrites,
		PerformedEnvWrite:     pack.PerformedEnvWrite,
	}
	if len(data.SafetyNotes) == 0 {
		data.SafetyNotes = []string{"Dashboard app context is read-only; no files or external resources were modified."}
	}
	return data
}

func dashboardServices(packages []app.Package) []DashboardService {
	services := make([]DashboardService, 0, len(packages))
	for _, pkg := range packages {
		services = append(services, DashboardService{
			Path: pkg.Path,
			Dir:  pkg.Dir,
		})
	}
	return services
}

func dashboardComponents(components []app.Component) []DashboardComponent {
	items := make([]DashboardComponent, 0, len(components))
	for _, component := range components {
		items = append(items, DashboardComponent{
			Name:      component.Name,
			Package:   component.Package,
			Listeners: append([]string{}, component.Listeners...),
		})
	}
	return items
}

func dashboardEdges(edges []app.Edge) []DashboardEdge {
	items := make([]DashboardEdge, 0, len(edges))
	for _, edge := range edges {
		items = append(items, DashboardEdge{
			Caller: edge.Caller,
			Callee: edge.Callee,
		})
	}
	return items
}

func dashboardAPIs(endpoints app.EndpointsContext) []DashboardAPI {
	apis := []DashboardAPI{}
	for _, declaration := range endpoints.Declarations {
		apis = append(apis, DashboardAPI{
			Kind:         declaration.Kind,
			Protocol:     declaration.Protocol,
			Component:    declaration.Component,
			Listener:     declaration.Listener,
			Service:      declaration.Service,
			Method:       declaration.Method,
			Path:         declaration.Path,
			RequestType:  declaration.RequestType,
			ResponseType: declaration.ResponseType,
			Source:       declaration.Source,
		})
	}
	for _, contract := range endpoints.Contracts {
		apis = append(apis, DashboardAPI{
			Component: contract.Component,
			Listener:  contract.Listener,
			Method:    contract.Method,
			Path:      contract.Path,
			Source:    contract.Source,
		})
	}
	if len(apis) > 0 {
		sort.Slice(apis, func(i, j int) bool {
			if apis[i].Component != apis[j].Component {
				return apis[i].Component < apis[j].Component
			}
			if apis[i].Listener != apis[j].Listener {
				return apis[i].Listener < apis[j].Listener
			}
			if apis[i].Protocol != apis[j].Protocol {
				return apis[i].Protocol < apis[j].Protocol
			}
			if apis[i].Service != apis[j].Service {
				return apis[i].Service < apis[j].Service
			}
			if apis[i].Path != apis[j].Path {
				return apis[i].Path < apis[j].Path
			}
			return apis[i].Method < apis[j].Method
		})
		return apis
	}
	for _, endpoint := range endpoints.Endpoints {
		for _, listener := range endpoint.Listeners {
			apis = append(apis, DashboardAPI{
				Component: endpoint.Component,
				Listener:  listener,
			})
		}
	}
	return apis
}

func dashboardProtobuf(protobuf app.ProtobufContext) DashboardProtobuf {
	services := make([]DashboardProtoService, 0, len(protobuf.Services))
	for _, service := range protobuf.Services {
		services = append(services, DashboardProtoService{
			Name:    service.Name,
			Package: service.Package,
			RPCs:    append([]app.ProtobufRPC{}, service.RPCs...),
			Source:  service.Source,
		})
	}
	return DashboardProtobuf{
		Packages:     append([]app.ProtobufPackage{}, protobuf.Packages...),
		Services:     services,
		Messages:     append([]app.ProtobufMessage{}, protobuf.Messages...),
		RuntimeHints: append([]app.ProtobufRuntimeHint{}, protobuf.RuntimeHints...),
		Files:        append([]string{}, protobuf.Files...),
	}
}

func dashboardClientSDK(clientSDK app.ClientSDKContext) DashboardClientSDK {
	return DashboardClientSDK{
		Targets:       append([]app.ClientSDKTarget{}, clientSDK.Targets...),
		Operations:    append([]app.ClientSDKOperation{}, clientSDK.Operations...),
		RPCOperations: append([]app.ClientSDKRPCOperation{}, clientSDK.RPCOperations...),
	}
}

func dashboardContractTests(contractTests app.ContractTestsContext) DashboardContractTests {
	targets := make([]string, 0, len(contractTests.Targets))
	for _, target := range contractTests.Targets {
		targets = append(targets, target.Path)
	}
	return DashboardContractTests{
		Cases:    append([]app.ContractTestCase{}, contractTests.Cases...),
		RPCCases: append([]app.RPCContractTestCase{}, contractTests.RPCCases...),
		Targets:  targets,
	}
}

func dashboardConfig(config app.ConfigContext) DashboardConfigContext {
	return DashboardConfigContext{
		Schemas:     append([]app.ConfigSchema{}, config.Schemas...),
		Bindings:    append([]app.ConfigBinding{}, config.Bindings...),
		Limitations: append([]string{}, config.Limitations...),
	}
}

func dashboardMiddleware(middleware app.MiddlewareContext) DashboardMiddlewareContext {
	return DashboardMiddlewareContext{
		Components:  append([]string{}, middleware.Components...),
		Middleware:  append([]app.MiddlewareBinding{}, middleware.Middleware...),
		Limitations: append([]string{}, middleware.Limitations...),
	}
}

func dashboardResources(resources []app.ResourceSurface) []DashboardResource {
	items := make([]DashboardResource, 0, len(resources))
	for _, resource := range resources {
		items = append(items, DashboardResource{
			Name:      resource.Name,
			Kind:      resource.Kind,
			Component: resource.Component,
			Type:      resource.Type,
			Lifecycle: resource.Lifecycle,
			Binding:   resource.Binding,
			Provider:  resource.Provider,
			Required:  resource.Required,
			Source:    resource.Source,
		})
	}
	return items
}

func dashboardLocalCompose(localCompose app.LocalComposeContext) DashboardLocalCompose {
	return DashboardLocalCompose{
		Project:  localCompose.Project,
		Services: dashboardLocalComposeServices(localCompose.Services),
		Volumes:  append([]app.LocalComposeVolume{}, localCompose.Volumes...),
		Skipped:  append([]app.LocalComposeSkippedResource{}, localCompose.Skipped...),
	}
}

func dashboardLocalComposeServices(services []app.LocalComposeService) []DashboardLocalComposeService {
	items := make([]DashboardLocalComposeService, 0, len(services))
	for _, service := range services {
		resources := make([]DashboardLocalComposeServiceResource, 0, len(service.Resources))
		for _, resource := range service.Resources {
			resources = append(resources, DashboardLocalComposeServiceResource{
				Name:      resource.Name,
				Kind:      resource.Kind,
				Component: resource.Component,
			})
		}
		items = append(items, DashboardLocalComposeService{
			Name:         service.Name,
			Image:        service.Image,
			ResourceName: service.ResourceName,
			ResourceKind: service.ResourceKind,
			Component:    service.Component,
			Resources:    resources,
			Ports:        append([]string{}, service.Ports...),
		})
	}
	return items
}

func dashboardObservability(defaults []app.ObservabilityDefault) []DashboardObservability {
	items := make([]DashboardObservability, 0, len(defaults))
	for _, item := range defaults {
		items = append(items, DashboardObservability{
			Component: item.Component,
			Name:      item.Name,
			Kind:      item.Kind,
			Source:    item.Source,
		})
	}
	return items
}

func dashboardObservabilityContext(observability app.ObservabilityContext) DashboardObservabilityContext {
	return DashboardObservabilityContext{
		Defaults: dashboardObservability(observability.Defaults),
		Traces: DashboardTracesContext{
			Components:  append([]string{}, observability.Traces.Components...),
			Traces:      append([]app.TraceSurface{}, observability.Traces.Traces...),
			Limitations: append([]string{}, observability.Traces.Limitations...),
		},
		Logs: DashboardLogsContext{
			Components:  append([]string{}, observability.Logs.Components...),
			Sources:     append([]app.LogSource{}, observability.Logs.Sources...),
			Limitations: append([]string{}, observability.Logs.Limitations...),
		},
		Metrics: DashboardMetricsContext{
			Components:  append([]string{}, observability.Metrics.Components...),
			Signals:     append([]app.MetricSignal{}, observability.Metrics.Signals...),
			Limitations: append([]string{}, observability.Metrics.Limitations...),
		},
		Limitations: append([]string{}, observability.Limitations...),
	}
}

func dashboardDeployment(deployment app.DeploymentContext) DashboardDeployment {
	return DashboardDeployment{
		StatusComponents: append([]string{}, deployment.Status.Components...),
		Statuses:         append([]app.DeploymentStatus{}, deployment.Status.Statuses...),
		Kubernetes:       append([]app.KubernetesResource{}, deployment.Kubernetes.Resources...),
		AWS:              append([]app.AWSResource{}, deployment.AWS.Resources...),
		Rollout:          deployment.Kubernetes.Rollout,
		Limitations:      dashboardDeploymentLimitations(deployment),
	}
}

func dashboardDeploymentLimitations(deployment app.DeploymentContext) []string {
	limitations := []string{}
	limitations = append(limitations, deployment.Limitations...)
	limitations = append(limitations, deployment.Status.Limitations...)
	limitations = append(limitations, deployment.Kubernetes.Limitations...)
	limitations = append(limitations, deployment.AWS.Limitations...)
	return uniqueSortedStrings(limitations)
}

func dashboardTests(tests *app.Tests) []DashboardTestHint {
	if tests == nil {
		return []DashboardTestHint{}
	}
	hints := make([]DashboardTestHint, 0, len(tests.Components))
	for _, hint := range tests.Components {
		hints = append(hints, DashboardTestHint{
			Component: hint.Component,
			Package:   hint.Package,
			Status:    hint.Status,
		})
	}
	return hints
}

func dashboardLimitations(pack *app.ContextPack) []string {
	limitations := []string{}
	limitations = append(limitations, pack.Components.Limitations...)
	limitations = append(limitations, pack.Endpoints.Limitations...)
	limitations = append(limitations, pack.Protobuf.Limitations...)
	limitations = append(limitations, pack.ClientSDK.Limitations...)
	limitations = append(limitations, pack.ContractTests.Limitations...)
	limitations = append(limitations, pack.Config.Limitations...)
	limitations = append(limitations, pack.Middleware.Limitations...)
	limitations = append(limitations, pack.Resources.Limitations...)
	limitations = append(limitations, pack.LocalCompose.Limitations...)
	limitations = append(limitations, pack.Observability.Limitations...)
	limitations = append(limitations, pack.Observability.Traces.Limitations...)
	limitations = append(limitations, pack.Observability.Logs.Limitations...)
	limitations = append(limitations, pack.Observability.Metrics.Limitations...)
	limitations = append(limitations, pack.Deployment.Limitations...)
	if pack.Tests != nil {
		limitations = append(limitations, pack.Tests.Limitations...)
	}
	limitations = append(limitations, "Dashboard app context is a safe summary and does not include raw source, logs, traces, metrics, config values, request bodies, or secret-bearing details.")
	return uniqueSortedStrings(limitations)
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]struct{}{}
	unique := []string{}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	sort.Strings(unique)
	return unique
}
