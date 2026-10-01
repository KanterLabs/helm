#!/usr/bin/env python3
"""Build Helm release artifacts from one checked-in release configuration.

This tool owns the product build: frontend embedding, static Linux binaries,
archives, native package staging, and OCI image builds. Release receipts and
publication remain the responsibility of the separately vendored release
integrity tool used by CI.
"""

from __future__ import annotations

import argparse
import gzip
import json
import os
import re
import shutil
import struct
import subprocess
import sys
import tarfile
import tempfile
from contextlib import contextmanager
from pathlib import Path
from typing import Any, Iterable


ROOT = Path(__file__).resolve().parents[1]
SEMVER = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$")
SUPPORTED_PLATFORMS = {"linux/amd64", "linux/arm64"}


def fail(message: str) -> "NoReturn":
    raise SystemExit(f"build-release: {message}")


def run(command: list[str], *, cwd: Path | None = None, env: dict[str, str] | None = None) -> None:
    try:
        subprocess.run(command, cwd=cwd, env=env, check=True)
    except FileNotFoundError:
        fail(f"required command is missing: {command[0]}")
    except subprocess.CalledProcessError as error:
        fail(f"command failed with exit {error.returncode}: {' '.join(command)}")


def read_config(path: Path) -> dict[str, Any]:
    try:
        config = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        fail(f"could not read release config {path}: {error}")
    if not isinstance(config, dict):
        fail("release config must be a JSON object")
    version = config.get("version")
    if not isinstance(version, str) or not SEMVER.fullmatch(version):
        fail("release config version must be semantic x.y.z text")
    platforms = config.get("platforms")
    if not isinstance(platforms, list) or not platforms:
        fail("release config must contain platforms")
    seen: set[str] = set()
    for item in platforms:
        if not isinstance(item, dict):
            fail("each release platform must be an object")
        identity = f"{item.get('os', 'linux')}/{item.get('arch', '')}"
        if identity not in SUPPORTED_PLATFORMS:
            fail(f"unsupported release platform: {identity}")
        if identity in seen:
            fail(f"duplicate release platform: {identity}")
        seen.add(identity)
    missing = SUPPORTED_PLATFORMS - seen
    if missing:
        fail(f"release config must include: {', '.join(sorted(missing))}")
    return config


def source_epoch() -> int:
    value = os.environ.get("SOURCE_DATE_EPOCH", "")
    if value.isdigit():
        return int(value)
    try:
        result = subprocess.run(
            ["git", "show", "-s", "--format=%ct", "HEAD"],
            cwd=ROOT,
            check=True,
            capture_output=True,
            text=True,
        )
    except (OSError, subprocess.CalledProcessError):
        return 0
    return int(result.stdout.strip()) if result.stdout.strip().isdigit() else 0


def platform_items(config: dict[str, Any], requested: list[str]) -> list[dict[str, Any]]:
    known = {
        f"{item.get('os', 'linux')}/{item.get('arch', '')}": item
        for item in config["platforms"]
        if isinstance(item, dict)
    }
    selected = requested or sorted(SUPPORTED_PLATFORMS)
    unknown = set(selected) - set(known)
    if unknown:
        fail(f"unknown release platform(s): {', '.join(sorted(unknown))}")
    return [known[item] for item in selected]


def platform_value(item: dict[str, Any], key: str) -> str:
    value = item.get(key)
    if not isinstance(value, str) or not value:
        fail(f"release platform is missing {key}")
    return value


