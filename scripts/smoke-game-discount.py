#!/usr/bin/env python3
"""Build and exercise both real Go services in isolated in-memory processes.

No live deployment, database, credentials or game feed are changed. This tests
actual service contracts, not PostgreSQL/Docker startup.
"""
import argparse
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import sys
import tempfile
import time
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parents[1]


def port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def request(origin, path, key="", admin="", method="GET", body=None, headers=None):
    h = {"Content-Type": "application/json", **(headers or {})}
    if key:
        h["X-API-Key"] = key
    if admin:
        h["X-Admin-Token"] = admin
    req = Request(origin + path, method=method, headers=h,
                  data=None if body is None else json.dumps(body).encode())
    try:
        response = urlopen(req, timeout=5)
    except HTTPError as exc:
        response = exc
    with response:
        return response.code, response.headers, response.read()


def wait_ready(origin, path, proc):
    for _ in range(100):
        if proc.poll() is not None:
            raise RuntimeError("service exited during startup")
        try:
            if request(origin, path)[0] == 200:
                return
        except (URLError, OSError):
            pass
        time.sleep(0.1)
    raise RuntimeError("service startup timed out")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--project", type=Path, default=ROOT.parent / "game-discount-api")
    args = parser.parse_args()
    game_project = args.project.resolve()
    with tempfile.TemporaryDirectory(prefix="game-discount-integration-") as folder:
        tmp = Path(folder)
        for project, binary in [(ROOT, "gateway"), (game_project, "game")]:
            subprocess.run(["go", "build", "-o", str(tmp / binary), "./cmd/server"], cwd=project, check=True)
        game_port, gateway_port = port(), port()
        while game_port == gateway_port:
            gateway_port = port()
        game, gateway = f"http://127.0.0.1:{game_port}", f"http://127.0.0.1:{gateway_port}"
        upstream_key, upstream_admin, gateway_admin = (secrets.token_hex(32) for _ in range(3))
        game_env = {**os.environ, "PORT": str(game_port), "ADMIN_TOKEN": upstream_admin,
                    "GATEWAY_API_KEY": upstream_key, "DATABASE_URL": "", "RATE_LIMIT": "6000",
                    "ANON_RATE_LIMIT": "12000", "FEED_ALLOW_STALE": "false", "TRUST_PROXY_HEADERS": "false",
                    "FEED_FILE": str(game_project / "docs/authorized-feed.example.json")}
        gateway_env = {**os.environ, "HTTP_ADDR": f"127.0.0.1:{gateway_port}", "ADMIN_TOKEN": gateway_admin,
                       "POSTGRES_DSN": "", "USE_REDIS": "false", "OTEL_ENABLED": "false",
                       "PLUGIN_DIR": str(tmp / "plugins"), "USER_JWT_SECRET": secrets.token_hex(32), "CREDENTIAL_ENCRYPTION_KEY": secrets.token_hex(32),
                       "API_UPSTREAM_CREDENTIALS": json.dumps({"game-discount": {"origin": game, "api_key": upstream_key}})}
        processes = []
        with (tmp / "game.log").open("w+") as game_log, (tmp / "gateway.log").open("w+") as gateway_log:
            try:
                processes.append(subprocess.Popen([str(tmp / "game")], env=game_env, cwd=game_project, stdout=game_log, stderr=game_log))
                processes.append(subprocess.Popen([str(tmp / "gateway")], env=gateway_env, cwd=ROOT, stdout=gateway_log, stderr=gateway_log))
                wait_ready(game, "/health", processes[0])
                wait_ready(gateway, "/health/ready", processes[1])
                register = [sys.executable, str(ROOT / "scripts/register-game-discount.py"), "--base-url", gateway,
                            "--upstream-url", game, "--publish"]
                reg_env = {**os.environ, "API_MANAGER_ADMIN_TOKEN": gateway_admin}
                key_path = tmp / "client.key"
                subprocess.run(register + ["--client-key-file", str(key_path)], env=reg_env, check=True)
                caller_key = key_path.read_text().strip()
                first = json.loads(request(gateway, "/admin/v1/apis", admin=gateway_admin)[2])
                assert len(first) == 3
                releases = {a["id"]: request(gateway, f"/admin/v1/apis/{a['id']}/releases", admin=gateway_admin)[2] for a in first}
                subprocess.run(register, env=reg_env, check=True)
                for api in first:
                    assert request(gateway, f"/admin/v1/apis/{api['id']}/releases", admin=gateway_admin)[2] == releases[api["id"]], "idempotent registration created releases"
                prefix = "/api/game-discount/v1"
                assert request(gateway, prefix + "/offers?region=HK")[0] == 401
                assert request(gateway, prefix + "/status", key=upstream_key)[0] == 401
                assert request(game, "/v1/status", key=caller_key)[0] == 401
                code, h, body = request(gateway, prefix + "/offers?region=HK&limit=1", key=caller_key,
                                        headers={"X-Request-ID": "game-discount-smoke"})
                assert code == 200, (code, body)
                data = json.loads(body)
                assert data["offers"] and len(data["offers"]) == 1
                assert h["X-Request-ID"] == "game-discount-smoke"
                assert request(gateway, prefix + "/offers?region=HK&limit=1", key=caller_key,
                               headers={"If-None-Match": h["ETag"]})[0] == 304
                external_id = data["offers"][0]["external_id"]
                assert request(gateway, prefix + "/offers/" + external_id, key=caller_key)[0] == 200
                assert request(gateway, prefix + "/offers/does-not-exist", key=caller_key)[0] == 404
                assert request(gateway, prefix + "/offers?region=US", key=caller_key)[0] == 400
                assert request(gateway, prefix + "/offers?region=HK&page_cursor=invalid", key=caller_key)[0] == 400
                assert request(gateway, prefix + "/offers?region=HK&sync_cursor=stale", key=caller_key)[0] == 410
                assert request(gateway, prefix + "/status", headers={"Authorization": "Bearer " + caller_key})[0] == 200
                assert request(gateway, "/api/game-discount/api/admin/keys", key=caller_key)[0] == 404
                # Feed fixture may grow; verify subsequent page when one exists.
                if data["page"]["has_more"]:
                    from urllib.parse import quote
                    cursor = quote(data["page"]["next_cursor"], safe="")
                    code, _, page2 = request(gateway, prefix + "/offers?region=HK&limit=1&page_cursor=" + cursor, key=caller_key)
                    assert code == 200
                    assert json.loads(page2)["offers"][0]["external_id"] != external_id
                status_api = next(a for a in first if a["path"].endswith("/status"))
                status_api["rate_limit_per_minute"] = 1
                assert request(gateway, "/admin/v1/apis/" + status_api["id"], admin=gateway_admin,
                               method="PUT", body=status_api)[0] == 200
                assert request(gateway, prefix + "/status", key=caller_key)[0] == 429
                assert request(gateway, prefix + "/status", headers={"Authorization": "Bearer " + caller_key})[0] == 429
                metrics = request(gateway, "/metrics")[2]
                assert b"api_manager_gateway_requests_total" in metrics
                # Revoking upstream service identity must not be bypassed by the gateway.
                assert request(game, "/api/admin/keys/revoke", admin=upstream_admin, method="POST",
                               body={"key_id": "api-manager-gateway"})[0] == 200
                assert request(gateway, prefix + "/offers?region=HK", key=caller_key)[0] == 401
                print("PASS: real services, idempotent registration, auth isolation, parameter mapping, pagination, ETag/304, 400/404/410/429, request ID, metrics and upstream revocation")
            finally:
                for proc in processes:
                    if proc.poll() is None:
                        proc.terminate()
                for proc in processes:
                    try:
                        proc.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        proc.kill()
                        proc.wait()


if __name__ == "__main__":
    main()
