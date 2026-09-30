#!/usr/bin/env python3
"""Offline-consistent production backup. Requires the API to be stopped first.

Set PGSERVICE/PGSERVICEFILE/PGPASSFILE for libpq; never put credentials in
arguments. The operator must separately test restores to an isolated database.
"""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile

VOLUME = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]*$")
TOOL_IMAGE = re.compile(r"^docker\.io/dingding229/api-manager:[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$")


def run(*args, **kwargs):
    return subprocess.run(args, check=True, **kwargs)


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--plugin-volume", default="api-manager-prod-plugin-data")
    parser.add_argument("--library-volume", default="api-manager-prod-plugin-library")
    parser.add_argument("--observability-volume", default="api-manager-prod-observability")
    parser.add_argument("--tool-image", default="docker.io/dingding229/api-manager:latest", help="Docker Hub image containing tar (default: docker.io/dingding229/api-manager:latest)")
    args = parser.parse_args()
    if not os.getenv("PGSERVICE") or not os.getenv("PGSERVICEFILE") or not os.getenv("PGPASSFILE"):
        parser.error("PGSERVICE, PGSERVICEFILE and PGPASSFILE must be set (no password arguments)")
    if not TOOL_IMAGE.fullmatch(args.tool_image):
        parser.error("--tool-image must use a Docker Hub tag such as docker.io/dingding229/api-manager:latest")
    for volume in (args.plugin_volume, args.library_volume, args.observability_volume):
        if not VOLUME.fullmatch(volume):
            parser.error("invalid Docker volume name")
    project = os.getenv("COMPOSE_PROJECT_NAME", "api-manager-production")
    running = run("docker", "ps", "--quiet", "--filter", f"label=com.docker.compose.project={project}",
                  "--filter", "label=com.docker.compose.service=api-manager", capture_output=True, text=True).stdout.strip()
    if running:
        parser.error("API is still running; stop writes before taking a consistent database/plugin backup")
    run("docker", "pull", "--quiet", args.tool_image)
    for volume in (args.plugin_volume, args.library_volume, args.observability_volume):
        run("docker", "volume", "inspect", volume, stdout=subprocess.DEVNULL)
    if args.output_dir.is_symlink():
        parser.error("output directory must not be a symlink")
    if not args.output_dir.exists():
        args.output_dir.mkdir(parents=True, mode=0o700)
    if args.output_dir.stat().st_mode & 0o077:
        parser.error("output directory must not be accessible to group/other users (mode 0700)")
    stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    target = args.output_dir / f"api-manager-{stamp}-{os.urandom(3).hex()}"
    target.mkdir(mode=0o700)
    try:
        database = target / "database.dump"
        run("pg_dump", "--format=custom", "--file", str(database))
        run("pg_restore", "--list", str(database), stdout=subprocess.DEVNULL)
        files = {"database.dump": digest(database)}
        for volume, name in ((args.plugin_volume, "plugins.tar"), (args.library_volume, "plugin-library.tar"), (args.observability_volume, "observability.tar")):
            output = target / name
            with output.open("xb") as stream:
                run("docker", "run", "--rm", "--network", "none", "--mount", f"type=volume,src={volume},dst=/source,readonly",
                    "--entrypoint", "tar", args.tool_image, "-C", "/source", "-cf", "-", ".", stdout=stream)
            with tarfile.open(output, "r:") as archive:
                for entry in archive:
                    if entry.name.startswith("/") or ".." in Path(entry.name).parts or not (entry.isfile() or entry.isdir()):
                        raise ValueError(f"unsafe volume archive entry in {name}")
            files[name] = digest(output)
        manifest = {"created_utc": stamp, "project": project,
                    "volumes": {"plugins": args.plugin_volume, "library": args.library_volume, "observability": args.observability_volume}, "sha256": files,
                    "note": "Restore test required; keep backup with production secrets in separate protected storage."}
        (target / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
        (target / "COMPLETE").write_text("verified archive structure and hashes; restore not verified\n")
        print(f"Backup created at {target}; an isolated restore test is still required.")
    except Exception:
        print(f"Backup FAILED; incomplete directory retained at {target}", file=sys.stderr)
        raise


if __name__ == "__main__":
    main()