def artifact_name(config: dict[str, Any], kind: str, *, item: dict[str, Any]) -> str:
    artifacts = config.get("artifacts", {})
    template = artifacts.get(kind) if isinstance(artifacts, dict) else None
    if not isinstance(template, str) or not template:
        fail(f"release config is missing artifacts.{kind}")
    values = {
        "name": str(config.get("name", "helm")),
        "version": str(config["version"]),
        "package_release": int(config.get("package_release", 1)),
        "os": platform_value(item, "os") if "os" in item else "linux",
        "arch": platform_value(item, "arch"),
        "deb_arch": platform_value(item, "deb_arch"),
        "rpm_arch": platform_value(item, "rpm_arch"),
    }
    try:
        result = template.format(**values)
    except (KeyError, ValueError) as error:
        fail(f"invalid {kind} artifact template: {error}")
    if not result or Path(result).name != result:
        fail(f"{kind} artifact template must produce a plain filename")
    return result


def copy_tree(source: Path, destination: Path) -> None:
    if not source.is_dir():
        fail(f"required directory is missing: {source}")
    for child in source.rglob("*"):
        if child.is_symlink():
            fail(f"frontend tree contains unsupported symlink: {child}")
    if destination.exists():
        shutil.rmtree(destination)
    destination.mkdir(parents=True, exist_ok=True)
    for child in source.iterdir():
        target = destination / child.name
        if child.is_dir():
            shutil.copytree(child, target)
        else:
            shutil.copy2(child, target)


def build_frontend() -> None:
    web = ROOT / "web"
    npm_command = os.environ.get("NPM", "npm")
    run([npm_command, "ci", "--ignore-scripts"], cwd=web)
    run([npm_command, "run", "openapi:check"], cwd=web)
    run([npm_command, "run", "check"], cwd=web)
    run([npm_command, "test"], cwd=web)
    run([npm_command, "run", "build"], cwd=web)
    built = web / "dist"
    index = built / "index.html"
    if not index.is_file() or "<div id=\"app\">" not in index.read_text(encoding="utf-8"):
        fail("frontend build did not produce the production app shell")
    copy_tree(built, ROOT / "internal/webassets/dist")
    copy_tree(built, ROOT / "internal/frontend/dist")


@contextmanager
def production_frontend() -> Any:
    """Embed a built bundle while leaving checked-in fallback trees untouched."""
    targets = (ROOT / "internal/webassets/dist", ROOT / "internal/frontend/dist")
    with tempfile.TemporaryDirectory(prefix="helm-frontend-fallback-") as temporary:
        backup_root = Path(temporary)
        existed: dict[Path, bool] = {}
        for index, target in enumerate(targets):
            existed[target] = target.exists()
            if target.exists():
                shutil.copytree(target, backup_root / str(index), symlinks=True)
        try:
            build_frontend()
            yield
        finally:
            for index, target in enumerate(targets):
                if target.exists():
                    shutil.rmtree(target)
                if existed[target]:
                    shutil.copytree(backup_root / str(index), target, symlinks=True)


def assert_binary_arch(binary: Path, arch: str) -> None:
    try:
        header = binary.read_bytes()[:20]
    except OSError as error:
        fail(f"could not read built binary {binary}: {error}")
    if len(header) < 20 or header[:4] != b"\x7fELF" or header[4] != 2:
        fail(f"built binary is not a 64-bit ELF executable: {binary}")
    endian = "<" if header[5] == 1 else ">" if header[5] == 2 else ""
    if not endian:
        fail(f"built binary has invalid ELF byte order: {binary}")
    machine = struct.unpack_from(endian + "H", header, 18)[0]
    expected = {"amd64": 62, "arm64": 183}.get(arch)
    if expected is None or machine != expected:
        fail(f"built binary {binary} has ELF machine {machine}, expected {expected}")


