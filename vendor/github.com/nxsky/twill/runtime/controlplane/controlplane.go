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

// Package controlplane provides a minimal, local, single-tenant control
// plane backed by the application graph store. It tracks applications,
// environments, deployment versions, components, instances, routes, and
// config without requiring an external database or cluster connection.
package controlplane

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/nxsky/twill/runtime/environment"
)

// Application is a registered Twill application in the control plane.
type Application struct {
	Name         string              `json:"name"`
	Components   []ComponentRecord   `json:"components"`
	Environments []EnvironmentRecord `json:"environments"`
	Routes       []RouteRecord       `json:"routes"`
	CreatedAt    time.Time           `json:"created_at"`
}

// ComponentRecord describes a component managed by the control plane.
type ComponentRecord struct {
	Name      string   `json:"name"`
	Package   string   `json:"package"`
	Listeners []string `json:"listeners,omitempty"`
}

// EnvironmentRecord describes an environment configured for an application.
type EnvironmentRecord struct {
	Name      string           `json:"name"`
	Type      environment.Type `json:"type"`
	Namespace string           `json:"namespace"`
}

// RouteRecord describes a routing rule from an external path to a component.
type RouteRecord struct {
	Path      string   `json:"path"`
	Component string   `json:"component"`
	Listener  string   `json:"listener"`
	Methods   []string `json:"methods,omitempty"`
}

// DeploymentStatus describes the lifecycle state of a deployment.
type DeploymentStatus string

const (
	DeploymentStatusPending    DeploymentStatus = "pending"
	DeploymentStatusApplying   DeploymentStatus = "applying"
	DeploymentStatusHealthy    DeploymentStatus = "healthy"
	DeploymentStatusUnhealthy  DeploymentStatus = "unhealthy"
	DeploymentStatusRolledBack DeploymentStatus = "rolled_back"
	DeploymentStatusFailed     DeploymentStatus = "failed"
)

// DeploymentRecord tracks a single deployment version in the control plane.
type DeploymentRecord struct {
	ID          string           `json:"id"`
	Application string           `json:"application"`
	Version     string           `json:"version"`
	Environment string           `json:"environment"`
	Image       string           `json:"image"`
	Status      DeploymentStatus `json:"status"`
	AppliedAt   time.Time        `json:"applied_at"`
	Components  []string         `json:"components"`
}

// LocalControlPlane is an in-memory, single-tenant control plane. It is
// safe for concurrent use.
type LocalControlPlane struct {
	mu            sync.RWMutex
	applications  map[string]*Application
	deployments   map[string][]DeploymentRecord
	deploymentSeq int
}

// NewLocalControlPlane returns an empty local control plane.
func NewLocalControlPlane() *LocalControlPlane {
	return &LocalControlPlane{
		applications: map[string]*Application{},
		deployments:  map[string][]DeploymentRecord{},
	}
}

// RegisterApplication registers or updates an application in the control
// plane. If the application already exists, its components, environments,
// and routes are replaced.
func (cp *LocalControlPlane) RegisterApplication(app Application) error {
	if app.Name == "" {
		return fmt.Errorf("application name must not be empty")
	}
	app.CreatedAt = time.Now()
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.applications[app.Name] = &app
	return nil
}

// GetApplication returns the application with the given name.
func (cp *LocalControlPlane) GetApplication(name string) (*Application, error) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	app, ok := cp.applications[name]
	if !ok {
		return nil, fmt.Errorf("application %q not found", name)
	}
	return app, nil
}

// ListApplications returns all registered applications, sorted by name.
func (cp *LocalControlPlane) ListApplications() []Application {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	apps := make([]Application, 0, len(cp.applications))
	for _, app := range cp.applications {
		apps = append(apps, *app)
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
	return apps
}

// RecordDeployment records a deployment version for an application in a
// specific environment. The deployment ID is auto-generated.
func (cp *LocalControlPlane) RecordDeployment(rec DeploymentRecord) (DeploymentRecord, error) {
	if rec.Application == "" {
		return rec, fmt.Errorf("deployment application must not be empty")
	}
	if rec.Environment == "" {
		return rec, fmt.Errorf("deployment environment must not be empty")
	}
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.deploymentSeq++
	rec.ID = fmt.Sprintf("%s-%d", rec.Application, cp.deploymentSeq)
	if rec.Status == "" {
		rec.Status = DeploymentStatusPending
	}
	rec.AppliedAt = time.Now()
	cp.deployments[rec.Application] = append(cp.deployments[rec.Application], rec)
	return rec, nil
}

// UpdateDeploymentStatus updates the status of a deployment by ID.
func (cp *LocalControlPlane) UpdateDeploymentStatus(appName, deploymentID string, status DeploymentStatus) error {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	records := cp.deployments[appName]
	for i := range records {
		if records[i].ID == deploymentID {
			records[i].Status = status
			return nil
		}
	}
	return fmt.Errorf("deployment %q not found for application %q", deploymentID, appName)
}

// ListDeployments returns deployment records for an application, optionally
// filtered by environment.
func (cp *LocalControlPlane) ListDeployments(appName string, envFilter string) ([]DeploymentRecord, error) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	records, ok := cp.deployments[appName]
	if !ok {
		return nil, fmt.Errorf("application %q not found", appName)
	}
	filtered := make([]DeploymentRecord, 0, len(records))
	for _, rec := range records {
		if envFilter != "" && rec.Environment != envFilter {
			continue
		}
		filtered = append(filtered, rec)
	}
	return filtered, nil
}

// GetLatestDeployment returns the most recent deployment for an application
// in the given environment.
func (cp *LocalControlPlane) GetLatestDeployment(appName, envName string) (*DeploymentRecord, error) {
	records, err := cp.ListDeployments(appName, envName)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("no deployments found for application %q in environment %q", appName, envName)
	}
	latest := records[len(records)-1]
	return &latest, nil
}

// RegisterRoute adds a route to an application.
func (cp *LocalControlPlane) RegisterRoute(appName string, route RouteRecord) error {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	app, ok := cp.applications[appName]
	if !ok {
		return fmt.Errorf("application %q not found", appName)
	}
	app.Routes = append(app.Routes, route)
	return nil
}

// GetRoutes returns routes for an application, optionally filtered by listener.
func (cp *LocalControlPlane) GetRoutes(appName string, listenerFilter string) ([]RouteRecord, error) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	app, ok := cp.applications[appName]
	if !ok {
		return nil, fmt.Errorf("application %q not found", appName)
	}
	routes := make([]RouteRecord, 0, len(app.Routes))
	for _, route := range app.Routes {
		if listenerFilter != "" && route.Listener != listenerFilter {
			continue
		}
		routes = append(routes, route)
	}
	return routes, nil
}

// AddEnvironment adds an environment to an application.
func (cp *LocalControlPlane) AddEnvironment(appName string, env EnvironmentRecord) error {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	app, ok := cp.applications[appName]
	if !ok {
		return fmt.Errorf("application %q not found", appName)
	}
	for _, existing := range app.Environments {
		if existing.Name == env.Name {
			return fmt.Errorf("environment %q already exists for application %q", env.Name, appName)
		}
	}
	app.Environments = append(app.Environments, env)
	return nil
}

// GetEnvironments returns environments configured for an application.
func (cp *LocalControlPlane) GetEnvironments(appName string) ([]EnvironmentRecord, error) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	app, ok := cp.applications[appName]
	if !ok {
		return nil, fmt.Errorf("application %q not found", appName)
	}
	return append([]EnvironmentRecord{}, app.Environments...), nil
}
