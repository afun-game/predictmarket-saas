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
	"sort"
	"strconv"
	"strings"
)

const localComposeSchemaVersion = "twill.local.compose.v1"

// LocalComposeOptions configures local Docker Compose dry-run planning.
type LocalComposeOptions struct {
	Project  string
	Patterns []string
}

// LocalComposeContext describes local dependent infrastructure without file contents.
type LocalComposeContext struct {
	SchemaVersion     string                        `json:"schema_version"`
	Project           string                        `json:"project"`
	DryRun            bool                          `json:"dry_run"`
	Services          []LocalComposeService         `json:"services"`
	Volumes           []LocalComposeVolume          `json:"volumes"`
	Skipped           []LocalComposeSkippedResource `json:"skipped"`
	Sources           []string                      `json:"sources,omitempty"`
	Limitations       []string                      `json:"limitations"`
	VerifyCommands    []string                      `json:"verify_commands"`
	PerformedWrites   bool                          `json:"performed_writes"`
	PerformedEnvWrite bool                          `json:"performed_environment_write"`
	Up                bool                          `json:"up,omitempty"`
	UpOutput          string                        `json:"up_output,omitempty"`
}

// LocalComposePlan is the dry-run output returned by twill deploy compose.
// When Up is true, UpOutput records the docker compose command result.
// When Down is true, DownOutput records the docker compose down result.
type LocalComposePlan struct {
	SchemaVersion     string                        `json:"schema_version"`
	Project           string                        `json:"project"`
	DryRun            bool                          `json:"dry_run"`
	Services          []LocalComposeService         `json:"services"`
	Volumes           []LocalComposeVolume          `json:"volumes"`
	Skipped           []LocalComposeSkippedResource `json:"skipped"`
	Files             []LocalComposeFile            `json:"files"`
	WrittenFiles      []string                      `json:"written_files,omitempty"`
	Sources           []string                      `json:"sources,omitempty"`
	Limitations       []string                      `json:"limitations"`
	VerifyCommands    []string                      `json:"verify_commands"`
	PerformedWrites   bool                          `json:"performed_writes"`
	PerformedEnvWrite bool                          `json:"performed_environment_write"`
	Up                bool                          `json:"up,omitempty"`
	UpOutput          string                        `json:"up_output,omitempty"`
	Down              bool                          `json:"down,omitempty"`
	DownOutput        string                        `json:"down_output,omitempty"`
}

// LocalComposeService describes one Docker Compose service proposal.
type LocalComposeService struct {
	Name         string                        `json:"name"`
	Image        string                        `json:"image"`
	ResourceName string                        `json:"resource_name"`
	ResourceKind string                        `json:"resource_kind"`
	Component    string                        `json:"component,omitempty"`
	Resources    []LocalComposeServiceResource `json:"resources,omitempty"`
	Ports        []string                      `json:"ports,omitempty"`
	Environment  map[string]string             `json:"environment,omitempty"`
	Command      []string                      `json:"command,omitempty"`
	Healthcheck  *LocalComposeHealthcheck      `json:"healthcheck,omitempty"`
}

// LocalComposeServiceResource describes a Twill resource mapped to one local Compose service.
type LocalComposeServiceResource struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Component string `json:"component,omitempty"`
}

// LocalComposeHealthcheck describes a Docker Compose service healthcheck proposal.
type LocalComposeHealthcheck struct {
	Test     []string `json:"test"`
	Interval string   `json:"interval,omitempty"`
	Timeout  string   `json:"timeout,omitempty"`
	Retries  int      `json:"retries,omitempty"`
}

// LocalComposeVolume describes one Docker Compose volume proposal.
type LocalComposeVolume struct {
	Name    string `json:"name"`
	Service string `json:"service"`
}

// LocalComposeSkippedResource describes a resource that does not map to a local service.
type LocalComposeSkippedResource struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Component string `json:"component,omitempty"`
	Reason    string `json:"reason"`
}

// LocalComposeFile is a proposed generated Docker Compose file.
type LocalComposeFile struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

// InspectLocalCompose returns a dry-run Docker Compose plan for local dependencies.
func InspectLocalCompose(ctx context.Context, opts GraphOptions, composeOpts LocalComposeOptions) (*LocalComposePlan, error) {
	resources, err := InspectResourcesContext(ctx, opts)
	if err != nil {
		return nil, err
	}
	composeOpts.Patterns = opts.Patterns
	return LocalComposePlanForResources(resources, composeOpts), nil
}