def build_binaries(config: dict[str, Any], staging_root: Path, items: Iterable[dict[str, Any]]) -> None:
    build = config.get("build")
    if not isinstance(build, dict):
        fail("release config is missing build settings")
    source = build.get("binaries")
    if not isinstance(source, list) or len(source) != 1 or not isinstance(source[0], dict):
        fail("release config must describe exactly one server binary")
    if build.get("cgo_enabled", 0) != 0:
        fail("release binaries must use CGO_ENABLED=0")
    binary = source[0]
    source_path = str(binary.get("source", "./cmd/helm"))
    go_command = os.environ.get("GO", "go")
    if build.get("goos", "linux") != "linux":
        fail("release binaries must target linux")
    for item in items:
        arch = platform_value(item, "arch")
        stage = staging_root / f"linux-{arch}"
        if stage.exists():
            shutil.rmtree(stage)
        stage.mkdir(parents=True, exist_ok=True)
        output = stage / "usr/bin/helm"
        output.parent.mkdir(parents=True, exist_ok=True)
        env = os.environ.copy()
        env.update(
            {
                "CGO_ENABLED": "0",
                "GOOS": platform_value(item, "os"),
                "GOARCH": arch,
            }
        )
        run(
            [go_command, "build", "-trimpath", "-ldflags=-s -w", "-o", str(output), source_path],
            cwd=ROOT,
            env=env,
        )
        output.chmod(0o755)
        assert_binary_arch(output, arch)


