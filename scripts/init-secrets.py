#!/usr/bin/env python3
"""Create first-install Compose secrets without printing them. Never run to rotate an existing stack."""
from pathlib import Path
import os
import secrets

root = Path(__file__).resolve().parents[1]
target = root / ".env"
if target.exists():
    raise SystemExit(".env already exists; refusing to overwrite existing secrets")
values = {
    "ADMIN_TOKEN": secrets.token_hex(32),
    "USER_JWT_SECRET": secrets.token_hex(32),
    "CREDENTIAL_ENCRYPTION_KEY": secrets.token_hex(32),
    "POSTGRES_PASSWORD": secrets.token_hex(24),
    "GRAFANA_ADMIN_PASSWORD": secrets.token_hex(24),
}
fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "w") as out:
    out.write("# New install only. Back up before changing any value. Do not commit.\n")
    for key, value in values.items():
        out.write(f"{key}={value}\n")
print("Created .env with restricted permissions; no secret values were printed.")
