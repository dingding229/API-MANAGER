"""Preflight must reject common unsafe deployment inputs without leaking values."""
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("preflight", ROOT / "scripts/preflight-production.py")
preflight = importlib.util.module_from_spec(spec)
spec.loader.exec_module(preflight)


class PreflightTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name) / "secrets"
        self.directory.mkdir(mode=0o700)
        self.values = {
            "admin_token": "a" * 64, "user_jwt_secret": "b" * 64,
            "credential_encryption_key": "c" * 64, "metrics_token": "d" * 64,
            "postgres_dsn": "postgres://api:private@db.internal/api?sslmode=verify-full",
            "redis_password": "e" * 64,
        }
        for name, value in self.values.items():
            file = self.directory / name
            file.write_text(value + "\n")
            file.chmod(0o444)
        self.env = {"PROD_SECRETS_DIR": str(self.directory),
                    "API_MANAGER_IMAGE": "example/api@sha256:" + "a" * 64,
                    "PROD_REDIS_ADDR": "redis.internal:6380"}

    def test_valid_inputs(self):
        self.assertEqual(preflight.validate(self.env, ROOT), [])

    def test_unsafe_values_are_rejected_without_echoing_secrets(self):
        self.env["API_MANAGER_IMAGE"] = "example/api:latest"
        self.env["PROD_REDIS_ADDR"] = "localhost:6379"
        dsn = self.directory / "postgres_dsn"
        dsn.chmod(0o600)
        dsn.write_text("postgres://api:private@db.internal/api?sslmode=disable")
        dsn.chmod(0o444)
        errors = preflight.validate(self.env, ROOT)
        self.assertGreaterEqual(len(errors), 3)
        self.assertNotIn("private", " ".join(errors))

    def test_private_ca_override_requires_readable_certificates(self):
        ca = Path(self.temp.name) / "cas"
        ca.mkdir(mode=0o755)
        self.env["PROD_CA_CERTS_DIR"] = str(ca)
        self.assertTrue(preflight.validate(self.env, ROOT))
        for name in ("redis-ca.pem", "postgres-ca.pem"):
            file = ca / name
            file.write_text("-----BEGIN CERTIFICATE-----\nfixture\n-----END CERTIFICATE-----\n")
            file.chmod(0o444)
        self.assertEqual(preflight.validate(self.env, ROOT), [])

    def test_symlink_or_exposed_directory_is_rejected(self):
        (self.directory / "metrics_token").unlink()
        (self.directory / "metrics_token").symlink_to(self.directory / "admin_token")
        self.directory.chmod(0o755)
        self.assertTrue(preflight.validate(self.env, ROOT))


if __name__ == "__main__":
    unittest.main()
