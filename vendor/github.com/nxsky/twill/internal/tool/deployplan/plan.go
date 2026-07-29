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

// Package deployplan contains pure dry-run deployment planners shared by CLI
// commands and local application context exporters.
package deployplan

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/nxsky/twill/runtime/deployers"
)

const KubernetesPlanSchemaVersion = "twill.deploy.kubernetes.plan.v1"
const AWSPlanSchemaVersion = "twill.deploy.aws.plan.v1"

const (
	DefaultKubernetesApp       = "twill-app"
	DefaultKubernetesNamespace = "default"
	DefaultHealthPath          = "/debug/twill/healthz"
	DefaultHTTPPort            = 8080
	DefaultServicePort         = 80
	DefaultReplicas            = 1
	DefaultMaxReplicas         = 3
	DefaultCPURequest          = "100m"
	DefaultMemoryRequest       = "128Mi"
	DefaultCPULimit            = "500m"
	DefaultMemoryLimit         = "512Mi"

	DefaultAWSRegion       = "us-east-1"
	DefaultAWSCluster      = "twill-cluster"
	DefaultAWSIngressClass = "alb"
)

const (
	resourceSourceKubernetesPlan         = "twill deploy k8s dry-run plan"
	resourceSourceEmbeddedKubernetesPlan = "embedded twill deploy k8s dry-run plan"
	resourceSourceAWSPlan                = "twill deploy aws dry-run plan"

	resourceLayerNative   = "native"
	resourceLayerOverlay  = "overlay"
	resourceLayerEmbedded = "embedded"

	resourceTargetKubernetes = "k8s"
	resourceTargetAWS        = "aws"

	resourceManifestTypeKubernetes = "kubernetes_manifest"
	resourceManifestTypeReview     = "review_metadata"
)

type KubernetesPlanner struct{}

type ResourceRequirements struct {
	CPURequest    string
	MemoryRequest string
	CPULimit      string
	MemoryLimit   string
}

var _ deployers.Planner = KubernetesPlanner{}

func (KubernetesPlanner) Target() string {
	return "k8s"
}

func (KubernetesPlanner) Plan(ctx context.Context, req deployers.PlanRequest) (*deployers.Plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Target != "" && req.Target != "k8s" {
		return nil, fmt.Errorf("planner target %q cannot handle request target %q", "k8s", req.Target)
	}
	appName := req.App
	if appName == "" {
		appName = DefaultKubernetesApp
	}
	name := KubernetesName(appName)
	namespace := req.Namespace
	if namespace == "" {
		namespace = DefaultKubernetesNamespace
	}
	image := req.Image
	if image == "" {
		image = name + ":latest"
	}
	ingressClass := strings.TrimSpace(req.IngressClass)
	ingressHost := strings.TrimSpace(req.IngressHost)
	gatewayClass := strings.TrimSpace(req.GatewayClass)
	healthPath := planHealthPath(req.HealthPath)
	replicas, maxReplicas := planReplicas(req.Replicas, req.MaxReplicas)
	requirements := planResourceRequirements(req)
	if err := validateKubernetesPlanInput(
		name,
		namespace,
		image,
		ingressClass,
		ingressHost,
		gatewayClass,
		healthPath,
		replicas,
		maxReplicas,
		requirements,
	); err != nil {
		return nil, err
	}

	components := append([]string{}, req.Components...)
	sort.Strings(components)
	verifyCommands := kubernetesVerifyCommands(
		name,
		namespace,
		image,
		ingressClass,
		ingressHost,
		gatewayClass,
		healthPath,
		replicas,
		maxReplicas,
		requirements,
	)
	resources := []deployers.Resource{
		serviceAccountResource(name, namespace),
		configMapResource(name, namespace),
		secretResource(name, namespace),
		deploymentResource(name, namespace, image, components, healthPath, replicas, requirements),
		serviceResource(name, namespace),
		hpaResource(name, namespace, replicas, maxReplicas),
	}
	if gatewayClass != "" {
		resources = append(resources,
			gatewayResource(name, namespace, gatewayClass),
			httpRouteResource(name, namespace, ingressHost, req.Endpoints),
		)
	} else {
		resources = append(resources, ingressResource(name, namespace, ingressClass, ingressHost, req.Endpoints))
	}
	rollbackCommands := kubernetesRollbackCommands(name, namespace)
	return &deployers.Plan{
		SchemaVersion: KubernetesPlanSchemaVersion,
		Target:        "k8s",
		App:           name,
		Namespace:     namespace,
		Image:         image,
		DryRun:        true,
		Components:    components,
		Rollout: deployers.Rollout{
			Strategy:         "RollingUpdate",
			Replicas:         replicas,
			MaxReplicas:      maxReplicas,
			HealthPath:       healthPath,
			VerifyCommands:   verifyCommands,
			RollbackCommands: rollbackCommands,
		},
		Resources:         resources,
		Limitations:       kubernetesLimitations(gatewayClass != ""),
		VerifyCommands:    append(verifyCommands, rollbackCommands...),
		PerformedWrites:   false,
		PerformedEnvWrite: false,
	}, nil
}