// InspectLocalComposeContext returns local Compose context without generated file contents.
func InspectLocalComposeContext(ctx context.Context, opts GraphOptions, composeOpts LocalComposeOptions) (LocalComposeContext, error) {
	resources, err := InspectResourcesContext(ctx, opts)
	if err != nil {
		return LocalComposeContext{}, err
	}
	composeOpts.Patterns = opts.Patterns
	return LocalComposeContextForResources(resources, composeOpts), nil
}

// LocalComposeContextForResources returns local Compose context from safe resource metadata.
func LocalComposeContextForResources(resources ResourcesContext, opts LocalComposeOptions) LocalComposeContext {
	project := localComposeProject(opts.Project)
	services, volumes, skipped := localComposeResources(resources.Resources)
	hasEnvExample := renderLocalComposeEnvExample(services) != ""
	return LocalComposeContext{
		SchemaVersion:     localComposeSchemaVersion,
		Project:           project,
		DryRun:            true,
		Services:          services,
		Volumes:           volumes,
		Skipped:           skipped,
		Sources:           append([]string{}, resources.Files...),
		Limitations:       localComposeLimitations(),
		VerifyCommands:    localComposeVerifyCommands(project, opts.Patterns, hasEnvExample),
		PerformedWrites:   false,
		PerformedEnvWrite: false,
	}
}

// LocalComposePlanForResources returns a dry-run Docker Compose plan.
func LocalComposePlanForResources(resources ResourcesContext, opts LocalComposeOptions) *LocalComposePlan {
	ctx := LocalComposeContextForResources(resources, opts)
	files := []LocalComposeFile{{
		Path:     "docker-compose.twill.yaml",
		Contents: renderLocalComposeYAML(ctx.Project, ctx.Services, ctx.Volumes),
	}}
	if envExample := renderLocalComposeEnvExample(ctx.Services); envExample != "" {
		files = append(files, LocalComposeFile{
			Path:     "docker-compose.twill.env.example",
			Contents: envExample,
		})
	}
	return &LocalComposePlan{
		SchemaVersion:     ctx.SchemaVersion,
		Project:           ctx.Project,
		DryRun:            ctx.DryRun,
		Services:          append([]LocalComposeService{}, ctx.Services...),
		Volumes:           append([]LocalComposeVolume{}, ctx.Volumes...),
		Skipped:           append([]LocalComposeSkippedResource{}, ctx.Skipped...),
		Files:             files,
		Sources:           append([]string{}, ctx.Sources...),
		Limitations:       append([]string{}, ctx.Limitations...),
		VerifyCommands:    append([]string{}, ctx.VerifyCommands...),
		PerformedWrites:   false,
		PerformedEnvWrite: false,
	}
}

func localComposeProject(value string) string {
	value = composeName(value)
	if value == "" {
		return "twill-local"
	}
	return value
}

func localComposeResources(resources []ResourceSurface) (
	[]LocalComposeService,
	[]LocalComposeVolume,
	[]LocalComposeSkippedResource,
) {
	servicesByName := map[string]LocalComposeService{}
	volumesByName := map[string]LocalComposeVolume{}
	skipped := []LocalComposeSkippedResource{}
	for _, resource := range resources {
		service, volume, ok, reason := localComposeServiceForResource(resource)
		if !ok {
			skipped = append(skipped, localComposeSkippedResource(resource, reason))
			continue
		}
		if existing, exists := servicesByName[service.Name]; exists {
			existing.Resources = append(
				existing.Resources,
				localComposeServiceResource(resource),
			)
			sortLocalComposeServiceResources(existing.Resources)
			servicesByName[service.Name] = existing
			continue
		}
		service.Resources = []LocalComposeServiceResource{
			localComposeServiceResource(resource),
		}
		servicesByName[service.Name] = service
		if volume.Name != "" {
			if _, exists := volumesByName[volume.Name]; !exists {
				volumesByName[volume.Name] = volume
			}
		}
	}

	services := make([]LocalComposeService, 0, len(servicesByName))
	for _, service := range servicesByName {
		services = append(services, service)
	}
	sortLocalComposeServices(services)

	volumes := make([]LocalComposeVolume, 0, len(volumesByName))
	for _, volume := range volumesByName {
		volumes = append(volumes, volume)
	}
	sort.Slice(volumes, func(i, j int) bool {
		if volumes[i].Service != volumes[j].Service {
			return volumes[i].Service < volumes[j].Service
		}
		return volumes[i].Name < volumes[j].Name
	})
	sort.Slice(skipped, func(i, j int) bool {
		if skipped[i].Kind != skipped[j].Kind {
			return skipped[i].Kind < skipped[j].Kind
		}
		if skipped[i].Name != skipped[j].Name {
			return skipped[i].Name < skipped[j].Name
		}
		return skipped[i].Component < skipped[j].Component
	})
	return services, volumes, skipped
}

