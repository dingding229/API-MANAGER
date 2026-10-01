#!/usr/bin/env python3
"""Validate the single Compose deployment without displaying credentials."""
import argparse
import json
import subprocess
import os
from pathlib import Path
import re
import stat

DEFAULT_IMAGE = "docker.io/dingding229/api-manager:latest"
IMAGE = re.compile(r"^docker\.io/dingding229/api-manager:[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$")
SIZE = re.compile(r"^(\d+)([bkmg])?$", re.IGNORECASE)
FILES = ("admin_password", "credential_encryption_key", "metrics_token", "postgres_password", "redis_password", "grafana_admin_password")


def validate(environ, project_root):
    errors = []
    if not IMAGE.fullmatch(environ.get("API_MANAGER_IMAGE") or DEFAULT_IMAGE):
        errors.append("API_MANAGER_IMAGE must use a docker.io/dingding229/api-manager tag")
    folder = Path(environ.get("SECRETS_DIR") or project_root / "secrets")
    if not folder.is_absolute():
        folder = project_root / folder
    if folder.is_symlink() or not folder.is_dir():
        return errors + ["Secret directory is missing; run python3 scripts/init-production-secrets.py"]
    if stat.S_IMODE(folder.stat().st_mode) != 0o700:
        errors.append("Secret directory must have mode 0700")
    values = []
    for name in FILES:
        path = folder / name
        if path.is_symlink() or not path.is_file():
            errors.append(f"{name} must be a regular file")
            continue
        if stat.S_IMODE(path.stat().st_mode) != 0o444:
            errors.append(f"{name} must have mode 0444 for non-root file mounts")
        if path.stat().st_size > 65536:
            errors.append(f"{name} is too large")
            continue
        try:
            value = path.read_text().rstrip("\r\n")
        except (OSError, UnicodeError):
            errors.append(f"{name} is not readable text")
            continue
        if len(value.encode()) < (12 if name == "admin_password" else 32) or (name == "admin_password" and len(value.encode()) > 72) or value != value.strip() or any(c in value for c in ("\r", "\n", "\x00")):
            errors.append(f"{name} must contain a single-line 32+ byte secret")
        values.append(value)
    if len(set(values)) != len(values):
        errors.append("All credentials must be distinct")
    if environ.get("OBSERVABILITY_STACK_ENABLED", "false").lower() in ("1", "true", "yes"):
        match = SIZE.fullmatch(environ.get("API_MEMORY_LIMIT") or "2g")
        multipliers = {"": 1, "b": 1, "k": 1 << 10, "m": 1 << 20, "g": 1 << 30}
        if not match or int(match.group(1)) * multipliers[(match.group(2) or "").lower()] < 2 << 30:
            errors.append("The observability stack requires API_MEMORY_LIMIT of at least 2g")
    return errors


def main():
    argparse.ArgumentParser(description=__doc__).parse_args()
    project_root = Path(__file__).resolve().parents[1]
    try:
        result = subprocess.run(["docker", "compose", "--project-directory", str(project_root), "-f", str(project_root / "docker-compose.yml"), "config", "--format", "json"], check=True, capture_output=True, text=True)
        config = json.loads(result.stdout)
        service = config["services"]["api-manager"]
        effective = dict(os.environ)
        effective["API_MANAGER_IMAGE"] = service["image"]
        effective["SECRETS_DIR"] = str(Path(config["secrets"]["admin_password"]["file"]).parent)
        effective["OBSERVABILITY_STACK_ENABLED"] = str(service["environment"]["OBSERVABILITY_STACK_ENABLED"])
        effective["API_MEMORY_LIMIT"] = str(service["mem_limit"])
        errors = validate(effective, project_root)
    except (OSError, subprocess.CalledProcessError, ValueError, KeyError):
        print("FAIL: cannot resolve docker-compose.yml; check Docker Compose and configuration")
        raise SystemExit(1)
    for error in errors:
        print("FAIL: " + error)
    if not errors:
        print("PASS: Compose inputs checked; no credentials displayed")
    raise SystemExit(1 if errors else 0)


if __name__ == "__main__":
    main()