def copy_file(source: Path, destination: Path, mode: int) -> None:
    if not source.is_file():
        fail(f"required packaging file is missing: {source}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)
    destination.chmod(mode)


def archive_stage(stage: Path, *, config: dict[str, Any]) -> None:
    for path in stage.rglob("*"):
        if path.is_symlink():
            fail(f"staging root contains unsupported symlink: {path}")
    copy_file(ROOT / "packaging/systemd/helm.service", stage / "usr/lib/systemd/system/helm.service", 0o644)
    copy_file(ROOT / "packaging/sysusers.d/helm.conf", stage / "usr/lib/sysusers.d/helm.conf", 0o644)
    copy_file(ROOT / "packaging/tmpfiles.d/helm.conf", stage / "usr/lib/tmpfiles.d/helm.conf", 0o644)
    copy_file(ROOT / "packaging/etc/helm.env", stage / "etc/helm/helm.env", 0o640)
    for directory, mode in (
        ("var/lib/helm", 0o750),
        ("var/lib/helm/codex-users", 0o700),
        ("usr/lib/helm", 0o755),
    ):
        path = stage / directory
        path.mkdir(parents=True, exist_ok=True)
        path.chmod(mode)


def write_archive(stage: Path, output: Path, *, top_level: str, epoch: int) -> None:
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open("wb") as stream:
        with gzip.GzipFile(fileobj=stream, mode="wb", compresslevel=9, mtime=epoch) as compressed:
            with tarfile.open(fileobj=compressed, mode="w") as tar:
                root_info = tarfile.TarInfo(top_level)
                root_info.type = tarfile.DIRTYPE
                root_info.mode = 0o755
                root_info.uid = root_info.gid = 0
                root_info.uname = root_info.gname = "root"
                root_info.mtime = epoch
                tar.addfile(root_info)
                paths = sorted(stage.rglob("*"), key=lambda path: path.relative_to(stage).as_posix())
                for path in paths:
                    info_path = Path(top_level) / path.relative_to(stage)
                    # add_tar_entry takes a root-relative arcname; use a
                    # temporary wrapper so the release root is included.
                    info = tar.gettarinfo(str(path), arcname=info_path.as_posix())
                    info.uid = 0
                    info.gid = 0
                    info.uname = "root"
                    info.gname = "root"
                    info.mtime = epoch
                    if path.is_file():
                        with path.open("rb") as file:
                            tar.addfile(info, file)
                    else:
                        tar.addfile(info)


def build_archives(config: dict[str, Any], output: Path, staging_root: Path, items: list[dict[str, Any]]) -> None:
    with production_frontend():
        build_binaries(config, staging_root, items)
    epoch = source_epoch()
    for item in items:
        arch = platform_value(item, "arch")
        stage = staging_root / f"linux-{arch}"
        archive_stage(stage, config=config)
        archive = output / artifact_name(config, "archive", item=item)
        write_archive(stage, archive, top_level=archive.name.removesuffix(".tar.gz"), epoch=epoch)
    (output / "release-version").write_text(str(config["version"]) + "\n", encoding="ascii")


def build_packages(
    config_path: Path,
    config: dict[str, Any],
    output: Path,
    staging_root: Path,
    items: list[dict[str, Any]],
    package_format: str,
) -> None:
    missing = [item for item in items if not (staging_root / f"linux-{platform_value(item, 'arch')}" / "usr/bin/helm").is_file()]
    if missing:
        with production_frontend():
            build_binaries(config, staging_root, missing)
    command = [
        sys.executable,
        str(ROOT / "ci/build-native-packages.py"),
        "--config",
        str(config_path),
        "--staging-root",
        str(staging_root),
        "--output",
        str(output),
        "--format",
        package_format,
    ]
    for item in items:
        command.extend(("--platform", f"linux/{platform_value(item, 'arch')}"))
    run(command, cwd=ROOT)
    (output / "release-version").write_text(str(config["version"]) + "\n", encoding="ascii")


def build_oci(config: dict[str, Any], output: Path, platforms: list[str], *, image: str, push: bool, load: bool) -> None:
    if not platforms:
        platforms = sorted(SUPPORTED_PLATFORMS)
    unknown = set(platforms) - SUPPORTED_PLATFORMS
    if unknown:
        fail(f"unsupported OCI platform(s): {', '.join(sorted(unknown))}")
    version = str(config["version"])
    tag = f"{image}:{version}"
    platform_label = "multiarch" if len(platforms) > 1 else platforms[0].split("/", 1)[1]
    oci_output = output / f"helm-{platform_label}-oci.tar"
    receipt_output = output / f"oci-build-{platform_label}.json"
    output.mkdir(parents=True, exist_ok=True)
    command = ["docker", "buildx", "build", "--platform", ",".join(platforms), "--tag", tag, "--file", str(ROOT / "Dockerfile")]
    if push:
        command.append("--push")
    elif load:
        if len(platforms) != 1:
            fail("--load requires one OCI platform; use --push or an OCI output for a matrix")
        command.append("--load")
    else:
        # A local OCI archive keeps a multi-platform build inspectable without
        # mutating a registry. Buildx accepts this output for one or many
        # platforms and the caller can hand it to the shared receipt tool.
        command.extend(("--output", f"type=oci,dest={oci_output}"))
    command.append(str(ROOT))
    run(command, cwd=ROOT)
    receipt = {
        "version": version,
        "image": tag,
        "platforms": platforms,
        "output": str(oci_output) if not push and not load else None,
    }
    receipt_output.write_text(json.dumps(receipt, indent=2) + "\n", encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("archives", "packages", "oci", "all"))
    parser.add_argument("--config", type=Path, default=ROOT / "packaging/release.json")
    parser.add_argument("--output", type=Path, default=ROOT / "dist/release")
    parser.add_argument("--staging-root", type=Path, default=ROOT / "dist/staging")
    parser.add_argument("--platform", action="append", dest="platforms", default=[])
    parser.add_argument("--format", choices=("all", "deb", "rpm"), default="all")
    parser.add_argument("--image", default=os.environ.get("OCI_IMAGE", "helm"))
    parser.add_argument("--push", action="store_true")
    parser.add_argument("--load", action="store_true")
    args = parser.parse_args()
    config = read_config(args.config)
    items = platform_items(config, args.platforms)
    args.output.mkdir(parents=True, exist_ok=True)
    args.staging_root.mkdir(parents=True, exist_ok=True)
    if args.command in {"archives", "all"}:
        build_archives(config, args.output, args.staging_root, items)
    if args.command in {"packages", "all"}:
        build_packages(args.config, config, args.output, args.staging_root, items, args.format)
    if args.command in {"oci", "all"}:
        build_oci(config, args.output, args.platforms, image=args.image, push=args.push, load=args.load)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except BrokenPipeError:
        raise SystemExit(1)