func kubernetesVerifyCommands(
	name string,
	namespace string,
	image string,
	ingressClass string,
	ingressHost string,
	gatewayClass string,
	healthPath string,
	replicas int,
	maxReplicas int,
	requirements ResourceRequirements,
) []string {
	command := []string{
		"twill deploy k8s",
		"--app " + name,
		"--namespace " + namespace,
		"--image " + image,
	}
	if gatewayClass != "" {
		command = append(command, "--gateway-class "+gatewayClass)
	} else if ingressClass != "" {
		command = append(command, "--ingress-class "+ingressClass)
	}
	if ingressHost != "" {
		command = append(command, "--ingress-host "+ingressHost)
	}
	if healthPath != DefaultHealthPath {
		command = append(command, "--health-path "+healthPath)
	}
	if replicas != DefaultReplicas {
		command = append(command, fmt.Sprintf("--replicas %d", replicas))
	}
	if maxReplicas != DefaultMaxReplicas {
		command = append(command, fmt.Sprintf("--max-replicas %d", maxReplicas))
	}
	command = appendResourceRequirementFlags(command, requirements)
	command = append(command, "./...")
	writeCommand := append([]string{}, command[:len(command)-1]...)
	writeCommand = append(writeCommand, "--output <review-dir>", command[len(command)-1])
	return []string{
		"twill app graph ./...",
		strings.Join(command, " "),
		strings.Join(writeCommand, " "),
		"kubectl apply --dry-run=client -f <reviewed-manifests.yaml>",
	}
}

func kubernetesRollbackCommands(name string, namespace string) []string {
	ns := ""
	if namespace != "" && namespace != "default" {
		ns = " --namespace " + namespace
	}
	return []string{
		"kubectl rollout status deployment/" + name + ns,
		"kubectl rollout undo deployment/" + name + ns,
	}
}

func kubernetesLimitations(gatewayEnabled bool) []string {
	limitations := []string{
		"Dry-run plan only; no Kubernetes API calls, kubeconfig reads, image builds, image pushes, or file writes are performed.",
		"Generated manifests are a conservative starting point and must be reviewed before apply support is enabled.",
	}
	if gatewayEnabled {
		limitations = append(limitations,
			"Gateway API resources are generated for review; GatewayClass availability, TLS, and cross-namespace routes are not queried or modeled yet.",
			"ConfigMaps, Secrets, cloud load balancers, and live rollout health are not queried or modeled yet.",
		)
	} else {
		limitations = append(limitations,
			"ConfigMaps, Secrets, Gateway API resources, cloud load balancers, and live rollout health are not queried or modeled yet.",
		)
	}
	return limitations
}

type AWSPlanner struct {
	Region       string
	AccountID    string
	Cluster      string
	Repository   string
	IngressClass string
}

var _ deployers.Planner = AWSPlanner{}

func (AWSPlanner) Target() string {
	return "aws"
}

