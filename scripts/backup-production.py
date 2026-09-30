#!/usr/bin/env python3
"""Offline backup of the single Compose deployment. Stop API and Redis first."""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile

ROOT = Path(__file__).resolve().parents[1]
COMPOSE = ["docker", "compose", "--project-directory", str(ROOT), "-f", str(ROOT / "docker-compose.yml")]


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()
    config = json.loads(run(*COMPOSE, "config", "--format", "json", capture_output=True, text=True).stdout)
    for service in ("api-manager", "redis"):
        running = run(*COMPOSE, "ps", "--status", "running", "-q", service, capture_output=True, text=True).stdout.strip()
        if running:
            parser.error("stop api-manager and redis before creating a consistent backup")
    if args.output_dir.is_symlink():
        parser.error("output directory must not be a symlink")
    args.output_dir.mkdir(parents=True, mode=0o700, exist_ok=True)
    if args.output_dir.stat().st_mode & 0o077:
        parser.error("backup directory must have mode 0700")
    stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    target = args.output_dir / f"api-manager-{stamp}-{os.urandom(3).hex()}"
    target.mkdir(mode=0o700)
    tool_image = config["services"]["api-manager"]["image"]
    run("docker", "pull", "--quiet", tool_image)
    database = target / "database.dump"
    with database.open("xb") as stream:
        run(*COMPOSE, "exec", "-T", "postgres", "pg_dump", "-U", "api_manager", "-d", "api_manager", "--format=custom", stdout=stream)
    with database.open("rb") as stream:
        run(*COMPOSE, "exec", "-T", "postgres", "pg_restore", "--list", stdin=stream, stdout=subprocess.DEVNULL)
    files = {database.name: digest(database)}
    for logical, name in (("redis_data", "redis.tar"), ("plugin_data", "plugins.tar"), ("plugin_library", "plugin-library.tar"), ("observability_data", "observability.tar")):
        volume = config["volumes"][logical]["name"]
        run("docker", "volume", "inspect", volume, stdout=subprocess.DEVNULL)
        output = target / name
        with output.open("xb") as stream:
            run("docker", "run", "--rm", "--network", "none", "--read-only", "--user", "0:0",
                "--cap-drop", "ALL", "--cap-add", "DAC_READ_SEARCH", "--security-opt", "no-new-privileges:true",
                "--mount", f"type=volume,src={volume},dst=/source,readonly", "--entrypoint", "tar", tool_image,
                "-C", "/source", "-cf", "-", ".", stdout=stream)
        with tarfile.open(output, "r:") as archive:
            for entry in archive:
                if entry.name.startswith("/") or ".." in Path(entry.name).parts or not (entry.isfile() or entry.isdir()):
                    raise ValueError("unsafe volume archive entry")
        files[name] = digest(output)
    (target / "manifest.json").write_text(json.dumps({"created_utc": stamp, "sha256": files, "note": "Credentials are not included; an isolated restore test is required."}, indent=2) + "\n")
    (target / "COMPLETE").write_text("Archive structure and checksums verified; restore not verified.\n")
    print(f"Backup created at {target}. Back up secrets separately and test restoration.")


if __name__ == "__main__":
    main()
