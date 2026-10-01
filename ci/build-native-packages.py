#!/usr/bin/env python3
"""Build Debian and RPM packages from the shared release metadata.

The release tool owns Go/frontend compilation and archive/image assembly. This
small adapter owns only native package layout, making the package contract
reviewable and usable by local release builds as well as CI. It accepts a
staging root per architecture containing ``usr/bin/helm`` and adds the
reviewed systemd/sysusers/tmpfiles files from ``packaging/``.
"""

from __future__ import annotations

import argparse
import json
import os
import platform as host_platform
import re
import shutil
import struct
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any


ROOT = Path(__file__).resolve().parents[1]
SEMVER = re.compile(r"^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$")


def fail(message: str) -> "NoReturn":
    raise SystemExit(f"build-native-packages: {message}")


def run(command: list[str], *, cwd: Path | None = None, env: dict[str, str] | None = None) -> None:
    try:
        subprocess.run(command, cwd=cwd, env=env, check=True)
    except FileNotFoundError:
        fail(f"required command is missing: {command[0]}")
    except subprocess.CalledProcessError as error:
        fail(f"command failed with exit {error.returncode}: {' '.join(command)}")


def metadata(config_path: Path) -> dict[str, Any]:
    try:
        config = json.loads(config_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        fail(f"could not read JSON config {config_path}: {error}")
    if not isinstance(config, dict):
        fail("release config must be a JSON object")
    version = config.get("version")
    if not isinstance(version, str) or not SEMVER.fullmatch(version):
        fail("release config version must be semantic x.y.z text")
    platforms = config.get("platforms")
    if not isinstance(platforms, list) or not platforms:
        fail("release config must list at least one platform")
    seen: set[str] = set()
    for platform in platforms:
        if not isinstance(platform, dict):
            fail("each platform must be an object")
        os_name = platform.get("os", "linux")
        arch = platform.get("arch")
        if os_name != "linux" or arch not in {"amd64", "arm64"}:
            fail("native packages support only linux/amd64 and linux/arm64")
        identity = f"{os_name}/{arch}"
        if identity in seen:
            fail(f"duplicate platform in release config: {identity}")
        seen.add(identity)
    return config


def source_epoch() -> str:
    value = os.environ.get("SOURCE_DATE_EPOCH", "")
    if value.isdigit():
        return value
    try:
        result = subprocess.run(
            ["git", "show", "-s", "--format=%ct", "HEAD"],
            cwd=ROOT,
            check=True,
            capture_output=True,
            text=True,
        )
    except (OSError, subprocess.CalledProcessError):
        return "0"
    value = result.stdout.strip()
    return value if value.isdigit() else "0"


def platform_value(platform: dict[str, Any], key: str, fallback: str = "") -> str:
    value = platform.get(key, fallback)
    if not isinstance(value, str) or not value:
        fail(f"platform is missing {key}")
    return value


def copy_file(source: Path, destination: Path, mode: int | None = None) -> None:
    if not source.is_file():
        fail(f"required packaging file is missing: {source}")
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)
    if mode is not None:
        destination.chmod(mode)
    else:
        destination.chmod(source.stat().st_mode & 0o777)


def assert_binary_arch(binary: Path, arch: str) -> None:
    try:
        header = binary.read_bytes()[:20]
    except OSError as error:
        fail(f"could not read staged binary {binary}: {error}")
    if len(header) < 20 or header[:4] != b"\x7fELF" or header[4] != 2:
        fail(f"staged binary is not a 64-bit ELF executable: {binary}")
    byte_order = "<" if header[5] == 1 else ">" if header[5] == 2 else ""
    if not byte_order:
        fail(f"staged binary has an invalid ELF byte order: {binary}")
    machine = struct.unpack_from(byte_order + "H", header, 18)[0]
    expected = {"amd64": 62, "arm64": 183}.get(arch)
    if expected is None:
        fail(f"unsupported Linux package architecture: {arch}")
    if machine != expected:
        fail(f"staged binary {binary} has ELF machine {machine}, expected {expected} for {arch}")


def assert_safe_stage(stage: Path) -> None:
    for path in stage.rglob("*"):
        if path.is_symlink():
            fail(f"staging root contains unsupported symlink: {path}")


def install_layout(
    stage: Path,
    *,
    config: dict[str, Any],
    companion_dir: Path | None,
    companions: set[str],
    arch: str,
) -> None:
    binary = stage / "usr/bin/helm"
    if not binary.is_file() or not os.access(binary, os.X_OK):
        fail(f"staging root {stage} must contain executable usr/bin/helm")
    assert_binary_arch(binary, arch)
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

    # Native companions are opt-in. A package containing only helm remains
    # startable when these paths do not exist.
    if companions and companion_dir is None:
        fail("--companion requires --companion-dir")
    if companion_dir is not None:
        for name in companions:
            source = companion_dir / arch / name
            if source.is_symlink() or not source.is_file():
                fail(f"requested companion {name} is missing for linux/{arch}: {source}")
            destination = stage / "usr/lib/helm" / name
            copy_file(source, destination, 0o755)


