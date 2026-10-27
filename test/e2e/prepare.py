#!/usr/bin/env python3
"""Build and verify immutable E2E executables after the owner embeds web assets.

Usage: prepare.py prepare --root REPO --output BUNDLE [--github]
       prepare.py verify  --root REPO --output BUNDLE [--github]

Both commands print JSON executable paths. Flags default to HELM_E2E_GOFLAGS;
CI should use --go-flags=-race. This tool never builds or restores frontend
assets and never imports an executable with unproven provenance.
HELM_E2E_GO_COMMAND may supply a quoted argv prefix (default: go); it is never
executed through a shell and must remain identical when verifying a bundle.
"""
import argparse
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import signal
import stat
import subprocess
import sys
import tempfile


SCHEMA = 1
GO_ENV = ("GOVERSION", "GOOS", "GOARCH", "CGO_ENABLED", "GOEXPERIMENT",
          "GOFLAGS", "GOTOOLCHAIN", "GOAMD64", "GOARM64", "CC", "CXX",
          "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS",
          "GOWORK", "GOMOD", "GOENV", "GOTOOLDIR", "GOFIPS140")
VALUE_FLAGS = {"-tags", "-ldflags", "-gcflags", "-asmflags", "-buildvcs", "-mod"}
BOOL_FLAGS = {"-race", "-msan", "-asan", "-a", "-v", "-x", "-work"}


class InvalidBundle(Exception):
    pass


class Cancelled(BaseException):
    def __init__(self, signum):
        self.signum = signum


def cancel(signum, _frame):
    # Repeated cancellation must not interrupt process cleanup or stage removal.
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    signal.signal(signal.SIGINT, signal.SIG_IGN)
    raise Cancelled(signum)


def stop_command(process):
    def send(signum):
        try:
            os.killpg(process.pid, signum)
        except ProcessLookupError:
            pass
    send(signal.SIGTERM)
    try:
        process.communicate(timeout=2)
    except subprocess.TimeoutExpired:
        pass
    # The leader may have exited while a descendant kept running or held a pipe.
    send(signal.SIGKILL)
    process.communicate(timeout=2)


def run(root, args, env=None):
    process = subprocess.Popen(args, cwd=root, env=env, stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE, start_new_session=True)
    try:
        stdout, stderr = process.communicate()
    except BaseException:
        stop_command(process)
        raise
    if process.returncode:
        raise InvalidBundle(f"command failed: {shlex.join(args)}\n"
                            + stderr.decode(errors="replace"))
    return stdout


def go_command():
    prefix = shlex.split(os.environ.get("HELM_E2E_GO_COMMAND", "go"))
    if not prefix or not prefix[0]:
        raise InvalidBundle("HELM_E2E_GO_COMMAND must contain a nonempty command")
    return prefix


def run_go(root, args, env=None):
    return run(root, [*go_command(), *args], env=env)


