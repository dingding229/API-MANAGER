#!/usr/bin/env python3
"""Create six independent deployment secrets without displaying or overwriting them."""
import argparse
import json
import subprocess
import os
from pathlib import Path
import secrets

FILES = ("admin_token", "credential_encryption_key", "metrics_token", "postgres_password", "redis_password", "grafana_admin_password")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dir", type=Path, help="Override the Compose Secret directory")
    args = parser.parse_args()
    if args.dir is None:
        root = Path(__file__).resolve().parents[1]
        try:
            result = subprocess.run(["docker", "compose", "--project-directory", str(root), "-f", str(root / "docker-compose.yml"), "config", "--format", "json"], check=True, capture_output=True, text=True)
            args.dir = Path(json.loads(result.stdout)["secrets"]["admin_token"]["file"]).parent
        except (OSError, subprocess.CalledProcessError, ValueError, KeyError):
            parser.error("install Docker Compose or explicitly supply --dir")
    target = args.dir.expanduser()
    if target.is_symlink() or target.exists():
        parser.error("directory already exists; keep existing credentials for upgrades (nothing overwritten)")
    target.mkdir(mode=0o700, parents=True, exist_ok=False)
    target.chmod(0o700)
    for name in FILES:
        fd = os.open(target / name, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(fd, "w") as output:
            output.write(secrets.token_hex(32) + "\n")
        # The private host directory protects file-backed Compose secrets. The
        # individual mounts must be readable by the non-root container users.
        (target / name).chmod(0o444)
    print(f"Created six independent credentials in {target}. No values displayed.")
    print("Start with: docker compose up -d")


if __name__ == "__main__":
    main()
