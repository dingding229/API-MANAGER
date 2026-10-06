#!/usr/bin/env python3
"""Create missing deployment credentials without displaying or replacing existing files."""
import argparse
import json
import subprocess
import os
from pathlib import Path
import secrets

FILES = ("admin_bootstrap_key", "credential_encryption_key", "metrics_token", "postgres_password", "redis_password", "smtp_password", "github_update_token")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dir", type=Path, help="Override the Compose Secret directory")
    args = parser.parse_args()
    if args.dir is None:
        root = Path(__file__).resolve().parents[1]
        try:
            result = subprocess.run(["docker", "compose", "--project-directory", str(root), "-f", str(root / "docker-compose.yml"), "config", "--format", "json"], check=True, capture_output=True, text=True)
            args.dir = Path(json.loads(result.stdout)["secrets"]["admin_bootstrap_key"]["file"]).parent
        except (OSError, subprocess.CalledProcessError, ValueError, KeyError):
            parser.error("install Docker Compose or explicitly supply --dir")
    target = args.dir.expanduser()
    if target.is_symlink() or (target.exists() and not target.is_dir()):
        parser.error("Secret path must be a real directory")
    target.mkdir(mode=0o700, parents=True, exist_ok=True)
    if target.stat().st_mode & 0o077:
        parser.error("Secret directory must have mode 0700")
    created = 0
    for name in FILES:
        path = target / name
        if path.exists() or path.is_symlink():
            if path.is_symlink() or not path.is_file():
                parser.error("existing credentials must be regular files")
            continue
        fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(fd, "w") as output:
            output.write("" if name in ("smtp_password", "github_update_token") else secrets.token_hex(32) + "\n")
        path.chmod(0o444)
        created += 1
    print(f"Created {created} missing credential files in {target}; existing values kept, no values displayed.")
    print("Start with: docker compose up -d; first administrator registers with admin_bootstrap_key, choosing their own password.")


if __name__ == "__main__":
    main()