func localComposeServiceForResource(resource ResourceSurface) (
	LocalComposeService,
	LocalComposeVolume,
	bool,
	string,
) {
	switch resource.Kind {
	case "database":
		return localComposeDatabase(resource)
	case "cache":
		return localComposeCache(resource)
	case "pubsub", "topic", "subscription", "queue":
		return localComposeNATS(resource), LocalComposeVolume{}, true, ""
	case "object_storage":
		return localComposeMinIO(resource), localComposeVolume("minio", "minio"), true, ""
	case "listener":
		return LocalComposeService{}, LocalComposeVolume{}, false, "listener resources are served by the Twill app runtime"
	case "cron":
		return LocalComposeService{}, LocalComposeVolume{}, false, "cron resources run in the Twill app process"
	case "secret":
		return LocalComposeService{}, LocalComposeVolume{}, false, "secret resources are resolved from environment variables, not Docker Compose services"
	default:
		return LocalComposeService{}, LocalComposeVolume{}, false, "resource kind is not mapped to Docker Compose"
	}
}

func localComposeCache(resource ResourceSurface) (
	LocalComposeService,
	LocalComposeVolume,
	bool,
	string,
) {
	switch cacheFlavor(resource) {
	case "memcached":
		return localComposeMemcached(resource), LocalComposeVolume{}, true, ""
	default:
		return localComposeRedis(resource), localComposeVolume("redis", "redis"), true, ""
	}
}

func cacheFlavor(resource ResourceSurface) string {
	value := strings.ToLower(resource.Name + " " + resource.Type)
	if strings.Contains(value, "memcache") {
		return "memcached"
	}
	return "redis"
}

func localComposeDatabase(resource ResourceSurface) (
	LocalComposeService,
	LocalComposeVolume,
	bool,
	string,
) {
	flavor := databaseFlavor(resource)
	switch flavor {
	case "mysql":
		service := LocalComposeService{
			Name:         "mysql",
			Image:        "mysql:8.4",
			ResourceName: resource.Name,
			ResourceKind: resource.Kind,
			Component:    resource.Component,
			Ports:        []string{"3306:3306"},
			Environment: map[string]string{
				"MYSQL_DATABASE":      "twill",
				"MYSQL_ROOT_PASSWORD": "${TWILL_MYSQL_ROOT_PASSWORD:?set local dev password}",
			},
			Healthcheck: localComposeHealthcheck("mysql"),
		}
		return service, localComposeVolume("mysql", "mysql"), true, ""
	case "postgres":
		service := LocalComposeService{
			Name:         "postgres",
			Image:        "postgres:16-alpine",
			ResourceName: resource.Name,
			ResourceKind: resource.Kind,
			Component:    resource.Component,
			Ports:        []string{"5432:5432"},
			Environment: map[string]string{
				"POSTGRES_DB":       "twill",
				"POSTGRES_PASSWORD": "${TWILL_POSTGRES_PASSWORD:?set local dev password}",
				"POSTGRES_USER":     "twill",
			},
			Healthcheck: localComposeHealthcheck("postgres"),
		}
		return service, localComposeVolume("postgres", "postgres"), true, ""
	default:
		service := LocalComposeService{
			Name:         "postgres",
			Image:        "postgres:16-alpine",
			ResourceName: resource.Name,
			ResourceKind: resource.Kind,
			Component:    resource.Component,
			Ports:        []string{"5432:5432"},
			Environment: map[string]string{
				"POSTGRES_DB":       "twill",
				"POSTGRES_PASSWORD": "${TWILL_POSTGRES_PASSWORD:?set local dev password}",
				"POSTGRES_USER":     "twill",
			},
			Healthcheck: localComposeHealthcheck("postgres"),
		}
		return service, localComposeVolume("postgres", "postgres"), true, ""
	}
}