func (p AWSPlanner) Plan(ctx context.Context, req deployers.PlanRequest) (*deployers.Plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Target != "" && req.Target != "aws" {
		return nil, fmt.Errorf("planner target %q cannot handle request target %q", "aws", req.Target)
	}
	if strings.TrimSpace(req.GatewayClass) != "" {
		return nil, fmt.Errorf("aws planner does not support GatewayClass %q; use twill deploy k8s for Gateway API resources", req.GatewayClass)
	}

	appName := req.App
	if appName == "" {
		appName = DefaultKubernetesApp
	}
	name := KubernetesName(appName)
	namespace := req.Namespace
	if namespace == "" {
		namespace = DefaultKubernetesNamespace
	}
	region := defaultString(p.Region, DefaultAWSRegion)
	cluster := defaultString(p.Cluster, DefaultAWSCluster)
	ingressClass := defaultString(p.IngressClass, DefaultAWSIngressClass)
	repository := p.Repository
	if repository == "" {
		repository = name
	}
	image := req.Image
	if image == "" {
		image = awsImageURI(p.AccountID, region, repository)
	}
	ingressHost := strings.TrimSpace(req.IngressHost)
	healthPath := planHealthPath(req.HealthPath)
	replicas, maxReplicas := planReplicas(req.Replicas, req.MaxReplicas)
	requirements := planResourceRequirements(req)
	if err := validateAWSPlanInput(
		name,
		namespace,
		image,
		region,
		p.AccountID,
		cluster,
		repository,
		ingressClass,
		ingressHost,
		healthPath,
		replicas,
		maxReplicas,
		requirements,
	); err != nil {
		return nil, err
	}

	kubernetes, err := KubernetesPlanner{}.Plan(ctx, deployers.PlanRequest{
		App:           name,
		Target:        "k8s",
		Namespace:     namespace,
		Image:         image,
		IngressHost:   ingressHost,
		HealthPath:    healthPath,
		Replicas:      replicas,
		MaxReplicas:   maxReplicas,
		CPURequest:    requirements.CPURequest,
		MemoryRequest: requirements.MemoryRequest,
		CPULimit:      requirements.CPULimit,
		MemoryLimit:   requirements.MemoryLimit,
		Components:    append([]string{}, req.Components...),
		Endpoints:     append([]deployers.Endpoint{}, req.Endpoints...),
	})
	if err != nil {
		return nil, err
	}

	components := append([]string{}, kubernetes.Components...)
	sort.Strings(components)
	verifyCommands := awsVerifyCommands(
		name,
		namespace,
		image,
		region,
		p.AccountID,
		cluster,
		repository,
		ingressClass,
		ingressHost,
		healthPath,
		replicas,
		maxReplicas,
		requirements,
	)

	resources := []deployers.Resource{
		ecrRepositoryResource(repository, region, p.AccountID, image),
		eksClusterContextResource(cluster, region),
		irsaResource(name, namespace, region, p.AccountID),
		awsIngressResource(name, namespace, ingressClass, ingressHost),
	}
	resources = append(resources, embeddedKubernetesResources(kubernetes.Resources)...)

	return &deployers.Plan{
		SchemaVersion: AWSPlanSchemaVersion,
		Target:        "aws",
		App:           name,
		Namespace:     namespace,
		Image:         image,
		DryRun:        true,
		Components:    components,
		Rollout: deployers.Rollout{
			Strategy:         kubernetes.Rollout.Strategy,
			Replicas:         kubernetes.Rollout.Replicas,
			MaxReplicas:      kubernetes.Rollout.MaxReplicas,
			HealthPath:       kubernetes.Rollout.HealthPath,
			VerifyCommands:   verifyCommands,
			RollbackCommands: kubernetes.Rollout.RollbackCommands,
		},
		Resources: resources,
		Limitations: []string{
			"Dry-run plan only; no AWS API calls, kubeconfig reads, image builds, image pushes, Kubernetes API calls, or file writes are performed.",
			"AWS account, ECR repository, EKS cluster, IAM role, and ALB/Gateway metadata must be reviewed before apply support is enabled.",
			"IAM policies, subnet selection, security groups, DNS, certificates, image scanning, and live rollout health are not modeled yet.",
			"AWS IAM and ALB resources are review-only overlays; they do not patch the embedded Kubernetes manifests.",
			"Embedded Kubernetes manifests are conservative dry-run planner metadata inherited from twill deploy k8s.",
		},
		VerifyCommands:    append(verifyCommands, kubernetes.Rollout.RollbackCommands...),
		PerformedWrites:   false,
		PerformedEnvWrite: false,
	}, nil
}

func awsVerifyCommands(
	name string,
	namespace string,
	image string,
	region string,
	accountID string,
	cluster string,
	repository string,
	ingressClass string,
	ingressHost string,
	healthPath string,
	replicas int,
	maxReplicas int,
	requirements ResourceRequirements,
) []string {
	command := []string{
		"twill deploy aws",
		"--app " + name,
		"--namespace " + namespace,
		"--region " + region,
	}
	if accountID != "" {
		command = append(command, "--account "+accountID)
	}
	command = append(command,
		"--cluster "+cluster,
		"--repository "+repository,
		"--ingress-class "+ingressClass,
		"--image "+image,
	)
	if ingressHost != "" {
		command = append(command, "--ingress-host "+ingressHost)
	}
	if healthPath != DefaultHealthPath {
		command = append(command, "--health-path "+healthPath)
	}
	if replicas != DefaultReplicas {
		command = append(command, fmt.Sprintf("--replicas %d", replicas))
	}
	if maxReplicas != DefaultMaxReplicas {
		command = append(command, fmt.Sprintf("--max-replicas %d", maxReplicas))
	}
	command = appendResourceRequirementFlags(command, requirements)
	command = append(command, "./...")
	writeCommand := append([]string{}, command[:len(command)-1]...)
	writeCommand = append(writeCommand, "--output <review-dir>", command[len(command)-1])
	applyCommand := append([]string{}, command[:len(command)-1]...)
	applyCommand = append(applyCommand, "--apply", command[len(command)-1])
	return []string{
		"twill app graph ./...",
		strings.Join(command, " "),
		"twill app deployment ./...",
		strings.Join(writeCommand, " "),
		strings.Join(applyCommand, " "),
	}
}

