#!/usr/bin/env python3
"""Validate production Compose inputs without connecting or displaying secrets.

Run before `docker compose -f compose.production.yaml up`. This validates local
inputs only; it does not replace TLS handshakes, restore tests, or a staging run.
"""
import argparse
import os
from pathlib import Path
import re
import stat
from urllib.parse import urlsplit, parse_qs

IMAGE = re.compile(r"^[^\s@]+@sha256:[0-9a-f]{64}$")
FILES = ("admin_token", "user_jwt_secret", "credential_encryption_key",
         "metrics_token", "postgres_dsn", "redis_password")


def validate(environ, project_root):
    errors = []
    image = environ.get("API_MANAGER_IMAGE", "")
    if not IMAGE.fullmatch(image):
        errors.append("API_MANAGER_IMAGE must be pinned to a full sha256 digest")
    directory = environ.get("PROD_SECRETS_DIR", "")
    if not directory or not Path(directory).is_absolute():
        return errors + ["PROD_SECRETS_DIR must be an absolute private path"]
    folder = Path(directory)
    if folder.is_symlink() or not folder.is_dir():
        return errors + ["PROD_SECRETS_DIR must be a real directory, not a symlink"]
    if folder.resolve() == project_root or project_root in folder.resolve().parents:
        errors.append("production secrets must be outside the repository")
    if stat.S_IMODE(folder.stat().st_mode) != 0o700:
        errors.append("PROD_SECRETS_DIR must have mode 0700")

    values = {}
    for name in FILES:
        path = folder / name
        if path.is_symlink() or not path.is_file():
            errors.append(f"{name} must be a regular file, not a symlink")
            continue
        if stat.S_IMODE(path.stat().st_mode) != 0o444:
            errors.append(f"{name} must have mode 0444 for non-root Compose bind mounts")
        if path.stat().st_size > 65536:
            errors.append(f"{name} is too large")
            continue
        try:
            values[name] = path.read_text(encoding="utf-8").rstrip("\r\n")
        except (UnicodeError, OSError):
            errors.append(f"{name} is not readable text")
            continue
        if not values[name] or "\x00" in values[name]:
            errors.append(f"{name} must contain a non-empty text value")

    app_secrets = [values.get(n, "") for n in FILES[:4]]
    if any(len(s) < 32 for s in app_secrets) or len(set(app_secrets)) != 4:
        errors.append("application and metrics secrets must be distinct and at least 32 characters")
    redis_password = values.get("redis_password", "")
    if not redis_password or redis_password in app_secrets:
        errors.append("Redis password must be non-empty and distinct from application secrets")
    try:
        dsn = urlsplit(values.get("postgres_dsn", ""))
        sslmodes = parse_qs(dsn.query).get("sslmode", [])
        if dsn.scheme not in ("postgres", "postgresql") or not dsn.hostname or sslmodes != ["verify-full"]:
            raise ValueError
        if dsn.hostname in ("localhost", "127.0.0.1", "::1"):
            raise ValueError
        # Accessing .port also validates that an explicitly supplied port is well-formed.
        _ = dsn.port
    except ValueError:
        errors.append("postgres_dsn must use a non-loopback PostgreSQL URL with sslmode=verify-full")
    address = environ.get("PROD_REDIS_ADDR", "")
    try:
        host, port = address.rsplit(":", 1)
        if host.startswith("[") and host.endswith("]"):
            host = host[1:-1]
        if not host or host in ("localhost", "127.0.0.1", "::1") or not (1 <= int(port) <= 65535):
            raise ValueError
    except ValueError:
        errors.append("PROD_REDIS_ADDR must be a non-loopback host:port for Redis TLS")
    ca_dir = environ.get("PROD_CA_CERTS_DIR", "")
    if ca_dir:
        ca = Path(ca_dir)
        if not ca.is_absolute() or ca.is_symlink() or not ca.is_dir():
            errors.append("PROD_CA_CERTS_DIR must be an absolute real directory")
        else:
            if not (ca.stat().st_mode & stat.S_IXOTH):
                errors.append("PROD_CA_CERTS_DIR must be traversable by the non-root container")
            for name in ("redis-ca.pem", "postgres-ca.pem"):
                path = ca / name
                if path.is_symlink() or not path.is_file() or not (path.stat().st_mode & stat.S_IROTH):
                    errors.append(f"{name} must be a readable regular CA certificate")
                    continue
                if path.stat().st_size > 65536:
                    errors.append(f"{name} is too large")
                    continue
                content = path.read_bytes()
                if b"-----BEGIN CERTIFICATE-----" not in content or b"PRIVATE KEY" in content:
                    errors.append(f"{name} must contain only public CA certificates")
    return errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    args = parser.parse_args()
    project_root = Path(__file__).resolve().parents[1]
    errors = validate(os.environ, project_root)
    if errors:
        for error in errors:
            print("FAIL: " + error)
        raise SystemExit(1)
    print("PASS: local production inputs validated (no network or secret values displayed)")


if __name__ == "__main__":
    main()
