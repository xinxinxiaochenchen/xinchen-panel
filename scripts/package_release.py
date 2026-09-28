#!/usr/bin/env python3
"""Create a minimal, secret-free release archive for the VPS Compose deployment."""

from __future__ import annotations

import argparse
import tarfile
from pathlib import Path


BINARIES = (
    "control-plane",
    "migrate",
    "admin-bootstrap",
    "node-bootstrap",
    "agent",
    "agent-token",
)
COMPOSE_FILES = (
    "compose.yaml",
    "compose.agent-local.yaml",
    "compose.agent-tls.yaml",
    "compose.proxy-secrets.yaml",
    "compose.relay-secrets.yaml",
    "Dockerfile.prebuilt",
    "Dockerfile.agent",
    ".env.example",
)


def _is_linux_amd64(path: Path) -> bool:
    data = path.read_bytes()
    return len(data) >= 20 and data[:4] == b"\x7fELF" and data[4] == 2 and data[5] == 1 and int.from_bytes(data[18:20], "little") == 62


def collect_release_files(root: Path) -> list[Path]:
    missing = [root / "bin" / name for name in BINARIES if not (root / "bin" / name).is_file()]
    if missing:
        raise ValueError("missing release binary: " + ", ".join(str(path.relative_to(root)) for path in missing))
    symlinks = [root / "bin" / name for name in BINARIES if (root / "bin" / name).is_symlink()]
    if symlinks:
        raise ValueError("release binaries must not be symlinked: " + ", ".join(path.name for path in symlinks))
    wrong = [root / "bin" / name for name in BINARIES if not _is_linux_amd64(root / "bin" / name)]
    if wrong:
        raise ValueError("release binaries must be Linux amd64: " + ", ".join(path.name for path in wrong))

    migrations = sorted((root / "migrations").glob("[0-9][0-9][0-9][0-9][0-9][0-9]_*.up.sql"))
    for up in migrations:
        down = up.with_name(up.name.replace(".up.sql", ".down.sql"))
        if not down.is_file():
            raise ValueError(f"migration pair missing for {up.name}")
    if not migrations:
        raise ValueError("migration files are missing")
    web_index = root / "apps" / "web" / "dist" / "index.html"
    if not web_index.is_file():
        raise ValueError("WebUI build is missing: apps/web/dist/index.html")
    missing_compose = [root / "deployments" / "compose" / name for name in COMPOSE_FILES if not (root / "deployments" / "compose" / name).is_file()]
    if missing_compose:
        raise ValueError("Compose files are missing: " + ", ".join(path.name for path in missing_compose))

    docs = [root / "README.md", root / "docs" / "deployment" / "vps-webui.md"]
    missing_docs = [path for path in docs if not path.is_file()]
    if missing_docs:
        raise ValueError("release documentation is missing: " + ", ".join(str(path.relative_to(root)) for path in missing_docs))

    paths = docs + [root / "bin" / name for name in BINARIES]
    paths += [root / "apps" / "web" / "dist"]
    paths += [root / "migrations"]
    paths += [root / "deployments" / "compose" / name for name in COMPOSE_FILES]
    return paths


def _iter_files(path: Path):
    if path.is_symlink():
        raise ValueError(f"release artifact must not be a symlink: {path}")
    if path.is_dir():
        for child in sorted(path.rglob("*")):
            if child.is_symlink():
                raise ValueError(f"release artifact must not be a symlink: {child}")
            if child.is_file() and not any(part.startswith(".") for part in child.relative_to(path).parts):
                yield child
    elif path.is_file():
        yield path


def write_release(root: Path, output: Path) -> None:
    files = collect_release_files(root)
    output.parent.mkdir(parents=True, exist_ok=True)
    with tarfile.open(output, "w:gz") as archive:
        for path in sorted({item for source in files for item in _iter_files(source)}):
            relative = path.relative_to(root)
            info = archive.gettarinfo(str(path), arcname=str(relative))
            info.mtime = 0
            with path.open("rb") as stream:
                archive.addfile(info, stream)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    output = args.output or args.root / "release.tar.gz"
    write_release(args.root, output)
    print(output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
