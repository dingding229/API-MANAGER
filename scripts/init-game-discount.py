#!/usr/bin/env python3
"""Create local integration credentials once; never print or overwrite secrets."""
import argparse
import os
from pathlib import Path
import secrets

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=ROOT / "integrations/game-discount/.env")
    args = parser.parse_args()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    if args.output.exists():
        print(f"保留现有配置，不覆盖：{args.output}")
        return
    values = {
        "API_MANAGER_ADMIN_TOKEN": os.environ.get("API_MANAGER_ADMIN_TOKEN") or secrets.token_hex(32),
        "GAME_DISCOUNT_GATEWAY_KEY": secrets.token_hex(32),
        "GAME_DISCOUNT_ADMIN_TOKEN": secrets.token_hex(32),
        "GAME_DISCOUNT_DB_PASSWORD": secrets.token_hex(24),
    }
    # Generated values are hexadecimal. An existing admin token must be a simple
    # env value too; rejecting ambiguity is safer than dotenv interpolation.
    if any(not v or any(c not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" for c in v) for v in values.values()):
        parser.error("existing admin token must contain only letters, digits, '-' and '_'")
    fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as out:
        out.write("# Private integration config; do not commit.\n")
        for key, value in values.items():
            out.write(f"{key}={value}\n")
    print(f"已生成私有配置（0600）：{args.output}")


if __name__ == "__main__":
    main()
