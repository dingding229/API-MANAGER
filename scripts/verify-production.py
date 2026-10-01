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


def request(base, path, token=None, header="X-API-Key"):
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
    parser.add_argument("--secret-dir", default=Path(__file__).resolve().parents[1] / "secrets", type=Path)
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
        fake_business_key = "ak_" + "invalid-key-for-auth-isolation-check"
        checks = {
            "readiness": (request(base, "/health/ready"), 200),
            "public frontend": (request(base, "/"), 200),
            "public catalog": (request(base, "/catalog.json"), 200),
            "anonymous metrics denied": (request(base, "/metrics"), 401),
            "metrics KEY accepted": (request(base, "/metrics", metrics), 200),
            "anonymous management denied": (request(base, "/admin/v1/apis"), 401),
            "management rejects API KEY": (request(base, "/admin/v1/apis", fake_business_key), 401),
            "metrics KEY cannot administer": (request(base, "/admin/v1/apis", metrics), 401),
            "console rejects API KEY": (request(base, "/auth/v1/me", fake_business_key), 401),
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