func databaseFlavor(resource ResourceSurface) string {
	value := strings.ToLower(resource.Name + " " + resource.Type)
	switch {
	case strings.Contains(value, "mysql"):
		return "mysql"
	case strings.Contains(value, "postgres"), strings.Contains(value, "pgx"), strings.Contains(value, "pq"):
		return "postgres"
	default:
		return ""
	}
}

func localComposeRedis(resource ResourceSurface) LocalComposeService {
	return LocalComposeService{
		Name:         "redis",
		Image:        "redis:7-alpine",
		ResourceName: resource.Name,
		ResourceKind: resource.Kind,
		Component:    resource.Component,
		Ports:        []string{"6379:6379"},
		Healthcheck:  localComposeHealthcheck("redis"),
	}
}

func localComposeMemcached(resource ResourceSurface) LocalComposeService {
	return LocalComposeService{
		Name:         "memcached",
		Image:        "memcached:1.6-alpine",
		ResourceName: resource.Name,
		ResourceKind: resource.Kind,
		Component:    resource.Component,
		Ports:        []string{"11211:11211"},
	}
}

func localComposeNATS(resource ResourceSurface) LocalComposeService {
	return LocalComposeService{
		Name:         "nats",
		Image:        "nats:2-alpine",
		ResourceName: resource.Name,
		ResourceKind: resource.Kind,
		Component:    resource.Component,
		Ports:        []string{"4222:4222"},
	}
}

func localComposeMinIO(resource ResourceSurface) LocalComposeService {
	return LocalComposeService{
		Name:         "minio",
		Image:        "minio/minio:RELEASE.2025-09-07T16-13-09Z",
		ResourceName: resource.Name,
		ResourceKind: resource.Kind,
		Component:    resource.Component,
		Ports:        []string{"9000:9000", "9001:9001"},
		Environment: map[string]string{
			"MINIO_ROOT_PASSWORD": "${TWILL_MINIO_ROOT_PASSWORD:?set local dev password}",
			"MINIO_ROOT_USER":     "twill",
		},
		Command: []string{"server", "/data", "--console-address", ":9001"},
	}
}

func localComposeVolume(name string, service string) LocalComposeVolume {
	return LocalComposeVolume{
		Name:    name + "-data",
		Service: service,
	}
}

func localComposeHealthcheck(service string) *LocalComposeHealthcheck {
	switch service {
	case "mysql":
		return &LocalComposeHealthcheck{
			Test:     []string{"CMD-SHELL", "mysqladmin ping -h localhost -uroot -p$$MYSQL_ROOT_PASSWORD"},
			Interval: "10s",
			Timeout:  "5s",
			Retries:  5,
		}
	case "postgres":
		return &LocalComposeHealthcheck{
			Test:     []string{"CMD-SHELL", "pg_isready -U twill -d twill"},
			Interval: "10s",
			Timeout:  "5s",
			Retries:  5,
		}
	case "redis":
		return &LocalComposeHealthcheck{
			Test:     []string{"CMD", "redis-cli", "ping"},
			Interval: "10s",
			Timeout:  "5s",
			Retries:  5,
		}
	default:
		return nil
	}
}

func localComposeSkippedResource(resource ResourceSurface, reason string) LocalComposeSkippedResource {
	return LocalComposeSkippedResource{
		Name:      resource.Name,
		Kind:      resource.Kind,
		Component: resource.Component,
		Reason:    reason,
	}
}

func localComposeServiceResource(resource ResourceSurface) LocalComposeServiceResource {
	return LocalComposeServiceResource{
		Name:      resource.Name,
		Kind:      resource.Kind,
		Component: resource.Component,
	}
}

func sortLocalComposeServiceResources(resources []LocalComposeServiceResource) {
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Kind != resources[j].Kind {
			return resources[i].Kind < resources[j].Kind
		}
		if resources[i].Name != resources[j].Name {
			return resources[i].Name < resources[j].Name
		}
		return resources[i].Component < resources[j].Component
	})
}

