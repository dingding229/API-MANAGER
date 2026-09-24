"""Offline backup safety behavior with fake Docker/libpq clients (no live data)."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
FAKE_TOOL = '''#!/usr/bin/env python3
import io, os, pathlib, sys, tarfile
name = pathlib.Path(sys.argv[0]).name
if name == "docker":
    if sys.argv[1] == "ps":
        if os.environ.get("FAKE_API_RUNNING"): print("container-id")
    elif sys.argv[1] == "run":
        with tarfile.open(fileobj=sys.stdout.buffer, mode="w|") as archive:
            value = b"fixture content"
            item = tarfile.TarInfo("./plugin.wasm")
            item.size = len(value)
            archive.addfile(item, io.BytesIO(value))
elif name == "pg_dump":
    pathlib.Path(sys.argv[sys.argv.index("--file") + 1]).write_bytes(b"fake custom dump")
'''


class BackupTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        tools = self.root / "tools"
        tools.mkdir()
        for name in ("docker", "pg_dump", "pg_restore"):
            file = tools / name
            file.write_text(FAKE_TOOL)
            file.chmod(0o700)
        self.env = dict(os.environ, PATH=f"{tools}:{os.environ['PATH']}",
                        PGSERVICE="test", PGSERVICEFILE="/not-used-in-mock", PGPASSFILE="/not-used-in-mock")
        self.cmd = ["python3", str(ROOT / "scripts/backup-production.py"),
                    "--output-dir", str(self.root / "backups"),
                    "--tool-image", "example/tar@sha256:" + "a" * 64]

    def test_creates_complete_manifest_without_secret_output(self):
        result = subprocess.run(self.cmd, env=self.env, capture_output=True, text=True, check=True)
        target, = (self.root / "backups").iterdir()
        self.assertTrue((target / "COMPLETE").is_file())
        manifest = json.loads((target / "manifest.json").read_text())
        self.assertEqual(set(manifest["sha256"]), {"database.dump", "plugins.tar", "plugin-library.tar"})
        self.assertEqual(target.stat().st_mode & 0o777, 0o700)
        self.assertNotIn("fake custom dump", result.stdout)

    def test_rejects_running_api_before_writing(self):
        env = dict(self.env, FAKE_API_RUNNING="1")
        result = subprocess.run(self.cmd, env=env, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / "backups").exists())


if __name__ == "__main__":
    unittest.main()