func serviceAccountResource(name, namespace string) deployers.Resource {
	return deployers.Resource{
		Kind:         "ServiceAccount",
		Name:         name,
		Source:       resourceSourceKubernetesPlan,
		Layer:        resourceLayerNative,
		Target:       resourceTargetKubernetes,
		ManifestType: resourceManifestTypeKubernetes,
		Manifest: map[string]any{
			"apiVersion": "v1",
			"kind":       "ServiceAccount",
			"metadata": metadata(name, namespace, map[string]string{
				"app.kubernetes.io/component": "runtime",
			}),
		},
	}
}

// configMapResource emits a ConfigMap that mounts at TWILL_CONFIG_DIR. Keys
// use dotted resource paths (for example twill.resources.db.dsn) so
// runtime/config.FileLoader can resolve them when the volume is mounted.
func configMapResource(name, namespace string) deployers.Resource {
	return deployers.Resource{
		Kind:         "ConfigMap",
		Name:         name + "-config",
		Source:       resourceSourceKubernetesPlan,
		Layer:        resourceLayerNative,
		Target:       resourceTargetKubernetes,
		ManifestType: resourceManifestTypeKubernetes,
		Manifest: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": metadata(name+"-config", namespace, map[string]string{
				"app.kubernetes.io/component": "config",
			}),
			"data": map[string]string{
				// Placeholder entries document the expected key shape for
				// FileLoader. Operators replace values before apply, or
				// overlay real data via kustomize/helm.
				"twill.log.level":  "info",
				"twill.log.format": "json",
			},
		},
	}
}

// secretResource emits an empty Secret shell that mounts at TWILL_SECRET_DIR.
// Real secret values must be supplied out-of-band (sealed-secrets, External
// Secrets Operator, or kubectl create secret). The object is optional so
// apply succeeds before secrets exist.
func secretResource(name, namespace string) deployers.Resource {
	return deployers.Resource{
		Kind:         "Secret",
		Name:         name + "-secrets",
		Source:       resourceSourceKubernetesPlan,
		Layer:        resourceLayerNative,
		Target:       resourceTargetKubernetes,
		ManifestType: resourceManifestTypeKubernetes,
		Manifest: map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": metadata(name+"-secrets", namespace, map[string]string{
				"app.kubernetes.io/component": "secrets",
			}),
			"type": "Opaque",
			"data": map[string]string{},
		},
	}
}

func deploymentResource(
	name string,
	namespace string,
	image string,
	components []string,
	healthPath string,
	replicas int,
	requirements ResourceRequirements,
) deployers.Resource {
	labels := appLabels(name, map[string]string{"app.kubernetes.io/component": "runtime"})
	return deployers.Resource{
		Kind:         "Deployment",
		Name:         name,
		Source:       resourceSourceKubernetesPlan,
		Layer:        resourceLayerNative,
		Target:       resourceTargetKubernetes,
		ManifestType: resourceManifestTypeKubernetes,
		Manifest: map[string]any{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata":   metadata(name, namespace, labels),
			"spec": map[string]any{
				"replicas": replicas,
				"selector": map[string]any{
					"matchLabels": appLabels(name, nil),
				},
				"strategy": map[string]any{
					"type": "RollingUpdate",
					"rollingUpdate": map[string]string{
						"maxSurge":       "25%",
						"maxUnavailable": "0",
					},
				},
				"template": map[string]any{
					"metadata": map[string]any{
						"labels": labels,
						"annotations": map[string]string{
							"twill.dev/components":       strings.Join(components, ","),
							"twill.dev/health-path":      healthPath,
							"twill.dev/rollout-strategy": "RollingUpdate",
						},
					},
					"spec": map[string]any{
						"serviceAccountName":            name,
						"terminationGracePeriodSeconds": 30,
						"containers": []map[string]any{{
							"name":  name,
							"image": image,
							"ports": []map[string]any{{
								"name":          "http",
								"containerPort": DefaultHTTPPort,
							}},
							"env": []map[string]any{
								{"name": "TWILL_CONFIG_DIR", "value": "/etc/twill/config"},
								{"name": "TWILL_SECRET_DIR", "value": "/etc/twill/secrets"},
								{"name": "TWILL_METRICS_PATH", "value": "/metrics"},
								{"name": "TWILL_TRACE_EXPORTER", "value": "otlp"},
							},
							"envFrom": []map[string]any{
								{"configMapRef": map[string]any{"name": name + "-config", "optional": true}},
								{"secretRef": map[string]any{"name": name + "-secrets", "optional": true}},
							},
							"volumeMounts": []map[string]any{
								{"name": "config", "mountPath": "/etc/twill/config", "readOnly": true},
								{"name": "secrets", "mountPath": "/etc/twill/secrets", "readOnly": true},
							},
							"resources": map[string]any{
								"requests": map[string]string{
									"cpu":    requirements.CPURequest,
									"memory": requirements.MemoryRequest,
								},
								"limits": map[string]string{
									"cpu":    requirements.CPULimit,
									"memory": requirements.MemoryLimit,
								},
							},
							"lifecycle": map[string]any{
								"preStop": map[string]any{
									"exec": map[string]any{
										"command": []string{"/bin/sh", "-c", "sleep 5"},
									},
								},
							},
							"readinessProbe": httpProbe(healthPath, DefaultHTTPPort),
							"livenessProbe":  httpProbe(healthPath, DefaultHTTPPort),
						}},
						"volumes": []map[string]any{
							{
								"name": "config",
								"configMap": map[string]any{
									"name":     name + "-config",
									"optional": true,
								},
							},
							{
								"name": "secrets",
								"secret": map[string]any{
									"secretName": name + "-secrets",
									"optional":   true,
								},
							},
						},
					},
				},
			},
		},
	}
}

