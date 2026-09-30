#!/usr/bin/env python3
"""Regression checks for production scripts without live dependencies or secrets."""
import importlib.util
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import tempfile
import threading
import unittest

ROOT = Path(__file__).resolve().parents[1]


def load(name):
    spec = importlib.util.spec_from_file_location(name, ROOT / "scripts" / f"{name}.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


preflight = load("preflight-production")
backup = load("backup-production")
verify = load("verify-production")


class ProductionScriptTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.secret_dir = Path(self.temp.name)
        self.secret_dir.chmod(0o700)
        values = {
            name: f"regression-{name}-{'x' * 32}" for name in preflight.FILES
        }
        values["postgres_dsn"] = "postgres://test:test@postgres.example:5432/test?sslmode=verify-full"
        for name, value in values.items():
            path = self.secret_dir / name
            path.write_text(value + "\n")
            path.chmod(0o444)
        self.env = {"PROD_SECRETS_DIR": str(self.secret_dir), "PROD_REDIS_ADDR": "redis.example:6380"}

    def test_default_and_empty_override_match_compose(self):
        for override in (None, "", preflight.DEFAULT_IMAGE, "docker.io/dingding229/api-manager:0.2.2"):
            with self.subTest(override=override):
                env = self.env.copy()
                if override is not None:
                    env["API_MANAGER_IMAGE"] = override
                self.assertEqual(preflight.validate(env, ROOT), [])

    def test_rejects_non_hub_and_digest_overrides(self):
        for image in ("ghcr.io/example/api-manager:latest", "api-manager:local",
                      "docker.io/dingding229/api-manager@sha256:" + "a" * 64,
                      "docker.io/dingding229/api-manager:latest other"):
            with self.subTest(image=image):
                env = dict(self.env, API_MANAGER_IMAGE=image)
                self.assertTrue(any("API_MANAGER_IMAGE" in e for e in preflight.validate(env, ROOT)))

    def test_observability_memory_can_use_bytes_or_units(self):
        for memory in ("2g", "2048m", "2147483648"):
            with self.subTest(memory=memory):
                env = dict(self.env, PROD_OBSERVABILITY_STACK_ENABLED="true", PROD_API_MEMORY_LIMIT=memory)
                self.assertEqual(preflight.validate(env, ROOT), [])
        for memory in ("1024", "1g", "invalid", ""):
            with self.subTest(memory=memory):
                env = dict(self.env, PROD_OBSERVABILITY_STACK_ENABLED="true", PROD_API_MEMORY_LIMIT=memory)
                self.assertTrue(any("2g" in e for e in preflight.validate(env, ROOT)))

    def test_backup_uses_same_hub_tag_policy(self):
        self.assertTrue(backup.TOOL_IMAGE.fullmatch(preflight.DEFAULT_IMAGE))
        self.assertFalse(backup.TOOL_IMAGE.fullmatch("ghcr.io/example/tool:latest"))

    def test_verifier_does_not_follow_credential_redirect(self):
        received = []

        class Target(BaseHTTPRequestHandler):
            def do_GET(self):
                received.append(dict(self.headers))
                self.send_response(200)
                self.end_headers()

            def log_message(self, *args):
                pass

        target = ThreadingHTTPServer(("127.0.0.1", 0), Target)
        target_thread = threading.Thread(target=target.serve_forever, daemon=True)
        target_thread.start()

        class Redirect(BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(302)
                self.send_header("Location", f"http://127.0.0.1:{target.server_port}/target")
                self.end_headers()

            def log_message(self, *args):
                pass

        redirect = ThreadingHTTPServer(("127.0.0.1", 0), Redirect)
        redirect_thread = threading.Thread(target=redirect.serve_forever, daemon=True)
        redirect_thread.start()
        try:
            base = f"http://127.0.0.1:{redirect.server_port}"
            self.assertEqual(verify.request(base, "/metrics", "test-only-token"), 302)
            self.assertEqual(received, [])
        finally:
            redirect.shutdown()
            redirect.server_close()
            target.shutdown()
            target.server_close()
            redirect_thread.join()
            target_thread.join()


if __name__ == "__main__":
    unittest.main()