def artifact_name(config: dict[str, Any], kind: str, *, platform: dict[str, Any]) -> str:
    artifacts = config.get("artifacts", {})
    template = artifacts.get(kind) if isinstance(artifacts, dict) else None
    if not isinstance(template, str) or not template:
        defaults = {
            "deb": "{name}_{version}_{deb_arch}.deb",
            "rpm": "{name}-{version}-{package_release}.{rpm_arch}.rpm",
        }
        template = defaults[kind]
    values = {
        "name": str(config.get("name", "helm")),
        "version": str(config["version"]),
        "package_release": int(config.get("package_release", 1)),
        "arch": platform_value(platform, "arch"),
        "deb_arch": platform_value(platform, "deb_arch"),
        "rpm_arch": platform_value(platform, "rpm_arch"),
    }
    try:
        result = template.format(**values)
    except (KeyError, ValueError) as error:
        fail(f"invalid {kind} artifact template: {error}")
    if not result or Path(result).name != result:
        fail(f"{kind} artifact template must produce a plain filename")
    return result


def write_deb_control(control_dir: Path, *, config: dict[str, Any], platform: dict[str, Any]) -> None:
    name = str(config.get("name", "helm"))
    version = str(config["version"])
    release = config.get("package_release", 1)
    if not isinstance(release, int) or release < 1:
        fail("package_release must be a positive integer")
    description = str(config.get("description", "Helm project board"))
    arch = platform_value(platform, "deb_arch")
    control_dir.mkdir(parents=True, exist_ok=True)
    (control_dir / "control").write_text(
        "\n".join(
            [
                "Package: " + name,
                f"Version: {version}-{release}",
                "Section: admin",
                "Priority: optional",
                "Architecture: " + arch,
                "Maintainer: " + str(config.get("maintainer", "KanterLabs")),
                "Description: " + description,
                " A self-hosted project board for humans and software agents.",
                "",
            ]
        ),
        encoding="utf-8",
    )
    (control_dir / "conffiles").write_text("/etc/helm/helm.env\n", encoding="utf-8")
    (control_dir / "postinst").write_text(
        """#!/bin/sh
set -eu
if command -v systemd-sysusers >/dev/null 2>&1; then
    systemd-sysusers /usr/lib/sysusers.d/helm.conf || true
fi
if command -v systemd-tmpfiles >/dev/null 2>&1; then
    systemd-tmpfiles --create /usr/lib/tmpfiles.d/helm.conf || true
fi
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload >/dev/null 2>&1 || true
fi
exit 0
""",
        encoding="utf-8",
    )
    (control_dir / "postrm").write_text(
        """#!/bin/sh
set -eu
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload >/dev/null 2>&1 || true
fi
exit 0
""",
        encoding="utf-8",
    )
    (control_dir / "postinst").chmod(0o755)
    (control_dir / "postrm").chmod(0o755)


def build_deb(stage: Path, output: Path, *, config: dict[str, Any], platform: dict[str, Any], epoch: str) -> None:
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="helm-deb-") as temporary:
        package_root = Path(temporary) / "root"
        shutil.copytree(stage, package_root, symlinks=True)
        write_deb_control(package_root / "DEBIAN", config=config, platform=platform)
        env = os.environ.copy()
        env["SOURCE_DATE_EPOCH"] = epoch
        run(["dpkg-deb", "--build", "--root-owner-group", str(package_root), str(output)], env=env)


def rpm_files(stage: Path) -> list[str]:
    files: list[str] = []
    for path in sorted(stage.rglob("*")):
        relative = "/" + path.relative_to(stage).as_posix()
        if path.is_dir():
            # Explicit directory entries keep state directories in the native
            # package contract; files below them remain package-owned payload.
            files.append(f"%dir {relative}")
        elif path.is_file():
            if relative == "/etc/helm/helm.env":
                files.append(f"%config(noreplace) {relative}")
            else:
                files.append(relative)
    return files