func sortLocalComposeServices(services []LocalComposeService) {
	sort.Slice(services, func(i, j int) bool {
		if services[i].Name != services[j].Name {
			return services[i].Name < services[j].Name
		}
		if services[i].ResourceKind != services[j].ResourceKind {
			return services[i].ResourceKind < services[j].ResourceKind
		}
		return services[i].ResourceName < services[j].ResourceName
	})
}

func localComposeLimitations() []string {
	return []string{
		"Dry-run plan only; no Docker commands are run and no files are written unless --write-dir is provided.",
		"Even when --write-dir is provided, Compose services, volumes, containers, and networks are not created or started.",
		"Compose services are inferred from safe resource kind and type metadata only.",
		"Database resources without an explicit MySQL or PostgreSQL type default to PostgreSQL for local development.",
		"Healthchecks are generated only for local service images with standard built-in readiness commands.",
		"Multiple resources that map to the same local service share one Compose service and are listed on that service.",
		"Credentials are emitted as environment variable references and optional .env examples, not secret values.",
		"Cloud provider resource names, connection strings, secret names, and config values are not read or exposed.",
		"Cron jobs and Twill listeners are owned by the application runtime and do not create Compose services.",
	}
}

func localComposeWrittenLimitations() []string {
	return []string{
		"Docker Compose files were written only under the requested --write-dir.",
		"Existing files with different contents are not overwritten; rerun after reviewing conflicts.",
		"No Docker commands were run and no Compose services, volumes, containers, or networks were created.",
		"Compose services are inferred from safe resource kind and type metadata only.",
		"Database resources without an explicit MySQL or PostgreSQL type default to PostgreSQL for local development.",
		"Healthchecks are generated only for local service images with standard built-in readiness commands.",
		"Multiple resources that map to the same local service share one Compose service and are listed on that service.",
		"Credentials are emitted as environment variable references and optional .env examples, not secret values.",
		"Cloud provider resource names, connection strings, secret names, and config values are not read or exposed.",
		"Cron jobs and Twill listeners are owned by the application runtime and do not create Compose services.",
	}
}

func localComposeVerifyCommands(project string, patterns []string, hasEnvExample bool) []string {
	patternArgs := verifyPatternArgs(patterns)
	configCommand := "docker compose -f docker-compose.twill.yaml config"
	if hasEnvExample {
		configCommand = "docker compose --env-file docker-compose.twill.env.example -f docker-compose.twill.yaml config"
	}
	return []string{
		"twill app resources " + patternArgs,
		"twill deploy compose --project " + project + " " + patternArgs,
		configCommand,
	}
}

func renderLocalComposeYAML(project string, services []LocalComposeService, volumes []LocalComposeVolume) string {
	var b strings.Builder
	fmt.Fprintf(&b, "name: %s\n", project)
	b.WriteString("services:\n")
	if len(services) == 0 {
		b.WriteString("  {}\n")
	} else {
		for _, service := range services {
			renderLocalComposeService(&b, service)
		}
	}
	b.WriteString("volumes:\n")
	if len(volumes) == 0 {
		b.WriteString("  {}\n")
	} else {
		for _, volume := range volumes {
			fmt.Fprintf(&b, "  %s: {}\n", volume.Name)
		}
	}
	return b.String()
}

func renderLocalComposeEnvExample(services []LocalComposeService) string {
	variables := localComposeEnvVariables(services)
	if len(variables) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Generated by twill deploy compose.\n")
	b.WriteString("# Use with docker compose --env-file docker-compose.twill.env.example or export these variables before validation.\n")
	b.WriteString("# Values are placeholders; replace them with local development secrets.\n")
	for _, variable := range variables {
		fmt.Fprintf(&b, "%s=change-me\n", variable)
	}
	return b.String()
}

func localComposeEnvVariables(services []LocalComposeService) []string {
	seen := map[string]struct{}{}
	for _, service := range services {
		for _, value := range service.Environment {
			name, ok := localComposeEnvVariable(value)
			if !ok {
				continue
			}
			seen[name] = struct{}{}
		}
	}
	variables := make([]string, 0, len(seen))
	for variable := range seen {
		variables = append(variables, variable)
	}
	sort.Strings(variables)
	return variables
}

func localComposeEnvVariable(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
		return "", false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
	name, _, _ := strings.Cut(inner, ":")
	if name == "" || !localComposeEnvVariableName(name) {
		return "", false
	}
	return name, true
}