func serviceResource(name, namespace string) deployers.Resource {
	return deployers.Resource{
		Kind:         "Service",
		Name:         name,
		Source:       resourceSourceKubernetesPlan,
		Layer:        resourceLayerNative,
		Target:       resourceTargetKubernetes,
		ManifestType: resourceManifestTypeKubernetes,
		Manifest: map[string]any{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata":   metadata(name, namespace, map[string]string{"app.kubernetes.io/component": "runtime"}),
			"spec": map[string]any{
				"selector": appLabels(name, nil),
				"ports": []map[string]any{{
					"name":       "http",
					"port":       DefaultServicePort,
					"targetPort": DefaultHTTPPort,
				}},
			},
		},
	}
}

func hpaResource(name, namespace string, replicas, maxReplicas int) deployers.Resource {
	return deployers.Resource{
		Kind:         "HorizontalPodAutoscaler",
		Name:         name,
		Source:       resourceSourceKubernetesPlan,
		Layer:        resourceLayerNative,
		Target:       resourceTargetKubernetes,
		ManifestType: resourceManifestTypeKubernetes,
		Manifest: map[string]any{
			"apiVersion": "autoscaling/v2",
			"kind":       "HorizontalPodAutoscaler",
			"metadata":   metadata(name, namespace, nil),
			"spec": map[string]any{
				"scaleTargetRef": map[string]any{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"name":       name,
				},
				"minReplicas": replicas,
				"maxReplicas": maxReplicas,
				"metrics": []map[string]any{{
					"type": "Resource",
					"resource": map[string]any{
						"name": "cpu",
						"target": map[string]any{
							"type":               "Utilization",
							"averageUtilization": 70,
						},
					},
				}},
			},
		},
	}
}

func ingressResource(name, namespace, ingressClass, ingressHost string, endpoints []deployers.Endpoint) deployers.Resource {
	paths := []map[string]any{}
	for _, endpoint := range endpoints {
		path := kubernetesEndpointIngressPath(endpoint)
		if path == "" {
			continue
		}
		paths = append(paths, ingressPath(path, name))
	}
	paths = dedupeIngressPaths(paths)
	if len(paths) == 0 {
		paths = append(paths, ingressPath("/", name))
	}
	rule := map[string]any{
		"http": map[string]any{
			"paths": paths,
		},
	}
	if ingressHost != "" {
		rule["host"] = ingressHost
	}
	spec := map[string]any{
		"rules": []map[string]any{rule},
	}
	if ingressClass != "" {
		spec["ingressClassName"] = ingressClass
	}
	return deployers.Resource{
		Kind:         "Ingress",
		Name:         name,
		Source:       resourceSourceKubernetesPlan,
		Layer:        resourceLayerNative,
		Target:       resourceTargetKubernetes,
		ManifestType: resourceManifestTypeKubernetes,
		Manifest: map[string]any{
			"apiVersion": "networking.k8s.io/v1",
			"kind":       "Ingress",
			"metadata": metadata(name, namespace, map[string]string{
				"app.kubernetes.io/component": "gateway",
			}),
			"spec": spec,
		},
	}
}

func gatewayResource(name, namespace, gatewayClass string) deployers.Resource {
	return deployers.Resource{
		Kind:         "Gateway",
		Name:         name,
		Source:       resourceSourceKubernetesPlan,
		Layer:        resourceLayerNative,
		Target:       resourceTargetKubernetes,
		ManifestType: resourceManifestTypeKubernetes,
		Manifest: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "Gateway",
			"metadata": metadata(name, namespace, map[string]string{
				"app.kubernetes.io/component": "gateway",
			}),
			"spec": map[string]any{
				"gatewayClassName": gatewayClass,
				"listeners": []map[string]any{{
					"name":     "http",
					"protocol": "HTTP",
					"port":     80,
					"allowedRoutes": map[string]any{
						"namespaces": map[string]any{
							"from": "Same",
						},
					},
				}},
			},
		},
	}
}