def build_rpm(stage: Path, output: Path, *, config: dict[str, Any], platform: dict[str, Any], epoch: str) -> None:
    output.parent.mkdir(parents=True, exist_ok=True)
    name = str(config.get("name", "helm"))
    version = str(config["version"])
    release = config.get("package_release", 1)
    if not isinstance(release, int) or release < 1:
        fail("package_release must be a positive integer")
    rpm_arch = platform_value(platform, "rpm_arch")
    build_arch = {"amd64": "x86_64", "arm64": "aarch64"}.get(
        host_platform.machine(), host_platform.machine()
    )
    if not build_arch:
        fail("could not determine the RPM build architecture")
    with tempfile.TemporaryDirectory(prefix="helm-rpm-") as temporary:
        top = Path(temporary)
        for directory in ("BUILD", "BUILDROOT", "RPMS", "SOURCES", "SPECS", "SRPMS"):
            (top / directory).mkdir()
        (top / "rpmdb").mkdir()
        stage_copy = top / "SOURCES" / "stage"
        shutil.copytree(stage, stage_copy, symlinks=True)
        files = rpm_files(stage)
        files_text = "\n".join(files)
        spec = top / "SPECS" / f"{name}.spec"
        spec.write_text(
            f"""Name:           {name}
Version:        {version}
Release:        {release}
Summary:        {config.get('description', 'Helm project board')}
License:        {config.get('license', 'MIT')}
URL:            {config.get('homepage', 'https://github.com/KanterLabs/helm')}

%description
{config.get('description', 'Helm project board')}

%prep

%build

%install
rm -rf %{{buildroot}}
mkdir -p %{{buildroot}}
cp -a %{{_sourcedir}}/stage/. %{{buildroot}}/

%post
if command -v systemd-sysusers >/dev/null 2>&1; then
    systemd-sysusers /usr/lib/sysusers.d/helm.conf || :
fi
if command -v systemd-tmpfiles >/dev/null 2>&1; then
    systemd-tmpfiles --create /usr/lib/tmpfiles.d/helm.conf || :
fi
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload >/dev/null 2>&1 || :
fi

%postun
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload >/dev/null 2>&1 || :
fi

%files
%defattr(-,root,root,-)
{files_text}

%changelog
* Thu Oct 01 2026 KanterLabs <opensource@kanterlabs.dev> - {version}-{release}
- Reproducible native release package.
""",
            encoding="utf-8",
        )
        env = os.environ.copy()
        env["SOURCE_DATE_EPOCH"] = epoch
        env["TZ"] = "UTC"
        run(
            [
                "rpmbuild",
                "--define",
                f"_topdir {top}",
                "--define",
                f"_build_id_links none",
                "--define",
                "__strip /bin/true",
                "--define",
                "__objdump /bin/true",
                "--target",
                f"{rpm_arch}-linux",
                "--define",
                f"_arch {rpm_arch}",
                "--define",
                f"_build_arch {build_arch}",
                "--dbpath",
                str(top / "rpmdb"),
                "-bb",
                str(spec),
            ],
            env=env,
        )
        candidates = sorted((top / "RPMS").rglob("*.rpm"))
        if len(candidates) != 1:
            fail(f"rpmbuild produced {len(candidates)} packages for {rpm_arch}")
        shutil.copyfile(candidates[0], output)


def stage_for_platform(staging_root: Path, arch: str) -> Path:
    candidates = [staging_root / f"linux-{arch}", staging_root / arch]
    for candidate in candidates:
        if candidate.is_dir():
            return candidate
    fail(f"no staging directory for linux/{arch}; tried {', '.join(map(str, candidates))}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=ROOT / "packaging/release.json")
    parser.add_argument("--staging-root", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument(
        "--companion-dir",
        type=Path,
        help="optional directory containing <arch>/codex and/or <arch>/helm-beta-switchd",
    )
    parser.add_argument(
        "--companion",
        action="append",
        choices=("codex", "helm-beta-switchd"),
        default=[],
        help="explicitly include this optional companion; repeat for multiple companions",
    )
    parser.add_argument(
        "--format",
        choices=("all", "deb", "rpm"),
        default="all",
        help="native package format to build (default: all)",
    )
    parser.add_argument(
        "--platform",
        action="append",
        dest="platforms",
        help="build only this platform (linux/amd64 or linux/arm64); repeat for a matrix",
    )
    args = parser.parse_args()
    config = metadata(args.config)
    epoch = source_epoch()
    args.output.mkdir(parents=True, exist_ok=True)
    requested = set(args.platforms or [])
    known = {
        f"{item.get('os', 'linux')}/{item.get('arch', '')}"
        for item in config["platforms"]
        if isinstance(item, dict)
    }
    unknown = requested - known
    if unknown:
        fail(f"unknown platform(s): {', '.join(sorted(unknown))}")
    selected = [
        item
        for item in config["platforms"]
        if not requested or f"{item.get('os', 'linux')}/{item.get('arch', '')}" in requested
    ]
    if not selected:
        fail("no platforms selected")
    for platform in selected:
        if not isinstance(platform, dict):
            fail("each platform must be an object")
        arch = platform_value(platform, "arch")
        stage = stage_for_platform(args.staging_root, arch)
        assert_safe_stage(stage)
        # Copy each source staging tree before adding package files so a
        # caller can reuse its archive staging directory unchanged.
        with tempfile.TemporaryDirectory(prefix=f"helm-package-{arch}-") as temporary:
            package_stage = Path(temporary) / f"linux-{arch}"
            shutil.copytree(stage, package_stage, symlinks=True)
            install_layout(
                package_stage,
                config=config,
                companion_dir=args.companion_dir,
                companions=set(args.companion),
                arch=arch,
            )
            if args.format in {"all", "deb"}:
                build_deb(
                    package_stage,
                    args.output / artifact_name(config, "deb", platform=platform),
                    config=config,
                    platform=platform,
                    epoch=epoch,
                )
            if args.format in {"all", "rpm"}:
                build_rpm(
                    package_stage,
                    args.output / artifact_name(config, "rpm", platform=platform),
                    config=config,
                    platform=platform,
                    epoch=epoch,
                )
    print(f"built native packages in {args.output}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except BrokenPipeError:
        raise SystemExit(1)
