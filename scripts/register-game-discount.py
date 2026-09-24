#!/usr/bin/env python3
"""Register Game Discount through the authenticated management API (with audit)."""
import argparse
import json
import os
from pathlib import Path
import sys
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import Request, build_opener, HTTPRedirectHandler

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / "integrations/game-discount/routes.json"


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None  # Never forward the admin credential to a redirected host.


def env_file(path):
    if not path.exists():
        return {}
    result = {}
    for line in path.read_text().splitlines():
        if line.strip() and not line.lstrip().startswith("#"):
            key, value = line.split("=", 1)
            result[key.strip()] = value.strip()
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:8080")
    parser.add_argument("--upstream-url", default="http://game-discount:8089")
    parser.add_argument("--env-file", type=Path, default=ROOT / "integrations/game-discount/.env")
    parser.add_argument("--publish", action="store_true", help="Publish newly registered drafts")
    parser.add_argument("--update", action="store_true", help="Explicitly allow updates to previously managed routes")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--client-key-file", type=Path, help="Create a gateway caller key, write it once with mode 0600")
    args = parser.parse_args()
    for value in (args.base_url, args.upstream_url):
        u = urlsplit(value)
        if u.scheme not in ("http", "https") or not u.netloc or u.username or u.query or u.fragment or u.path not in ("", "/"):
            parser.error("URLs must be HTTP(S) origins without credentials, paths, queries or fragments")
    routes = json.loads(MANIFEST.read_text())
    for item in routes:
        item["upstream_url"] = args.upstream_url.rstrip("/")
    if args.dry_run:
        print(json.dumps(routes, ensure_ascii=False, indent=2))
        return
    token = os.environ.get("API_MANAGER_ADMIN_TOKEN") or env_file(args.env_file).get("API_MANAGER_ADMIN_TOKEN")
    if not token:
        parser.error("set API_MANAGER_ADMIN_TOKEN or initialize --env-file")
    if args.client_key_file and args.client_key_file.exists():
        parser.error("client-key-file already exists; refusing to create an unrecoverable duplicate key")
    opener = build_opener(NoRedirect())

    def call(method, path, body=None):
        data = None if body is None else json.dumps(body).encode()
        request = Request(args.base_url.rstrip("/") + path, data=data, method=method,
                          headers={"X-Admin-Token": token, "Content-Type": "application/json"})
        try:
            with opener.open(request, timeout=15) as response:
                payload = response.read()
                return json.loads(payload) if payload else None
        except HTTPError as exc:
            # Do not print response bodies or credential-bearing Request objects.
            raise RuntimeError(f"{method} {path}: HTTP {exc.code}") from None

    existing = {(item["method"], item["path"]): item for item in call("GET", "/admin/v1/apis")}
    # Preflight every conflict before making changes. Never overwrite unrelated APIs.
    for route in routes:
        old = existing.get((route["method"], route["path"]))
        if old and old.get("description") != route["description"]:
            raise RuntimeError(f"路由冲突，保留现有接口：{route['path']}")
        if old and any(old.get(k) != v for k, v in route.items()) and not args.update:
            raise RuntimeError(f"已有接入配置不同，需显式 --update：{route['path']}")
    for route in routes:
        old = existing.get((route["method"], route["path"]))
        changed = old is None or any(old.get(k) != v for k, v in route.items())
        if old is None:
            item = call("POST", "/admin/v1/apis", route)
        elif changed:
            # Preserve fields that the integration manifest does not manage.
            item = call("PUT", "/admin/v1/apis/" + old["id"], {**old, **route})
        else:
            item = old
        if args.publish and (changed or not item.get("enabled") or not item.get("published_at")):
            call("POST", "/admin/v1/apis/" + item["id"] + "/publish")
        print(f"已注册 {route['method']} {route['path']}" + ("（发布）" if args.publish else ""))
    if args.client_key_file:
        args.client_key_file.parent.mkdir(parents=True, exist_ok=True)
        # Reserve the private file before minting a credential.
        fd = os.open(args.client_key_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        try:
            with os.fdopen(fd, "w") as out:
                created = call("POST", "/admin/v1/credentials", {"name": "game-discount-client"})
                out.write(created["api_key"] + "\n")
        except Exception:
            args.client_key_file.unlink(missing_ok=True)
            raise
        print(f"调用 Key 已保存到私有文件：{args.client_key_file}")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, URLError, OSError, ValueError) as exc:
        print(f"接入失败：{exc}", file=sys.stderr)
        sys.exit(1)