def digest_file(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def add_digest(digest, value):
    # Length prefixes keep arbitrary filenames and binary contents unambiguous.
    digest.update(len(value).to_bytes(8, "big"))
    digest.update(value)


def fingerprint(root, paths, allow_symlinks=False):
    digest = hashlib.sha256()
    for relative in sorted(set(paths)):
        path = root / relative
        add_digest(digest, os.fsencode(relative))
        try:
            mode = path.lstat().st_mode
        except FileNotFoundError:
            add_digest(digest, b"missing")
            continue
        add_digest(digest, str(stat.S_IFMT(mode) | stat.S_IMODE(mode)).encode())
        if stat.S_ISLNK(mode) and allow_symlinks:
            add_digest(digest, os.fsencode(os.readlink(path)))
        elif stat.S_ISREG(mode):
            add_digest(digest, digest_file(path).encode())
        else:
            raise InvalidBundle(f"unsupported input type: {path}")
    return digest.hexdigest()


def frontend(root):
    assets = root / "internal/webassets/dist"
    if not assets.is_dir() or assets.is_symlink():
        raise InvalidBundle("embedded frontend directory is missing or a symlink")
    paths = []
    for path in assets.rglob("*"):
        if path.is_symlink():
            raise InvalidBundle(f"frontend symlink is not supported: {path}")
        if path.is_file():
            paths.append(str(path.relative_to(assets)))
    if not paths or not (assets / "index.html").is_file():
        raise InvalidBundle("embedded frontend requires index.html and built assets")
    return fingerprint(assets, paths)


def source(root, excluded):
    head = run(root, ["git", "rev-parse", "HEAD"]).decode().strip()
    names = run(root, ["git", "ls-files", "-z", "--cached", "--others",
                       "--exclude-standard"]).split(b"\0")
    paths = []
    for name in names:
        if not name:
            continue
        relative = os.fsdecode(name)
        path = root / relative
        if path.is_relative_to(root / "internal/webassets/dist"):
            continue
        if any(path == excluded_path or path.is_relative_to(excluded_path)
               for excluded_path in excluded):
            continue
        paths.append(relative)
    return {"head": head, "fingerprint": fingerprint(root, paths, allow_symlinks=True)}


def parse_flags(value):
    flags = shlex.split(value)
    pending = False
    for flag in flags:
        if pending:
            pending = False
            continue
        key, separator, _ = flag.partition("=")
        if key in VALUE_FLAGS:
            pending = not separator
        elif key not in BOOL_FLAGS:
            raise InvalidBundle(f"unsupported E2E Go flag: {flag}; output, targets and -p are owned")
    if pending:
        raise InvalidBundle("E2E Go flag requires a value")
    return flags


def race_expected(flags, goflags):
    result = False
    for flag in shlex.split(goflags) + flags:
        if flag == "-race":
            result = True
        elif flag.startswith("-race="):
            value = flag.partition("=")[2]
            if value not in ("true", "false"):
                raise InvalidBundle("-race must be true or false")
            result = value == "true"
    return result


def configuration(root, flags):
    jobs = os.environ.get("HELM_CI_BUILD_JOBS", "6")
    if not jobs.isdecimal() or not 1 <= int(jobs) <= 6:
        raise InvalidBundle("HELM_CI_BUILD_JOBS must be 1..6")
    toolchain = json.loads(run_go(root, ["env", "-json", *GO_ENV]))
    if set(toolchain) != set(GO_ENV):
        raise InvalidBundle("Go environment is incomplete")
    return {"toolchain": toolchain, "go_command": go_command(), "flags": flags, "jobs": int(jobs),
            "race": race_expected(flags, toolchain["GOFLAGS"]), "trimpath": True,
            "run_id": os.environ.get("GITHUB_RUN_ID", "local"),
            "run_attempt": os.environ.get("GITHUB_RUN_ATTEMPT", "local")}


def build_info(root, path):
    output = run_go(root, ["version", "-m", str(path)]).decode()
    lines = output.splitlines()
    if not lines or ": " not in lines[0]:
        raise InvalidBundle(f"missing Go build information: {path}")
    info = {"version": lines[0].split(": ", 1)[1], "settings": {}}
    for line in lines[1:]:
        fields = line.strip().split("\t", 1)
        if len(fields) != 2:
            continue
        kind, value = fields
        if kind == "build":
            key, equal, setting = value.partition("=")
            if equal:
                info["settings"][key] = setting
        elif kind == "path":
            info["path"] = value
    return info


def validate_build(info, config):
    settings = info["settings"]
    expected = config["toolchain"]
    if info["version"] != expected["GOVERSION"]:
        raise InvalidBundle("binary Go version does not match the selected toolchain")
    if settings.get("-trimpath") != "true":
        raise InvalidBundle("binary is missing the requested -trimpath setting")
    if (settings.get("-race", "false") == "true") != config["race"]:
        raise InvalidBundle("binary race instrumentation does not match requested flags")
    for key in ("GOOS", "GOARCH", "CGO_ENABLED"):
        if settings.get(key) != expected[key]:
            raise InvalidBundle(f"binary {key} does not match the selected toolchain")


def binary_record(root, path, config):
    if path.is_symlink() or not path.is_file() or not os.access(path, os.X_OK):
        raise InvalidBundle(f"binary is missing, not executable, or a symlink: {path}")
    info = build_info(root, path)
    validate_build(info, config)
    return {"sha256": digest_file(path), "build_info": info}


@contextlib.contextmanager
def bundle_lock(output):
    output.parent.mkdir(parents=True, exist_ok=True)
    lock_path = output.with_name(output.name + ".lock")
    with lock_path.open("a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise InvalidBundle("prepared bundle is in use by another coordinator") from error
        yield lock_path


def paths_for(output, github):
    paths = {"helm": str(output / "helm"), "metadata": str(output / "metadata.json")}
    if github:
        paths["github"] = str(output / "helm-githubbeta")
    return paths


def prepare(root, output, github, flags, lock_path):
    if output.exists():
        raise InvalidBundle("bundle already exists; verify it or prepare a new output directory")
    stage = Path(tempfile.mkdtemp(prefix=f".{output.name}.prepare-", dir=output.parent))
    try:
        excluded = [output, stage, lock_path]
        config = configuration(root, flags)
        snapshot, assets = source(root, excluded), frontend(root)
        binaries = {}
        targets = {"helm": "./cmd/helm"}
        if github:
            targets["helm-githubbeta"] = "./test/githubbeta"
        # Build limits apply to compiler children only, never runtime servers.
        env = {**os.environ, "GOMAXPROCS": str(config["jobs"])}
        for name, target in targets.items():
            run_go(root, ["build", *flags, "-p=" + str(config["jobs"]),
                          "-trimpath", "-o", str(stage / name), target], env=env)
            binaries[name] = binary_record(root, stage / name, config)
        if source(root, excluded) != snapshot or frontend(root) != assets:
            raise InvalidBundle("source or frontend changed while compiling")
        if configuration(root, flags) != config:
            raise InvalidBundle("toolchain changed while compiling")
        metadata = {"schema": SCHEMA, "source": snapshot, "frontend_sha256": assets,
                    "configuration": config, "binaries": binaries}
        (stage / "metadata.json").write_text(json.dumps(metadata, indent=2, sort_keys=True) + "\n")
        stage.rename(output)
    finally:
        if stage.exists():
            shutil.rmtree(stage)
    return paths_for(output, github)


def verify(root, output, github, flags, lock_path):
    if output.is_symlink():
        raise InvalidBundle("prepared bundle may not be a symlink")
    metadata_path = output / "metadata.json"
    if metadata_path.is_symlink():
        raise InvalidBundle("prepared metadata may not be a symlink")
    try:
        metadata = json.loads(metadata_path.read_text())
    except (OSError, ValueError) as error:
        raise InvalidBundle("missing or invalid metadata; use prepare.py prepare first") from error
    config = configuration(root, flags)
    expected = {"schema": SCHEMA, "source": source(root, [output, lock_path]),
                "frontend_sha256": frontend(root), "configuration": config}
    for key, value in expected.items():
        if metadata.get(key) != value:
            raise InvalidBundle(f"prepared bundle {key} does not match current inputs")
    records = metadata.get("binaries")
    if not isinstance(records, dict) or "helm" not in records:
        raise InvalidBundle("bundle is missing Helm binary metadata")
    if set(records) - {"helm", "helm-githubbeta"}:
        raise InvalidBundle("bundle contains unexpected binary metadata")
    if github and "helm-githubbeta" not in records:
        raise InvalidBundle("bundle is missing the required GitHub harness")
    for name, record in records.items():
        if binary_record(root, output / name, config) != record:
            raise InvalidBundle(f"prepared binary hash or build settings changed: {name}")
    return paths_for(output, github)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("prepare", "verify"))
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--github", action="store_true")
    parser.add_argument("--go-flags", default=os.environ.get("HELM_E2E_GOFLAGS", ""))
    args = parser.parse_args()
    signal.signal(signal.SIGTERM, cancel)
    signal.signal(signal.SIGINT, cancel)
    try:
        root, output = args.root.resolve(), args.output.absolute()
        if root == output or root.is_relative_to(output):
            raise InvalidBundle("bundle directory cannot contain the source checkout")
        flags = parse_flags(args.go_flags)
        with bundle_lock(output) as lock_path:
            operation = prepare if args.action == "prepare" else verify
            result = operation(root, output, args.github, flags, lock_path)
        print(json.dumps(result, sort_keys=True))
    except Cancelled as error:
        print(f"E2E preparation canceled by signal {error.signum}", file=sys.stderr)
        return 128 + error.signum
    except (InvalidBundle, OSError, ValueError, TypeError) as error:
        print(f"E2E preparation: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
