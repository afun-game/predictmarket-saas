#!/usr/bin/env python3
"""Validate the rendered Kubernetes shape of the sports ingestion worker."""

from __future__ import annotations

import pathlib
import subprocess
import sys
from typing import Any

import yaml


ROOT = pathlib.Path(__file__).resolve().parents[1]
WORKER_NAME = "predictmarket-sports-ingest"
API_NAME = "predictmarket-api"
KUSTOMIZATION_DIRS = (pathlib.Path("k8s"), pathlib.Path("k8s/overlays/dev"))
REQUIRED_CONFIG = {
    "LMB_BASE_URL": "https://lmb.com.mx",
    "LMB_CALENDAR_TIMEZONE": "Asia/Shanghai",
    "LMB_MARKET_TIMEZONE": "America/Mexico_City",
    "LMB_REQUEST_TIMEOUT": "15s",
    "SPORTS_INGEST_POLL_INTERVAL": "15m",
    "SPORTS_INGEST_LOOKAHEAD_DAYS": "7",
    "SPORTS_INGEST_RUN_ONCE": "false",
}


def fail(message: str) -> None:
    print(f"sports-ingest deployment validation failed: {message}", file=sys.stderr)
    raise SystemExit(1)


def render_resources(kustomization_dir: pathlib.Path) -> list[dict[str, Any]]:
    kustomization_arg = str(kustomization_dir)
    for command in (
        ["kubectl", "kustomize", kustomization_arg],
        ["kustomize", "build", kustomization_arg],
    ):
        try:
            rendered = subprocess.run(
                command,
                cwd=ROOT,
                check=True,
                capture_output=True,
                text=True,
            )
        except FileNotFoundError:
            continue
        except subprocess.CalledProcessError as error:
            detail = error.stderr.strip() or error.stdout.strip()
            fail(f"could not render k8s with {' '.join(command)}: {detail}")
        break
    else:
        return load_resources_without_kustomize(kustomization_dir)

    resources = [
        resource
        for resource in yaml.safe_load_all(rendered.stdout)
        if isinstance(resource, dict)
    ]
    if not resources:
        fail("kustomize rendered no resources")
    return resources


def load_resources_without_kustomize(kustomization_dir: pathlib.Path) -> list[dict[str, Any]]:
    """Load declared resources when kubectl/kustomize is unavailable.

    CI and deployment hosts use the rendered form above. The fallback keeps the
    shape validator usable for local Go-only development; it intentionally reads
    only the resources explicitly listed by the target kustomization file.
    """

    kustomization_path = ROOT / kustomization_dir / "kustomization.yaml"
    try:
        kustomization = yaml.safe_load(kustomization_path.read_text(encoding="utf-8"))
    except OSError as error:
        fail(f"could not read {kustomization_path}: {error}")
    if not isinstance(kustomization, dict):
        fail(f"{kustomization_path} is not a Kubernetes kustomization")

    declared_resources = kustomization.get("resources")
    if not isinstance(declared_resources, list):
        fail(f"{kustomization_path} is missing a resources list")

    resources: list[dict[str, Any]] = []
    for relative_path in declared_resources:
        if not isinstance(relative_path, str):
            fail(f"{kustomization_path} contains a non-string resource path")
        resource_path = kustomization_path.parent / relative_path
        try:
            documents = yaml.safe_load_all(resource_path.read_text(encoding="utf-8"))
            resources.extend(document for document in documents if isinstance(document, dict))
        except OSError as error:
            fail(f"could not read declared resource {resource_path}: {error}")
        except yaml.YAMLError as error:
            fail(f"could not parse declared resource {resource_path}: {error}")
    return resources


def metadata_name(resource: dict[str, Any]) -> str:
    metadata = resource.get("metadata")
    return metadata.get("name", "") if isinstance(metadata, dict) else ""


def named_resources(resources: list[dict[str, Any]], kind: str, name: str) -> list[dict[str, Any]]:
    return [
        resource
        for resource in resources
        if resource.get("kind") == kind and metadata_name(resource) == name
    ]


def required_env_from(container: dict[str, Any], kind: str, name: str) -> bool:
    for value in container.get("envFrom", []):
        if not isinstance(value, dict):
            continue
        reference = value.get(kind)
        if isinstance(reference, dict) and reference.get("name") == name:
            return True
    return False


def has_database_secret(container: dict[str, Any]) -> bool:
    for value in container.get("env", []):
        if not isinstance(value, dict) or value.get("name") != "DATABASE_URL":
            continue
        value_from = value.get("valueFrom")
        if not isinstance(value_from, dict):
            continue
        secret_key_ref = value_from.get("secretKeyRef")
        if (
            isinstance(secret_key_ref, dict)
            and secret_key_ref.get("name") == "predictmarket-secrets"
            and secret_key_ref.get("key") == "DATABASE_URL"
        ):
            return True
    return False


def container_named(deployment: dict[str, Any], name: str) -> dict[str, Any] | None:
    spec = deployment.get("spec")
    template = spec.get("template") if isinstance(spec, dict) else None
    pod_spec = template.get("spec") if isinstance(template, dict) else None
    containers = pod_spec.get("containers") if isinstance(pod_spec, dict) else None
    if not isinstance(containers, list):
        return None
    for container in containers:
        if isinstance(container, dict) and container.get("name") == name:
            return container
    return None


