#!/usr/bin/env python3
"""Calculate and apply explicit SDK release version bumps."""

from __future__ import annotations

import argparse
import json
import re
from pathlib import Path


STABLE_SEMVER = re.compile(r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$")
VALID_LEVELS = {"patch", "minor", "major"}


def next_version(current: str, level: str) -> str:
    match = STABLE_SEMVER.fullmatch(current)
    if match is None:
        raise ValueError(f"expected a stable semantic version, got {current!r}")
    if level not in VALID_LEVELS:
        raise ValueError(
            f"unsupported bump level {level!r}; expected patch, minor, or major"
        )

    major, minor, patch = (int(part) for part in match.groups())
    if level == "patch":
        patch += 1
    elif level == "minor":
        minor += 1
        patch = 0
    else:
        major += 1
        minor = 0
        patch = 0
    return f"{major}.{minor}.{patch}"


def _write_json(path: Path, document: dict) -> None:
    path.write_text(json.dumps(document, indent=2, ensure_ascii=False) + "\n")


def _update_npm(
    root: Path, manifest: str, package_name: str, version: str
) -> list[Path]:
    manifest_path = root / manifest
    package = json.loads(manifest_path.read_text())
    if package.get("name") != package_name:
        raise ValueError(
            f"{manifest} contains package {package.get('name')!r}, expected {package_name!r}"
        )
    package["version"] = version
    _write_json(manifest_path, package)
    changed = [Path(manifest)]

    lock_path = root / "package-lock.json"
    if lock_path.exists():
        lock = json.loads(lock_path.read_text())
        lock["version"] = version
        root_package = lock.get("packages", {}).get("")
        if isinstance(root_package, dict):
            root_package["version"] = version
        _write_json(lock_path, lock)
        changed.append(Path("package-lock.json"))
    return changed


def _replace_once(text: str, pattern: str, replacement: str, label: str) -> str:
    updated, count = re.subn(pattern, replacement, text, count=1, flags=re.MULTILINE)
    if count != 1:
        raise ValueError(f"could not find exactly one {label}")
    return updated


def _update_pypi(root: Path, manifest: str, version: str) -> list[Path]:
    path = root / manifest
    updated = _replace_once(
        path.read_text(),
        r'^(__version__\s*=\s*)["\'][^"\']+["\']',
        rf'\g<1>"{version}"',
        f"__version__ assignment in {manifest}",
    )
    path.write_text(updated)
    return [Path(manifest)]


def _update_crates(
    root: Path, manifest: str, package_name: str, version: str
) -> list[Path]:
    manifest_path = root / manifest
    manifest_text = manifest_path.read_text()
    package_section = re.search(
        r"(?ms)^\[package\]\s*$.*?(?=^\[|\Z)", manifest_text
    )
    if package_section is None:
        raise ValueError(f"could not find [package] section in {manifest}")
    updated_section = _replace_once(
        package_section.group(0),
        r'^(version\s*=\s*)"[^"]+"',
        rf'\g<1>"{version}"',
        f"package version in {manifest}",
    )
    updated_manifest_text = (
        manifest_text[: package_section.start()]
        + updated_section
        + manifest_text[package_section.end() :]
    )
    changed = [Path(manifest)]

    lock_path = root / "Cargo.lock"
    updated_lock_text: str | None = None
    if lock_path.exists():
        lock_text = lock_path.read_text()
        package_blocks = list(
            re.finditer(r"(?ms)^\[\[package\]\]\s*$.*?(?=^\[\[package\]\]|\Z)", lock_text)
        )
        matching_blocks = [
            block
            for block in package_blocks
            if re.search(rf'^name\s*=\s*"{re.escape(package_name)}"\s*$', block.group(0), re.MULTILINE)
        ]
        if len(matching_blocks) != 1:
            raise ValueError(
                f"expected one {package_name!r} package entry in Cargo.lock, found {len(matching_blocks)}"
            )
        block = matching_blocks[0]
        updated_block = _replace_once(
            block.group(0),
            r'^(version\s*=\s*)"[^"]+"',
            rf'\g<1>"{version}"',
            f"{package_name} version in Cargo.lock",
        )
        updated_lock_text = (
            lock_text[: block.start()] + updated_block + lock_text[block.end() :]
        )
        changed.append(Path("Cargo.lock"))

    manifest_path.write_text(updated_manifest_text)
    if updated_lock_text is not None:
        lock_path.write_text(updated_lock_text)
    return changed


def update_version(
    kind: str,
    directory: str | Path,
    manifest: str,
    package_name: str,
    version: str,
) -> list[Path]:
    if STABLE_SEMVER.fullmatch(version) is None:
        raise ValueError(f"expected a stable semantic version, got {version!r}")

    root = Path(directory)
    if kind == "npm":
        return _update_npm(root, manifest, package_name, version)
    if kind == "pypi":
        return _update_pypi(root, manifest, version)
    if kind == "crates":
        return _update_crates(root, manifest, package_name, version)
    if kind in {"go", "packagist"}:
        return []
    raise ValueError(f"unsupported registry kind {kind!r}")


def main() -> int:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)

    next_parser = subparsers.add_parser("next")
    next_parser.add_argument("current")
    next_parser.add_argument("level", choices=sorted(VALID_LEVELS))

    update_parser = subparsers.add_parser("update")
    update_parser.add_argument("kind")
    update_parser.add_argument("directory")
    update_parser.add_argument("manifest")
    update_parser.add_argument("package_name")
    update_parser.add_argument("version")

    arguments = parser.parse_args()
    if arguments.command == "next":
        print(next_version(arguments.current, arguments.level))
    else:
        changed = update_version(
            arguments.kind,
            arguments.directory,
            arguments.manifest,
            arguments.package_name,
            arguments.version,
        )
        for path in changed:
            print(path)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