func httpRouteResource(name, namespace, host string, endpoints []deployers.Endpoint) deployers.Resource {
	matches := []map[string]any{}
	seen := map[string]struct{}{}
	for _, endpoint := range endpoints {
		path := kubernetesEndpointIngressPath(endpoint)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		matches = append(matches, map[string]any{
			"path": map[string]any{
				"type":  "PathPrefix",
				"value": path,
			},
		})
	}
	if len(matches) == 0 {
		matches = append(matches, map[string]any{
			"path": map[string]any{
				"type":  "PathPrefix",
				"value": "/",
			},
		})
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i]["path"].(map[string]any)["value"].(string) <
			matches[j]["path"].(map[string]any)["value"].(string)
	})
	rule := map[string]any{
		"matches": matches,
		"backendRefs": []map[string]any{{
			"name": name,
			"port": DefaultServicePort,
		}},
	}
	spec := map[string]any{
		"parentRefs": []map[string]any{{
			"name": name,
		}},
		"rules": []map[string]any{rule},
	}
	if host != "" {
		spec["hostnames"] = []any{host}
	}
	return deployers.Resource{
		Kind:         "HTTPRoute",
		Name:         name,
		Source:       resourceSourceKubernetesPlan,
		Layer:        resourceLayerNative,
		Target:       resourceTargetKubernetes,
		ManifestType: resourceManifestTypeKubernetes,
		Manifest: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata": metadata(name, namespace, map[string]string{
				"app.kubernetes.io/component": "gateway",
			}),
			"spec": spec,
		},
	}
}

func kubernetesEndpointIngressPath(endpoint deployers.Endpoint) string {
	if endpoint.Path == "" {
		return "/" + KubernetesName(endpoint.Listener)
	}
	return KubernetesIngressPath(endpoint.Path)
}

func ingressPath(path, serviceName string) map[string]any {
	return map[string]any{
		"path":     path,
		"pathType": "Prefix",
		"backend": map[string]any{
			"service": map[string]any{
				"name": serviceName,
				"port": map[string]int{"number": DefaultServicePort},
			},
		},
	}
}

func dedupeIngressPaths(paths []map[string]any) []map[string]any {
	deduped := []map[string]any{}
	seen := map[string]struct{}{}
	for _, path := range paths {
		value, ok := path["path"].(string)
		if !ok || value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		deduped = append(deduped, path)
	}
	sort.Slice(deduped, func(i, j int) bool {
		return deduped[i]["path"].(string) < deduped[j]["path"].(string)
	})
	return deduped
}

func metadata(name, namespace string, labels map[string]string) map[string]any {
	return map[string]any{
		"name":      name,
		"namespace": namespace,
		"labels":    appLabels(name, labels),
	}
}

func appLabels(name string, extra map[string]string) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/managed-by": "twill",
	}
	for key, value := range extra {
		labels[key] = value
	}
	return labels
}

func httpProbe(path string, port int) map[string]any {
	return map[string]any{
		"httpGet": map[string]any{
			"path": path,
			"port": port,
		},
	}
}

func ecrRepositoryResource(repository, region, accountID, image string) deployers.Resource {
	return deployers.Resource{
		Kind:         "ECRRepository",
		Name:         repository,
		Source:       resourceSourceAWSPlan,
		Layer:        resourceLayerOverlay,
		Target:       resourceTargetAWS,
		ManifestType: resourceManifestTypeReview,
		Manifest: map[string]any{
			"service":      "ecr",
			"repository":   repository,
			"region":       region,
			"account_id":   accountID,
			"image":        image,
			"scan_on_push": "review_required",
		},
	}
}

func eksClusterContextResource(cluster, region string) deployers.Resource {
	return deployers.Resource{
		Kind:         "EKSClusterContext",
		Name:         cluster,
		Source:       resourceSourceAWSPlan,
		Layer:        resourceLayerOverlay,
		Target:       resourceTargetAWS,
		ManifestType: resourceManifestTypeReview,
		Manifest: map[string]any{
			"service": "eks",
			"cluster": cluster,
			"region":  region,
			"context": "review_required",
		},
	}
}

func irsaResource(name, namespace, region, accountID string) deployers.Resource {
	roleName := name + "-runtime"
	return deployers.Resource{
		Kind:         "IAMRoleForServiceAccount",
		Name:         roleName,
		Source:       resourceSourceAWSPlan,
		Layer:        resourceLayerOverlay,
		Target:       resourceTargetAWS,
		ManifestType: resourceManifestTypeReview,
		Manifest: map[string]any{
			"service":         "iam",
			"role":            roleName,
			"region":          region,
			"account_id":      accountID,
			"service_account": name,
			"namespace":       namespace,
			"annotation":      "eks.amazonaws.com/role-arn",
			"policy":          "review_required",
			"overlay_mode":    "review_only",
			"overlay_target": map[string]string{
				"kind":      "ServiceAccount",
				"name":      name,
				"namespace": namespace,
			},
			"embedded_kubernetes_patch": false,
		},
	}
}

