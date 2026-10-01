#!/usr/bin/env python3
"""Run boundary tests against the actual Fumadocs projection and Next.js route."""
from pathlib import Path
import subprocess

if __name__ == "__main__":
    root = Path(__file__).resolve().parents[1]
    raise SystemExit(subprocess.call(["npm", "test"], cwd=root / "public-ui"))
