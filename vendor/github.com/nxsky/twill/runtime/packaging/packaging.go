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

// Package packaging provides enterprise console packaging for deploying
// the Twill web console in production environments. It generates
// deployment manifests, configuration, and Dockerfile for the console
// service with auth, TLS, and multi-environment support.
package packaging

import (
	"fmt"
	"sort"
	"strings"
)

// ConsolePackage describes a packaged enterprise console deployment.
type ConsolePackage struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Environment string            `json:"environment"`
	Config      ConsoleConfig     `json:"config"`
	Manifests   []ConsoleManifest `json:"manifests"`
	Dockerfile  string            `json:"dockerfile"`
	Limitations []string          `json:"limitations"`
}

// ConsoleConfig describes runtime configuration for the console.
type ConsoleConfig struct {
	Port           int      `json:"port"`
	TLSEnabled     bool     `json:"tls_enabled"`
	TLSCertRef     string   `json:"tls_cert_ref,omitempty"`
	TLSKeyRef      string   `json:"tls_key_ref,omitempty"`
	AuthEnabled    bool     `json:"auth_enabled"`
	AuthType       string   `json:"auth_type,omitempty"`
	CORSEnabled    bool     `json:"cors_enabled"`
	AllowedOrigins []string `json:"allowed_origins,omitempty"`
	Replicas       int      `json:"replicas"`
	CPURequest     string   `json:"cpu_request,omitempty"`
	MemoryRequest  string   `json:"memory_request,omitempty"`
	CPULimit       string   `json:"cpu_limit,omitempty"`
	MemoryLimit    string   `json:"memory_limit,omitempty"`
}

// ConsoleManifest describes one Kubernetes manifest in the package.
type ConsoleManifest struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// PackageInput configures enterprise console packaging.
type PackageInput struct {
	Name        string
	Version     string
	Environment string
	Image       string
	Replicas    int
	EnableTLS   bool
	EnableAuth  bool
	AuthType    string
}

// Package generates an enterprise console deployment package.
func Package(input PackageInput) ConsolePackage {
	name := input.Name
	if name == "" {
		name = "twill-console"
	}
	version := input.Version
	if version == "" {
		version = "latest"
	}
	env := input.Environment
	if env == "" {
		env = "production"
	}
	replicas := input.Replicas
	if replicas <= 0 {
		replicas = 2
	}
	image := input.Image
	if image == "" {
		image = name + ":" + version
	}

	pkg := ConsolePackage{
		Name:        name,
		Version:     version,
		Environment: env,
		Config: ConsoleConfig{
			Port:          8080,
			TLSEnabled:    input.EnableTLS,
			AuthEnabled:   input.EnableAuth,
			AuthType:      input.AuthType,
			CORSEnabled:   true,
			Replicas:      replicas,
			CPURequest:    "100m",
			MemoryRequest: "128Mi",
			CPULimit:      "500m",
			MemoryLimit:   "512Mi",
		},
		Limitations: []string{
			"Console package is a template; review auth, TLS, and resource settings before production deployment.",
			"Auth integration requires external identity provider configuration.",
			"TLS certificates must be provisioned via cert-manager or equivalent.",
		},
	}

	pkg.Manifests = generateManifests(name, env, image, pkg.Config)
	pkg.Dockerfile = generateDockerfile(name, version)

	return pkg
}

func generateManifests(name, env, image string, config ConsoleConfig) []ConsoleManifest {
	var manifests []ConsoleManifest

	manifests = append(manifests, ConsoleManifest{
		Kind:    "Deployment",
		Name:    name,
		Content: renderDeployment(name, env, image, config),
	})

	manifests = append(manifests, ConsoleManifest{
		Kind:    "Service",
		Name:    name,
		Content: renderService(name, config),
	})

	manifests = append(manifests, ConsoleManifest{
		Kind:    "ConfigMap",
		Name:    name + "-config",
		Content: renderConfigMap(name, env, config),
	})

	if config.AuthEnabled {
		manifests = append(manifests, ConsoleManifest{
			Kind:    "Secret",
			Name:    name + "-auth",
			Content: renderAuthSecret(name),
		})
	}

	manifests = append(manifests, ConsoleManifest{
		Kind:    "HorizontalPodAutoscaler",
		Name:    name,
		Content: renderHPA(name),
	})

	sort.Slice(manifests, func(i, j int) bool {
		return manifests[i].Kind < manifests[j].Kind
	})

	return manifests
}

func renderDeployment(name, env, image string, config ConsoleConfig) string {
	port := fmt.Sprintf("%d", config.Port)
	return fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  labels:
    app.kubernetes.io/name: %s
    app.kubernetes.io/component: console
    twill.dev/environment: %s
spec:
  replicas: %d
  selector:
    matchLabels:
      app.kubernetes.io/name: %s
  template:
    metadata:
      labels:
        app.kubernetes.io/name: %s
        app.kubernetes.io/component: console
    spec:
      containers:
        - name: %s
          image: %s
          ports:
            - containerPort: %s
              name: http
          envFrom:
            - configMapRef:
                name: %s-config
          readinessProbe:
            httpGet:
              path: /api/health
              port: http
          livenessProbe:
            httpGet:
              path: /api/health
              port: http
          resources:
            requests:
              cpu: %s
              memory: %s
            limits:
              cpu: %s
              memory: %s
`, name, name, env, config.Replicas, name, name, name, image, port, name,
		config.CPURequest, config.MemoryRequest, config.CPULimit, config.MemoryLimit)
}

func renderService(name string, config ConsoleConfig) string {
	port := fmt.Sprintf("%d", config.Port)
	return fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
  labels:
    app.kubernetes.io/name: %s
spec:
  type: ClusterIP
  ports:
    - port: %s
      targetPort: http
      protocol: TCP
      name: http
  selector:
    app.kubernetes.io/name: %s
`, name, name, port, name)
}

func renderConfigMap(name, env string, config ConsoleConfig) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %s-config
data:
  TWILL_CONSOLE_ENV: "%s"
  TWILL_CONSOLE_PORT: "%d"
  TWILL_CONSOLE_TLS_ENABLED: "%t"
  TWILL_CONSOLE_AUTH_ENABLED: "%t"
  TWILL_CONSOLE_CORS_ENABLED: "%t"
`, name, env, config.Port, config.TLSEnabled, config.AuthEnabled, config.CORSEnabled))
	if config.AuthType != "" {
		sb.WriteString(fmt.Sprintf("  TWILL_CONSOLE_AUTH_TYPE: \"%s\"\n", config.AuthType))
	}
	return sb.String()
}

func renderAuthSecret(name string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s-auth
type: Opaque
stringData:
  auth-config.yaml: |
    # Auth configuration for Twill console
    # Populate with your identity provider settings
`, name)
}

func renderHPA(name string) string {
	return fmt.Sprintf(`apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: %s
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: %s
  minReplicas: 2
  maxReplicas: 5
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: 80
`, name, name)
}

func generateDockerfile(name, version string) string {
	return fmt.Sprintf(`FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY . .
RUN go build -o /%s ./cmd/twill

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /%s /usr/local/bin/%s
EXPOSE 8080
ENTRYPOINT ["%s", "dashboard", "--host", "0.0.0.0", "--port", "8080"]
LABEL twill.dev.version="%s" twill.dev.component="console"
`, name, name, name, name, version)
}