func awsIngressResource(name, namespace, ingressClass, ingressHost string) deployers.Resource {
	manifest := map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
			"annotations": map[string]string{
				"kubernetes.io/ingress.class":      ingressClass,
				"alb.ingress.kubernetes.io/scheme": "internet-facing",
				"twill.dev/aws-target":             "eks",
			},
		},
		"ingress_class": ingressClass,
		"gateway":       "review_required",
		"overlay_mode":  "review_only",
		"overlay_target": map[string]string{
			"kind":      "Ingress",
			"name":      name,
			"namespace": namespace,
		},
		"embedded_kubernetes_patch": false,
	}
	if ingressHost != "" {
		manifest["host"] = ingressHost
	}
	return deployers.Resource{
		Kind:         "ALBIngress",
		Name:         name,
		Source:       resourceSourceAWSPlan,
		Layer:        resourceLayerOverlay,
		Target:       resourceTargetAWS,
		ManifestType: resourceManifestTypeReview,
		Manifest:     manifest,
	}
}

func embeddedKubernetesResources(resources []deployers.Resource) []deployers.Resource {
	out := make([]deployers.Resource, 0, len(resources))
	for _, resource := range resources {
		resource.Source = resourceSourceEmbeddedKubernetesPlan
		resource.Layer = resourceLayerEmbedded
		resource.Target = resourceTargetKubernetes
		resource.ManifestType = resourceManifestTypeKubernetes
		resource.EmbeddedFromSchemaVersion = KubernetesPlanSchemaVersion
		out = append(out, resource)
	}
	return out
}

func validateKubernetesPlanInput(
	name string,
	namespace string,
	image string,
	ingressClass string,
	ingressHost string,
	gatewayClass string,
	healthPath string,
	replicas int,
	maxReplicas int,
	requirements ResourceRequirements,
) error {
	if !isDNSLabel(name) {
		return fmt.Errorf("app name %q is not a valid Kubernetes DNS label after normalization", name)
	}
	if !isDNSLabel(namespace) {
		return fmt.Errorf("namespace %q must be a valid Kubernetes DNS label", namespace)
	}
	if !isShellSafeToken(image) {
		return fmt.Errorf("image %q must not be empty or contain whitespace, control characters, or shell metacharacters", image)
	}
	if gatewayClass != "" && ingressClass != "" {
		return fmt.Errorf("--gateway-class %q and --ingress-class %q cannot both be set", gatewayClass, ingressClass)
	}
	if gatewayClass != "" && !isShellSafeToken(gatewayClass) {
		return fmt.Errorf("gateway class %q must not contain whitespace, control characters, or shell metacharacters", gatewayClass)
	}
	if ingressClass != "" && !isShellSafeToken(ingressClass) {
		return fmt.Errorf("ingress class %q must not contain whitespace, control characters, or shell metacharacters", ingressClass)
	}
	if ingressHost != "" && !validIngressHost(ingressHost) {
		return fmt.Errorf("ingress host %q must be a DNS host such as app.example.com", ingressHost)
	}
	if !validHealthPath(healthPath) {
		return fmt.Errorf("health path %q must start with / and must not contain whitespace, control characters, or shell metacharacters", healthPath)
	}
	if replicas < 1 {
		return fmt.Errorf("replicas %d must be at least 1", replicas)
	}
	if maxReplicas < replicas {
		return fmt.Errorf("max replicas %d must be greater than or equal to replicas %d", maxReplicas, replicas)
	}
	if err := validateResourceRequirements(requirements); err != nil {
		return err
	}
	return nil
}

func validateAWSPlanInput(
	name string,
	namespace string,
	image string,
	region string,
	accountID string,
	cluster string,
	repository string,
	ingressClass string,
	ingressHost string,
	healthPath string,
	replicas int,
	maxReplicas int,
	requirements ResourceRequirements,
) error {
	if err := validateKubernetesPlanInput(
		name,
		namespace,
		image,
		"",
		ingressHost,
		"",
		healthPath,
		replicas,
		maxReplicas,
		requirements,
	); err != nil {
		return err
	}
	if !validAWSLabel(region) {
		return fmt.Errorf("region %q must not be empty or contain whitespace, control characters, or shell metacharacters", region)
	}
	if accountID != "" && !validAWSAccountID(accountID) {
		return fmt.Errorf("account %q must be a 12 digit AWS account ID", accountID)
	}
	if !validAWSLabel(cluster) {
		return fmt.Errorf("cluster %q must not be empty or contain whitespace, control characters, or shell metacharacters", cluster)
	}
	if !validAWSRepository(repository) {
		return fmt.Errorf("repository %q must be an ECR repository path without whitespace or control characters", repository)
	}
	if !validAWSLabel(ingressClass) {
		return fmt.Errorf("ingress class %q must not be empty or contain whitespace, control characters, or shell metacharacters", ingressClass)
	}
	return nil
}

