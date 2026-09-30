#!/usr/bin/env python3
"""Non-destructive production security smoke test; never prints credentials."""
import argparse
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import HTTPRedirectHandler, ProxyHandler, Request, build_opener


class NoRedirects(HTTPRedirectHandler):
    """Credentials must never be forwarded to a redirect destination."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def request(base, path, token=None, header="Authorization"):
    headers = {header: (f"Bearer {token}" if header == "Authorization" else token)} if token else {}
    # The verifier targets the direct API; do not route its credentials through
    # environment-configured proxies or follow even same-origin redirects.
    opener = build_opener(ProxyHandler({}), NoRedirects())
    try:
        with opener.open(Request(base + path, headers=headers), timeout=5) as response:
            return response.status
    except HTTPError as error:
        error.close()
        return error.code


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", required=True, help="Private direct API URL; no redirects or cached endpoints")
    parser.add_argument("--secret-dir", required=True, type=Path)
    args = parser.parse_args()
    base = args.url.rstrip("/")
    try:
        parsed = urlsplit(base)
        port = parsed.port
    except ValueError:
        parser.error("invalid API URL")
    if (not parsed.hostname or parsed.username is not None or parsed.password is not None
            or parsed.query or parsed.fragment
            or not (parsed.scheme == "https" or (parsed.scheme == "http"
                    and parsed.hostname in ("127.0.0.1", "localhost") and port))):
        parser.error("use HTTPS or a direct loopback address, without credentials, query or fragment")

    try:
        metrics = (args.secret_dir / "metrics_token").read_text().rstrip("\r\n")
        admin = (args.secret_dir / "admin_token").read_text().rstrip("\r\n")
        checks = {
            "readiness": (request(base, "/health/ready"), 200),
            "anonymous metrics denied": (request(base, "/metrics"), 401),
            "metrics bearer accepted": (request(base, "/metrics", metrics), 200),
            "bootstrap token not accepted on management APIs": (request(base, "/admin/v1/apis", admin, "X-Admin-Token"), 401),
        }
    except (OSError, URLError, ValueError):
        print("FAIL: API connection or Secret read failed; credentials are not displayed")
        return 1
    failed = False
    for name, (actual, expected) in checks.items():
        ok = actual == expected
        print(f"{'PASS' if ok else 'FAIL'} {name} (HTTP {actual}, expected {expected})")
        failed |= not ok
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
