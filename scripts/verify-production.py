#!/usr/bin/env python3
"""Non-destructive production security smoke test; never prints credentials."""
import argparse
from pathlib import Path
from urllib.error import HTTPError
from urllib.request import Request, urlopen

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--url", required=True, help="Private direct API URL; do not use an externally cached endpoint")
parser.add_argument("--secret-dir", required=True, type=Path)
args = parser.parse_args()
base = args.url.rstrip("/")
if not base.startswith(("http://127.0.0.1:", "http://localhost:", "https://")):
    parser.error("use HTTPS or a direct loopback address")


def request(path, token=None, header="Authorization"):
    headers = {header: (f"Bearer {token}" if header == "Authorization" else token)} if token else {}
    try:
        with urlopen(Request(base+path, headers=headers), timeout=5) as response:
            return response.status
    except HTTPError as error:
        return error.code


metrics = (args.secret_dir / "metrics_token").read_text().strip()
admin = (args.secret_dir / "admin_token").read_text().strip()
checks = {
    "readiness": (request("/health/ready"), 200),
    "anonymous metrics denied": (request("/metrics"), 401),
    "metrics bearer accepted": (request("/metrics", metrics), 200),
    "bootstrap token not accepted on management APIs": (request("/admin/v1/apis", admin, "X-Admin-Token"), 401),
}
failed = False
for name, (actual, expected) in checks.items():
    ok = actual == expected
    print(f"{'PASS' if ok else 'FAIL'} {name} (HTTP {actual}, expected {expected})")
    failed |= not ok
raise SystemExit(1 if failed else 0)