func validateResourceRequirements(requirements ResourceRequirements) error {
	values := []struct {
		name  string
		value string
	}{
		{name: "cpu request", value: requirements.CPURequest},
		{name: "memory request", value: requirements.MemoryRequest},
		{name: "cpu limit", value: requirements.CPULimit},
		{name: "memory limit", value: requirements.MemoryLimit},
	}
	for _, entry := range values {
		if !isShellSafeToken(entry.value) {
			return fmt.Errorf(
				"%s %q must not be empty or contain whitespace, control characters, or shell metacharacters",
				entry.name,
				entry.value,
			)
		}
	}
	return nil
}

func planHealthPath(healthPath string) string {
	healthPath = strings.TrimSpace(healthPath)
	if healthPath == "" {
		return DefaultHealthPath
	}
	return healthPath
}

func planReplicas(replicas, maxReplicas int) (int, int) {
	if replicas == 0 {
		replicas = DefaultReplicas
	}
	if maxReplicas == 0 {
		maxReplicas = DefaultMaxReplicas
	}
	return replicas, maxReplicas
}

func planResourceRequirements(req deployers.PlanRequest) ResourceRequirements {
	requirements := ResourceRequirements{
		CPURequest:    DefaultCPURequest,
		MemoryRequest: DefaultMemoryRequest,
		CPULimit:      DefaultCPULimit,
		MemoryLimit:   DefaultMemoryLimit,
	}
	if value := strings.TrimSpace(req.CPURequest); value != "" {
		requirements.CPURequest = value
	}
	if value := strings.TrimSpace(req.MemoryRequest); value != "" {
		requirements.MemoryRequest = value
	}
	if value := strings.TrimSpace(req.CPULimit); value != "" {
		requirements.CPULimit = value
	}
	if value := strings.TrimSpace(req.MemoryLimit); value != "" {
		requirements.MemoryLimit = value
	}
	return requirements
}

func appendResourceRequirementFlags(command []string, requirements ResourceRequirements) []string {
	if requirements.CPURequest != DefaultCPURequest {
		command = append(command, "--cpu-request "+requirements.CPURequest)
	}
	if requirements.MemoryRequest != DefaultMemoryRequest {
		command = append(command, "--memory-request "+requirements.MemoryRequest)
	}
	if requirements.CPULimit != DefaultCPULimit {
		command = append(command, "--cpu-limit "+requirements.CPULimit)
	}
	if requirements.MemoryLimit != DefaultMemoryLimit {
		command = append(command, "--memory-limit "+requirements.MemoryLimit)
	}
	return command
}

const shellUnsafeRunes = ";&|<>$`\\\"'(){}[]*?"

func isShellSafeToken(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(shellUnsafeRunes, r) {
			return false
		}
	}
	return true
}

func isDNSLabel(value string) bool {
	if value == "" || len(value) > 63 {
		return false
	}
	for i, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-'
		if !valid {
			return false
		}
		if (i == 0 || i == len(value)-1) && r == '-' {
			return false
		}
	}
	return true
}

func validIngressHost(value string) bool {
	if strings.TrimSpace(value) == "" || len(value) > 253 {
		return false
	}
	if strings.Contains(value, "..") || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if !isDNSLabel(label) {
			return false
		}
	}
	return true
}

func validHealthPath(value string) bool {
	return strings.HasPrefix(value, "/") && isShellSafeToken(value)
}

func KubernetesName(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(value) {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return DefaultKubernetesApp
	}
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	if name == "" || !unicode.IsLetter(rune(name[0])) && !unicode.IsDigit(rune(name[0])) {
		return DefaultKubernetesApp
	}
	return name
}

func KubernetesIngressPath(path string) string {
	if !strings.HasPrefix(path, "/") {
		return ""
	}
	segments := []string{}
	for _, segment := range strings.Split(path, "/")[1:] {
		if strings.Contains(segment, "{") || strings.Contains(segment, "}") {
			break
		}
		if segment == "" {
			continue
		}
		segments = append(segments, segment)
	}
	if len(segments) == 0 {
		return "/"
	}
	return "/" + strings.Join(segments, "/")
}

func awsImageURI(accountID, region, repository string) string {
	if accountID == "" {
		return repository + ":latest"
	}
	return accountID + ".dkr.ecr." + region + ".amazonaws.com/" + repository + ":latest"
}

func validAWSAccountID(accountID string) bool {
	if len(accountID) != 12 {
		return false
	}
	for _, r := range accountID {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validAWSLabel(value string) bool {
	return isShellSafeToken(value)
}

func validAWSRepository(repository string) bool {
	if strings.TrimSpace(repository) == "" {
		return false
	}
	for _, r := range repository {
		valid := r >= 'a' && r <= 'z' ||
			r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == '.' || r == '/'
		if !valid {
			return false
		}
	}
	return true
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
