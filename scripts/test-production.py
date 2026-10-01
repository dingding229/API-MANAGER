#!/usr/bin/env python3
"""Regression checks for deployment scripts; uses only temporary fake secrets."""
import importlib.util
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import subprocess
import sys
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
verify = load("verify-production")


class ProductionScriptTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.folder = Path(self.temp.name) / "secrets"
        subprocess.run([sys.executable, str(ROOT / "scripts/init-production-secrets.py"), "--dir", str(self.folder)], check=True, stdout=subprocess.DEVNULL)
        self.env = {"SECRETS_DIR": str(self.folder)}

    def test_initialization_is_complete_and_never_overwrites(self):
        before = {p.name: p.read_bytes() for p in self.folder.iterdir()}
        self.assertEqual(set(before), set(preflight.FILES))
        self.assertEqual(len(set(before.values())), 6)
        result = subprocess.run([sys.executable, str(ROOT / "scripts/init-production-secrets.py"), "--dir", str(self.folder)], capture_output=True)
        self.assertEqual(result.returncode, 0)
        self.assertEqual(before, {p.name: p.read_bytes() for p in self.folder.iterdir()})

    def test_latest_and_version_tag_are_accepted(self):
        for image in (None, "", preflight.DEFAULT_IMAGE, "docker.io/dingding229/api-manager:0.3.0"):
            env = dict(self.env)
            if image is not None:
                env["API_MANAGER_IMAGE"] = image
            self.assertEqual(preflight.validate(env, ROOT), [])

    def test_other_registries_and_digest_pins_are_rejected(self):
        for image in ("ghcr.io/example/api:latest", "api-manager:local", "docker.io/dingding229/api-manager@sha256:" + "a" * 64):
            self.assertTrue(preflight.validate(dict(self.env, API_MANAGER_IMAGE=image), ROOT))

    def test_duplicate_and_symlink_credentials_are_rejected(self):
        source = self.folder / "admin_password"
        target = self.folder / "metrics_token"
        target.chmod(0o600)
        target.write_bytes(source.read_bytes())
        target.chmod(0o444)
        self.assertTrue(preflight.validate(self.env, ROOT))
        target.unlink()
        target.symlink_to(source)
        self.assertTrue(preflight.validate(self.env, ROOT))

    def test_stack_memory_bounds(self):
        for memory in ("2g", "2048m", "2147483648", ""):
            env = dict(self.env, OBSERVABILITY_STACK_ENABLED="true", API_MEMORY_LIMIT=memory)
            self.assertEqual(preflight.validate(env, ROOT), [])
        for memory in ("1g", "invalid"):
            env = dict(self.env, OBSERVABILITY_STACK_ENABLED="true", API_MEMORY_LIMIT=memory)
            self.assertTrue(preflight.validate(env, ROOT))

    def test_verifier_never_follows_key_redirects(self):
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
            self.assertEqual(verify.request(f"http://127.0.0.1:{redirect.server_port}", "/metrics", "fake-key"), 302)
            self.assertEqual(received, [])
        finally:
            redirect.shutdown(); redirect.server_close()
            target.shutdown(); target.server_close()
            redirect_thread.join(); target_thread.join()


if __name__ == "__main__":
    unittest.main()
