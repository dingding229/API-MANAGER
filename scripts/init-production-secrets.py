#!/usr/bin/env python3
"""Create fresh per-deployment API secrets; never prints or overwrites values."""
import argparse
import os
from pathlib import Path
import secrets

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--dir", type=Path, required=True, help="New private directory outside this repository")
args = parser.parse_args()
root = Path(__file__).resolve().parents[1]
target = args.dir.expanduser().resolve(strict=False)
if target == root or root in target.parents or target.exists():
    parser.error("choose a new directory outside the repository; existing secrets cannot be overwritten")
target.mkdir(mode=0o700, parents=True, exist_ok=False)
for name in ("admin_token", "user_jwt_secret", "credential_encryption_key", "metrics_token", "grafana_admin_password"):
    fd = os.open(target / name, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, "w") as output:
        output.write(secrets.token_hex(32) + "\n")
    # File-backed Compose secrets are bind mounts: UID/mode remapping is ignored.
    # The 0700 parent protects host access; 0444 lets the non-root container read.
    os.chmod(target / name, 0o444)
print(f"Generated five independent secret files in {target}; no values printed.")
print("Provision postgres_dsn and redis_password separately under this 0700 directory (mode 0444 for non-root Compose file mounts); do not commit them.")
