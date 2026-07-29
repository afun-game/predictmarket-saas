#!/usr/bin/env python3
"""Validate OpenAPI YAML and keep operations aligned with Go HTTP routes."""

from pathlib import Path
import re
import sys

import yaml


def implementation_routes() -> set[tuple[str, str]]:
    routes: set[tuple[str, str]] = {
        ("get", "/debug/twill/healthz"),
        ("get", "/healthz"),
        ("get", "/readyz"),
        ("get", "/metrics"),
    }
    for path in Path("internal/httpapi").glob("*_handler.go"):
        source = path.read_text(encoding="utf-8")
        for method, route in re.findall(r'"(GET|POST|PATCH|DELETE) (/[^" ]+)', source):
            routes.add((method.lower(), route))
    return routes


def documented_routes(spec: dict) -> set[tuple[str, str]]:
    methods = {"get", "post", "patch", "delete"}
    return {
        (method, path)
        for path, path_item in spec.get("paths", {}).items()
        for method in path_item
        if method in methods
    }


def main() -> int:
    spec = yaml.safe_load(Path("openapi.yaml").read_text(encoding="utf-8"))
    if not isinstance(spec, dict) or not str(spec.get("openapi", "")).startswith("3."):
        print("openapi.yaml is not an OpenAPI 3 document", file=sys.stderr)
        return 1
    implementation = implementation_routes()
    documented = documented_routes(spec)
    missing = sorted(implementation - documented)
    extra = sorted(documented - implementation)
    if missing or extra:
        print(f"missing OpenAPI operations: {missing}", file=sys.stderr)
        print(f"extra OpenAPI operations: {extra}", file=sys.stderr)
        return 1
    print(f"OpenAPI YAML and {len(documented)} operations: OK")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