func localComposeEnvVariableName(name string) bool {
	for i, r := range name {
		valid := r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_'
		if !valid {
			return false
		}
		if i == 0 && r >= '0' && r <= '9' {
			return false
		}
	}
	return true
}

func renderLocalComposeService(b *strings.Builder, service LocalComposeService) {
	fmt.Fprintf(b, "  %s:\n", service.Name)
	fmt.Fprintf(b, "    image: %s\n", strconv.Quote(service.Image))
	labels := localComposeServiceLabels(service)
	if len(labels) > 0 {
		b.WriteString("    labels:\n")
		keys := make([]string, 0, len(labels))
		for key := range labels {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(b, "      %s: %s\n", key, strconv.Quote(labels[key]))
		}
	}
	if len(service.Command) > 0 {
		b.WriteString("    command:\n")
		for _, item := range service.Command {
			fmt.Fprintf(b, "      - %s\n", strconv.Quote(item))
		}
	}
	if len(service.Ports) > 0 {
		b.WriteString("    ports:\n")
		for _, port := range service.Ports {
			fmt.Fprintf(b, "      - %s\n", strconv.Quote(port))
		}
	}
	if len(service.Environment) > 0 {
		b.WriteString("    environment:\n")
		keys := make([]string, 0, len(service.Environment))
		for key := range service.Environment {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(b, "      %s: %s\n", key, strconv.Quote(service.Environment[key]))
		}
	}
	if service.Healthcheck != nil {
		renderLocalComposeHealthcheck(b, service.Healthcheck)
	}
	volumeName := service.Name + "-data"
	if service.Name == "postgres" || service.Name == "mysql" || service.Name == "redis" || service.Name == "minio" {
		target := localComposeVolumeTarget(service.Name)
		fmt.Fprintf(b, "    volumes:\n      - %s:%s\n", volumeName, target)
	}
}

func renderLocalComposeHealthcheck(b *strings.Builder, healthcheck *LocalComposeHealthcheck) {
	b.WriteString("    healthcheck:\n")
	b.WriteString("      test:\n")
	for _, item := range healthcheck.Test {
		fmt.Fprintf(b, "        - %s\n", strconv.Quote(item))
	}
	if healthcheck.Interval != "" {
		fmt.Fprintf(b, "      interval: %s\n", strconv.Quote(healthcheck.Interval))
	}
	if healthcheck.Timeout != "" {
		fmt.Fprintf(b, "      timeout: %s\n", strconv.Quote(healthcheck.Timeout))
	}
	if healthcheck.Retries > 0 {
		fmt.Fprintf(b, "      retries: %d\n", healthcheck.Retries)
	}
}

func localComposeServiceLabels(service LocalComposeService) map[string]string {
	labels := map[string]string{}
	if service.ResourceKind != "" {
		labels["twill.resource.kind"] = service.ResourceKind
	}
	if service.ResourceName != "" {
		labels["twill.resource.name"] = service.ResourceName
	}
	if len(service.Resources) > 0 {
		names := make([]string, 0, len(service.Resources))
		for _, resource := range service.Resources {
			names = append(names, resource.Name)
		}
		sort.Strings(names)
		labels["twill.resources"] = strings.Join(names, ",")
		if components := localComposeServiceComponents(service.Resources); len(components) > 0 {
			labels["twill.components"] = strings.Join(components, ",")
		}
	}
	if service.Component != "" {
		labels["twill.component"] = service.Component
	}
	return labels
}

func localComposeServiceComponents(resources []LocalComposeServiceResource) []string {
	seen := map[string]struct{}{}
	for _, resource := range resources {
		if resource.Component == "" {
			continue
		}
		seen[resource.Component] = struct{}{}
	}
	components := make([]string, 0, len(seen))
	for component := range seen {
		components = append(components, component)
	}
	sort.Strings(components)
	return components
}

func localComposeVolumeTarget(service string) string {
	switch service {
	case "mysql":
		return "/var/lib/mysql"
	case "postgres":
		return "/var/lib/postgresql/data"
	case "redis":
		return "/data"
	case "minio":
		return "/data"
	default:
		return "/data"
	}
}

func composeName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	previousDash := false
	for _, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if valid {
			b.WriteRune(r)
			previousDash = false
			continue
		}
		if !previousDash {
			b.WriteByte('-')
			previousDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