def validate_deployment(resources: list[dict[str, Any]], bundle_name: str) -> None:
    deployments = named_resources(resources, "Deployment", WORKER_NAME)
    if len(deployments) != 1:
        fail(f"expected exactly one Deployment named {WORKER_NAME}, found {len(deployments)}")
    deployment = deployments[0]

    spec = deployment.get("spec")
    if not isinstance(spec, dict) or spec.get("replicas") != 1:
        fail(f"Deployment {WORKER_NAME} must set replicas: 1")

    template = spec.get("template")
    pod_spec = template.get("spec") if isinstance(template, dict) else None
    if not isinstance(pod_spec, dict):
        fail(f"Deployment {WORKER_NAME} is missing spec.template.spec")
    if pod_spec.get("automountServiceAccountToken") is not False:
        fail(f"Deployment {WORKER_NAME} must disable service account token mounting")

    pod_security = pod_spec.get("securityContext")
    seccomp = pod_security.get("seccompProfile") if isinstance(pod_security, dict) else None
    if (
        not isinstance(pod_security, dict)
        or pod_security.get("runAsNonRoot") is not True
        or not isinstance(seccomp, dict)
        or seccomp.get("type") != "RuntimeDefault"
    ):
        fail(f"Deployment {WORKER_NAME} must use a non-root RuntimeDefault pod security context")

    containers = pod_spec.get("containers")
    if not isinstance(containers, list) or len(containers) != 1:
        fail(f"Deployment {WORKER_NAME} must have exactly one container")
    container = containers[0]
    if not isinstance(container, dict) or container.get("name") != "sports-ingest":
        fail(f"Deployment {WORKER_NAME} container must be named sports-ingest")
    if container.get("command") != ["./sports-ingest"]:
        fail(f"Deployment {WORKER_NAME} command must be [\"./sports-ingest\"]")
    if container.get("ports"):
        fail(f"Deployment {WORKER_NAME} must not expose container ports")
    if not required_env_from(container, "configMapRef", "predictmarket-config"):
        fail(f"Deployment {WORKER_NAME} must load predictmarket-config")
    if not required_env_from(container, "secretRef", "predictmarket-secrets"):
        fail(f"Deployment {WORKER_NAME} must load predictmarket-secrets")
    if not has_database_secret(container):
        fail(f"Deployment {WORKER_NAME} must read DATABASE_URL from predictmarket-secrets")

    security = container.get("securityContext")
    capabilities = security.get("capabilities") if isinstance(security, dict) else None
    if (
        not isinstance(security, dict)
        or security.get("allowPrivilegeEscalation") is not False
        or security.get("readOnlyRootFilesystem") is not True
        or not isinstance(capabilities, dict)
        or capabilities.get("drop") != ["ALL"]
    ):
        fail(f"Deployment {WORKER_NAME} must use the hardened container security context")

    api_deployments = named_resources(resources, "Deployment", API_NAME)
    if len(api_deployments) != 1:
        fail(f"{bundle_name} must have exactly one Deployment named {API_NAME}")
    api_container = container_named(api_deployments[0], "api")
    if api_container is None or not api_container.get("image"):
        fail(f"{bundle_name} API deployment must have an image")
    if container.get("image") != api_container.get("image"):
        fail(f"Deployment {WORKER_NAME} must use the same image as {API_NAME}")


def validate_configmap(resources: list[dict[str, Any]]) -> None:
    configmaps = named_resources(resources, "ConfigMap", "predictmarket-config")
    if len(configmaps) != 1:
        fail(f"expected exactly one ConfigMap named predictmarket-config, found {len(configmaps)}")
    data = configmaps[0].get("data")
    if not isinstance(data, dict):
        fail("predictmarket-config must have data")
    for key, expected in REQUIRED_CONFIG.items():
        if data.get(key) != expected:
            fail(f"predictmarket-config {key} must equal {expected!r}")


def validate_no_network_resources(resources: list[dict[str, Any]]) -> None:
    for kind in ("Service", "Ingress"):
        if named_resources(resources, kind, WORKER_NAME):
            fail(f"{WORKER_NAME} must not have a {kind}")


def validate_dev_workflow() -> None:
    workflow_path = ROOT / ".github/workflows/verify.yml"
    try:
        workflow = workflow_path.read_text(encoding="utf-8")
    except OSError as error:
        fail(f"could not read development deployment workflow: {error}")

    required_steps = (
        "kubectl kustomize k8s/overlays/dev",
        "rollout status deployment/predictmarket-sports-ingest --timeout=300s",
        "logs deployment/predictmarket-sports-ingest --tail=100 || true",
    )
    for required_step in required_steps:
        if required_step not in workflow:
            fail(f"development deployment workflow must include {required_step!r}")


def main() -> None:
    for kustomization_dir in KUSTOMIZATION_DIRS:
        resources = render_resources(kustomization_dir)
        validate_configmap(resources)
        validate_deployment(resources, str(kustomization_dir))
        validate_no_network_resources(resources)
    validate_dev_workflow()
    print("Sports ingest Kubernetes deployment: OK")


if __name__ == "__main__":
    main()
